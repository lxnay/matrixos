package releaser

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/seeder"
)

type trackingOstree struct {
	*ostree.MockOstree
	inFlight    *atomic.Int32
	maxInFlight *atomic.Int32
	initCalls   *atomic.Int32
}

func (t *trackingOstree) enter() {
	cur := t.inFlight.Add(1)
	for {
		prev := t.maxInFlight.Load()
		if cur <= prev || t.maxInFlight.CompareAndSwap(prev, cur) {
			break
		}
	}
	time.Sleep(15 * time.Millisecond)
	t.inFlight.Add(-1)
}

func (t *trackingOstree) InitRepo() error {
	t.enter()
	t.initCalls.Add(1)
	// Simulate ostree creating objects/ directory during init.
	repoDir, _ := t.RepoDir()
	_ = os.MkdirAll(filepath.Join(repoDir, "objects"), 0755)
	return nil
}

func (t *trackingOstree) SetGpg(enabled bool) error {
	t.enter()
	return nil
}

func (t *trackingOstree) Commit(opts ostree.CommitOptions) error {
	t.enter()
	return nil
}

func (t *trackingOstree) Prune() error {
	t.enter()
	return nil
}

func (t *trackingOstree) GenerateStaticDelta() error {
	t.enter()
	return nil
}

func (t *trackingOstree) UpdateSummary() error {
	t.enter()
	return nil
}

func TestReleaser_ConcurrentOstreeRepoMutationsSerialized(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "ostree-repo")

	var inFlight, maxInFlight, initCalls atomic.Int32

	const workers = 4
	var wg sync.WaitGroup
	for i := range workers {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			imgDir := filepath.Join(tmp, "img-"+string(rune('a'+idx)))
			vdbDir := filepath.Join(imgDir, "usr/var/db/pkg/cat/pkg-1.0")
			if err := os.MkdirAll(vdbDir, 0755); err != nil {
				t.Errorf("mkdir vdb: %v", err)
				return
			}

			cfg := &config.MockConfig{
				Items: map[string][]string{
					"Releaser.ReadOnlyVdb":     {"usr/var/db/pkg"},
					"Seeder.ChrootMetadataDir": {"etc/metadata"},
				},
				Bools: map[string]bool{
					"Releaser.GenerateStaticDeltas": true,
				},
			}
			ot := &trackingOstree{
				MockOstree: &ostree.MockOstree{
					RepoDir_:     repoDir,
					OsName_:      "matrixos",
					FancyOsName_: "matrixOS",
					Ref_:         "matrixos/amd64/dev/test",
				},
				inFlight:    &inFlight,
				maxInFlight: &maxInFlight,
				initCalls:   &initCalls,
			}

			r, err := NewReleaser(cfg, ot, &NewReleaserOptions{
				ImageDir: imgDir,
				Ref:      "matrixos/amd64/dev/test",
			})
			if err != nil {
				t.Errorf("NewReleaser: %v", err)
				return
			}
			r.SetStdout(io.Discard)
			r.SetStderr(io.Discard)
			if err := r.MaybeOstreeInit(); err != nil {
				t.Errorf("MaybeOstreeInit: %v", err)
				return
			}
			if err := r.Release(CommitOptions{Branch: "matrixos/amd64/dev/test"}); err != nil {
				t.Errorf("Release: %v", err)
				return
			}
		}(i)
	}
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("concurrent ostree repo mutations detected: maxInFlight = %d, want 1", got)
	}
	if got := initCalls.Load(); got != 1 {
		t.Errorf("InitRepo called %d times across concurrent workers, want 1", got)
	}
}

func TestParallelRelease_NonPositiveParallelismReturnsError(t *testing.T) {
	opts := testParallelOpts([]seeder.SeederInfo{{Name: "00-bedrock"}}, t.TempDir())
	opts.Parallelism = 0

	if err := ParallelRelease(context.Background(), opts); err == nil {
		t.Fatal("expected error for Parallelism <= 0, got nil")
	}
}

func TestParallelRelease_CancelsInFlightWorkersOnFirstError(t *testing.T) {
	tmp := t.TempDir()
	locksDir := filepath.Join(tmp, "locks")
	slowChroot := filepath.Join(tmp, "slow-chroot")
	if err := os.MkdirAll(slowChroot, 0755); err != nil {
		t.Fatal(err)
	}

	opts := testParallelOpts([]seeder.SeederInfo{
		{Name: "00-fail"},
		{Name: "01-slow"},
	}, slowChroot)
	opts.Parallelism = 2
	opts.Config = &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot":              {"/sysroot"},
			"Ostree.FullBranchSuffix":     {"full"},
			"matrixOS.OsName":             {"matrixos"},
			"matrixOS.Arch":               {"amd64"},
			"matrixOS.PrivateGitRepoPath": {tmp},
			"Releaser.LocksDir":           {locksDir},
			"Releaser.LockWaitSeconds":    {"5"},
		},
	}

	slowInFind := make(chan struct{})
	failDone := make(chan struct{})

	opts.FindChrootDir = func(info seeder.SeederInfo) (string, error) {
		if info.Name == "01-slow" {
			close(slowInFind)
			<-failDone
			time.Sleep(30 * time.Millisecond)
			return slowChroot, nil
		}
		<-slowInFind
		defer close(failDone)
		return "", errors.New("find chroot boom")
	}

	var slowReachedBuild atomic.Bool
	baseOt := opts.Ostree.(*ostree.MockOstree)
	opts.Ostree = &buildDetectOstree{MockOstree: baseOt, buildTouched: &slowReachedBuild}

	err := ParallelRelease(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error from failing release worker")
	}
	if slowReachedBuild.Load() {
		t.Fatal("in-flight sibling worker entered Build despite earlier failure (pool context not cancelled)")
	}
}

type buildDetectOstree struct {
	*ostree.MockOstree
	buildTouched *atomic.Bool
}

func (b *buildDetectOstree) CloneForRef(ref string) (ostree.IOstree, error) {
	c := *b.MockOstree
	c.Ref_ = ref
	return &buildDetectOstree{MockOstree: &c, buildTouched: b.buildTouched}, nil
}

func (b *buildDetectOstree) BranchToFull() (string, error) {
	b.buildTouched.Store(true)
	return "", errors.New("should not reach build")
}
