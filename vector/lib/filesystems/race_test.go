package filesystems

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sys/unix"
	"matrixos/vector/lib/runner"
)

func TestCopyFile_ConcurrentSameDest(t *testing.T) {
	tmp := t.TempDir()
	src1 := filepath.Join(tmp, "src1")
	src2 := filepath.Join(tmp, "src2")
	dst := filepath.Join(tmp, "dst")
	if err := os.WriteFile(src1, []byte("aaaa-content-1"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src2, []byte("bbbb-content-2"), 0644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 100)
	for _, src := range []string{src1, src2} {
		wg.Add(1)
		go func(s string) {
			defer wg.Done()
			for range 50 {
				if err := CopyFile(s, dst); err != nil {
					errs <- err
					return
				}
			}
		}(src)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent CopyFile failed: %v", err)
	}
}

func TestCopyFile_PreservesSourceMode(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src.pub")
	dst := filepath.Join(tmp, "dst.pub")
	if err := os.WriteFile(src, []byte("pubkey"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := CopyFile(src, dst); err != nil {
		t.Fatalf("CopyFile: %v", err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Errorf("CopyFile mode = %04o, want 0644", got)
	}
}

func TestLoop_DetachClearsDeviceAndBackingFile(t *testing.T) {
	setupMockLoop(t)

	loopDir := filepath.Join(sysBlockPrefix, "loop5", "loop")
	if err := os.MkdirAll(loopDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, "backing_file"), []byte("/images/root.img\n"), 0644); err != nil {
		t.Fatal(err)
	}

	f, err := os.CreateTemp(t.TempDir(), "loopdev")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	}

	l := NewLoopFromDevice("/dev/loop5")
	if err := l.Detach(); err != nil {
		t.Fatalf("Detach failed: %v", err)
	}
	if got := l.BackingFile(); got != "" {
		t.Errorf("BackingFile() after Detach = %q, want empty", got)
	}
}

func TestLoop_AttachRetriesOnEBUSY(t *testing.T) {
	setupMockLoop(t)

	tmp := t.TempDir()
	ctlFile, err := os.CreateTemp(tmp, "ctl")
	if err != nil {
		t.Fatal(err)
	}
	defer ctlFile.Close()
	imgFile, err := os.CreateTemp(tmp, "img")
	if err != nil {
		t.Fatal(err)
	}
	defer imgFile.Close()
	loopFile, err := os.CreateTemp(tmp, "loop")
	if err != nil {
		t.Fatal(err)
	}
	defer loopFile.Close()

	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		switch name {
		case loopControlPath:
			return ctlFile, nil
		case "/tmp/retry.img":
			return imgFile, nil
		default:
			return loopFile, nil
		}
	}

	nextDev := 0
	ioctlRetInt = func(fd int, req uint) (int, error) {
		if req != unix.LOOP_CTL_GET_FREE {
			return 0, errors.New("unexpected ioctl")
		}
		d := nextDev
		nextDev++
		return d, nil
	}

	setFDAttempts := 0
	ioctlSetInt = func(fd int, req uint, val int) error {
		if req == unix.LOOP_SET_FD {
			setFDAttempts++
			if setFDAttempts <= 2 {
				return unix.EBUSY
			}
		}
		return nil
	}

	l, err := NewLoop("/tmp/retry.img")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Attach(); err != nil {
		t.Fatalf("Attach should retry on EBUSY, got: %v", err)
	}
	if setFDAttempts != 3 {
		t.Errorf("LOOP_SET_FD attempts = %d, want 3", setFDAttempts)
	}
	if l.DevicePath() != "/dev/loop2" {
		t.Errorf("DevicePath() = %q, want /dev/loop2", l.DevicePath())
	}
}

func TestLoop_AttachExhaustsEBUSYRetries(t *testing.T) {
	setupMockLoop(t)

	f, err := os.CreateTemp(t.TempDir(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	}
	attempts := 0
	ioctlSetInt = func(fd int, req uint, val int) error {
		if req == unix.LOOP_SET_FD {
			attempts++
			return unix.EBUSY
		}
		return nil
	}

	l, err := NewLoop("/tmp/busy.img")
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Attach(); !errors.Is(err, unix.EBUSY) {
		t.Fatalf("expected EBUSY error after retries exhausted, got: %v", err)
	}
	if attempts != loopAttachMaxRetries {
		t.Errorf("attempts = %d, want %d", attempts, loopAttachMaxRetries)
	}
	if l.DevicePath() != "" {
		t.Errorf("DevicePath() = %q, want empty after failed attach", l.DevicePath())
	}
}

func TestLoop_ConcurrentAttachAndDeviceRead(t *testing.T) {
	setupMockLoop(t)

	tmp := t.TempDir()
	f, err := os.CreateTemp(tmp, "fake")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	}

	l, err := NewLoop("/tmp/race.img")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		_ = l.Attach()
	}()
	go func() {
		defer wg.Done()
		_ = l.DevicePath()
		_ = l.BackingFile()
	}()
	go func() {
		defer wg.Done()
		_ = l.Detach()
	}()
	wg.Wait()
}

