package cleaners

import "time"

const (
	downloadsCutoffAge = 30 * 24 * time.Hour
)

type DownloadsCleaner struct {
	baseCleaner
}

func (c *DownloadsCleaner) Name() string {
	return "downloads"
}

func (c *DownloadsCleaner) isDryRun() (bool, error) {
	return c.isDryRunKey("DownloadsCleaner.DryRun")
}

func (c *DownloadsCleaner) getDownloadsDir() (string, error) {
	return c.cfg.GetItem("Seeder.DownloadsDir")
}

func (c *DownloadsCleaner) Run() error {
	downloadsDir, err := c.getDownloadsDir()
	if err != nil {
		return err
	}
	dryRun, err := c.isDryRun()
	if err != nil {
		return err
	}

	return cleanDirectoryBasedOnMtime(downloadsDir, downloadsCutoffAge, dryRun, c.stdout, c.stderr)
}
