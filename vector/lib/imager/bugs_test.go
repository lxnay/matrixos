package imager

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"matrixos/vector/lib/ostree"
)

func TestExtractReleaseVersion_KeepsDateFallbackWhenMetadataMissingSeedName(t *testing.T) {
	today := time.Now().Format("20060102")

	for _, tc := range []struct {
		name    string
		content string
	}{
		{name: "NoSeedNameLine", content: "BUILD_DATE=2026-02-15\n"},
		{name: "SeedNameWithoutDash", content: "SEED_NAME=matrixos\n"},
		{name: "SeedNameTrailingDash", content: "SEED_NAME=matrixos-gnome-\n"},
		{name: "SeedNameTrailingSpaces", content: "SEED_NAME=matrixos-gnome-   \n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			metadataDir := filepath.Join(tmpDir, "etc", "matrixos")
			if err := os.MkdirAll(metadataDir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(metadataDir, "build.txt"), []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}

			im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
			im.SetRootfs(tmpDir)
			got, err := im.ExtractReleaseVersion()
			if err != nil {
				t.Fatalf("ExtractReleaseVersion: %v", err)
			}
			if got != today {
				t.Errorf("ExtractReleaseVersion() = %q, want date fallback %q", got, today)
			}
		})
	}
}

func TestSetupPasswords_ReturnsScannerErrInsteadOfTruncatingShadow(t *testing.T) {
	if _, err := exec.LookPath("openssl"); err != nil {
		t.Skip("openssl not available")
	}

	tmpDir := t.TempDir()
	etcDir := filepath.Join(tmpDir, "etc")
	if err := os.MkdirAll(etcDir, 0755); err != nil {
		t.Fatal(err)
	}
	shadowPath := filepath.Join(etcDir, "shadow")
	// Craft a line longer than bufio.MaxScanTokenSize (64 KiB) so scanner stops with ErrTooLong.
	longLine := "alice:" + strings.Repeat("x", 70*1024) + ":19000:0:99999:7:::\n"
	orig := []byte("daemon:*:19000:0:99999:7:::\n" + longLine + "nobody:*:19000:0:99999:7:::\n")
	if err := os.WriteFile(shadowPath, orig, 0640); err != nil {
		t.Fatal(err)
	}

	im := newTestImager(baseImageConfig(), &ostree.MockOstree{})
	im.SetRootfs(tmpDir)
	err := im.SetupPasswords()
	if err == nil {
		t.Fatal("SetupPasswords succeeded despite bufio.Scanner ErrTooLong and silently truncated /etc/shadow")
	}
	// Original shadow file must remain unmodified when scanning fails.
	after, rerr := os.ReadFile(shadowPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if !bytes.Equal(after, orig) {
		t.Error("SetupPasswords overwrote /etc/shadow despite scanner error")
	}
}
