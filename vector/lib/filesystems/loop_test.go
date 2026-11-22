package filesystems

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

// ---------------------------------------------------------------------------
// helpers to save / restore loop.go mockable variables
// ---------------------------------------------------------------------------

func setupMockLoop(t *testing.T) {
	t.Helper()

	origReadFileBytes := readFileBytes
	origOpenFile := openFile
	origCloseFile := closeFile
	origIoctlRetInt := ioctlRetInt
	origIoctlSetInt := ioctlSetInt
	origIoctlLoopInfo := ioctlLoopInfo
	origSysBlockPrefix := sysBlockPrefix
	origDevPrefix := devPrefix
	origLoopControlPath := loopControlPath

	t.Cleanup(func() {
		readFileBytes = origReadFileBytes
		openFile = origOpenFile
		closeFile = origCloseFile
		ioctlRetInt = origIoctlRetInt
		ioctlSetInt = origIoctlSetInt
		ioctlLoopInfo = origIoctlLoopInfo
		sysBlockPrefix = origSysBlockPrefix
		devPrefix = origDevPrefix
		loopControlPath = origLoopControlPath
	})

	// Defaults: all ioctls succeed, close succeeds.
	ioctlRetInt = func(fd int, req uint) (int, error) { return 0, nil }
	ioctlSetInt = func(fd int, req uint, val int) error { return nil }
	ioctlLoopInfo = func(fd int, info *unix.LoopInfo64) error { return nil }
	closeFile = func(f *os.File) error { return nil }

	// Point sysBlockPrefix at a temp dir so tests can create sysfs files.
	sysBlockPrefix = t.TempDir()
}

// ---------------------------------------------------------------------------
// BackingFile tests
// ---------------------------------------------------------------------------

func TestLoopBackingFile(t *testing.T) {
	setupMockLoop(t)

	t.Run("HasBackingFile", func(t *testing.T) {
		loopDir := filepath.Join(sysBlockPrefix, "loop5", "loop")
		if err := os.MkdirAll(loopDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(loopDir, "backing_file"), []byte("/images/root.img\n"), 0644); err != nil {
			t.Fatal(err)
		}

		l := NewLoopFromDevice("/dev/loop5")
		got := l.BackingFile()
		if got != "/images/root.img" {
			t.Errorf("expected /images/root.img, got %q", got)
		}
	})

	t.Run("NoBackingFile", func(t *testing.T) {
		l := NewLoopFromDevice("/dev/loop99")
		got := l.BackingFile()
		if got != "" {
			t.Errorf("expected empty string, got %q", got)
		}
	})

	t.Run("EmptyBackingFile", func(t *testing.T) {
		loopDir := filepath.Join(sysBlockPrefix, "loop6", "loop")
		if err := os.MkdirAll(loopDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(loopDir, "backing_file"), []byte("   \n"), 0644); err != nil {
			t.Fatal(err)
		}

		l := NewLoopFromDevice("/dev/loop6")
		got := l.BackingFile()
		if got != "" {
			t.Errorf("expected empty string for whitespace-only file, got %q", got)
		}
	})

	t.Run("NoDeviceSet", func(t *testing.T) {
		l, err := NewLoop("/some/image.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		got := l.BackingFile()
		if got != "" {
			t.Errorf("expected empty string when Device is unset, got %q", got)
		}
	})
}

// ---------------------------------------------------------------------------
// Detach tests
// ---------------------------------------------------------------------------

func TestLoopDetach(t *testing.T) {
	setupMockLoop(t)

	t.Run("Success", func(t *testing.T) {
		f, err := os.CreateTemp("", "loopdetach")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())

		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			return f, nil
		}

		var calledCLR bool
		ioctlSetInt = func(fd int, req uint, val int) error {
			if req == unix.LOOP_CLR_FD {
				calledCLR = true
			}
			return nil
		}

		l := NewLoopFromDevice("/dev/loop0")
		if err := l.Detach(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !calledCLR {
			t.Error("LOOP_CLR_FD was not called")
		}
	})

	t.Run("NotAttached", func(t *testing.T) {
		l, err := NewLoop("/some/image.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Detach(); err == nil {
			t.Error("expected error when not attached")
		}
	})

	t.Run("DoubleDetach", func(t *testing.T) {
		f, err := os.CreateTemp("", "loopdetach")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())

		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			return f, nil
		}

		l := NewLoopFromDevice("/dev/loop0")
		if err := l.Detach(); err != nil {
			t.Fatalf("first detach failed: %v", err)
		}
		if err := l.Detach(); err == nil {
			t.Error("expected error on double detach")
		}
	})

	t.Run("OpenFail", func(t *testing.T) {
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			return nil, errors.New("device gone")
		}
		l := NewLoopFromDevice("/dev/loop0")
		if err := l.Detach(); err == nil {
			t.Error("expected error when open fails")
		}
	})

	t.Run("IoctlFail", func(t *testing.T) {
		f, err := os.CreateTemp("", "loopdetach")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())

		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			return f, nil
		}
		ioctlSetInt = func(fd int, req uint, val int) error {
			return errors.New("busy")
		}

		l := NewLoopFromDevice("/dev/loop0")
		if err := l.Detach(); err == nil {
			t.Error("expected error when ioctl fails")
		}
	})
}

