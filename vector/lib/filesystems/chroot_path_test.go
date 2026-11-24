package filesystems

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

type fakeFileInfo struct {
	name string
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.mode.IsDir() }
func (f fakeFileInfo) Sys() any           { return nil }

func TestResolvePathInRootWith_RootSlashWalksSymlinksAndDotDot(t *testing.T) {
	// Virtual filesystem rooted at "/":
	//   /lib        (dir)
	//   /lib/modules -> /usr/lib/modules (symlink)
	//   /usr        (dir)
	//   /usr/lib    (dir)
	//   /usr/lib/modules (dir)
	//   /a          (dir)
	//   /a/b        (dir)
	//   /a/target   (regular file)
	//   /link       -> /a/b (symlink)
	//   /etc        (dir)
	//   /etc/passwd (regular file)
	entries := map[string]fs.FileMode{
		"/":                fs.ModeDir | 0755,
		"/lib":             fs.ModeDir | 0755,
		"/lib/modules":     fs.ModeSymlink | 0777,
		"/usr":             fs.ModeDir | 0755,
		"/usr/lib":         fs.ModeDir | 0755,
		"/usr/lib/modules": fs.ModeDir | 0755,
		"/a":               fs.ModeDir | 0755,
		"/a/b":             fs.ModeDir | 0755,
		"/a/target":        0644,
		"/link":            fs.ModeSymlink | 0777,
		"/etc":             fs.ModeDir | 0755,
		"/etc/passwd":      0644,
	}
	links := map[string]string{
		"/lib/modules": "/usr/lib/modules",
		"/link":        "/a/b",
	}

	lstatFn := func(p string) (os.FileInfo, error) {
		m, ok := entries[p]
		if !ok {
			return nil, &os.PathError{Op: "lstat", Path: p, Err: syscall.ENOENT}
		}
		return fakeFileInfo{name: filepath.Base(p), mode: m}, nil
	}
	statFn := func(p string) (os.FileInfo, error) {
		if p == "/" {
			return fakeFileInfo{name: "/", mode: fs.ModeDir | 0755}, nil
		}
		// Simulate OS stat resolving /lib/modules and /link/../target.
		if p == "/lib/modules" {
			return fakeFileInfo{name: "modules", mode: fs.ModeDir | 0755}, nil
		}
		if p == "/link/../target" {
			return fakeFileInfo{name: "target", mode: 0644}, nil
		}
		return lstatFn(p)
	}
	readlinkFn := func(p string) (string, error) {
		t, ok := links[p]
		if !ok {
			return "", &os.PathError{Op: "readlink", Path: p, Err: syscall.EINVAL}
		}
		return t, nil
	}

	t.Run("ExpandsSymlinkUnderRootSlash", func(t *testing.T) {
		got, fi, err := ResolvePathInRootWith("/", "/lib/modules", lstatFn, statFn, readlinkFn)
		if err != nil {
			t.Fatalf("ResolvePathInRootWith(/, /lib/modules): %v", err)
		}
		if got != "/usr/lib/modules" {
			t.Errorf("resolved path = %q, want /usr/lib/modules", got)
		}
		if !fi.IsDir() {
			t.Errorf("expected directory FileInfo, got mode=%v", fi.Mode())
		}
	})

	t.Run("AppliesDotDotAfterSymlinkUnderRootSlash", func(t *testing.T) {
		got, fi, err := ResolvePathInRootWith("/", "/link/../target", lstatFn, statFn, readlinkFn)
		if err != nil {
			t.Fatalf("ResolvePathInRootWith(/, /link/../target): %v", err)
		}
		if got != "/a/target" {
			t.Errorf("resolved path = %q, want /a/target", got)
		}
		if fi.IsDir() {
			t.Errorf("expected regular file FileInfo, got dir")
		}
	})

	t.Run("RelativePathResolvesAgainstRootSlashNotCWD", func(t *testing.T) {
		got, _, err := ResolvePathInRootWith("/", "etc/passwd", lstatFn, statFn, readlinkFn)
		if err != nil {
			t.Fatalf("ResolvePathInRootWith(/, etc/passwd): %v", err)
		}
		if got != "/etc/passwd" {
			t.Errorf("resolved path = %q, want /etc/passwd", got)
		}
	})
}

func TestResolvePathInRoot_NonDirectoryRootRejectsDotAndDotDot(t *testing.T) {
	rootFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(rootFile, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/", "/.", "/..", ".", ".."} {
		t.Run(p, func(t *testing.T) {
			got, _, err := ResolvePathInRoot(rootFile, p)
			if err == nil {
				t.Fatalf("ResolvePathInRoot(%q, %q) succeeded returning %q on non-directory root", rootFile, p, got)
			}
			if !errors.Is(err, syscall.ENOTDIR) {
				t.Errorf("ResolvePathInRoot(%q, %q) err = %v, want ENOTDIR", rootFile, p, err)
			}
		})
	}
}