func TestFsenc_NilCallbacksDoNotPanic(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")
	if err := os.WriteFile(desiredDevice, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	f, err := NewFsenc(baseFsencConfig(), nil, nil)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	f.runner = runner.NewMockRunner().Run

	if err := f.LuksEncrypt("/dev/loop0p3", desiredDevice); err != nil {
		t.Fatalf("LuksEncrypt with nil callbacks failed: %v", err)
	}
}

func TestFsenc_KeyFileCheckedOnce(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "enc.key")
	if err := os.WriteFile(keyFile, []byte("secret-key"), 0600); err != nil {
		t.Fatal(err)
	}
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")
	if err := os.WriteFile(desiredDevice, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	cfg := baseFsencConfig()
	cfg.Items["Imager.EncryptionKey"] = []string{keyFile}
	f, err := NewFsenc(cfg, noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}

	var openStdinNonNil bool
	f.runner = func(cmd *runner.Cmd) error {
		if containsArg(cmd.Args, "luksFormat") {
			// Remove key file between luksFormat and open to expose TOCTOU re-stat.
			_ = os.Remove(keyFile)
		}
		if containsArg(cmd.Args, "open") {
			openStdinNonNil = cmd.Stdin != nil
		}
		return nil
	}

	if err := f.LuksEncrypt("/dev/loop0p3", desiredDevice); err != nil {
		t.Fatalf("LuksEncrypt: %v", err)
	}
	if openStdinNonNil {
		t.Error("cryptsetup open received non-nil Stdin despite keyFileArg being a file path (TOCTOU re-stat)")
	}
}

func TestFsenc_CleanupWaitsForInFlightCryptsetupOpen(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}

	var cleanedMu sync.Mutex
	var cleanedMappers []string
	var openFinishedAtCleanup bool
	var openFinished atomic.Bool
	f.cleanupDevices = func(devices []string, _, _ io.Writer) []string {
		cleanedMu.Lock()
		cleanedMappers = append(cleanedMappers, devices...)
		openFinishedAtCleanup = openFinished.Load()
		cleanedMu.Unlock()
		return nil
	}

	inOpen := make(chan struct{})
	releaseOpen := make(chan struct{})
	f.runner = func(cmd *runner.Cmd) error {
		if containsArg(cmd.Args, "open") {
			close(inOpen)
			<-releaseOpen
			// Simulate cryptsetup open creating the device node right before returning.
			_ = os.WriteFile(desiredDevice, []byte{}, 0600)
			openFinished.Store(true)
		}
		return nil
	}

	encryptDone := make(chan error, 1)
	go func() {
		encryptDone <- f.LuksEncrypt("/dev/loop0p3", desiredDevice)
	}()

	<-inOpen

	cleanupDone := make(chan struct{})
	go func() {
		f.Cleanup()
		close(cleanupDone)
	}()

	// Give Cleanup a moment to contend with in-flight cryptsetup open.
	time.Sleep(40 * time.Millisecond)
	close(releaseOpen)

	if err := <-encryptDone; err == nil {
		t.Fatal("expected LuksEncrypt to return error when Cleanup() ran concurrently with cryptsetup open")
	}
	<-cleanupDone

	cleanedMu.Lock()
	defer cleanedMu.Unlock()
	if !openFinishedAtCleanup {
		t.Fatal("Cleanup ran cleanupDevices while cryptsetup open was still in flight")
	}
	if len(cleanedMappers) != 1 || cleanedMappers[0] != "matrixosroot" {
		t.Fatalf("cleanedMappers = %v, want [matrixosroot]", cleanedMappers)
	}
}

