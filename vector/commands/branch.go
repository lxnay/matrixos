package commands

import (
	"flag"
	"fmt"
	"matrixos/vector/lib/ostree"
	"slices"
	"strings"
)

// BranchCommand is a command for managing branches
type BranchCommand struct {
	BaseCommand
	UI
	fs   *flag.FlagSet
	sub  string
	args []string
}

// NewBranchCommand creates a new BranchCommand
func NewBranchCommand() *BranchCommand {
	return &BranchCommand{}
}

func (c *BranchCommand) Name() string {
	return "branch"
}

func (c *BranchCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	if err := c.initClientConfig(); err != nil {
		return err
	}

	if err := c.initOstree(); err != nil {
		return err
	}

	c.StartUI()
	c.SetupPlainPrinters()

	return nil
}

// parseArgs parses the command-line arguments without initializing config or ostree.
func (c *BranchCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("branch", flag.ContinueOnError)
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s <subcommand>\n", c.Name())
		var scs []string
		for k := range c.subcommands() {
			scs = append(scs, k)
		}
		slices.Sort(scs)
		fmt.Printf("Subcommands: %s\n", strings.Join(scs, ", "))
	}
	err := c.fs.Parse(args)
	if err != nil {
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

func (c *BranchCommand) printDeployment(dep *ostree.Deployment) {
	if dep.Booted {
		c.Println("Booted deployment:")
	} else {
		c.Println("Available deployment:")
	}
	c.Printf("  Name: %s\n", dep.Stateroot)
	c.Printf("  Index: %d\n", dep.Index)
	c.Printf("  Branch/Refspec: %s\n", dep.Refspec)
	c.Printf("  Checksum: %s\n", dep.Checksum)
	c.Printf("  Serial: %d\n", dep.Serial)
	c.Printf("  Pending: %t\n", dep.Pending)
	c.Printf("  Staged: %t\n", dep.Staged)
	c.Printf("  Rollback: %t\n", dep.Rollback)
}

func (c *BranchCommand) show() error {
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		c.Println("No deployments found.")
		return nil
	}

	// Sort to show the booted deployment first.
	var booted *ostree.Deployment
	for _, dep := range deployments {
		if dep.Booted {
			booted = &dep
			break
		}
	}
	if booted == nil {
		return fmt.Errorf("no booted deployment found")
	}

	c.printDeployment(booted)
	return nil
}

func (c *BranchCommand) deployment() error {
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		c.Println("No deployments found.")
		return nil
	}

	// Sort deployments considering:
	// booted deployment first, then everything else sorted by index.
	var booted *ostree.Deployment
	var others []*ostree.Deployment
	for _, dep := range deployments {
		if dep.Booted {
			booted = &dep
			continue
		}
		others = append(others, &dep)
	}

	slices.SortFunc(others, func(a, b *ostree.Deployment) int {
		return a.Index - b.Index
	})

	if booted != nil {
		c.printDeployment(booted)
	}

	for _, dep := range others {
		c.printDeployment(dep)
	}

	return nil
}

func (c *BranchCommand) pinOrUnpin(pin bool) error {
	if len(c.args) < 1 {
		if pin {
			return fmt.Errorf("pin command requires a deployment index")
		} else {
			return fmt.Errorf("unpin command requires a deployment index")
		}
	}

	var targetIndex int
	_, err := fmt.Sscanf(c.args[0], "%d", &targetIndex)
	if err != nil {
		return fmt.Errorf("invalid deployment index: %w", err)
	}

	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return fmt.Errorf("failed to list deployments: %w", err)
	}

	if len(deployments) == 0 {
		c.Println("No deployments found.")
		return nil
	}

	// Find the target deployment by index.
	var target *ostree.Deployment
	for _, dep := range deployments {
		if dep.Index == targetIndex {
			target = &dep
			break
		}
	}
	if target == nil {
		return fmt.Errorf("deployment with index %d not found", targetIndex)
	}

	// Perform the pin.
	if pin {
		if err := c.ot.Pin(target.Index); err != nil {
			return fmt.Errorf("failed to pin deployment: %w", err)
		}
	} else {
		if err := c.ot.Unpin(target.Index); err != nil {
			return fmt.Errorf("failed to unpin deployment: %w", err)
		}
	}

	return nil
}

func (c *BranchCommand) pin() error {
	return c.pinOrUnpin(true)
}

func (c *BranchCommand) unpin() error {
	return c.pinOrUnpin(false)
}

func (c *BranchCommand) remote() error {
	refs, err := c.ot.RemoteRefs()
	if err != nil {
		return fmt.Errorf("failed to list remote refs: %w", err)
	}
	for _, ref := range refs {
		c.Println(ref)
	}
	return nil
}

func (c *BranchCommand) local() error {
	refs, err := c.ot.LocalRefs()
	if err != nil {
		return fmt.Errorf("failed to list local refs: %w", err)
	}
	for _, ref := range refs {
		c.Println(ref)
	}
	return nil
}

func (c *BranchCommand) switch_() error {
	if len(c.args) < 1 {
		return fmt.Errorf("switch command requires a branch/ref name")
	}
	c.ot.SetRef(c.args[0])
	c.ot.SetVerbose(false) // ostree's own verbose flag, separate from ours.
	return c.ot.Switch()
}

func (c *BranchCommand) subcommands() map[string]func() error {
	scs := map[string]func() error{
		"show":       c.show,
		"deployment": c.deployment,
		"pin":        c.pin,
		"unpin":      c.unpin,
		"remote":     c.remote,
		"local":      c.local,
		"switch":     c.switch_,
	}
	return scs
}

func (c *BranchCommand) Run() error {
	defer c.FlushPrinters()

	scs := c.subcommands()
	cmd, ok := scs[c.sub]
	if !ok {
		return fmt.Errorf("unknown subcommand: %s", c.sub)
	}
	return cmd()
}
