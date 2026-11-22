package seeder

import (
	"context"
	"fmt"
	"io"
	"sync"
	"syscall"
	"time"
)

// ParallelSeedOptions configures the parallel seed execution engine.
type ParallelSeedOptions struct {
	// Seeders is the ordered list of seeders to build.
	Seeders []SeederInfo
	// ParamsByName maps each seeder name to its parsed parameters.
	ParamsByName map[string]*SeederParams
	// Parallelism is the maximum number of concurrent seeder builds.
	Parallelism int
	// Verbose enables verbose mode for created seeder instances.
	Verbose bool
	// ChrootDir overrides the chroot directory for all seeders.
	// When empty, each seeder's PreferredChrootDir from params is used.
	ChrootDir string
	// Resume enables resume mode for prepper scripts.
	Resume bool
	// Stage3File is the path to a pre-downloaded stage3 tarball.
	Stage3File string

	// SysProcAttr returns the SysProcAttr for a given worker index,
	// used for cgroup isolation.  May be nil when no isolation is needed.
	SysProcAttr func(workerIndex int) *syscall.SysProcAttr
	// NewSeeder creates an isolated ISeeder instance for a worker goroutine.
	NewSeeder func(opts *NewSeederOptions) (ISeeder, error)
	// NewStdoutWriter creates a labeled stdout writer for a worker.
	NewStdoutWriter func(label string) io.Writer
	// NewStderrWriter creates a labeled stderr writer for a worker.
	NewStderrWriter func(label string) io.Writer
	// PushCleanup registers a function to be called during shutdown.
	PushCleanup func(fn func())
	// OnSeederDone is called when a seeder build completes successfully.
	// It receives the seeder name and resolved chroot directory.
	// Must be safe for concurrent use.
	OnSeederDone func(name, chrootDir string) error

	// BoostWorker upgrades a worker's resource limits to the full system
	// capacity.  Called when only one seed is runnable at a time.  May be nil.
	BoostWorker func(workerIndex int) error
	// UnboostWorker restores a worker's resource limits to the partitioned
	// share.  May be nil.
	UnboostWorker func(workerIndex int) error
}

// ResolveChrootDir returns the chroot directory for a seeder, preferring
// overrideDir when non-empty.
func ResolveChrootDir(name string, params *SeederParams, overrideDir string) (string, error) {
	chrootDir := params.PreferredChrootDir
	if overrideDir != "" {
		chrootDir = overrideDir
	}
	if chrootDir == "" {
		return "", fmt.Errorf(
			"[%s] no chroot dir specified in params or --chroot-dir",
			name,
		)
	}
	return chrootDir, nil
}

// ParallelSeed builds seeders concurrently, respecting dependency
// order and the configured parallelism limit.  Each seeder gets its
// own ISeeder instance so stdout/stderr and mount tracking are isolated.
func ParallelSeed(ctx context.Context, opts *ParallelSeedOptions) error {
	infoByName := make(map[string]SeederInfo, len(opts.Seeders))
	for _, info := range opts.Seeders {
		infoByName[info.Name] = info
	}

	if (opts.BoostWorker == nil) != (opts.UnboostWorker == nil) {
		return fmt.Errorf("BoostWorker and UnboostWorker must both be set or both be nil")
	}

	graph := buildSeedGraph(opts.Seeders, opts.ParamsByName, infoByName)

	return runWorkerPool(ctx, &workerPoolOpts{
		parallelism:  opts.Parallelism,
		infoByName:   infoByName,
		paramsByName: opts.ParamsByName,
		graph:        graph,
		opts:         opts,
	})
}

// seedGraph holds the shared dependency-graph state for the worker pool.
type seedGraph struct {
	mu         sync.Mutex
	depCount   map[string]int
	dependents map[string][]string
	remaining  int
	stopped    bool
	ready      chan string
	// descCount holds the number of transitive dependents for each
	// seed, computed once at build time.  It is used together with
	// remaining to decide whether a seed is the sole bottleneck.
	descCount map[string]int
	// boostSeed tags seeds that should receive full resources when a
	// worker picks them up.  Set at dispatch time (under mu) by
	// checking the graph structure; consumed at receive time.
	boostSeed map[string]bool
}

func buildSeedGraph(
	seeders []SeederInfo,
	paramsByName map[string]*SeederParams,
	infoByName map[string]SeederInfo,
) *seedGraph {
	graph := &seedGraph{
		depCount:   make(map[string]int, len(seeders)),
		dependents: make(map[string][]string),
		remaining:  len(seeders),
		ready:      make(chan string, len(seeders)),
		boostSeed:  make(map[string]bool),
	}
	for _, info := range seeders {
		count := 0
		for _, dep := range paramsByName[info.Name].Depends {
			if _, ok := infoByName[dep]; ok {
				count++
				graph.dependents[dep] = append(
					graph.dependents[dep], info.Name,
				)
			}
			// Dependencies not in the current set are assumed satisfied.
		}
		graph.depCount[info.Name] = count
	}

	graph.descCount = computeDescendantCounts(graph.dependents, seeders)

	// Seed the ready queue with seeders that have zero unsatisfied deps.
	if len(seeders) == 0 {
		close(graph.ready)
		return graph
	}
	for _, info := range seeders {
		if graph.depCount[info.Name] == 0 {
			if graph.descCount[info.Name]+1 == graph.remaining {
				graph.boostSeed[info.Name] = true
			}
			graph.ready <- info.Name
		}
	}
	return graph
}

