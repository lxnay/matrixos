package commands

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
)

func TestJailbreakCleanConfigFixSrv_RealDanglingSymlink(t *testing.T) {
	sysroot := t.TempDir()
	srv := filepath.Join(sysroot, "srv")
	if err := os.Symlink("/nonexistent/ostree-srv-target", srv); err != nil {
		t.Fatal(err)
	}

	r := defaultRunner()
	r.stdout = io.Discard
	r.stderr = io.Discard

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), r)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand: %v", err)
	}
	if err := cmd.cleanConfigFixSrv(sysroot); err != nil {
		t.Fatalf("cleanConfigFixSrv failed on dangling symlink: %v", err)
	}
	info, err := os.Lstat(srv)
	if err != nil {
		t.Fatalf("Lstat(%s): %v", srv, err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("expected %s to be a real directory, got mode=%v", srv, info.Mode())
	}
	if _, err := os.Stat(filepath.Join(srv, ".keep")); err != nil {
		t.Fatalf("expected %s/.keep to exist: %v", srv, err)
	}
}

func TestSetupOSChangeLuksPassword_BoundedRetries(t *testing.T) {
	runner := testSetupOSRunner()
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("root=UUID=abc rd.luks.uuid=my-luks-uuid rw"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.fileExists = func(path string) bool {
		return path == "/dev/disk/by-uuid/my-luks-uuid"
	}

	const failLimit = 20
	attempts := 0
	runner.execCommand = func(name string, args ...string) cmdRunner {
		if name == "cryptsetup" {
			attempts++
			if attempts > failLimit {
				// Prevent an infinite loop from hanging the test suite when reproducing.
				return &mockCmdRunner{}
			}
			return &mockCmdRunner{runErr: errors.New("cryptsetup EOF")}
		}
		return &mockCmdRunner{}
	}

	cmd := initSetupOSCmd(runner)
	err := cmd.changeLuksPassword()
	if err == nil {
		t.Fatalf("expected error after bounded retries, got nil (attempts=%d)", attempts)
	}
	if attempts > 10 {
		t.Fatalf("changeLuksPassword retried %d times (unbounded loop)", attempts)
	}
}

func TestSetupOSCheckPartitionForWindows_CleansUpUniqueTempDir(t *testing.T) {
	runner := testSetupOSRunner()
	tmpRoot := t.TempDir()
	runner.mkdirTemp = func(_, pattern string) (string, error) {
		return os.MkdirTemp(tmpRoot, pattern)
	}
	var mountedTarget string
	var unmountedTarget string
	var removedPaths []string

	runner.execCommand = func(name string, args ...string) cmdRunner {
		switch name {
		case "lsblk":
			return &mockCmdRunner{outputVal: []byte("")}
		case "mount":
			if len(args) >= 4 {
				mountedTarget = args[3]
			}
			return &mockCmdRunner{}
		case "umount":
			if len(args) >= 1 {
				unmountedTarget = args[0]
			}
			return &mockCmdRunner{}
		}
		return &mockCmdRunner{}
	}
	runner.removeFile = func(path string) error {
		removedPaths = append(removedPaths, path)
		return os.Remove(path)
	}

	cmd := initSetupOSCmd(runner)
	if err := cmd.checkPartitionForWindows("/dev/sda1", "/efi/grub.cfg", "/EFI/Microsoft/Boot/bootmgfw.efi"); err != nil {
		t.Fatalf("checkPartitionForWindows: %v", err)
	}
	if mountedTarget == "" {
		t.Fatal("expected mount to be called with a temp mountpoint")
	}
	if mountedTarget == "/tmp/setupos-win-sda1" {
		t.Errorf("checkPartitionForWindows used predictable fixed path %q instead of randomized temp dir", mountedTarget)
	}
	if unmountedTarget != mountedTarget {
		t.Errorf("umount target = %q, want %q", unmountedTarget, mountedTarget)
	}
	foundRemoved := false
	for _, p := range removedPaths {
		if p == mountedTarget {
			foundRemoved = true
			break
		}
	}
	if !foundRemoved {
		t.Errorf("temp mount dir %q was never removed after umount (removed=%v)", mountedTarget, removedPaths)
	}
}

func TestResolveRefRemote_WarningLogsOriginalRef(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Remote": {"origin"},
		},
	}
	cmd := &BaseCommand{
		cfg: cfg,
		ot:  &ostree.MockOstree{Remote_: "origin"},
	}

	var buf bytes.Buffer
	warnf := func(format string, args ...any) {
		fmt.Fprintf(&buf, format, args...)
	}

	_, err := cmd.resolveRefRemote("custom:matrixos/x86_64/dev/gnome", warnf)
	if err != nil {
		t.Fatalf("resolveRefRemote: %v", err)
	}
	if !strings.Contains(buf.String(), "custom:matrixos/x86_64/dev/gnome") {
		t.Errorf("warning did not include original remoted ref; got: %q", buf.String())
	}
}