// ---------------------------------------------------------------------------
// Attach tests
// ---------------------------------------------------------------------------

func TestLoopAttach(t *testing.T) {
	setupMockLoop(t)

	makeTemp := func(t *testing.T, name string) *os.File {
		t.Helper()
		f, err := os.CreateTemp("", name)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.Remove(f.Name()) })
		return f
	}

	t.Run("Success", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		imgFile := makeTemp(t, "img")
		loopFile := makeTemp(t, "loop")

		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			switch callN {
			case 1:
				return ctlFile, nil // loop-control
			case 2:
				return imgFile, nil // image
			case 3:
				return loopFile, nil // /dev/loopN
			}
			return nil, errors.New("unexpected open")
		}

		ioctlRetInt = func(fd int, req uint) (int, error) {
			if req == unix.LOOP_CTL_GET_FREE {
				return 3, nil
			}
			return 0, errors.New("unexpected ioctl")
		}

		var setFDCalled, setStatusCalled bool
		ioctlSetInt = func(fd int, req uint, val int) error {
			if req == unix.LOOP_SET_FD {
				setFDCalled = true
			}
			return nil
		}
		ioctlLoopInfo = func(fd int, info *unix.LoopInfo64) error {
			if info.Flags&unix.LO_FLAGS_PARTSCAN != 0 {
				setStatusCalled = true
			}
			return nil
		}

		devPrefix = "/dev"
		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if l.DevicePath() != "/dev/loop3" {
			t.Errorf("expected /dev/loop3, got %q", l.DevicePath())
		}
		if l.Path != "/tmp/disk.img" {
			t.Errorf("expected /tmp/disk.img, got %q", l.Path)
		}
		if !setFDCalled {
			t.Error("LOOP_SET_FD was not called")
		}
		if !setStatusCalled {
			t.Error("LOOP_SET_STATUS64 was not called with PARTSCAN")
		}
	})

	t.Run("DoubleAttach", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		imgFile := makeTemp(t, "img")
		loopFile := makeTemp(t, "loop")

		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			switch callN {
			case 1:
				return ctlFile, nil
			case 2:
				return imgFile, nil
			case 3:
				return loopFile, nil
			}
			return nil, errors.New("unexpected open")
		}
		ioctlRetInt = func(fd int, req uint) (int, error) { return 0, nil }

		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err != nil {
			t.Fatalf("first attach failed: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error on double attach")
		}
	})

	t.Run("ControlOpenFail", func(t *testing.T) {
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			return nil, errors.New("no loop-control")
		}
		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error when loop-control open fails")
		}
	})

	t.Run("GetFreeFail", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			if callN == 1 {
				return ctlFile, nil
			}
			return nil, errors.New("unexpected open")
		}
		ioctlRetInt = func(fd int, req uint) (int, error) {
			return 0, errors.New("no free loop")
		}

		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error when LOOP_CTL_GET_FREE fails")
		}
	})

	t.Run("ImageOpenFail", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			switch callN {
			case 1:
				return ctlFile, nil
			case 2:
				return nil, errors.New("image not found")
			}
			return nil, errors.New("unexpected open")
		}
		ioctlRetInt = func(fd int, req uint) (int, error) { return 0, nil }

		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error when image open fails")
		}
	})

	t.Run("SetFDFail", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		imgFile := makeTemp(t, "img")
		loopFile := makeTemp(t, "loop")

		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			switch callN {
			case 1:
				return ctlFile, nil
			case 2:
				return imgFile, nil
			case 3:
				return loopFile, nil
			}
			return nil, errors.New("unexpected open")
		}
		ioctlRetInt = func(fd int, req uint) (int, error) { return 0, nil }
		ioctlSetInt = func(fd int, req uint, val int) error {
			if req == unix.LOOP_SET_FD {
				return errors.New("LOOP_SET_FD failed")
			}
			return nil
		}

		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error when LOOP_SET_FD fails")
		}
	})

	t.Run("SetStatusFail", func(t *testing.T) {
		ctlFile := makeTemp(t, "ctl")
		imgFile := makeTemp(t, "img")
		loopFile := makeTemp(t, "loop")

		callN := 0
		openFile = func(name string, flag int, perm os.FileMode) (*os.File, error) {
			callN++
			switch callN {
			case 1:
				return ctlFile, nil
			case 2:
				return imgFile, nil
			case 3:
				return loopFile, nil
			}
			return nil, errors.New("unexpected open")
		}
		ioctlRetInt = func(fd int, req uint) (int, error) { return 0, nil }
		ioctlLoopInfo = func(fd int, info *unix.LoopInfo64) error {
			return errors.New("LOOP_SET_STATUS64 failed")
		}

		var detachCalled bool
		ioctlSetInt = func(fd int, req uint, val int) error {
			if req == unix.LOOP_CLR_FD {
				detachCalled = true
			}
			return nil
		}

		l, err := NewLoop("/tmp/disk.img")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := l.Attach(); err == nil {
			t.Error("expected error when LOOP_SET_STATUS64 fails")
		}
		if !detachCalled {
			t.Error("LOOP_CLR_FD should be called on status64 failure")
		}
		if l.DevicePath() != "" {
			t.Errorf("Device should remain empty on failure, got %q", l.DevicePath())
		}
	})
}

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewLoop(t *testing.T) {
	l, err := NewLoop("/tmp/test.img")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Path != "/tmp/test.img" {
		t.Errorf("expected /tmp/test.img, got %q", l.Path)
	}
	if l.DevicePath() != "" {
		t.Errorf("expected empty Device, got %q", l.DevicePath())
	}
}

