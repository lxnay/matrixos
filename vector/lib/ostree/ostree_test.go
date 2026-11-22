package ostree

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/runner"
)

// checkOstreeAvailable skips the current test if the ostree binary is not
// found on the system.  Shared across all _test.go files in this package.
func checkOstreeAvailable(t *testing.T) {
	t.Helper()
	_, err := exec.LookPath("ostree")
	if err != nil {
		t.Skip("ostree binary not found, skipping integration tests")
	}
}

// setupTestRepo creates a temporary ostree archive repository and returns its
// path.  Shared across all _test.go files in this package.
func setupTestRepo(t *testing.T) string {
	t.Helper()
	checkOstreeAvailable(t)
	dir := t.TempDir()

	cmd := exec.Command("ostree", "init", "--repo="+dir, "--mode=archive")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to init ostree repo: %v, output: %s", err, out)
	}
	return dir
}

// errorReader is an io.Reader that always returns an error.
type errorReader struct{}

func (e *errorReader) Read(p []byte) (n int, err error) {
	return 0, fmt.Errorf("simulated error")
}

func TestSetupEnvironment(t *testing.T) {
	os.Unsetenv("LC_TIME")
	SetupEnvironment()
	if got := os.Getenv("LC_TIME"); got != "C" {
		t.Errorf("LC_TIME = %q, want C", got)
	}
}

func TestReaderHelpers(t *testing.T) {
	// readerToList
	r := strings.NewReader("line1\n  line2  \n\nline3")
	list, err := readerToList(r)
	if err != nil {
		t.Errorf("readerToList failed: %v", err)
	}
	if len(list) != 3 || list[1] != "line2" {
		t.Errorf("readerToList mismatch: %v", list)
	}

	_, err = readerToList(&errorReader{})
	if err == nil {
		t.Error("readerToList should fail with errorReader")
	}

	// readerToFirstNonEmptyLine
	r = strings.NewReader("\n  \n  first  \nsecond")
	line, err := readerToFirstNonEmptyLine(r)
	if err != nil {
		t.Errorf("readerToFirstNonEmptyLine failed: %v", err)
	}
	if line != "first" {
		t.Errorf("readerToFirstNonEmptyLine = %q, want 'first'", line)
	}

	_, err = readerToFirstNonEmptyLine(&errorReader{})
	if err == nil {
		t.Error("readerToFirstNonEmptyLine should fail with errorReader")
	}
}

func TestFileHelpers(t *testing.T) {
	tmpDir := t.TempDir()
	file := filepath.Join(tmpDir, "file")
	os.WriteFile(file, []byte("content"), 0644)

	if !pathExists(file) {
		t.Error("pathExists(file) = false")
	}
	if !pathExists(tmpDir) {
		t.Error("pathExists(dir) = false")
	}
	if pathExists(filepath.Join(tmpDir, "nonexistent")) {
		t.Error("pathExists(nonexistent) = true")
	}

	if !fileExists(file) {
		t.Error("fileExists(file) = false")
	}
	if fileExists(tmpDir) {
		t.Error("fileExists(dir) = true")
	}

	if directoryExists(file) {
		t.Error("directoryExists(file) = true")
	}
	if !directoryExists(tmpDir) {
		t.Error("directoryExists(dir) = false")
	}
}

func TestRunVerbose(t *testing.T) {
	o, err := NewOstree(NewOstreeOptions{
		Config:  &config.MockConfig{},
		Verbose: true,
	})
	if err != nil {
		t.Fatalf("NewOstree failed: %v", err)
	}
	o.runner = func(cmd *runner.Cmd) error {
		args := cmd.Args
		if len(args) > 0 && args[0] == "--verbose" {
			return nil
		}
		return fmt.Errorf("expected --verbose")
	}

	if err := o.ostreeRun("arg"); err != nil {
		t.Errorf("ostreeRun failed: %v", err)
	}
}
