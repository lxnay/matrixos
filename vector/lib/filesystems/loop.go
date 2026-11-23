package filesystems

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const (
	loopAttachMaxRetries    = 16
	loopDetachMaxRetries    = 8
	loopSetStatusMaxRetries = 8
)

var loopRetryDelay = 15 * time.Millisecond

// Mockable variables for loop-device operations.
var (
	loopControlPath = "/dev/loop-control"
	sysBlockPrefix  = "/sys/block"
	devPrefix       = "/dev"

	// Low-level wrappers; replaced by fakes in tests.
	openFile      = os.OpenFile
	ioctlRetInt   = unix.IoctlRetInt
	ioctlSetInt   = unix.IoctlSetInt
	ioctlLoopInfo = unix.IoctlLoopSetStatus64
	closeFile     = func(f *os.File) error { return f.Close() }
	readFileBytes = os.ReadFile

	// attachMu serialises Attach calls across all Loop instances to
	// prevent the TOCTOU race between LOOP_CTL_GET_FREE and LOOP_SET_FD.
	attachMu sync.Mutex
)

// Loop manages the lifecycle of a Linux loop device backed by an image file.
// All methods are safe for concurrent use.
type Loop struct {
	mu       sync.Mutex
	Path     string // image file path, immutable after construction
	device   string // loop device path (e.g. /dev/loop3), set by Attach, cleared by Detach
	attached bool   // true after a successful Attach, false after Detach
}

// NewLoop returns a Loop for the given image path.
// Use Attach() to associate it with a loop device.
func NewLoop(imagePath string) (*Loop, error) {
	return &Loop{Path: imagePath}, nil
}

// NewLoopFromDevice returns a Loop for an already-attached loop device.
// Use BackingFile() / Detach() to query or release it.
func NewLoopFromDevice(device string) *Loop {
	return &Loop{device: device, attached: true}
}

// DevicePath returns the currently attached loop device path (e.g. /dev/loop3),
// or an empty string if the loop is not attached.
func (l *Loop) DevicePath() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.device
}

// BackingFile returns the kernel-reported backing file for the loop device
// by reading /sys/block/loopN/loop/backing_file.
// Returns an empty string when the device has no backing file.
func (l *Loop) BackingFile() string {
	if l == nil {
		return ""
	}
	l.mu.Lock()
	dev := l.device
	l.mu.Unlock()

	if dev == "" {
		return ""
	}
	base := filepath.Base(dev) // "loop0"
	p := filepath.Join(sysBlockPrefix, base, "loop", "backing_file")
	data, err := readFileBytes(p)
	if err != nil {
		return ""
	}
	path := strings.TrimSpace(string(data))
	if path == "" {
		return ""
	}

	return path
}

// detachLoopFd issues LOOP_CLR_FD on fd, retrying briefly on transient EBUSY
// (e.g. while udev finishes probing partitions after unmount/LUKS close).
func detachLoopFd(fd int) error {
	var detachErr error
	for attempt := range loopDetachMaxRetries {
		detachErr = ioctlSetInt(fd, unix.LOOP_CLR_FD, 0)
		if detachErr == nil {
			return nil
		}
		if !errors.Is(detachErr, unix.EBUSY) || attempt == loopDetachMaxRetries-1 {
			return detachErr
		}
		time.Sleep(loopRetryDelay)
	}
	return detachErr
}

