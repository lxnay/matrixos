package imager

import (
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"syscall"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"

	"matrixos/vector/lib/ostree"
)

type ImageMode int

const (
	ModeFlashToDevice ImageMode = iota
	ModeCreateImageFile
)

// NewImagerOptions contains device configuration for image creation.
type NewImagerOptions struct {
	EfiDevice  string
	BootDevice string
	RootDevice string
	DevicePath string
	Mode       ImageMode
}

// IImager defines the interface for image operations used by command-level
// callers. Only methods that are called through IImager-typed variables
// appear here; the remaining public methods live on the concrete *Imager.
type IImager interface {
	// SetStdout replaces the writer used for informational output.
	SetStdout(w io.Writer)
	// SetStderr replaces the writer used for warnings and errors.
	SetStderr(w io.Writer)

	// Print writes a formatted informational message to stdout.
	Print(format string, args ...any)
	// PrintWarning writes a formatted warning message to stderr.
	PrintWarning(format string, args ...any)
	// PrintError writes a formatted error/diagnostic message to stderr.
	PrintError(format string, args ...any)

	// SetRef sets the ostree ref.
	SetRef(ref string)

	// Build implements the core image setup logic. It partitions, formats,
	// mounts, deploys ostree, installs the bootloader, and performs
	// post-processing (productionization, compression, signing).
	Build(opts *BuildOptions) error
	// Cleanup unmounts all mount points tracked by this Imager instance in
	// reverse order, detaches loop devices, syncs, and removes temp dirs.
	// It is safe to call multiple times.
	Cleanup()
	// ExecuteWithImageLock acquires an exclusive file lock for the given ref,
	// executes fn under that lock, and releases the lock when fn returns.
	ExecuteWithImageLock(fn func() error) error
}

// Imager provides image creation and manipulation operations.
type Imager struct {
	*ImagerConfig
	ostree              ostree.IOstree
	fsenc               filesystems.IFsenc
	runner              runner.Func
	chrootRunner        runner.ChrootRunFunc
	bootloader          Bootloader
	stdout              io.Writer
	stderr              io.Writer
	efiDevice           string
	bootDevice          string
	rootDevice          string
	realRootDevice      string // if encrypted, devicePath is replaced.
	devicePath          string
	imagePath           string
	compressedImagePath string
	mode                ImageMode
	rootfs              string
	ref                 string
	encrypted           bool

	// Mount points, set by Mount* methods on success.
	efifsMount  string
	bootfsMount string
	rootfsMount string

	// QA validation instance.
	qa *validation.QA

	// trackedMounts records every mount point created by this Imager
	// so that Cleanup can attempt to unmount them all on failure or signal.
	trackedMountsMu      sync.Mutex
	trackedMounts        []string
	trackedTmpDirsMu     sync.Mutex
	trackedTmpDirs       []string
	trackedLoopDevicesMu sync.Mutex
	trackedLoopDevices   []*filesystems.Loop
}

func (im *Imager) SetStdout(w io.Writer) { im.stdout = w }

func (im *Imager) SetStderr(w io.Writer) { im.stderr = w }

func (im *Imager) Stdout() io.Writer { return im.stdout }

func (im *Imager) Stderr() io.Writer { return im.stderr }

func (im *Imager) Print(format string, args ...any) {
	fmt.Fprintf(im.stdout, format, args...)
}

func (im *Imager) PrintWarning(format string, args ...any) {
	fmt.Fprintf(im.stderr, format, args...)
}

func (im *Imager) PrintError(format string, args ...any) {
	fmt.Fprintf(im.stderr, format, args...)
}

// trackMount appends a single mount point to the tracked list.
func (im *Imager) trackMount(mnt string) {
	im.trackedMountsMu.Lock()
	defer im.trackedMountsMu.Unlock()
	im.trackedMounts = append(im.trackedMounts, mnt)
}

// trackMounts appends multiple mount points to the tracked list.
func (im *Imager) trackMounts(mnts []string) {
	im.trackedMountsMu.Lock()
	defer im.trackedMountsMu.Unlock()
	im.trackedMounts = append(im.trackedMounts, mnts...)
}

// trackTmpDir appends a single temporary directory to the tracked list.
func (im *Imager) trackTmpDir(tmpDir string) {
	im.trackedTmpDirsMu.Lock()
	defer im.trackedTmpDirsMu.Unlock()
	im.trackedTmpDirs = append(im.trackedTmpDirs, tmpDir)
}

// trackLoopDevice appends a loop device to the tracked list.
func (im *Imager) trackLoopDevice(loop *filesystems.Loop) {
	im.trackedLoopDevicesMu.Lock()
	defer im.trackedLoopDevicesMu.Unlock()
	im.trackedLoopDevices = append(im.trackedLoopDevices, loop)
}

