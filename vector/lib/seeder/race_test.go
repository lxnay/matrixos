package seeder

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestParallelSeed_EmptySeedersDoesNotDeadlock(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ParallelSeed(ctx, &ParallelSeedOptions{
		Seeders:         nil,
		Parallelism:     2,
		NewStdoutWriter: func(string) io.Writer { return io.Discard },
		NewStderrWriter: func(string) io.Writer { return io.Discard },
	})
	if err != nil {
		t.Fatalf("ParallelSeed with empty seeders returned error: %v", err)
	}
	if ctx.Err() != nil {
		t.Fatalf("ParallelSeed deadlocked until context expired: %v", ctx.Err())
	}
}

func TestParallelSeed_NewSeederErrorWithUnboostErrorDoesNotPanic(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	opts := &ParallelSeedOptions{
		Seeders:      infos,
		ParamsByName: paramsMap,
		Parallelism:  1,
		NewSeeder: func(_ *NewSeederOptions) (ISeeder, error) {
			return nil, errors.New("new seeder failed")
		},
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
		BoostWorker:     func(int) error { return nil },
		UnboostWorker:   func(int) error { return errors.New("unboost failed") },
	}

	err := ParallelSeed(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "new seeder failed") {
		t.Fatalf("expected new seeder error without panic, got: %v", err)
	}
}

func TestParallelSeed_DependencyCycleReturnsError(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-a", "01-b"})
	paramsMap := map[string]*SeederParams{
		"00-a": {ChrootName: "a", PreferredChrootDir: chrootDirs["00-a"], Depends: []string{"01-b"}},
		"01-b": {ChrootName: "b", PreferredChrootDir: chrootDirs["01-b"], Depends: []string{"00-a"}},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ParallelSeed(ctx, &ParallelSeedOptions{
		Seeders:         infos,
		ParamsByName:    paramsMap,
		Parallelism:     2,
		NewSeeder:       func(_ *NewSeederOptions) (ISeeder, error) { return DefaultMockSeeder(), nil },
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Fatalf("expected dependency cycle error, got err=%v ctxErr=%v", err, ctx.Err())
	}
}

func TestParallelSeed_NonPositiveParallelismReturnsError(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	err := ParallelSeed(context.Background(), &ParallelSeedOptions{
		Seeders:         infos,
		ParamsByName:    paramsMap,
		Parallelism:     0,
		NewSeeder:       func(_ *NewSeederOptions) (ISeeder, error) { return DefaultMockSeeder(), nil },
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
	})
	if err == nil {
		t.Fatal("expected error for Parallelism <= 0, got nil")
	}
}

type cancelCheckSeeder struct {
	*MockSeeder
	onPrepper func(info SeederInfo) error
	seedRan   *atomic.Bool
}

func (c *cancelCheckSeeder) ExecutePrepper(info SeederInfo, params *SeederParams, opts *PrepperOptions) error {
	if c.onPrepper != nil {
		return c.onPrepper(info)
	}
	return nil
}

func (c *cancelCheckSeeder) Seed(opts *SeedOptions) error {
	c.seedRan.Store(true)
	return nil
}

func TestParallelSeed_CancelsInFlightWorkersOnFirstError(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-fail", "01-slow"})
	paramsMap := map[string]*SeederParams{
		"00-fail": {ChrootName: "fail", PreferredChrootDir: chrootDirs["00-fail"]},
		"01-slow": {ChrootName: "slow", PreferredChrootDir: chrootDirs["01-slow"]},
	}

	slowEntered := make(chan struct{})
	failDone := make(chan struct{})
	var slowSeedRan atomic.Bool

	opts := &ParallelSeedOptions{
		Seeders:      infos,
		ParamsByName: paramsMap,
		Parallelism:  2,
		NewSeeder: func(_ *NewSeederOptions) (ISeeder, error) {
			return &cancelCheckSeeder{
				MockSeeder: DefaultMockSeeder(),
				seedRan:    &slowSeedRan,
				onPrepper: func(info SeederInfo) error {
					if info.Name == "01-slow" {
						close(slowEntered)
						<-failDone
						// Give setError a moment to cancel poolCtx.
						time.Sleep(30 * time.Millisecond)
						return nil
					}
					<-slowEntered
					defer close(failDone)
					return errors.New("prepper boom")
				},
			}, nil
		},
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
	}

	err := ParallelSeed(context.Background(), opts)
	if err == nil {
		t.Fatal("expected error from failing seeder")
	}
	if slowSeedRan.Load() {
		t.Fatal("in-flight sibling worker ran Seed despite earlier worker failure (pool context not cancelled)")
	}
}

func TestParallelSeed_SerializesOnSeederDone(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-a", "01-b", "02-c", "03-d"})
	paramsMap := map[string]*SeederParams{
		"00-a": {ChrootName: "a", PreferredChrootDir: chrootDirs["00-a"]},
		"01-b": {ChrootName: "b", PreferredChrootDir: chrootDirs["01-b"]},
		"02-c": {ChrootName: "c", PreferredChrootDir: chrootDirs["02-c"]},
		"03-d": {ChrootName: "d", PreferredChrootDir: chrootDirs["03-d"]},
	}

	var doneNames []string // intentionally unsynchronized to verify ParallelSeed serializes OnSeederDone
	var inFlight, maxInFlight atomic.Int32

	opts := &ParallelSeedOptions{
		Seeders:         infos,
		ParamsByName:    paramsMap,
		Parallelism:     4,
		NewSeeder:       func(_ *NewSeederOptions) (ISeeder, error) { return DefaultMockSeeder(), nil },
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
		OnSeederDone: func(name, _ string) error {
			cur := inFlight.Add(1)
			for {
				prev := maxInFlight.Load()
				if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
					break
				}
			}
			time.Sleep(10 * time.Millisecond)
			doneNames = append(doneNames, name)
			inFlight.Add(-1)
			return nil
		},
	}

	if err := ParallelSeed(context.Background(), opts); err != nil {
		t.Fatalf("ParallelSeed: %v", err)
	}
	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("OnSeederDone ran concurrently: maxInFlight = %d, want 1", got)
	}
	if len(doneNames) != len(infos) {
		t.Errorf("len(doneNames) = %d, want %d", len(doneNames), len(infos))
	}
}

