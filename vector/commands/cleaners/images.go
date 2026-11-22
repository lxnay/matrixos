package cleaners

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const (

	// ImageFileNamePattern defines a regexp string to match for matrixOS image file names.
	ImageFileNamePattern = "(matrixos.*)-([0-9]{8}).img(.xz|.zstd|.gz|.bz2|.qcow2|)(.pubkey.asc|.asc|.sha256|.packages.txt|)"
)

type ImagesCleaner struct {
	baseCleaner
}

func (c *ImagesCleaner) Name() string {
	return "images"
}

func (c *ImagesCleaner) isDryRun() (bool, error) {
	return c.isDryRunKey("ImagesCleaner.DryRun")
}

func (c *ImagesCleaner) MinAmountOfImages() (int, error) {
	val, err := c.cfg.GetItem("ImagesCleaner.MinAmountOfImages")
	if err != nil {
		return 0, err
	}
	amount, err := strconv.Atoi(val)
	if err != nil {
		return 0, err
	}
	return amount, nil
}

func filterEntry(regex *regexp.Regexp, path string, entry os.DirEntry, stdout, stderr io.Writer) bool {
	stat, err := os.Lstat(path)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to stat image %s: %v\n", path, err)
		return false
	}

	// Only accept files.
	if stat.IsDir() {
		fmt.Fprintf(stdout, "Path %s is a directory. Skipping.\n", path)
		return false
	}

	mode := stat.Mode()
	isFile := mode.IsRegular()
	if !isFile {
		fmt.Fprintf(stdout, "Path %s is not a regular file. Ignoring this file.\n", path)
		return false
	}

	name := entry.Name()
	// Search for a %Y%m%d pattern in the file name. Also, file names have to then have an
	// .img.* pattern too and only some extensions are allowed. So, putting everything together:
	return regex.Match([]byte(name))
}

func buildBuckets(candidates []string, regex *regexp.Regexp, stdout, stderr io.Writer) map[string]map[string][]string {
	// This looks like:
	// "matrixos_amd64_dev_gnome" -> 20260125 -> [
	// 		.../matrixos_amd64_dev_gnome-20260125.img.xz,
	// 		.../matrixos_amd64_dev_gnome-20260125.img.xz.asc,
	//      ...,
	// ]
	buckets := make(map[string]map[string][]string)
	for _, path := range candidates {
		// extract prefix and date
		name := filepath.Base(path)
		matches := regex.FindStringSubmatch(name)

		// We expect at least 3 elements: [0]=full_match, [1]=prefix, [2]=date
		if len(matches) < 3 {
			fmt.Fprintf(stderr, "Cannot match %s. Skipping.\n", name)
			continue
		}

		prefix, date := matches[1], matches[2]
		fmt.Fprintf(stdout, "Found image: %s (Prefix: %s, Date: %s)\n", name, prefix, date)

		val, ok := buckets[prefix]
		if !ok {
			val = make(map[string][]string)
			buckets[prefix] = val
		}
		val[date] = append(val[date], path)
	}
	return buckets
}

func (c *ImagesCleaner) Run() error {
	val, err := c.cfg.GetItem("Imager.ImagesDir")
	if err != nil {
		return err
	}
	imgDir := val

	minAmountOfImages, err := c.MinAmountOfImages()
	if err != nil {
		return err
	}

	fmt.Fprintf(c.stdout, "Cleaning old images from %s ...\n", imgDir)

	regex, err := regexp.Compile(ImageFileNamePattern)
	if err != nil {
		fmt.Fprintf(c.stderr, "Unable to compile regex %s: %v\n", ImageFileNamePattern, err)
		return err
	}

	// Here we are ok following symlinks, because the user could have just swapped
	// out a normal dir for a dir symlink.
	stat, err := os.Stat(imgDir)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintf(c.stderr, "Images directory %s does not exist. Nothing to do.\n", imgDir)
		return nil
	}
	if !stat.IsDir() {
		fmt.Fprintf(c.stderr, "Images directory %s is not a directory.\n", imgDir)
		return os.ErrNotExist
	}

	entries, err := os.ReadDir(imgDir)
	if err != nil {
		fmt.Fprintf(c.stderr, "Failed to read images directory %s: %v\n", imgDir, err)
		return err
	}

	var candidates []string
	for _, entry := range entries {
		path := filepath.Join(imgDir, entry.Name())
		match := filterEntry(regex, path, entry, c.stdout, c.stderr)
		if !match {
			continue
		}
		fmt.Fprintf(c.stdout, "Found candidate image file: %s\n", path)
		candidates = append(candidates, path)
	}

	var pathsToRemove []string
	buckets := buildBuckets(candidates, regex, c.stdout, c.stderr)
	for prefix, datedData := range buckets {
		fmt.Fprintf(c.stdout, "Scanning prefix: %s\n", prefix)
		if len(datedData) < minAmountOfImages {
			fmt.Fprintf(c.stdout, "Nothing to do for prefix %s. Within the minimum amount of images.\n", prefix)
			continue
		}

		var dates []string
		for date := range datedData {
			dates = append(dates, date)
		}

		// Sort dates by newest to oldest!
		slices.SortFunc(dates, func(a, b string) int {
			iA, _ := strconv.Atoi(a)
			iB, _ := strconv.Atoi(b)
			return iB - iA
		})
		dates = dates[minAmountOfImages:]

		fmt.Fprintf(c.stdout, "Candidate dates for %s: %v\n", prefix, strings.Join(dates, ", "))
		for _, date := range dates {
			pathsToRemove = append(pathsToRemove, datedData[date]...)
		}
	}

	if len(pathsToRemove) == 0 {
		fmt.Fprintln(c.stdout, "No images to remove.")
		return nil
	}

	for _, path := range pathsToRemove {
		fmt.Fprintf(c.stdout, "Selected: %s\n", path)
	}

	dryRun, err := c.isDryRun()
	if err != nil {
		return err
	}

	if dryRun {
		fmt.Fprintln(c.stdout, "Dry run mode enabled. Not cleaning images.")
		return nil
	}

	return deletePaths(pathsToRemove, c.stdout, c.stderr)
}
