// Package cgroups provides helpers for managing cgroup v2 hierarchies,
// primarily for memory-limiting parallel worker processes.
package cgroups

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	defaultCgroupV2Root = "/sys/fs/cgroup"
	cpuPeriod           = 100000
)

var (
	poolSeq                atomic.Uint64
	cgroupRemoveFunc       = os.Remove
	cgroupRemoveMaxRetries = 10
	cgroupRemoveRetryDelay = 25 * time.Millisecond
)

// workerCgroup holds the state for a single per-worker cgroup.
type workerCgroup struct {
	dir string
	fd  int
}

// WorkerPool manages a set of per-worker cgroups with memory limits.
// A nil *WorkerPool is safe to call methods on (no-op).
type WorkerPool struct {
	mu        sync.RWMutex
	parentDir string
	workers   []*workerCgroup
	staleDirs []string
	opts      *WorkerPoolOptions
	useCpuset bool
}

// WorkerPoolOptions contains the resource limits for a worker pool.
type WorkerPoolOptions struct {
	Parallelism       int
	MemPerWorkerBytes uint64
	// TotalMemBytes is the actual total memory budget.  Used by
	// BoostWorker to give a sole worker full resources without the
	// lossy MemPerWorkerBytes * Parallelism round-trip.
	TotalMemBytes uint64
	NumCPUs       int
	// CoresMultiplier scales the number of CPU cores assigned to each
	// worker's cpuset.  Values > 1.0 cause overlapping ranges (cores
	// shared between workers); values < 1.0 reduce each worker's
	// slice.  Defaults to 1.0 (strict partition, no overlap).
	CoresMultiplier float64
	// CgroupRoot overrides the cgroup v2 mount point (default: /sys/fs/cgroup).
	// Useful for testing with a fake cgroup tree.
	CgroupRoot string
}

// cgroupRoot returns the effective cgroup v2 mount point.
func (o *WorkerPoolOptions) cgroupRoot() string {
	if o.CgroupRoot != "" {
		return o.CgroupRoot
	}
	return defaultCgroupV2Root
}

// NewWorkerPool creates a cgroup v2 hierarchy for parallel workers,
// each limited to the specified memory and CPU slice.
func NewWorkerPool(opts *WorkerPoolOptions) (*WorkerPool, error) {
	if opts == nil {
		return nil, fmt.Errorf("cgroups: options must not be nil")
	}
	if opts.Parallelism < 1 {
		return nil, fmt.Errorf("cgroups: parallelism must be >= 1, got %d", opts.Parallelism)
	}
	if opts.NumCPUs < 1 {
		return nil, fmt.Errorf("cgroups: numCPUs must be >= 1, got %d", opts.NumCPUs)
	}
	if opts.TotalMemBytes == 0 {
		return nil, fmt.Errorf("cgroups: TotalMemBytes must be set")
	}
	if opts.MemPerWorkerBytes == 0 {
		return nil, fmt.Errorf("cgroups: MemPerWorkerBytes must be > 0")
	}

	root := opts.cgroupRoot()

	// Verify cgroup v2 is available.
	subCtl := filepath.Join(root, "cgroup.subtree_control")
	if _, err := os.Stat(subCtl); err != nil {
		return nil, fmt.Errorf("cgroups: cgroup v2 not available (%s): %w", subCtl, err)
	}

	// Try to enable controllers on the root cgroup so they are
	// available for delegation into our child hierarchy.  This is
	// best-effort — the root may already have them, or we may lack
	// permission, both of which are fine.
	enableRootControllers(subCtl)

	parentDir := filepath.Join(root, fmt.Sprintf("vector-seeds-%d-%d", os.Getpid(), poolSeq.Add(1)))
	if err := os.Mkdir(parentDir, 0755); err != nil {
		return nil, fmt.Errorf("cgroups: failed to create parent dir %s: %w", parentDir, err)
	}

	useCpuset, err := enableControllers(parentDir, opts)
	if err != nil {
		os.Remove(parentDir)
		return nil, err
	}

	optsCopy := *opts
	pool := &WorkerPool{
		parentDir: parentDir,
		opts:      &optsCopy,
		useCpuset: useCpuset,
	}
	for i := range opts.Parallelism {
		wc, err := createWorkerCgroup(parentDir, i, opts, useCpuset)
		if err != nil {
			pool.Close()
			return nil, err
		}
		pool.workers = append(pool.workers, wc)
	}

	return pool, nil
}

