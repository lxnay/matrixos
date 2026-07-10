package ostree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
)

func (o *Ostree) SetupEtc(rootfs string) error {
	o.Print("Setting up /etc...\n")
	etcDir := filepath.Join(rootfs, "etc")
	usrEtcDir := filepath.Join(rootfs, "usr", "etc")

	o.Print("Moving %s to %s\n", etcDir, usrEtcDir)
	return filesystems.Move(etcDir, usrEtcDir)
}

func (o *Ostree) prepareVarHome(imageDir, homeName, varHomeName string) error {
	homeDir := filepath.Join(imageDir, homeName)
	varHomeDir := filepath.Join(imageDir, "var", varHomeName)

	homeInfo, err := os.Lstat(homeDir)
	homeExists := err == nil

	if homeExists && (homeInfo.Mode()&os.ModeSymlink != 0) {
		if info, err := os.Stat(varHomeDir); err == nil && info.IsDir() {
			link, _ := os.Readlink(homeDir)
			if strings.HasSuffix(link, "var/"+varHomeName) {
				o.Print("%s is a symlink and %s is a directory. All good.\n", homeDir, varHomeDir)
			} else {
				o.PrintError("%s symlink points to an unexpected path: %s\n", homeDir, link)
				return fmt.Errorf("home symlink invalid")
			}
		}
	} else if homeExists && homeInfo.IsDir() {
		if pathExists(varHomeDir) { // path exists is correct.
			o.PrintError("WARNING: removing %s\n", varHomeDir)
			os.RemoveAll(varHomeDir)
		}
		if err := filesystems.Move(homeDir, varHomeDir); err != nil {
			return fmt.Errorf("failed to move home: %w", err)
		}
	} else if homeExists {
		if err := os.Remove(homeDir); err != nil {
			return fmt.Errorf("failed to remove home: %w", err)
		}
	}
	if _, err := os.Stat(varHomeDir); os.IsNotExist(err) {
		if err := os.MkdirAll(varHomeDir, 0755); err != nil {
			return fmt.Errorf("failed to create %v: %w", varHomeDir, err)
		}
	}
	// && !os.IsExist(err) done because of the complexity of the conditions above.
	if err := os.Symlink("var/"+varHomeName, homeDir); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to symlink %v: %w", homeDir, err)
	}
	return nil
}

// moveDirToTargetAndSymlink moves srcDir to targetDir (if srcDir exists as a real
// directory or removes it if it's a non-directory), ensures targetDir exists, and
// creates a symlink at srcDir pointing to symlinkTarget.
func moveDirToTargetAndSymlink(srcDir, targetDir, symlinkTarget string) error {
	if info, err := os.Lstat(srcDir); err == nil {
		if info.IsDir() {
			if pathExists(targetDir) {
				os.RemoveAll(targetDir)
			}
			fmt.Fprintf(os.Stderr, "WARNING: moving %s to %s.\n", srcDir, targetDir)
			if err := filesystems.Move(srcDir, targetDir); err != nil {
				return fmt.Errorf("failed to move %s: %w", srcDir, err)
			}
		} else {
			if err := os.Remove(srcDir); err != nil {
				return fmt.Errorf("failed to remove %s: %w", srcDir, err)
			}
		}
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", targetDir, err)
	}

	if err := os.Symlink(symlinkTarget, srcDir); err != nil && !os.IsExist(err) {
		return fmt.Errorf("failed to symlink %s: %w", srcDir, err)
	}
	return nil
}

// prepareSysrootAndOstreeLink creates the /sysroot directory and the
// /ostree -> sysroot/ostree symlink inside imageDir.
func prepareSysrootAndOstreeLink(imageDir string) error {
	if err := os.Mkdir(filepath.Join(imageDir, "sysroot"), 0755); err != nil {
		return fmt.Errorf("failed to create sysroot: %w", err)
	}

	ostreeLink := filepath.Join(imageDir, "ostree")
	if _, err := os.Lstat(ostreeLink); err == nil {
		if err := os.Remove(ostreeLink); err != nil {
			return fmt.Errorf("failed to remove existing ostree link: %w", err)
		}
	}
	if err := os.Symlink("sysroot/ostree", ostreeLink); err != nil {
		return fmt.Errorf("failed to symlink ostree: %w", err)
	}
	return nil
}

