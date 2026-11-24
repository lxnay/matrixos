package commands

import (
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
)

// subcommandRouter is embedded by commands that dispatch to internal
// method-based subcommands (e.g. branch, kargs, cfg).
type subcommandRouter struct {
	fs   *flag.FlagSet
	sub  string
	args []string
}

func (r *subcommandRouter) parseSubcommandArgs(
	name string,
	args []string,
	scs map[string]func() error,
	usageOut io.Writer,
	argSpec string,
	examples ...string,
) error {
	r.fs = flag.NewFlagSet(name, flag.ContinueOnError)
	r.fs.Usage = func() {
		fmt.Fprintf(usageOut, "Usage: vector %s %s\n", name, argSpec)
		names := make([]string, 0, len(scs))
		for k := range scs {
			names = append(names, k)
		}
		slices.Sort(names)
		fmt.Fprintf(usageOut, "Subcommands: %s\n", strings.Join(names, ", "))
		if len(examples) > 0 {
			fmt.Fprintln(usageOut, "\nExamples:")
			for _, ex := range examples {
				fmt.Fprintf(usageOut, "  %s\n", ex)
			}
		}
	}
	if err := r.fs.Parse(args); err != nil {
		return err
	}
	if r.fs.NArg() < 1 {
		r.fs.Usage()
		return fmt.Errorf("no subcommand provided")
	}
	r.sub = r.fs.Arg(0)
	r.args = r.fs.Args()[1:]
	return nil
}

func (r *subcommandRouter) dispatchSubcommand(scs map[string]func() error) error {
	cmd, ok := scs[r.sub]
	if !ok {
		return fmt.Errorf("unknown subcommand: %s", r.sub)
	}
	return cmd()
}

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
