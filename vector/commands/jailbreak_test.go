package commands

import (
	"bytes"
	"fmt"
	"io"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/ostree"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// mockCmdRunner is a test-only cmdRunner.
type mockCmdRunner struct {
	runErr    error
	outputVal []byte
	outputErr error
}

func (m *mockCmdRunner) Run() error              { return m.runErr }
func (m *mockCmdRunner) Output() ([]byte, error) { return m.outputVal, m.outputErr }
func (m *mockCmdRunner) SetStdout(_ io.Writer)   {}
func (m *mockCmdRunner) SetStderr(_ io.Writer)   {}

// newTestJailbreakCommand creates a JailbreakCommand with injected mocks,
// bypassing initConfig/initOstree.
func newTestJailbreakCommand(
	ot ostree.IOstree,
	cfg *config.MockConfig,
	runner *jailbreakRunner,
) (*JailbreakCommand, error) {
	return newTestJailbreakCommandWithArgs(ot, cfg, runner, nil)
}

func newTestJailbreakCommandWithArgs(
	ot ostree.IOstree,
	cfg *config.MockConfig,
	runner *jailbreakRunner,
	args []string,
) (*JailbreakCommand, error) {
	cmd := &JailbreakCommand{}
	cmd.ot = ot
	cmd.cfg = cfg
	cmd.StartUI()
	cmd.run = runner
	cmd.prompt = NewPrompter(runner.stdin, runner.stdout, runner.stderr, &cmd.UI)
	if err := cmd.parseArgs(args); err != nil {
		return nil, err
	}
	return cmd, nil
}

// testRunner creates a jailbreakRunner with sensible defaults for testing.
// The caller can override individual fields after creation.
func testRunner() *jailbreakRunner {
	return &jailbreakRunner{
		execCommand: func(name string, args ...string) cmdRunner {
			return &mockCmdRunner{}
		},
		readFile: func(path string) ([]byte, error) {
			return nil, fmt.Errorf("readFile not mocked for %s", path)
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			return nil
		},
		appendFile: func(path string, data []byte) error {
			return nil
		},
		mkdirAll: func(path string, perm os.FileMode) error {
			return nil
		},
		stat: func(path string) (os.FileInfo, error) {
			return nil, fmt.Errorf("stat not mocked for %s", path)
		},
		removeFile: func(path string) error { return nil },
		removeAll:  func(path string) error { return nil },
		rename:     func(src, dst string) error { return nil },
		realpath:   func(path string) (string, error) { return path, nil },
		copyFile:   func(src, dst string) error { return nil },
		getMountInfo: func(mnt string) (*mountInfo, error) {
			return &mountInfo{UUID: "test-uuid", FSType: "ext4"}, nil
		},
		remountRW: func(mnt string) error { return nil },
		stdin:     strings.NewReader("DESTROYALL\n"),
		stdout:    io.Discard,
		stderr:    io.Discard,
	}
}

func defaultTestConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{
			"Ostree.Sysroot":                    {"/sysroot"},
			"Ostree.FullBranchSuffix":           {"full"},
			"Imager.BootRoot":                   {"/boot"},
			"Imager.EfiRoot":                    {"/efi"},
			"Releaser.ReadOnlyVdb":              {"/usr/var-db-pkg"},
			"matrixOS.OsName":                   {"matrixos"},
			"matrixOS.DefaultUsername":          {"matrix"},
			"Jailbreak.BootLoaderEntry":         {"matrixos-jailbroken.conf"},
			"Seeder.DefaultSecureBootPublicKey": {"/etc/matrixos-private/secureboot/keys/db/db.pem"},
		},
	}
}

func defaultTestMockOstree() *ostree.MockOstree {
	return &ostree.MockOstree{
		Deployments: []ostree.Deployment{
			{
				Booted:    true,
				Checksum:  "abc123def456",
				Stateroot: "matrixos",
				Refspec:   "origin:matrixos/amd64/gnome-full",
				Index:     0,
				Serial:    0,
			},
		},
		Refs: []string{
			"origin:matrixos/amd64/gnome-full",
			"origin:matrixos/amd64/server-full",
		},
	}
}

