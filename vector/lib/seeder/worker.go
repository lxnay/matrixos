package seeder

import (
	"bytes"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"text/template"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

var (
	funcMap = template.FuncMap{
		"shq": func(s string) string {
			if strings.ContainsAny(s, "\x00\n\r") {
				panic(fmt.Sprintf("shq: unsafe characters in %q", s))
			}
			return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
		},
	}

	paramsParser = template.Must(
		template.New("paramsParser").Funcs(funcMap).Parse(`
set -eu
export MATRIXOS_DEV_DIR={{shq .DevDir}}
source {{shq .ParamsPath}}
echo "${SEEDER_DEPENDS}"
echo "${SEEDER_CHROOT_NAME}"
echo "${SEEDER_CHROOTS_DIR}"
echo "${PREFERRED_SEEDER_CHROOT_DIR}"
echo $("{{.SeedName}}_params.find_latest_chroot_dir" {{shq .Name}} || true)
{{.SeedName}}_params.find_all_chroot_dirs {{shq .Name}} || echo ""
{{.SeedName}}_params.find_partial_chroot_dirs {{shq .Name}} || echo ""
`))
)

// SeederParams holds the key variables exported by a seeder's params.sh.
type SeederParams struct {
	Depends            []string // SEEDER_DEPENDS (space-separated list)
	ChrootName         string   // SEEDER_CHROOT_NAME
	ChrootsDir         string   // SEEDER_CHROOTS_DIR
	PreferredChrootDir string   // PREFERRED_SEEDER_CHROOT_DIR
	// Computed path to the latest available chroot directory for this seeder.
	// This points to the latest effectively available directory, which may
	// differ from PREFERRED_SEEDER_CHROOT_DIR if that directory is missing or not ready.
	LatestAvailableChrootDir string
	CompleteChrootDirs       []string
	PartialChrootDirs        []string
}

// PrepperOptions configures how the prepper script is executed.
type PrepperOptions struct {
	ChrootDir  string
	Resume     bool
	Stage3File string
}

// --- Done flag management ---

// SeederDoneFlagFile computes the done-flag file path for the given
// seeder name and chroot directory.
// Format: <chrootDir><stateDir>/<prefix>_<name>
func (s *Seeder) SeederDoneFlagFile(name, chrootDir string) (string, error) {
	stateDir, err := s.PhasesStateDir()
	if err != nil {
		return "", err
	}
	prefix, err := s.SeederDoneFlagFilePrefix()
	if err != nil {
		return "", err
	}
	base := filepath.Join(chrootDir, stateDir)
	return filepath.Join(base, prefix+"_"+name), nil
}

// IsSeederDone checks whether the done-flag file exists for the seeder.
func (s *Seeder) IsSeederDone(name, chrootDir string) (bool, error) {
	flagFile, err := s.SeederDoneFlagFile(name, chrootDir)
	if err != nil {
		return false, err
	}
	return filesystems.FileExists(flagFile), nil
}

// MarkSeederDone creates the done-flag file for the given seeder.
func (s *Seeder) MarkSeederDone(name, chrootDir string) error {
	flagFile, err := s.SeederDoneFlagFile(name, chrootDir)
	if err != nil {
		return err
	}
	flagDir := filepath.Dir(flagFile)
	if err := os.MkdirAll(flagDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", flagDir, err)
	}
	f, err := os.Create(flagFile)
	if err != nil {
		return fmt.Errorf("failed to touch %s: %w", flagFile, err)
	}
	return f.Close()
}

// --- Params parsing ---

func (s *Seeder) parseParamsVariables(name, paramsPath string) (*SeederParams, error) {
	devDir, err := s.DevDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get dev dir: %w", err)
	}

	var scriptBuf bytes.Buffer
	if err := paramsParser.Execute(&scriptBuf, map[string]string{
		"DevDir":     devDir,
		"ParamsPath": paramsPath,
		"SeedName":   SeederNameWithoutOrderPrefix(name),
		"Name":       name,
	}); err != nil {
		return nil, fmt.Errorf("failed to render params parser script: %w", err)
	}

	// Source params.sh and echo the required variables.
	script := scriptBuf.String()

	env := os.Environ()
	env, err = s.generateSharedEnvVars(env)
	if err != nil {
		return nil, err
	}

	var stdout bytes.Buffer
	cmd := &runner.Cmd{
		Name:   "bash",
		Args:   []string{"-c", script},
		Env:    env,
		Stdout: &stdout,
		Stderr: s.stderr,
	}
	if err := s.runner(cmd); err != nil {
		return nil, fmt.Errorf(
			"failed to source params %s: %w", paramsPath, err,
		)
	}

	// Split on newlines and drop the trailing empty element caused by
	// the final newline. Do not use TrimSpace on the whole output because
	// that would collapse an empty 4th line (missing latest chroot dir).
	lines := strings.Split(stdout.String(), "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) < 7 {
		return nil, fmt.Errorf(
			"expected 7 lines from params %s, got %d",
			paramsPath, len(lines),
		)
	}

	var depends []string
	for field := range strings.FieldsSeq(lines[0]) {
		if field != "" {
			depends = append(depends, field)
		}
	}

	completeChrootDirs := strings.Fields(lines[5])

	var partialChrootDirs []string
	for _, line := range lines[6:] {
		partialChrootDirs = append(partialChrootDirs, strings.Fields(line)...)
	}

	params := &SeederParams{
		Depends:                  depends,
		ChrootName:               strings.TrimSpace(lines[1]),
		ChrootsDir:               strings.TrimSpace(lines[2]),
		PreferredChrootDir:       strings.TrimSpace(lines[3]),
		LatestAvailableChrootDir: strings.TrimSpace(lines[4]),
		CompleteChrootDirs:       completeChrootDirs,
		PartialChrootDirs:        partialChrootDirs,
	}

	if params.ChrootName == "" {
		return nil, fmt.Errorf(
			"SEEDER_CHROOT_NAME is empty in %s", paramsPath,
		)
	}
	if params.ChrootsDir == "" {
		return nil, fmt.Errorf(
			"SEEDER_CHROOTS_DIR is empty in %s", paramsPath,
		)
	}
	if params.PreferredChrootDir == "" {
		return nil, fmt.Errorf(
			"PREFERRED_SEEDER_CHROOT_DIR is empty in %s", paramsPath,
		)
	}
	return params, nil
}