func TestFsenc_CleanupDuringLuksFormatPreventsSubsequentOpen(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")
	if err := os.WriteFile(desiredDevice, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}

	inFormat := make(chan struct{})
	releaseFormat := make(chan struct{})
	var openCalled atomic.Bool
	f.runner = func(cmd *runner.Cmd) error {
		if containsArg(cmd.Args, "luksFormat") {
			close(inFormat)
			<-releaseFormat
		}
		if containsArg(cmd.Args, "open") {
			openCalled.Store(true)
		}
		return nil
	}

	encryptErr := make(chan error, 1)
	go func() {
		encryptErr <- f.LuksEncrypt("/dev/loop0p3", desiredDevice)
	}()

	<-inFormat
	// Cleanup runs while luksFormat is in flight (e.g. SIGINT).
	f.Cleanup()
	close(releaseFormat)

	if err := <-encryptErr; err == nil {
		t.Fatal("expected LuksEncrypt to fail when Cleanup() ran during luksFormat")
	}
	if openCalled.Load() {
		t.Fatal("cryptsetup open was called after Cleanup() already ran (leaked LUKS mapper)")
	}
}

func TestFsenc_LuksEncryptRejectsEmptyKey(t *testing.T) {
	cfg := baseFsencConfig()
	cfg.Items["Imager.EncryptionKey"] = []string{""}
	f, err := NewFsenc(cfg, noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	var runnerCalled bool
	f.runner = func(*runner.Cmd) error {
		runnerCalled = true
		return nil
	}
	if err := f.LuksEncrypt("/dev/loop0p3", "/dev/mapper/matrixosroot"); err == nil {
		t.Fatal("expected error for empty encryption key, got nil")
	}
	if runnerCalled {
		t.Fatal("cryptsetup should not be invoked with an empty encryption key")
	}
}

func TestFsenc_ConcurrentCleanupSerializes(t *testing.T) {
	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	f.openMappers = []string{"crypt-test"}

	inCleanup := make(chan struct{})
	releaseCleanup := make(chan struct{})
	var calls atomic.Int32
	f.cleanupDevices = func(devices []string, _, _ io.Writer) []string {
		if calls.Add(1) == 1 {
			close(inCleanup)
			<-releaseCleanup
			// Simulate close failure on first attempt so second Cleanup retries it.
			return devices
		}
		return nil
	}

	call1Done := make(chan struct{})
	go func() {
		f.Cleanup()
		close(call1Done)
	}()

	<-inCleanup

	call2Done := make(chan struct{})
	go func() {
		f.Cleanup()
		close(call2Done)
	}()

	select {
	case <-call2Done:
		close(releaseCleanup)
		<-call1Done
		t.Fatal("second concurrent Fsenc.Cleanup() returned while first Cleanup() was still closing LUKS mappers")
	case <-time.After(100 * time.Millisecond):
		close(releaseCleanup)
	}

	<-call1Done
	<-call2Done

	if got := calls.Load(); got != 2 {
		t.Errorf("cleanupDevices calls = %d, want 2 (second Cleanup should retry failed mapper)", got)
	}
}

func TestFsenc_UsesInjectedWriters(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")
	if err := os.WriteFile(desiredDevice, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	var stdout, stderr bytes.Buffer
	f.SetStdout(&stdout)
	f.SetStderr(&stderr)
	f.runner = func(cmd *runner.Cmd) error {
		if cmd.Stdout != &stdout {
			t.Error("expected runner cmd.Stdout to be injected stdout writer")
		}
		if cmd.Stderr != &stderr {
			t.Error("expected runner cmd.Stderr to be injected stderr writer")
		}
		return nil
	}

	if err := f.ValidateLuksVariables(); err != nil {
		t.Fatalf("ValidateLuksVariables: %v", err)
	}
	if err := f.LuksEncrypt("/dev/loop0p3", desiredDevice); err != nil {
		t.Fatalf("LuksEncrypt: %v", err)
	}
	if !strings.Contains(stdout.String(), "Formatting /dev/loop0p3") {
		t.Errorf("expected LuksEncrypt output in injected stdout, got %q", stdout.String())
	}
}

func TestMountInfo_PrefixCollisionFailClosedAndTopmost(t *testing.T) {
	t.Run("NoPrefixCollisionOnSiblingDir", func(t *testing.T) {
		setupMockMountInfo(t, []*MountInfoEntry{
			{Mountpoint: "/chroot/gnome.ostree_rootfs/proc", Source: "proc", FSType: "proc"},
		})
		if err := CheckActiveMounts("/chroot/gnome"); err != nil {
			t.Fatalf("CheckActiveMounts falsely matched sibling path: %v", err)
		}
	})

	t.Run("CheckActiveMountsPropagatesReadError", func(t *testing.T) {
		setupMockMountInfoFail(t)
		if err := CheckActiveMounts("/chroot/gnome"); err == nil {
			t.Fatal("CheckActiveMounts failed open on ReadMountInfo error")
		}
	})

	t.Run("IsMountedPropagatesReadError", func(t *testing.T) {
		setupMockMountInfoFail(t)
		if _, err := IsMounted("/chroot/gnome"); err == nil {
			t.Fatal("IsMounted failed open on ReadMountInfo error")
		}
	})

	t.Run("BestMountPrefersTopmostOvermount", func(t *testing.T) {
		entries := []*MountInfoEntry{
			{MountID: 1, Mountpoint: "/mnt", Source: "/dev/sda1", FSType: "ext4"},
			{MountID: 2, Mountpoint: "/mnt", Source: "/dev/sda2", FSType: "btrfs"},
		}
		got := bestMountForPath(entries, "/mnt/file")
		if got == nil || got.Source != "/dev/sda2" {
			t.Fatalf("bestMountForPath returned %+v, want topmost /dev/sda2", got)
		}
	})
}

func TestCopyDirPreserve_FIFODoesNotHangAndPreservesRootSticky(t *testing.T) {
	src := t.TempDir()
	dst := filepath.Join(t.TempDir(), "dst")

	if err := os.Chmod(src, 0755|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	fifoPath := filepath.Join(src, "test.fifo")
	if err := unix.Mkfifo(fifoPath, 0644); err != nil {
		t.Fatalf("Mkfifo: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- CopyDirPreserve(src, dst)
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("CopyDirPreserve failed on FIFO: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("CopyDirPreserve hung forever trying to open FIFO")
	}

	rootInfo, err := os.Stat(dst)
	if err != nil {
		t.Fatalf("Stat(dst): %v", err)
	}
	if rootInfo.Mode()&os.ModeSticky == 0 {
		t.Errorf("CopyDirPreserve dropped sticky bit on root dst (mode=%v)", rootInfo.Mode())
	}
	fifoInfo, err := os.Lstat(filepath.Join(dst, "test.fifo"))
	if err != nil {
		t.Fatalf("Lstat(dst/test.fifo): %v", err)
	}
	if fifoInfo.Mode()&os.ModeNamedPipe == 0 {
		t.Errorf("expected named pipe at dst/test.fifo, got mode=%v", fifoInfo.Mode())
	}
}

func TestPathInfoEquals_IgnoresNonRegularFileSize(t *testing.T) {
	dirA := mkPI("/etc/conf.d", "d", 0755, 0, 0, 0, "")
	dirB := mkPI("/etc/conf.d", "d", 0755, 0, 0, 4096, "")
	if !dirA.Equals(&dirB) {
		t.Error("PathInfo.Equals reported directories unequal solely due to filesystem Size difference")
	}

	linkA := mkPI("/etc/localtime", "l", 0777, 0, 0, 0, "../usr/share/zoneinfo/UTC")
	linkB := mkPI("/etc/localtime", "l", 0777, 0, 0, 25, "../usr/share/zoneinfo/UTC")
	if !linkA.Equals(&linkB) {
		t.Error("PathInfo.Equals reported identical symlinks unequal solely due to Size difference")
	}
}

func TestLoop_DetachRetriesOnEBUSYAndNilReceiverSafe(t *testing.T) {
	setupMockLoop(t)

	var nilLoop *Loop
	if err := nilLoop.Attach(); err == nil {
		t.Error("expected error on nil Loop.Attach")
	}
	if err := nilLoop.Detach(); err == nil {
		t.Error("expected error on nil Loop.Detach")
	}

	f, err := os.CreateTemp(t.TempDir(), "loopdev")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	}

	attempts := 0
	ioctlSetInt = func(fd int, req uint, val int) error {
		if req == unix.LOOP_CLR_FD {
			attempts++
			if attempts < 3 {
				return unix.EBUSY
			}
		}
		return nil
	}

	l := NewLoopFromDevice("/dev/loop0")
	if err := l.Detach(); err != nil {
		t.Fatalf("Detach should retry on transient EBUSY, got: %v", err)
	}
	if attempts != 3 {
		t.Errorf("Detach attempts = %d, want 3", attempts)
	}
}

func TestRemoveDir_RejectsRootAndRemovesDanglingSymlink(t *testing.T) {
	if err := RemoveDir("/", RemoveDirOptions{Stdout: io.Discard}); err == nil {
		t.Fatal("RemoveDir('/') should be rejected by CheckDirIsRoot")
	}
	if err := EmptyDir("/", EmptyDirOptions{Stdout: io.Discard}); err == nil {
		t.Fatal("EmptyDir('/') should be rejected by CheckDirIsRoot")
	}
	nonexistent := filepath.Join(t.TempDir(), "does-not-exist")
	if err := RemoveDir(nonexistent, RemoveDirOptions{Stdout: io.Discard}); err != nil {
		t.Fatalf("RemoveDir on nonexistent path should succeed, got: %v", err)
	}
	link := filepath.Join(t.TempDir(), "dangling")
	if err := os.Symlink("/nonexistent/target", link); err != nil {
		t.Fatal(err)
	}
	if err := RemoveDir(link, RemoveDirOptions{Stdout: io.Discard}); err != nil {
		t.Fatalf("RemoveDir on dangling symlink failed: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatal("RemoveDir left dangling symlink behind")
	}
}

func TestFstrim_NilWritersDoNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Fstrim with nil writers panicked: %v", r)
		}
	}()
	if err := Fstrim(func(*runner.Cmd) error { return nil }, nil, nil, "/mnt/test"); err != nil {
		t.Fatalf("Fstrim: %v", err)
	}
}

func TestMoveCrossDevice_PreservesDanglingSymlink(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "src-link")
	dst := filepath.Join(tmp, "sub", "dst-link")
	if err := os.Symlink("/nonexistent/target", src); err != nil {
		t.Fatal(err)
	}

	if err := moveCrossDevice(src, dst); err != nil {
		t.Fatalf("moveCrossDevice on dangling symlink failed: %v", err)
	}
	target, err := os.Readlink(dst)
	if err != nil {
		t.Fatalf("Readlink(dst): %v", err)
	}
	if target != "/nonexistent/target" {
		t.Errorf("symlink target = %q, want /nonexistent/target", target)
	}
	if _, err := os.Lstat(src); !os.IsNotExist(err) {
		t.Error("expected source symlink to be removed")
	}
}

func TestAcquireFileLock_DoubleUnlockIsSafe(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "test.lock")
	unlock, err := AcquireFileLock(lockPath, time.Second)
	if err != nil {
		t.Fatalf("AcquireFileLock: %v", err)
	}
	// Concurrent/repeated unlocks must be idempotent and never race on f.Close().
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			unlock()
		}()
	}
	wg.Wait()
}

