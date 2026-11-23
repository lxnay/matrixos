package cleaners

import (
	"cmp"
	"fmt"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/seeder"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
)

var (
	ChrootDirNamePattern = regexp.MustCompile("([a-zA-Z0-9_]+)-([0-9]{8})")
)

func sortChrootDirs(dirs []string) []string {
	slices.SortStableFunc(dirs, func(a, b string) int {
		matchesA := ChrootDirNamePattern.FindStringSubmatch(filepath.Base(a))
		matchesB := ChrootDirNamePattern.FindStringSubmatch(filepath.Base(b))
		hasA := len(matchesA) >= 3
		hasB := len(matchesB) >= 3
		if !hasA && !hasB {
			return 0
		}
		if !hasA {
			return -1
		}
		if !hasB {
			return 1
		}
		verA, _ := strconv.Atoi(matchesA[2])
		verB, _ := strconv.Atoi(matchesB[2])
		return cmp.Compare(verA, verB)
	})
	return dirs
}

type SeedsCleaner struct {
	baseCleaner
}

func (c *SeedsCleaner) Name() string {
	return "seeds"
}

func (c *SeedsCleaner) isDryRun() (bool, error) {
	return c.isDryRunKey("SeedsCleaner.DryRun")
}

func (c *SeedsCleaner) MinAmountOfSeeds() (int, error) {
	val, err := c.cfg.GetItem("SeedsCleaner.MinAmountOfSeeds")
	if err != nil {
		return 0, err
	}
	amount, err := strconv.Atoi(val)
	if err != nil {
		return 0, err
	}
	if amount < 0 {
		return 0, fmt.Errorf("invalid SeedsCleaner.MinAmountOfSeeds %d: must be non-negative", amount)
	}
	return amount, nil
}

func (c *SeedsCleaner) Run() error {
	det, err := seeder.NewSeederDetector(c.cfg)
	if err != nil {
		return err
	}

	infos, err := det.Detect(nil, nil)
	if err != nil {
		return err
	}

	fmt.Fprintf(c.stdout, "Detected %d seeds:\n", len(infos))
	for _, info := range infos {
		fmt.Fprintf(c.stdout, " [%s] %s\n", info.Name, info.Dir)
	}

	opts := seeder.NewSeederOptions{}
	sd, err := seeder.NewSeeder(c.cfg, &opts)
	if err != nil {
		return err
	}
	defer sd.Cleanup()

	minAmountOfSeeds, err := c.MinAmountOfSeeds()
	if err != nil {
		return err
	}

	var markedForRemoval []string

	for _, info := range infos {
		name := seeder.SeederNameWithoutOrderPrefix(info.Name)
		fmt.Fprintf(c.stdout, "[%s] Working on seed %s ...\n", info.Name, name)

		// Parse seeder params.
		params, err := sd.ParseSeederParams(info)
		if err != nil {
			fmt.Fprintf(
				c.stderr,
				"[%s] Unable to parse params: %v",
				info.Name, err,
			)
			continue
		}

		if params.LatestAvailableChrootDir == "" {
			fmt.Fprintf(
				c.stderr,
				"[%s] A latest available chroot dir variable does not exist. Skipping ...",
				info.Name,
			)
			continue
		}

		var consideredDirs []string
		consideredDirs = append(consideredDirs, params.CompleteChrootDirs...)
		consideredDirs = append(consideredDirs, params.PartialChrootDirs...)
		// Deduplicate considered dirs.
		slices.Sort(consideredDirs)
		consideredDirs = slices.Compact(consideredDirs)

		if len(consideredDirs) == 0 {
			fmt.Fprintf(
				c.stderr,
				"[%s] No chroot dirs exist. Skipping ...",
				info.Name,
			)
			continue
		}

		for _, chrootDir := range consideredDirs {
			fmt.Fprintf(c.stdout, "[%s] Dir: %s\n", info.Name, chrootDir)
		}
		if len(consideredDirs) < minAmountOfSeeds {
			fmt.Fprintf(
				c.stdout,
				"[%s] Nothing to do. Within the minimum amount of seeds (%d).\n",
				info.Name,
				minAmountOfSeeds,
			)
			continue
		}

		sortedChrootDirs := sortChrootDirs(consideredDirs)
		for _, chrootDir := range sortedChrootDirs {
			fmt.Fprintf(c.stdout, "[%s|sorted] Dir: %s\n", info.Name, chrootDir)
		}
		// Pick the first N elements up to minAmountOfSeeds
		rmrf := sortedChrootDirs[:len(sortedChrootDirs)-minAmountOfSeeds]
		for _, chrootDir := range rmrf {
			fmt.Fprintf(c.stdout, "[%s|marked] Dir: %s\n", info.Name, chrootDir)
		}
		markedForRemoval = append(markedForRemoval, rmrf...)
	}

	if len(markedForRemoval) == 0 {
		fmt.Fprintln(c.stdout, "No seeds to remove.")
		return nil
	}

	dryRun, err := c.isDryRun()
	if err != nil {
		return err
	}

	var errors []error

	for _, path := range markedForRemoval {
		stat, err := os.Stat(path)
		if err != nil {
			errors = append(errors, err)
			fmt.Fprintf(c.stderr, "Failed to stat path %s: %v\n", path, err)
			continue
		}
		if stat.Mode().IsRegular() {
			if dryRun {
				fmt.Fprintf(c.stdout, "Dry run mode enabled. Not removing %s ...\n", path)
				continue
			}
			fmt.Fprintf(c.stderr, "Path %s is a regular file. Removing ...\n", path)
			if err := os.Remove(path); err != nil {
				errors = append(errors, err)
				fmt.Fprintf(c.stderr, "Failed to remove path %s: %v\n", path, err)
			}
			continue
		}

		if !stat.IsDir() {
			errors = append(errors, fmt.Errorf("path %s is not a directory", path))
			fmt.Fprintf(c.stderr, "Path %s is not a directory. Skipping.\n", path)
			continue
		}

		if !filesystems.DirectoryExists(path) {
			errors = append(errors, fmt.Errorf("directory %s does not exist", path))
			fmt.Fprintf(c.stderr, "Directory %s does not exist. Skipping.\n", path)
			continue
		}

		if err := filesystems.CheckDirIsRoot(path); err != nil {
			errors = append(errors, err)
			fmt.Fprintf(c.stderr, "%v\n", err)
			continue
		}

		if err := filesystems.CheckActiveMounts(path); err != nil {
			errors = append(errors, err)
			fmt.Fprintf(c.stderr, "Directory %s has active mounts. Skipping.\n", path)
			continue
		}

		if dryRun {
			fmt.Fprintf(c.stdout, "Dry run mode enabled. Not removing %s ...\n", path)
			continue
		}

		fmt.Fprintf(c.stdout, "Removing: %s\n", path)
		if err := os.RemoveAll(path); err != nil {
			errors = append(errors, err)
			fmt.Fprintf(
				c.stderr, "Failed to remove path %s: %v. Continuing ...\n",
				path,
				err,
			)
			continue
		}
	}

	if len(errors) > 0 {
		return fmt.Errorf("errors occurred during cleanup: %v", errors)
	}
	return nil
}
