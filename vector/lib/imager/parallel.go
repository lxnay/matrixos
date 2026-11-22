package imager

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/worker"
)

// luksMapperSeq generates unique per-worker suffixes for host-side LUKS
// device-mapper names during parallel encrypted image builds.
var luksMapperSeq atomic.Uint64

// ParallelImageOptions configures the parallel image creation engine.
type ParallelImageOptions struct {
	// Refs is the ordered list of ostree refs to image.
	Refs []string
	// Parallelism is the maximum number of concurrent image builds.
	Parallelism int

	// Config is the application configuration.
	Config config.IConfig

	// NewStdoutWriter creates a labeled stdout writer for a worker.
	NewStdoutWriter func(label string) io.Writer
	// NewStderrWriter creates a labeled stderr writer for a worker.
	NewStderrWriter func(label string) io.Writer
	// PushCleanup registers a function to be called during shutdown.
	PushCleanup func(fn func())

	// ShortRef returns a short display name for a branch ref.
	ShortRef func(ref string) string
	// OnImageDone is called when an image build completes
	// successfully.  It receives the ref name.  Serialised
	// internally — the callback does not need its own locking.
	OnImageDone func(ref string) error

	// SetupBuild is called inside the image lock before Build.  The
	// pushCleanup callback can be used to register cleanup functions
	// (e.g. GPG shutdown).  Use this for GPG initialization, ostree
	// pulling, and other per-ref setup that must happen under the
	// lock.
	SetupBuild func(pushCleanup func(func()), ot ostree.IOstree, im IImager) error

	// setupMu serialises the SetupBuild phase (GPG init, ostree
	// pull, etc.) which is not safe for concurrent execution.
	setupMu sync.Mutex
	// onImageDoneMu serialises the OnImageDone callback.
	onImageDoneMu sync.Mutex
}

// ParallelImage builds images concurrently using a simple
// semaphore-based worker pool.  Unlike seed parallelism, image builds
// have no inter-dependencies so all refs are immediately eligible.
func ParallelImage(ctx context.Context, opts *ParallelImageOptions) error {
	if opts.Parallelism < 1 {
		return fmt.Errorf("parallelism must be >= 1, got %d", opts.Parallelism)
	}

	poolStderr := opts.NewStderrWriter("images")

	return worker.RunPool(ctx, worker.PoolOptions[string]{
		Items:       opts.Refs,
		Parallelism: opts.Parallelism,
		Run: func(poolCtx context.Context, ref string) error {
			if err := imageWorker(poolCtx, opts, ref); err != nil {
				return fmt.Errorf("ref %s image failed: %w", ref, err)
			}
			return nil
		},
		OnError: func(err error) {
			fmt.Fprintf(poolStderr, "%v\n", err)
		},
		OnSkip: func(ref string) {
			fmt.Fprintf(poolStderr, "Skipping ref %s due to earlier failure.\n", ref)
		},
	})
}