func TestFsenc_LuksEncryptRefusesFormatAfterCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")
	if err := os.WriteFile(desiredDevice, []byte{}, 0600); err != nil {
		t.Fatal(err)
	}

	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	var formatCalled atomic.Bool
	f.runner = func(cmd *runner.Cmd) error {
		if containsArg(cmd.Args, "luksFormat") {
			formatCalled.Store(true)
		}
		return nil
	}

	f.Cleanup()

	if err := f.LuksEncrypt("/dev/loop0p3", desiredDevice); err == nil {
		t.Fatal("expected LuksEncrypt to fail when Cleanup() already ran before luksFormat")
	}
	if formatCalled.Load() {
		t.Fatal("cryptsetup luksFormat ran even though Cleanup() already ran")
	}
}

func TestFsenc_LuksEncryptFailsWhenCleanupClosesMapperAfterOpen(t *testing.T) {
	tmpDir := t.TempDir()
	desiredDevice := filepath.Join(tmpDir, "matrixosroot")

	origExecRun := execRun
	execRun = func(*runner.Cmd) error { return nil }
	t.Cleanup(func() { execRun = origExecRun })

	f, err := NewFsenc(baseFsencConfig(), noop, noop)
	if err != nil {
		t.Fatalf("NewFsenc: %v", err)
	}
	f.cleanupDevices = func(devices []string, _, _ io.Writer) []string {
		return nil
	}

	inOpen := make(chan struct{})
	releaseOpen := make(chan struct{})
	f.runner = func(cmd *runner.Cmd) error {
		if containsArg(cmd.Args, "open") {
			close(inOpen)
			<-releaseOpen
			_ = os.WriteFile(desiredDevice, []byte{}, 0600)
		}
		return nil
	}

	encryptDone := make(chan error, 1)
	go func() {
		encryptDone <- f.LuksEncrypt("/dev/loop0p3", desiredDevice)
	}()

	<-inOpen
	cleanupDone := make(chan struct{})
	go func() {
		f.Cleanup()
		close(cleanupDone)
	}()

	time.Sleep(40 * time.Millisecond)
	close(releaseOpen)

	<-cleanupDone
	if err := <-encryptDone; err == nil {
		t.Fatal("LuksEncrypt returned nil even though Cleanup() ran and closed the mapper")
	}
}

