package seeder

import (
	"fmt"
	"os"
	"strings"
	"time"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/runner"
)

func (s *Seeder) RetryableCmd(tries int, name string, args ...string) error {
	var lastErr error
	for attempt := 1; attempt <= tries; attempt++ {
		lastErr = s.runner(&runner.Cmd{
			Name:   name,
			Args:   args,
			Stdout: s.Stdout(),
			Stderr: s.Stderr(),
		})
		if lastErr == nil {
			return nil
		}
		if attempt < tries {
			s.PrintError("Attempt %d/%d failed! Retrying in 5 seconds...\n", attempt, tries)
			time.Sleep(5 * time.Second)
		}
	}
	return fmt.Errorf("command failed after %d attempts: %w", tries, lastErr)
}

func (s *Seeder) MaybeInitializePrivateRepo() error {
	repoPath, err := s.PrivateGitRepoPath()
	if err != nil {
		return fmt.Errorf("failed to get private git repo path: %w", err)
	}
	if repoPath == "" {
		return fmt.Errorf("matrixOS private repo path is not set")
	}

	gitURL, err := s.PrivateExampleGitRepo()
	if err != nil {
		return fmt.Errorf("failed to get private example git repo URL: %w", err)
	}

	if !filesystems.DirectoryExists(repoPath) || dirIsEmpty(repoPath) {
		if err := s.cloneRepo(repoPath, gitURL); err != nil {
			return err
		}
		return s.runMakeInDir(repoPath)
	}

	// Repo exists – validate it is a git repo.
	gitDir := repoPath + "/.git"
	if !filesystems.DirectoryExists(gitDir) {
		return fmt.Errorf("%s must be a git repo", repoPath)
	}

	// If not yet built, run make.sh.
	builtMarker := repoPath + "/.built"
	if !filesystems.PathExists(builtMarker) {
		s.PrintError("Updating %s ...\n", repoPath)
		if err := s.runMakeInDir(repoPath); err != nil {
			return err
		}
	}

	return nil
}

// cloneRepo clones a git repo into repoPath.
func (s *Seeder) cloneRepo(repoPath, gitURL string) error {
	s.PrintError("%s does not exist or is empty. Pulling it from: %s ...\n", repoPath, gitURL)

	if err := os.MkdirAll(repoPath, 0755); err != nil {
		return fmt.Errorf("failed to create directory %s: %w", repoPath, err)
	}

	cloneArgs, err := s.gitCloneArgs()
	if err != nil {
		return fmt.Errorf("failed to get git clone args: %w", err)
	}

	gitArgs := []string{"clone"}
	gitArgs = append(gitArgs, cloneArgs...)
	gitArgs = append(gitArgs, gitURL, repoPath)

	if err := s.runner(&runner.Cmd{
		Name:   "git",
		Args:   gitArgs,
		Stdout: s.Stdout(),
		Stderr: s.Stderr(),
	}); err != nil {
		return fmt.Errorf("git clone failed: %w", err)
	}

	return nil
}

// gitCloneArgs returns the configured git clone arguments as a parsed
// slice, splitting on whitespace.
func (s *Seeder) gitCloneArgs() ([]string, error) {
	raw, err := s.GitCloneArgs()
	if err != nil {
		return nil, err
	}
	return strings.Fields(raw), nil
}

// runMakeInDir runs ./make.sh inside the given directory.
func (s *Seeder) runMakeInDir(dir string) error {
	cmd := &runner.Cmd{Name: "./make.sh", Stdout: s.Stdout(), Stderr: s.Stderr()}
	cmd.Dir = dir
	return s.runner(cmd)
}

// dirIsEmpty returns true when dir exists but contains no entries.
func dirIsEmpty(dir string) bool {
	empty, err := filesystems.DirEmpty(dir)
	if err != nil {
		return true // treat unreadable dirs as empty
	}
	return empty
}
