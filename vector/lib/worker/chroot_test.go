package worker

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"matrixos/vector/lib/runner"
)

func TestRunInitChroot_SuccessAndEnvFiltering(t *testing.T) {
	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	if err := os.MkdirAll(initDir, 0755); err != nil {
		t.Fatal(err)
	}
	initScript := filepath.Join(initDir, "init.sh")
	if err := os.WriteFile(initScript, []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	var gotCmd *runner.ChrootCmd
	var outBuf, errBuf bytes.Buffer
	err := RunInitChroot(InitChrootOptions{
		DevDir:     devDir,
		ChrootDir:  "/tmp/test-chroot",
		RunnerType: "imager",
		Name:       "/bin/echo",
		Args:       []string{"hello"},
		Env:        []string{"MATRIXOS_DEV_DIR=/old", "RUNNER_TYPE=old", "KEEP=1"},
		Stdout:     &outBuf,
		Stderr:     &errBuf,
		ChrootRun: func(cmd *runner.ChrootCmd) error {
			gotCmd = cmd
			return nil
		},
	})
	if err != nil {
		t.Fatalf("RunInitChroot unexpected error: %v", err)
	}
	if gotCmd == nil {
		t.Fatal("ChrootRun was not invoked")
	}
	if gotCmd.ChrootExec != initScript {
		t.Errorf("ChrootExec = %q, want %q", gotCmd.ChrootExec, initScript)
	}
	if gotCmd.ChrootDir != "/tmp/test-chroot" {
		t.Errorf("ChrootDir = %q, want /tmp/test-chroot", gotCmd.ChrootDir)
	}
	if gotCmd.Stdout != &outBuf || gotCmd.Stderr != &errBuf {
		t.Error("Stdout/Stderr were not wired to ChrootCmd")
	}
	wantEnv := []string{"KEEP=1", "MATRIXOS_DEV_DIR=" + devDir, "RUNNER_TYPE=imager"}
	if !slices.Equal(gotCmd.Env, wantEnv) {
		t.Errorf("Env = %v, want %v", gotCmd.Env, wantEnv)
	}
}

func TestRunInitChroot_MissingInitScript(t *testing.T) {
	err := RunInitChroot(InitChrootOptions{
		DevDir:     t.TempDir(),
		ChrootDir:  "/tmp/test-chroot",
		RunnerType: "releaser",
		Name:       "/bin/true",
		ChrootRun:  func(*runner.ChrootCmd) error { return nil },
	})
	if err == nil || !strings.Contains(err.Error(), "init script not found") {
		t.Fatalf("expected missing init script error, got: %v", err)
	}
}

func TestRunInitChroot_PropagatesError(t *testing.T) {
	devDir := t.TempDir()
	initDir := filepath.Join(devDir, "build", "init")
	if err := os.MkdirAll(initDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(initDir, "init.sh"), []byte("#!/bin/bash\n"), 0755); err != nil {
		t.Fatal(err)
	}

	boom := errors.New("boom")
	err := RunInitChroot(InitChrootOptions{
		DevDir:     devDir,
		ChrootDir:  "/tmp/test-chroot",
		RunnerType: "releaser",
		Name:       "/bin/false",
		ChrootRun:  func(*runner.ChrootCmd) error { return boom },
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected wrapped boom error, got: %v", err)
	}
}
