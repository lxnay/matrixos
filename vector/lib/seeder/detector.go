package seeder

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
)

// Compile-time interface check.
var _ ISeederDetector = (*SeederDetector)(nil)

// ISeederDetector defines the interface for detecting available seeders.
type ISeederDetector interface {
	// Detect scans the seeders directory and returns all valid seeders
	// after applying the skip and only filter functions.
	// skip returns true for seeders that should be skipped.
	// only returns true for seeders that are allowed (pass-through); if nil, all are allowed.
	Detect(skip, only SeederFilterFunc) ([]SeederInfo, error)

	// SetStderr replaces the writer used for filter-skip messages.
	SetStderr(io.Writer)

	// Stderr returns the current warning/error output writer.
	Stderr() io.Writer
}

// SeederDetector discovers available seeders by walking the seeders directory
// and applying caller-provided filters.
type SeederDetector struct {
	cfg    config.IConfig
	stderr io.Writer
}

// NewSeederDetector creates a new SeederDetector instance.
func NewSeederDetector(cfg config.IConfig) (*SeederDetector, error) {
	if cfg == nil {
		return nil, errors.New("missing config parameter")
	}
	return &SeederDetector{
		cfg:    cfg,
		stderr: os.Stderr,
	}, nil
}

// SetStderr replaces the writer used for filter-skip messages.
func (d *SeederDetector) SetStderr(w io.Writer) { d.stderr = w }

// Stderr returns the current warning/error output writer.
func (d *SeederDetector) Stderr() io.Writer { return d.stderr }

// configItem retrieves a non-empty config string or returns an error.
func (d *SeederDetector) configItem(key string) (string, error) {
	return config.GetRequiredItem(d.cfg, key)
}

// Detect scans the seeders directory and returns all valid seeders
// after applying the skip and only filter functions.
func (d *SeederDetector) Detect(skip, only SeederFilterFunc) ([]SeederInfo, error) {
	seedersDir, err := d.configItem("Seeder.SeedersDir")
	if err != nil {
		return nil, fmt.Errorf("failed to get seeders dir: %w", err)
	}
	if !filesystems.DirectoryExists(seedersDir) {
		return nil, fmt.Errorf("%s seeders dir is not a directory", seedersDir)
	}

	chrootSeedersDir, err := d.configItem("Seeder.ChrootSeedersDir")
	if err != nil {
		return nil, fmt.Errorf("failed to get chroot seeders dir: %w", err)
	}
	// do not test for the path inside the chroot, because it may not exist.

	disabledFile, err := d.configItem("Seeder.SeederDisabledFileName")
	if err != nil {
		return nil, fmt.Errorf("failed to get disabled seeder file name: %w", err)
	}
	chrootExecName, err := d.configItem("Seeder.ChrootExecutableName")
	if err != nil {
		return nil, fmt.Errorf("failed to get chroot exec name: %w", err)
	}
	prepperExecName, err := d.configItem("Seeder.PrepperExecutableName")
	if err != nil {
		return nil, fmt.Errorf("failed to get prepper exec name: %w", err)
	}
	postBuildExecName, err := d.configItem("Seeder.PostBuildExecutableName")
	if err != nil {
		return nil, fmt.Errorf("failed to get post-build exec name: %w", err)
	}

	entries, err := os.ReadDir(seedersDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read seeders dir %s: %w", seedersDir, err)
	}

	var seeders []SeederInfo
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		seederDir := filepath.Join(seedersDir, entry.Name())
		chrootSeederDir := filepath.Join(chrootSeedersDir, entry.Name())

		// Check for disabled marker file.
		disabled := filepath.Join(seederDir, disabledFile)
		if filesystems.PathExists(disabled) {
			fmt.Fprintf(d.stderr, "Skipping disabled seeder in: %s\n", seederDir)
			continue
		}

		seederExec := filepath.Join(seederDir, chrootExecName)
		prepperExec := filepath.Join(seederDir, prepperExecName)

		// The chroot exec must exist; skip directories without one.
		if !filesystems.PathExists(seederExec) {
			continue
		}

		seederName := SeederExecToName(seederExec)

		// Apply skip filter.
		if skip != nil && skip(seederName) {
			fmt.Fprintf(d.stderr, "Skipping seeder: %s as requested by flags.\n", seederName)
			continue
		}
		// Apply only filter.
		if only != nil && !only(seederName) {
			fmt.Fprintf(d.stderr, "Skipping seeder: %s not in list of seeders to execute.\n", seederName)
			continue
		}

		// Validate chroot exec is executable.
		if err := checkExecutable(seederExec); err != nil {
			return nil, fmt.Errorf("please chmod +x %s", seederExec)
		}

		// Prepper exec must exist and be executable.
		if !filesystems.PathExists(prepperExec) {
			return nil, fmt.Errorf("%s does not exist", prepperExec)
		}
		if err := checkExecutable(prepperExec); err != nil {
			return nil, fmt.Errorf("please chmod +x %s", prepperExec)
		}

		chrootSeederExec := filepath.Join(chrootSeederDir, chrootExecName)
		fmt.Fprintf(
			d.stderr,
			"Found seeder at: %s (inside chroot: %s)\n",
			seederExec,
			chrootSeederExec,
		)
		fmt.Fprintf(d.stderr, "Found prepper at: %s\n", prepperExec)

		info := SeederInfo{
			Name:             seederName,
			Dir:              seederDir,
			ChrootExec:       seederExec,
			ChrootChrootExec: chrootSeederExec,
			PrepperExec:      prepperExec,
		}

		// Post-build script is optional.
		postBuildExec := filepath.Join(seederDir, postBuildExecName)
		if filesystems.PathExists(postBuildExec) {
			if err := checkExecutable(postBuildExec); err != nil {
				return nil, fmt.Errorf("please chmod +x %s", postBuildExec)
			}
			info.PostBuildExec = postBuildExec
			info.PostBuildChrootExec = filepath.Join(chrootSeederDir, postBuildExecName)
			fmt.Fprintf(d.stderr, "Found post-build at: %s\n", postBuildExec)
		}

		seeders = append(seeders, info)
	}

	return seeders, nil
}

// SeederExecToName returns the seeder name (directory basename) for the given executable path.
func SeederExecToName(execPath string) string {
	return filepath.Base(filepath.Dir(execPath))
}

// SeederNameWithoutOrderPrefix strips the numeric order prefix (e.g. "00-bedrock" → "bedrock").
func SeederNameWithoutOrderPrefix(name string) string {
	if idx := strings.Index(name, "-"); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// checkExecutable verifies that path exists and has at least one execute permission bit set.
func checkExecutable(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("%s is not executable", path)
	}
	return nil
}
