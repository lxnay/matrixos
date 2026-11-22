package filesystems

import (
	"fmt"
	"matrixos/vector/lib/runner"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func mkPI(path, typ string, perms uint32, uid, gid, size uint64, link string) PathInfo {
	return PathInfo{
		Mode: &PathMode{Type: typ, Perms: os.FileMode(perms)},
		Uid:  uid, Gid: gid, Size: size,
		Path: path, Link: link,
	}
}

func TestPathInfoMetaEqual(t *testing.T) {
	a := mkPI("/usr/etc/foo", "-", 0644, 0, 0, 100, "")
	b := mkPI("/etc/foo", "-", 0644, 0, 0, 100, "")
	if !a.Equals(&b) {
		t.Error("Expected equal (path is not compared)")
	}

	// Different perms
	c := mkPI("/etc/foo", "-", 0755, 0, 0, 100, "")
	if a.Equals(&c) {
		t.Error("Expected not equal (different perms)")
	}

	// Different size
	d := mkPI("/etc/foo", "-", 0644, 0, 0, 200, "")
	if a.Equals(&d) {
		t.Error("Expected not equal (different size)")
	}

	// Different type
	e := mkPI("/etc/foo", "l", 0644, 0, 0, 100, "/bar")
	if a.Equals(&e) {
		t.Error("Expected not equal (different type)")
	}

	// Symlinks with different targets
	f := mkPI("/etc/link", "l", 0777, 0, 0, 0, "target_a")
	g := mkPI("/etc/link", "l", 0777, 0, 0, 0, "target_b")
	if f.Equals(&g) {
		t.Error("Expected not equal (different link target)")
	}
}

// fakeExecCommand mocks exec.Command for testing purposes.
func fakeExecCommand(command string, args ...string) *exec.Cmd {
	cs := []string{"-test.run=TestHelperProcess", "--", command}
	cs = append(cs, args...)
	cmd := exec.Command(os.Args[0], cs...)
	cmd.Env = []string{"GO_WANT_HELPER_PROCESS=1"}
	// Pass through specific environment variables for controlling mock behavior
	for _, env := range os.Environ() {
		if strings.HasPrefix(env, "MOCK_") {
			cmd.Env = append(cmd.Env, env)
		}
	}
	return cmd
}

// fakeExecRun wraps fakeExecCommand to implement runner.Func.
func fakeExecRun(c *runner.Cmd) error {
	cmd := fakeExecCommand(c.Name, c.Args...)
	cmd.Stdin = c.Stdin
	cmd.Stdout = c.Stdout
	cmd.Stderr = c.Stderr
	return cmd.Run()
}

// fakeExecOutput wraps fakeExecCommand to implement runner.OutputFunc.
func fakeExecOutput(c *runner.Cmd) ([]byte, error) {
	return fakeExecCommand(c.Name, c.Args...).Output()
}

// fakeChrootOutput wraps fakeExecCommand to implement runner.ChrootOutputFunc.
func fakeChrootOutput(c *runner.ChrootCmd) ([]byte, error) {
	if c.ChrootDir == "" {
		return nil, fmt.Errorf("missing chrootDir parameter")
	}
	if c.Name == "" {
		return nil, fmt.Errorf("missing Name parameter")
	}
	cmdArgs := []string{
		"--pid", "--fork", "--kill-child", "--mount", "--uts", "--ipc",
		fmt.Sprintf("--mount-proc=%s/proc", c.ChrootDir),
		"chroot", c.ChrootDir, c.Name,
	}
	cmdArgs = append(cmdArgs, c.Args...)
	return fakeExecOutput(&runner.Cmd{Name: "unshare", Args: cmdArgs})
}

// setupMockExec swaps all execution vars with fakes and registers cleanup.
func setupMockExec(t *testing.T) {
	origExecRun := execRun
	origChrootOutput := ExecChrootOutput

	execRun = fakeExecRun
	ExecChrootOutput = fakeChrootOutput

	t.Cleanup(func() {
		execRun = origExecRun
		ExecChrootOutput = origChrootOutput
	})
}

func setupMockSyscalls(t *testing.T) {
	origMount := Mount
	origUnmount := Unmount
	origIoctl := sysIoctl

	Mount = func(source string, target string, fstype string, flags uintptr, data string) error {
		if os.Getenv("MOCK_MOUNT_FAIL") == "1" {
			return fmt.Errorf("mock mount failed")
		}
		return nil
	}
	Unmount = func(target string, flags int) error {
		if os.Getenv("MOCK_UMOUNT_FAIL") == "1" {
			return fmt.Errorf("mock unmount failed")
		}
		return nil
	}
	sysIoctl = func(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err unix.Errno) {
		return 0, 0, 0
	}

	t.Cleanup(func() {
		Mount = origMount
		Unmount = origUnmount
		sysIoctl = origIoctl
	})
}

// setupMockXattr swaps xattr syscall vars with an in-memory store.
func setupMockXattr(t *testing.T) {
	origLsetxattr := sysLsetxattr
	origLgetxattr := sysLgetxattr
	origLlistxattr := sysLlistxattr

	xattrs := make(map[string]map[string][]byte)

	sysLsetxattr = func(path, attr string, data []byte, flags int) error {
		if _, ok := xattrs[path]; !ok {
			xattrs[path] = make(map[string][]byte)
		}
		val := make([]byte, len(data))
		copy(val, data)
		xattrs[path][attr] = val
		return nil
	}

	sysLgetxattr = func(path, attr string, dest []byte) (int, error) {
		fileAttrs, ok := xattrs[path]
		if !ok {
			return 0, unix.ENODATA
		}
		val, ok := fileAttrs[attr]
		if !ok {
			return 0, unix.ENODATA
		}
		if dest == nil {
			return len(val), nil
		}
		return copy(dest, val), nil
	}

	sysLlistxattr = func(path string, dest []byte) (int, error) {
		fileAttrs, ok := xattrs[path]
		if !ok {
			return 0, nil
		}
		var packed []byte
		for name := range fileAttrs {
			packed = append(packed, []byte(name)...)
			packed = append(packed, 0)
		}
		if dest == nil {
			return len(packed), nil
		}
		return copy(dest, packed), nil
	}

	t.Cleanup(func() {
		sysLsetxattr = origLsetxattr
		sysLgetxattr = origLgetxattr
		sysLlistxattr = origLlistxattr
	})
}

// TestHelperProcess is the mock process that runs instead of the real commands.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 {
		if args[0] == "--" {
			args = args[1:]
			break
		}
		args = args[1:]
	}
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "No command\n")
		os.Exit(2)
	}

	cmd, args := args[0], args[1:]
	switch cmd {
	case "cryptsetup":
		if os.Getenv("MOCK_CRYPTSETUP_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "cryptsetup failed")
			os.Exit(1)
		}
	case "udevadm", "blockdev":
		// No-op success
	case "unshare":
		if os.Getenv("MOCK_UNSHARE_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "unshare failed")
			os.Exit(1)
		}
	default:
		// Pass for other commands
	}
	os.Exit(0)
}

