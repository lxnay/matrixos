package commands

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"matrixos/vector/lib/config"
)

// newTestKargsCommand creates a KargsCommand with injected mocks,
// bypassing initConfig which requires real config files.
// It wires a shared buffer into the UI printers so tests can inspect output.
func newTestKargsCommand(
	cfg *config.MockConfig,
	runner *kargsRunner,
	args []string,
	outBuf *bytes.Buffer,
) (*KargsCommand, error) {
	cmd := &KargsCommand{}
	cmd.cfg = cfg
	cmd.StartUI()
	if outBuf != nil {
		cmd.printer = newStyledWriter(outBuf, nil, "", "", "", false)
		cmd.errPrinter = newStyledWriter(outBuf, nil, "", "", "", false)
	}
	cmd.run = runner
	if err := cmd.parseArgs(args); err != nil {
		return nil, err
	}
	return cmd, nil
}

// testKargsRunner creates a kargsRunner with sensible defaults for testing.
func testKargsRunner() *kargsRunner {
	return &kargsRunner{
		readFile: func(path string) ([]byte, error) {
			return nil, fmt.Errorf("readFile not mocked for %s", path)
		},
		writeFile: func(path string, data []byte, perm os.FileMode) error {
			return nil
		},
		glob: func(pattern string) ([]string, error) {
			return nil, nil
		},
		getEuid: func() int { return 0 },
	}
}

func kargsTestConfig() *config.MockConfig {
	return &config.MockConfig{
		Items: map[string][]string{},
	}
}

// sampleBLS returns a typical BLS config file content.
func sampleBLS(options string) string {
	return fmt.Sprintf(`title matrixOS (Gentoo-based) (ostree:1)
version 1
options %s
linux /ostree/matrixos-abc123/vmlinuz-6.19.3-matrixos
initrd /ostree/matrixos-abc123/initramfs-6.19.3-matrixos.img`, options)
}

func TestKargsName(t *testing.T) {
	cmd := &KargsCommand{}
	if cmd.Name() != "kargs" {
		t.Fatalf("expected name 'kargs', got %q", cmd.Name())
	}
}

func TestKargsNoSubcommand(t *testing.T) {
	runner := testKargsRunner()
	_, err := newTestKargsCommand(kargsTestConfig(), runner, nil, nil)
	if err == nil || !strings.Contains(err.Error(), "no subcommand provided") {
		t.Fatalf("expected 'no subcommand' error, got: %v", err)
	}
}

func TestKargsUnknownSubcommand(t *testing.T) {
	runner := testKargsRunner()
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"bogus"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

func TestKargsAddRequiresRoot(t *testing.T) {
	runner := testKargsRunner()
	runner.getEuid = func() int { return 1000 }
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "quiet"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "must be run as root") {
		t.Fatalf("expected root error, got: %v", err)
	}
}

func TestKargsRmRequiresRoot(t *testing.T) {
	runner := testKargsRunner()
	runner.getEuid = func() int { return 1000 }
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"rm", "quiet"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "must be run as root") {
		t.Fatalf("expected root error, got: %v", err)
	}
}

func TestKargsAddNoArgs(t *testing.T) {
	runner := testKargsRunner()
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "requires at least one kernel argument") {
		t.Fatalf("expected 'requires at least one' error, got: %v", err)
	}
}

func TestKargsRmNoArgs(t *testing.T) {
	runner := testKargsRunner()
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"rm"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "requires at least one kernel argument") {
		t.Fatalf("expected 'requires at least one' error, got: %v", err)
	}
}

func TestKargsAddNoBLSEntries(t *testing.T) {
	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return nil, nil
	}
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "quiet"}, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "no BLS entries") {
		t.Fatalf("expected 'no BLS entries' error, got: %v", err)
	}
}

func TestKargsAddSingleEntry(t *testing.T) {
	var buf bytes.Buffer
	initialOptions := "root=UUID=abc rw splash quiet"
	written := map[string]string{}

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/ostree-1.conf"}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		return []byte(sampleBLS(initialOptions)), nil
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "debug"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify the file was written with the new arg.
	content, ok := written["/boot/loader/entries/ostree-1.conf"]
	if !ok {
		t.Fatal("expected file to be written")
	}
	if !strings.Contains(content, "debug") {
		t.Errorf("expected 'debug' in written content, got: %s", content)
	}
	if !strings.Contains(content, initialOptions) {
		t.Errorf("expected original options preserved, got: %s", content)
	}
}

func TestKargsAddDuplicateSkipped(t *testing.T) {
	var buf bytes.Buffer
	initialOptions := "root=UUID=abc rw splash quiet"
	writeCount := 0

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/ostree-1.conf"}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		return []byte(sampleBLS(initialOptions)), nil
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writeCount++
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "quiet"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if writeCount != 0 {
		t.Errorf("expected no writes for duplicate arg, got %d", writeCount)
	}
	output := buf.String()
	if !strings.Contains(output, "No changes needed") {
		t.Errorf("expected 'No changes needed' message, got: %s", output)
	}
}

func TestKargsAddMultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	opts1 := "root=UUID=abc rw"
	opts2 := "root=UUID=def rw"
	written := map[string]string{}

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{
			"/boot/loader/entries/ostree-1.conf",
			"/boot/loader/entries/ostree-2.conf",
		}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/boot/loader/entries/ostree-1.conf":
			return []byte(sampleBLS(opts1)), nil
		case "/boot/loader/entries/ostree-2.conf":
			return []byte(sampleBLS(opts2)), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "quiet", "splash"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, path := range []string{
		"/boot/loader/entries/ostree-1.conf",
		"/boot/loader/entries/ostree-2.conf",
	} {
		content, ok := written[path]
		if !ok {
			t.Fatalf("expected %s to be written", path)
		}
		if !strings.Contains(content, "quiet") || !strings.Contains(content, "splash") {
			t.Errorf("expected 'quiet splash' in %s, got: %s", path, content)
		}
	}

	output := buf.String()
	if !strings.Contains(output, "Current kernel options") {
		t.Error("expected summary to be printed")
	}
}

