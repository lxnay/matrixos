package filesystems

import (
	"errors"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// noop is a no-op callback for testing.
var noop = func(string) {}

func baseFsencConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{
			"Imager.EncryptionKey":       {"superSecret123"},
			"Imager.EncryptedRootFsName": {"matrixosroot"},
			"matrixOS.OsName":            {"matrixos"},
		},
		Bools: map[string]bool{
			"Imager.Encryption": true,
		},
	}
}

// --- NewFsenc Tests ---

func TestNewFsenc(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, err := NewFsenc(cfg, noop, noop)
		if err != nil {
			t.Fatalf("NewFsenc() returned error: %v", err)
		}
		if f == nil {
			t.Fatal("NewFsenc() returned nil")
		}
	})

	t.Run("NilConfig", func(t *testing.T) {
		_, err := NewFsenc(nil, nil, nil)
		if err == nil {
			t.Fatal("NewFsenc(nil) should return error")
		}
	})
}

// --- Config Accessor Tests ---

func TestEncryptionEnabled(t *testing.T) {
	t.Run("Enabled", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		enabled, err := f.EncryptionEnabled()
		if err != nil {
			t.Fatalf("EncryptionEnabled() error: %v", err)
		}
		if !enabled {
			t.Error("Expected encryption to be enabled")
		}
	})

	t.Run("Disabled", func(t *testing.T) {
		cfg := baseFsencConfig()
		cfg.Bools["Imager.Encryption"] = false
		f, _ := NewFsenc(cfg, noop, noop)
		enabled, err := f.EncryptionEnabled()
		if err != nil {
			t.Fatalf("EncryptionEnabled() error: %v", err)
		}
		if enabled {
			t.Error("Expected encryption to be disabled")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		f, _ := NewFsenc(ec, noop, noop)
		_, err := f.EncryptionEnabled()
		if err == nil {
			t.Error("Expected error from broken config")
		}
	})
}

func TestEncryptionKey(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		key, err := f.EncryptionKey()
		if err != nil {
			t.Fatalf("EncryptionKey() error: %v", err)
		}
		if key != "superSecret123" {
			t.Errorf("Expected 'superSecret123', got %q", key)
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		f, _ := NewFsenc(ec, noop, noop)
		_, err := f.EncryptionKey()
		if err == nil {
			t.Error("Expected error from broken config")
		}
	})
}

func TestEncryptedRootFsName(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		name, err := f.EncryptedRootFsName()
		if err != nil {
			t.Fatalf("EncryptedRootFsName() error: %v", err)
		}
		if name != "matrixosroot" {
			t.Errorf("Expected 'matrixosroot', got %q", name)
		}
	})

	t.Run("Empty", func(t *testing.T) {
		cfg := baseFsencConfig()
		cfg.Items["Imager.EncryptedRootFsName"] = []string{""}
		f, _ := NewFsenc(cfg, noop, noop)
		_, err := f.EncryptedRootFsName()
		if err == nil {
			t.Error("Expected error for empty EncryptedRootFsName")
		}
	})

	t.Run("ConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg error")}
		f, _ := NewFsenc(ec, noop, noop)
		_, err := f.EncryptedRootFsName()
		if err == nil {
			t.Error("Expected error from broken config")
		}
	})
}

// --- LuksEncrypt Tests ---