func TestCopyDirPreserve_ReadOnlyDirWithFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root bypasses DAC read-only directory checks")
	}
	src := filepath.Join(t.TempDir(), "src")
	roSub := filepath.Join(src, "rosub")
	if err := os.MkdirAll(roSub, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "top.txt"), []byte("top"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(roSub, "inner.txt"), []byte("inner"), 0644); err != nil {
		t.Fatal(err)
	}

	// Make both sub-dir and root src read-only (0555).
	if err := os.Chmod(roSub, 0555); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(src, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(src, 0755)
		_ = os.Chmod(roSub, 0755)
	})

	dst := filepath.Join(t.TempDir(), "dst")
	t.Cleanup(func() {
		_ = os.Chmod(dst, 0755)
		_ = os.Chmod(filepath.Join(dst, "rosub"), 0755)
	})

	if err := CopyDirPreserve(src, dst); err != nil {
		t.Fatalf("CopyDirPreserve failed on read-only 0555 directory tree: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dst, "rosub", "inner.txt"))
	if err != nil {
		t.Fatalf("ReadFile(dst/rosub/inner.txt): %v", err)
	}
	if string(got) != "inner" {
		t.Errorf("inner.txt = %q, want inner", string(got))
	}
	for _, p := range []string{dst, filepath.Join(dst, "rosub")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("Stat(%s): %v", p, err)
		}
		if info.Mode().Perm() != 0555 {
			t.Errorf("%s perms = %04o, want 0555", p, info.Mode().Perm())
		}
	}
}