func TestNewLoopFromDevice(t *testing.T) {
	l := NewLoopFromDevice("/dev/loop4")
	if l.DevicePath() != "/dev/loop4" {
		t.Errorf("expected /dev/loop4, got %q", l.DevicePath())
	}
	if l.Path != "" {
		t.Errorf("expected empty Path, got %q", l.Path)
	}
}

// ---------------------------------------------------------------------------
// Integration test — requires root (run with: sudo go test -run Integration)
// ---------------------------------------------------------------------------

func TestLoopIntegration(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("skipping integration test: must run as root")
	}

	// Create a 1 MiB sparse image file.
	img := filepath.Join(t.TempDir(), "test.img")
	f, err := os.Create(img)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 20); err != nil {
		f.Close()
		t.Fatal(err)
	}
	f.Close()

	absImg, err := filepath.Abs(img)
	if err != nil {
		t.Fatal(err)
	}

	l, err := NewLoop(absImg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// --- Attach ---
	if err := l.Attach(); err != nil {
		t.Fatalf("Attach() failed: %v", err)
	}
	dev := l.DevicePath()
	t.Logf("attached %s -> %s", absImg, dev)

	if dev == "" {
		t.Fatal("Device is empty after Attach")
	}

	// The device node must exist.
	if _, err := os.Stat(dev); err != nil {
		t.Fatalf("device %s does not exist: %v", dev, err)
	}

	// --- BackingFile ---
	bf := l.BackingFile()
	if bf == "" {
		t.Errorf("BackingFile() = %q, want empty string.", bf)
	}

	// --- Double-attach must fail ---
	if err := l.Attach(); err == nil {
		t.Error("double Attach() should return an error")
	}

	// --- Detach ---
	if err := l.Detach(); err != nil {
		t.Fatalf("Detach() failed: %v", err)
	}
	DevicesSettle()
	t.Logf("detached %s", dev)

	// After detach, backing file should be gone.
	if bf2 := l.BackingFile(); bf2 != "" {
		t.Errorf("BackingFile() after Detach = %q, want empty", bf2)
	}

	// --- Double-detach must fail ---
	if err := l.Detach(); err == nil {
		t.Error("double Detach() should return an error")
	}
}
