// Package runner provides a shared command execution abstraction for running
// external programs, plus test helpers (MockRunner) for unit testing.
package runner

import (
	"fmt"
	"io"
	"os/exec"
	"syscall"
)

// Cmd describes an external command to execute. Zero-value fields are
// treated as "inherit from parent" or "not set":
//   - Dir=""      → inherit working directory
//   - Env=nil     → inherit environment
//   - Stdin=nil   → no standard input
//   - Stdout=nil  → output discarded
//   - Stderr=nil  → output discarded
type Cmd struct {
	Name        string
	Args        []string
	Dir         string   // working directory; empty inherits parent
	Env         []string // environment; nil inherits parent
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	SysProcAttr *syscall.SysProcAttr // optional process attributes (e.g. UseCgroupFD)
}

// Func is the canonical function type for executing an external command.
// Consumers store a value of this type and call it to run shell commands;
// tests replace it with MockRunner.Run (or a custom closure) to avoid
// real process execution.
type Func func(cmd *Cmd) error

// OutputFunc is a function type that executes an external command and
// returns its standard output. It mirrors the (*exec.Cmd).Output() pattern.
// Tests can replace the default with a mock to avoid real process execution.
type OutputFunc func(cmd *Cmd) ([]byte, error)

func buildExecCmd(c *Cmd) *exec.Cmd {
	cmd := exec.Command(c.Name, c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env
	cmd.Stdin = c.Stdin
	cmd.SysProcAttr = c.SysProcAttr
	return cmd
}

// Run is the default Func implementation. It executes the named program
// with the given arguments, wiring all Cmd fields to the underlying
// exec.Cmd.
var Run Func = func(c *Cmd) error {
	cmd := buildExecCmd(c)
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	return cmd.Run()
}

// Output is the default OutputFunc implementation. It executes the named
// program and returns its standard output, mirroring (*exec.Cmd).Output().
var Output OutputFunc = func(c *Cmd) ([]byte, error) {
	return buildExecCmd(c).Output()
}

// chrootArgs builds the unshare argument list for running a command inside
// a chroot. It preserves the exact invocation pattern:
//
//	unshare --pid --fork --kill-child --mount --uts --ipc --wd=<Dir> \
//	    --mount-proc=<chrootDir>/proc chroot <chrootDir> <chrootExec> [args...]
func chrootArgs(c *ChrootCmd) ([]string, error) {
	if c.ChrootDir == "" {
		return nil, fmt.Errorf("missing chrootDir parameter")
	}
	if c.Name == "" {
		return nil, fmt.Errorf("missing chrootExec parameter")
	}

	var dirArgs []string
	if c.Dir != "" {
		dirArgs = []string{"--wd", c.Dir}
	}

	unshareArgs := []string{
		"--pid",
		"--fork",
		"--kill-child",
		"--mount",
		"--uts",
		"--ipc",
		fmt.Sprintf("--mount-proc=%s/proc", c.ChrootDir),
	}
	if c.SysProcAttr != nil && c.SysProcAttr.UseCgroupFD {
		unshareArgs = append(unshareArgs, "--cgroup")
	}
	unshareArgs = append(unshareArgs, dirArgs...)

	chrootExec := c.ChrootExec
	if chrootExec == "" {
		chrootExec = "chroot"
	}

	execArgs := []string{
		chrootExec,
		c.ChrootDir,
		c.Name,
	}

	var cmdArgs []string
	cmdArgs = append(cmdArgs, unshareArgs...)
	cmdArgs = append(cmdArgs, execArgs...)
	cmdArgs = append(cmdArgs, c.Args...)
	return cmdArgs, nil
}

// ChrootCmd describes a command to execute inside a chroot via unshare.
// It embeds Cmd (where Name is the executable to run inside the chroot)
// and adds the chroot-specific ChrootDir field. The unshare argument
// list is built automatically by the default ChrootRun / ChrootOutput
// implementations.
type ChrootCmd struct {
	Cmd
	// ChrootExec is the executable to run to effectively "chroot".
	// The dumbest possible executable is just "chroot", which is also
	// the default for ChrootCmd if ChrootExec is not set.
	// More complex executables can be specified such that you can inject
	// a custom init logic (e.g. a real init system, the mounting of additional
	// filesystems, etc.) or just to interpose some custom commands before
	// calling exec and leaving the chroot.sh script executed inside the chroot
	// as PID 1. In this case, the chroot.sh script would be responsible for executing
	// the actual command specified in the ChrootCmd.Name field.
	// In the context of this toolkit, this is used mainly for bind mounting additional
	// filesystems privately inside the chroot, leveraging mount namespaces isolated
	// via unshare, such that we avoid leaking mounts to the host, especially when bad
	// things happen inside the chroot and we need to clean up mounts on exit (e.g
	// due to a SIGKILL).
	ChrootExec string
	// ChrootDir is the root directory for the chroot.
	ChrootDir string
}

// ChrootRunFunc is a function type that executes a command inside a chroot
// via unshare, wiring stdin/stdout/stderr to the supplied writers.
type ChrootRunFunc func(cmd *ChrootCmd) error

// ChrootOutputFunc is a function type that executes a command inside a chroot
// via unshare and returns its standard output.
type ChrootOutputFunc func(cmd *ChrootCmd) ([]byte, error)

// ChrootRun is the default ChrootRunFunc implementation.
var ChrootRun ChrootRunFunc = func(c *ChrootCmd) error {
	uArgs, err := chrootArgs(c)
	if err != nil {
		return err
	}
	return Run(&Cmd{
		Name:        "unshare",
		Args:        uArgs,
		Env:         c.Env,
		Stdin:       c.Stdin,
		Stdout:      c.Stdout,
		Stderr:      c.Stderr,
		SysProcAttr: c.SysProcAttr,
	})
}

// ChrootOutput is the default ChrootOutputFunc implementation.
var ChrootOutput ChrootOutputFunc = func(c *ChrootCmd) ([]byte, error) {
	uArgs, err := chrootArgs(c)
	if err != nil {
		return nil, err
	}
	return Output(&Cmd{
		Name:        "unshare",
		Args:        uArgs,
		Env:         c.Env,
		Stdin:       c.Stdin,
		SysProcAttr: c.SysProcAttr,
	})
}
