package releaser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/pms"
)

func (r *Releaser) SymlinkEtc() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	r.Print("Symlinking /etc to prevent emerge packages recreating it ...\n")
	return os.Symlink("usr/etc", filepath.Join(r.imageDir, "etc"))
}

func (r *Releaser) UnlinkEtc() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	r.Print("Removing /etc symlink before ostree commit ...\n")
	return os.Remove(filepath.Join(r.imageDir, "etc"))
}

func (r *Releaser) AddExtraDotDotToUsrEtcPortage() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	r.Print("Fixing /usr/etc/portage symlink after move of /etc to /usr/etc ...\n")
	etcPortageDir := filepath.Join(r.imageDir, "usr/etc/portage")

	target, err := os.Readlink(etcPortageDir)
	if err != nil {
		return fmt.Errorf("failed to read symlink %s: %w", etcPortageDir, err)
	}

	if err := os.Remove(etcPortageDir); err != nil {
		return err
	}
	newTarget := "../" + target
	if err := os.Symlink(newTarget, etcPortageDir); err != nil {
		return err
	}

	r.Print("New /usr/etc/portage symlink: %s -> %s\n", etcPortageDir, newTarget)

	// Verify the symlink is not broken.
	if _, err := os.Stat(etcPortageDir); err != nil {
		return fmt.Errorf("symlink is broken: %s", etcPortageDir)
	}
	return nil
}

func (r *Releaser) RemoveExtraDotDotFromUsrEtcPortage() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	r.Print("Removing extra ../ from /usr/etc/portage so that it works after client deployment.\n")
	etcPortageDir := filepath.Join(r.imageDir, "usr/etc/portage")

	target, err := os.Readlink(etcPortageDir)
	if err != nil {
		return fmt.Errorf("failed to read symlink %s: %w", etcPortageDir, err)
	}

	if err := os.Remove(etcPortageDir); err != nil {
		return err
	}

	newTarget := strings.TrimPrefix(target, "../")
	if err := os.Symlink(newTarget, etcPortageDir); err != nil {
		return err
	}

	r.Print(
		"New /usr/etc/portage symlink status (might be broken): %s -> %s\n",
		etcPortageDir,
		newTarget,
	)
	return nil
}

func (r *Releaser) OstreePrepare() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	if err := r.ostree.PrepareFilesystemHierarchy(r.imageDir); err != nil {
		return err
	}
	return r.ostree.ValidateFilesystemHierarchy(r.imageDir)
}

var (
	ostreeRepoLocksMu sync.Mutex
	ostreeRepoLocks   = make(map[string]*sync.Mutex)
)

// lockOstreeRepo acquires an exclusive in-process lock for repoDir and returns
// its unlock function. This serializes ostree repository mutations (init,
// commit, prune, static-delta, summary) across parallel release workers.
func lockOstreeRepo(repoDir string) func() {
	key := filepath.Clean(repoDir)
	ostreeRepoLocksMu.Lock()
	mu, ok := ostreeRepoLocks[key]
	if !ok {
		mu = &sync.Mutex{}
		ostreeRepoLocks[key] = mu
	}
	ostreeRepoLocksMu.Unlock()

	mu.Lock()
	return mu.Unlock
}

func (r *Releaser) MaybeOstreeInit() error {
	repoDir, err := r.ostree.RepoDir()
	if err != nil {
		return err
	}

	unlock := lockOstreeRepo(repoDir)
	defer unlock()

	objectsDir := filepath.Join(repoDir, "objects")
	if filesystems.DirectoryExists(objectsDir) {
		r.Print("ostree repository %s already present.\n", repoDir)
		return nil
	}

	r.Print("Creating ostree repository %s ...\n", repoDir)
	if err := os.MkdirAll(repoDir, 0755); err != nil {
		return err
	}

	if err := r.ostree.InitRepo(); err != nil {
		return err
	}

	gpgEnabled, err := r.ostree.GpgEnabled()
	if err != nil {
		return err
	}

	return r.ostree.SetGpg(gpgEnabled)
}