func TestLuksEncrypt(t *testing.T) {
	t.Run("EmptyDevicePath", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.LuksEncrypt("", "/dev/mapper/root")
		if err == nil {
			t.Error("Expected error for empty devicePath")
		}
	})

	t.Run("EmptyDesiredLuksDevice", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.LuksEncrypt("/dev/loop0p3", "")
		if err == nil {
			t.Error("Expected error for empty desiredLuksDevice")
		}
	})

	t.Run("EncryptionKeyError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("key error")}
		f, _ := NewFsenc(ec, noop, noop)
		err := f.LuksEncrypt("/dev/loop0p3", "/dev/mapper/root")
		if err == nil {
			t.Error("Expected error from config failure")
		}
	})

	t.Run("LuksFormatFails", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		mr := runner.NewMockRunnerFailOnCall(0, errors.New("luksFormat failed"))
		f.runner = mr.Run

		err := f.LuksEncrypt("/dev/loop0p3", "/dev/mapper/matrixosroot")
		if err == nil {
			t.Fatal("Expected error from luksFormat failure")
		}
		if !strings.Contains(err.Error(), "luksFormat") {
			t.Errorf("Error should mention luksFormat: %v", err)
		}
	})

	t.Run("CryptsetupOpenFails", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		mr := runner.NewMockRunnerFailOnCall(1, errors.New("open failed"))
		f.runner = mr.Run

		err := f.LuksEncrypt("/dev/loop0p3", "/dev/mapper/matrixosroot")
		if err == nil {
			t.Fatal("Expected error from cryptsetup open failure")
		}
		if !strings.Contains(err.Error(), "cryptsetup open") {
			t.Errorf("Error should mention cryptsetup open: %v", err)
		}
	})

	t.Run("DesiredLuksDeviceDoesNotExist", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		mr := runner.NewMockRunner()
		f.runner = mr.Run

		// Use a path that won't exist.
		err := f.LuksEncrypt("/dev/loop0p3", "/dev/mapper/nonexistent_test_device")
		if err == nil {
			t.Fatal("Expected error because desired LUKS device does not exist")
		}
		if !strings.Contains(err.Error(), "does not exist") {
			t.Errorf("Error should mention 'does not exist': %v", err)
		}
		// The device mapper name should still be tracked internally for cleanup.
		if len(f.openMappers) != 1 || f.openMappers[0] != "nonexistent_test_device" {
			t.Errorf("Expected openMappers to contain 'nonexistent_test_device', got %v", f.openMappers)
		}
	})

	t.Run("SuccessWithPassphrase", func(t *testing.T) {
		// Create a fake LUKS device path that "exists" after open.
		tmpDir := t.TempDir()
		desiredDevice := filepath.Join(tmpDir, "matrixosroot")
		os.WriteFile(desiredDevice, []byte{}, 0600)

		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		mr := runner.NewMockRunner()
		f.runner = mr.Run

		err := f.LuksEncrypt("/dev/loop0p3", desiredDevice)
		if err != nil {
			t.Fatalf("LuksEncrypt() error: %v", err)
		}

		// Should have made 2 runner calls: luksFormat + open.
		if len(mr.Calls) != 2 {
			t.Fatalf("Expected 2 runner calls, got %d", len(mr.Calls))
		}

		// Verify luksFormat call.
		if mr.Calls[0].Name != "cryptsetup" {
			t.Errorf("Call 0: expected 'cryptsetup', got %q", mr.Calls[0].Name)
		}
		if !containsArg(mr.Calls[0].Args, "luksFormat") {
			t.Errorf("Call 0 should contain 'luksFormat': %v", mr.Calls[0].Args)
		}
		// Key is a passphrase (not a file), so key-file should be "-".
		if !containsArg(mr.Calls[0].Args, "-") {
			t.Errorf("Call 0 should use '-' as key file for passphrase mode: %v", mr.Calls[0].Args)
		}

		// Verify open call.
		if !containsArg(mr.Calls[1].Args, "open") {
			t.Errorf("Call 1 should contain 'open': %v", mr.Calls[1].Args)
		}
		if !containsArg(mr.Calls[1].Args, "--allow-discards") {
			t.Errorf("Call 1 should contain '--allow-discards': %v", mr.Calls[1].Args)
		}

		// Verify internal device mapper tracking.
		expectedName := filepath.Base(desiredDevice)
		if len(f.openMappers) != 1 || f.openMappers[0] != expectedName {
			t.Errorf("Expected openMappers to contain %q, got %v", expectedName, f.openMappers)
		}
	})

	t.Run("SuccessWithKeyFile", func(t *testing.T) {
		// Create a real key file and a fake LUKS device.
		tmpDir := t.TempDir()
		keyFile := filepath.Join(tmpDir, "enc.key")
		os.WriteFile(keyFile, []byte("file-based-key"), 0600)
		desiredDevice := filepath.Join(tmpDir, "matrixosroot")
		os.WriteFile(desiredDevice, []byte{}, 0600)

		cfg := baseFsencConfig()
		cfg.Items["Imager.EncryptionKey"] = []string{keyFile}
		f, _ := NewFsenc(cfg, noop, noop)
		mr := runner.NewMockRunner()
		f.runner = mr.Run

		err := f.LuksEncrypt("/dev/loop0p3", desiredDevice)
		if err != nil {
			t.Fatalf("LuksEncrypt() error: %v", err)
		}

		// luksFormat should use the actual key file path, not "-".
		if !containsArg(mr.Calls[0].Args, keyFile) {
			t.Errorf("Call 0 should use key file path %q: %v", keyFile, mr.Calls[0].Args)
		}
		// open should reference the key file.
		expectedKeyFileArg := "--key-file=" + keyFile
		if !containsArg(mr.Calls[1].Args, expectedKeyFileArg) {
			t.Errorf("Call 1 should use %q: %v", expectedKeyFileArg, mr.Calls[1].Args)
		}
	})
}

