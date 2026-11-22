package filesystems

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"matrixos/vector/lib/runner"
)

var (
	execRun          runner.Func             = runner.Run
	ExecChrootOutput runner.ChrootOutputFunc = runner.ChrootOutput
	devMapperPrefix                          = "/dev/mapper"
	sysIoctl                                 = unix.Syscall
	sysLsetxattr                             = unix.Lsetxattr
	sysLgetxattr                             = unix.Lgetxattr
	sysLlistxattr                            = unix.Llistxattr

	// Mockable paths for block-device sysfs queries.
	sysClassBlockPath = "/sys/class/block"
)

// PathMode represents the mode of a path.
type PathMode struct {
	Type   string      // E.g., "-", "d", "l"
	SetUID bool        // Set-user-ID bit
	SetGID bool        // Set-group-ID bit
	Sticky bool        // Sticky bit
	Perms  fs.FileMode // Stored as uint32, printed as octal
}

// PathInfo represents the information of a path in an ostree commit.
type PathInfo struct {
	Mode           *PathMode // Mode information of the path
	Uid            uint64    // User ID of the owner
	Gid            uint64    // Group ID of the owner
	Size           uint64    // Size of the file in bytes
	OSTreeChecksum string    // Checksum of the path if regular file
	Path           string    // Full path of the file
	Link           string    // Target of the symlink if Type is "l"
}

// Equals compares two PathInfo entries for metadata equality:
// type, permission bits, uid, gid, size, symlink target and checksums.
func (a *PathInfo) Equals(b *PathInfo) bool {
	if a.Mode.Type != b.Mode.Type {
		return false
	}
	if a.Mode.Perms != b.Mode.Perms {
		return false
	}
	if a.Mode.SetUID != b.Mode.SetUID || a.Mode.SetGID != b.Mode.SetGID || a.Mode.Sticky != b.Mode.Sticky {
		return false
	}
	if a.Uid != b.Uid || a.Gid != b.Gid {
		return false
	}
	if a.Mode.Type == "-" && a.Size != b.Size {
		return false
	}
	if a.Link != b.Link {
		return false
	}
	aCksum := "0"
	bCksum := "0"
	if a.Mode.Type == "-" {
		aCksum = a.OSTreeChecksum
	}
	if b.Mode.Type == "-" {
		bCksum = b.OSTreeChecksum
	}
	if aCksum != bCksum {
		return false
	}
	return true
}

// String returns a short human-readable description of a PathInfo.
func (pi *PathInfo) String() string {
	if pi == nil {
		return "(absent)"
	}
	typ := "file"
	switch pi.Mode.Type {
	case "d":
		typ = "dir"
	case "l":
		typ = fmt.Sprintf("link -> %s", pi.Link)
	}
	return fmt.Sprintf("%s %04o uid=%d gid=%d size=%d, csum=%s",
		typ, pi.Mode.Perms, pi.Uid, pi.Gid, pi.Size, pi.OSTreeChecksum)
}

// ListContents lists the contents of a path on the filesystem.
// It walks the directory tree recursively and returns information
// about regular files, directories, and symlinks, ignoring everything else.
func ListContents(path string) ([]*PathInfo, error) {
	if path == "" {
		return nil, fmt.Errorf("missing path parameter")
	}

	otRegFileChecksum := func(p string) string {
		ck, err := OstreeChecksumFileAt(p, OstreeObjectTypeFile, OstreeChecksumFlagsNone)
		if err != nil {
			log.Printf("WARNING: failed to compute OSTree checksum for %s: %v. Using dummy checksum.\n", p, err)
			return "0"
		}
		return ck
	}

	var pis []*PathInfo

	err := filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		mode := info.Mode()
		ft := mode.Type()

		var typeStr string
		var otChksum string
		switch {
		case ft.IsRegular():
			typeStr = "-"
			otChksum = otRegFileChecksum(p)
		case ft.IsDir():
			typeStr = "d"
		case ft&fs.ModeSymlink != 0:
			typeStr = "l"
		default:
			// Ignore anything that is not a regular file, directory, or symlink
			return nil
		}

		pm := &PathMode{
			Type:   typeStr,
			SetUID: mode&fs.ModeSetuid != 0,
			SetGID: mode&fs.ModeSetgid != 0,
			Sticky: mode&fs.ModeSticky != 0,
			Perms:  mode.Perm(),
		}

		pi := &PathInfo{
			Mode:           pm,
			Size:           uint64(info.Size()),
			Path:           p,
			OSTreeChecksum: otChksum,
		}

		// Get UID/GID from the underlying syscall stat
		if stat, ok := info.Sys().(*syscall.Stat_t); ok {
			pi.Uid = uint64(stat.Uid)
			pi.Gid = uint64(stat.Gid)
		}

		// Resolve symlink target
		if typeStr == "l" {
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			pi.Link = target
		}

		pis = append(pis, pi)
		return nil
	})
	if err != nil {
		return nil, err
	}

	return pis, nil
}

