package filesystems

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"golang.org/x/sys/unix"
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