func TestKargsRmSingleEntry(t *testing.T) {
	var buf bytes.Buffer
	initialOptions := "root=UUID=abc rw splash quiet debug"
	written := map[string]string{}

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/ostree-1.conf"}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		return []byte(sampleBLS(initialOptions)), nil
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"rm", "debug", "splash"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	content, ok := written["/boot/loader/entries/ostree-1.conf"]
	if !ok {
		t.Fatal("expected file to be written")
	}
	if strings.Contains(content, "debug") {
		t.Errorf("expected 'debug' to be removed, got: %s", content)
	}
	if strings.Contains(content, "splash") {
		t.Errorf("expected 'splash' to be removed, got: %s", content)
	}
	if !strings.Contains(content, "root=UUID=abc") || !strings.Contains(content, "rw") || !strings.Contains(content, "quiet") {
		t.Errorf("expected remaining args preserved, got: %s", content)
	}
}

func TestKargsRmNotPresent(t *testing.T) {
	var buf bytes.Buffer
	initialOptions := "root=UUID=abc rw quiet"
	writeCount := 0

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/ostree-1.conf"}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		return []byte(sampleBLS(initialOptions)), nil
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		writeCount++
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"rm", "nonexistent"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if writeCount != 0 {
		t.Errorf("expected no writes when removing non-existent arg, got %d", writeCount)
	}
	if !strings.Contains(buf.String(), "No changes needed") {
		t.Error("expected 'No changes needed' message")
	}
}

func TestKargsRmMultipleEntries(t *testing.T) {
	var buf bytes.Buffer
	opts1 := "root=UUID=abc rw splash quiet debug"
	opts2 := "root=UUID=def rw splash quiet debug"
	written := map[string]string{}

	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{
			"/boot/loader/entries/ostree-1.conf",
			"/boot/loader/entries/ostree-2.conf",
		}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		switch path {
		case "/boot/loader/entries/ostree-1.conf":
			return []byte(sampleBLS(opts1)), nil
		case "/boot/loader/entries/ostree-2.conf":
			return []byte(sampleBLS(opts2)), nil
		}
		return nil, fmt.Errorf("unexpected path: %s", path)
	}
	runner.writeFile = func(path string, data []byte, perm os.FileMode) error {
		written[path] = string(data)
		return nil
	}

	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"rm", "debug"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	for _, path := range []string{
		"/boot/loader/entries/ostree-1.conf",
		"/boot/loader/entries/ostree-2.conf",
	} {
		content, ok := written[path]
		if !ok {
			t.Fatalf("expected %s to be written", path)
		}
		if strings.Contains(content, "debug") {
			t.Errorf("expected 'debug' removed from %s, got: %s", path, content)
		}
	}
}

func TestKargsNoOptionsLine(t *testing.T) {
	runner := testKargsRunner()
	runner.glob = func(pattern string) ([]string, error) {
		return []string{"/boot/loader/entries/ostree-1.conf"}, nil
	}
	runner.readFile = func(path string) ([]byte, error) {
		return []byte("title matrixOS\nversion 1\nlinux /vmlinuz"), nil
	}

	var buf bytes.Buffer
	cmd, err := newTestKargsCommand(kargsTestConfig(), runner, []string{"add", "quiet"}, &buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	err = cmd.Run()
	if err == nil || !strings.Contains(err.Error(), "no 'options' line found") {
		t.Fatalf("expected 'no options line' error, got: %v", err)
	}
}

func TestGetOptionsLine(t *testing.T) {
	lines := []string{
		"title matrixOS",
		"version 1",
		"options root=UUID=abc rw quiet",
		"linux /vmlinuz",
	}

	idx, opts := getOptionsLine(lines)
	if idx != 2 {
		t.Errorf("expected index 2, got %d", idx)
	}
	if opts != "root=UUID=abc rw quiet" {
		t.Errorf("unexpected options: %q", opts)
	}
}

func TestGetOptionsLineNotFound(t *testing.T) {
	lines := []string{
		"title matrixOS",
		"version 1",
		"linux /vmlinuz",
	}

	idx, opts := getOptionsLine(lines)
	if idx != -1 {
		t.Errorf("expected -1, got %d", idx)
	}
	if opts != "" {
		t.Errorf("expected empty, got %q", opts)
	}
}

func TestKargsPrintAllEntries(t *testing.T) {
	opts := "root=UUID=abc rw quiet"
	runner := testKargsRunner()
	runner.readFile = func(path string) ([]byte, error) {
		return []byte(sampleBLS(opts)), nil
	}

	cmd := &KargsCommand{}
	cmd.StartUI()
	cmd.run = runner
	var buf bytes.Buffer
	cmd.printer = newStyledWriter(&buf, nil, "", "", "", false)
	cmd.errPrinter = newStyledWriter(&buf, nil, "", "", "", false)

	err := cmd.printAllEntries([]string{"/boot/loader/entries/ostree-1.conf"})
	if err != nil {
		t.Fatalf("printAllEntries failed: %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "ostree-1.conf") {
		t.Error("expected entry filename in output")
	}
	if !strings.Contains(output, "root=UUID=abc") {
		t.Error("expected kernel args in output")
	}
}