// DevicesSettle waits for udev events to settle.
func DevicesSettle() {
	execRun(&runner.Cmd{
		Name: "udevadm",
		Args: []string{"settle"},
	})
}

// FlushBlockDeviceBuffers flushes the buffers of a block device.
func FlushBlockDeviceBuffers(devPath string) error {
	if devPath == "" {
		return fmt.Errorf("missing devPath parameter")
	}

	f, err := os.Open(devPath)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, _, errno := sysIoctl(unix.SYS_IOCTL, f.Fd(), uintptr(unix.BLKFLSBUF), 0); errno != 0 {
		return fmt.Errorf("ioctl BLKFLSBUF failed: %w", errno)
	}
	return nil
}

// GetLuksRootfsDevicePath returns the device path for a given LUKS name.
func GetLuksRootfsDevicePath(luksName string) (string, error) {
	if luksName == "" {
		return "", fmt.Errorf("missing luksName parameter")
	}
	return filepath.Join(devMapperPrefix, luksName), nil
}

// DeviceUUID returns the UUID of a given device path.
func DeviceUUID(devPath string) (string, error) {
	if devPath == "" {
		return "", fmt.Errorf("missing argument devpath")
	}
	return resolveDeviceAttribute(devPath, "UUID")
}

// DevicePartUUID returns the PARTUUID of a given device path.
func DevicePartUUID(devPath string) (string, error) {
	if devPath == "" {
		return "", fmt.Errorf("missing argument devpath")
	}
	return resolveDeviceAttribute(devPath, "PARTUUID")
}

// PathExists returns true if the path exists (file, directory, or other).
func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// FileExists returns true if path exists and is a regular file.
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// DirectoryExists returns true if path exists and is a directory.
func DirectoryExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// CheckDirIsRoot checks if a directory is the root of the filesystem and exits if it is.
func CheckDirIsRoot(chrootDir string) error {
	if chrootDir == "" {
		return fmt.Errorf("missing chrootDir parameter")
	}
	rootStat, err := os.Stat("/")
	if err != nil {
		return err
	}
	chrootStat, err := os.Stat(chrootDir)
	if err != nil {
		return err
	}
	if os.SameFile(rootStat, chrootStat) {
		return fmt.Errorf("CRITICAL ERROR: %s IS MAPPED TO HOST ROOT. ABORTING", chrootDir)
	}
	return nil
}

// CreateTempDir creates a temporary directory.
func CreateTempDir(parentDir, prefix string) (string, error) {
	if parentDir == "" {
		return "", fmt.Errorf("missing parentDir parameter")
	}
	if prefix == "" {
		prefix = "tmp"
	}
	// os.MkdirTemp is the replacement for ioutil.TempDir
	tempDir, err := os.MkdirTemp(parentDir, prefix)
	if err != nil {
		return "", fmt.Errorf("failed to create temporary directory: %w", err)
	}
	return tempDir, nil
}

// CreateTempFile creates a temporary file.
func CreateTempFile(parentDir, prefix string) (*os.File, error) {
	if parentDir == "" {
		return nil, fmt.Errorf("missing parentDir parameter")
	}
	if prefix == "" {
		prefix = "tmp"
	}
	// os.CreateTemp is the replacement for ioutil.TempFile
	tempFile, err := os.CreateTemp(parentDir, prefix)
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary file: %w", err)
	}
	return tempFile, nil
}

// RemoveFileWithGlobOptions holds options for RemoveFileWithGlob.
type RemoveFileWithGlobOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