func TestLoop_AttachRollbackRetriesOnEBUSY(t *testing.T) {
	setupMockLoop(t)

	f, err := os.CreateTemp(t.TempDir(), "fake")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
		return f, nil
	}
	ioctlLoopInfo = func(fd int, info *unix.LoopInfo64) error {
		return unix.EINVAL
	}
	clrAttempts := 0
	ioctlSetInt = func(fd int, req uint, val int) error {
		if req == unix.LOOP_CLR_FD {
			clrAttempts++
			if clrAttempts < 3 {
				return unix.EBUSY
			}
		}
		return nil
	}

	l, err := NewLoop("/tmp/rollback.img")
	if err != nil {
		t.Fatal(err)
	}
	err = l.Attach()
	if err == nil {
		t.Fatal("expected error when LOOP_SET_STATUS64 fails")
	}
	if strings.Contains(err.Error(), "also failed to detach") {
		t.Fatalf("rollback LOOP_CLR_FD did not retry transient EBUSY: %v", err)
	}
	if clrAttempts != 3 {
		t.Errorf("rollback LOOP_CLR_FD attempts = %d, want 3", clrAttempts)
	}
}

func TestCopyFileReflink_CapturesStderrInError(t *testing.T) {
	tmp := t.TempDir()
	err := CopyFileReflink(filepath.Join(tmp, "does-not-exist"), filepath.Join(tmp, "dst"))
	if err == nil {
		t.Fatal("expected error for nonexistent source")
	}
	// cp prints the missing path to stderr; it must be captured in the error, not leaked to os.Stderr.
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Fatalf("CopyFileReflink error did not capture cp stderr: %v", err)
	}
}

func TestDefaultBlkidLookup_CapturesStderrInError(t *testing.T) {
	binDir := t.TempDir()
	fakeBlkid := filepath.Join(binDir, "blkid")
	script := "#!/bin/sh\necho 'blkid-custom-stderr-marker' >&2\nexit 2\n"
	if err := os.WriteFile(fakeBlkid, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	_, err := defaultBlkidLookup("/dev/fake0", "UUID")
	if err == nil {
		t.Fatal("expected error from failing blkid")
	}
	if !strings.Contains(err.Error(), "blkid-custom-stderr-marker") {
		t.Fatalf("defaultBlkidLookup error did not capture stderr: %v", err)
	}
}
