package ostree

import (
	"fmt"
	"io"
	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestGpgHelpers(t *testing.T) {
	if got := GpgSignedFilePath("file"); got != "file.asc" {
		t.Errorf("GpgSignedFilePath(file) = %q, want file.asc", got)
	}
}

func TestKillGpgDaemons(t *testing.T) {
	var called []string
	mockRunner := func(cmd *runner.Cmd) error {
		called = append(called, cmd.Name+" "+strings.Join(cmd.Args, " "))
		return nil
	}

	// Empty homedir should be a no-op.
	KillGpgDaemons(mockRunner, "", nil, nil)
	if len(called) != 0 {
		t.Errorf("Expected no calls for empty homedir, got %v", called)
	}

	// Non-empty homedir should call gpgconf.
	KillGpgDaemons(mockRunner, "/tmp/test-gpg", nil, nil)
	if len(called) != 1 {
		t.Fatalf("Expected 1 call, got %d", len(called))
	}
	want := "gpgconf --homedir /tmp/test-gpg --kill all"
	if called[0] != want {
		t.Errorf("KillGpgDaemons command = %q, want %q", called[0], want)
	}

	// Errors should not propagate (fire-and-forget).
	errRunner := func(cmd *runner.Cmd) error {
		return fmt.Errorf("gpgconf failed")
	}
	KillGpgDaemons(errRunner, "/tmp/test-gpg", nil, nil) // should not panic
}

func TestOstreeKillGpgDaemons(t *testing.T) {
	var called []string
	tmpDir := t.TempDir()

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	o.runner = func(cmd *runner.Cmd) error {
		called = append(called, cmd.Name+" "+strings.Join(cmd.Args, " "))
		return nil
	}

	o.KillGpgDaemons()

	if len(called) != 1 {
		t.Fatalf("Expected 1 call, got %d: %v", len(called), called)
	}
	want := "gpgconf --homedir " + filepath.Join(tmpDir, "gpg") + " --kill all"
	if called[0] != want {
		t.Errorf("KillGpgDaemons command = %q, want %q", called[0], want)
	}
}

func TestOstreeKillGpgDaemons_BadConfig(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {""},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	// Should not panic on bad config.
	o.KillGpgDaemons()
}

func TestPatchGpgHomeDir(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("Skipping TestPatchGpgHomeDir: requires root privileges for chown")
	}
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "gpg-home")

	if err := PatchGpgHomeDir(homeDir); err != nil {
		t.Fatalf("PatchGpgHomeDir failed: %v", err)
	}

	info, err := os.Stat(homeDir)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Errorf("homeDir perm = %v, want 0700", info.Mode().Perm())
	}
}

