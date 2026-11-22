package seeder

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// ---------- RetryableCmd ----------

func TestRetryableCmd_SuccessOnFirstAttempt(t *testing.T) {
	mr := runner.NewMockRunner()
	s := newTestSeeder()
	s.runner = mr.Run

	err := s.RetryableCmd(3, "echo", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "echo" {
		t.Errorf("call name = %q, want %q", mr.Calls[0].Name, "echo")
	}
}

func TestRetryableCmd_AllAttemptsFail(t *testing.T) {
	wantErr := errors.New("broken")
	mr := &runner.MockRunner{FailOn: -1, Err: wantErr}
	s := newTestSeeder()
	s.runner = mr.Run

	// We override sleep indirectly: since the mock fails immediately,
	// we just verify the error message and number of attempts.
	// NOTE: This test will sleep 5s per retry in real code. To avoid
	// that, we use tries=1.
	err := s.RetryableCmd(1, "fail-cmd")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
}

func TestRetryableCmd_SucceedsAfterRetry(t *testing.T) {
	// Fail on first call (index 0), succeed on second.
	wantErr := errors.New("transient")
	mr := runner.NewMockRunnerFailOnCall(0, wantErr)
	s := newTestSeeder()
	s.runner = mr.Run

	// NOTE: tries=2 means one retry after one failure, but real code
	// sleeps 5s between retries. Using tries=2 to test the retry path
	// while accepting the cost. For fast tests, tries=1 was tested above.
	// We accept the 5s sleep in CI.
	err := s.RetryableCmd(2, "retry-cmd")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(mr.Calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(mr.Calls))
	}
}

// ---------- MaybeInitializePrivateRepo ----------

func newPrivateRepoTestSeeder(t *testing.T) (*Seeder, *runner.MockRunner) {
	t.Helper()
	mr := runner.NewMockRunner()
	s := newTestSeeder()
	s.runner = mr.Run
	s.cfg.(*config.MockConfig).Items["matrixOS.PrivateGitRepoPath"] = []string{filepath.Join(t.TempDir(), "private")}
	s.cfg.(*config.MockConfig).Items["matrixOS.PrivateExampleGitRepo"] = []string{"https://example.com/private.git"}
	s.cfg.(*config.MockConfig).Items["Seeder.GitCloneArgs"] = []string{"--depth 1"}
	return s, mr
}

func TestMaybeInitializePrivateRepo_ClonesWhenMissing(t *testing.T) {
	s, mr := newPrivateRepoTestSeeder(t)

	err := s.MaybeInitializePrivateRepo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should have called git clone and then ./make.sh (via dirRunner).
	if len(mr.Calls) < 2 {
		t.Fatalf("expected at least 2 calls, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "git" {
		t.Errorf("first call = %q, want %q", mr.Calls[0].Name, "git")
	}
	wantGitArgs := []string{"clone", "--depth", "1", "https://example.com/private.git"}
	// Last arg is the temp dir path, just check the prefix.
	gotArgs := mr.Calls[0].Args
	if len(gotArgs) < 5 {
		t.Fatalf("expected at least 5 git args, got %d: %v", len(gotArgs), gotArgs)
	}
	for i, want := range wantGitArgs {
		if gotArgs[i] != want {
			t.Errorf("git arg[%d] = %q, want %q", i, gotArgs[i], want)
		}
	}
	if mr.Calls[1].Name != "./make.sh" {
		t.Errorf("second call = %q, want %q", mr.Calls[1].Name, "./make.sh")
	}
}

func TestMaybeInitializePrivateRepo_RunsMakeWhenNotBuilt(t *testing.T) {
	s, mr := newPrivateRepoTestSeeder(t)

	repoPath, _ := s.PrivateGitRepoPath()
	// Create the repo dir with a .git directory (simulates existing repo).
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0755); err != nil {
		t.Fatal(err)
	}

	err := s.MaybeInitializePrivateRepo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should only call ./make.sh via dirRunner (no git clone).
	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "./make.sh" {
		t.Errorf("call = %q, want %q", mr.Calls[0].Name, "./make.sh")
	}
}