func (r *Releaser) listPackages(imageDir string) ([]string, error) {
	roVdb, err := r.ReadOnlyVdb()
	if err != nil {
		return nil, err
	}
	rwVdb := ostree.RwVdbPath

	roPath := filepath.Join(imageDir, roVdb)
	rwPath := filepath.Join(imageDir, rwVdb)

	var vdb string
	if filesystems.DirectoryExists(roPath) {
		vdb = roPath
	} else if filesystems.DirectoryExists(rwPath) {
		vdb = rwPath
	}

	if vdb == "" {
		return nil, errors.New("no vdb directory found in image")
	}
	return pms.PackageList(vdb)
}

func (r *Releaser) Release(opts CommitOptions) error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	if opts.Branch == "" {
		return errors.New("missing branch in CommitOptions")
	}

	// Verify /etc does not exist (it should have been moved to /usr/etc).
	etcDir := filepath.Join(r.imageDir, "etc")
	if filesystems.PathExists(etcDir) {
		return fmt.Errorf("%s/etc exists; this is illegal and breaks clients", r.imageDir)
	}

	// Read build metadata.
	metadataFile, err := r.BuildMetadataFile()
	if err != nil {
		return err
	}
	metadata := "not available"
	metadataPath := filepath.Join(r.imageDir, metadataFile, "build")
	if filesystems.FileExists(metadataPath) {
		r.Print("Reading metadata file %s for release commit subject ...\n", metadataPath)
		data, err := os.ReadFile(metadataPath)
		if err == nil {
			metadata = string(data)
		}
	}

	osName, err := r.ostree.OsName()
	if err != nil {
		return err
	}

	subject := fmt.Sprintf("Automated release of %s for %s at %s",
		osName, opts.Branch, time.Now().Format("2006-01-02"))

	parentBranch := opts.ParentBranch
	if parentBranch == "" {
		parentBranch = "none"
	}

	fancyOsName, err := r.ostree.FancyOsName()
	if err != nil {
		return err
	}

	pkgList, err := r.listPackages(r.imageDir)
	if err != nil {
		return fmt.Errorf("failed to list packages for release commit body: %w", err)
	}

	body := fmt.Sprintf(
		"%s %s (parent: %s) at %s\n\nBuild metadata:\n%s\nPackages:\n%s\n",
		fancyOsName,
		opts.Branch,
		parentBranch,
		time.Now().Format("2006-01-02"),
		metadata,
		strings.Join(pkgList, "\n"),
	)

	// Normalise timestamps before commit.
	r.Print(
		"Normalizing files at %s before ostree commit to have same timestamp ...\n",
		r.imageDir,
	)

	timeZero := time.Unix(1, 0)
	if err := filesystems.NormalizeTimestamps(r.imageDir, timeZero); err != nil {
		return err
	}

	// Serialize ostree repository mutations (commit, prune, delta, summary)
	// across parallel release workers targeting the same repository.
	repoDir, err := r.ostree.RepoDir()
	if err != nil {
		return err
	}
	unlock := lockOstreeRepo(repoDir)
	defer unlock()

	// Build and run the ostree commit.
	gpgArgs, err := r.ostree.GpgArgs()
	if err != nil {
		return err
	}
	commitOpts := ostree.CommitOptions{
		Subject:  subject,
		Body:     body,
		Parent:   opts.ParentRev,
		GpgArgs:  gpgArgs,
		Consume:  opts.Consume,
		ImageDir: r.imageDir,
	}
	if err := r.ostree.Commit(commitOpts); err != nil {
		return fmt.Errorf("ostree commit failed: %w", err)
	}

	if err := r.ostree.Prune(); err != nil {
		return fmt.Errorf("ostree prune failed: %w", err)
	}

	genDeltas, err := r.GenerateStaticDeltas()
	if err != nil {
		return err
	}
	if genDeltas {
		if err := r.ostree.GenerateStaticDelta(); err != nil {
			return fmt.Errorf("ostree static delta generation failed: %w", err)
		}
	} else {
		r.Print("Skipping static delta generation as requested by flags.\n")
	}

	if err := r.ostree.UpdateSummary(); err != nil {
		return fmt.Errorf("ostree update summary failed: %w", err)
	}

	return nil
}
