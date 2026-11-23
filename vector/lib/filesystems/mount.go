package filesystems

import (
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/sys/unix"

	"matrixos/vector/lib/runner"
)

// Mount wraps unix.Mount and can be replaced in tests.
var Mount = unix.Mount

// Unmount wraps unix.Unmount and can be replaced in tests.
var Unmount = unix.Unmount

// MountpointToDevice returns the device path for a given mountpoint
// by reading /proc/self/mountinfo.
func MountpointToDevice(mnt string) (string, error) {
	if mnt == "" {
		return "", fmt.Errorf("missing mnt parameter")
	}

	entry, err := findMountByTargetResolveAutofs(mnt)
	if err != nil {
		return "", fmt.Errorf("no device found for mountpoint %s", mnt)
	}
	if entry.Source == "" {
		return "", fmt.Errorf("no device found for mountpoint %s", mnt)
	}
	return entry.Source, nil
}

// MountpointToUUID returns the UUID for a given mountpoint by reading
// /proc/self/mountinfo and resolving the device UUID.
func MountpointToUUID(mnt string) (string, error) {
	if mnt == "" {
		return "", fmt.Errorf("missing mnt parameter")
	}

	entry, err := findMountContainingPath(mnt)
	if err != nil {
		return "", fmt.Errorf("no UUID found for mountpoint %s", mnt)
	}
	uuid, err := resolveDeviceAttribute(entry.Source, "UUID")
	if err != nil {
		return "", fmt.Errorf("no UUID found for mountpoint %s: %w", mnt, err)
	}
	return uuid, nil
}

// MountpointToFSType returns the filesystem type for a given mountpoint
// by reading /proc/self/mountinfo.
func MountpointToFSType(mnt string) (string, error) {
	if mnt == "" {
		return "", fmt.Errorf("missing mnt parameter")
	}

	entry, err := findMountContainingPath(mnt)
	if err != nil {
		return "", fmt.Errorf("no FSTYPE found for mountpoint %s", mnt)
	}
	if entry.FSType == "" {
		return "", fmt.Errorf("no FSTYPE found for mountpoint %s", mnt)
	}
	return entry.FSType, nil
}

// RemountReadWrite remounts the given mountpoint as read-write.
func RemountReadWrite(mnt string) error {
	if mnt == "" {
		return fmt.Errorf("missing mnt parameter")
	}

	return execRun(&runner.Cmd{
		Name: "mount",
		Args: []string{"-o", "remount,rw", "--", mnt},
	})
}

// CleanupMountsOptions represents the options for cleaning up mounts.
type CleanupMountsOptions struct {
	Mounts []string
	Stdout io.Writer
	Stderr io.Writer
}

// CleanupMounts unmounts a list of mounts in reverse order.
func CleanupMounts(opts CleanupMountsOptions) {
	mounts := opts.Mounts

	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	DevicesSettle()
	for i := len(mounts) - 1; i >= 0; i-- {
		mnt := mounts[i]
		mounted, err := IsMounted(mnt)
		if err != nil {
			fmt.Fprintf(stderr, "Unable to check if %s is mounted: %v\n", mnt, err)
			continue
		}
		if !mounted {
			continue
		}
		fmt.Fprintf(stdout, "Unmounting %s ...\n", mnt)
		if err := Unmount(mnt, 0); err != nil {
			fmt.Fprintf(stderr, "Unable to umount %s: %v\n", mnt, err)
			if entry, mntErr := findMountByTarget(mnt); mntErr == nil {
				if strings.HasPrefix(entry.Source, "/dev/") {
					FlushBlockDeviceBuffers(entry.Source)
				}
				fmt.Fprintf(stderr, "%s\n", entry.String())
			}
			fmt.Fprintf(stderr, "For safety, calling umount -l %s\n", mnt)
			Unmount(mnt, unix.MNT_DETACH)
			continue
		}
	}
	DevicesSettle()
}

// CleanupCryptsetupDevices closes a list of cryptsetup devices.
func CleanupCryptsetupDevices(devices []string) {
	cleanupCryptsetupDevices(devices, os.Stdout, os.Stderr)
}

// cleanupCryptsetupDevices closes the given cryptsetup devices, routing output
// through stdout/stderr, and returns the subset of devices that failed to close.
func cleanupCryptsetupDevices(devices []string, stdout, stderr io.Writer) []string {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	var failed []string
	DevicesSettle()
	for _, cd := range devices {
		cdpath, err := GetLuksRootfsDevicePath(cd)
		if err != nil {
			fmt.Fprintf(stderr, "%v\n", err)
			continue
		}
		if _, err := os.Stat(cdpath); os.IsNotExist(err) {
			continue
		}

		fmt.Fprintf(stdout, "Closing LUKS device: %s ...\n", cd)
		FlushBlockDeviceBuffers(cdpath)
		cmd := &runner.Cmd{
			Name:   "cryptsetup",
			Args:   []string{"close", cd},
			Stdout: stdout,
			Stderr: stderr,
		}
		if err := execRun(cmd); err != nil {
			fmt.Fprintf(stderr, "Unable to cryptsetup close %s\n", cdpath)
			if entries, mntErr := findMountsBySource(cdpath); mntErr == nil {
				fmt.Fprintf(stderr, "%s\n", formatMountEntries(entries))
			}
			failed = append(failed, cd)
			continue
		}
	}
	DevicesSettle()
	return failed
}

// CheckActiveMounts checks for active mounts under a given directory
// by reading /proc/self/mountinfo.
func CheckActiveMounts(chrootDir string) error {
	if chrootDir == "" {
		return fmt.Errorf("missing chrootDir parameter")
	}

	entries, err := listMountsByPrefix(chrootDir)
	if err != nil {
		return fmt.Errorf("failed to inspect active mounts for %s: %w", chrootDir, err)
	}
	if len(entries) == 0 {
		return nil
	}

	var foundMounts []string
	for _, e := range entries {
		foundMounts = append(foundMounts, e.Mountpoint)
	}

	return fmt.Errorf(
		"cannot operate sync to %s. Active mounts detected:\n- %s\nPlease umount manually.",
		chrootDir, strings.Join(foundMounts, "\n- "),
	)
}
