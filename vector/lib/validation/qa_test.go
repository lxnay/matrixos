package validation

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"math/big"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeExecChrootOutput mocks filesystems.ExecChrootOutput for tests.
func fakeExecChrootOutput(c *runner.ChrootCmd) ([]byte, error) {
	allArgs := strings.Join(c.Args, " ")
	if c.Name == "modinfo" && strings.Contains(allArgs, "-F sig_key") {
		return []byte(os.Getenv("MOCK_SIG_KEY")), nil
	}
	if c.Name == "which" {
		return []byte("/usr/bin/" + c.Args[len(c.Args)-1]), nil
	}
	return nil, fmt.Errorf("unknown mock chroot command: %s %s", c.Name, allArgs)
}

func setupMockChrootOutput(t *testing.T) {
	orig := filesystems.ExecChrootOutput
	filesystems.ExecChrootOutput = runner.ChrootOutputFunc(fakeExecChrootOutput)
	t.Cleanup(func() { filesystems.ExecChrootOutput = orig })
}

func TestCheckMatrixOSPrivate(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.MockConfig{Items: map[string][]string{
		"matrixOS.PrivateGitRepoPath": {tmp},
	}}
	q, _ := New(cfg)

	if err := q.CheckMatrixOSPrivate(); err != nil {
		t.Fatalf("expected nil for existing dir, got %v", err)
	}

	// non-existent
	cfg.Items["matrixOS.PrivateGitRepoPath"] = []string{filepath.Join(tmp, "nope")}
	if err := q.CheckMatrixOSPrivate(); err == nil {
		t.Fatalf("expected error for missing dir, got nil")
	}
}

func TestCheckNumberOfKernels(t *testing.T) {
	tmp := t.TempDir()
	// create usr/lib/modules/v1/vmlinuz
	p := filepath.Join(tmp, "usr/lib/modules/v1")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	v := filepath.Join(p, "vmlinuz")
	if err := os.WriteFile(v, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	q, _ := New(&config.MockConfig{})
	if err := q.CheckNumberOfKernels(tmp, 1); err != nil {
		t.Fatalf("expected nil when count matches, got %v", err)
	}
	if err := q.CheckNumberOfKernels(tmp, 2); err == nil {
		t.Fatalf("expected error when count mismatches, got nil")
	}
}

func TestVerifyEnvironmentSetup(t *testing.T) {
	tmp := t.TempDir()
	// create a directory that will be used as dir arg
	d := "/usr/share/shim"
	if err := os.MkdirAll(filepath.Join(tmp, d), 0o755); err != nil {
		t.Fatal(err)
	}
	// create a /bin/sh inside the temp image
	binsh := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binsh, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binsh, "sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// choose an executable that exists (sh) and a path-like executable inside image
	exes := []string{"sh", "/bin/sh"}
	dirs := []string{d}
	// Since verifyEnvironmentSetup is now private or just used internally by methods that are on QA,
	// but verifyEnvironmentSetup itself is a plain function in my last edit?
	// Let's check `qa.go` content. I kept it as a private function `verifyEnvironmentSetup`.
	// So I can test it directly if I am in the same package.
	if err := verifyEnvironmentSetup(tmp, exes, dirs); err != nil {
		t.Fatalf("expected nil environment setup, got %v", err)
	}
}

func TestCheckSecureBoot(t *testing.T) {
	setupMockChrootOutput(t)

	tmp := t.TempDir()
	// Create dummy usb-storage.ko
	modDir := filepath.Join(tmp, "lib/modules/5.15.0")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "usb-storage.ko"), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create dummy cert
	priv, _ := rsa.GenerateKey(rand.Reader, 2048)
	serialNumber := big.NewInt(123456)
	template := x509.Certificate{
		SerialNumber: serialNumber,
	}
	certDer, _ := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	certPem := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDer})
	certPath := filepath.Join(tmp, "cert.pem")
	if err := os.WriteFile(certPath, certPem, 0o644); err != nil {
		t.Fatal(err)
	}

	// We expect the mock command to return the serial: 01:e2:40 (123456 in hex)
	// OpenSSL serial format is usually lower case hex colon separated.
	// 123456 = 0x1E240 => 01:e2:40 (even lengthed).
	os.Setenv("MOCK_SIG_KEY", "01:e2:40")
	defer os.Unsetenv("MOCK_SIG_KEY")

	q, err := New(&config.MockConfig{})
	if err != nil {
		t.Fatal(err)
	}

	if err := q.CheckSecureBoot(tmp, certPath); err != nil {
		t.Fatalf("expected nil for matching serial, got %v", err)
	}

	// Mismatch
	os.Setenv("MOCK_SIG_KEY", "aa:bb:cc")
	if err := q.CheckSecureBoot(tmp, certPath); err == nil {
		t.Fatalf("expected error for mismatched serial, got nil")
	}
}