// prepareTmpDir ensures /tmp is an empty directory with mode 1777 (sticky).
func prepareTmpDir(imageDir string) error {
	tmpDir := filepath.Join(imageDir, "tmp")

	if err := os.RemoveAll(tmpDir); err != nil {
		return fmt.Errorf("failed to remove tmp: %w", err)
	}
	if err := os.Mkdir(tmpDir, os.ModeSticky|0o777); err != nil {
		return fmt.Errorf("failed to create tmp: %w", err)
	}
	// Chmod explicitly to apply sticky bit regardless of umask.
	if err := os.Chmod(tmpDir, os.ModeSticky|0o777); err != nil {
		return fmt.Errorf("failed to chmod tmp: %w", err)
	}
	return nil
}

// prepareMachineID resets /etc/machine-id to an empty file.
func prepareMachineID(imageDir string) error {
	machineID := filepath.Join(imageDir, "etc", "machine-id")
	_ = os.Remove(machineID)
	f, err := os.Create(machineID)
	if err != nil {
		return fmt.Errorf("failed to touch machine-id: %w", err)
	}
	f.Close()
	return nil
}

// prepareVarDbPkg moves var/db/pkg to the read-only VDB location and creates
// a relative symlink back.
func (o *Ostree) prepareVarDbPkg(imageDir, roVdbPath string) error {
	o.Print("Setting up %s...\n", roVdbPath)
	varDbPkg := filepath.Join(imageDir, RwVdbPath)
	usrVarDbPkg := filepath.Join(imageDir, roVdbPath)

	o.Print("Moving %s to %s\n", varDbPkg, usrVarDbPkg)
	if err := os.MkdirAll(filepath.Dir(usrVarDbPkg), 0755); err != nil {
		return fmt.Errorf("failed to create parent of usrVarDbPkg: %w", err)
	}
	if err := filesystems.Move(varDbPkg, usrVarDbPkg); err != nil {
		return fmt.Errorf("failed to move var/db/pkg: %w", err)
	}

	if err := os.Symlink(filepath.Join("..", "..", roVdbPath), varDbPkg); err != nil {
		return fmt.Errorf("failed to symlink var/db/pkg: %w", err)
	}
	return nil
}

// prepareOpt moves /opt to /usr/opt and symlinks it.
func (o *Ostree) prepareOpt(imageDir string) error {
	o.Print("Setting up /opt...\n")
	return moveDirToTargetAndSymlink(
		filepath.Join(imageDir, "opt"),
		filepath.Join(imageDir, "usr", "opt"),
		"usr/opt",
	)
}

// prepareSrv moves /srv to /var/srv and symlinks it.
func (o *Ostree) prepareSrv(imageDir string) error {
	o.Print("Setting up /srv...\n")
	return moveDirToTargetAndSymlink(
		filepath.Join(imageDir, "srv"),
		filepath.Join(imageDir, "var", "srv"),
		"var/srv",
	)
}

// prepareStaticDirs creates /lab, /snap, and /usr/src directories.
func (o *Ostree) prepareStaticDirs(imageDir string) error {
	dirs := []struct {
		path string
		desc string
	}{
		{"lab", "Setting up /lab (for everything homelabbing and your LAN)...\n"},
		{"snap", "Setting up /snap ...\n"},
		{filepath.Join("usr", "src"), "Setting up /usr/src (for snap) ...\n"},
	}
	for _, d := range dirs {
		o.Print("%s", d.desc)
		if err := os.MkdirAll(filepath.Join(imageDir, d.path), 0755); err != nil {
			return fmt.Errorf("failed to create %s: %w", d.path, err)
		}
	}
	return nil
}

// prepareUsrLocal moves /usr/local to /var/usrlocal and symlinks it.
func (o *Ostree) prepareUsrLocal(imageDir string) error {
	o.Print("Setting up /usr/local...\n")
	usrLocalDir := filepath.Join(imageDir, "usr", "local")
	relUsrLocal := "var/usrlocal"
	imageUsrLocal := filepath.Join(imageDir, relUsrLocal)

	if pathExists(usrLocalDir) {
		if err := filesystems.Move(usrLocalDir, imageUsrLocal); err != nil {
			return fmt.Errorf("failed to move usr/local: %w", err)
		}
	} else {
		os.MkdirAll(imageUsrLocal, 0755)
	}
	if err := os.Symlink(filepath.Join("..", relUsrLocal), usrLocalDir); err != nil {
		return fmt.Errorf("failed to symlink usr/local: %w", err)
	}
	return nil
}