// --- ValidateLuksVariables Tests ---

func TestValidateLuksVariables(t *testing.T) {
	t.Run("EncryptionDisabled", func(t *testing.T) {
		cfg := baseFsencConfig()
		cfg.Bools["Imager.Encryption"] = false
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.ValidateLuksVariables()
		if err != nil {
			t.Fatalf("ValidateLuksVariables() should succeed when encryption is disabled: %v", err)
		}
	})

	t.Run("EncryptionEnabledAllSet", func(t *testing.T) {
		cfg := baseFsencConfig()
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.ValidateLuksVariables()
		if err != nil {
			t.Fatalf("ValidateLuksVariables() error: %v", err)
		}
	})

	t.Run("EncryptionEnabledMissingKey", func(t *testing.T) {
		cfg := baseFsencConfig()
		cfg.Items["Imager.EncryptionKey"] = []string{""}
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.ValidateLuksVariables()
		if err == nil {
			t.Error("Expected error for missing EncryptionKey")
		}
		if !strings.Contains(err.Error(), "EncryptionKey") {
			t.Errorf("Error should mention EncryptionKey: %v", err)
		}
	})

	t.Run("EncryptionEnabledMissingRootFsName", func(t *testing.T) {
		cfg := baseFsencConfig()
		cfg.Items["Imager.EncryptedRootFsName"] = []string{""}
		f, _ := NewFsenc(cfg, noop, noop)
		err := f.ValidateLuksVariables()
		if err == nil {
			t.Error("Expected error for missing EncryptedRootFsName")
		}
		if !strings.Contains(err.Error(), "EncryptedRootFsName") {
			t.Errorf("Error should mention EncryptedRootFsName: %v", err)
		}
	})

	t.Run("EncryptionCheckConfigError", func(t *testing.T) {
		ec := &config.ErrConfig{Err: errors.New("cfg broken")}
		f, _ := NewFsenc(ec, noop, noop)
		err := f.ValidateLuksVariables()
		if err == nil {
			t.Error("Expected error from broken config")
		}
	})
}

// --- FileExists / PathExists / DirectoryExists Tests ---

func TestFileExistsHelper(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "testfile")
	os.WriteFile(file, []byte("data"), 0644)

	if !FileExists(file) {
		t.Error("FileExists(file) should be true")
	}
	if FileExists(tmpDir) {
		t.Error("FileExists(dir) should be false")
	}
	if FileExists(filepath.Join(tmpDir, "nonexistent")) {
		t.Error("FileExists(nonexistent) should be false")
	}
}

func TestPathExistsHelper(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "testfile")
	os.WriteFile(file, []byte("data"), 0644)

	if !PathExists(file) {
		t.Error("PathExists(file) should be true")
	}
	if !PathExists(tmpDir) {
		t.Error("PathExists(dir) should be true")
	}
	if PathExists(filepath.Join(tmpDir, "nonexistent")) {
		t.Error("PathExists(nonexistent) should be false")
	}
}

func TestDirectoryExistsHelper(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "testfile")
	os.WriteFile(file, []byte("data"), 0644)

	if !DirectoryExists(tmpDir) {
		t.Error("DirectoryExists(dir) should be true")
	}
	if DirectoryExists(file) {
		t.Error("DirectoryExists(file) should be false")
	}
	if DirectoryExists(filepath.Join(tmpDir, "nonexistent")) {
		t.Error("DirectoryExists(nonexistent) should be false")
	}
}

// --- IFsenc Interface Compliance ---

func TestFsencImplementsIFsenc(t *testing.T) {
	cfg := baseFsencConfig()
	f, _ := NewFsenc(cfg, noop, noop)
	// Compile-time interface check.
	var _ IFsenc = f
}

// --- Helpers ---

func containsArg(args []string, target string) bool {
	for _, a := range args {
		if a == target {
			return true
		}
	}
	return false
}
