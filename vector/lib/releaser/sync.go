package releaser

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/filesystems"
)

// syncExcludedPaths returns the list of paths to exclude when syncing
// from a chroot to an image directory.
func (r *Releaser) syncExcludedPaths(dst string) ([]string, error) {
	seedersArtifactsDir, err := r.cfg.GetItem("Seeder.ChrootBuildArtifactsDir")
	if err != nil {
		return nil, err
	}
	preppersArtifactsDir, err := r.cfg.GetItem("Seeder.ChrootPreppersPhasesStateDir")
	if err != nil {
		return nil, err
	}

	return []string{
		filepath.Join(dst, "/tmp/*"),
		// There can be some device nodes that we do not want to copy over.
		filepath.Join(dst, seedersArtifactsDir),
		filepath.Join(dst, preppersArtifactsDir),
		filepath.Join(dst, "/var/spool/nullmailer/trigger"),
		filepath.Join(dst, "/var/cache/portage/*"),
		filepath.Join(dst, "/var/cache/distfiles/*"),
		filepath.Join(dst, "/var/cache/binpkgs/*"),
		filepath.Join(dst, "/var/tmp/portage") + "/", // for rsync.
	}, nil
}

// cpReflinkCopy copies src to dst using cp --reflink=auto.
func (r *Releaser) cpReflinkCopy(src, dst string) error {
	excludes, err := r.syncExcludedPaths(dst)
	if err != nil {
		return err
	}

	r.Print("Removing %s ...\n", dst)
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("failed to remove %s: %w", dst, err)
	}

	r.Print("Spawning cp --preserve=links --reflink=auto from %s to %s ...\n", src, dst)
	if err := filesystems.CopyFileReflink(src, dst); err != nil {
		return err
	}

	r.Print("Copy with --reflink=auto complete. Removing excluded paths:\n")
	for _, d := range excludes {
		if !strings.HasPrefix(d, dst) {
			return fmt.Errorf("path %s is outside of %s", d, dst)
		}
		r.Print("  %s\n", d)
	}

	cleanDst := filepath.Clean(dst)
	for _, d := range excludes {
		// Glob-safe removal (handles patterns like /tmp/*).
		matches, _ := filepath.Glob(d)
		for _, m := range matches {
			if filepath.Clean(m) == cleanDst {
				continue // never remove the destination root
			}
			os.RemoveAll(m)
		}
	}
	return nil
}

func (r *Releaser) SyncFilesystem() error {
	if err := checkChrootDir(r.chrootDir); err != nil {
		return err
	}
	if r.imageDir == "" {
		return fmt.Errorf("imageDir is empty")
	}
	if r.chrootDir == r.imageDir {
		return fmt.Errorf("chrootDir and imageDir are the same: %s", r.imageDir)
	}

	if err := os.MkdirAll(r.imageDir, 0755); err != nil {
		return fmt.Errorf("failed to create imageDir: %w", err)
	}
	if err := filesystems.CheckDirIsRoot(r.imageDir); err != nil {
		return err
	}
	if err := filesystems.CheckActiveMounts(r.imageDir); err != nil {
		return err
	}

	r.Print("Using cp --reflink=auto copy mode ...\n")
	if err := r.cpReflinkCopy(r.chrootDir, r.imageDir); err != nil {
		return err
	}

	opts := filesystems.CheckHardlinkPreservationOptions{
		Stdout: r.Stdout(),
		Stderr: r.Stderr(),
	}
	return filesystems.CheckHardlinkPreservation(r.chrootDir, r.imageDir, opts)
}
