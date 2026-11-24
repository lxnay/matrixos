package commands

import (
	"fmt"
	"os"
	"strings"
)

// CfgCommand provides shell-script-friendly access to configuration values.
// All data output goes to stdout; errors go to stderr. No fancy UI formatting.
type CfgCommand struct {
	BaseCommand
	subcommandRouter
}

// NewCfgCommand creates a new CfgCommand.
func NewCfgCommand() *CfgCommand {
	return &CfgCommand{}
}

// Name returns the command name.
func (c *CfgCommand) Name() string {
	return "cfg"
}

// Init parses flags and loads config.
func (c *CfgCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}
	return c.initBaseConfig()
}

// parseArgs parses the command-line arguments without initializing config.
func (c *CfgCommand) parseArgs(args []string) error {
	return c.parseSubcommandArgs(
		c.Name(), args, c.subcommands(), os.Stderr, "<subcommand> [args...]",
		"vector cfg get matrixOS.Root Ostree.Remote",
	)
}

func (c *CfgCommand) subcommands() map[string]func() error {
	return map[string]func() error{
		"get": c.runGet,
	}
}

// Run dispatches to the selected subcommand.
func (c *CfgCommand) Run() error {
	return c.dispatchSubcommand(c.subcommands())
}

// runGet retrieves config values for the given keys and prints them as key=value lines.
func (c *CfgCommand) runGet() error {
	if len(c.args) == 0 {
		return fmt.Errorf("get requires at least one config key")
	}

	for _, key := range c.args {
		// Try bool first; if that fails, fall back to string.
		boolVal, boolErr := c.cfg.GetBool(key)
		strVal, strErr := c.cfg.GetItem(key)

		if strErr != nil {
			fmt.Fprintf(os.Stderr, "error: %s: %v\n", key, strErr)
			continue
		}

		// Emit bool representation when the raw string is a recognised boolean literal.
		lower := strings.ToLower(strVal)
		if boolErr == nil && (lower == "true" || lower == "false") {
			if boolVal {
				fmt.Fprintf(os.Stdout, "%s=true\n", key)
			} else {
				fmt.Fprintf(os.Stdout, "%s=false\n", key)
			}
			continue
		}

		fmt.Fprintf(os.Stdout, "%s=%s\n", key, strVal)
	}
	return nil
}