// ParseSeederParams resolves the params executable inside info.Dir,
// checks that it exists, and runs it in a bash subshell to extract the
// required seeder variables.
func (s *Seeder) ParseSeederParams(info SeederInfo) (*SeederParams, error) {
	paramsName, err := s.ParamsExecutableName()
	if err != nil {
		return nil, fmt.Errorf("[%s] failed to get params name: %w", info.Name, err)
	}
	paramsPath := filepath.Join(info.Dir, paramsName)
	if !filesystems.FileExists(paramsPath) {
		return nil, fmt.Errorf("[%s] params file not found: %s", info.Name, paramsPath)
	}
	return s.parseParamsVariables(info.Name, paramsPath)
}

// --- GPG key management ---

// ImportGentooGpgKeys patches the GPG homedir permissions and imports
// the Gentoo release engineering GPG keys.
func (s *Seeder) ImportGentooGpgKeys() error {
	homedir, err := s.GpgKeysDir()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(homedir, 0700); err != nil {
		return fmt.Errorf("mkdir GPG homedir %s: %w", homedir, err)
	}
	if err := os.Chmod(homedir, 0700); err != nil {
		return fmt.Errorf("chmod GPG homedir: %w", err)
	}

	// Fix permissions on existing files inside the GPG homedir.
	_ = filepath.WalkDir(homedir,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			return os.Chmod(path, 0600)
		},
	)

	s.Print("Importing Gentoo GPG keys (--homedir=%s) ...\n", homedir)
	return s.runner(&runner.Cmd{
		Name: "gpg",
		Args: []string{
			"--homedir=" + homedir,
			"--batch", "--yes",
			"--auto-key-locate=clear,nodefault,wkd",
			"--locate-key",
			"releng@gentoo.org",
		},
		Stdout: s.stdout,
		Stderr: s.stderr,
	})
}

// KillGpgDaemons kills any gpg-agent, dirmngr, and scdaemon processes
// for the seeder GPG keys homedir.
func (s *Seeder) KillGpgDaemons() {
	homedir, err := s.GpgKeysDir()
	if err != nil {
		return
	}
	s.Print("Killing GPG daemons for %s ...\n", homedir)
	ostree.KillGpgDaemons(s.runner, homedir, s.stdout, s.stderr)
}