// imageWorker processes a single image build.
func imageWorker(ctx context.Context, opts *ParallelImageOptions, ref string) error {
	// Create per-worker stdout writer.
	workerStdout := opts.NewStdoutWriter(
		fmt.Sprintf("images:%s", ref),
	)
	workerPrint := func(format string, args ...any) {
		fmt.Fprintf(workerStdout, format, args...)
	}

	imageStart := time.Now()
	workerPrint(
		"[%s] Image started at %s\n",
		ref, imageStart.Format(time.RFC3339),
	)
	defer func() {
		imageEnd := time.Now()
		workerPrint(
			"Image finished at %s (elapsed: %s)\n",
			imageEnd.Format(time.RFC3339),
			imageEnd.Sub(imageStart).Round(time.Second),
		)
	}()

	workerPrint("Working on ref %s\n", ref)

	if err := ctx.Err(); err != nil {
		return err
	}

	// Set up image-specific styled writers.
	shortRef := ref
	if opts.ShortRef != nil {
		shortRef = opts.ShortRef(ref)
	}
	imgStdout := opts.NewStdoutWriter(
		fmt.Sprintf("image:%s", shortRef),
	)
	imgStderr := opts.NewStderrWriter(
		fmt.Sprintf("image:%s", shortRef),
	)
	flushWriters := func() {
		worker.FlushWriters(imgStdout, imgStderr)
	}
	defer flushWriters()

	// Clone the config so each worker has an isolated copy
	// (Build mutates Ostree.Sysroot via AddOverlay).
	workerCfg := opts.Config.Clone()

	// Give each parallel worker a unique host-side LUKS device-mapper name so
	// concurrent encrypted image builds do not collide on /dev/mapper/<name>.
	// Guest boot arguments use rd.luks.uuid=<UUID>, not this mapper name.
	if baseName, err := workerCfg.GetItem("Imager.EncryptedRootFsName"); err == nil && baseName != "" {
		uniqueName := fmt.Sprintf("%s-%d-%d", baseName, os.Getpid(), luksMapperSeq.Add(1))
		if err := workerCfg.AddOverlay(map[string][]string{
			"Imager.EncryptedRootFsName": {uniqueName},
		}); err != nil {
			return fmt.Errorf("failed to set unique LUKS mapper name for ref %s: %w", ref, err)
		}
	}

	// Create an isolated ostree instance for this worker using the
	// cloned config.  We cannot use CloneForRef here because it
	// shares the parent's config, and the imager will mutate
	// Ostree.Sysroot via AddOverlay during Build.
	workerOt, err := ostree.NewOstree(ostree.NewOstreeOptions{
		Config:  workerCfg,
		Stdout:  imgStdout,
		Stderr:  imgStderr,
		Verbose: false,
		Ref:     ref,
	})
	if err != nil {
		return fmt.Errorf(
			"failed to create ostree for ref %s: %w",
			ref, err,
		)
	}

	// Create per-worker fsenc.
	fsenc, err := filesystems.NewFsenc(
		workerCfg,
		func(mapperName string) {
			fmt.Fprintf(imgStdout, "Opening encrypted rootfs as %s ...\n", mapperName)
		},
		func(mapperName string) {
			fmt.Fprintf(imgStdout, "Closing encrypted rootfs as %s ...\n", mapperName)
		},
	)
	if err != nil {
		return fmt.Errorf(
			"failed to create fsenc for ref %s: %w",
			ref, err,
		)
	}
	if err := fsenc.ValidateLuksVariables(); err != nil {
		return fmt.Errorf(
			"LUKS validation failed for ref %s: %w",
			ref, err,
		)
	}

	// Create the imager instance.
	imOpts := &NewImagerOptions{}
	im, err := NewImager(workerCfg, workerOt, fsenc, imOpts)
	if err != nil {
		return fmt.Errorf(
			"failed to initialize imager: %w", err,
		)
	}
	im.SetStdout(imgStdout)
	im.SetStderr(imgStderr)

	if err := ctx.Err(); err != nil {
		return err
	}

	im.SetRef(ref)
	workerOt.SetRef(ref)

	if err := ctx.Err(); err != nil {
		return err
	}

	// Run the image pipeline under an exclusive image lock.
	buildOpts := &BuildOptions{}
	err = im.ExecuteWithImageLock(func() error {
		opts.PushCleanup(func() {
			im.Cleanup()
			fsenc.Cleanup()
			flushWriters()
		})

		// Let the command layer set up GPG, pull ostree, etc.
		// Serialised: SetupBuild is not safe for concurrent use.
		if opts.SetupBuild != nil {
			opts.setupMu.Lock()
			setupErr := opts.SetupBuild(
				opts.PushCleanup, workerOt, im,
			)
			opts.setupMu.Unlock()
			if setupErr != nil {
				return setupErr
			}
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if err := im.Build(buildOpts); err != nil {
			return err
		}

		im.Print("Built image for ref: %s.\n", ref)

		if opts.OnImageDone != nil {
			opts.onImageDoneMu.Lock()
			onDoneErr := opts.OnImageDone(ref)
			opts.onImageDoneMu.Unlock()
			if onDoneErr != nil {
				return fmt.Errorf(
					"failed to record built image: %w", onDoneErr,
				)
			}
		}

		return nil
	})
	return err
}