// RemoveFileWithGlob removes files matching a glob pattern.
func RemoveFileWithGlob(target string, opts RemoveFileWithGlobOptions) error {
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}

	matches, err := filepath.Glob(target)
	if err != nil {
		return err
	}

	if len(matches) == 0 {
		fmt.Fprintf(stdout, "Removing (glob): %s does not exist\n", target)
	}

	for _, match := range matches {
		fmt.Fprintf(stdout, "Removing file (glob): %s\n", match)
		err := os.Remove(match)
		if err != nil {
			return err
		}
	}
	return nil
}

// RemoveDirOptions holds options for RemoveDir.
type RemoveDirOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

// RemoveDir removes a directory (or symlink at target).
func RemoveDir(target string, opts RemoveDirOptions) error {
	if target == "" {
		return fmt.Errorf("missing target parameter")
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	info, err := os.Lstat(target)
	if os.IsNotExist(err) {
		fmt.Fprintf(stdout, "Removing: %s does not exist\n", target)
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink == 0 {
		if err := CheckDirIsRoot(target); err != nil {
			return err
		}
	}
	fmt.Fprintf(stdout, "Removing %s\n", target)
	return os.RemoveAll(target)
}

// EmptyDirOptions holds options for EmptyDir.
type EmptyDirOptions struct {
	Stdout io.Writer
	Stderr io.Writer
}

// EmptyDir empties a directory.
func EmptyDir(target string, opts EmptyDirOptions) error {
	if target == "" {
		return fmt.Errorf("missing target parameter")
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		fmt.Fprintf(stdout, "Emptying: %s does not exist\n", target)
		return nil
	}
	if err := CheckDirIsRoot(target); err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Emptying directory %s\n", target)

	entries, err := os.ReadDir(target)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(target, entry.Name())
		if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	return nil
}

// DirEmpty checks if a directory is empty.
func DirEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()

	_, err = f.ReadDir(1)
	if err == io.EOF {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

// ChrootOutput runs a command in a chroot environment using unshare
// and returns its standard output.
func ChrootOutput(chrootDir, chrootExec string, args ...string) ([]byte, error) {
	return ExecChrootOutput(&runner.ChrootCmd{
		Cmd:       runner.Cmd{Name: chrootExec, Args: args},
		ChrootDir: chrootDir,
	})
}

// BlockDeviceNthPartition returns the device path of the nth partition
// (1-based) on a block device by scanning sysfs for child partitions.
func BlockDeviceNthPartition(blockDevice string, nth int) (string, error) {
	if blockDevice == "" {
		return "", fmt.Errorf("missing blockDevice parameter")
	}
	if nth <= 0 {
		return "", fmt.Errorf("invalid nth parameter: %d", nth)
	}

	parentBase := filepath.Base(blockDevice)
	parentSysfs := filepath.Join(sysClassBlockPath, parentBase)

	entries, err := os.ReadDir(parentSysfs)
	if err != nil {
		return "", fmt.Errorf("cannot read sysfs for %s: %w", blockDevice, err)
	}

	nthStr := strconv.Itoa(nth)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// Partition directories start with the parent device name.
		if !strings.HasPrefix(e.Name(), parentBase) {
			continue
		}
		// Read the partition number from sysfs.
		partFile := filepath.Join(parentSysfs, e.Name(), "partition")
		data, err := readFileBytes(partFile)
		if err != nil {
			continue
		}
		if strings.TrimSpace(string(data)) == nthStr {
			return filepath.Join(filepath.Dir(blockDevice), e.Name()), nil
		}
	}

	return "", fmt.Errorf("partition %d not found on %s", nth, blockDevice)
}

// BlockDeviceForPartition returns the parent block device for a partition
// path by resolving the partition's sysfs entry and walking up to the
// parent device.
func BlockDeviceForPartition(partitionPath string) (string, error) {
	if partitionPath == "" {
		return "", fmt.Errorf("missing partitionPath parameter")
	}

	partBase := filepath.Base(partitionPath)
	partSysfs := filepath.Join(sysClassBlockPath, partBase)

	// Verify this is actually a partition by checking the "partition" file.
	partFile := filepath.Join(partSysfs, "partition")
	if _, err := readFileBytes(partFile); err != nil {
		return "", fmt.Errorf("not a partition or cannot read sysfs for %s: %w", partitionPath, err)
	}

	// The real sysfs path for a partition is:
	//   /sys/devices/.../sdX/sdX1
	// Resolving the sysfs symlink and going up one directory gives the parent.
	realPath, err := filepath.EvalSymlinks(partSysfs)
	if err != nil {
		return "", fmt.Errorf("cannot resolve sysfs path for %s: %w", partitionPath, err)
	}

	parentSysfs := filepath.Dir(realPath)
	parentName := filepath.Base(parentSysfs)

	// Verify the parent is a valid block device.
	parentDev := filepath.Join(filepath.Dir(partitionPath), parentName)
	return parentDev, nil
}

// PartitionNumber returns the partition number of a partition device
// by reading its sysfs "partition" attribute.
func PartitionNumber(partitionPath string) (string, error) {
	if partitionPath == "" {
		return "", fmt.Errorf("missing partitionPath parameter")
	}

	partBase := filepath.Base(partitionPath)
	partFile := filepath.Join(sysClassBlockPath, partBase, "partition")
	data, err := readFileBytes(partFile)
	if err != nil {
		return "", fmt.Errorf("cannot read partition number for %s: %w", partitionPath, err)
	}

	return strings.TrimSpace(string(data)), nil
}

// PartitionLabel returns the filesystem label of a partition device.
func PartitionLabel(partitionPath string) (string, error) {
	if partitionPath == "" {
		return "", fmt.Errorf("missing partitionPath parameter")
	}

	label, err := resolveDeviceAttribute(partitionPath, "LABEL")
	if err != nil {
		// No label is not necessarily an error; some partitions have none.
		return "", nil
	}
	return label, nil
}

// PartitionType returns the partition type GUID (uppercased) for a
// partition device.
func PartitionType(partitionPath string) (string, error) {
	if partitionPath == "" {
		return "", fmt.Errorf("missing partitionPath parameter")
	}

	partType, err := resolveDeviceAttribute(partitionPath, "PARTTYPE")
	if err != nil {
		return "", fmt.Errorf("cannot determine partition type for %s: %w", partitionPath, err)
	}
	return strings.ToUpper(partType), nil
}

// PrintDirectoryTree walks a directory tree rooted at root and prints every
// path to w, one per line – equivalent to running "find <root>".
func PrintDirectoryTree(w io.Writer, root string) error {
	if root == "" {
		return fmt.Errorf("missing root parameter")
	}
	return filepath.WalkDir(root, func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			// Report inaccessible paths but keep walking.
			fmt.Fprintf(w, "%s [error: %v]\n", path, err)
			return nil
		}
		fmt.Fprintln(w, path)
		return nil
	})
}