// enableControllers enables cgroup v2 controllers on the parent directory.
// memory is always required.  cpuset is preferred for CPU pinning; cpu
// bandwidth throttling (cpu.max) is only enabled as a fallback.
func enableControllers(parentDir string, opts *WorkerPoolOptions) (useCpuset bool, _ error) {
	ctl := filepath.Join(parentDir, "cgroup.subtree_control")
	if err := os.WriteFile(ctl, []byte("+memory"), 0644); err != nil {
		return useCpuset, fmt.Errorf("cgroups: failed to enable memory controller: %w", err)
	}

	useCpuset = false
	cpusPerWorker := opts.NumCPUs / opts.Parallelism
	if cpusPerWorker >= 1 {
		cpusetErr := os.WriteFile(ctl, []byte("+cpuset"), 0644)
		if cpusetErr == nil {
			useCpuset = true
		} else {
			fmt.Fprintf(
				os.Stderr,
				"WARNING: cpuset controller not available, falling back to cpu bandwidth throttling: %v\n",
				cpusetErr,
			)
		}
	}

	if !useCpuset {
		// cpuset not available — fall back to cpu bandwidth throttling.
		if err := os.WriteFile(ctl, []byte("+cpu"), 0644); err != nil {
			return false, fmt.Errorf("cgroups: failed to enable cpu controller: %w", err)
		}
	}
	return useCpuset, nil
}

// enableRootControllers tries to enable memory, cpu, and cpuset on the
// root cgroup's subtree_control.  Each write is independent and
// best-effort — failures are silently ignored (the controller may
// already be enabled, or we may lack permission).
func enableRootControllers(subtreeControlPath string) {
	for _, ctl := range []string{"+memory", "+cpu", "+cpuset"} {
		_ = os.WriteFile(subtreeControlPath, []byte(ctl), 0644)
	}
}

// createWorkerCgroup creates a single worker cgroup directory with
// memory, cpu, and optional cpuset limits, and returns an open fd.
func createWorkerCgroup(parentDir string, index int, opts *WorkerPoolOptions, useCpuset bool) (*workerCgroup, error) {
	dir := filepath.Join(parentDir, fmt.Sprintf("worker-%d", index))
	if err := os.Mkdir(dir, 0755); err != nil {
		return nil, fmt.Errorf("cgroups: failed to create worker-%d dir: %w", index, err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.Remove(dir)
		}
	}()

	if err := writeMemoryLimit(dir, index, opts.MemPerWorkerBytes); err != nil {
		return nil, err
	}
	if useCpuset {
		// cpuset pins workers to specific cores, which already limits
		// CPU usage and provides cache locality.  cpu.max bandwidth
		// throttling is only needed as a fallback.
		if err := writeCpusetPinning(dir, index, opts); err != nil {
			return nil, err
		}
	} else {
		if err := writeCPUQuota(dir, index, opts); err != nil {
			return nil, err
		}
	}

	fd, err := syscall.Open(dir, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("cgroups: failed to open worker-%d cgroup fd: %w", index, err)
	}
	ok = true
	return &workerCgroup{dir: dir, fd: fd}, nil
}

func writeMemoryLimit(dir string, index int, memBytes uint64) error {
	path := filepath.Join(dir, "memory.max")
	if err := os.WriteFile(path, fmt.Appendf(nil, "%d", memBytes), 0644); err != nil {
		return fmt.Errorf("cgroups: failed to write memory.max for worker-%d: %w", index, err)
	}
	return nil
}

