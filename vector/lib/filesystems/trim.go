package filesystems

import (
	"fmt"
	"io"
	"os"

	"matrixos/vector/lib/runner"
)

// Fstrim runs "fstrim -v" on the given mount point using the provided runner.
// fstrim discards unused blocks on a mounted filesystem, which improves
// compression ratios for sparse image files.
// Errors from fstrim are returned to the caller (some devices, e.g. USB
// sticks, do not support TRIM and the caller may choose to ignore failures).
func Fstrim(run runner.Func, stdout, stderr io.Writer, mountPoint string) error {
	if mountPoint == "" {
		return fmt.Errorf("missing mount point for fstrim")
	}
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	fmt.Fprintf(stdout, "Executing fstrim on %s\n", mountPoint)
	return run(&runner.Cmd{
		Name:   "fstrim",
		Args:   []string{"-v", mountPoint},
		Stdout: stdout,
		Stderr: stderr,
	})
}

// FstrimAll runs fstrim on every mount point in the supplied list. It logs
// each invocation but ignores individual errors (some filesystems or devices
// do not support TRIM). This is the typical behavior needed when finalizing
// image filesystems before compression.
func FstrimAll(run runner.Func, stdout, stderr io.Writer, mountPoints ...string) {
	for _, mp := range mountPoints {
		// Errors are intentionally ignored – fstrim may fail on USB
		// sticks or other devices that do not support discard.
		_ = Fstrim(run, stdout, stderr, mp)
	}
}
