package validation

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type QA struct {
	cfg config.IConfig
}

// New creates a new QA instance.
func New(cfg config.IConfig) (*QA, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	return &QA{
		cfg: cfg,
	}, nil
}

// CheckMatrixOSPrivate verifies PrivateGitRepoPath exists and is a directory.
func (q *QA) CheckMatrixOSPrivate() error {
	path, err := q.cfg.GetItem("matrixOS.PrivateGitRepoPath")
	if err != nil {
		return err
	}
	if path == "" {
		return errors.New("matrixOS.PrivateGitRepoPath not set")
	}
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s does not exist: %w", path, err)
	}
	if !st.IsDir() {
		return fmt.Errorf("%s is not a directory", path)
	}
	return nil
}

// CheckSecureBoot validates SecureBoot signing key of usb-storage kernel modules
// matches the serial in the provided certificate file. It requires `openssl` or
// can parse certificates directly.
func (q *QA) CheckSecureBoot(imageDir, sbcertPath string) error {
	modulesdir := filepath.Join(imageDir, "lib/modules")
	var usbMods []string
	// Walk the modulesdir to find usb-storage.ko*
	_ = filepath.WalkDir(modulesdir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() && strings.HasPrefix(filepath.Base(p), "usb-storage.ko") {
			usbMods = append(usbMods, p)
		}
		return nil
	})
	if len(usbMods) == 0 {
		return fmt.Errorf("No usb-storage.ko found in %s", modulesdir)
	}

	// Parse cert serial from PEM
	serial, err := certSerialColon(sbcertPath)
	if err != nil {
		return fmt.Errorf("Cannot extract SecureBoot serial from %s: %w", sbcertPath, err)
	}

	for _, mod := range usbMods {
		rel := strings.TrimPrefix(mod, strings.TrimRight(imageDir, "/"))

		out, err := filesystems.ChrootOutput(imageDir, "modinfo", "-F", "sig_key", rel)
		if err != nil {
			return fmt.Errorf("chroot modinfo failed for %s: %w", rel, err)
		}

		sig := strings.TrimSpace(string(out))
		if sig == "" {
			return fmt.Errorf("No sig_key found for %s", rel)
		}

		if !strings.EqualFold(serial, sig) {
			return fmt.Errorf(
				"%s SecureBoot serial and module signature key mismatch: cert='%s' module='%s'",
				rel, serial, sig,
			)
		}
	}
	return nil
}

// certSerialColon reads a certificate file (PEM or DER) and returns the serial
// number formatted as hex bytes separated by colons (aa:bb:cc...)
func certSerialColon(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	// try PEM decode
	if bytes.HasPrefix(b, []byte("-----BEGIN")) {
		blk, _ := pem.Decode(b)
		if blk == nil {
			return "", errors.New("failed to decode PEM")
		}
		b = blk.Bytes
	}
	cert, err := x509.ParseCertificate(b)
	if err != nil {
		return "", err
	}
	// serial is big.Int; get bytes
	s := cert.SerialNumber.Bytes()
	if len(s) == 0 {
		return "", errors.New("empty serial")
	}
	hexs := hex.EncodeToString(s)
	// ensure even length
	if len(hexs)%2 != 0 {
		hexs = "0" + hexs
	}
	parts := make([]string, 0, len(hexs)/2)
	for i := 0; i < len(hexs); i += 2 {
		parts = append(parts, hexs[i:i+2])
	}
	return strings.ToLower(strings.Join(parts, ":")), nil
}

// verifyEnvironmentSetup checks for the presence of executables and directories
// inside an image directory. executables should be the list as used in the
// shell script and dirs is the list of directories to verify (absolute paths).
func verifyEnvironmentSetup(imageDir string, executables, dirs []string) error {
	if imageDir == "" {
		return errors.New("missing parameter imageDir")
	}

	var retErrs []string
	for _, exe := range executables {
		// path-like executable
		if strings.Contains(exe, "/") {
			// check file inside root
			target := exe
			if imageDir != "/" {
				target = imageDir + exe
			}

			if st, err := os.Stat(target); err != nil || st.Mode()&0111 == 0 {
				retErrs = append(retErrs, fmt.Sprintf("%s not found", target))
			}
			continue
		}

		// Simple command name (e.g. "sh")
		if imageDir == "/" {
			if _, err := exec.LookPath(exe); err != nil {
				retErrs = append(retErrs, fmt.Sprintf("%s not found", exe))
			}
			continue
		}

		// First, check common bin locations inside the image
		tryPaths := []string{
			imageDir + "/bin/" + exe,
			imageDir + "/usr/bin/" + exe,
		}

		found := false
		for _, p := range tryPaths {
			if st, err := os.Stat(p); err == nil && st.Mode()&0111 != 0 {
				found = true
				break
			}
		}

		if !found {
			// fallback to chroot when available
			out, err := filesystems.ChrootOutput(imageDir, "which", exe)
			if err != nil || len(bytes.TrimSpace(out)) == 0 {
				retErrs = append(
					retErrs,
					fmt.Sprintf("%s not found in chroot %s", exe, imageDir),
				)
			}
		}
	}

	for _, d := range dirs {
		p := filepath.Join(imageDir, d)
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			retErrs = append(retErrs, fmt.Sprintf("%s not found", p))
		}
	}

	if len(retErrs) > 0 {
		return errors.New(strings.Join(retErrs, "; "))
	}
	return nil
}

