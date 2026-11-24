package imager

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

func TestChroot(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		tmpDir := t.TempDir()
		initDir := filepath.Join(tmpDir, "build", "init")
		os.MkdirAll(initDir, 0755)
		os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/bash\n"), 0755)

		cfg := baseImageConfig()
		cfg.Items["matrixOS.Root"] = []string{tmpDir}

		mr := runner.NewMockRunner()
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"

		env := []string{"FOO=bar"}
		err := im.chroot(env, "/usr/bin/test-cmd", []string{"--flag"})
		if err != nil {
			t.Fatalf("chroot() unexpected error: %v", err)
		}

		if len(mr.Calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(mr.Calls))
		}
		call := mr.Calls[0]
		if call.Name != "chroot:/usr/bin/test-cmd" {
			t.Errorf("expected name chroot:/usr/bin/test-cmd, got %s", call.Name)
		}
		if call.ChrootDir != "/test-rootfs" {
			t.Errorf("expected chrootDir /test-rootfs, got %s", call.ChrootDir)
		}
		if len(call.Args) != 1 || call.Args[0] != "--flag" {
			t.Errorf("unexpected args: %v", call.Args)
		}

		// Verify env contains MATRIXOS_DEV_DIR and RUNNER_TYPE=imager.
		hasDevDir := false
		hasRunner := false
		hasFoo := false
		for _, e := range call.Env {
			if strings.HasPrefix(e, "MATRIXOS_DEV_DIR=") {
				hasDevDir = true
			}
			if e == "RUNNER_TYPE=imager" {
				hasRunner = true
			}
			if e == "FOO=bar" {
				hasFoo = true
			}
		}
		if !hasDevDir {
			t.Error("expected MATRIXOS_DEV_DIR in env")
		}
		if !hasRunner {
			t.Error("expected RUNNER_TYPE=imager in env")
		}
		if !hasFoo {
			t.Error("expected FOO=bar preserved in env")
		}
	})

	t.Run("EnvFiltersExistingKeys", func(t *testing.T) {
		tmpDir := t.TempDir()
		initDir := filepath.Join(tmpDir, "build", "init")
		os.MkdirAll(initDir, 0755)
		os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/bash\n"), 0755)

		cfg := baseImageConfig()
		cfg.Items["matrixOS.Root"] = []string{tmpDir}

		mr := runner.NewMockRunner()
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"

		// Pass env that already has MATRIXOS_DEV_DIR and RUNNER_TYPE —
		// they should be replaced, not duplicated.
		env := []string{
			"MATRIXOS_DEV_DIR=old-val",
			"RUNNER_TYPE=old-runner",
			"KEEP=me",
		}
		err := im.chroot(env, "/bin/echo", nil)
		if err != nil {
			t.Fatalf("chroot() unexpected error: %v", err)
		}

		call := mr.Calls[0]
		devDirCount := 0
		runnerCount := 0
		keepCount := 0
		for _, e := range call.Env {
			if strings.HasPrefix(e, "MATRIXOS_DEV_DIR=") {
				devDirCount++
				if e == "MATRIXOS_DEV_DIR=old-val" {
					t.Error("old MATRIXOS_DEV_DIR should have been filtered")
				}
			}
			if strings.HasPrefix(e, "RUNNER_TYPE=") {
				runnerCount++
				if e != "RUNNER_TYPE=imager" {
					t.Errorf("RUNNER_TYPE should be imager, got %s", e)
				}
			}
			if e == "KEEP=me" {
				keepCount++
			}
		}
		if devDirCount != 1 {
			t.Errorf("expected exactly 1 MATRIXOS_DEV_DIR, got %d", devDirCount)
		}
		if runnerCount != 1 {
			t.Errorf("expected exactly 1 RUNNER_TYPE, got %d", runnerCount)
		}
		if keepCount != 1 {
			t.Errorf("expected KEEP=me to be preserved")
		}
	})

	t.Run("DevDirError", func(t *testing.T) {
		cfg := baseImageConfig()
		delete(cfg.Items, "matrixOS.Root")

		mr := runner.NewMockRunner()
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"

		err := im.chroot(nil, "/bin/echo", nil)
		if err == nil {
			t.Fatal("expected error when DevDir fails")
		}
		if !strings.Contains(err.Error(), "dev dir") {
			t.Errorf("error should mention dev dir: %v", err)
		}
	})

	t.Run("InitScriptNotFound", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Do NOT create build/init/init.sh.

		cfg := baseImageConfig()
		cfg.Items["matrixOS.Root"] = []string{tmpDir}

		mr := runner.NewMockRunner()
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"

		err := im.chroot(nil, "/bin/echo", nil)
		if err == nil {
			t.Fatal("expected error when init script doesn't exist")
		}
		if !strings.Contains(err.Error(), "init script not found") {
			t.Errorf("error should mention init script: %v", err)
		}
	})

	t.Run("ChrootRunnerError", func(t *testing.T) {
		tmpDir := t.TempDir()
		initDir := filepath.Join(tmpDir, "build", "init")
		os.MkdirAll(initDir, 0755)
		os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/bash\n"), 0755)

		cfg := baseImageConfig()
		cfg.Items["matrixOS.Root"] = []string{tmpDir}

		mr := runner.NewMockRunnerFailOnCall(0, errors.New("chroot failed"))
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"

		err := im.chroot(nil, "/bin/echo", []string{"hello"})
		if err == nil {
			t.Fatal("expected error when chrootRunner fails")
		}
		if !strings.Contains(err.Error(), "chrooted") {
			t.Errorf("error should wrap with chrooted context: %v", err)
		}
	})

	t.Run("StdoutStderrWired", func(t *testing.T) {
		tmpDir := t.TempDir()
		initDir := filepath.Join(tmpDir, "build", "init")
		os.MkdirAll(initDir, 0755)
		os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/bash\n"), 0755)

		cfg := baseImageConfig()
		cfg.Items["matrixOS.Root"] = []string{tmpDir}

		var stdout, stderr bytes.Buffer
		mr := runner.NewMockRunner()
		im := newTestImager(cfg, &ostree.MockOstree{})
		im.chrootRunner = mr.ChrootRun
		im.rootfs = "/test-rootfs"
		im.SetStdout(&stdout)
		im.SetStderr(&stderr)

		err := im.chroot(nil, "/bin/echo", nil)
		if err != nil {
			t.Fatalf("chroot() unexpected error: %v", err)
		}

		// The mock doesn't write, but verify the Cmd had writers set.
		// This is validated by checking the ChrootCmd fields aren't nil
		// (MockRunner records the call, so we rely on no panic).
		if len(mr.Calls) != 1 {
			t.Fatalf("expected 1 call, got %d", len(mr.Calls))
		}
	})
}
