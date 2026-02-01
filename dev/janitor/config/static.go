package config

import (
	"fmt"
	"os"
)

// StaticConfig is a placeholder config reader and getter for the matrixOS build
// toolkit. The placeholder will go away as soon as all config files are moved from
// bash env var files to INI/TOML config files.
type StaticConfig struct {
	cfg map[string][]string
}

func (c *StaticConfig) Load() error {
	// Get MATRIXOS_DEV_DIR, otherwise default to "/matrixos".
	// Like in env.include.sh.
	devDir := os.Getenv("MATRIXOS_DEV_DIR")
	if devDir == "" {
		devDir = "/matrixos"
	}

	logsDir := os.Getenv("MATRIXOS_LOGS_DIR")
	if logsDir == "" {
		logsDir = devDir + "/logs"
	}

	// Get MATRIXOS_OUT_DIR otherwise default to MATRIXOS_DEV_DIR + /out.
	// Like in env.include.sh.
	outDir := os.Getenv("MATRIXOS_OUT_DIR")
	if outDir == "" {
		outDir = devDir + "/out"
	}

	seederOutDIr := os.Getenv("MATRIXOS_SEEDER_OUT_DIR")
	if seederOutDIr == "" {
		seederOutDIr = outDir + "/seeder"
	}

	// Get MATRIXOS_IMAGES_OUT_DIR otherwise default to MATRIXOS_OUT_DIR + /images.
	// Like in imagerenv.include.sh.
	imagesDir := os.Getenv("MATRIXOS_IMAGES_OUT_DIR")
	if imagesDir == "" {
		imagesDir = outDir + "/images"
	}

	downloadsDir := os.Getenv("MATRIXOS_SEEDER_DOWNLOADS_DIR")
	if downloadsDir == "" {
		downloadsDir = seederOutDIr + "/downloads"
	}

	c.cfg = map[string][]string{
		"matrixOS.Root": {
			devDir,
		},
		"matrixOS.OutDir": {
			outDir,
		},
		"matrixOS.LogsDir": {
			logsDir,
		},
		"Imager.OutDir": {
			imagesDir,
		},
		"Seeder.OutDir": {
			seederOutDIr,
		},
		"Seeder.DownloadsDir": {
			downloadsDir,
		},
		"DownloadsCleaner.DryRun": {
			"false",
		},
		"LogsCleaner.DryRun": {
			"false",
		},
		"ImagesCleaner.DryRun": {
			"false",
		},
		// MinAmountOfImages defines the minimum amount of image files that we are ok keeping around for
		// each dated fileset.
		"ImagesCleaner.MinAmountOfImages": {
			"3",
		},
	}
	return nil
}

func (c *StaticConfig) GetItem(key string) (SingleConfigValue, error) {
	cfg := SingleConfigValue{}
	lst, ok := c.cfg[key]
	if !ok {
		return cfg, fmt.Errorf("invalid key %s", key)
	}
	cfg.Item = lst[0]
	return cfg, nil
}

func (c *StaticConfig) GetItems(key string) (MultipleConfigValues, error) {
	cfg := MultipleConfigValues{}
	lst, ok := c.cfg[key]
	if !ok {
		return cfg, fmt.Errorf("invalid key %s", key)
	}
	cfg.Items = lst
	return cfg, nil
}