// generateSharedEnvVars generates the environment variables that need to be shared
// between the seeder and prepper scripts. Those variables must be valid both inside
// and outside the chroot, meaning that either one of these conditions holds:
//   - The variable is set to the same value both inside and outside the chroot, value is
//     valid in both cases, for example if used by params.sh.
//   - For inside-of-chroot paths, values must be used only via chroot.sh.
//   - For outside-of-chroot paths, values must be used only via prepper.sh and not passed
//     into the chroot.
func (s *Seeder) generateSharedEnvVars(env []string) ([]string, error) {
	seederPhasesStateDir, err := s.PhasesStateDir()
	if err != nil {
		return nil, err
	}

	seederDoneFlagFilePrefix, err := s.SeederDoneFlagFilePrefix()
	if err != nil {
		return nil, err
	}

	seedsVersioningCadence, err := s.SeedsVersioningCadence()
	if err != nil {
		return nil, err
	}

	defaultDevDir, err := s.DefaultDevDir()
	if err != nil {
		return nil, err
	}

	overlayGitRepo, err := s.configItem("matrixOS.OverlayGitRepo")
	if err != nil {
		return nil, err
	}

	defaultGitRepoPath, err := s.DefaultPrivateGitRepoPath()
	if err != nil {
		return nil, err
	}

	env = config.FilterEnvKey(env, "DEFAULT_MATRIXOS_DEV_DIR")
	env = config.FilterEnvKey(env, "SEEDERS_PHASES_STATE_DIR")
	env = config.FilterEnvKey(env, "SEEDER_DATE_CADENCE")
	env = config.FilterEnvKey(env, "SEEDER_OVERLAY_GIT_REPO")
	env = config.FilterEnvKey(env, "DEFAULT_PRIVATE_GIT_REPO_PATH")
	env = append(env,
		"SEEDERS_PHASES_STATE_DIR="+seederPhasesStateDir,
		"SEEDER_DONE_FLAG_FILE_PREFIX="+seederDoneFlagFilePrefix,
		"SEEDER_DATE_CADENCE="+seedsVersioningCadence,
		"DEFAULT_MATRIXOS_DEV_DIR="+defaultDevDir,
		"SEEDER_OVERLAY_GIT_REPO="+overlayGitRepo,
		"DEFAULT_PRIVATE_GIT_REPO_PATH="+defaultGitRepoPath,
	)
	return env, nil
}

// generatePrepperEnvVars generates the environment variables that are
// needed by the prepper script.
func (s *Seeder) generatePrepperEnvVars(env []string, params *SeederParams, opts *PrepperOptions) ([]string, error) {
	downloadsDir, err := s.DownloadsDir()
	if err != nil {
		return nil, err
	}

	stage3URL, err := s.Stage3DownloadUrl()
	if err != nil {
		return nil, err
	}

	lockDir, err := s.LockDir()
	if err != nil {
		return nil, err
	}

	lockWaitSeconds, err := s.LockWaitSeconds()
	if err != nil {
		return nil, err
	}

	preppersPhasesStateDir, err := s.PreppersPhasesStateDir()
	if err != nil {
		return nil, err
	}

	metadataFile, err := s.BuildMetadataFile()
	if err != nil {
		return nil, err
	}

	var resume string
	if opts.Resume {
		resume = "1"
	}

	gpgKeysDir, err := s.GpgKeysDir()
	if err != nil {
		return nil, err
	}

	env = config.FilterEnvKey(env, "CHROOT_DIR")
	env = config.FilterEnvKey(env, "CHROOT_RESUME")
	env = config.FilterEnvKey(env, "DOWNLOAD_DIR")
	env = config.FilterEnvKey(env, "STAGE3_URL")
	env = config.FilterEnvKey(env, "STAGE3_FILE")
	env = config.FilterEnvKey(env, "SEEDER_LOCK_DIR")
	env = config.FilterEnvKey(env, "SEEDER_LOCK_WAIT_SECS")
	env = config.FilterEnvKey(env, "SEEDER_CHROOT_NAME")
	env = config.FilterEnvKey(env, "SEEDER_CHROOTS_DIR")
	env = config.FilterEnvKey(env, "SEEDER_BUILD_METADATA_FILE")
	env = config.FilterEnvKey(env, "PREFERRED_SEEDER_CHROOT_DIR")
	env = config.FilterEnvKey(env, "PREPPERS_PHASES_STATE_DIR")
	env = config.FilterEnvKey(env, "SEEDER_GPG_KEYS_DIR")
	env = append(env,
		"CHROOT_DIR="+opts.ChrootDir,
		"CHROOT_RESUME="+resume,
		"DOWNLOAD_DIR="+downloadsDir,
		"STAGE3_URL="+stage3URL,
		"STAGE3_FILE="+opts.Stage3File,
		"SEEDER_LOCK_DIR="+lockDir,
		"SEEDER_LOCK_WAIT_SECS="+lockWaitSeconds,
		"SEEDER_CHROOT_NAME="+params.ChrootName,
		"SEEDER_CHROOTS_DIR="+params.ChrootsDir,
		"SEEDER_BUILD_METADATA_FILE="+metadataFile,
		"PREFERRED_SEEDER_CHROOT_DIR="+params.PreferredChrootDir,
		"PREPPERS_PHASES_STATE_DIR="+preppersPhasesStateDir,
		"SEEDER_GPG_KEYS_DIR="+gpgKeysDir,
	)

	return env, nil
}