// computeDescendantCounts returns, for each seed, the number of seeds
// that transitively depend on it (its descendants in the dependency
// DAG).  This is a static graph property used to detect bottleneck
// seeds that block all remaining work.
func computeDescendantCounts(dependents map[string][]string, seeders []SeederInfo) map[string]int {
	counts := make(map[string]int, len(seeders))
	visited := make(map[string]bool)
	var walk func(n string)
	walk = func(n string) {
		for _, d := range dependents[n] {
			if !visited[d] {
				visited[d] = true
				walk(d)
			}
		}
	}
	for _, info := range seeders {
		clear(visited)
		walk(info.Name)
		counts[info.Name] = len(visited)
	}
	return counts
}

type workerPoolOpts struct {
	parallelism  int
	infoByName   map[string]SeederInfo
	paramsByName map[string]*SeederParams
	graph        *seedGraph
	opts         *ParallelSeedOptions
}

// runWorkerPool spawns a fixed pool of worker goroutines that pull
// seeders from the graph's ready channel.  Workers stop when the
// context is cancelled, an error occurs, or all seeders are done.
func runWorkerPool(ctx context.Context, wp *workerPoolOpts) error {
	var firstErr error
	var errMu sync.Mutex

	setError := func(err error) {
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
		}
		errMu.Unlock()
		// Signal all workers to stop by closing the ready channel.
		wp.graph.mu.Lock()
		if !wp.graph.stopped {
			wp.graph.stopped = true
			close(wp.graph.ready)
		}
		wp.graph.mu.Unlock()
	}

	// Fixed worker pool: each goroutine pulls from ready until the
	// channel is closed or the context is cancelled.
	var wg sync.WaitGroup
	for i := 0; i < wp.parallelism; i++ {
		workerIdx := i
		wg.Go(func() {
			var sysProcAttr *syscall.SysProcAttr
			if wp.opts.SysProcAttr != nil {
				sysProcAttr = wp.opts.SysProcAttr(workerIdx)
			}
			for {
				var seederName string
				select {
				case <-ctx.Done():
					return
				case name, ok := <-wp.graph.ready:
					if !ok {
						return
					}
					seederName = name
				}

				// Check whether this seed was tagged for boost at
				// dispatch time.  The tag is set when the seed's
				// transitive dependents + itself cover all remaining
				// seeds, meaning nothing else can run concurrently.
				wantBoost := false
				gotBoost := false
				wp.graph.mu.Lock()
				if wp.graph.boostSeed[seederName] {
					delete(wp.graph.boostSeed, seederName)
					if wp.opts.BoostWorker != nil {
						wantBoost = true
					}
				}
				wp.graph.mu.Unlock()
				if wantBoost {
					if err := wp.opts.BoostWorker(workerIdx); err != nil {
						setError(fmt.Errorf("[%s] failed to boost worker: %w", seederName, err))
						return
					}
					gotBoost = true
				}

				info := wp.infoByName[seederName]

				// Create an isolated ISeeder for this worker.
				sopts := &NewSeederOptions{
					Verbose: wp.opts.Verbose,
					Stdout:  wp.opts.NewStdoutWriter(fmt.Sprintf("seeds:%s", seederName)),
					Stderr:  wp.opts.NewStderrWriter(fmt.Sprintf("seeds:%s", seederName)),
				}
				workerSD, err := wp.opts.NewSeeder(sopts)
				if err != nil {
					if gotBoost && wp.opts.UnboostWorker != nil {
						ubErr := wp.opts.UnboostWorker(workerIdx)
						if ubErr != nil {
							workerSD.PrintError(
								"[%s] failed to unboost worker after failed boost: %v\n",
								seederName,
								ubErr,
							)
						}
					}
					setError(fmt.Errorf("[%s] failed to create seeder: %w", seederName, err))
					return
				}

				wp.opts.PushCleanup(workerSD.Cleanup)
				if gotBoost {
					workerSD.Print(
						"[%s] Worker boosted to max resources. Woohoo.\n",
						seederName,
					)
				}

				// Run the worker under its file lock.
				workerStart := time.Now()
				workerSD.Print(
					"[%s] Worker started at %s\n",
					seederName, workerStart.Format(time.RFC3339),
				)
				err = workerSD.ExecuteWithSeederLock(seederName, func() error {
					return seedWorker(ctx, &seedWorkerOpts{
						sd:          workerSD,
						info:        info,
						params:      wp.paramsByName[seederName],
						sysProcAttr: sysProcAttr,
						opts:        wp.opts,
					})
				})
				workerSD.Cleanup()
				workerEnd := time.Now()
				workerSD.Print(
					"[%s] Worker finished at %s (elapsed: %s)\n",
					seederName, workerEnd.Format(time.RFC3339), workerEnd.Sub(workerStart),
				)

				if err != nil {
					if gotBoost && wp.opts.UnboostWorker != nil {
						_ = wp.opts.UnboostWorker(workerIdx)
					}
					setError(fmt.Errorf("seeder %s failed: %w", seederName, err))
					return
				}

				// Unboost before releasing dependents so new workers
				// see the original resource limits.
				if gotBoost && wp.opts.UnboostWorker != nil {
					if err := wp.opts.UnboostWorker(workerIdx); err != nil {
						setError(fmt.Errorf("[%s] failed to unboost worker: %w", seederName, err))
						return
					}
				}

				// Notify dependents and possibly close ready.
				wp.graph.mu.Lock()
				if !wp.graph.stopped {
					wp.graph.remaining--
					for _, dep := range wp.graph.dependents[seederName] {
						wp.graph.depCount[dep]--
						if wp.graph.depCount[dep] == 0 {
							if wp.graph.descCount[dep]+1 == wp.graph.remaining {
								wp.graph.boostSeed[dep] = true
							}
							wp.graph.ready <- dep
						}
					}
					if wp.graph.remaining == 0 {
						wp.graph.stopped = true
						close(wp.graph.ready)
					}
				}
				wp.graph.mu.Unlock()
			}
		})
	}

	wg.Wait()
	return firstErr
}

