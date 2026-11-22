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

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// IFsenc defines the interface for filesystem encryption operations.
// It mirrors all public methods of Fsenc for testability.
type IFsenc interface {
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

// Fsenc provides filesystem encryption operations backed by LUKS/cryptsetup.
type Fsenc struct {
	cfg           config.IConfig
	runner        runner.Func
	openMappersMu sync.Mutex
	openMappers   []string
	opening       func(string)
	opened        func(string)
}

// NewFsenc creates a new Fsenc instance.
func NewFsenc(cfg config.IConfig, opening, opened func(string)) (*Fsenc, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	return &Fsenc{
		cfg:     cfg,
		runner:  runner.Run,
		opening: opening,
		opened:  opened,
	}, nil
}

// add adds a device-mapper name to the tracking list of opened mappers for cleanup.
func (f *Fsenc) add(mapperName string) {
	f.openMappersMu.Lock()
	defer f.openMappersMu.Unlock()
	f.openMappers = append(f.openMappers, mapperName)
}

func (f *Fsenc) Cleanup() {
	f.openMappersMu.Lock()
	mappers := slices.Clone(f.openMappers)
	f.openMappers = nil
	f.openMappersMu.Unlock()

	CleanupCryptsetupDevices(mappers)
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

	var stdin io.Reader
	var keyFileArg string

	if FileExists(encKey) {
		fmt.Fprintln(os.Stdout, "LUKS Encryption key is a file.")
		keyFileArg = encKey
		stdin = nil
	} else {
		fmt.Fprintln(os.Stdout, "LUKS Encryption key is NOT a file.")
		keyFileArg = "-"
		stdin = strings.NewReader(encKey)
	}

	luksName := filepath.Base(desiredLuksDevice)

	// Format the device with LUKS encryption.
	fmt.Fprintf(os.Stdout, "Formatting %s using LUKS Encryption ...\n", devicePath)
	err = f.runner(&runner.Cmd{
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
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("cryptsetup luksFormat failed on %s: %w", devicePath, err)
	}

	// Re-create stdin for the open command if the key is not a file.
	if !FileExists(encKey) {
		stdin = strings.NewReader(encKey)
	} else {
		stdin = nil
	}

	// Track the opened device-mapper name for cleanup.
	f.add(luksName)
	f.opening(luksName)
	err = f.runner(&runner.Cmd{
		Name: "cryptsetup",
		Args: []string{
			"open",
			"--allow-discards",
			"--key-file=" + keyFileArg,
			devicePath,
			luksName,
		},
		Stdin:  stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	})
	if err != nil {
		return fmt.Errorf("cryptsetup open failed on %s: %w", devicePath, err)
	}
	f.opened(luksName)

	// Wait for the device node to appear.
	DevicesSettle()

	if _, err := os.Stat(desiredLuksDevice); err != nil {
		return fmt.Errorf("%s does not exist: cannot set up LUKS Encryption: %w", desiredLuksDevice, err)
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

	fmt.Fprintln(os.Stdout, "Encryption of rootfs enabled. Setting up...")

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
