package commands

import (
	"flag"
	"fmt"

	"matrixos/vector/lib/validation"
)

// environmentChecker abstracts the environment verification methods used by
// CheckCommand.  *validation.QA satisfies this interface.
type environmentChecker interface {
	VerifySeederEnvironmentSetup(imageDir string) error
	VerifyReleaserEnvironmentSetup(imageDir string) error
	VerifyImagerEnvironmentSetup(imageDir string) error
}

// CheckCommand verifies that the host has all the required binaries
// and directories to run the matrixOS build, release and image
// workflows.
type CheckCommand struct {
	BaseCommand
	UI
	fs *flag.FlagSet

	qa environmentChecker
}

// NewCheckCommand creates a new CheckCommand.
func NewCheckCommand() *CheckCommand {
	return &CheckCommand{
		fs: flag.NewFlagSet("check", flag.ContinueOnError),
	}
}

func (c *CheckCommand) Name() string {
	return c.fs.Name()
}

func (c *CheckCommand) Init(args []string) error {
	if err := c.initBaseConfig(); err != nil {
		return err
	}
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector dev %s\n", c.Name())
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}

	qa, err := validation.New(c.cfg)
	if err != nil {
		return fmt.Errorf("failed to initialize QA: %w", err)
	}
	c.qa = qa

	c.StartUI()

	return nil
}

func (c *CheckCommand) Run() error {
	c.SetupPrinters("check")
	defer c.FlushPrinters()

	c.Println("If the following lines contain errors, install the respective packages.")
	c.Println("")

	allOk := true
	checks := []struct {
		label string
		fn    func(string) error
	}{
		{"seeders support (to build root filesystem from gentoo stage3)", c.qa.VerifySeederEnvironmentSetup},
		{"releaser support (to release chroots to ostree)", c.qa.VerifyReleaserEnvironmentSetup},
		{"imager support (to create bootable images)", c.qa.VerifyImagerEnvironmentSetup},
	}

	for _, chk := range checks {
		c.Printf("%sChecking for %s:%s\n", c.cBold, chk.label, c.cReset)
		if err := chk.fn("/"); err != nil {
			c.PrintErrf("%s%s%s\n", c.cRed, err, c.cReset)
			allOk = false
		} else {
			c.Printf("  %s%sOK%s\n", c.cGreen, c.iconCheck, c.cReset)
		}
		c.Println("")
	}

	if allOk {
		c.Printf("%s%sAll environment checks passed.%s\n",
			c.cGreen, c.iconCheck, c.cReset)
	} else {
		return fmt.Errorf("one or more environment checks failed")
	}

	return nil
}
