package releaser

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/seeder"
)

// mkdirAll is a package-level variable for os.MkdirAll, overridable
// in tests.
var mkdirAll = os.MkdirAll

// ParallelReleaseOptions configures the parallel release execution
// engine.
type ParallelReleaseOptions struct {
	// Seeders is the ordered list of seeders to release.
	Seeders []seeder.SeederInfo
	// Parallelism is the maximum number of concurrent releases.
	Parallelism int
	// ReleaseStage is the release stage (dev or prod).
	ReleaseStage string

	// Config is the application configuration.
	Config config.IConfig
	// Ostree is the ostree instance used as prototype for cloning
	// per-worker instances.
	Ostree ostree.IOstree

	// NewStdoutWriter creates a labeled stdout writer for a worker.
	NewStdoutWriter func(label string) io.Writer
	// NewStderrWriter creates a labeled stderr writer for a worker.
	NewStderrWriter func(label string) io.Writer
	// PushCleanup registers a function to be called during shutdown.
	PushCleanup func(fn func())

	// FindChrootDir resolves the chroot directory for a seeder.
	FindChrootDir func(info seeder.SeederInfo) (string, error)
	// ShortRef returns a short display name for a branch ref.
	ShortRef func(ref string) string
	// OnReleaseDone is called when a release completes successfully.
	// It receives the branch name.  Serialized internally — the callback
	// does not need its own locking.
	OnReleaseDone func(branch string) error
}

// ParallelRelease builds releases concurrently using a simple
// semaphore-based worker pool.  Unlike seed parallelism, releases
// have no inter-dependencies so all seeders are immediately eligible.
func ParallelRelease(ctx context.Context, opts *ParallelReleaseOptions) error {
	if opts.Parallelism < 1 {
		return fmt.Errorf("parallelism must be >= 1, got %d", opts.Parallelism)
	}
	return runReleaseWorkerPool(ctx, opts)
}

// runReleaseWorkerPool spawns a fixed pool of worker goroutines that
// pull seeders from a work channel.
func runReleaseWorkerPool(ctx context.Context, opts *ParallelReleaseOptions) error {
	poolCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var firstErr error
	var errMu sync.Mutex
	var onReleaseDoneMu sync.Mutex

	setError := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		cancel()
	}

	work := make(chan seeder.SeederInfo, len(opts.Seeders))
	for _, info := range opts.Seeders {
		work <- info
	}
	close(work)

	var wg sync.WaitGroup
	for i := 0; i < opts.Parallelism; i++ {
		wg.Go(func() {
			for {
				select {
				case <-poolCtx.Done():
					return
				case info, ok := <-work:
					if !ok {
						return
					}

					// Check for earlier failure.
					errMu.Lock()
					failed := firstErr != nil
					errMu.Unlock()
					if failed {
						return
					}

					err := releaseWorker(poolCtx, opts, info, &onReleaseDoneMu)
					if err != nil {
						setError(fmt.Errorf(
							"seeder %s release failed: %w",
							info.Name, err,
						))
						return
					}
				}
			}
		})
	}

	wg.Wait()
	return firstErr
}

