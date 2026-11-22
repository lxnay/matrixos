package commands

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const blsEntriesDir = "/boot/loader/entries"

// kargsRunner abstracts OS-level operations so they can be replaced in tests.
type kargsRunner struct {
	// readFile reads a file's content.
	readFile func(path string) ([]byte, error)
	// writeFile writes data to a file with the given permissions.
	writeFile func(path string, data []byte, perm os.FileMode) error
	// glob returns paths matching a pattern.
	glob func(pattern string) ([]string, error)
	// getEuid returns the effective user ID.
	getEuid func() int
}

func defaultKargsRunner() *kargsRunner {
	return &kargsRunner{
		readFile:  os.ReadFile,
		writeFile: os.WriteFile,
		glob:      filepath.Glob,
		getEuid:   getEuid,
	}
}

// KargsCommand manages kernel boot arguments in BLS (Boot Loader Specification) configs.
type KargsCommand struct {
	BaseCommand
	UI
	subcommandRouter
	run *kargsRunner
}

// NewKargsCommand creates a new KargsCommand.
func NewKargsCommand() *KargsCommand {
	return &KargsCommand{}
}

// Name returns the command name.
func (c *KargsCommand) Name() string {
	return "kargs"
}

// Init parses flags and prepares the UI.
func (c *KargsCommand) Init(args []string) error {
	if err := c.parseArgs(args); err != nil {
		return err
	}

	c.StartUI()
	c.SetupPrinters(c.Name())
	c.run = defaultKargsRunner()

	return nil
}

// parseArgs parses the command-line arguments without initializing config.
func (c *KargsCommand) parseArgs(args []string) error {
	return c.parseSubcommandArgs(
		c.Name(), args, c.subcommands(), os.Stdout, "<subcommand> <karg> [<karg>...]",
		"vector kargs add quiet splash",
		"vector kargs rm quiet splash",
	)
}

func (c *KargsCommand) subcommands() map[string]func() error {
	return map[string]func() error{
		"add": c.add,
		"rm":  c.rm,
	}
}

// Run dispatches to the appropriate subcommand.
func (c *KargsCommand) Run() error {
	defer c.FlushPrinters()
	return c.dispatchSubcommand(c.subcommands())
}

func (c *KargsCommand) updateEntries(op string, fn func(string, []string) error) error {
	if len(c.args) == 0 {
		return fmt.Errorf("%s requires at least one kernel argument", op)
	}

	if err := requireRootFn(c.run.getEuid); err != nil {
		return err
	}

	entries, err := c.listBLSEntries()
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if err := fn(entry, c.args); err != nil {
			return fmt.Errorf("failed to update %s: %w", filepath.Base(entry), err)
		}
	}

	return c.printAllEntries(entries)
}

// add appends kernel arguments to all BLS configs (if not already present).
func (c *KargsCommand) add() error {
	return c.updateEntries("add", c.addKargsToEntry)
}

// rm removes kernel arguments from all BLS configs.
func (c *KargsCommand) rm() error {
	return c.updateEntries("rm", c.rmKargsFromEntry)
}

// listBLSEntries returns all .conf files in the BLS entries directory.
func (c *KargsCommand) listBLSEntries() ([]string, error) {
	return listBLSEntryPaths(c.run.glob)
}

// parseBLSEntry reads a BLS config file and returns its lines.
func (c *KargsCommand) parseBLSEntry(path string) ([]string, error) {
	data, err := c.run.readFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	return strings.Split(string(data), "\n"), nil
}

// writeBLSEntry writes lines back to a BLS config file.
func (c *KargsCommand) writeBLSEntry(path string, lines []string) error {
	data := strings.Join(lines, "\n")
	if err := c.run.writeFile(path, []byte(data), 0644); err != nil {
		return fmt.Errorf("failed to write %s: %w", path, err)
	}
	return nil
}

// getOptionsLine returns the index and content of the "options" line in BLS lines.
// Returns -1 and empty string if not found.
func getOptionsLine(lines []string) (int, string) {
	for i, line := range lines {
		if strings.HasPrefix(line, "options ") {
			return i, strings.TrimPrefix(line, "options ")
		}
	}
	return -1, ""
}