// Detach clears the file-descriptor association of the loop device,
// equivalent to `losetup -d /dev/loopN`. Retries briefly on transient EBUSY
// (e.g. while udev finishes probing partitions after unmount/LUKS close).
// Returns an error if the device was never attached or already detached.
func (l *Loop) Detach() error {
	if l == nil {
		return fmt.Errorf("loop detach: nil loop device")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	if !l.attached {
		return fmt.Errorf("loop detach: device not attached")
	}

	f, err := openFile(l.device, os.O_RDONLY, 0)
	if err != nil {
		return fmt.Errorf("loop detach: open %s: %w", l.device, err)
	}
	defer closeFile(f)

	if detachErr := detachLoopFd(int(f.Fd())); detachErr != nil {
		return fmt.Errorf("loop detach: LOOP_CLR_FD on %s: %w", l.device, detachErr)
	}

	l.device = ""
	l.attached = false
	return nil
}

// Attach associates the image at l.Path with the next free loop device
// (with partition scanning enabled), equivalent to
// `losetup --show -fP <imagePath>`.
// On success DevicePath() returns the allocated device path.
// Retries on EBUSY from LOOP_SET_FD to tolerate cross-process races
// between LOOP_CTL_GET_FREE and LOOP_SET_FD.
// Returns an error if already attached.
func (l *Loop) Attach() error {
	if l == nil {
		return fmt.Errorf("loop attach: nil loop device")
	}
	l.mu.Lock()
	defer l.mu.Unlock()

	attachMu.Lock()
	defer attachMu.Unlock()

	if l.attached {
		return fmt.Errorf("loop attach: already attached to %s", l.device)
	}

	var imgFile *os.File
	defer func() {
		if imgFile != nil {
			closeFile(imgFile)
		}
	}()

	var lastErr error
	for range loopAttachMaxRetries {
		// 1. Get a free loop device number from /dev/loop-control.
		ctl, err := openFile(loopControlPath, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("loop attach: open %s: %w", loopControlPath, err)
		}
		devNr, err := ioctlRetInt(int(ctl.Fd()), unix.LOOP_CTL_GET_FREE)
		closeFile(ctl)
		if err != nil {
			return fmt.Errorf("loop attach: LOOP_CTL_GET_FREE: %w", err)
		}

		loopPath := fmt.Sprintf("%s/loop%d", devPrefix, devNr)

		// 2. Open the backing image file once and reuse across EBUSY retries.
		if imgFile == nil {
			imgFile, err = openFile(l.Path, os.O_RDWR, 0)
			if err != nil {
				return fmt.Errorf("loop attach: open image %s: %w", l.Path, err)
			}
		}

		// 3. Open the loop device and associate the image fd with LOOP_SET_FD.
		loopFile, err := openFile(loopPath, os.O_RDWR, 0)
		if err != nil {
			return fmt.Errorf("loop attach: open %s: %w", loopPath, err)
		}

		loopFd := int(loopFile.Fd())
		imgFileFd := int(imgFile.Fd())

		if err := ioctlSetInt(loopFd, unix.LOOP_SET_FD, imgFileFd); err != nil {
			closeFile(loopFile)
			if errors.Is(err, unix.EBUSY) {
				lastErr = fmt.Errorf("loop attach: LOOP_SET_FD on %s: %w", loopPath, err)
				continue
			}
			return fmt.Errorf("loop attach: LOOP_SET_FD on %s: %w", loopPath, err)
		}

		// 4. Enable partition scanning via LOOP_SET_STATUS64, retrying on
		// transient EAGAIN/EBUSY while the kernel/udev settles.
		info := unix.LoopInfo64{
			Flags: unix.LO_FLAGS_PARTSCAN,
		}
		var statusErr error
		for attempt := range loopSetStatusMaxRetries {
			statusErr = ioctlLoopInfo(loopFd, &info)
			if statusErr == nil {
				break
			}
			if (!errors.Is(statusErr, unix.EAGAIN) && !errors.Is(statusErr, unix.EBUSY)) || attempt == loopSetStatusMaxRetries-1 {
				break
			}
			time.Sleep(loopRetryDelay)
		}
		if statusErr != nil {
			// Best-effort detach on failure (retrying transient EBUSY).
			beErr := detachLoopFd(loopFd)
			closeFile(loopFile)
			if beErr != nil {
				return fmt.Errorf(
					"loop attach: LOOP_SET_STATUS64 on %s: %v (also failed to detach: %v)",
					loopPath,
					statusErr,
					beErr,
				)
			}
			return fmt.Errorf("loop attach: LOOP_SET_STATUS64 on %s: %w", loopPath, statusErr)
		}

		closeFile(loopFile)
		l.device = loopPath
		l.attached = true
		return nil
	}

	return lastErr
}
