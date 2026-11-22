package commands

import (
	"flag"
	"fmt"
	"strings"
)

// groupCommand is a reusable subcommand router embedded by uber-commands
// such as BuildCommand and DevCommand.
type groupCommand struct {
	fs          *flag.FlagSet
	subcommands map[string]func() ICommand
	sub         string
	args        []string
}

func newGroupCommand(name string, subcommands map[string]func() ICommand) groupCommand {
	return groupCommand{
		fs:          flag.NewFlagSet(name, flag.ExitOnError),
		subcommands: subcommands,
	}
}

func (c *groupCommand) Name() string {
	return c.fs.Name()
}

func (c *groupCommand) Init(args []string) error {
	var names []string
	for name := range c.subcommands {
		names = append(names, name)
	}
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s <subcommand>\n", c.fs.Name())
		fmt.Println("Subcommands: " + strings.Join(names, ", "))
		c.fs.PrintDefaults()
	}
	if err := c.fs.Parse(args); err != nil {
		return err
	}
	if c.fs.NArg() < 1 {
		c.fs.Usage()
		return fmt.Errorf("no subcommand provided")
	}
	c.sub = c.fs.Arg(0)
	c.args = c.fs.Args()[1:]
	return nil
}

func (c *groupCommand) Run() error {
	sf, ok := c.subcommands[c.sub]
	if !ok {
		return fmt.Errorf("unknown subcommand: %s", c.sub)
	}
	subcommand := sf()

	if err := subcommand.Init(c.args); err != nil {
		return fmt.Errorf("failed to initialize subcommand: %w", err)
	}
	if err := subcommand.Run(); err != nil {
		return fmt.Errorf("failed to run subcommand: %w", err)
	}

	return nil
}

// BuildCommand is an uber command for orchestrating build workflow and tools
// (image, seed, release, etc.).
type BuildCommand struct {
	groupCommand
}

// NewBuildCommand creates a new BuildCommand
func NewBuildCommand() *BuildCommand {
	return &BuildCommand{
		groupCommand: newGroupCommand("build", map[string]func() ICommand{
			"all":      func() ICommand { return NewAllCommand() },
			"image":    func() ICommand { return NewImageCommand() },
			"images":   func() ICommand { return NewImagesCommand() },
			"release":  func() ICommand { return NewReleaseCommand() },
			"releases": func() ICommand { return NewReleasesCommand() },
			"seeds":    func() ICommand { return NewSeedsCommand() },
		}),
	}
}
