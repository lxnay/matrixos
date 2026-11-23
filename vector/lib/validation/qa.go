package validation

import (
	"bytes"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type QA struct {
	cfg    config.IConfig
	mu     sync.RWMutex
	stderr io.Writer
}

// New creates a new QA instance.
func New(cfg config.IConfig) (*QA, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	return &QA{
		cfg:    cfg,
		stderr: os.Stderr,
	}, nil
}

// SetStderr replaces the writer used for diagnostic output.
func (q *QA) SetStderr(w io.Writer) {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.stderr = w
	q.mu.Unlock()
}

// Stderr returns the configured diagnostic writer, defaulting to os.Stderr.
func (q *QA) Stderr() io.Writer {
	if q == nil {
		return os.Stderr
	}
	q.mu.RLock()
	w := q.stderr
	q.mu.RUnlock()
	if w == nil {
		return os.Stderr
	}
	return w
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
	if imageDir == "" {
		return errors.New("missing imageDir parameter")
	}
	imageDir = filepath.Clean(imageDir)
	modulesdir := filepath.Join(imageDir, "lib/modules")
	var usbMods []string
	// Walk the modulesdir to find usb-storage.ko*
	if err := filepath.WalkDir(modulesdir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasPrefix(filepath.Base(p), "usb-storage.ko") {
			usbMods = append(usbMods, p)
		}
		return nil
	}); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("No usb-storage.ko found in %s: %w", modulesdir, err)
		}
		return fmt.Errorf("failed to walk %s: %w", modulesdir, err)
	}
	if len(usbMods) == 0 {
		return fmt.Errorf("No usb-storage.ko found in %s", modulesdir)
	}

	// Parse cert serial from PEM
	serial, err := certSerialColon(sbcertPath)
	if err != nil {
		return fmt.Errorf("Cannot extract SecureBoot serial from %s: %w", sbcertPath, err)
	}

	for _, mod := range usbMods {
		relPath, err := filepath.Rel(imageDir, mod)
		if err != nil {
			return fmt.Errorf("failed to compute relative path for %s: %w", mod, err)
		}
		rel := "/" + filepath.ToSlash(relPath)

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
	// Scan all PEM blocks for a CERTIFICATE block (handles leading comments
	// and multi-block PEM bundles). Fall back to raw DER only when no PEM
	// blocks are present at all.
	var certDER []byte
	sawPEM := false
	rest := b
	for {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		sawPEM = true
		if blk.Type == "CERTIFICATE" {
			certDER = blk.Bytes
			break
		}
	}
	if sawPEM && certDER == nil {
		return "", errors.New("no CERTIFICATE PEM block found")
	}
	if certDER == nil {
		certDER = b
	}
	cert, err := x509.ParseCertificate(certDER)
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

// statInRoot stats virtual path p confined to chroot root. When root == "/" it
// delegates directly to os.Stat; otherwise it resolves symlinks component by
// component strictly inside root (clamping ".." at virtual "/" and re-rooting
// absolute symlink targets inside root so merged-usr layouts resolve cleanly
// without escaping onto the host filesystem).
func statInRoot(root, p string) (os.FileInfo, error) {
	if root == "/" {
		return os.Stat(p)
	}
	root = filepath.Clean(root)
	cleanP := filepath.Clean("/" + p)
	var pending []string
	for _, seg := range strings.Split(cleanP, "/") {
		if seg != "" {
			pending = append(pending, seg)
		}
	}

	const maxSymlinkHops = 40
	var resolved []string
	hops := 0

	for len(pending) > 0 {
		comp := pending[0]
		pending = pending[1:]
		if comp == "" || comp == "." {
			continue
		}
		if comp == ".." {
			if len(resolved) > 0 {
				resolved = resolved[:len(resolved)-1]
			}
			continue
		}

		next := make([]string, len(resolved)+1)
		copy(next, resolved)
		next[len(resolved)] = comp

		hostPath := filepath.Join(append([]string{root}, next...)...)
		fi, err := os.Lstat(hostPath)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			hops++
			if hops > maxSymlinkHops {
				return nil, fmt.Errorf("too many levels of symbolic links resolving %s under %s", p, root)
			}
			target, err := os.Readlink(hostPath)
			if err != nil {
				return nil, err
			}
			var targetParts []string
			for _, seg := range strings.Split(target, "/") {
				if seg != "" {
					targetParts = append(targetParts, seg)
				}
			}
			if strings.HasPrefix(target, "/") {
				resolved = nil
			}
			pending = append(targetParts, pending...)
			continue
		}
		resolved = next
		if len(pending) == 0 {
			return fi, nil
		}
		if !fi.IsDir() {
			return nil, fmt.Errorf("%s is not a directory", hostPath)
		}
	}

	if len(resolved) == 0 {
		return os.Stat(root)
	}
	return os.Lstat(filepath.Join(append([]string{root}, resolved...)...))
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
			target := exe
			if imageDir != "/" {
				target = filepath.Join(imageDir, exe)
			}

			if st, err := statInRoot(imageDir, exe); err != nil || st.IsDir() || st.Mode()&0111 == 0 {
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

		// First, check common bin/sbin locations inside the image
		tryPaths := []string{
			filepath.Join("/bin", exe),
			filepath.Join("/usr/bin", exe),
			filepath.Join("/sbin", exe),
			filepath.Join("/usr/sbin", exe),
		}

		found := false
		for _, p := range tryPaths {
			if st, err := statInRoot(imageDir, p); err == nil && !st.IsDir() && st.Mode()&0111 != 0 {
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
		if fi, err := statInRoot(imageDir, d); err != nil || !fi.IsDir() {
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
		return fmt.Errorf("%s not set", key)
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
	matches, err := filepath.Glob(filepath.Join(imageDir, "usr/lib/modules/*/vmlinuz"))
	if err != nil {
		return fmt.Errorf("failed to glob kernels in %s: %w", imageDir, err)
	}
	found := len(matches)
	if found != expectedAmount {
		stderr := q.Stderr()
		fmt.Fprintf(stderr, "Found %d kernels in /usr/lib/modules, expected %d\n", found, expectedAmount)
		// list directory
		entries, _ := os.ReadDir(filepath.Join(imageDir, "usr/lib/modules/"))
		for _, e := range entries {
			fmt.Fprintf(stderr, "%s\n", e.Name())
		}
		return fmt.Errorf("found %d kernels, expected %d", found, expectedAmount)
	}
	return nil
}
