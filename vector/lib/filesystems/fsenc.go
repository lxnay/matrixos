package filesystems

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// IFsenc defines the interface for filesystem encryption operations.
// It mirrors all public methods of Fsenc for testability.
type IFsenc interface {
	// SetStdout replaces the writer used for informational output.
	SetStdout(w io.Writer)
	// SetStderr replaces the writer used for warnings and errors.
	SetStderr(w io.Writer)

	// EncryptionEnabled returns whether rootfs encryption is enabled.
	EncryptionEnabled() (bool, error)
	// EncryptionKey returns the configured encryption key (passphrase or file path).
	EncryptionKey() (string, error)
	// EncryptedRootFsName returns the configured LUKS device-mapper name for the
	// encrypted root filesystem.
	EncryptedRootFsName() (string, error)

	// LuksEncrypt encrypts the given block device with LUKS and opens it as
	// desiredLuksDevice (the full /dev/mapper/<name> path).
	LuksEncrypt(devicePath, desiredLuksDevice string) error
	// ValidateLuksVariables checks that all required LUKS-related configuration
	// variables are set when encryption is enabled.
	ValidateLuksVariables() error
	// Cleanup cleans up the previously opened (or in opening) device mappers.
	Cleanup()
}

// Compile-time interface check.
var _ IFsenc = (*Fsenc)(nil)

// Fsenc provides filesystem encryption operations backed by LUKS/cryptsetup.
type Fsenc struct {
	cfg            config.IConfig
	runner         runner.Func
	cleanupDevices func(devices []string, stdout, stderr io.Writer) []string
	ioMu           sync.Mutex
	stdout         io.Writer
	stderr         io.Writer
	openMu         sync.Mutex
	closed         atomic.Bool
	openMappers    []string
	opening        func(string)
	opened         func(string)
}

// NewFsenc creates a new Fsenc instance.
func NewFsenc(cfg config.IConfig, opening, opened func(string)) (*Fsenc, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	return &Fsenc{
		cfg:            cfg,
		runner:         runner.Run,
		cleanupDevices: cleanupCryptsetupDevices,
		opening:        opening,
		opened:         opened,
	}, nil
}

// SetStdout replaces the writer used for informational output.
func (f *Fsenc) SetStdout(w io.Writer) {
	f.ioMu.Lock()
	defer f.ioMu.Unlock()
	f.stdout = w
}

// SetStderr replaces the writer used for warnings and errors.
func (f *Fsenc) SetStderr(w io.Writer) {
	f.ioMu.Lock()
	defer f.ioMu.Unlock()
	f.stderr = w
}

func (f *Fsenc) getStdout() io.Writer {
	f.ioMu.Lock()
	defer f.ioMu.Unlock()
	if f.stdout == nil {
		return os.Stdout
	}
	return f.stdout
}

func (f *Fsenc) getStderr() io.Writer {
	f.ioMu.Lock()
	defer f.ioMu.Unlock()
	if f.stderr == nil {
		return os.Stderr
	}
	return f.stderr
}

func (f *Fsenc) Cleanup() {
	// Mark closed before acquiring openMu so an in-flight cryptsetup open
	// observes Cleanup() immediately upon finishing instead of returning nil
	// to FormatRootfs while Cleanup() is still waiting to close the mapper.
	f.closed.Store(true)

	f.openMu.Lock()
	defer f.openMu.Unlock()

	if len(f.openMappers) == 0 {
		return
	}
	mappers := slices.Clone(f.openMappers)
	cleanup := f.cleanupDevices
	if cleanup == nil {
		cleanup = cleanupCryptsetupDevices
	}
	// Retain any mappers that failed to close so a subsequent Cleanup() can retry.
	f.openMappers = cleanup(mappers, f.getStdout(), f.getStderr())
}

func (f *Fsenc) EncryptionEnabled() (bool, error) {
	return f.cfg.GetBool("Imager.Encryption")
}

func (f *Fsenc) EncryptionKey() (string, error) {
	key, err := f.cfg.GetItem("Imager.EncryptionKey")
	if err != nil {
		return "", err
	}
	return key, nil
}

func (f *Fsenc) EncryptedRootFsName() (string, error) {
	return config.GetRequiredItem(f.cfg, "Imager.EncryptedRootFsName")
}