func TestDeviceUUID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		setupMockBlkid(t, map[string]string{
			"/dev/sda1:UUID": "1234-5678",
		})

		uuid, err := DeviceUUID("/dev/sda1")
		if err != nil {
			t.Fatalf("DeviceUUID failed: %v", err)
		}
		if uuid != "1234-5678" {
			t.Errorf("Expected UUID 1234-5678, got %s", uuid)
		}
	})

	t.Run("NoDevPath", func(t *testing.T) {
		_, err := DeviceUUID("")
		if err == nil {
			t.Error("Expected error for missing devPath, got nil")
		}
	})

	t.Run("DeviceNotFound", func(t *testing.T) {
		setupMockBlkidFail(t)
		_, err := DeviceUUID("/dev/sda1")
		if err == nil {
			t.Error("Expected error for device not found in by-uuid, got nil")
		}
	})

	t.Run("NonexistentDevice", func(t *testing.T) {
		setupMockBlkidFail(t)
		_, err := DeviceUUID("/dev/nonexistent_device_xyz")
		if err == nil {
			t.Error("Expected error for nonexistent device path, got nil")
		}
	})
}

func TestDevicePartUUID(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		setupMockBlkid(t, map[string]string{
			"/dev/sda1:PARTUUID": "abcdef-01",
		})

		partuuid, err := DevicePartUUID("/dev/sda1")
		if err != nil {
			t.Fatalf("DevicePartUUID failed: %v", err)
		}
		if partuuid != "abcdef-01" {
			t.Errorf("Expected PARTUUID abcdef-01, got %s", partuuid)
		}
	})

	t.Run("NoDevPath", func(t *testing.T) {
		_, err := DevicePartUUID("")
		if err == nil {
			t.Error("Expected error for missing devPath, got nil")
		}
	})

	t.Run("DeviceNotFound", func(t *testing.T) {
		setupMockBlkidFail(t)
		_, err := DevicePartUUID("/dev/sda1")
		if err == nil {
			t.Error("Expected error for device not found in by-partuuid, got nil")
		}
	})

	t.Run("NonexistentDevice", func(t *testing.T) {
		setupMockBlkidFail(t)
		_, err := DevicePartUUID("/dev/nonexistent_device_xyz")
		if err == nil {
			t.Error("Expected error for nonexistent device path, got nil")
		}
	})
}

