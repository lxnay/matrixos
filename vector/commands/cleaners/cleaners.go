package cleaners

import (
	"errors"
	"fmt"
	"io"
	"matrixos/vector/lib/config"
	"os"
	"path/filepath"
	"time"
)

// ICleaner defines the interface for a janitor cleaner.
type ICleaner interface {
	// Name returns the human-readable name of the cleaner.
	Name() string
	// Init initialises the cleaner with the given configuration.
	Init(cfg config.IConfig, stdout, stderr io.Writer) error
	// Run executes the cleaner's cleanup logic.
	Run() error
}

// baseCleaner holds the shared state and helpers embedded by all cleaners.
type baseCleaner struct {
	cfg    config.IConfig
	stdout io.Writer
	stderr io.Writer
}

func (c *baseCleaner) Init(cfg config.IConfig, stdout, stderr io.Writer) error {
	c.cfg = cfg
	c.stdout = stdout
	c.stderr = stderr
	return nil
}

func (c *baseCleaner) isDryRunKey(key string) (bool, error) {
	val, err := c.cfg.GetItem(key)
	if err != nil {
		return false, err
	}
	return val == "true", nil
}

func deletePaths(paths []string, stdout, stderr io.Writer) error {
	for _, path := range paths {
		fmt.Fprintf(stdout, "Deleting: %s\n", path)
		err := os.Remove(path)
		if err != nil {
			fmt.Fprintf(stderr, "Failed to delete %s: %v.\n", path, err)
			return err
		}
	}
	return nil
}

func cleanDirectoryBasedOnMtime(dir string, cutoffAge time.Duration, dryRun bool, stdout, stderr io.Writer) error {
	// Here we are ok following symlinks, because the user could have just swapped
	// out a normal dir for a dir symlink.
	stat, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			fmt.Fprintf(stderr, "Directory %s does not exist. Nothing to do.\n", dir)
			return nil
		}
		fmt.Fprintf(stderr, "Failed to stat directory %s: %v\n", dir, err)
		return err
	}
	if !stat.IsDir() {
		fmt.Fprintf(stderr, "Directory %s is not a directory.\n", dir)
		return os.ErrNotExist
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to read directory %s: %v\n", dir, err)
		return err
	}

	var candidates []string
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		lstat, err := os.Lstat(path)
		if err != nil {
			fmt.Fprintf(stderr, "Failed to stat log %s: %v\n", path, err)
			continue
		}

		mode := lstat.Mode()
		isFile := mode.IsRegular()
		if !isFile {
			fmt.Fprintf(stderr, "Path %s is not a regular file. Ignoring this file.\n", path)
			continue
		}

		mtime := lstat.ModTime()
		if time.Since(mtime) < cutoffAge {
			fmt.Fprintf(
				stdout,
				"%s is newer than %v days. Skipping.\n",
				path,
				cutoffAge.Hours()/24,
			)
			continue
		}

		fmt.Fprintf(stdout, "Found candidate file: %s\n", path)
		candidates = append(candidates, path)
	}

	if len(candidates) == 0 {
		fmt.Fprintln(stdout, "No files to remove.")
		return nil
	}

	for _, path := range candidates {
		fmt.Fprintf(stdout, "Selected: %s\n", path)
	}

	if dryRun {
		fmt.Fprintln(stdout, "Dry run mode enabled. Not cleaning downloads.")
		return nil
	}

	return deletePaths(candidates, stdout, stderr)
}