func writeCPUQuotaValue(dir string, index, cpuQuota int, suffix string) error {
	path := filepath.Join(dir, "cpu.max")
	if err := os.WriteFile(path, fmt.Appendf(nil, "%d %d", cpuQuota, cpuPeriod), 0644); err != nil {
		return fmt.Errorf("cgroups: failed to write cpu.max%s for worker-%d: %w", suffix, index, err)
	}
	return nil
}

func writeCPUQuota(dir string, index int, opts *WorkerPoolOptions) error {
	// CPU bandwidth limit via cpu.max: "$MAX $PERIOD" in µs.
	// e.g. 4 CPUs → "400000 100000" (400ms of CPU time per 100ms).
	// Supports fractional CPUs (e.g. 12 CPUs / 5 workers = 240000).
	cpuQuota := max(opts.NumCPUs*cpuPeriod/opts.Parallelism, cpuPeriod)
	return writeCPUQuotaValue(dir, index, cpuQuota, "")
}

func writeCpusetRange(dir string, index, cpuStart, cpuEnd int, suffix string) error {
	path := filepath.Join(dir, "cpuset.cpus")
	if err := os.WriteFile(path, fmt.Appendf(nil, "%d-%d", cpuStart, cpuEnd), 0644); err != nil {
		return fmt.Errorf("cgroups: failed to write cpuset.cpus%s for worker-%d: %w", suffix, index, err)
	}
	return nil
}

func writeCpusetPinning(dir string, index int, opts *WorkerPoolOptions) error {
	// Base cores per worker (strict partition).
	baseCPUs := opts.NumCPUs / opts.Parallelism
	extraCPUs := opts.NumCPUs % opts.Parallelism

	// Apply the multiplier and clamp to total available cores.
	multiplier := opts.CoresMultiplier
	if multiplier <= 0 {
		multiplier = 1.0
	}
	baseForWorker := baseCPUs
	if index < extraCPUs {
		baseForWorker++
	}

	effective := min(
		max(int(math.Round(float64(baseForWorker)*multiplier)), 1),
		opts.NumCPUs,
	)

	// Compute the start position from strict partitioning, then centre
	// the (potentially wider) range around it.
	strictStart := index*baseCPUs + min(index, extraCPUs)
	strictMid := strictStart + baseForWorker/2

	cpuStart := strictMid - effective/2
	cpuEnd := cpuStart + effective - 1

	// Clamp to [0, NumCPUs-1].
	if cpuStart < 0 {
		cpuStart = 0
		cpuEnd = effective - 1
	}
	if cpuEnd >= opts.NumCPUs {
		cpuEnd = opts.NumCPUs - 1
		cpuStart = max(cpuEnd-effective+1, 0)
	}

	return writeCpusetRange(dir, index, cpuStart, cpuEnd, "")
}

func (p *WorkerPool) setWorkerResources(workerIndex int, boosted bool) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if workerIndex < 0 || workerIndex >= len(p.workers) {
		return nil
	}
	w := p.workers[workerIndex]
	memBytes := p.opts.MemPerWorkerBytes
	if boosted {
		memBytes = p.opts.TotalMemBytes
	}
	if err := writeMemoryLimit(w.dir, workerIndex, memBytes); err != nil {
		return err
	}
	if p.useCpuset {
		if boosted {
			return writeCpusetRange(w.dir, workerIndex, 0, p.opts.NumCPUs-1, " (boost)")
		}
		return writeCpusetPinning(w.dir, workerIndex, p.opts)
	}
	if boosted {
		return writeCPUQuotaValue(w.dir, workerIndex, p.opts.NumCPUs*cpuPeriod, " (boost)")
	}
	return writeCPUQuota(w.dir, workerIndex, p.opts)
}