func TestGetLuksRootfsDevicePath(t *testing.T) {
	tests := []struct {
		name     string
		luksName string
		want     string
		wantErr  bool
	}{
		{"Valid", "mycrypt", "/dev/mapper/mycrypt", false},
		{"Empty", "", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetLuksRootfsDevicePath(tt.luksName)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetLuksRootfsDevicePath() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("GetLuksRootfsDevicePath() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTempDirAndFileOperations(t *testing.T) {
	tmpDir, err := CreateTempDir(os.TempDir(), "test-fs-")
	if err != nil {
		t.Fatalf("CreateTempDir failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	info, err := os.Stat(tmpDir)
	if err != nil || !info.IsDir() {
		t.Errorf("CreateTempDir did not create a directory")
	}

	tmpFile, err := CreateTempFile(tmpDir, "test-file-")
	if err != nil {
		t.Fatalf("CreateTempFile failed: %v", err)
	}
	tmpFile.Close()

	if _, err := os.Stat(tmpFile.Name()); os.IsNotExist(err) {
		t.Errorf("CreateTempFile did not create a file")
	}

	isEmpty, err := DirEmpty(tmpDir)
	if err != nil {
		t.Errorf("DirEmpty failed: %v", err)
	}
	if isEmpty {
		t.Errorf("DirEmpty returned true for non-empty dir")
	}

	pattern := filepath.Join(tmpDir, "test-file-*")
	if err := RemoveFileWithGlob(pattern, RemoveFileWithGlobOptions{}); err != nil {
		t.Errorf("RemoveFileWithGlob failed: %v", err)
	}

	if _, err := os.Stat(tmpFile.Name()); !os.IsNotExist(err) {
		t.Errorf("RemoveFileWithGlob did not remove the file")
	}

	isEmpty, err = DirEmpty(tmpDir)
	if err != nil {
		t.Errorf("DirEmpty failed: %v", err)
	}
	if !isEmpty {
		t.Errorf("DirEmpty returned false for empty dir")
	}

	if _, err := os.Create(filepath.Join(tmpDir, "another")); err != nil {
		t.Fatal(err)
	}
	if err := EmptyDir(tmpDir, EmptyDirOptions{}); err != nil {
		t.Errorf("EmptyDir failed: %v", err)
	}
	isEmpty, _ = DirEmpty(tmpDir)
	if !isEmpty {
		t.Errorf("EmptyDir did not empty the directory")
	}

	if err := RemoveDir(tmpDir, RemoveDirOptions{}); err != nil {
		t.Errorf("RemoveDir failed: %v", err)
	}
	if _, err := os.Stat(tmpDir); !os.IsNotExist(err) {
		t.Errorf("RemoveDir did not remove the directory")
	}
}

func TestCheckDirIsRoot(t *testing.T) {
	err := CheckDirIsRoot("/")
	if err == nil {
		t.Error("CheckDirIsRoot(/) should fail")
	}

	tmpDir := t.TempDir()
	err = CheckDirIsRoot(tmpDir)
	if err != nil {
		t.Errorf("CheckDirIsRoot(tmpDir) failed: %v", err)
	}
}

func TestDevicesSettle(t *testing.T) {
	setupMockExec(t)
	// Simple execution test to ensure it runs without error
	DevicesSettle()
}

func TestFlushBlockDeviceBuffers(t *testing.T) {
	setupMockExec(t)
	setupMockSyscalls(t)

	t.Run("Success", func(t *testing.T) {
		f, err := os.CreateTemp(t.TempDir(), "blockdev")
		if err != nil {
			t.Fatal(err)
		}
		f.Close()
		if err := FlushBlockDeviceBuffers(f.Name()); err != nil {
			t.Errorf("FlushBlockDeviceBuffers failed: %v", err)
		}
	})

	t.Run("NoDevPath", func(t *testing.T) {
		if err := FlushBlockDeviceBuffers(""); err == nil {
			t.Error("Expected error for missing devPath, got nil")
		}
	})
}

func TestChrootOutput(t *testing.T) {
	setupMockExec(t)

	t.Run("Success", func(t *testing.T) {
		out, err := ChrootOutput("/target", "/bin/sh", "-c", "echo hello")
		if err != nil {
			t.Fatalf("ChrootOutput failed: %v", err)
		}
		// The mock unshare handler exits 0 with no output by default
		_ = out
	})

	t.Run("MissingChrootDir", func(t *testing.T) {
		_, err := ChrootOutput("", "/bin/sh")
		if err == nil {
			t.Error("Expected error for missing chrootDir, got nil")
		}
	})

	t.Run("MissingChrootExec", func(t *testing.T) {
		_, err := ChrootOutput("/target", "")
		if err == nil {
			t.Error("Expected error for missing chrootExec, got nil")
		}
	})
}

func TestListContents(t *testing.T) {
	t.Run("EmptyPath", func(t *testing.T) {
		_, err := ListContents("")
		if err == nil {
			t.Fatal("Expected error for empty path, got nil")
		}
	})

	t.Run("NonExistentPath", func(t *testing.T) {
		_, err := ListContents("/nonexistent/path/that/does/not/exist")
		if err == nil {
			t.Fatal("Expected error for non-existent path, got nil")
		}
	})

	t.Run("EmptyDirectory", func(t *testing.T) {
		dir := t.TempDir()

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}
		// Should contain only the root directory itself
		if len(pis) != 1 {
			t.Fatalf("Expected 1 entry (root dir), got %d", len(pis))
		}
		if pis[0].Mode.Type != "d" {
			t.Errorf("Expected type 'd', got %q", pis[0].Mode.Type)
		}
		if pis[0].Path != dir {
			t.Errorf("Expected path %q, got %q", dir, pis[0].Path)
		}
	})

	t.Run("RegularFiles", func(t *testing.T) {
		dir := t.TempDir()

		content := []byte("hello world")
		filePath := filepath.Join(dir, "file.txt")
		if err := os.WriteFile(filePath, content, 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// root dir + file.txt
		if len(pis) != 2 {
			t.Fatalf("Expected 2 entries, got %d", len(pis))
		}

		// Find the file entry
		var filePi *PathInfo
		for _, pi := range pis {
			if pi.Path == filePath {
				filePi = pi
				break
			}
		}
		if filePi == nil {
			t.Fatal("File entry not found in results")
		}
		if filePi.Mode.Type != "-" {
			t.Errorf("Expected type '-', got %q", filePi.Mode.Type)
		}
		if filePi.Size != uint64(len(content)) {
			t.Errorf("Expected size %d, got %d", len(content), filePi.Size)
		}
	})

	t.Run("Subdirectories", func(t *testing.T) {
		dir := t.TempDir()

		subdir := filepath.Join(dir, "subdir")
		if err := os.Mkdir(subdir, 0755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}
		nestedFile := filepath.Join(subdir, "nested.txt")
		if err := os.WriteFile(nestedFile, []byte("nested"), 0600); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// root dir + subdir + nested.txt
		if len(pis) != 3 {
			t.Fatalf("Expected 3 entries, got %d", len(pis))
		}

		pathSet := make(map[string]string) // path -> type
		for _, pi := range pis {
			pathSet[pi.Path] = pi.Mode.Type
		}
		if typ, ok := pathSet[dir]; !ok || typ != "d" {
			t.Errorf("Root dir missing or wrong type: ok=%v type=%q", ok, typ)
		}
		if typ, ok := pathSet[subdir]; !ok || typ != "d" {
			t.Errorf("Subdir missing or wrong type: ok=%v type=%q", ok, typ)
		}
		if typ, ok := pathSet[nestedFile]; !ok || typ != "-" {
			t.Errorf("Nested file missing or wrong type: ok=%v type=%q", ok, typ)
		}
	})

	t.Run("Symlinks", func(t *testing.T) {
		dir := t.TempDir()

		target := filepath.Join(dir, "target.txt")
		if err := os.WriteFile(target, []byte("target"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		link := filepath.Join(dir, "link.txt")
		if err := os.Symlink("target.txt", link); err != nil {
			t.Fatalf("Symlink failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// root dir + target.txt + link.txt
		if len(pis) != 3 {
			t.Fatalf("Expected 3 entries, got %d", len(pis))
		}

		var linkPi *PathInfo
		for _, pi := range pis {
			if pi.Path == link {
				linkPi = pi
				break
			}
		}
		if linkPi == nil {
			t.Fatal("Symlink entry not found in results")
		}
		if linkPi.Mode.Type != "l" {
			t.Errorf("Expected type 'l', got %q", linkPi.Mode.Type)
		}
		if linkPi.Link != "target.txt" {
			t.Errorf("Expected link target 'target.txt', got %q", linkPi.Link)
		}
	})

	t.Run("SpecialFilesIgnored", func(t *testing.T) {
		dir := t.TempDir()

		// Create a regular file and a FIFO (named pipe)
		regFile := filepath.Join(dir, "regular.txt")
		if err := os.WriteFile(regFile, []byte("data"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}
		fifoPath := filepath.Join(dir, "myfifo")
		if err := unix.Mkfifo(fifoPath, 0644); err != nil {
			t.Fatalf("Mkfifo failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// root dir + regular.txt only; FIFO should be ignored
		if len(pis) != 2 {
			t.Fatalf("Expected 2 entries (fifo should be ignored), got %d", len(pis))
		}
		for _, pi := range pis {
			if pi.Path == fifoPath {
				t.Error("FIFO should have been ignored but was included")
			}
		}
	})

	t.Run("Permissions", func(t *testing.T) {
		dir := t.TempDir()

		filePath := filepath.Join(dir, "perms.txt")
		if err := os.WriteFile(filePath, []byte("x"), 0755); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		var filePi *PathInfo
		for _, pi := range pis {
			if pi.Path == filePath {
				filePi = pi
				break
			}
		}
		if filePi == nil {
			t.Fatal("File entry not found")
		}
		if filePi.Mode.Perms != 0755 {
			t.Errorf("Expected perms 0755, got %04o", filePi.Mode.Perms)
		}
	})

	t.Run("UidGid", func(t *testing.T) {
		dir := t.TempDir()

		filePath := filepath.Join(dir, "owner.txt")
		if err := os.WriteFile(filePath, []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		var filePi *PathInfo
		for _, pi := range pis {
			if pi.Path == filePath {
				filePi = pi
				break
			}
		}
		if filePi == nil {
			t.Fatal("File entry not found")
		}
		// The file should be owned by the current user
		if filePi.Uid != uint64(os.Getuid()) {
			t.Errorf("Expected UID %d, got %d", os.Getuid(), filePi.Uid)
		}
		if filePi.Gid != uint64(os.Getgid()) {
			t.Errorf("Expected GID %d, got %d", os.Getgid(), filePi.Gid)
		}
	})

	t.Run("SymlinkToDirectoryNotFollowed", func(t *testing.T) {
		dir := t.TempDir()

		// Create a subdirectory with a file in it
		subdir := filepath.Join(dir, "realdir")
		if err := os.Mkdir(subdir, 0755); err != nil {
			t.Fatalf("Mkdir failed: %v", err)
		}
		if err := os.WriteFile(filepath.Join(subdir, "inner.txt"), []byte("x"), 0644); err != nil {
			t.Fatalf("WriteFile failed: %v", err)
		}

		// Create a symlink pointing to the subdirectory
		dirLink := filepath.Join(dir, "linkdir")
		if err := os.Symlink("realdir", dirLink); err != nil {
			t.Fatalf("Symlink failed: %v", err)
		}

		pis, err := ListContents(dir)
		if err != nil {
			t.Fatalf("ListContents failed: %v", err)
		}

		// root dir + realdir + inner.txt + linkdir (symlink, not followed)
		if len(pis) != 4 {
			for _, pi := range pis {
				t.Logf("  %s %s (link=%q)", pi.Mode.Type, pi.Path, pi.Link)
			}
			t.Fatalf("Expected 4 entries, got %d", len(pis))
		}

		var linkPi *PathInfo
		for _, pi := range pis {
			if pi.Path == dirLink {
				linkPi = pi
				break
			}
		}
		if linkPi == nil {
			t.Fatal("Directory symlink entry not found")
		}
		if linkPi.Mode.Type != "l" {
			t.Errorf("Expected symlink type 'l', got %q", linkPi.Mode.Type)
		}
		if linkPi.Link != "realdir" {
			t.Errorf("Expected link target 'realdir', got %q", linkPi.Link)
		}
	})
}

// --- Block device sysfs helpers ---

// setupMockSysClassBlock creates a temp directory simulating /sys/class/block/
// and redirects sysClassBlockPath to it. Returns the temp sysfs root.
func setupMockSysClassBlock(t *testing.T) string {
	t.Helper()
	tmpDir := t.TempDir()
	origSysClassBlock := sysClassBlockPath
	sysClassBlockPath = tmpDir
	t.Cleanup(func() { sysClassBlockPath = origSysClassBlock })
	return tmpDir
}

// createSysfsPartition creates a fake sysfs partition directory inside the
// parent device directory with the given partition number.
func createSysfsPartition(t *testing.T, sysClassBlock, parentName, partName string, partNum int) {
	t.Helper()
	partDir := filepath.Join(sysClassBlock, parentName, partName)
	if err := os.MkdirAll(partDir, 0755); err != nil {
		t.Fatal(err)
	}
	partFile := filepath.Join(partDir, "partition")
	if err := os.WriteFile(partFile, []byte(intToStr(partNum)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	// Also create the sysfs entry for the partition itself (for PartitionNumber lookups).
	partSysfs := filepath.Join(sysClassBlock, partName)
	if err := os.MkdirAll(partSysfs, 0755); err != nil {
		t.Fatal(err)
	}
	partFileDirect := filepath.Join(partSysfs, "partition")
	if err := os.WriteFile(partFileDirect, []byte(intToStr(partNum)+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
}

func intToStr(n int) string {
	return fmt.Sprintf("%d", n)
}

// --- Block device sysfs tests ---

func TestBlockDeviceNthPartition(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		// Create parent device directory.
		parentName := "sda"
		parentDir := filepath.Join(sysfs, parentName)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			t.Fatal(err)
		}

		// Create partitions sda1 (partition 1) and sda2 (partition 2).
		createSysfsPartition(t, sysfs, parentName, "sda1", 1)
		createSysfsPartition(t, sysfs, parentName, "sda2", 2)

		// Look up partition 2.
		result, err := BlockDeviceNthPartition("/dev/sda", 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/sda2" {
			t.Errorf("expected /dev/sda2, got %s", result)
		}
	})

	t.Run("LoopDevice", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		parentName := "loop0"
		parentDir := filepath.Join(sysfs, parentName)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			t.Fatal(err)
		}

		createSysfsPartition(t, sysfs, parentName, "loop0p1", 1)
		createSysfsPartition(t, sysfs, parentName, "loop0p2", 2)
		createSysfsPartition(t, sysfs, parentName, "loop0p3", 3)

		result, err := BlockDeviceNthPartition("/dev/loop0", 3)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/loop0p3" {
			t.Errorf("expected /dev/loop0p3, got %s", result)
		}
	})

	t.Run("NvmeDevice", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		parentName := "nvme0n1"
		parentDir := filepath.Join(sysfs, parentName)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			t.Fatal(err)
		}

		createSysfsPartition(t, sysfs, parentName, "nvme0n1p1", 1)
		createSysfsPartition(t, sysfs, parentName, "nvme0n1p2", 2)

		result, err := BlockDeviceNthPartition("/dev/nvme0n1", 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/nvme0n1p1" {
			t.Errorf("expected /dev/nvme0n1p1, got %s", result)
		}
	})

	t.Run("PartitionNotFound", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		parentName := "sda"
		parentDir := filepath.Join(sysfs, parentName)
		if err := os.MkdirAll(parentDir, 0755); err != nil {
			t.Fatal(err)
		}

		createSysfsPartition(t, sysfs, parentName, "sda1", 1)

		_, err := BlockDeviceNthPartition("/dev/sda", 5)
		if err == nil {
			t.Error("expected error for non-existent partition")
		}
	})

	t.Run("EmptyBlockDevice", func(t *testing.T) {
		_, err := BlockDeviceNthPartition("", 1)
		if err == nil {
			t.Error("expected error for empty blockDevice")
		}
	})

	t.Run("InvalidNth", func(t *testing.T) {
		_, err := BlockDeviceNthPartition("/dev/sda", 0)
		if err == nil {
			t.Error("expected error for nth <= 0")
		}
		_, err = BlockDeviceNthPartition("/dev/sda", -1)
		if err == nil {
			t.Error("expected error for nth < 0")
		}
	})

	t.Run("DeviceNotInSysfs", func(t *testing.T) {
		setupMockSysClassBlock(t)
		_, err := BlockDeviceNthPartition("/dev/nonexistent", 1)
		if err == nil {
			t.Error("expected error for device not in sysfs")
		}
	})
}

func TestBlockDeviceForPartition(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		// Create a sysfs structure that mimics real devices:
		// /sys/class/block/sda1 -> ../../devices/pci/.../sda/sda1
		// We simulate this by creating a real directory hierarchy and a symlink.
		devicesDir := filepath.Join(sysfs, ".devices", "sda", "sda1")
		if err := os.MkdirAll(devicesDir, 0755); err != nil {
			t.Fatal(err)
		}
		// Create partition file in the real path.
		if err := os.WriteFile(filepath.Join(devicesDir, "partition"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create the sysfs symlink: sysClassBlock/sda1 -> .devices/sda/sda1
		partSysLink := filepath.Join(sysfs, "sda1")
		if err := os.Symlink(devicesDir, partSysLink); err != nil {
			t.Fatal(err)
		}

		result, err := BlockDeviceForPartition("/dev/sda1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/sda" {
			t.Errorf("expected /dev/sda, got %s", result)
		}
	})

	t.Run("LoopDevice", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		devicesDir := filepath.Join(sysfs, ".devices", "loop0", "loop0p1")
		if err := os.MkdirAll(devicesDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(devicesDir, "partition"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}

		partSysLink := filepath.Join(sysfs, "loop0p1")
		if err := os.Symlink(devicesDir, partSysLink); err != nil {
			t.Fatal(err)
		}

		result, err := BlockDeviceForPartition("/dev/loop0p1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/loop0" {
			t.Errorf("expected /dev/loop0, got %s", result)
		}
	})

	t.Run("NvmeDevice", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		devicesDir := filepath.Join(sysfs, ".devices", "nvme0n1", "nvme0n1p2")
		if err := os.MkdirAll(devicesDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(devicesDir, "partition"), []byte("2\n"), 0644); err != nil {
			t.Fatal(err)
		}

		partSysLink := filepath.Join(sysfs, "nvme0n1p2")
		if err := os.Symlink(devicesDir, partSysLink); err != nil {
			t.Fatal(err)
		}

		result, err := BlockDeviceForPartition("/dev/nvme0n1p2")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "/dev/nvme0n1" {
			t.Errorf("expected /dev/nvme0n1, got %s", result)
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		_, err := BlockDeviceForPartition("")
		if err == nil {
			t.Error("expected error for empty partitionPath")
		}
	})

	t.Run("NotAPartition", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		// Create a sysfs entry without a "partition" file.
		devDir := filepath.Join(sysfs, "sda")
		if err := os.MkdirAll(devDir, 0755); err != nil {
			t.Fatal(err)
		}

		_, err := BlockDeviceForPartition("/dev/sda")
		if err == nil {
			t.Error("expected error for non-partition device")
		}
	})
}

func TestPartitionNumber(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		partDir := filepath.Join(sysfs, "sda1")
		if err := os.MkdirAll(partDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(partDir, "partition"), []byte("1\n"), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := PartitionNumber("/dev/sda1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "1" {
			t.Errorf("expected 1, got %s", result)
		}
	})

	t.Run("PartitionThree", func(t *testing.T) {
		sysfs := setupMockSysClassBlock(t)

		partDir := filepath.Join(sysfs, "sda3")
		if err := os.MkdirAll(partDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(partDir, "partition"), []byte("3\n"), 0644); err != nil {
			t.Fatal(err)
		}

		result, err := PartitionNumber("/dev/sda3")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "3" {
			t.Errorf("expected 3, got %s", result)
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		_, err := PartitionNumber("")
		if err == nil {
			t.Error("expected error for empty path")
		}
	})

	t.Run("NonexistentDevice", func(t *testing.T) {
		setupMockSysClassBlock(t)
		_, err := PartitionNumber("/dev/nonexistent99")
		if err == nil {
			t.Error("expected error for nonexistent device")
		}
	})
}

func TestPartitionLabel(t *testing.T) {
	t.Run("HasLabel", func(t *testing.T) {
		setupMockBlkid(t, map[string]string{
			"/dev/sda1:LABEL": "MY_LABEL",
		})

		result, err := PartitionLabel("/dev/sda1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "MY_LABEL" {
			t.Errorf("expected MY_LABEL, got %s", result)
		}
	})

	t.Run("NoLabel", func(t *testing.T) {
		setupMockBlkidFail(t)

		// No label found is not an error for PartitionLabel.
		result, err := PartitionLabel("/dev/sdb1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if result != "" {
			t.Errorf("expected empty string, got %s", result)
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		_, err := PartitionLabel("")
		if err == nil {
			t.Error("expected error for empty path")
		}
	})
}

func TestPartitionType(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		typeGUID := "c12a7328-f81f-11d2-ba4b-00a0c93ec93b"
		setupMockBlkid(t, map[string]string{
			"/dev/sda1:PARTTYPE": typeGUID,
		})

		result, err := PartitionType("/dev/sda1")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		expected := "C12A7328-F81F-11D2-BA4B-00A0C93EC93B"
		if result != expected {
			t.Errorf("expected %s, got %s", expected, result)
		}
	})

	t.Run("NotFound", func(t *testing.T) {
		setupMockBlkidFail(t)

		_, err := PartitionType("/dev/sdb1")
		if err == nil {
			t.Error("expected error when partition type not found")
		}
	})

	t.Run("EmptyPath", func(t *testing.T) {
		_, err := PartitionType("")
		if err == nil {
			t.Error("expected error for empty path")
		}
	})
}

// --- AcquireFileLock tests ---

func TestAcquireFileLock_Basic(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")

	unlock, err := AcquireFileLock(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireFileLock: %v", err)
	}
	defer unlock()

	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("lock file should exist: %v", err)
	}
}

func TestAcquireFileLock_CreatesFile(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, "subdir", "new.lock")

	// Parent directory doesn't exist — should fail.
	_, err := AcquireFileLock(lockPath, 1*time.Second)
	if err == nil {
		t.Fatal("expected error when parent dir does not exist")
	}
}

func TestAcquireFileLock_UnlockReleases(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "release.lock")

	unlock, err := AcquireFileLock(lockPath, 5*time.Second)
	if err != nil {
		t.Fatalf("AcquireFileLock: %v", err)
	}

	// Release the lock.
	unlock()

	// Should be able to re-acquire immediately.
	unlock2, err := AcquireFileLock(lockPath, 1*time.Second)
	if err != nil {
		t.Fatalf("re-acquire after unlock failed: %v", err)
	}
	unlock2()
}

func TestAcquireFileLock_MutualExclusion(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "contended.lock")

	// Hold the lock externally via a raw flock.
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		t.Fatalf("flock: %v", err)
	}

	// Try to acquire with a very short timeout — should fail.
	_, err = AcquireFileLock(lockPath, 200*time.Millisecond)
	f.Close() // release external lock

	if err == nil {
		t.Fatal("expected timeout error while lock was held externally")
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected 'timed out' message, got: %v", err)
	}
}

func TestAcquireFileLock_SequentialAccess(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "serial.lock")

	var mu sync.Mutex
	var order []int

	appendOrder := func(v int) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, v)
	}

	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			unlock, err := AcquireFileLock(lockPath, 10*time.Second)
			if err != nil {
				t.Errorf("goroutine %d: %v", id, err)
				return
			}
			appendOrder(id)
			time.Sleep(10 * time.Millisecond)
			unlock()
		}(i)
	}
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 3 {
		t.Fatalf("expected 3 executions, got %d", len(order))
	}
}

func TestAcquireFileLock_NoOverlap(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "overlap.lock")

	var mu sync.Mutex
	active := 0
	maxActive := 0

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock, err := AcquireFileLock(lockPath, 10*time.Second)
			if err != nil {
				t.Errorf("AcquireFileLock: %v", err)
				return
			}
			mu.Lock()
			active++
			if active > maxActive {
				maxActive = active
			}
			mu.Unlock()

			time.Sleep(5 * time.Millisecond)

			mu.Lock()
			active--
			mu.Unlock()
			unlock()
		}()
	}
	wg.Wait()

	if maxActive > 1 {
		t.Fatalf("lock did not provide mutual exclusion: maxActive=%d", maxActive)
	}
}