func (f *Fsenc) LuksEncrypt(devicePath, desiredLuksDevice string) error {
	if devicePath == "" {
		return errors.New("missing devicePath parameter")
	}
	if desiredLuksDevice == "" {
		return errors.New("missing desiredLuksDevice parameter")
	}

	encKey, err := f.EncryptionKey()
	if err != nil {
		return fmt.Errorf("failed to retrieve encryption key: %w", err)
	}
	if encKey == "" {
		return errors.New("Imager.EncryptionKey is empty")
	}

	stdout := f.getStdout()
	stderr := f.getStderr()

	var stdin io.Reader
	var keyFileArg string

	keyIsFile := FileExists(encKey)
	if keyIsFile {
		fmt.Fprintln(stdout, "LUKS Encryption key is a file.")
		keyFileArg = encKey
		stdin = nil
	} else {
		fmt.Fprintln(stdout, "LUKS Encryption key is NOT a file.")
		keyFileArg = "-"
		stdin = strings.NewReader(encKey)
	}

	luksName := filepath.Base(desiredLuksDevice)

	// Hold openMu across closed-check and luksFormat so Cleanup() waits for an
	// in-flight luksFormat before returning (preventing Imager.Cleanup from
	// detaching the underlying loop device mid-format) and so luksFormat cannot
	// start after Cleanup() has already run.
	if err := f.runUnderOpenLock(
		fmt.Sprintf("cleanup already ran; refusing to format LUKS device %s", devicePath),
		fmt.Sprintf("cryptsetup luksFormat failed on %s", devicePath),
		fmt.Sprintf("cleanup interrupted LUKS format on %s", devicePath),
		func() { fmt.Fprintf(stdout, "Formatting %s using LUKS Encryption ...\n", devicePath) },
		&runner.Cmd{
			Name: "cryptsetup",
			Args: []string{
				"-c",
				"aes-xts-plain64",
				"-s",
				"512",
				"luksFormat",
				devicePath,
				keyFileArg,
			},
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		},
	); err != nil {
		return err
	}

	// Re-create stdin for the open command if the key is not a file.
	if !keyIsFile {
		stdin = strings.NewReader(encKey)
	} else {
		stdin = nil
	}

	if f.closed.Load() {
		return fmt.Errorf("cleanup already ran; refusing to open LUKS device %s", luksName)
	}

	if f.opening != nil {
		f.opening(luksName)
	}
	// Hold openMu across closed-check, tracking, and cryptsetup open so a
	// concurrent Cleanup() cannot race with open and leak /dev/mapper/<luksName>.
	if err := f.runUnderOpenLock(
		fmt.Sprintf("cleanup already ran; refusing to open LUKS device %s", luksName),
		fmt.Sprintf("cryptsetup open failed on %s", devicePath),
		fmt.Sprintf("cleanup interrupted LUKS open for %s", luksName),
		func() { f.openMappers = append(f.openMappers, luksName) },
		&runner.Cmd{
			Name: "cryptsetup",
			Args: []string{
				"open",
				"--allow-discards",
				"--key-file=" + keyFileArg,
				devicePath,
				luksName,
			},
			Stdin:  stdin,
			Stdout: stdout,
			Stderr: stderr,
		},
	); err != nil {
		return err
	}

	if f.opened != nil {
		f.opened(luksName)
	}

	// Wait for the device node to appear.
	DevicesSettle()

	if _, err := os.Stat(desiredLuksDevice); err != nil {
		return fmt.Errorf("%s does not exist: cannot set up LUKS Encryption: %w", desiredLuksDevice, err)
	}

	if f.closed.Load() {
		return fmt.Errorf("cleanup interrupted LUKS open for %s", luksName)
	}

	return nil
}

func (f *Fsenc) runUnderOpenLock(refuseMsg, failPrefix, interruptMsg string, beforeRun func(), cmd *runner.Cmd) error {
	f.openMu.Lock()
	defer f.openMu.Unlock()
	if f.closed.Load() {
		return errors.New(refuseMsg)
	}
	if beforeRun != nil {
		beforeRun()
	}
	if err := f.runner(cmd); err != nil {
		return fmt.Errorf("%s: %w", failPrefix, err)
	}
	if f.closed.Load() {
		return errors.New(interruptMsg)
	}
	return nil
}

func (f *Fsenc) ValidateLuksVariables() error {
	enabled, err := f.EncryptionEnabled()
	if err != nil {
		return fmt.Errorf("failed to check Imager.Encryption: %w", err)
	}

	if !enabled {
		return nil
	}

	fmt.Fprintln(f.getStdout(), "Encryption of rootfs enabled. Setting up...")

	key, err := f.EncryptionKey()
	if err != nil {
		return fmt.Errorf("failed to retrieve Imager.EncryptionKey: %w", err)
	}
	if key == "" {
		return errors.New("Imager.EncryptionKey not set: please set it to a passphrase")
	}

	if _, err := f.EncryptedRootFsName(); err != nil {
		return fmt.Errorf("failed to retrieve Imager.EncryptedRootFsName: %w", err)
	}

	return nil
}
