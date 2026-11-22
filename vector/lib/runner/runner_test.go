package runner

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// ---------------------------------------------------------------------------
// Run / Output / CombinedOutput – real execution with a trivial command
// ---------------------------------------------------------------------------

func TestRun_Echo(t *testing.T) {
	var stdout, stderr bytes.Buffer
	err := Run(&Cmd{Name: "echo", Args: []string{"hello"}, Stdout: &stdout, Stderr: &stderr})
	if err != nil {
		t.Fatalf("Run(echo hello): unexpected error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "hello" {
		t.Errorf("stdout = %q, want %q", got, "hello")
	}
}

func TestRun_Failure(t *testing.T) {
	err := Run(&Cmd{Name: "false", Stdout: io.Discard, Stderr: io.Discard})
	if err == nil {
		t.Fatal("Run(false): expected error, got nil")
	}
}

func TestOutput_Echo(t *testing.T) {
	out, err := Output(&Cmd{Name: "echo", Args: []string{"world"}})
	if err != nil {
		t.Fatalf("Output(echo world): unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "world" {
		t.Errorf("output = %q, want %q", got, "world")
	}
}

func TestCombinedOutput_Echo(t *testing.T) {
	out, err := CombinedOutput(
		&Cmd{Name: "echo", Args: []string{"combined"}},
	)
	if err != nil {
		t.Fatalf("CombinedOutput(echo combined): unexpected error: %v", err)
	}
	if got := strings.TrimSpace(string(out)); got != "combined" {
		t.Errorf("output = %q, want %q", got, "combined")
	}
}

func TestRun_WithDir(t *testing.T) {
	var stdout bytes.Buffer
	cmd := &Cmd{Name: "pwd", Stdout: &stdout, Stderr: io.Discard}
	cmd.Dir = "/tmp"
	if err := Run(cmd); err != nil {
		t.Fatalf("Run(pwd): unexpected error: %v", err)
	}
	got := strings.TrimSpace(stdout.String())
	// /tmp may be a symlink (e.g. /private/tmp on macOS, /sysroot/tmp on
	// ostree-based systems), so resolve it before comparing.
	want, err := filepath.EvalSymlinks("/tmp")
	if err != nil {
		want = "/tmp"
	}
	if got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

func TestRun_WithEnv(t *testing.T) {
	var stdout bytes.Buffer
	cmd := &Cmd{Name: "env", Stdout: &stdout, Stderr: io.Discard}
	cmd.Env = []string{"MY_TEST_VAR=hello_runner"}
	if err := Run(cmd); err != nil {
		t.Fatalf("Run(env): unexpected error: %v", err)
	}
	if !strings.Contains(stdout.String(), "MY_TEST_VAR=hello_runner") {
		t.Errorf("env output should contain MY_TEST_VAR, got: %s",
			stdout.String())
	}
}

func TestOutput_WithEnv(t *testing.T) {
	cmd := &Cmd{
		Name: "env",
		Env:  []string{"MY_OUTPUT_VAR=from_output"},
	}
	out, err := Output(cmd)
	if err != nil {
		t.Fatalf("Output(env): unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "MY_OUTPUT_VAR=from_output") {
		t.Errorf("env output should contain MY_OUTPUT_VAR, got: %s", string(out))
	}
}

func TestCombinedOutput_WithEnv(t *testing.T) {
	cmd := &Cmd{
		Name: "env",
		Env:  []string{"MY_COMBINED_VAR=from_combined"},
	}
	out, err := CombinedOutput(cmd)
	if err != nil {
		t.Fatalf("CombinedOutput(env): unexpected error: %v", err)
	}
	if !strings.Contains(string(out), "MY_COMBINED_VAR=from_combined") {
		t.Errorf("env output should contain MY_COMBINED_VAR, got: %s", string(out))
	}
}

func TestChrootRun_PassesEnv(t *testing.T) {
	origRun := Run
	defer func() { Run = origRun }()

	var capturedEnv []string
	Run = func(c *Cmd) error {
		capturedEnv = c.Env
		return nil
	}

	env := []string{"CHROOT_VAR=run_env"}
	err := ChrootRun(&ChrootCmd{
		Cmd: Cmd{
			Name:   "/bin/sh",
			Env:    env,
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootRun: unexpected error: %v", err)
	}
	if len(capturedEnv) != 1 || capturedEnv[0] != "CHROOT_VAR=run_env" {
		t.Errorf("env not passed through ChrootRun: got %v", capturedEnv)
	}
}

func TestChrootOutput_PassesEnv(t *testing.T) {
	origOutput := Output
	defer func() { Output = origOutput }()

	var capturedEnv []string
	Output = func(c *Cmd) ([]byte, error) {
		capturedEnv = c.Env
		return nil, nil
	}

	env := []string{"CHROOT_VAR=output_env"}
	_, err := ChrootOutput(&ChrootCmd{
		Cmd:       Cmd{Name: "/bin/sh", Env: env},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootOutput: unexpected error: %v", err)
	}
	if len(capturedEnv) != 1 || capturedEnv[0] != "CHROOT_VAR=output_env" {
		t.Errorf("env not passed through ChrootOutput: got %v", capturedEnv)
	}
}

// ---------------------------------------------------------------------------
// chrootArgs
// ---------------------------------------------------------------------------

func TestChrootArgs_Valid(t *testing.T) {
	c := &ChrootCmd{
		Cmd:       Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"chroot", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d", len(args), len(expected))
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_WithDir(t *testing.T) {
	c := &ChrootCmd{
		Cmd:       Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}, Dir: "/work"},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"--wd", "/work",
		"chroot", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d\nargs: %v", len(args), len(expected), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_WithoutDir(t *testing.T) {
	c := &ChrootCmd{
		Cmd:       Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}
	for _, a := range args {
		if a == "--wd" {
			t.Fatal("--wd should not be present when Dir is empty")
		}
	}
}

func TestChrootArgs_CustomChrootExec(t *testing.T) {
	c := &ChrootCmd{
		Cmd:        Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}},
		ChrootExec: "/usr/local/bin/my-chroot",
		ChrootDir:  "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"/usr/local/bin/my-chroot", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d\nargs: %v", len(args), len(expected), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_CustomChrootExecWithDir(t *testing.T) {
	c := &ChrootCmd{
		Cmd:        Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}, Dir: "/work"},
		ChrootExec: "/opt/chroot-init",
		ChrootDir:  "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"--wd", "/work",
		"/opt/chroot-init", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d\nargs: %v", len(args), len(expected), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_NoExtraArgs(t *testing.T) {
	c := &ChrootCmd{
		Cmd:       Cmd{Name: "/bin/sh"},
		ChrootDir: "/chroot",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}
	if args[len(args)-1] != "/bin/sh" {
		t.Errorf("last arg = %q, want %q", args[len(args)-1], "/bin/sh")
	}
}

func TestChrootArgs_EmptyDir(t *testing.T) {
	_, err := chrootArgs(&ChrootCmd{Cmd: Cmd{Name: "/bin/sh"}})
	if err == nil {
		t.Fatal("expected error for empty chrootDir")
	}
}

func TestChrootArgs_EmptyExec(t *testing.T) {
	_, err := chrootArgs(&ChrootCmd{ChrootDir: "/mnt"})
	if err == nil {
		t.Fatal("expected error for empty chrootExec")
	}
}

// ---------------------------------------------------------------------------
// ChrootRun / ChrootOutput – with mocked Run/Output
// ---------------------------------------------------------------------------

func TestChrootRun_DelegatesToRun(t *testing.T) {
	origRun := Run
	defer func() { Run = origRun }()

	var captured struct {
		name string
		args []string
	}
	Run = func(c *Cmd) error {
		captured.name = c.Name
		captured.args = c.Args
		return nil
	}

	err := ChrootRun(&ChrootCmd{
		Cmd: Cmd{
			Name:   "/bin/bash",
			Args:   []string{"-c", "ls"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootRun: unexpected error: %v", err)
	}
	if captured.name != "unshare" {
		t.Errorf("command = %q, want %q", captured.name, "unshare")
	}
	if captured.args[len(captured.args)-1] != "ls" {
		t.Errorf("last arg = %q, want %q", captured.args[len(captured.args)-1], "ls")
	}
}

func TestChrootRun_CustomChrootExec(t *testing.T) {
	origRun := Run
	defer func() { Run = origRun }()

	var captured struct {
		name string
		args []string
	}
	Run = func(c *Cmd) error {
		captured.name = c.Name
		captured.args = c.Args
		return nil
	}

	err := ChrootRun(&ChrootCmd{
		Cmd: Cmd{
			Name:   "/bin/bash",
			Args:   []string{"-c", "ls"},
			Stdout: io.Discard,
			Stderr: io.Discard,
		},
		ChrootExec: "/custom/chroot-exec",
		ChrootDir:  "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootRun: unexpected error: %v", err)
	}
	if captured.name != "unshare" {
		t.Errorf("command = %q, want %q", captured.name, "unshare")
	}
	// Verify custom ChrootExec appears in args instead of default "chroot"
	found := false
	for _, a := range captured.args {
		if a == "/custom/chroot-exec" {
			found = true
		}
		if a == "chroot" {
			t.Error("default 'chroot' should not appear when ChrootExec is set")
		}
	}
	if !found {
		t.Errorf("custom ChrootExec not found in args: %v", captured.args)
	}
}

func TestChrootOutput_CustomChrootExec(t *testing.T) {
	origOutput := Output
	defer func() { Output = origOutput }()

	var capturedArgs []string
	Output = func(c *Cmd) ([]byte, error) {
		capturedArgs = c.Args
		return []byte("ok"), nil
	}

	out, err := ChrootOutput(&ChrootCmd{
		Cmd:        Cmd{Name: "/bin/echo"},
		ChrootExec: "/custom/chroot-exec",
		ChrootDir:  "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootOutput: unexpected error: %v", err)
	}
	if string(out) != "ok" {
		t.Errorf("output = %q, want %q", string(out), "ok")
	}
	// Verify custom ChrootExec appears in args instead of default "chroot"
	found := false
	for _, a := range capturedArgs {
		if a == "/custom/chroot-exec" {
			found = true
		}
		if a == "chroot" {
			t.Error("default 'chroot' should not appear when ChrootExec is set")
		}
	}
	if !found {
		t.Errorf("custom ChrootExec not found in args: %v", capturedArgs)
	}
}

func TestChrootRun_ErrorOnEmptyDir(t *testing.T) {
	err := ChrootRun(&ChrootCmd{
		Cmd: Cmd{Name: "/bin/bash", Stdout: io.Discard, Stderr: io.Discard},
	})
	if err == nil {
		t.Fatal("expected error for empty chrootDir")
	}
}

func TestChrootOutput_DelegatesToOutput(t *testing.T) {
	origOutput := Output
	defer func() { Output = origOutput }()

	Output = func(c *Cmd) ([]byte, error) {
		return []byte("mocked"), nil
	}

	out, err := ChrootOutput(&ChrootCmd{
		Cmd:       Cmd{Name: "/bin/echo"},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootOutput: unexpected error: %v", err)
	}
	if string(out) != "mocked" {
		t.Errorf("output = %q, want %q", string(out), "mocked")
	}
}

func TestChrootOutput_ErrorOnEmptyName(t *testing.T) {
	_, err := ChrootOutput(&ChrootCmd{ChrootDir: "/mnt"})
	if err == nil {
		t.Fatal("expected error for empty Name")
	}
}

// ---------------------------------------------------------------------------
// MockRunner basics
// ---------------------------------------------------------------------------

func TestMockRunner_Success(t *testing.T) {
	mr := NewMockRunner()
	err := mr.Run(&Cmd{Name: "cmd", Args: []string{"a", "b"}, Stdout: io.Discard, Stderr: io.Discard})
	if err != nil {
		t.Fatalf("MockRunner.Run: unexpected error: %v", err)
	}
	if len(mr.Calls) != 1 {
		t.Fatalf("len(Calls) = %d, want 1", len(mr.Calls))
	}
	if mr.Calls[0].Name != "cmd" {
		t.Errorf("Calls[0].Name = %q, want %q", mr.Calls[0].Name, "cmd")
	}
}

func TestMockRunner_FailOnCall(t *testing.T) {
	testErr := errors.New("boom")
	mr := NewMockRunnerFailOnCall(1, testErr)

	// Call 0 succeeds
	if err := mr.Run(&Cmd{Name: "a", Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("call 0: unexpected error: %v", err)
	}
	// Call 1 fails
	if err := mr.Run(&Cmd{Name: "b", Stdout: io.Discard, Stderr: io.Discard}); !errors.Is(err, testErr) {
		t.Fatalf("call 1: got %v, want %v", err, testErr)
	}
	// Call 2 succeeds
	if err := mr.Run(&Cmd{Name: "c", Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		t.Fatalf("call 2: unexpected error: %v", err)
	}
}

func TestMockRunner_OutputWithData(t *testing.T) {
	mr := &MockRunner{
		FailOn: -1,
		OutputData: map[int][]byte{
			0: []byte("first"),
			1: []byte("second"),
		},
	}

	out0, err := mr.Output(&Cmd{Name: "cmd0"})
	if err != nil {
		t.Fatalf("Output call 0: %v", err)
	}
	out1, err := mr.Output(&Cmd{Name: "cmd1"})
	if err != nil {
		t.Fatalf("Output call 1: %v", err)
	}
	if string(out0) != "first" {
		t.Errorf("out0 = %q, want %q", string(out0), "first")
	}
	if string(out1) != "second" {
		t.Errorf("out1 = %q, want %q", string(out1), "second")
	}
}

// ---------------------------------------------------------------------------
// SysProcAttr – chrootArgs --cgroup flag and forwarding
// ---------------------------------------------------------------------------

func TestChrootArgs_WithUseCgroupFD(t *testing.T) {
	c := &ChrootCmd{
		Cmd: Cmd{
			Name:        "/bin/bash",
			Args:        []string{"-c", "ls"},
			SysProcAttr: &syscall.SysProcAttr{UseCgroupFD: true},
		},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"--cgroup",
		"chroot", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d\nargs: %v", len(args), len(expected), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_WithUseCgroupFDAndDir(t *testing.T) {
	c := &ChrootCmd{
		Cmd: Cmd{
			Name:        "/bin/bash",
			Args:        []string{"-c", "ls"},
			Dir:         "/work",
			SysProcAttr: &syscall.SysProcAttr{UseCgroupFD: true},
		},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}

	expected := []string{
		"--pid", "--fork", "--kill-child",
		"--mount", "--uts", "--ipc",
		"--mount-proc=/mnt/root/proc",
		"--cgroup",
		"--wd", "/work",
		"chroot", "/mnt/root", "/bin/bash",
		"-c", "ls",
	}
	if len(args) != len(expected) {
		t.Fatalf("len(args) = %d, want %d\nargs: %v", len(args), len(expected), args)
	}
	for i := range expected {
		if args[i] != expected[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], expected[i])
		}
	}
}

func TestChrootArgs_WithoutUseCgroupFD(t *testing.T) {
	c := &ChrootCmd{
		Cmd: Cmd{
			Name:        "/bin/bash",
			Args:        []string{"-c", "ls"},
			SysProcAttr: &syscall.SysProcAttr{UseCgroupFD: false},
		},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}
	for _, a := range args {
		if a == "--cgroup" {
			t.Fatal("--cgroup should not be present when UseCgroupFD is false")
		}
	}
}

func TestChrootArgs_NilSysProcAttr(t *testing.T) {
	c := &ChrootCmd{
		Cmd:       Cmd{Name: "/bin/bash", Args: []string{"-c", "ls"}},
		ChrootDir: "/mnt/root",
	}
	args, err := chrootArgs(c)
	if err != nil {
		t.Fatalf("chrootArgs: unexpected error: %v", err)
	}
	for _, a := range args {
		if a == "--cgroup" {
			t.Fatal("--cgroup should not be present when SysProcAttr is nil")
		}
	}
}

func TestChrootRun_ForwardsSysProcAttr(t *testing.T) {
	origRun := Run
	defer func() { Run = origRun }()

	var capturedAttr *syscall.SysProcAttr
	Run = func(c *Cmd) error {
		capturedAttr = c.SysProcAttr
		return nil
	}

	spa := &syscall.SysProcAttr{UseCgroupFD: true}
	err := ChrootRun(&ChrootCmd{
		Cmd: Cmd{
			Name:        "/bin/bash",
			Args:        []string{"-c", "ls"},
			Stdout:      io.Discard,
			Stderr:      io.Discard,
			SysProcAttr: spa,
		},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootRun: unexpected error: %v", err)
	}
	if capturedAttr != spa {
		t.Errorf("SysProcAttr not forwarded through ChrootRun: got %v, want %v",
			capturedAttr, spa)
	}
}

func TestChrootOutput_ForwardsSysProcAttr(t *testing.T) {
	origOutput := Output
	defer func() { Output = origOutput }()

	var capturedAttr *syscall.SysProcAttr
	Output = func(c *Cmd) ([]byte, error) {
		capturedAttr = c.SysProcAttr
		return nil, nil
	}

	spa := &syscall.SysProcAttr{UseCgroupFD: true}
	_, err := ChrootOutput(&ChrootCmd{
		Cmd: Cmd{
			Name:        "/bin/echo",
			SysProcAttr: spa,
		},
		ChrootDir: "/mnt",
	})
	if err != nil {
		t.Fatalf("ChrootOutput: unexpected error: %v", err)
	}
	if capturedAttr != spa {
		t.Errorf("SysProcAttr not forwarded through ChrootOutput: got %v, want %v",
			capturedAttr, spa)
	}
}