// BlockDevicePartitionInfo holds blkid-style attributes for a single device.
type BlockDevicePartitionInfo struct {
	Device   string // e.g. /dev/loop0p1
	UUID     string // filesystem UUID
	PartUUID string // GPT partition UUID
	Label    string // filesystem label
	FSType   string // filesystem type (from mountinfo)
	PartType string // GPT partition type GUID
}

// String formats the info in a blkid-like output line:
//
//	/dev/loop0p1: UUID="..." PARTUUID="..." LABEL="..." TYPE="..." PARTTYPE="..."
func (bi *BlockDevicePartitionInfo) String() string {
	var parts []string
	if bi.UUID != "" {
		parts = append(parts, fmt.Sprintf("UUID=%q", bi.UUID))
	}
	if bi.PartUUID != "" {
		parts = append(parts, fmt.Sprintf("PARTUUID=%q", bi.PartUUID))
	}
	if bi.Label != "" {
		parts = append(parts, fmt.Sprintf("LABEL=%q", bi.Label))
	}
	if bi.FSType != "" {
		parts = append(parts, fmt.Sprintf("TYPE=%q", bi.FSType))
	}
	if bi.PartType != "" {
		parts = append(parts, fmt.Sprintf("PARTTYPE=%q", bi.PartType))
	}
	return fmt.Sprintf("%s: %s", bi.Device, strings.Join(parts, " "))
}