// addKargsToBLSFile reads a BLS config file, appends kernel arguments that
// are not already present on the options line, and writes it back.
// Returns the list of kargs that were actually added (empty if all were
// already present, in which case the file is not rewritten).
// This is a shared helper used by both KargsCommand and SetupOSCommand.
func addKargsToBLSFile(
	path string,
	kargs []string,
	readFile func(string) ([]byte, error),
	writeFile func(string, []byte, os.FileMode) error,
) ([]string, error) {
	data, err := readFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read %s: %w", path, err)
	}
	lines := strings.Split(string(data), "\n")

	idx, options := getOptionsLine(lines)
	if idx < 0 {
		return nil, fmt.Errorf("no 'options' line found in %s", filepath.Base(path))
	}

	existingArgs := strings.Fields(options)
	existingSet := make(map[string]bool, len(existingArgs))
	for _, arg := range existingArgs {
		existingSet[arg] = true
	}

	var added []string
	for _, karg := range kargs {
		if !existingSet[karg] {
			added = append(added, karg)
			existingSet[karg] = true
		}
	}

	if len(added) > 0 {
		newOptions := options + " " + strings.Join(added, " ")
		lines[idx] = "options " + newOptions
		output := strings.Join(lines, "\n")
		if err := writeFile(path, []byte(output), 0644); err != nil {
			return nil, fmt.Errorf("failed to write %s: %w", path, err)
		}
	}

	return added, nil
}

// listBLSEntryPaths returns sorted .conf files from the BLS entries directory.
// This is a shared helper used by both KargsCommand and SetupOSCommand.
func listBLSEntryPaths(glob func(string) ([]string, error)) ([]string, error) {
	pattern := filepath.Join(blsEntriesDir, "*.conf")
	entries, err := glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("failed to list BLS entries in %s: %w", blsEntriesDir, err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("no BLS entries found in %s", blsEntriesDir)
	}
	slices.Sort(entries)
	return entries, nil
}

// addKargsToEntry appends kernel arguments to a BLS entry, skipping duplicates.
func (c *KargsCommand) addKargsToEntry(path string, kargs []string) error {
	added, err := addKargsToBLSFile(path, kargs, c.run.readFile, c.run.writeFile)
	if err != nil {
		return err
	}

	if len(added) > 0 {
		c.Printf("Added [%s] to %s\n",
			strings.Join(added, ", "), filepath.Base(path))
	} else {
		c.Printf("No changes needed for %s (args already present)\n",
			filepath.Base(path))
	}

	return nil
}

// rmKargsFromEntry removes kernel arguments from a BLS entry.
func (c *KargsCommand) rmKargsFromEntry(path string, kargs []string) error {
	lines, err := c.parseBLSEntry(path)
	if err != nil {
		return err
	}

	idx, options := getOptionsLine(lines)
	if idx < 0 {
		return fmt.Errorf("no 'options' line found in %s", filepath.Base(path))
	}

	removeSet := make(map[string]bool, len(kargs))
	for _, karg := range kargs {
		removeSet[karg] = true
	}

	existingArgs := strings.Fields(options)
	var kept []string
	var removed []string
	for _, arg := range existingArgs {
		if removeSet[arg] {
			removed = append(removed, arg)
		} else {
			kept = append(kept, arg)
		}
	}

	if len(removed) > 0 {
		lines[idx] = "options " + strings.Join(kept, " ")
		if err := c.writeBLSEntry(path, lines); err != nil {
			return err
		}
		c.Printf("Removed [%s] from %s\n",
			strings.Join(removed, ", "), filepath.Base(path))
	} else {
		c.Printf("No changes needed for %s (args not present)\n",
			filepath.Base(path))
	}

	return nil
}

// printAllEntries prints the current kernel options for each BLS entry.
func (c *KargsCommand) printAllEntries(entries []string) error {
	c.Println("")
	c.Println("Current kernel options:")

	for _, entry := range entries {
		lines, err := c.parseBLSEntry(entry)
		if err != nil {
			return err
		}
		_, options := getOptionsLine(lines)
		name := filepath.Base(entry)
		c.Printf("  %s:\n", name)
		// Print each karg on its own line for readability.
		for _, arg := range strings.Fields(options) {
			c.Printf("    %s\n", arg)
		}
	}

	return nil
}