// --- Prepper execution ---

// ExecutePrepper runs the prepper script with the required env vars.
func (s *Seeder) ExecutePrepper(info SeederInfo, params *SeederParams, opts *PrepperOptions) error {
	devDir, err := s.DevDir()
	if err != nil {
		return err
	}

	env := os.Environ()
	env, err = s.generateSharedEnvVars(env)
	if err != nil {
		return err
	}
	env, err = s.generatePrepperEnvVars(env, params, opts)
	if err != nil {
		return err
	}

	env = config.FilterEnvKey(env, "MATRIXOS_DEV_DIR")
	env = append(env,
		"MATRIXOS_DEV_DIR="+devDir,
	)

	cmd := &runner.Cmd{
		Name:   info.PrepperExec,
		Env:    env,
		Stdout: s.stdout,
		Stderr: s.stderr,
	}
	return s.runner(cmd)
}

// --- Chroot DNS ---

// SetupChrootDNS copies /etc/resolv.conf into the chroot.
func (s *Seeder) SetupChrootDNS(chrootDir string) error {
	src := "/etc/resolv.conf"
	dst := filepath.Join(chrootDir, "etc", "resolv.conf")

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(dst), err)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", src, err)
	}

	s.Print("Copying %s to %s\n", src, dst)
	return os.WriteFile(dst, data, 0644)
}

// --- Chroot directory setup ---

// setupDevDir sets up the dev toolkit into the chroot.
func (s *Seeder) setupDevDir(chrootDevDir string) error {
	localClone, err := s.UseLocalGitRepoInsideChroot()
	if err != nil {
		return err
	}
	cloneArgs, err := s.gitCloneArgs()
	if err != nil {
		return fmt.Errorf("error getting git clone args: %w", err)
	}

	if localClone {
		devDir, err := s.DevDir()
		if err != nil {
			return fmt.Errorf("error getting dev dir: %w", err)
		}
		s.Print(
			"Cloning %s (local) into %s ...\n",
			devDir, chrootDevDir,
		)
		args := append([]string{"clone"}, cloneArgs...)
		args = append(args, devDir, chrootDevDir)
		if err := s.runner(&runner.Cmd{
			Name:   "git",
			Args:   args,
			Stdout: s.stdout,
			Stderr: s.stderr,
		}); err != nil {
			return fmt.Errorf("error cloning git repo (local): %w", err)
		}
	} else {
		gitRepo, err := s.GitRepo()
		if err != nil {
			return fmt.Errorf("error getting git repo: %w", err)
		}
		s.Print(
			"Cloning %s (remote) into %s ...\n",
			gitRepo, chrootDevDir,
		)
		args := append([]string{"clone"}, cloneArgs...)
		args = append(args, gitRepo, chrootDevDir)
		if err := s.RetryableCmd(
			6, "git", args...,
		); err != nil {
			return fmt.Errorf("error cloning git repo (remote): %w", err)
		}
	}

	return nil
}

// cleanDevDirGitDir deletes the .git directory from the dev toolkit in the chroot
// if it exists and the seeder is configured to do so.
func (s *Seeder) cleanDevDirGitDir(chrootDevDir string) error {
	// Maybe delete .git directory.
	deleteDotGit, err := s.DeleteDotGitFromGitRepo()
	if err != nil {
		return fmt.Errorf("error checking if .git should be deleted: %w", err)
	}
	if !deleteDotGit {
		return nil
	}

	dotGitDir := filepath.Join(chrootDevDir, ".git")
	if !filesystems.DirectoryExists(dotGitDir) {
		return nil
	}

	s.Print("Deleting %s ...\n", dotGitDir)
	if err := os.RemoveAll(dotGitDir); err != nil {
		return fmt.Errorf("error deleting %s: %w", dotGitDir, err)
	}

	return nil
}