// BlockDeviceInfo collects blkid-style information for all partitions on
// a block device (e.g. /dev/loop0) by querying sysfs and /dev/disk/by-*
// symlinks. The filesystem type is resolved from /proc/self/mountinfo
// when the partition is currently mounted.
func BlockDeviceInfo(blockDevice string) ([]BlockDevicePartitionInfo, error) {
	if blockDevice == "" {
		return nil, fmt.Errorf("missing blockDevice parameter")
	}

	parentBase := filepath.Base(blockDevice)
	parentSysfs := filepath.Join(sysClassBlockPath, parentBase)

	entries, err := os.ReadDir(parentSysfs)
	if err != nil {
		return nil, fmt.Errorf("cannot read sysfs for %s: %w", blockDevice, err)
	}

	var infos []BlockDevicePartitionInfo
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), parentBase) {
			continue
		}
		// Verify it's a partition by checking the "partition" sysfs file.
		partFile := filepath.Join(parentSysfs, e.Name(), "partition")
		if _, err := readFileBytes(partFile); err != nil {
			continue
		}

		partDev := filepath.Join(filepath.Dir(blockDevice), e.Name())
		info := BlockDevicePartitionInfo{Device: partDev}

		// UUID (best-effort, some partitions may not have one).
		if uuid, err := resolveDeviceAttribute(partDev, "UUID"); err == nil {
			info.UUID = uuid
		}
		// PARTUUID
		if partuuid, err := resolveDeviceAttribute(partDev, "PARTUUID"); err == nil {
			info.PartUUID = partuuid
		}
		// LABEL
		if label, err := resolveDeviceAttribute(partDev, "LABEL"); err == nil {
			info.Label = label
		}
		// PARTTYPE
		if partType, err := resolveDeviceAttribute(partDev, "PARTTYPE"); err == nil {
			info.PartType = strings.ToUpper(partType)
		}
		// FSType – resolve via mountinfo if the partition is mounted.
		if mounts, err := findMountsBySource(partDev); err == nil && len(mounts) > 0 {
			info.FSType = mounts[0].FSType
		}

		infos = append(infos, info)
	}

	return infos, nil
}

// PrintBlockDeviceInfo writes blkid-style information for all partitions
// on blockDevice to w.
func PrintBlockDeviceInfo(w io.Writer, blockDevice string) error {
	infos, err := BlockDeviceInfo(blockDevice)
	if err != nil {
		return err
	}
	for _, info := range infos {
		fmt.Fprintln(w, info.String())
	}
	return nil
}

// AcquireFileLock acquires an exclusive flock on the file at path,
// creating it if necessary, and returns an unlock function that
// releases the lock and closes the file.
//
// The goroutine+select pattern matches the locking used by the seeder,
// releaser and imager packages.  If the lock cannot be acquired within
// timeout the call fails with an error.
func AcquireFileLock(path string, timeout time.Duration) (func(), error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return nil, fmt.Errorf("failed to open lock file %s: %w", path, err)
	}

	deadline := time.Now().Add(timeout)
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) {
			f.Close()
			return nil, fmt.Errorf("failed to acquire lock on %s: %w", path, err)
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, fmt.Errorf("timed out waiting for lock on %s", path)
		}
		time.Sleep(10 * time.Millisecond)
	}

	return sync.OnceFunc(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}), nil
}

// EnsureLockDir ensures the lock directory exists.
func EnsureLockDir(lockDir string) (string, error) {
	if err := os.MkdirAll(lockDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create lock directory %s: %w", lockDir, err)
	}
	return lockDir, nil
}

// EnsureLockPath computes <lockDir>/<name>.lock and ensures its parent directory exists.
func EnsureLockPath(lockDir, name string) (string, error) {
	lockFile := filepath.Join(lockDir, name+".lock")
	lockFileDir := filepath.Dir(lockFile)
	if err := os.MkdirAll(lockFileDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create lock file directory %s: %w", lockFileDir, err)
	}
	return lockFile, nil
}

// ExecuteWithLock acquires an exclusive file lock at lockPath with a timeout
// parsed from timeoutStr (in seconds), logs acquisition via printFn if non-nil,
// runs fn, and releases the lock.
func ExecuteWithLock(lockPath, timeoutStr string, fn func() error, onAcquired func()) error {
	timeoutSecs, err := strconv.Atoi(timeoutStr)
	if err != nil {
		return fmt.Errorf("invalid lock wait seconds %q: %w", timeoutStr, err)
	}
	unlock, err := AcquireFileLock(lockPath, time.Duration(timeoutSecs)*time.Second)
	if err != nil {
		return err
	}
	defer unlock()
	if onAcquired != nil {
		onAcquired()
	}
	return fn()
}