func TestMaybeInitializePrivateRepo_SkipsMakeWhenBuilt(t *testing.T) {
	s, mr := newPrivateRepoTestSeeder(t)

	repoPath, _ := s.PrivateGitRepoPath()
	if err := os.MkdirAll(filepath.Join(repoPath, ".git"), 0755); err != nil {
		t.Fatal(err)
	}
	// Create .built marker.
	if err := os.WriteFile(filepath.Join(repoPath, ".built"), []byte{}, 0644); err != nil {
		t.Fatal(err)
	}

	err := s.MaybeInitializePrivateRepo()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// No commands should be run.
	if len(mr.Calls) != 0 {
		t.Fatalf("expected 0 calls, got %d", len(mr.Calls))
	}
}

func TestMaybeInitializePrivateRepo_ErrorNotGitRepo(t *testing.T) {
	s, _ := newPrivateRepoTestSeeder(t)

	repoPath, _ := s.PrivateGitRepoPath()
	// Create the repo dir without .git.
	if err := os.MkdirAll(repoPath, 0755); err != nil {
		t.Fatal(err)
	}
	// Add a file so it's not empty.
	if err := os.WriteFile(filepath.Join(repoPath, "somefile"), []byte("data"), 0644); err != nil {
		t.Fatal(err)
	}

	err := s.MaybeInitializePrivateRepo()
	if err == nil {
		t.Fatal("expected error for non-git repo")
	}
	if !containsString(err.Error(), "must be a git repo") {
		t.Fatalf("expected 'must be a git repo' message, got: %v", err)
	}
}

func TestMaybeInitializePrivateRepo_MissingConfig(t *testing.T) {
	s := newTestSeeder()
	// No matrixOS.PrivateGitRepoPath configured.

	err := s.MaybeInitializePrivateRepo()
	if err == nil {
		t.Fatal("expected error for missing config")
	}
}

