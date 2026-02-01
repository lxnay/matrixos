package cleaners

import (
	"fmt"
	"matrixos/dev/janitor/config"
	"path"
	"time"
)

const (
	logsCutoffAge = 30 * 24 * time.Hour
)

type LogsCleaner struct {
	cfg config.IConfig
}

func (c *LogsCleaner) Name() string {
	return "logs"
}

func (c *LogsCleaner) Init(cfg config.IConfig) error {
	c.cfg = cfg
	return nil
}

func (c *LogsCleaner) isDryRun() (bool, error) {
	val, err := c.cfg.GetItem("LogsCleaner.DryRun")
	if err != nil {
		return false, err
	}
	return val.Item == "true", nil
}

func (c *LogsCleaner) getLogsDir() (string, error) {
	val, err := c.cfg.GetItem("matrixOS.LogsDir")
	if err != nil {
		return "", err
	}
	return val.Item, nil
}

func (c *LogsCleaner) Run() error {
	logsDir, err := c.getLogsDir()
	if err != nil {
		return err
	}

	dryRun, err := c.isDryRun()
	if err != nil {
		return err
	}

	fmt.Printf("Cleaning old logs from %s ...\n", logsDir)

	dirs := []string{
		path.Join(logsDir, "weekly-builder"),
	}
	for _, dir := range dirs {
		err := cleanDirectoryBasedOnMtime(dir, logsCutoffAge, dryRun)
		if err != nil {
			return err
		}
	}
	return nil
}