func TestWalkGpgHomeDirEntryDisappears(t *testing.T) {
	homeDir := t.TempDir()
	trigger := filepath.Join(homeDir, "a-trigger")
	vanishing := filepath.Join(homeDir, "b-vanishing")
	for _, path := range []string{trigger, vanishing} {
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
	}

	triggered := false
	err := walkGpgHomeDir(homeDir, func(path string, _ os.FileInfo) error {
		if path == trigger {
			triggered = true
			return os.Remove(vanishing)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walkGpgHomeDir failed for a concurrently removed entry: %v", err)
	}
	if !triggered {
		t.Fatal("walkGpgHomeDir did not visit the trigger entry")
	}
}

func TestGpgKeyID(t *testing.T) {
	tmpDir := t.TempDir()
	pubKey := filepath.Join(tmpDir, "pub.key")
	if err := os.WriteFile(pubKey, []byte("dummy"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
			"Ostree.GpgPublicKey":  {pubKey},
		},
	}

	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		stdout := cmd.Stdout
		// Mock gpg output
		// Format: pub:u:4096:1:3260D9CC6D9275DD:1678752000:::u:::scESC:
		fmt.Fprintln(stdout, "pub:u:4096:1:3260D9CC6D9275DD:1678752000:::u:::scESC:")
		return nil
	}

	keyID, err := o.GpgKeyID()
	if err != nil {
		t.Fatalf("GpgKeyID failed: %v", err)
	}
	if keyID != "3260D9CC6D9275DD" {
		t.Errorf("GpgKeyID = %q, want 3260D9CC6D9275DD", keyID)
	}
}

func TestClientSideGpgArgs(t *testing.T) {
	// Test standalone function
	args, _ := ClientSideGpgArgs(false, "")
	if len(args) != 1 || args[0] != "--no-gpg-verify" {
		t.Errorf("ClientSideGpgArgs(false) = %v, want [--no-gpg-verify]", args)
	}

	args, _ = ClientSideGpgArgs(true, "/path/to/key")
	if len(args) != 2 || args[0] != "--set=gpg-verify=true" || args[1] != "--gpg-import=/path/to/key" {
		t.Errorf("ClientSideGpgArgs(true) = %v", args)
	}
}

func TestGpgSignFile(t *testing.T) {
	var cmds []string
	tmpDir := t.TempDir()
	dummyFile := filepath.Join(tmpDir, "file.txt")
	if err := os.WriteFile(dummyFile, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}
	pubKey := filepath.Join(tmpDir, "pub.key")
	if err := os.WriteFile(pubKey, []byte("key"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
			"Ostree.GpgPublicKey":  {pubKey},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, stdout := cmd.Args, cmd.Stdout
		cmds = append(cmds, strings.Join(args, " "))
		// Mock GpgKeyID call
		for _, arg := range args {
			if arg == "--show-keys" {
				fmt.Fprintln(stdout, "pub:u:4096:1:KEYID123:1678752000:::u:::scESC:")
				return nil
			}
		}
		return nil
	}

	if err := o.GpgSignFile(dummyFile); err != nil {
		t.Fatalf("GpgSignFile failed: %v", err)
	}

	// Verify commands: 1. gpg --show-keys (GpgKeyID), 2. gpg --detach-sign
	if len(cmds) != 2 {
		t.Errorf("Expected 2 commands, got %d", len(cmds))
	}
	if !strings.Contains(cmds[1], "--detach-sign") {
		t.Errorf("Expected detach-sign command, got: %s", cmds[1])
	}
	if !strings.Contains(cmds[1], "KEYID123") {
		t.Errorf("Expected key ID in sign command, got: %s", cmds[1])
	}
}

func TestImportGpgKey(t *testing.T) {
	var lastArgs []string
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "key.asc")
	if err := os.WriteFile(keyFile, []byte("key data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		lastArgs = args
		return nil
	}

	if err := o.ImportGpgKey(keyFile); err != nil {
		t.Fatalf("ImportGpgKey failed: %v", err)
	}

	// Expected: gpg --homedir ... --batch --yes --import keyFile
	foundImport := false
	for i, arg := range lastArgs {
		if arg == "--import" && i+1 < len(lastArgs) && lastArgs[i+1] == keyFile {
			foundImport = true
			break
		}
	}
	if !foundImport {
		t.Errorf("ImportGpgKey args missing --import %s: %v", keyFile, lastArgs)
	}
}

func TestGpgKeySelection(t *testing.T) {
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	offKey := filepath.Join(tmpDir, "off.key")

	// Case 1: No keys exist
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.GpgPublicKey":         {privKey},
			"Ostree.GpgOfficialPublicKey": {offKey},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	if _, err := o.AvailableGpgPubKeyPaths(); err == nil {
		t.Error("AvailableGpgPubKeyPaths should fail when no keys exist")
	}

	// Case 2: Only official key exists
	if err := os.WriteFile(offKey, []byte("off"), 0644); err != nil {
		t.Fatal(err)
	}
	paths, err := o.AvailableGpgPubKeyPaths()
	if err != nil {
		t.Errorf("AvailableGpgPubKeyPaths failed: %v", err)
	}
	if len(paths) != 1 || paths[0] != offKey {
		t.Errorf("Expected [offKey], got %v", paths)
	}
	best, _ := o.GpgBestPubKeyPath()
	if best != offKey {
		t.Errorf("Best key should be offKey, got %s", best)
	}

	// Case 3: Both exist (Private should be preferred/first)
	if err := os.WriteFile(privKey, []byte("priv"), 0644); err != nil {
		t.Fatal(err)
	}
	paths, err = o.AvailableGpgPubKeyPaths()
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || paths[0] != privKey {
		t.Errorf("Expected [privKey, offKey], got %v", paths)
	}
	best, _ = o.GpgBestPubKeyPath()
	if best != privKey {
		t.Errorf("Best key should be privKey, got %s", best)
	}
}

func TestMaybeInitializeGpg(t *testing.T) {
	var cmds [][]string
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	pubKey := filepath.Join(tmpDir, "pub.key")
	offKey := filepath.Join(tmpDir, "off.key")

	for _, f := range []string{privKey, pubKey, offKey} {
		if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":              {"/repo"},
			"Ostree.Remote":               {"origin"},
			"Ostree.GpgPrivateKey":        {privKey},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {offKey},
			"Ostree.DevGpgHomeDir":        {filepath.Join(tmpDir, "gpg")},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": true,
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		cmds = append(cmds, args)
		return nil
	}

	if err := o.MaybeInitializeGpg(); err != nil {
		t.Fatalf("MaybeInitializeGpg failed: %v", err)
	}

	// We expect calls for each key:
	// 1. ImportGpgKey (gpg --import)
	// 2. remote gpg-import (ostree remote gpg-import)
	// Keys: priv, pub, off. (pub is best, off is different)

	// We should see at least 3 ostree remote gpg-import calls and 3 gpg --import calls.
	ostreeImports := 0
	gpgImports := 0

	for _, cmd := range cmds {
		if len(cmd) > 0 {
			if cmd[0] == "--repo=/repo" && cmd[1] == "remote" && cmd[2] == "gpg-import" {
				ostreeImports++
			}
			// Check for gpg --import
			// cmd structure: [--status-fd=3 --homedir ... --batch --yes --import keyPath]
			for _, arg := range cmd {
				if arg == "--import" {
					gpgImports++
					break
				}
			}
		}
	}

	if ostreeImports != 3 {
		t.Errorf("Expected 3 ostree remote gpg-import calls, got %d", ostreeImports)
	}
	if gpgImports != 3 {
		t.Errorf("Expected 3 gpg --import calls, got %d", gpgImports)
	}
}

func TestGpgKeys(t *testing.T) {
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	pubKey := filepath.Join(tmpDir, "pub.key")
	offKey := filepath.Join(tmpDir, "off.key")

	for _, f := range []string{privKey, pubKey, offKey} {
		if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.GpgPrivateKey":        {privKey},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {offKey},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	keys, err := o.GpgKeys()
	if err != nil {
		t.Fatalf("GpgKeys failed: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("Expected 3 keys, got %d: %v", len(keys), keys)
	}
	if keys[0] != privKey {
		t.Errorf("Expected first key to be privKey, got %s", keys[0])
	}
	if keys[1] != pubKey {
		t.Errorf("Expected second key to be pubKey, got %s", keys[1])
	}
	if keys[2] != offKey {
		t.Errorf("Expected third key to be offKey, got %s", keys[2])
	}
}

func TestGpgKeysDedup(t *testing.T) {
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	// pubKey and offKey point to the same file to trigger dedup
	sameKey := filepath.Join(tmpDir, "same.key")

	for _, f := range []string{privKey, sameKey} {
		if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.GpgPrivateKey":        {privKey},
			"Ostree.GpgPublicKey":         {sameKey},
			"Ostree.GpgOfficialPublicKey": {sameKey},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	keys, err := o.GpgKeys()
	if err != nil {
		t.Fatalf("GpgKeys failed: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("Expected 2 keys (dedup), got %d: %v", len(keys), keys)
	}
}

func TestInitializeSigningGpg(t *testing.T) {
	var cmds [][]string
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	pubKey := filepath.Join(tmpDir, "pub.key")
	offKey := filepath.Join(tmpDir, "off.key")

	for _, f := range []string{privKey, pubKey, offKey} {
		if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.GpgPrivateKey":        {privKey},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {offKey},
			"Ostree.DevGpgHomeDir":        {filepath.Join(tmpDir, "gpg")},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		cmds = append(cmds, args)
		return nil
	}

	if err := o.InitializeSigningGpg(); err != nil {
		t.Fatalf("InitializeSigningGpg failed: %v", err)
	}

	gpgImports := 0
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if arg == "--import" {
				gpgImports++
				break
			}
		}
	}
	if gpgImports != 3 {
		t.Errorf("Expected 3 gpg --import calls, got %d", gpgImports)
	}

	// Ensure NO ostree remote gpg-import calls were made
	for _, cmd := range cmds {
		if len(cmd) > 2 && cmd[1] == "remote" && cmd[2] == "gpg-import" {
			t.Error("InitializeSigningGpg should not call ostree remote gpg-import")
		}
	}
}

func TestInitializeRemoteSigningGpg(t *testing.T) {
	var cmds [][]string
	tmpDir := t.TempDir()
	privKey := filepath.Join(tmpDir, "priv.key")
	pubKey := filepath.Join(tmpDir, "pub.key")
	offKey := filepath.Join(tmpDir, "off.key")

	for _, f := range []string{privKey, pubKey, offKey} {
		if err := os.WriteFile(f, []byte("data"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.GpgPrivateKey":        {privKey},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {offKey},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		cmds = append(cmds, args)
		return nil
	}

	if err := o.initializeRemoteSigningGpg("origin", "/repo"); err != nil {
		t.Fatalf("initializeRemoteSigningGpg failed: %v", err)
	}

	ostreeImports := 0
	for _, cmd := range cmds {
		if len(cmd) > 2 && cmd[0] == "--repo=/repo" && cmd[1] == "remote" && cmd[2] == "gpg-import" {
			ostreeImports++
		}
	}
	if ostreeImports != 3 {
		t.Errorf("Expected 3 ostree remote gpg-import calls, got %d", ostreeImports)
	}

	// Ensure NO local gpg --import calls were made
	for _, cmd := range cmds {
		for _, arg := range cmd {
			if arg == "--import" {
				t.Error("initializeRemoteSigningGpg should not call gpg --import")
				break
			}
		}
	}
}

func TestInitializeRemoteSigningGpgMissingParams(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	if err := o.initializeRemoteSigningGpg("", "/repo"); err == nil {
		t.Error("Expected error for empty remote")
	}
	if err := o.initializeRemoteSigningGpg("origin", ""); err == nil {
		t.Error("Expected error for empty repoDir")
	}
}

func TestGpgArgsEnabled(t *testing.T) {
	tmpDir := t.TempDir()
	pubKey := filepath.Join(tmpDir, "pub.key")
	os.WriteFile(pubKey, []byte("key"), 0644)

	// Mock GpgKeyID execution
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
			"Ostree.GpgPublicKey":  {pubKey},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": true,
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	o.runner = func(cmd *runner.Cmd) error {
		args, stdout := cmd.Args, cmd.Stdout
		for _, arg := range args {
			if arg == "--show-keys" {
				fmt.Fprintln(stdout, "pub:u:4096:1:KEYID123:1678752000:::u:::scESC:")
				return nil
			}
		}
		return nil
	}

	args, err := o.GpgArgs()
	if err != nil {
		t.Fatalf("GpgArgs failed: %v", err)
	}
	if len(args) != 2 {
		t.Errorf("Expected 2 args, got %d", len(args))
	}
	if !strings.Contains(args[0], "KEYID123") {
		t.Errorf("Expected key ID in args, got %s", args[0])
	}
}

// TestGpgIntegration is a hermetic integration test that generates a real
// GPG key inside a temporary GNUPGHOME directory and exercises
// ImportGpgKey, GpgKeyID, and GpgSignFile end-to-end.
func TestGpgIntegration(t *testing.T) {
	if _, err := exec.LookPath("gpg"); err != nil {
		t.Skip("gpg not found in PATH, skipping integration test")
	}

	tmpDir := t.TempDir()
	gpgHome := filepath.Join(tmpDir, "gnupg")
	if err := os.MkdirAll(gpgHome, 0700); err != nil {
		t.Fatal(err)
	}

	// Generate a throwaway GPG key using a batch spec.
	keySpec := filepath.Join(tmpDir, "key-spec")
	if err := os.WriteFile(keySpec, []byte(strings.Join([]string{
		"%echo Generating test key",
		"Key-Type: RSA",
		"Key-Length: 2048",
		"Name-Real: Test Key",
		"Name-Email: test@test.local",
		"Expire-Date: 0",
		"%no-protection",
		"%commit",
		"%echo Done",
	}, "\n")), 0600); err != nil {
		t.Fatal(err)
	}

	genCmd := exec.Command("gpg", "--homedir", gpgHome, "--batch", "--gen-key", keySpec)
	genCmd.Stderr = os.Stderr
	if err := genCmd.Run(); err != nil {
		t.Fatalf("gpg key generation failed: %v", err)
	}

	// Export the public key so we can point the config at it.
	pubKeyPath := filepath.Join(tmpDir, "pub.gpg")
	exportCmd := exec.Command("gpg", "--homedir", gpgHome, "--batch", "--yes",
		"--export", "--armor", "--output", pubKeyPath)
	exportCmd.Stderr = os.Stderr
	if err := exportCmd.Run(); err != nil {
		t.Fatalf("gpg export failed: %v", err)
	}

	// Export the private key to use as GpgPrivateKey config value.
	privKeyPath := filepath.Join(tmpDir, "priv.gpg")
	exportPriv := exec.Command("gpg", "--homedir", gpgHome, "--batch", "--yes",
		"--export-secret-keys", "--armor", "--output", privKeyPath)
	exportPriv.Stderr = os.Stderr
	if err := exportPriv.Run(); err != nil {
		t.Fatalf("gpg secret export failed: %v", err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir":        {gpgHome},
			"Ostree.GpgPublicKey":         {pubKeyPath},
			"Ostree.GpgOfficialPublicKey": {pubKeyPath},
			"Ostree.GpgPrivateKey":        {privKeyPath},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": true,
		},
	}

	o, err := NewOstree(NewOstreeOptions{Config: cfg})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}

	// 1. ImportGpgKey — re-import the public key (idempotent).
	t.Run("ImportGpgKey", func(t *testing.T) {
		if err := o.ImportGpgKey(pubKeyPath); err != nil {
			t.Fatalf("ImportGpgKey failed: %v", err)
		}
	})

	// 2. GpgKeyID — must return a non-empty key ID.
	var keyID string
	t.Run("GpgKeyID", func(t *testing.T) {
		keyID, err = o.GpgKeyID()
		if err != nil {
			t.Fatalf("GpgKeyID failed: %v", err)
		}
		if keyID == "" {
			t.Fatal("GpgKeyID returned empty key ID")
		}
		t.Logf("GpgKeyID = %s", keyID)
	})

	// 3. GpgSignFile — sign a dummy file and verify the .asc is created.
	t.Run("GpgSignFile", func(t *testing.T) {
		if keyID == "" {
			t.Skip("skipping: GpgKeyID did not produce a key ID")
		}
		dummyFile := filepath.Join(tmpDir, "payload.bin")
		if err := os.WriteFile(dummyFile, []byte("hello world\n"), 0644); err != nil {
			t.Fatal(err)
		}

		if err := o.GpgSignFile(dummyFile); err != nil {
			t.Fatalf("GpgSignFile failed: %v", err)
		}

		ascFile := GpgSignedFilePath(dummyFile)
		info, err := os.Stat(ascFile)
		if err != nil {
			t.Fatalf("signature file not found: %v", err)
		}
		if info.Size() == 0 {
			t.Fatal("signature file is empty")
		}
		t.Logf("signature file %s (%d bytes)", ascFile, info.Size())
	})

	// 4. GpgArgs — must return sign + homedir args.
	t.Run("GpgArgs", func(t *testing.T) {
		args, err := o.GpgArgs()
		if err != nil {
			t.Fatalf("GpgArgs failed: %v", err)
		}
		if len(args) != 2 {
			t.Fatalf("Expected 2 GpgArgs, got %d: %v", len(args), args)
		}
		if !strings.HasPrefix(args[0], "--gpg-sign=") {
			t.Errorf("args[0] = %q, want --gpg-sign=...", args[0])
		}
		if !strings.HasPrefix(args[1], "--gpg-homedir=") {
			t.Errorf("args[1] = %q, want --gpg-homedir=...", args[1])
		}
	})
}

// concurrencyRunner returns a mock runner that tracks the maximum number
// of concurrent executions.  Each invocation holds for the given
// duration to create a window where overlap would be detected.
func concurrencyRunner(hold time.Duration, maxConcurrent *atomic.Int32) runner.Func {
	var active atomic.Int32
	return func(cmd *runner.Cmd) error {
		n := active.Add(1)
		defer active.Add(-1)
		// Record the high-water mark.
		for {
			cur := maxConcurrent.Load()
			if n <= cur || maxConcurrent.CompareAndSwap(cur, n) {
				break
			}
		}
		time.Sleep(hold)
		return nil
	}
}

func TestImportGpgKey_Serialized(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "key.asc")
	if err := os.WriteFile(keyFile, []byte("key data"), 0644); err != nil {
		t.Fatal(err)
	}

	var maxConcurrent atomic.Int32
	mockRun := concurrencyRunner(50*time.Millisecond, &maxConcurrent)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			cfg := &config.MockConfig{
				Items: map[string][]string{
					"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
				},
			}
			o, err := NewOstree(NewOstreeOptions{Config: cfg})
			if err != nil {
				t.Errorf("NewOstree failed: %v", err)
				return
			}
			o.runner = mockRun
			if err := o.ImportGpgKey(keyFile); err != nil {
				t.Errorf("ImportGpgKey failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > 1 {
		t.Errorf("ImportGpgKey allowed %d concurrent executions, want 1", mc)
	}
}

func TestKillGpgDaemons_Serialized(t *testing.T) {
	var maxConcurrent atomic.Int32
	mockRun := concurrencyRunner(50*time.Millisecond, &maxConcurrent)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			KillGpgDaemons(mockRun, "/tmp/test-gpg", io.Discard, io.Discard)
		}()
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > 1 {
		t.Errorf("KillGpgDaemons allowed %d concurrent executions, want 1", mc)
	}
}

func TestInitializeRemoteSigningGpg_Serialized(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "key.asc")
	if err := os.WriteFile(keyFile, []byte("key data"), 0644); err != nil {
		t.Fatal(err)
	}

	var maxConcurrent atomic.Int32
	mockRun := concurrencyRunner(50*time.Millisecond, &maxConcurrent)

	const workers = 8
	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go func() {
			defer wg.Done()
			cfg := &config.MockConfig{
				Items: map[string][]string{
					"Ostree.GpgPrivateKey":        {keyFile},
					"Ostree.GpgPublicKey":         {keyFile},
					"Ostree.GpgOfficialPublicKey": {keyFile},
				},
			}
			o, err := NewOstree(NewOstreeOptions{Config: cfg})
			if err != nil {
				t.Errorf("NewOstree failed: %v", err)
				return
			}
			o.runner = mockRun
			if err := o.initializeRemoteSigningGpg("origin", "/repo"); err != nil {
				t.Errorf("initializeRemoteSigningGpg failed: %v", err)
			}
		}()
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > 1 {
		t.Errorf("initializeRemoteSigningGpg allowed %d concurrent executions, want 1", mc)
	}
}

func TestGpgMutex_CrossFunction(t *testing.T) {
	tmpDir := t.TempDir()
	keyFile := filepath.Join(tmpDir, "key.asc")
	if err := os.WriteFile(keyFile, []byte("key data"), 0644); err != nil {
		t.Fatal(err)
	}

	var maxConcurrent atomic.Int32
	mockRun := concurrencyRunner(50*time.Millisecond, &maxConcurrent)

	// Mix ImportGpgKey, KillGpgDaemons, and initializeRemoteSigningGpg
	// concurrently to verify they all share the same mutex.
	const workers = 12
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := 0; i < workers; i++ {
		fn := i % 3
		go func() {
			defer wg.Done()
			switch fn {
			case 0:
				cfg := &config.MockConfig{
					Items: map[string][]string{
						"Ostree.DevGpgHomeDir": {filepath.Join(tmpDir, "gpg")},
					},
				}
				o, _ := NewOstree(NewOstreeOptions{Config: cfg})
				o.runner = mockRun
				_ = o.ImportGpgKey(keyFile)
			case 1:
				KillGpgDaemons(mockRun, "/tmp/test-gpg", io.Discard, io.Discard)
			case 2:
				cfg := &config.MockConfig{
					Items: map[string][]string{
						"Ostree.GpgPrivateKey":        {keyFile},
						"Ostree.GpgPublicKey":         {keyFile},
						"Ostree.GpgOfficialPublicKey": {keyFile},
					},
				}
				o, _ := NewOstree(NewOstreeOptions{Config: cfg})
				o.runner = mockRun
				_ = o.initializeRemoteSigningGpg("origin", "/repo")
			}
		}()
	}
	wg.Wait()

	if mc := maxConcurrent.Load(); mc > 1 {
		t.Errorf("cross-function GPG calls allowed %d concurrent executions, want 1", mc)
	}
}