type flushTrackingWriter struct {
	flushed atomic.Bool
}

func (w *flushTrackingWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *flushTrackingWriter) Flush()                      { w.flushed.Store(true) }

func TestParallelSeed_FlushesWorkerWriters(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	stdoutW := &flushTrackingWriter{}
	stderrW := &flushTrackingWriter{}
	opts := &ParallelSeedOptions{
		Seeders:         infos,
		ParamsByName:    paramsMap,
		Parallelism:     1,
		NewSeeder:       func(_ *NewSeederOptions) (ISeeder, error) { return DefaultMockSeeder(), nil },
		NewStdoutWriter: func(string) io.Writer { return stdoutW },
		NewStderrWriter: func(string) io.Writer { return stderrW },
		PushCleanup:     noopCleanup,
	}

	if err := ParallelSeed(context.Background(), opts); err != nil {
		t.Fatalf("ParallelSeed: %v", err)
	}
	if !stdoutW.flushed.Load() {
		t.Error("expected worker stdout writer to be flushed")
	}
	if !stderrW.flushed.Load() {
		t.Error("expected worker stderr writer to be flushed")
	}
}

func TestParallelSeed_CancelledContextWithSeedersReturnsContextCanceled(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for range 50 {
		var called atomic.Bool
		err := ParallelSeed(ctx, &ParallelSeedOptions{
			Seeders:      infos,
			ParamsByName: paramsMap,
			Parallelism:  2,
			NewSeeder: func(_ *NewSeederOptions) (ISeeder, error) {
				called.Store(true)
				return DefaultMockSeeder(), nil
			},
			NewStdoutWriter: noopWriter,
			NewStderrWriter: noopWriter,
			PushCleanup:     noopCleanup,
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled for unfinished seeds with cancelled ctx, got: %v", err)
		}
		if called.Load() {
			t.Fatal("NewSeeder was called despite context already being cancelled")
		}
	}
}

func TestParallelSeed_DuplicateSeederNamesReturnsError(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	dupInfos := []SeederInfo{infos[0], infos[0]}
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	err := ParallelSeed(ctx, &ParallelSeedOptions{
		Seeders:         dupInfos,
		ParamsByName:    paramsMap,
		Parallelism:     2,
		NewSeeder:       func(_ *NewSeederOptions) (ISeeder, error) { return DefaultMockSeeder(), nil },
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Fatalf("expected duplicate seeder error, got: %v", err)
	}
}

type attrTrackingSeeder struct {
	*MockSeeder
	onSeed func(opts *SeedOptions) error
}

func (a *attrTrackingSeeder) Seed(opts *SeedOptions) error {
	if a.onSeed != nil {
		return a.onSeed(opts)
	}
	return nil
}

func TestParallelSeed_AcquireSysProcAttrReleasesFD(t *testing.T) {
	infos, chrootDirs := setupParallelSeeders(t, []string{"00-bedrock"})
	paramsMap := map[string]*SeederParams{
		"00-bedrock": {ChrootName: "bedrock", PreferredChrootDir: chrootDirs["00-bedrock"]},
	}

	var released atomic.Bool
	var releasedDuringSeed atomic.Bool
	var seenFD atomic.Int64

	opts := &ParallelSeedOptions{
		Seeders:      infos,
		ParamsByName: paramsMap,
		Parallelism:  1,
		AcquireSysProcAttr: func(workerIndex int) (*syscall.SysProcAttr, func()) {
			return &syscall.SysProcAttr{UseCgroupFD: true, CgroupFD: 42}, func() {
				released.Store(true)
			}
		},
		NewSeeder: func(_ *NewSeederOptions) (ISeeder, error) {
			return &attrTrackingSeeder{
				MockSeeder: DefaultMockSeeder(),
				onSeed: func(sopts *SeedOptions) error {
					if sopts.SysProcAttr != nil {
						seenFD.Store(int64(sopts.SysProcAttr.CgroupFD))
					}
					releasedDuringSeed.Store(released.Load())
					return nil
				},
			}, nil
		},
		NewStdoutWriter: noopWriter,
		NewStderrWriter: noopWriter,
		PushCleanup:     noopCleanup,
	}

	if err := ParallelSeed(context.Background(), opts); err != nil {
		t.Fatalf("ParallelSeed: %v", err)
	}
	if seenFD.Load() != 42 {
		t.Errorf("Seed received CgroupFD = %d, want 42", seenFD.Load())
	}
	if releasedDuringSeed.Load() {
		t.Error("AcquireSysProcAttr release callback ran before Seed completed")
	}
	if !released.Load() {
		t.Error("AcquireSysProcAttr release callback was not called after Seed completed")
	}
}