var imagerBaseExecutables = []string{
	"blockdev",
	"btrfs",
	"chroot",
	"cryptsetup",
	"efibootmgr",
	"findmnt",
	"fstrim",
	"gpg",
	"losetup",
	"mkfs.btrfs",
	"mkfs.vfat",
	"openssl",
	"ostree",
	"partprobe",
	"qemu-img",
	"qemu-system-x86_64",
	"sgdisk",
	"udevadm",
	"unshare",
	"xz",
}

// VerifyDistroRootfsEnvironmentSetup checks the typical distro binaries and dirs
func (q *QA) VerifyDistroRootfsEnvironmentSetup(imageDir string) error {
	executables := append(append([]string{}, imagerBaseExecutables...), "find", "git", "wget")
	if imageDir != "/" {
		executables = append(executables, "/usr/bin/grub-install")
	}
	return verifyEnvironmentSetup(imageDir, executables, []string{"/usr/share/shim"})
}

func (q *QA) verifyWithPrivateRepo(imageDir string, executables []string) error {
	if imageDir == "" {
		return errors.New("missing parameter imageDir")
	}
	key := "matrixOS.DefaultPrivateGitRepoPath"
	if imageDir == "/" {
		key = "matrixOS.PrivateGitRepoPath"
	}
	privatePath, err := q.cfg.GetItem(key)
	if err != nil {
		return err
	}
	if privatePath == "" {
		return errors.New("matrixOS.PrivateGitRepoPath not set")
	}
	return verifyEnvironmentSetup(imageDir, executables, []string{privatePath})
}

// VerifyReleaserEnvironmentSetup checks for tools required by releaser
func (q *QA) VerifyReleaserEnvironmentSetup(imageDir string) error {
	return q.verifyWithPrivateRepo(imageDir, []string{
		"chroot", "find", "findmnt", "gpg", "openssl", "ostree", "unshare",
	})
}

// VerifySeederEnvironmentSetup checks tools for seeder
func (q *QA) VerifySeederEnvironmentSetup(imageDir string) error {
	return q.verifyWithPrivateRepo(imageDir, []string{
		"chroot", "git", "gpg", "openssl", "ostree", "unshare", "wget",
	})
}

// VerifyImagerEnvironmentSetup checks tools for imager
func (q *QA) VerifyImagerEnvironmentSetup(imageDir string) error {
	executables := append(append([]string{}, imagerBaseExecutables...), "grub-install")
	return verifyEnvironmentSetup(imageDir, executables, []string{"/usr/share/shim"})
}

// CheckNumberOfKernels verifies the number of vmlinuz files under /usr/lib/modules
func (q *QA) CheckNumberOfKernels(imageDir string, expectedAmount int) error {
	if imageDir == "" {
		return errors.New("missing imageDir parameter")
	}
	if fi, err := os.Stat(imageDir); err != nil || !fi.IsDir() {
		return fmt.Errorf("%s is not a directory", imageDir)
	}
	found := 0
	matches, _ := filepath.Glob(filepath.Join(imageDir, "usr/lib/modules/*/vmlinuz"))
	found = len(matches)
	if found != expectedAmount {
		fmt.Fprintf(os.Stderr, "Found %d kernels in /usr/lib/modules, expected %d\n", found, expectedAmount)
		// list directory
		entries, _ := os.ReadDir(filepath.Join(imageDir, "usr/lib/modules/"))
		for _, e := range entries {
			fmt.Fprintf(os.Stderr, "%s\n", e.Name())
		}
		return fmt.Errorf("found %d kernels, expected %d", found, expectedAmount)
	}
	return nil
}