func TestVerifyWrappers(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.MockConfig{Items: map[string][]string{
		"matrixOS.PrivateGitRepoPath": {tmp},
	}}
	q, _ := New(cfg)

	// We can't easily test success without creating TONS of binaries files inside tmp
	// But we can test failure if dir is empty (which it is)

	// Create minimal structure to avoid "imagedir missing" error
	if err := q.VerifyDistroRootfsEnvironmentSetup(tmp); err == nil {
		t.Fatalf("expected error for empty image dir (missing binaries), got nil")
	}
	// We expect VerifySeederEnvironmentSetup to look for chroot etc.
	if err := q.VerifySeederEnvironmentSetup(tmp); err == nil {
		t.Fatalf("expected error for empty image dir, got nil")
	}
}

func TestQA_BugsReproducers(t *testing.T) {
	t.Run("VerifyEnvironmentSetupRejectsDirectoryAsExecutable", func(t *testing.T) {
		tmp := t.TempDir()
		// Create a directory named /usr/bin/fakeexe (mode 0755 has 0111 bits set!).
		if err := os.MkdirAll(filepath.Join(tmp, "usr/bin/fakeexe"), 0o755); err != nil {
			t.Fatal(err)
		}
		// Prevent chroot fallback from falsely finding it.
		orig := filesystems.ExecChrootOutput
		filesystems.ExecChrootOutput = func(*runner.ChrootCmd) ([]byte, error) {
			return nil, fmt.Errorf("not found")
		}
		t.Cleanup(func() { filesystems.ExecChrootOutput = orig })

		if err := verifyEnvironmentSetup(tmp, []string{"/usr/bin/fakeexe"}, nil); err == nil {
			t.Error("verifyEnvironmentSetup accepted directory as path-like executable")
		}
		if err := verifyEnvironmentSetup(tmp, []string{"fakeexe"}, nil); err == nil {
			t.Error("verifyEnvironmentSetup accepted directory as command-name executable")
		}
	})

	t.Run("VerifyEnvironmentSetupFindsSbinWithoutChroot", func(t *testing.T) {
		tmp := t.TempDir()
		sbinDir := filepath.Join(tmp, "usr", "sbin")
		if err := os.MkdirAll(sbinDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sbinDir, "sgdisk"), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		orig := filesystems.ExecChrootOutput
		filesystems.ExecChrootOutput = func(*runner.ChrootCmd) ([]byte, error) {
			return nil, fmt.Errorf("chroot fallback should not be needed for /usr/sbin")
		}
		t.Cleanup(func() { filesystems.ExecChrootOutput = orig })

		if err := verifyEnvironmentSetup(tmp, []string{"sgdisk"}, nil); err != nil {
			t.Errorf("verifyEnvironmentSetup did not find /usr/sbin/sgdisk: %v", err)
		}
	})

	t.Run("CertSerialColonHandlesLeadingPEMComments", func(t *testing.T) {
		priv, _ := rsa.GenerateKey(rand.Reader, 2048)
		tmpl := x509.Certificate{SerialNumber: big.NewInt(0x1234)}
		der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
		pemBytes := append([]byte("# Leading comment before PEM block\n\n"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
		p := filepath.Join(t.TempDir(), "commented.pem")
		if err := os.WriteFile(p, pemBytes, 0o644); err != nil {
			t.Fatal(err)
		}
		serial, err := certSerialColon(p)
		if err != nil {
			t.Fatalf("certSerialColon failed on PEM with leading comment: %v", err)
		}
		if serial != "12:34" {
			t.Errorf("serial = %q, want 12:34", serial)
		}
	})

	t.Run("VerifyWithPrivateRepoReportsActualKey", func(t *testing.T) {
		q, _ := New(&config.MockConfig{Items: map[string][]string{}})
		err := q.verifyWithPrivateRepo("/tmp/some-image", nil)
		if err == nil {
			t.Fatal("expected error for unset DefaultPrivateGitRepoPath")
		}
		if !strings.Contains(err.Error(), "matrixOS.DefaultPrivateGitRepoPath") {
			t.Errorf("error %q did not mention matrixOS.DefaultPrivateGitRepoPath", err.Error())
		}
	})

	t.Run("CheckSecureBootCleansImageDirForRelativeModPath", func(t *testing.T) {
		tmp := t.TempDir()
		modDir := filepath.Join(tmp, "lib/modules/6.12.0")
		if err := os.MkdirAll(modDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(modDir, "usb-storage.ko"), []byte("dummy"), 0o644); err != nil {
			t.Fatal(err)
		}
		priv, _ := rsa.GenerateKey(rand.Reader, 2048)
		tmpl := x509.Certificate{SerialNumber: big.NewInt(0xabcd)}
		der, _ := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
		certPath := filepath.Join(tmp, "cert.pem")
		if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
			t.Fatal(err)
		}

		var capturedModArg string
		orig := filesystems.ExecChrootOutput
		filesystems.ExecChrootOutput = func(c *runner.ChrootCmd) ([]byte, error) {
			capturedModArg = c.Args[len(c.Args)-1]
			return []byte("ab:cd\n"), nil
		}
		t.Cleanup(func() { filesystems.ExecChrootOutput = orig })

		q, _ := New(&config.MockConfig{})
		// Pass uncleaned imageDir with "/." suffix.
		if err := q.CheckSecureBoot(tmp+"/.", certPath); err != nil {
			t.Fatalf("CheckSecureBoot: %v", err)
		}
		if capturedModArg != "/lib/modules/6.12.0/usb-storage.ko" {
			t.Errorf("modinfo received unstripped path %q, want /lib/modules/6.12.0/usb-storage.ko", capturedModArg)
		}
	})

	t.Run("CheckNumberOfKernelsReturnsGlobError", func(t *testing.T) {
		badDir := filepath.Join(t.TempDir(), "img[bad")
		if err := os.MkdirAll(badDir, 0o755); err != nil {
			t.Fatal(err)
		}
		q, _ := New(&config.MockConfig{})
		err := q.CheckNumberOfKernels(badDir, 0)
		if err == nil {
			t.Fatal("CheckNumberOfKernels ignored filepath.ErrBadPattern and returned nil when expectedAmount=0")
		}
	})

	t.Run("CheckNumberOfKernelsRoutesDiagnosticsToStderrWriter", func(t *testing.T) {
		tmp := t.TempDir()
		if err := os.MkdirAll(filepath.Join(tmp, "usr/lib/modules/6.12.0"), 0o755); err != nil {
			t.Fatal(err)
		}
		q, _ := New(&config.MockConfig{})
		var stderr bytes.Buffer
		q.SetStderr(&stderr)
		if err := q.CheckNumberOfKernels(tmp, 1); err == nil {
			t.Fatal("expected error for 0 kernels")
		}
		if !strings.Contains(stderr.String(), "Found 0 kernels") || !strings.Contains(stderr.String(), "6.12.0") {
			t.Errorf("expected kernel diagnostics in injected stderr, got %q", stderr.String())
		}
	})
}
