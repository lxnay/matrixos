package seeder

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
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