func TestJailbreakName(t *testing.T) {
	cmd := &JailbreakCommand{}
	cmd.parseArgs(nil)
	if cmd.Name() != "jailbreak" {
		t.Fatalf("expected name 'jailbreak', got %q", cmd.Name())
	}
}

func TestJailbreakRequiresRoot(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 1000 }
	defer func() { getEuid = origEuid }()

	runner := testRunner()
	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "must be run as root") {
		t.Fatalf("expected root error, got: %v", err)
	}
}

func TestJailbreakSanityChecksSysrootMissing(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	runner := testRunner()
	// stat fails for sysroot
	runner.stat = func(path string) (os.FileInfo, error) {
		return nil, fmt.Errorf("not found")
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected sysroot-not-found error, got: %v", err)
	}
}

func TestJailbreakSanityChecksNotOnFullBranch(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	// Deployments on a non-full branch.
	mock := &ostree.MockOstree{
		Deployments: []ostree.Deployment{
			{
				Booted:    true,
				Checksum:  "abc123",
				Stateroot: "matrixos",
				Refspec:   "origin:matrixos/amd64/gnome", // no -full suffix
			},
		},
		Refs: []string{"origin:matrixos/amd64/gnome-full"},
	}

	runner := testRunner()
	runner.stat = func(path string) (os.FileInfo, error) {
		// Sysroot exists.
		if path == "/sysroot" || path == "/usr/var-db-pkg" {
			return fakeFileInfo{}, nil
		}
		return nil, fmt.Errorf("not found")
	}

	cmd, err := newTestJailbreakCommand(mock, defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "not on a -full branch") {
		t.Fatalf("expected not-on-full-branch error, got: %v", err)
	}
}

func TestJailbreakSanityChecksAbortOnConfirm(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	runner := testRunner()
	runner.stat = statAllowAll
	runner.stdin = strings.NewReader("NOPE\n")

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("expected aborted error, got: %v", err)
	}
}