func TestMaybeInitializePrivateRepo_GitCloneFails(t *testing.T) {
	wantErr := errors.New("git clone failed")
	mr := runner.NewMockRunnerFailOnCall(0, wantErr)
	s := newTestSeeder()
	s.runner = mr.Run
	s.cfg.(*config.MockConfig).Items["matrixOS.PrivateGitRepoPath"] = []string{filepath.Join(t.TempDir(), "private")}
	s.cfg.(*config.MockConfig).Items["matrixOS.PrivateExampleGitRepo"] = []string{"https://example.com/private.git"}

	err := s.MaybeInitializePrivateRepo()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// ---------- NewSeeder ----------

func TestNewSeeder_NilConfig(t *testing.T) {
	_, err := NewSeeder(nil, nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNewSeeder_NilOpts(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}}
	s, err := NewSeeder(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s == nil {
		t.Fatal("expected non-nil seeder")
	}
}

func TestNewSeeder_DefaultWriters(t *testing.T) {
	cfg := &config.MockConfig{Items: map[string][]string{}}
	s, err := NewSeeder(cfg, &NewSeederOptions{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s.Stdout() != os.Stdout {
		t.Error("expected default stdout")
	}
	if s.Stderr() != os.Stderr {
		t.Error("expected default stderr")
	}
}

// ---------- I/O ----------

func TestPrint_WritesToStdout(t *testing.T) {
	s := newTestSeeder()

	s.Print("hello %s %d", "world", 42)

	got := s.Stdout().(*bytes.Buffer).String()
	want := "hello world 42"
	if got != want {
		t.Fatalf("Print output = %q, want %q", got, want)
	}
}

func TestPrintWarning_WritesToStderr(t *testing.T) {
	s := newTestSeeder()

	s.PrintWarning("warn: %s", "oops")

	got := s.Stderr().(*bytes.Buffer).String()
	want := "warn: oops"
	if got != want {
		t.Fatalf("PrintWarning output = %q, want %q", got, want)
	}
}

func TestPrintError_WritesToStderr(t *testing.T) {
	s := newTestSeeder()

	s.PrintError("err: %d", 404)

	got := s.Stderr().(*bytes.Buffer).String()
	want := "err: 404"
	if got != want {
		t.Fatalf("PrintError output = %q, want %q", got, want)
	}
}

func TestSetStdout_ReplaceWriter(t *testing.T) {
	s := newTestSeeder()
	var buf bytes.Buffer

	s.SetStdout(&buf)
	if s.Stdout() != &buf {
		t.Fatal("Stdout() did not return the writer set by SetStdout")
	}
}

func TestSetStderr_ReplaceWriter(t *testing.T) {
	s := newTestSeeder()
	var buf bytes.Buffer

	s.SetStderr(&buf)
	if s.Stderr() != &buf {
		t.Fatal("Stderr() did not return the writer set by SetStderr")
	}
}

// ---------- runMakeInDir (integration) ----------

func TestRunMakeInDir_ExecutesMakeScript(t *testing.T) {
	dir := t.TempDir()

	// Create a make.sh that writes a marker file when executed.
	marker := filepath.Join(dir, "executed")
	script := "#!/bin/sh\ntouch " + marker + "\n"
	scriptPath := filepath.Join(dir, "make.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}

	s := newTestSeeder()
	s.runner = runner.Run // use the real implementation

	err := s.runMakeInDir(dir)
	if err != nil {
		t.Fatalf("runMakeInDir failed: %v", err)
	}

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("make.sh was not executed (marker file missing): %v", err)
	}
}

// ---------- gitCloneArgs (shell lexing) ----------

// ---------- cloneRepo ----------

func newCloneRepoTestSeeder(t *testing.T, gitCloneArgs string) (*Seeder, *runner.MockRunner) {
	t.Helper()
	mr := runner.NewMockRunner()
	s := newTestSeeder()
	s.runner = mr.Run
	s.cfg.(*config.MockConfig).Items["Seeder.GitCloneArgs"] = []string{gitCloneArgs}
	return s, mr
}

func TestCloneRepo_DefaultArgs(t *testing.T) {
	s, mr := newCloneRepoTestSeeder(t, "--depth 1")
	dir := t.TempDir() + "/repo"

	err := s.cloneRepo(dir, "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(mr.Calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(mr.Calls))
	}
	if mr.Calls[0].Name != "git" {
		t.Errorf("command = %q, want %q", mr.Calls[0].Name, "git")
	}
	wantArgs := []string{"clone", "--depth", "1", "https://example.com/repo.git", dir}
	if !reflect.DeepEqual(mr.Calls[0].Args, wantArgs) {
		t.Errorf("args = %v, want %v", mr.Calls[0].Args, wantArgs)
	}
}

func TestCloneRepo_MultipleCloneFlags(t *testing.T) {
	s, mr := newCloneRepoTestSeeder(t, "--depth 1 --single-branch --no-tags")
	dir := t.TempDir() + "/repo"

	err := s.cloneRepo(dir, "git@host:org/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	wantArgs := []string{"clone", "--depth", "1", "--single-branch", "--no-tags", "git@host:org/repo.git", dir}
	if !reflect.DeepEqual(mr.Calls[0].Args, wantArgs) {
		t.Errorf("args = %v, want %v", mr.Calls[0].Args, wantArgs)
	}
}

func TestCloneRepo_CreatesDirectory(t *testing.T) {
	s, _ := newCloneRepoTestSeeder(t, "--depth 1")
	dir := filepath.Join(t.TempDir(), "nested", "deep", "repo")

	err := s.cloneRepo(dir, "https://example.com/repo.git")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("expected directory, got file")
	}
}

func TestCloneRepo_GitFails(t *testing.T) {
	wantErr := errors.New("network error")
	mr := runner.NewMockRunnerFailOnCall(0, wantErr)
	s := newTestSeeder()
	s.runner = mr.Run
	s.cfg.(*config.MockConfig).Items["Seeder.GitCloneArgs"] = []string{"--depth 1"}

	err := s.cloneRepo(t.TempDir()+"/repo", "https://example.com/repo.git")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}

func TestCloneRepo_ConfigError(t *testing.T) {
	wantErr := errors.New("config broken")
	s := newTestSeeder()
	s.cfg = &config.ErrConfig{Err: wantErr}

	err := s.cloneRepo(t.TempDir()+"/repo", "https://example.com/repo.git")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}

// ---------- gitCloneArgs (shell lexing) ----------

func TestGitCloneArgs_SimpleSplit(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.GitCloneArgs"] = []string{"--depth 1"}

	got, err := s.gitCloneArgs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--depth", "1"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGitCloneArgs_MultipleFlags(t *testing.T) {
	s := newTestSeeder()
	s.cfg.(*config.MockConfig).Items["Seeder.GitCloneArgs"] = []string{
		"--depth 1 --single-branch --no-tags",
	}

	got, err := s.gitCloneArgs()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"--depth", "1", "--single-branch", "--no-tags"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestGitCloneArgs_ConfigError(t *testing.T) {
	wantErr := errors.New("cfg broken")
	s := newTestSeeder()
	s.cfg = &config.ErrConfig{Err: wantErr}

	_, err := s.gitCloneArgs()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("got error %v, want %v", err, wantErr)
	}
}