func (im *Imager) Cleanup() {
	im.trackedMountsMu.Lock()
	mounts := slices.Clone(im.trackedMounts)
	im.trackedMounts = nil
	im.trackedMountsMu.Unlock()

	copts := filesystems.CleanupMountsOptions{
		Stdout: im.stdout,
		Stderr: im.stderr,
		Mounts: mounts,
	}
	filesystems.CleanupMounts(copts)

	im.trackedLoopDevicesMu.Lock()
	loops := slices.Clone(im.trackedLoopDevices)
	im.trackedLoopDevices = nil
	im.trackedLoopDevicesMu.Unlock()

	for _, loop := range loops {
		if err := loop.Detach(); err != nil {
			fmt.Fprintf(
				im.stderr,
				"warning: failed to detach loop device %s (path: %s): %v\n",
				loop.Device,
				loop.Path,
				err,
			)
		}
	}

	// Sync buffers for image files after umount.
	syscall.Sync()

	im.trackedTmpDirsMu.Lock()
	tmpDirs := slices.Clone(im.trackedTmpDirs)
	im.trackedTmpDirs = nil
	im.trackedTmpDirsMu.Unlock()

	for _, tmpDir := range tmpDirs {
		fmt.Fprintf(im.stdout, "Removing temp dir %s\n", tmpDir)
		if err := os.RemoveAll(tmpDir); err != nil {
			fmt.Fprintf(im.stderr, "Warning: failed to remove temp dir %s: %v\n", tmpDir, err)
		}
	}
}

// NewImager creates a new Imager instance.
func NewImager(cfg config.IConfig, ot ostree.IOstree, fsenc filesystems.IFsenc, opts *NewImagerOptions) (*Imager, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	if ot == nil {
		return nil, errors.New("missing ostree parameter")
	}
	if fsenc == nil {
		return nil, errors.New("missing fsenc parameter")
	}
	encrypted, err := fsenc.EncryptionEnabled()
	if err != nil {
		return nil, fmt.Errorf("failed to check if encryption is enabled: %w", err)
	}

	qa, err := validation.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize QA: %w", err)
	}

	im := &Imager{
		ImagerConfig: NewImagerConfig(cfg),
		ostree:       ot,
		fsenc:        fsenc,
		qa:           qa,
		runner:       runner.Run,
		chrootRunner: runner.ChrootRun,
		stdout:       os.Stdout,
		stderr:       os.Stderr,
	}
	bootloaderName, err := im.Bootloader()
	if err != nil {
		return nil, fmt.Errorf("failed to read bootloader config: %w", err)
	}
	switch bootloaderName {
	case "grub":
		im.bootloader = NewGrubBootloader(im)
	case "systemd-boot":
		im.bootloader = NewSystemdBootBootloader(im)
	default:
		return nil, fmt.Errorf("unknown bootloader %q: must be \"grub\" or \"systemd-boot\"", bootloaderName)
	}
	if opts != nil {
		im.efiDevice = opts.EfiDevice
		im.bootDevice = opts.BootDevice
		im.rootDevice = opts.RootDevice
		im.devicePath = opts.DevicePath
		im.encrypted = encrypted
		im.mode = opts.Mode
	}
	return im, nil
}

func (im *Imager) SetEfiDevice(device string) { im.efiDevice = device }

func (im *Imager) EfiDevice() string { return im.efiDevice }

func (im *Imager) SetBootDevice(device string) { im.bootDevice = device }

func (im *Imager) BootDevice() string { return im.bootDevice }

func (im *Imager) SetRootDevice(device string) { im.rootDevice = device }

func (im *Imager) RootDevice() string { return im.rootDevice }

func (im *Imager) SetDevicePath(devicePath string) { im.devicePath = devicePath }

func (im *Imager) DevicePath() string { return im.devicePath }

func (im *Imager) SetRootfs(rootfs string) { im.rootfs = rootfs }

func (im *Imager) SetImagePath(imagePath string) { im.imagePath = imagePath }

func (im *Imager) ImagePath() string { return im.imagePath }

func (im *Imager) SetImageMode(mode ImageMode) error {
	switch mode {
	case ModeFlashToDevice:
		if im.devicePath == "" {
			return errors.New("devicePath must be set for ModeFlashToDevice")
		}
	case ModeCreateImageFile:
		if im.imagePath == "" {
			return errors.New("imagePath must be set for ModeCreateImageFile")
		}
	default:
		return errors.New("invalid image mode")
	}

	im.mode = mode
	return nil
}

func (im *Imager) ImageMode() ImageMode { return im.mode }

func (im *Imager) Rootfs() string { return im.rootfs }

func (im *Imager) Ref() string { return im.ref }

func (im *Imager) SetRef(ref string) { im.ref = ref }

func (im *Imager) EfifsMount() string { return im.efifsMount }

func (im *Imager) BootfsMount() string { return im.bootfsMount }

func (im *Imager) RootfsMount() string { return im.rootfsMount }
