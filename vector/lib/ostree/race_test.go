package ostree

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

func TestOstree_GpgConcurrentSerialization(t *testing.T) {
	tmp := t.TempDir()
	gpgHome := filepath.Join(tmp, "gnupg")
	pubKey := filepath.Join(tmp, "pub.asc")
	payload := filepath.Join(tmp, "payload.bin")
	if err := os.WriteFile(pubKey, []byte("pub"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(payload, []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.DevGpgHomeDir":        {gpgHome},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {pubKey},
		},
	}

	var inFlight atomic.Int32
	var maxInFlight atomic.Int32
	mockRunner := func(cmd *runner.Cmd) error {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(15 * time.Millisecond)
		inFlight.Add(-1)
		if cmd.Stdout != nil {
			fmt.Fprintln(cmd.Stdout, "pub:u:4096:1:AABBCCDDEEFF0011:1700000000:::")
		}
		return nil
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := NewOstree(NewOstreeOptions{Config: cfg})
			if err != nil {
				t.Errorf("NewOstree: %v", err)
				return
			}
			o.runner = mockRunner
			if _, err := o.GpgKeyID(); err != nil {
				t.Errorf("GpgKeyID: %v", err)
			}
			if err := o.GpgSignFile(payload); err != nil {
				t.Errorf("GpgSignFile: %v", err)
			}
		}()
	}
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("concurrent gpg invocations detected: maxInFlight = %d, want 1", got)
	}
}

func TestOstree_MaybeInitializeRemoteConcurrentTOCTOU(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "ostree-repo")
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":   {repoDir},
			"Ostree.Remote":    {"origin"},
			"Ostree.RemoteUrl": {"http://example.com"},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": false,
		},
	}

	var mu sync.Mutex
	remotes := map[string]bool{}
	var initCount atomic.Int32
	var remoteAddCount atomic.Int32

	mockRunner := func(cmd *runner.Cmd) error {
		args := cmd.Args
		for i, a := range args {
			switch a {
			case "init":
				initCount.Add(1)
				time.Sleep(15 * time.Millisecond)
				return os.MkdirAll(filepath.Join(repoDir, "objects"), 0755)
			case "remote":
				if i+1 < len(args) && args[i+1] == "list" {
					mu.Lock()
					for r := range remotes {
						fmt.Fprintln(cmd.Stdout, r)
					}
					mu.Unlock()
					return nil
				}
				if i+1 < len(args) && args[i+1] == "add" {
					remoteAddCount.Add(1)
					time.Sleep(15 * time.Millisecond)
					mu.Lock()
					defer mu.Unlock()
					force := false
					for _, x := range args {
						if x == "--force" {
							force = true
						}
					}
					if remotes["origin"] && !force {
						return fmt.Errorf("remote origin already exists")
					}
					remotes["origin"] = true
					return nil
				}
			}
		}
		return nil
	}

	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			o, err := NewOstree(NewOstreeOptions{Config: cfg})
			if err != nil {
				errs <- err
				return
			}
			o.runner = mockRunner
			if err := o.MaybeInitializeRemote(); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Errorf("concurrent MaybeInitializeRemote failed: %v", err)
	}
	if got := initCount.Load(); got != 1 {
		t.Errorf("InitRepo ran %d times across concurrent callers, want 1", got)
	}
	if got := remoteAddCount.Load(); got != 1 {
		t.Errorf("remote add ran %d times across concurrent callers, want 1", got)
	}
}

func TestOstree_WarningsAndValidationUseInjectedStderr(t *testing.T) {
	var stderrBuf bytes.Buffer
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir": {t.TempDir()},
		},
	}
	o, err := NewOstree(NewOstreeOptions{
		Config: cfg,
		Stdout: &bytes.Buffer{},
		Stderr: &stderrBuf,
		Ref:    "matrixos/amd64/dev/test",
	})
	if err != nil {
		t.Fatal(err)
	}

	// ValidateFilesystemHierarchy on an empty temp dir should report issues
	// to o.stderr instead of writing directly to os.Stderr.
	_ = o.ValidateFilesystemHierarchy(t.TempDir())
	if !strings.Contains(stderrBuf.String(), "Expected") {
		t.Errorf("ValidateFilesystemHierarchy did not write diagnostics to injected stderr; got %q", stderrBuf.String())
	}

	// GenerateStaticDelta warning fallback should also write to o.stderr.
	stderrBuf.Reset()
	o.runner = func(cmd *runner.Cmd) error {
		if len(cmd.Args) >= 3 && slices.Contains(cmd.Args, "rev-parse") {
			target := cmd.Args[len(cmd.Args)-1]
			if target == "matrixos/amd64/dev/test" {
				fmt.Fprintln(cmd.Stdout, "newrev")
				return nil
			}
			if target == "matrixos/amd64/dev/test^" {
				fmt.Fprintln(cmd.Stdout, "oldrev")
				return nil
			}
			// Fail rev-parse for oldrev to trigger WARNING fallback.
			return fmt.Errorf("missing oldrev")
		}
		return nil
	}
	if err := o.GenerateStaticDelta(); err != nil {
		t.Fatalf("GenerateStaticDelta: %v", err)
	}
	if !strings.Contains(stderrBuf.String(), "WARNING:") {
		t.Errorf("GenerateStaticDelta did not write fallback warning to injected stderr; got %q", stderrBuf.String())
	}
}