func TestJailbreakGenerateFstab(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	var appendedPath string
	var appendedData string

	runner := testRunner()
	runner.stat = statAllowAll
	runner.getMountInfo = func(mnt string) (*mountInfo, error) {
		switch mnt {
		case "/sysroot":
			return &mountInfo{UUID: "root-uuid", FSType: "btrfs"}, nil
		case "/boot":
			return &mountInfo{UUID: "boot-uuid", FSType: "ext4"}, nil
		case "/efi":
			return &mountInfo{UUID: "efi-uuid", FSType: "vfat"}, nil
		}
		return nil, fmt.Errorf("unknown mount %s", mnt)
	}
	runner.appendFile = func(path string, data []byte) error {
		appendedPath = path
		appendedData = string(data)
		return nil
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.generateFstab("/sysroot", "/boot", "/efi")
	if err != nil {
		t.Fatalf("generateFstab failed: %v", err)
	}

	if appendedPath != "/sysroot/etc/fstab" {
		t.Errorf("expected fstab path /sysroot/etc/fstab, got %s", appendedPath)
	}
	if !strings.Contains(appendedData, "UUID=root-uuid / btrfs") {
		t.Errorf("fstab missing root entry: %s", appendedData)
	}
	if !strings.Contains(appendedData, "UUID=boot-uuid /boot ext4") {
		t.Errorf("fstab missing boot entry: %s", appendedData)
	}
	if !strings.Contains(appendedData, "UUID=efi-uuid /efi vfat") {
		t.Errorf("fstab missing efi entry: %s", appendedData)
	}
}

func TestJailbreakBootloaderSetup(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	var writtenPath string
	var writtenData string

	runner := testRunner()
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("BOOT_IMAGE=/vmlinuz-6.12.0 root=UUID=abc rw ostree=/blah quiet splash"), nil
		}
		if path == "/proc/sys/kernel/osrelease" {
			return []byte("6.12.0\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.stat = statAllowAll
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writtenPath = path
		writtenData = string(data)
		return nil
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.bootloaderSetup("/boot")
	if err != nil {
		t.Fatalf("bootloaderSetup failed: %v", err)
	}

	expectedPath := "/boot/loader/entries/matrixos-jailbroken.conf"
	if writtenPath != expectedPath {
		t.Errorf("expected BLS path %s, got %s", expectedPath, writtenPath)
	}

	if !strings.Contains(writtenData, "title matrixOS (Gentoo-based, jailbroken)") {
		t.Errorf("BLS config missing title: %s", writtenData)
	}
	if !strings.Contains(writtenData, "root=UUID=abc") {
		t.Errorf("BLS config should contain root= arg: %s", writtenData)
	}
	if !strings.Contains(writtenData, "quiet") {
		t.Errorf("BLS config should contain quiet arg: %s", writtenData)
	}
	// rw and ostree= should be filtered out.
	if strings.Contains(writtenData, "ostree=") {
		t.Errorf("BLS config should not contain ostree= arg: %s", writtenData)
	}
	// kernel name should be translated from vmlinuz- to kernel-.
	if !strings.Contains(writtenData, "linux /kernel-6.12.0") {
		t.Errorf("BLS config should have translated kernel name: %s", writtenData)
	}
}

func TestJailbreakCleanConfigSetupVarDbPkg(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	var removedPath string
	var renamedFrom, renamedTo string

	runner := testRunner()
	runner.removeAll = func(path string) error {
		removedPath = path
		return nil
	}
	runner.rename = func(src, dst string) error {
		renamedFrom = src
		renamedTo = dst
		return nil
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.cleanConfigSetupVarDbPkg("/sysroot")
	if err != nil {
		t.Fatalf("cleanConfigSetupVarDbPkg failed: %v", err)
	}

	if removedPath != "/sysroot/var/db/pkg" {
		t.Errorf("expected removed /sysroot/var/db/pkg, got %s", removedPath)
	}
	if renamedFrom != "/sysroot/usr/var-db-pkg" {
		t.Errorf("expected rename from /sysroot/usr/var-db-pkg, got %s", renamedFrom)
	}
	if renamedTo != "/sysroot/var/db/pkg" {
		t.Errorf("expected rename to /sysroot/var/db/pkg, got %s", renamedTo)
	}
}

func TestJailbreakCleanConfigSetupBLS(t *testing.T) {
	runner := testRunner()
	var removedFiles []string
	runner.removeFile = func(path string) error {
		removedFiles = append(removedFiles, path)
		return nil
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.cleanConfigSetupBLS("/boot")
	if err != nil {
		t.Fatalf("cleanConfigSetupBLS failed: %v", err)
	}

	expected := []string{
		"/boot/loader/entries/ostree-1.conf",
		"/boot/loader/entries/ostree-2.conf",
	}
	if len(removedFiles) != len(expected) {
		t.Fatalf("expected %d removed files, got %d", len(expected), len(removedFiles))
	}
	for i, f := range expected {
		if removedFiles[i] != f {
			t.Errorf("expected removed file %s, got %s", f, removedFiles[i])
		}
	}
}

func TestJailbreakFullRun(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	tmpDir := t.TempDir()

	runner := testRunner()
	runner.stat = statAllowAll
	runner.stdin = strings.NewReader("DESTROYALL\n")
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("BOOT_IMAGE=/vmlinuz-6.12.0 root=UUID=abc rw quiet"), nil
		}
		if path == "/proc/sys/kernel/osrelease" {
			return []byte("6.12.0\n"), nil
		}
		// repos.conf
		reposConf := filepath.Join("/sysroot", "etc", "portage", "repos.conf", "eselect-repo.conf")
		if path == reposConf {
			return []byte("[myoverlay]\nsync-type = git\nsync-uri = https://example.com/repo.git\nlocation = /var/db/repos/myoverlay\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		dir := filepath.Dir(path)
		os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		return os.WriteFile(filepath.Join(tmpDir, path), data, perm)
	}
	runner.appendFile = func(path string, data []byte) error {
		dir := filepath.Dir(path)
		os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		f, err := os.OpenFile(filepath.Join(tmpDir, path), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(data)
		return err
	}
	runner.getMountInfo = func(mnt string) (*mountInfo, error) {
		return &mountInfo{UUID: "test-uuid-" + mnt, FSType: "ext4"}, nil
	}

	var stdout bytes.Buffer
	runner.stdout = &stdout

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "All done") {
		t.Errorf("expected 'All done' in output, got: %s", output)
	}
}

func TestJailbreakCleanConfigFixSrvDanglingSymlink(t *testing.T) {
	runner := testRunner()
	var createdDirs []string
	var writtenFiles []string

	runner.stat = func(path string) (os.FileInfo, error) {
		// Simulate a dangling symlink (stat fails with not-found).
		return nil, fmt.Errorf("not found: %s", path)
	}
	runner.mkdirAll = func(path string, perm os.FileMode) error {
		createdDirs = append(createdDirs, path)
		return nil
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writtenFiles = append(writtenFiles, path)
		return nil
	}

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	err = cmd.cleanConfigFixSrv("/sysroot")
	if err != nil {
		t.Fatalf("cleanConfigFixSrv failed: %v", err)
	}

	if len(createdDirs) == 0 || createdDirs[0] != "/sysroot/srv" {
		t.Errorf("expected /sysroot/srv to be created, got %v", createdDirs)
	}
	if len(writtenFiles) == 0 || writtenFiles[0] != "/sysroot/srv/.keep" {
		t.Errorf("expected /sysroot/srv/.keep to be created, got %v", writtenFiles)
	}
}

func TestJailbreakCloneToSysrootCopiesVar(t *testing.T) {
	runner := testRunner()
	runner.stat = statAllowAll

	var cmdsRun []string
	runner.execCommand = func(name string, args ...string) cmdRunner {
		full := name
		if len(args) > 0 {
			full = name + " " + strings.Join(args, " ")
		}
		cmdsRun = append(cmdsRun, full)
		return &mockCmdRunner{}
	}

	cfg := defaultTestConfig()
	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), cfg, runner)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := cmd.cloneToSysroot("/sysroot"); err != nil {
		t.Fatalf("cloneToSysroot failed: %v", err)
	}

	// Verify that a cpio command targeting /sysroot/var was issued.
	found := false
	for _, c := range cmdsRun {
		if strings.Contains(c, "/ostree/deploy/matrixos/var") &&
			strings.Contains(c, "cpio") &&
			strings.Contains(c, "/sysroot/var") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected a cpio command copying /ostree/deploy/matrixos/var to /sysroot/var, got commands:\n%s",
			strings.Join(cmdsRun, "\n"))
	}
}

func TestJailbreakCloneToSysrootSkipsVarWhenMissing(t *testing.T) {
	runner := testRunner()
	runner.stat = func(path string) (os.FileInfo, error) {
		// Return error only for the var dir, success for everything else.
		if path == "/ostree/deploy/matrixos/var" {
			return nil, fmt.Errorf("not found")
		}
		return fakeFileInfo{}, nil
	}

	var cmdsRun []string
	runner.execCommand = func(name string, args ...string) cmdRunner {
		full := name
		if len(args) > 0 {
			full = name + " " + strings.Join(args, " ")
		}
		cmdsRun = append(cmdsRun, full)
		return &mockCmdRunner{}
	}

	cfg := defaultTestConfig()
	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), cfg, runner)
	if err != nil {
		t.Fatalf("setup failed: %v", err)
	}

	if err := cmd.cloneToSysroot("/sysroot"); err != nil {
		t.Fatalf("cloneToSysroot failed: %v", err)
	}

	// No cpio command should reference /ostree/deploy/matrixos/var.
	for _, c := range cmdsRun {
		if strings.Contains(c, "/ostree/deploy/matrixos/var") {
			t.Errorf("expected no cpio for /var when dir is missing, but got: %s", c)
		}
	}
}

