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
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
	"matrixos/vector/lib/validation"
	"matrixos/vector/lib/worker"
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

// Compile-time interface check.
var _ IImager = (*Imager)(nil)

// Imager provides image creation and manipulation operations.
type Imager struct {
	*ImagerConfig
	worker.IO
	ostree         ostree.IOstree
	fsenc          filesystems.IFsenc
	runner         runner.Func
	chrootRunner   runner.ChrootRunFunc
	bootloader     Bootloader
	efiDevice      string
	bootDevice     string
	rootDevice     string
	realRootDevice string // if encrypted, devicePath is replaced.
	devicePath     string
	imagePath      string
	mode           ImageMode
	rootfs         string
	ref            string
	encrypted      bool

	// Mount points, set by Mount* methods on success.
	efifsMount  string
	bootfsMount string
	rootfsMount string

	// QA validation instance.
	qa *validation.QA

	// cleanupMu serializes Cleanup() calls so concurrent callers (e.g.
	// SignalGuard and finalizeBuild/defer) never remove temp dirs or close
	// LUKS/loop devices while an earlier Cleanup() is still unmounting.
	cleanupMu sync.Mutex

	// trackMu guards trackedMounts, trackedTmpDirs, trackedLoopDevices,
	// cleaning, and cleanupGen. Lock order: cleanupMu -> trackMu.
	trackMu            sync.Mutex
	trackedMounts      []string
	trackedTmpDirs     []string
	trackedLoopDevices []*filesystems.Loop
	cleaning           bool
	cleanupGen         uint64

	// inFlightOps tracks mountPartition / setupImageFile operations that were
	// admitted while !cleaning && cleanupGen == 0. Add(1) happens strictly
	// under trackMu before Cleanup() sets cleaning=true/cleanupGen++ and calls
	// Wait(), preventing Cleanup() from racing past an active mount(2) or
	// loop.Attach() to delete tmpDirs or exit early.
	inFlightOps sync.WaitGroup
}

// trackMount appends a single mount point to the tracked list.
func (im *Imager) trackMount(mnt string) {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	im.trackedMounts = append(im.trackedMounts, mnt)
}

// trackMountIfOpen appends a mount point, increments inFlightOps, and reports
// true only if Cleanup() has not started or already completed on this Imager.
// When it returns true, the caller MUST invoke finishTrackedOp() before
// returning or calling Cleanup().
func (im *Imager) trackMountIfOpen(mnt string) bool {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	if im.cleaning || im.cleanupGen > 0 {
		return false
	}
	im.trackedMounts = append(im.trackedMounts, mnt)
	im.inFlightOps.Add(1)
	return true
}

// finishTrackedOp marks a mountPartition or setupImageFile operation admitted
// by trackMountIfOpen / trackLoopDeviceIfOpen as complete.
func (im *Imager) finishTrackedOp() {
	im.inFlightOps.Done()
}

// trackTmpDir appends a single temporary directory to the tracked list.
func (im *Imager) trackTmpDir(tmpDir string) {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	im.trackedTmpDirs = append(im.trackedTmpDirs, tmpDir)
}

// untrackTmpDir removes a temporary directory from the tracked list once its
// owner has already cleaned it up.
func (im *Imager) untrackTmpDir(tmpDir string) {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	for i, d := range im.trackedTmpDirs {
		if d == tmpDir {
			im.trackedTmpDirs = append(im.trackedTmpDirs[:i], im.trackedTmpDirs[i+1:]...)
			return
		}
	}
}

// trackLoopDevice appends a loop device to the tracked list.
func (im *Imager) trackLoopDevice(loop *filesystems.Loop) {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	im.trackedLoopDevices = append(im.trackedLoopDevices, loop)
}

// trackLoopDeviceIfOpen appends a loop device, increments inFlightOps, and
// reports true only if Cleanup() has not started or already completed.
// When it returns true, the caller MUST invoke finishTrackedOp() before
// returning or calling Cleanup().
func (im *Imager) trackLoopDeviceIfOpen(loop *filesystems.Loop) bool {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	if im.cleaning || im.cleanupGen > 0 {
		return false
	}
	im.trackedLoopDevices = append(im.trackedLoopDevices, loop)
	im.inFlightOps.Add(1)
	return true
}

