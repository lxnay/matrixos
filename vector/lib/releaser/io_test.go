package releaser

import (
	"bytes"
	"os"
	"testing"
)

func TestSetStdout_ReplaceWriter(t *testing.T) {
	r := newTestReleaser()
	var buf bytes.Buffer

	r.SetStdout(&buf)
	if r.Stdout() != &buf {
		t.Fatal("Stdout() did not return the writer set by SetStdout")
	}
}

func TestSetStderr_ReplaceWriter(t *testing.T) {
	r := newTestReleaser()
	var buf bytes.Buffer

	r.SetStderr(&buf)
	if r.Stderr() != &buf {
		t.Fatal("Stderr() did not return the writer set by SetStderr")
	}
}

func TestSetChrootDir_And_ChrootDir(t *testing.T) {
	r := newTestReleaser()

	r.SetChrootDir("/some/chroot")
	if got := r.ChrootDir(); got != "/some/chroot" {
		t.Fatalf("ChrootDir() = %q, want %q", got, "/some/chroot")
	}
}

func TestSetImageDir_ValidDir(t *testing.T) {
	r := newTestReleaser()
	dir := t.TempDir()

	if err := r.SetImageDir(dir); err != nil {
		t.Fatalf("SetImageDir(%q) unexpected error: %v", dir, err)
	}
	if got := r.ImageDir(); got != dir {
		t.Fatalf("ImageDir() = %q, want %q", got, dir)
	}
}

func TestSetImageDir_EmptyString(t *testing.T) {
	r := newTestReleaser()

	if err := r.SetImageDir(""); err == nil {
		t.Fatal("SetImageDir(\"\") should return an error")
	}
}

func TestSetImageDir_NonexistentDir(t *testing.T) {
	r := newTestReleaser()

	if err := r.SetImageDir("/nonexistent/path/that/does/not/exist"); err == nil {
		t.Fatal("SetImageDir with nonexistent path should return an error")
	}
}

func TestSetImageDir_PreservesOldOnError(t *testing.T) {
	r := newTestReleaser()
	dir := t.TempDir()

	_ = r.SetImageDir(dir)
	_ = r.SetImageDir("/nonexistent")

	if got := r.ImageDir(); got != dir {
		t.Fatalf("ImageDir() = %q after failed SetImageDir, want %q", got, dir)
	}
}

func TestSetRef_And_Ref(t *testing.T) {
	r := newTestReleaser()

	r.SetRef("origin/matrixos")
	if got := r.Ref(); got != "origin/matrixos" {
		t.Fatalf("Ref() = %q, want %q", got, "origin/matrixos")
	}
}

func TestPrint_WritesToStdout(t *testing.T) {
	r := newTestReleaser()

	r.Print("hello %s %d", "world", 42)

	got := r.Stdout().(*bytes.Buffer).String()
	want := "hello world 42"
	if got != want {
		t.Fatalf("Print output = %q, want %q", got, want)
	}
}

func TestPrintWarning_WritesToStderr(t *testing.T) {
	r := newTestReleaser()

	r.PrintWarning("warn: %s", "oops")

	got := r.Stderr().(*bytes.Buffer).String()
	want := "warn: oops"
	if got != want {
		t.Fatalf("PrintWarning output = %q, want %q", got, want)
	}
}

func TestPrintError_WritesToStderr(t *testing.T) {
	r := newTestReleaser()

	r.PrintError("err: %d", 404)

	got := r.Stderr().(*bytes.Buffer).String()
	want := "err: 404"
	if got != want {
		t.Fatalf("PrintError output = %q, want %q", got, want)
	}
}

func TestSetImageDir_FileInsteadOfDir(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "notadir")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()

	r := newTestReleaser()
	if err := r.SetImageDir(f.Name()); err == nil {
		t.Fatal("SetImageDir with a file path should return an error")
	}
}