func TestJailbreakPrintTitle(t *testing.T) {
	runner := testRunner()
	var stderr bytes.Buffer
	runner.stderr = &stderr

	cmd, err := newTestJailbreakCommand(defaultTestMockOstree(), defaultTestConfig(), runner)
	if err != nil {
		t.Fatalf("newTestJailbreakCommand failed: %v", err)
	}

	cmd.printTitle("full")

	output := stderr.String()
	if !strings.Contains(output, "JAILBREAKING") {
		t.Errorf("expected JAILBREAKING in title, got: %s", output)
	}
	if !strings.Contains(output, "vector branch switch matrixos/<your branch>-full") {
		t.Errorf("expected switch instruction in title, got: %s", output)
	}
}

func TestJailbreakSkipDestroyConfirmationFlag(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	tmpDir := t.TempDir()

	runner := testRunner()
	runner.stat = statAllowAll
	// Provide empty stdin — if confirmDestroy is called it will fail.
	runner.stdin = strings.NewReader("")
	runner.readFile = func(path string) ([]byte, error) {
		if path == "/proc/cmdline" {
			return []byte("BOOT_IMAGE=/vmlinuz-6.12.0 root=UUID=abc rw quiet"), nil
		}
		if path == "/proc/sys/kernel/osrelease" {
			return []byte("6.12.0\n"), nil
		}
		return nil, fmt.Errorf("not mocked: %s", path)
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		dir := filepath.Dir(path)
		os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		return os.WriteFile(filepath.Join(tmpDir, path), data, perm)
	}
	runner.appendFile = func(path string, data []byte) error {
		dir := filepath.Dir(path)
		os.MkdirAll(filepath.Join(tmpDir, dir), 0755)
		f, err := os.OpenFile(filepath.Join(tmpDir, path), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = f.Write(data)
		return err
	}
	runner.getMountInfo = func(mnt string) (*mountInfo, error) {
		return &mountInfo{UUID: "test-uuid-" + mnt, FSType: "ext4"}, nil
	}

	var stdout bytes.Buffer
	runner.stdout = &stdout

	cmd, err := newTestJailbreakCommandWithArgs(
		defaultTestMockOstree(), defaultTestConfig(), runner,
		[]string{"--yolo-skip-destroy-confirmation"},
	)
	if err != nil {
		t.Fatalf("newTestJailbreakCommandWithArgs failed: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run should succeed with --yolo-skip-destroy-confirmation, got: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "All done") {
		t.Errorf("expected 'All done' in output, got: %s", output)
	}
}

func TestJailbreakSkipDestroyConfirmationFlagNotSet(t *testing.T) {
	origEuid := getEuid
	getEuid = func() int { return 0 }
	defer func() { getEuid = origEuid }()

	runner := testRunner()
	runner.stat = statAllowAll
	// Provide wrong input so confirmDestroy rejects.
	runner.stdin = strings.NewReader("NO\n")

	cmd, err := newTestJailbreakCommandWithArgs(
		defaultTestMockOstree(), defaultTestConfig(), runner,
		nil, // no flags
	)
	if err != nil {
		t.Fatalf("newTestJailbreakCommandWithArgs failed: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "aborted") {
		t.Fatalf("expected aborted error without skip flag, got: %v", err)
	}
}

func TestJailbreakParseArgsSkipDestroyConfirmation(t *testing.T) {
	cmd := &JailbreakCommand{}
	if err := cmd.parseArgs([]string{"--yolo-skip-destroy-confirmation"}); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if !cmd.yoloSkipDestroyConfirm {
		t.Error("expected yoloSkipDestroyConfirm to be true")
	}
}

func TestJailbreakParseArgsSkipDestroyConfirmationDefault(t *testing.T) {
	cmd := &JailbreakCommand{}
	if err := cmd.parseArgs(nil); err != nil {
		t.Fatalf("parseArgs failed: %v", err)
	}
	if cmd.yoloSkipDestroyConfirm {
		t.Error("expected yoloSkipDestroyConfirm to be false by default")
	}
}

// -- helpers --

// fakeFileInfo is a minimal os.FileInfo for tests.
type fakeFileInfo struct{}

func (fakeFileInfo) Name() string       { return "" }
func (fakeFileInfo) Size() int64        { return 0 }
func (fakeFileInfo) Mode() os.FileMode  { return 0755 }
func (fakeFileInfo) IsDir() bool        { return true }
func (fakeFileInfo) Sys() interface{}   { return nil }
func (fakeFileInfo) ModTime() time.Time { return time.Time{} }

// statAllowAll is a stat mock that returns success for any path.
var statAllowAll = func(path string) (os.FileInfo, error) {
	return fakeFileInfo{}, nil
}
