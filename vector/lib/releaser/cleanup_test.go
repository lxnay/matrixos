package releaser

import (
	"bytes"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// newTestReleaser builds a Releaser with mock dependencies for unit tests.
// It bypasses NewReleaser to avoid validation side-effects.
func newTestReleaser() *Releaser {
	r := &Releaser{
		ReleaserConfig: &ReleaserConfig{cfg: &config.MockConfig{Items: map[string][]string{
			"Seeder.ChrootBuildArtifactsDir":      {"/build-artifacts"},
			"Seeder.ChrootPreppersPhasesStateDir": {"/preppers-state"},
		}, Bools: map[string]bool{}}},
		ostree:       &ostree.MockOstree{},
		runner:       runner.Run,
		chrootRunner: runner.ChrootRunFunc(func(c *runner.ChrootCmd) error { return nil }),
	}
	r.SetStdout(&bytes.Buffer{})
	r.SetStderr(&bytes.Buffer{})
	return r
}

// mockMountSyscalls replaces filesystems.Mount/Unmount
// with no-op fakes so tests never perform real bind mounts.
// Originals are restored via t.Cleanup.
func mockMountSyscalls(t *testing.T) {
	t.Helper()

	origMount := filesystems.Mount
	origUnmount := filesystems.Unmount

	filesystems.Mount = func(source, target, fstype string, flags uintptr, data string) error {
		return nil
	}
	filesystems.Unmount = func(target string, flags int) error {
		return nil
	}

	t.Cleanup(func() {
		filesystems.Mount = origMount
		filesystems.Unmount = origUnmount
	})
}

func TestCleanup_Idempotent(t *testing.T) {
	r := newTestReleaser()
	r.Cleanup()
	r.Cleanup() // must not panic
}