// BoostWorker reconfigures a worker's cgroup to use all available
// resources (memory + CPU).  This is intended for situations where only
// one seed is runnable at a time and should get the full machine.
func (p *WorkerPool) BoostWorker(workerIndex int) error {
	return p.setWorkerResources(workerIndex, true)
}

// UnboostWorker restores a worker's cgroup to its partitioned share
// of resources.
func (p *WorkerPool) UnboostWorker(workerIndex int) error {
	return p.setWorkerResources(workerIndex, false)
}

// SysProcAttr returns a SysProcAttr configured to spawn a child process
// directly into the given worker's cgroup via clone3(CLONE_INTO_CGROUP),
// or nil if the pool is inactive.
func (p *WorkerPool) SysProcAttr(workerIndex int) *syscall.SysProcAttr {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if workerIndex < 0 || workerIndex >= len(p.workers) || p.workers[workerIndex].fd < 0 {
		return nil
	}
	return &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    p.workers[workerIndex].fd,
	}
}

// AcquireSysProcAttr duplicates the worker's cgroup directory file descriptor
// with FD_CLOEXEC and returns a SysProcAttr together with a release callback
// that closes the duplicate. Holding an owned duplicate prevents a concurrent
// WorkerPool.Close() from closing or allowing FD reuse of CgroupFD while a
// child process is being forked with clone3(CLONE_INTO_CGROUP).
// Returns (nil, noop) if the pool is nil, closed, or workerIndex is out of range.
func (p *WorkerPool) AcquireSysProcAttr(workerIndex int) (*syscall.SysProcAttr, func()) {
	noop := func() {}
	if p == nil {
		return nil, noop
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if workerIndex < 0 || workerIndex >= len(p.workers) || p.workers[workerIndex].fd < 0 {
		return nil, noop
	}
	dupFd, err := unix.FcntlInt(uintptr(p.workers[workerIndex].fd), unix.F_DUPFD_CLOEXEC, 0)
	if err != nil {
		return nil, noop
	}
	release := sync.OnceFunc(func() {
		_ = syscall.Close(dupFd)
	})
	return &syscall.SysProcAttr{
		UseCgroupFD: true,
		CgroupFD:    dupFd,
	}, release
}

// removeCgroupDir removes a cgroup directory, retrying briefly only on
// transient EBUSY while the kernel finishes reaping killed cgroup tasks.
func removeCgroupDir(dir string) error {
	var err error
	for attempt := range cgroupRemoveMaxRetries {
		err = cgroupRemoveFunc(dir)
		if err == nil || os.IsNotExist(err) {
			return nil
		}
		if !errors.Is(err, syscall.EBUSY) || attempt == cgroupRemoveMaxRetries-1 {
			return err
		}
		time.Sleep(cgroupRemoveRetryDelay)
	}
	return err
}

// Close releases all file descriptors and removes the cgroup hierarchy.
// Calling Close multiple times is safe and retries removal of any cgroup
// directories that were still busy (EBUSY) on an earlier call.
func (p *WorkerPool) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, w := range p.workers {
		if w.fd >= 0 {
			syscall.Close(w.fd)
			w.fd = -1
		}
		p.staleDirs = append(p.staleDirs, w.dir)
	}
	p.workers = nil

	var remaining []string
	for _, dir := range p.staleDirs {
		// Best-effort kill any lingering processes in this worker cgroup
		// (cgroup v2 cgroup.kill, Linux 5.14+) if the kernel interface exists.
		killPath := filepath.Join(dir, "cgroup.kill")
		if _, err := os.Stat(killPath); err == nil {
			_ = os.WriteFile(killPath, []byte("1"), 0644)
		}
		if err := removeCgroupDir(dir); err != nil {
			remaining = append(remaining, dir)
		}
	}
	p.staleDirs = remaining

	if p.parentDir != "" {
		if err := removeCgroupDir(p.parentDir); err == nil {
			p.parentDir = ""
		}
	}
}
