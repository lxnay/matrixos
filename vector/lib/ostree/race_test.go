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

func TestOstree_CommitAndUpdateSummarySerializeWithGpgMu(t *testing.T) {
	tmp := t.TempDir()
	gpgHome := filepath.Join(tmp, "gnupg")
	pubKey := filepath.Join(tmp, "pub.asc")
	imageDir := filepath.Join(tmp, "img")
	if err := os.WriteFile(pubKey, []byte("pub"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(imageDir, 0755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir":              {filepath.Join(tmp, "repo")},
			"Ostree.DevGpgHomeDir":        {gpgHome},
			"Ostree.GpgPublicKey":         {pubKey},
			"Ostree.GpgOfficialPublicKey": {pubKey},
		},
		Bools: map[string]bool{
			"Ostree.Gpg": true,
		},
	}

	var inFlight, maxInFlight atomic.Int32
	mockRunner := func(cmd *runner.Cmd) error {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		inFlight.Add(-1)
		if cmd.Name == "gpg" && cmd.Stdout != nil {
			fmt.Fprintln(cmd.Stdout, "pub:u:4096:1:AABBCCDDEEFF0011:1700000000:::")
		}
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: cfg, Ref: "matrixos/amd64/dev/a"})
		o.runner = mockRunner
		_ = o.Commit(CommitOptions{
			ImageDir: imageDir,
			GpgArgs:  []string{"--gpg-sign=AABBCCDDEEFF0011", "--gpg-homedir=" + gpgHome},
		})
	}()
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: cfg, Ref: "matrixos/amd64/dev/b"})
		o.runner = mockRunner
		_ = o.UpdateSummary()
	}()
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: cfg, Ref: "matrixos/amd64/dev/c"})
		o.runner = mockRunner
		_, _ = o.GpgKeyID()
	}()
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Fatalf("GPG-signing Commit/UpdateSummary overlapped with concurrent GPG ops: maxInFlight = %d, want 1", got)
	}
}

func TestOstree_AddRemotePullAndLastCommitSerializeWithLockRepo(t *testing.T) {
	tmp := t.TempDir()
	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Also exercise a non-canonical equivalent path ("repo/.").
	altRepoDir := filepath.Join(repoDir, ".", "sub", "..")

	makeCfg := func(dir string) *config.MockConfig {
		return &config.MockConfig{
			Items: map[string][]string{
				"Ostree.RepoDir":   {dir},
				"Ostree.Remote":    {"origin"},
				"Ostree.RemoteUrl": {"http://example.com"},
			},
			Bools: map[string]bool{
				"Ostree.Gpg": false,
			},
		}
	}

	var inFlight, maxInFlight atomic.Int32
	mockRunner := func(cmd *runner.Cmd) error {
		cur := inFlight.Add(1)
		for {
			prev := maxInFlight.Load()
			if cur <= prev || maxInFlight.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(25 * time.Millisecond)
		inFlight.Add(-1)
		if slices.Contains(cmd.Args, "rev-parse") && cmd.Stdout != nil {
			fmt.Fprintln(cmd.Stdout, "deadbeef")
		}
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: makeCfg(repoDir), Ref: "matrixos/amd64/dev/a"})
		o.runner = mockRunner
		if err := o.AddRemote(); err != nil {
			t.Errorf("AddRemote: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: makeCfg(altRepoDir), Ref: "matrixos/amd64/dev/b"})
		o.runner = mockRunner
		if err := o.Pull(); err != nil {
			t.Errorf("Pull: %v", err)
		}
	}()
	go func() {
		defer wg.Done()
		o, _ := NewOstree(NewOstreeOptions{Config: makeCfg(repoDir), Ref: "matrixos/amd64/dev/c"})
		o.runner = mockRunner
		if _, err := o.LastCommit(); err != nil {
			t.Errorf("LastCommit: %v", err)
		}
	}()
	wg.Wait()

	if got := maxInFlight.Load(); got > 1 {
		t.Fatalf("repo operations overlapped on same repo: maxInFlight = %d, want 1", got)
	}
}

func TestOstree_CloneForRefPreservesRunner(t *testing.T) {
	cfg := &config.MockConfig{
		Items: map[string][]string{
			"Ostree.RepoDir": {t.TempDir()},
		},
	}
	o, err := NewOstree(NewOstreeOptions{Config: cfg, Ref: "ref-a"})
	if err != nil {
		t.Fatal(err)
	}
	var called atomic.Bool
	o.runner = func(cmd *runner.Cmd) error {
		called.Store(true)
		if cmd.Stdout != nil {
			fmt.Fprintln(cmd.Stdout, "cafebabe")
		}
		return nil
	}
	cloned, err := o.CloneForRef("ref-b")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := cloned.LastCommit()
	if err != nil {
		t.Fatalf("cloned.LastCommit() failed (likely lost custom runner): %v", err)
	}
	if !called.Load() || commit != "cafebabe" {
		t.Fatalf("called=%v commit=%q, want true/cafebabe", called.Load(), commit)
	}
}

func TestParseOstreeLsChecksumLine_ShortDirectoryLineDoesNotPanic(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("ParseOstreeLsChecksumLine panicked on 6-field dir line: %v", r)
		}
	}()
	if _, err := ParseOstreeLsChecksumLine("d00755 0 0 0 aaa111 bbb222"); err == nil {
		t.Fatal("expected error for truncated 6-field directory line")
	}
}

func TestPrepareFilesystemHierarchy_RejectsRootAndEmpty(t *testing.T) {
	o, err := NewOstree(NewOstreeOptions{Config: &config.MockConfig{Items: map[string][]string{}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := o.PrepareFilesystemHierarchy(""); err == nil || !strings.Contains(err.Error(), "missing rootfs") {
		t.Fatalf("expected missing rootfs error for empty rootfs, got: %v", err)
	}
	if err := o.PrepareFilesystemHierarchy("/"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "root") {
		t.Fatalf("expected root safety error for '/', got: %v", err)
	}
}
