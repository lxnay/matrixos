package cleaners

import (
	"fmt"
	"matrixos/vector/lib/filesystems"
	"path"
	"time"
)

const (
	logsCutoffAge = 30 * 24 * time.Hour
)

type LogsCleaner struct {
	baseCleaner
}

func (c *LogsCleaner) Name() string {
	return "logs"
}

func (c *LogsCleaner) isDryRun() (bool, error) {
	return c.isDryRunKey("LogsCleaner.DryRun")
}

func (c *LogsCleaner) getLogsDir() (string, error) {
	val, err := c.cfg.GetItem("matrixOS.LogsDir")
	if err != nil {
		return "", err
	}
	if val == "" {
		return "", fmt.Errorf("matrixOS.LogsDir is not set")
	}
	return val, nil
}

func (c *LogsCleaner) Run() error {
	logsDir, err := c.getLogsDir()
	if err != nil {
		return err
	}
	if !filesystems.DirectoryExists(logsDir) {
		fmt.Fprintf(c.stderr, "Logs directory %s does not exist. Nothing to do.\n", logsDir)
		return nil
	}

	dryRun, err := c.isDryRun()
	if err != nil {
		return err
	}

	fmt.Fprintf(c.stdout, "Cleaning old logs from %s ...\n", logsDir)

	dirs := []string{
		path.Join(logsDir, "weekly-builder"),
	}
	for _, dir := range dirs {
		if err := cleanDirectoryBasedOnMtime(dir, logsCutoffAge, dryRun, c.stdout, c.stderr); err != nil {
			return err
		}
	}
	return nil
}