type seedWorkerOpts struct {
	sd          ISeeder
	info        SeederInfo
	params      *SeederParams
	sysProcAttr *syscall.SysProcAttr
	opts        *ParallelSeedOptions
}

// seedWorker processes a single seeder: resolve chroot dir, run
// prepper, set up DNS/dirs, execute chroot script, mark done, and
// invoke the OnSeederDone callback.
func seedWorker(ctx context.Context, sw *seedWorkerOpts) error {
	sd := sw.sd
	info := sw.info
	params := sw.params
	opts := sw.opts

	seedStart := time.Now()
	sd.Print(
		"[%s] Seed started at %s\n",
		info.Name, seedStart.Format(time.RFC3339),
	)
	defer func() {
		seedEnd := time.Now()
		sd.Print(
			"[%s] Seed finished at %s (elapsed: %s)\n",
			info.Name, seedEnd.Format(time.RFC3339), seedEnd.Sub(seedStart),
		)
	}()

	sd.Print(
		"[%s] Accepted seeder for execution\n", info.Name,
	)

	// Resolve chroot directory.
	chrootDir, err := ResolveChrootDir(info.Name, params, opts.ChrootDir)
	if err != nil {
		return err
	}
	if opts.ChrootDir != "" {
		sd.PrintWarning(
			"[%s] Overriding chroot dir with --chroot-dir='%s'. This can be dangerous for multiple chroots.\n",
			info.Name,
			opts.ChrootDir,
		)
	}

	flagFile, err := sd.SeederDoneFlagFile(info.Name, chrootDir)
	if err != nil {
		return err
	}

	// Check if already done.
	done, err := sd.IsSeederDone(info.Name, chrootDir)
	if err != nil {
		return err
	}

	if done {
		sd.Print(
			"[%s] Already marked as done via %s. Skipping.\n",
			info.Name, flagFile,
		)
		return nil
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Execute prepper.
	sd.Print(
		"[%s] Executing prepper %s ...\n",
		info.Name, info.PrepperExec,
	)
	prepOpts := &PrepperOptions{
		ChrootDir:  chrootDir,
		Resume:     opts.Resume,
		Stage3File: opts.Stage3File,
	}
	if err := sd.ExecutePrepper(
		info, params, prepOpts,
	); err != nil {
		return fmt.Errorf(
			"[%s] prepper failed: %w", info.Name, err,
		)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Setup DNS.
	if err := sd.SetupChrootDNS(chrootDir); err != nil {
		return fmt.Errorf(
			"[%s] DNS setup failed: %w", info.Name, err,
		)
	}

	// Setup chroot dirs.
	if err := sd.SetupChrootDirs(chrootDir); err != nil {
		return fmt.Errorf(
			"[%s] dir setup failed: %w", info.Name, err,
		)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Execute seeder inside chroot.
	sd.Print(
		"[%s] Running seeder inside %s ...\n",
		info.Name, chrootDir,
	)
	seedOpts := &SeedOptions{
		ChrootDir:   chrootDir,
		Info:        info,
		SysProcAttr: sw.sysProcAttr,
	}
	if err := sd.Seed(seedOpts); err != nil {
		return fmt.Errorf(
			"[%s] chroot execution failed: %w", info.Name, err,
		)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	// Mark done.
	sd.Print("[%s] Flagging %s as complete ...\n",
		info.Name, chrootDir,
	)
	if err := sd.MarkSeederDone(info.Name, chrootDir); err != nil {
		return err
	}

	// Notify the caller.
	if opts.OnSeederDone != nil {
		if err := opts.OnSeederDone(info.Name, chrootDir); err != nil {
			return fmt.Errorf(
				"[%s] failed to call OnSeederDone: %w", info.Name, err,
			)
		}
	}

	sd.Print("[%s] SUCCESS: Build complete.\n", info.Name)
	return nil
}