func (o *Ostree) PrepareFilesystemHierarchy(rootfs string) error {
	marker := filepath.Join(rootfs, "var", ".matrixos-prepared")
	if fileExists(marker) {
		return fmt.Errorf("filesystem hierarchy already prepared: %s exists", marker)
	}

	if err := prepareSysrootAndOstreeLink(rootfs); err != nil {
		return err
	}

	if err := prepareTmpDir(rootfs); err != nil {
		return err
	}

	if err := prepareMachineID(rootfs); err != nil {
		return err
	}

	if err := o.SetupEtc(rootfs); err != nil {
		return err
	}

	matrixOsRoVdb, err := o.cfg.GetItem("Releaser.ReadOnlyVdb")
	if err != nil {
		return err
	}
	if matrixOsRoVdb == "" {
		return fmt.Errorf("config item Releaser.ReadOnlyVdb is not set")
	}
	if err := o.prepareVarDbPkg(rootfs, matrixOsRoVdb); err != nil {
		return err
	}

	if err := o.prepareOpt(rootfs); err != nil {
		return err
	}

	if err := o.prepareSrv(rootfs); err != nil {
		return err
	}

	if err := o.prepareStaticDirs(rootfs); err != nil {
		return err
	}

	o.Print("Setting up /home ...\n")
	if err := o.prepareVarHome(rootfs, "home", "home"); err != nil {
		return err
	}
	o.Print("Setting up /root ...\n")
	if err := o.prepareVarHome(rootfs, "root", "roothome"); err != nil {
		return err
	}

	efiRoot, err := o.cfg.GetItem("Imager.EfiRoot")
	if err != nil {
		return err
	}
	if efiRoot == "" {
		return fmt.Errorf("config item Imager.EfiRoot is not set")
	}
	o.Print("Setting up %s...\n", efiRoot)
	os.MkdirAll(filepath.Join(rootfs, efiRoot), 0755)

	if err := o.prepareUsrLocal(rootfs); err != nil {
		return err
	}

	if err := os.WriteFile(marker, []byte("prepared"), 0644); err != nil {
		return fmt.Errorf("failed to create marker file: %w", err)
	}

	return nil
}

func (o *Ostree) ValidateFilesystemHierarchy(rootfs string) error {
	if rootfs == "" {
		return fmt.Errorf("missing rootfs parameter")
	}

	expected := []string{
		"/home",
		"/opt",
		"/root",
		"/srv",
		"/usr/local",
	}

	var issues int
	for _, relPath := range expected {
		fullPath := filepath.Join(rootfs, relPath)

		// Check if it's a symlink and if it points to a directory.
		// We use Lstat to check the link itself and Stat to check the target.
		lfi, lerr := os.Lstat(fullPath)
		if lerr == nil && lfi.Mode()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(fullPath); err == nil && fi.IsDir() {
				continue
			}
		}

		fmt.Fprintf(os.Stderr, "Expected %s to be a symlink to a directory.\n",
			fullPath)
		fmt.Fprintln(os.Stderr, "Please check the filesystem hierarchy.")
		issues++
	}

	if issues > 0 {
		return fmt.Errorf("filesystem hierarchy validation failed: %d issues",
			issues)
	}

	// /tmp must be a real (non-symlink) directory with mode 1777 (sticky).
	tmpPath := filepath.Join(rootfs, "tmp")
	fi, err := os.Lstat(tmpPath)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 ||
		fi.Mode().Perm() != 0o777 || fi.Mode()&os.ModeSticky == 0 {
		fmt.Fprintf(os.Stderr, "Expected %s to be a sticky directory with mode 1777.\n", tmpPath)
		fmt.Fprintln(os.Stderr, "Please check the filesystem hierarchy.")
		return fmt.Errorf("filesystem hierarchy validation failed: /tmp is not a sticky 1777 directory")
	}

	return nil
}