// beginTrackedOp increments inFlightOps and reports true only if Cleanup() has
// not started or already completed. When it returns true, the caller MUST
// invoke finishTrackedOp() before returning or calling Cleanup().
func (im *Imager) beginTrackedOp() bool {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	if im.cleaning || im.cleanupGen > 0 {
		return false
	}
	im.inFlightOps.Add(1)
	return true
}

// cleanupActiveOrRan reports whether Cleanup() is currently running or has
// already run at least once on this Imager instance.
func (im *Imager) cleanupActiveOrRan() bool {
	im.trackMu.Lock()
	defer im.trackMu.Unlock()
	return im.cleaning || im.cleanupGen > 0
}

func (im *Imager) Cleanup() {
	im.cleanupMu.Lock()
	defer im.cleanupMu.Unlock()

	im.trackMu.Lock()
	im.cleaning = true
	im.cleanupGen++
	im.trackMu.Unlock()

	defer func() {
		im.trackMu.Lock()
		im.cleaning = false
		im.cleanupGen++
		im.trackMu.Unlock()
	}()

	// Wait for any already-admitted mount(2) or loop.Attach() to complete
	// before unmounting, closing LUKS, detaching loops, or removing tmpDirs.
	im.inFlightOps.Wait()

	var failedLoops []*filesystems.Loop
	for {
		im.trackMu.Lock()
		mounts := slices.Clone(im.trackedMounts)
		im.trackedMounts = nil
		im.trackMu.Unlock()

		if len(mounts) > 0 {
			copts := filesystems.CleanupMountsOptions{
				Stdout: im.Stdout(),
				Stderr: im.Stderr(),
				Mounts: mounts,
			}
			filesystems.CleanupMounts(copts)
		}

		// Close LUKS mappers after unmounting filesystems and before detaching
		// the underlying loop devices so dm-crypt releases the backing image file.
		if im.fsenc != nil {
			im.fsenc.Cleanup()
		}

		// Retry any loops that failed on an earlier pass alongside newly
		// tracked loops now that newly drained mounts/LUKS mappers are closed.
		im.trackMu.Lock()
		loops := append(failedLoops, im.trackedLoopDevices...)
		im.trackedLoopDevices = nil
		im.trackMu.Unlock()
		failedLoops = nil

		for _, loop := range loops {
			dev := loop.DevicePath()
			if dev == "" {
				continue
			}
			if err := loop.Detach(); err != nil {
				im.PrintWarning(
					"warning: failed to detach loop device %s (path: %s): %v\n",
					dev,
					loop.Path,
					err,
				)
				if loop.DevicePath() != "" {
					failedLoops = append(failedLoops, loop)
				}
			}
		}

		// Sync buffers for image files after umount/detach.
		syscall.Sync()

		// Re-drain only if new mounts or loop devices were tracked during this pass.
		im.trackMu.Lock()
		moreWork := len(im.trackedMounts) > 0 || len(im.trackedLoopDevices) > 0
		im.trackMu.Unlock()
		if !moreWork {
			break
		}
	}
	if len(failedLoops) > 0 {
		im.trackMu.Lock()
		im.trackedLoopDevices = append(failedLoops, im.trackedLoopDevices...)
		im.trackMu.Unlock()
	}

	im.trackMu.Lock()
	tmpDirs := slices.Clone(im.trackedTmpDirs)
	im.trackedTmpDirs = nil
	im.trackMu.Unlock()

	var failedTmpDirs []string
	for _, tmpDir := range tmpDirs {
		if err := filesystems.CheckActiveMounts(tmpDir); err != nil {
			im.PrintWarning("Warning: skipping removal of temp dir %s: %v\n", tmpDir, err)
			failedTmpDirs = append(failedTmpDirs, tmpDir)
			continue
		}
		im.Print("Removing temp dir %s\n", tmpDir)
		if err := os.RemoveAll(tmpDir); err != nil {
			im.PrintWarning("Warning: failed to remove temp dir %s: %v\n", tmpDir, err)
			failedTmpDirs = append(failedTmpDirs, tmpDir)
		}
	}
	if len(failedTmpDirs) > 0 {
		im.trackMu.Lock()
		im.trackedTmpDirs = append(failedTmpDirs, im.trackedTmpDirs...)
		im.trackMu.Unlock()
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
		encrypted:    encrypted,
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
