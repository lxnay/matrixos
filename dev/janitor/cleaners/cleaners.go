package cleaners

import (
	"errors"
	"fmt"
	"matrixos/dev/janitor/config"
	"os"
	"path/filepath"
	"time"
)

// ICleaner defines the interface for a janitor cleaner
type ICleaner interface {
	Name() string
	Init(cfg config.IConfig) error
	Run() error
}

func deletePaths(paths []string) error {
	for _, path := range paths {
		fmt.Printf("Deleting: %s\n", path)
		err := os.Remove(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to delete %s: %v.\n", path, err)
			return err
		}
	}
	return nil
}

func cleanDirectoryBasedOnMtime(dir string, cutoffAge time.Duration, dryRun bool) error {
	// Here we are ok following symlinks, because the user could have just swapped
	// out a normal dir for a dir symlink.
	stat, err := os.Stat(dir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(os.Stderr, "Directory %s does not exist. Nothing to do.\n", dir)
		return nil
	}
	if !stat.IsDir() {
		fmt.Fprintf(os.Stderr, "Directory %s is not a directory.\n", dir)
		return os.ErrNotExist
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to read directory %s: %v\n", dir, err)
		return err
	}

	var candidates []string
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		lstat, err := os.Lstat(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to stat log %s: %v\n", path, err)
			continue
		}

		mode := lstat.Mode()
		isFile := mode.IsRegular()
		if !isFile {
			fmt.Fprintf(os.Stderr, "Path %s is not a regular file. Ignoring this file.\n", path)
			continue
		}

		mtime := lstat.ModTime()
		if time.Since(mtime) < cutoffAge {
			fmt.Fprintf(
				os.Stdout,
				"%s is newer than %v days. Skipping.\n",
				path,
				cutoffAge.Hours()/24,
			)
			continue
		}

		fmt.Fprintf(os.Stdout, "Found candidate file: %s\n", path)
		candidates = append(candidates, path)
	}

	if len(candidates) == 0 {
		fmt.Println("No files to remove.")
		return nil
	}

	for _, path := range candidates {
		fmt.Printf("Selected: %s\n", path)
	}

	if dryRun {
		fmt.Println("Dry run mode enabled. Not cleaning downloads.")
		return nil
	}

	return deletePaths(candidates)
}