// releaseWorker processes a single seeder release.
func releaseWorker(ctx context.Context, opts *ParallelReleaseOptions, info seeder.SeederInfo, onReleaseDoneMu *sync.Mutex) error {
	seederName := info.Name
	branchShortname := seeder.SeederNameWithoutOrderPrefix(
		seederName,
	)

	// Create per-worker stdout writer.
	workerStdout := opts.NewStdoutWriter(
		fmt.Sprintf("releases:%s", seederName),
	)
	workerPrint := func(format string, args ...any) {
		fmt.Fprintf(workerStdout, format, args...)
	}

	releaseStart := time.Now()
	workerPrint(
		"[%s] Release started at %s\n",
		seederName, releaseStart.Format(time.RFC3339),
	)
	defer func() {
		releaseEnd := time.Now()
		workerPrint(
			"[%s] Release finished at %s (elapsed: %s)\n",
			seederName,
			releaseEnd.Format(time.RFC3339),
			releaseEnd.Sub(releaseStart).Round(time.Second),
		)
	}()

	workerPrint(
		"Working on seeder %s, ostree branch short name: %s\n",
		seederName, branchShortname,
	)

	if err := ctx.Err(); err != nil {
		return err
	}

	// Compute the full ostree branch name.
	osName, err := opts.Ostree.OsName()
	if err != nil {
		return fmt.Errorf("failed to get OS name: %w", err)
	}
	arch, err := opts.Ostree.Arch()
	if err != nil {
		return fmt.Errorf("failed to get arch: %w", err)
	}
	branch, err := ostree.BranchShortnameToNormal(
		opts.ReleaseStage, branchShortname, osName, arch,
	)
	if err != nil {
		return fmt.Errorf(
			"unable to find ostree branch for %s: %w",
			branchShortname, err,
		)
	}

	workerPrint("Determined ostree branch to be: %s\n", branch)

	// Set up release-specific styled writers.
	shortRef := branch
	if opts.ShortRef != nil {
		shortRef = opts.ShortRef(branch)
	}
	relStdout := opts.NewStdoutWriter(
		fmt.Sprintf("release:%s", shortRef),
	)
	relStderr := opts.NewStderrWriter(
		fmt.Sprintf("release:%s", shortRef),
	)
	flushWriters := func() {
		if f, ok := relStdout.(interface{ Flush() }); ok {
			f.Flush()
		}
		if f, ok := relStderr.(interface{ Flush() }); ok {
			f.Flush()
		}
	}
	defer flushWriters()

	// Clone an isolated ostree instance for this worker.
	workerOt, err := opts.Ostree.CloneForRef(branch)
	if err != nil {
		return fmt.Errorf(
			"failed to clone ostree for ref %s: %w",
			branch, err,
		)
	}
	workerOt.SetStdout(relStdout)
	workerOt.SetStderr(relStderr)
	workerOt.SetVerbose(false)

	// Create the releaser instance.
	relOpts := &NewReleaserOptions{
		Ref: branch,
	}
	rel, err := NewReleaser(opts.Config, workerOt, relOpts)
	if err != nil {
		return fmt.Errorf(
			"failed to initialize releaser: %w", err,
		)
	}
	rel.SetStdout(relStdout)
	rel.SetStderr(relStderr)

	if err := ctx.Err(); err != nil {
		return err
	}

	// Run the release pipeline under an exclusive release lock.
	err = rel.ExecuteWithReleaseLock(func() error {
		opts.PushCleanup(func() {
			rel.Cleanup()
			flushWriters()
		})

		// Locate the chroot directory.
		chrootDir, err := opts.FindChrootDir(info)
		if err != nil {
			return err
		}
		workerPrint(
			"Selected chroot dir: %s for seeder: %s\n",
			chrootDir, seederName,
		)
		rel.SetChrootDir(chrootDir)

		// Compute image directory.
		imageDir := ChrootDirForImageDir(chrootDir)
		if err := mkdirAll(imageDir, 0755); err != nil {
			return fmt.Errorf(
				"failed to create image dir %s: %w",
				imageDir, err,
			)
		}
		if err := rel.SetImageDir(imageDir); err != nil {
			return fmt.Errorf(
				"failed to set image dir %s: %w",
				imageDir, err,
			)
		}

		if err := ctx.Err(); err != nil {
			return err
		}

		if err := rel.Build(); err != nil {
			return err
		}

		rel.Print(
			"Released filesystem to ostree as branch: %s.\n",
			branch,
		)

		if opts.OnReleaseDone != nil {
			onReleaseDoneMu.Lock()
			onDoneErr := opts.OnReleaseDone(branch)
			onReleaseDoneMu.Unlock()
			if onDoneErr != nil {
				return fmt.Errorf(
					"failed to record built release: %w", onDoneErr,
				)
			}
		}

		return nil
	})
	return err
}

// ChrootDirForImageDir computes the image directory path from a chroot
// directory, mirroring the bash chroot_dir_for_image_dir function.
func ChrootDirForImageDir(chrootDir string) string {
	return chrootDir + ".ostree_rootfs"
}
