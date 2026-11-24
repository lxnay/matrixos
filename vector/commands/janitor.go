package commands

import (
	"flag"
	"fmt"
	"matrixos/vector/commands/cleaners"
)

// JanitorCommand is a command for cleaning up development toolkit artifacts
type JanitorCommand struct {
	BaseCommand
	UI
	fs *flag.FlagSet
}

// NewJanitorCommand creates a new JanitorCommand
func NewJanitorCommand() *JanitorCommand {
	return &JanitorCommand{
		fs: flag.NewFlagSet("janitor", flag.ContinueOnError),
	}
}

func (c *JanitorCommand) Name() string {
	return c.fs.Name()
}

func (c *JanitorCommand) Init(args []string) error {
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s\n", c.Name())
		c.fs.PrintDefaults()
	}
	return c.fs.Parse(args)
}

func (c *JanitorCommand) Run() error {
	c.StartUI()
	c.SetupPrinters(c.Name())
	defer c.FlushPrinters()

	if err := requireRoot(); err != nil {
		return err
	}

	// Load the matrixOS config.
	if err := c.initBaseConfig(); err != nil {
		return fmt.Errorf("error reading config: %w", err)
	}

	clnrs := []cleaners.ICleaner{
		&cleaners.SeedsCleaner{},
		&cleaners.ImagesCleaner{},
		&cleaners.DownloadsCleaner{},
		&cleaners.LogsCleaner{},
	}
	for _, cln := range clnrs {
		c.Printf("Initializing %s cleaner ...\n", cln.Name())
		if err := cln.Init(c.cfg, c.printer, c.errPrinter); err != nil {
			return fmt.Errorf("error initializing %s cleaner: %w", cln.Name(), err)
		}
	}

	var errors []error
	for _, cln := range clnrs {
		c.Printf("Starting cleaner: %s\n", cln.Name())
		if err := cln.Run(); err != nil {
			c.PrintErrf("Error executing cleaner %s: %v\n", cln.Name(), err)
			errors = append(errors, err)
		}
	}
	if len(errors) > 0 {
		return fmt.Errorf("encountered %d errors during janitor execution", len(errors))
	}
	return nil
}