// SetupChrootDirs creates the seeder phase directories inside the chroot
// and clones the dev toolkit if it does not already exist.
func (s *Seeder) SetupChrootDirs(chrootDir string) error {
	// Create phases state dir.
	stateDir, err := s.PhasesStateDir()
	if err != nil {
		return err
	}
	phasesDir := filepath.Join(chrootDir, stateDir)
	if err := os.MkdirAll(phasesDir, 0755); err != nil {
		return fmt.Errorf("error creating phases dir %s: %w", phasesDir, err)
	}

	// Clone the dev toolkit into the chroot.
	defaultDevDir, err := s.DefaultDevDir()
	if err != nil {
		return err
	}
	chrootDevDir := filepath.Join(chrootDir, defaultDevDir)

	if !filesystems.DirectoryExists(chrootDevDir) {
		if err := s.setupDevDir(chrootDevDir); err != nil {
			return fmt.Errorf("error setting up dev dir: %w", err)
		}
	}

	// Maybe delete .git directory.
	if err := s.cleanDevDirGitDir(chrootDevDir); err != nil {
		return fmt.Errorf("error cleaning dev dir .git: %w", err)
	}

	return nil
}

func (s *Seeder) generateSeederEnvVars(env []string) ([]string, error) {
	defaultDevDir, err := s.DefaultDevDir()
	if err != nil {
		return nil, fmt.Errorf("error getting default dev dir: %w", err)
	}

	privatePath, err := s.PrivateGitRepoPath()
	if err != nil {
		return nil, fmt.Errorf("error getting private repo path: %w", err)
	}

	distDir, err := s.DistfilesDir()
	if err != nil {
		return nil, fmt.Errorf("error getting distfiles dir: %w", err)
	}

	binpkgsDir, err := s.BinpkgsDir()
	if err != nil {
		return nil, fmt.Errorf("error getting binpkgs dir: %w", err)
	}

	env = config.FilterEnvKey(env, "MATRIXOS_DEV_DIR")
	env = config.FilterEnvKey(env, "SEEDER_PRIVATE_GIT_REPO_PATH")
	env = config.FilterEnvKey(env, "SEEDER_DISTFILES_DIR")
	env = config.FilterEnvKey(env, "SEEDER_BINPKGS_DIR")
	env = config.FilterEnvKey(env, "RUNNER_TYPE")
	env = append(env,
		// Inside chroots, we always want /matrixos.
		"MATRIXOS_DEV_DIR="+defaultDevDir,
		// These 3 env vars are path outside of chroot, that can be
		// used to bind mount them inside the chroot. These variables
		// are not meant to be used within the chroot.sh, but by the
		// intermediate pre-chroot() init script.
		"SEEDER_PRIVATE_GIT_REPO_PATH="+privatePath,
		"SEEDER_DISTFILES_DIR="+distDir,
		"SEEDER_BINPKGS_DIR="+binpkgsDir,
		"RUNNER_TYPE=seeder",
	)
	return env, nil
}

// --- Chroot execution ---

// runInChroot executes execName (with optional args) inside opts.ChrootDir
// with the standard seeder environment and init.sh wrapper.
func (s *Seeder) runInChroot(opts *SeedOptions, execName string, args []string) error {
	// Start with a pristine environment.
	env, err := s.generateSharedEnvVars(opts.Env)
	if err != nil {
		return err
	}
	env, err = s.generateSeederEnvVars(env)
	if err != nil {
		return err
	}

	devDir, err := s.DevDir()
	if err != nil {
		return err
	}

	initScript := filepath.Join(devDir, "build", "init", "init.sh")

	stdin := opts.Stdin
	if stdin == nil {
		stdin = s.stdin
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = s.stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = s.stderr
	}
	dir := opts.Dir
	if dir == "" {
		dir = "/"
	}

	return s.chrootRunner(&runner.ChrootCmd{
		Cmd: runner.Cmd{
			Name:        execName,
			Args:        args,
			Env:         env,
			Dir:         dir,
			Stdin:       stdin,
			Stdout:      stdout,
			Stderr:      stderr,
			SysProcAttr: opts.SysProcAttr,
		},
		ChrootExec: initScript,
		ChrootDir:  opts.ChrootDir,
	})
}

// Seed runs the seeder's chroot script inside the chroot
// using unshare for namespace isolation.
func (s *Seeder) Seed(opts *SeedOptions) error {
	if opts == nil {
		return fmt.Errorf("opts cannot be nil")
	}
	return s.runInChroot(opts, opts.Info.ChrootChrootExec, opts.Info.ChrootChrootArgs)
}

// PostBuild runs the post-build script inside the chroot.
// It follows the same pattern as Seed but uses the PostBuildChrootExec path.
func (s *Seeder) PostBuild(opts *SeedOptions) error {
	if opts == nil {
		return fmt.Errorf("opts cannot be nil")
	}
	if opts.Info.PostBuildChrootExec == "" {
		return nil // No post-build script configured.
	}
	return s.runInChroot(opts, opts.Info.PostBuildChrootExec, nil)
}
