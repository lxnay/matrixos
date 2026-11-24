package worker

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// InitChrootOptions configures RunInitChroot.
type InitChrootOptions struct {
	DevDir     string
	ChrootDir  string
	RunnerType string // e.g. "imager" or "releaser"
	Name       string
	Args       []string
	Env        []string
	Stdout     io.Writer
	Stderr     io.Writer
	ChrootRun  runner.ChrootRunFunc
}

// RunInitChroot executes opts.Name inside opts.ChrootDir via build/init/init.sh
// and unshare, setting MATRIXOS_DEV_DIR and RUNNER_TYPE in the environment.
func RunInitChroot(opts InitChrootOptions) error {
	initScript := filepath.Join(opts.DevDir, "build", "init", "init.sh")
	if _, err := os.Stat(initScript); os.IsNotExist(err) {
		return fmt.Errorf("init script not found at %s", initScript)
	}

	env := config.FilterEnvKey(opts.Env, "MATRIXOS_DEV_DIR")
	env = config.FilterEnvKey(env, "RUNNER_TYPE")
	env = append(env,
		"MATRIXOS_DEV_DIR="+opts.DevDir,
		"RUNNER_TYPE="+opts.RunnerType,
	)

	run := opts.ChrootRun
	if run == nil {
		run = runner.ChrootRun
	}
	cmd := runner.ChrootCmd{
		Cmd: runner.Cmd{
			Name:   opts.Name,
			Args:   opts.Args,
			Env:    env,
			Stdout: opts.Stdout,
			Stderr: opts.Stderr,
		},
		ChrootExec: initScript,
		ChrootDir:  opts.ChrootDir,
	}
	if err := run(&cmd); err != nil {
		return fmt.Errorf("chrooted %s %s failed: %w", opts.Name, opts.Args, err)
	}
	return nil
}
