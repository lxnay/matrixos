package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"unicode"

	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
)

// Ensure UpgradeCommand implements ICommand.
var _ ICommand = (*UpgradeCommand)(nil)

var (
	grubEfiBinary        = "GRUBX64.EFI"
	systemdBootEfiBinary = "systemd-bootx64.efi"
	// shimMOKManager is the file shim ships alongside its own BOOTX64.EFI.
	// Its presence in an EFI directory is our signal that shim is actually
	// deployed there (and therefore that the directory's BOOTX64.EFI is the
	// shim binary, not e.g. systemd-boot's fallback).
	shimMOKManager = "mmx64.efi"
	bootloaders    = []string{
		grubEfiBinary,
		systemdBootEfiBinary,
	}
	// pagerBinary is the pager command to use for long output.
	pagerBinary = "less"
)

// UpgradeCommand is a command for upgrading the system
type UpgradeCommand struct {
	BaseCommand
	UI
	fs            *flag.FlagSet
	prompt        *Prompter
	assumeYes     bool
	updBootloader bool
	pretend       bool
	verbose       bool
	force         bool
	toCommit      string
}

// NewUpgradeCommand creates a new UpgradeCommand
func NewUpgradeCommand() *UpgradeCommand {
	return &UpgradeCommand{}
}

func (c *UpgradeCommand) Name() string {
	return "upgrade"
}

func (c *UpgradeCommand) Init(args []string) error {
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

	return nil
}

// parseArgs parses the command-line arguments without initializing config or ostree.
func (c *UpgradeCommand) parseArgs(args []string) error {
	c.fs = flag.NewFlagSet("upgrade", flag.ContinueOnError)
	c.fs.BoolVar(&c.updBootloader, "update-bootloader", false,
		"Update bootloader binaries in /efi")
	c.fs.BoolVar(&c.assumeYes, "y", false, "Assume yes to all prompts")
	c.fs.BoolVar(&c.pretend, "pretend", false, "Only fetch updates and show diff without applying them")
	c.fs.BoolVar(&c.verbose, "verbose", false, "Show detailed output")
	c.fs.BoolVar(&c.force, "force", false, "Force upgrade even if up to date")
	c.fs.StringVar(&c.toCommit, "to", "", "Upgrade to a specific commit hash (instead of the latest on the current branch)")
	c.fs.Usage = func() {
		fmt.Printf("Usage: vector %s [options]\n", c.Name())
		c.fs.PrintDefaults()
	}
	return c.fs.Parse(args)
}

func (c *UpgradeCommand) Run() error {
	c.SetupPrinters(c.Name())
	defer c.FlushPrinters()

	c.prompt = NewPrompter(os.Stdin, c.StdoutWriter(), c.StderrWriter(), &c.UI)

	// Check if we are running as root. If running as user, exit with error.
	if getEuid() != 0 {
		return fmt.Errorf("this command must be run as root")
	}

	oldCommit, ref, err := c.getCurrentState()
	if err != nil {
		return fmt.Errorf("failed to get current state: %w", err)
	}

	c.Printf("%s%sChecking for updates on branch: %s%s\n",
		c.cBlue, c.iconSearch, ref, c.cReset)
	c.Printf("  %sCurrent version: %s%s\n", c.cBold, oldCommit, c.cReset)

	c.Printf("\n%s%sFetching updates...%s\n",
		c.cBold, c.iconDownload, c.cReset)
	if err := c.upgradePull(); err != nil {
		return fmt.Errorf("failed to fetch updates: %w", err)
	}

	var newCommit string
	if c.toCommit != "" {
		newCommit = c.toCommit
	} else {
		c.ot.SetRef(ref)
		newCommit, err = c.ot.LastCommit()
		if err != nil {
			return fmt.Errorf("failed to get new commit: %w", err)
		}
	}

	updateBootloader := func() error {
		if c.updBootloader {
			if err := c.updateBootloader(newCommit); err != nil {
				return fmt.Errorf("failed to update bootloader: %w", err)
			}
		}
		return nil
	}

	if oldCommit == newCommit {
		c.Printf("\n%s%sSystem is already up to date.%s\n",
			c.cGreen, c.iconCheck, c.cReset)
		if !c.force {
			return updateBootloader()
		}
		c.Printf("\n%s%sForcing update despite no changes...%s\n",
			c.cYellow, c.iconWarn, c.cReset)
	} else {
		c.Printf("\n%s%sUpdate Available: %s%s\n",
			c.cGreen, c.iconNew, newCommit, c.cReset)
	}
	c.Println(c.separator)

	c.Printf("\n%s%sAnalyzing package changes...%s\n",
		c.cBold, c.iconPackage, c.cReset)
	if err := c.analyzeDiff(oldCommit, newCommit); err != nil {
		c.PrintErrf("Warning: failed to analyze diff: %v\n", err)
	}
	if err := c.analyzeEtcChanges(oldCommit, newCommit); err != nil {
		c.PrintErrf("Warning: failed to analyze /etc changes: %v\n", err)
	}
	c.Println(c.separator)

	if c.pretend {
		c.Printf("\n%sRunning in pretend mode. Exiting.%s\n", c.cYellow, c.cReset)
		return nil
	}

	if !c.assumeYes {
		c.Println("")
		promptMsg := fmt.Sprintf(
			"%s%sDo you want to apply this upgrade? [y/N] %s",
			c.cYellow, c.iconQuestion, c.cReset,
		)
		if !c.promptUser(promptMsg) {
			c.Printf("%sAborted.%s\n", c.iconError, c.cReset)
			return nil
		}
	}

	c.Printf("\n%s%sDeploying update...%s\n", c.cBold, c.iconRocket, c.cReset)
	if err := c.upgradeDeploy(); err != nil {
		return fmt.Errorf("failed to deploy update: %w", err)
	}

	if err := updateBootloader(); err != nil {
		return err
	}

	c.Printf("\n%s%sUpgrade successful!%s\n", c.cGreen, c.iconCheck, c.cReset)

	c.Printf("%s%sPlease reboot at your earliest convenience.%s\n",
		c.cYellow, c.iconWarn, c.cReset)
	return nil
}

func (c *UpgradeCommand) getCurrentState() (string, string, error) {
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return "", "", fmt.Errorf("failed to list deployments: %w", err)
	}

	for _, dep := range deployments {
		if dep.Booted {
			return dep.Checksum, dep.Refspec, nil
		}
	}

	return "", "", fmt.Errorf("no booted deployment found")
}

func (c *UpgradeCommand) upgradePull() error {
	return c.ot.Upgrade([]string{"--pull-only"})
}

func (c *UpgradeCommand) upgradeDeploy() error {
	return c.ot.Upgrade([]string{"--deploy-only"})
}

func (c *UpgradeCommand) updateBootloader(commit string) error {
	c.Printf("\n%s%sUpdating bootloader binaries...%s\n",
		c.cBold, c.iconGear, c.cReset)

	if err := c.updateEfiBinaries(commit); err != nil {
		return fmt.Errorf("failed to update EFI binaries: %w", err)
	}

	c.Printf("%s%sBootloader updated successfully for commit %s.%s\n",
		c.cGreen, c.iconCheck, commit, c.cReset)
	return nil
}

// updateEfiBinaries discovers and refreshes every supported bootloader binary
// already deployed under Imager.EfiRoot. GRUBX64.EFI and systemd-bootx64.efi
// are each replaced from their canonical location inside the new ostree
// deployment. Shim binaries are refreshed independently and only on EFI dirs
// where shim is actually deployed (signalled by the presence of mmx64.efi).
func (c *UpgradeCommand) updateEfiBinaries(commit string) error {
	efiRoot, err := c.cfg.GetItem("Imager.EfiRoot")
	if err != nil {
		return fmt.Errorf("failed to get EfiRoot from config: %w", err)
	}
	if efiRoot == "" {
		return fmt.Errorf("Imager.EfiRoot is not configured in matrixos.conf")
	}
	efiStat, err := os.Stat(efiRoot)
	if err != nil {
		return fmt.Errorf("failed to stat Imager.EfiRoot path: %w", err)
	}
	if !efiStat.IsDir() {
		return fmt.Errorf("Imager.EfiRoot path is not a directory: %s", efiRoot)
	}

	sbCertFileName, err := c.cfg.GetItem("Imager.EfiCertificateFileName")
	if err != nil {
		return fmt.Errorf("failed to get EfiCertificateFileName from config: %w", err)
	}
	if sbCertFileName == "" {
		return fmt.Errorf("Imager.EfiCertificateFileName is not configured in matrixos.conf")
	}

	sbCertPath := filepath.Join(efiRoot, sbCertFileName)
	if _, err := os.Stat(sbCertPath); os.IsNotExist(err) {
		return fmt.Errorf("certificate file not found at: %s", sbCertPath)
	} else if err != nil {
		return fmt.Errorf("failed to stat SecureBoot certificate file: %w", err)
	}

	newRoot, err := c.deploymentRoot(commit)
	if err != nil {
		return err
	}

	type efiHit struct{ dir, name string }
	var hits []efiHit
	err = filepath.WalkDir(efiRoot, func(
		path string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !slices.Contains(bootloaders, d.Name()) {
			return nil
		}
		c.Printf("   Found EFI file: %s%s%s\n", c.cBlue, path, c.cReset)

		cmd := execCommand("sbverify", "--cert", sbCertPath, path)
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			c.PrintErrf(
				"   %s%sError verifying EFI file %s: %v%s\n",
				c.cRed, c.iconError, path, err, c.cReset)
			return fmt.Errorf("sbverify failed for %s: %w", path, err)
		}
		c.Printf("   %sVerified EFI file: %s%s%s\n",
			c.iconCheck, c.cGreen, path, c.cReset)
		hits = append(hits, efiHit{dir: filepath.Dir(path), name: d.Name()})
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to collect bootloaders: %w", err)
	}

	var haveGrub, haveSystemd bool
	for _, h := range hits {
		c.Printf("   %sUpdating bootloader binaries in %s...\n",
			c.iconPackage, h.dir)
		switch h.name {
		case grubEfiBinary:
			haveGrub = true
			if err := c.updateGrubDir(newRoot, h.dir, commit); err != nil {
				return fmt.Errorf("failed to update grub binaries: %w", err)
			}
		case systemdBootEfiBinary:
			haveSystemd = true
			if err := c.updateSystemdBootDir(newRoot, h.dir, commit); err != nil {
				return fmt.Errorf("failed to update systemd-boot binaries: %w", err)
			}
		}
		c.Printf("   %sBootloader binaries updated successfully in %s.\n",
			c.iconCheck, h.dir)
	}

	if haveGrub && haveSystemd {
		c.PrintErrf(
			"%s%sBoth GRUB and systemd-boot detected in ESP; skipping shim refresh to avoid clobbering bootloaders.%s\n",
			c.cYellow, c.iconWarn, c.cReset)
		return nil
	}

	return c.updateShimIfDeployed(newRoot, efiRoot)
}

// deploymentRoot resolves the on-disk rootfs path of the given ostree commit.
func (c *UpgradeCommand) deploymentRoot(commit string) (string, error) {
	root, err := c.ot.Root()
	if err != nil {
		return "", fmt.Errorf("failed to get ostree root: %w", err)
	}
	deployments, err := c.ot.ListDeployments()
	if err != nil {
		return "", fmt.Errorf("failed to list deployments: %w", err)
	}
	for _, dep := range deployments {
		if dep.Checksum == commit {
			return ostree.BuildDeploymentRootfs(
				root, dep.Stateroot, commit, dep.Index,
			), nil
		}
	}
	return "", fmt.Errorf("deployment not found for commit %s", commit)
}

func (c *UpgradeCommand) updateGrubDir(newRoot, efiDir, commit string) error {
	c.Printf(
		"   %sUpdating GRUB in %s%s%s for commit %s%s%s...\n",
		c.iconUpdate, c.cBlue, efiDir, c.cReset, c.cBold, commit, c.cReset,
	)
	src := filepath.Join(newRoot, "/usr/lib/grub/grub-x86_64.efi.signed")
	dst := filepath.Join(efiDir, grubEfiBinary)
	return c.copyRequiredEfiFile(src, dst)
}

func (c *UpgradeCommand) updateSystemdBootDir(newRoot, efiDir, commit string) error {
	c.Printf(
		"   %sUpdating systemd-boot in %s%s%s for commit %s%s%s...\n",
		c.iconUpdate, c.cBlue, efiDir, c.cReset, c.cBold, commit, c.cReset,
	)
	src := filepath.Join(newRoot, "/usr/lib/systemd/boot/efi/systemd-bootx64.efi.signed")
	dst := filepath.Join(efiDir, systemdBootEfiBinary)
	return c.copyRequiredEfiFile(src, dst)
}

// updateShimIfDeployed refreshes shim binaries only on EFI dirs that already
// host shim (detected via the mmx64.efi MOK manager). This avoids clobbering
// systemd-boot's own BOOTX64.EFI on pure systemd-boot installs.
func (c *UpgradeCommand) updateShimIfDeployed(newRoot, efiRoot string) error {
	var targets []string
	err := filepath.WalkDir(efiRoot, func(
		path string, d os.DirEntry, err error,
	) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if d.Name() == shimMOKManager {
			targets = append(targets, filepath.Dir(path))
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to scan for deployed shim: %w", err)
	}
	if len(targets) == 0 {
		return nil
	}

	shimDir := filepath.Join(newRoot, "/usr/share/shim")
	shimFiles, err := os.ReadDir(shimDir)
	if err != nil {
		c.PrintErrf(
			"%s%sShim is deployed in ESP but %s is missing in the new commit; skipping shim refresh.%s\n",
			c.cYellow, c.iconWarn, shimDir, c.cReset)
		return nil
	}

	for _, target := range targets {
		c.Printf("   %sUpdating shim binaries in %s%s%s...\n",
			c.iconUpdate, c.cBlue, target, c.cReset)
		for _, entry := range shimFiles {
			if entry.IsDir() || !entry.Type().IsRegular() {
				continue
			}
			src := filepath.Join(shimDir, entry.Name())
			dst := filepath.Join(target, entry.Name())
			if err := c.copyEfiFile(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *UpgradeCommand) copyEfiFile(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		c.PrintErrf(
			"%s%sExpected file was not found in new commit: %s%s\n",
			c.cYellow, c.iconWarn, src, c.cReset)
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to stat expected file: %w", err)
	}
	c.Printf("   %sCopying %s to %s%s%s...\n",
		c.iconDoc, filepath.Base(src), c.cBold, dst, c.cReset)
	if err := filesystems.CopyFile(src, dst); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}
	return nil
}

// copyRequiredEfiFile is like copyEfiFile but treats a missing source as a
// hard error. Used for bootloader binaries where a missing signed binary in
// the new commit must abort the upgrade rather than leave a stale binary.
func (c *UpgradeCommand) copyRequiredEfiFile(src, dst string) error {
	if _, err := os.Stat(src); os.IsNotExist(err) {
		return fmt.Errorf("required bootloader binary missing in new commit: %s", src)
	} else if err != nil {
		return fmt.Errorf("failed to stat required bootloader binary: %w", err)
	}
	c.Printf("   %sCopying %s to %s%s%s...\n",
		c.iconDoc, filepath.Base(src), c.cBold, dst, c.cReset)
	if err := filesystems.CopyFile(src, dst); err != nil {
		return fmt.Errorf("failed to copy file: %w", err)
	}
	return nil
}

func (c *UpgradeCommand) promptUser(prompt string) bool {
	return c.prompt.AskConfirm(prompt)
}

func (c *UpgradeCommand) analyzeDiff(oldSHA, newSHA string) error {
	opkgs, err := c.ot.ListPackages(oldSHA)
	if err != nil {
		return err
	}
	oldPkgs := make(map[string]bool)
	for _, pkg := range opkgs {
		oldPkgs[pkg] = true
	}

	npkgs, err := c.ot.ListPackages(newSHA)
	if err != nil {
		return err
	}
	newPkgs := make(map[string]bool)
	for _, pkg := range npkgs {
		newPkgs[pkg] = true
	}

	removed := make(map[string]bool)
	added := make(map[string]bool)

	for pkg := range oldPkgs {
		if !newPkgs[pkg] {
			removed[pkg] = true
		}
	}
	for pkg := range newPkgs {
		if !oldPkgs[pkg] {
			added[pkg] = true
		}
	}

	if len(removed) == 0 && len(added) == 0 {
		c.Printf(
			"   %s%sNo package changes detected (Config/Binary only update).%s\n",
			c.cBlue, c.iconPackage, c.cReset,
		)
		return nil
	}

	var removedList []string
	for pkg := range removed {
		removedList = append(removedList, pkg)
	}
	sort.Strings(removedList)

	for _, pkg := range removedList {
		baseName := c.getPackageBaseName(pkg)
		var newVer string
		for addedPkg := range added {
			if c.getPackageBaseName(addedPkg) == baseName {
				newVer = addedPkg
				break
			}
		}

		if newVer != "" {
			c.Printf("   %s %s%s%s -> %s%s%s\n",
				c.iconUpdate, c.cYellow, pkg, c.cReset,
				c.cGreen, newVer, c.cReset)
			delete(added, newVer)
		} else {
			c.Printf("   %s %s%s%s (Removed)\n",
				c.iconError, c.cRed, pkg, c.cReset)
		}
	}

	var addedList []string
	for pkg := range added {
		addedList = append(addedList, pkg)
	}
	sort.Strings(addedList)

	for _, pkg := range addedList {
		c.Printf("   %s %s%s%s (New)\n",
			c.iconNew, c.cGreen, pkg, c.cReset)
	}

	c.Println(c.separator)
	return nil
}

func (c *UpgradeCommand) analyzeEtcChanges(oldSHA, newSHA string) error {
	changes, err := c.ot.ListEtcChanges(oldSHA, newSHA)
	if err != nil {
		return err
	}

	if len(changes) == 0 {
		c.Printf(
			"   %s%sNo /etc changes detected (Config/Binary only update).%s\n",
			c.cBlue, c.iconPackage, c.cReset,
		)
		return nil
	}
	c.Printf("   %s%s/etc changes detected:%s\n", c.cYellow, c.iconPackage, c.cReset)

	output := c.formatEtcChanges(changes)

	// Use a pager if the output is large (more than 30 lines)
	lines := strings.Count(output, "\n")
	if lines > 30 && !c.assumeYes {
		return c.showWithPager(output)
	}
	c.Printf("%s", output)
	return nil
}

// formatEtcChanges renders the list of EtcChange entries into a
// human-readable string using the UI icons and colours.
func (c *UpgradeCommand) formatEtcChanges(changes []ostree.EtcChange) string {
	var b strings.Builder

	// Group changes by action for a structured summary.
	var conflicts, updates, adds, removes, userOnly []ostree.EtcChange
	for _, ch := range changes {
		switch ch.Action {
		case ostree.EtcActionConflict:
			conflicts = append(conflicts, ch)
		case ostree.EtcActionUpdate:
			updates = append(updates, ch)
		case ostree.EtcActionAdd:
			adds = append(adds, ch)
		case ostree.EtcActionRemove:
			removes = append(removes, ch)
		case ostree.EtcActionUserOnly:
			userOnly = append(userOnly, ch)
		}
	}

	somethingPrinted := false

	// Conflicts first — they require attention.
	if len(conflicts) > 0 {
		somethingPrinted = true
		fmt.Fprintf(&b, "\n   %s%s Conflicts (manual resolution required):%s\n",
			c.cRed, c.iconWarn, c.cReset)
		for _, ch := range conflicts {
			fmt.Fprintf(&b, "      %s %s/etc/%s%s\n",
				c.iconError, c.cRed, ch.Path, c.cReset)
			c.writeChangeDetail(&b, ch)
		}
	}

	// Updates — clean upstream changes that will be applied.
	if len(updates) > 0 {
		somethingPrinted = true
		fmt.Fprintf(&b, "\n   %s%s Updated by upstream (will be applied):%s\n",
			c.cGreen, c.iconUpdate, c.cReset)
		for _, ch := range updates {
			fmt.Fprintf(&b, "      %s %s/etc/%s%s\n",
				c.iconUpdate, c.cGreen, ch.Path, c.cReset)
			c.writeChangeDetail(&b, ch)
		}
	}

	// Adds — new files from upstream.
	if len(adds) > 0 {
		somethingPrinted = true
		fmt.Fprintf(&b, "\n   %s%s New files from upstream:%s\n",
			c.cGreen, c.iconNew, c.cReset)
		for _, ch := range adds {
			fmt.Fprintf(&b, "      %s %s/etc/%s%s\n",
				c.iconNew, c.cGreen, ch.Path, c.cReset)
			c.writeChangeDetail(&b, ch)
		}
	}

	// Removes — files removed upstream.
	if len(removes) > 0 {
		somethingPrinted = true
		fmt.Fprintf(&b, "\n   %s%s Removed by upstream (will be deleted):%s\n",
			c.cYellow, c.iconError, c.cReset)
		for _, ch := range removes {
			fmt.Fprintf(&b, "      %s %s/etc/%s%s\n",
				c.iconError, c.cYellow, ch.Path, c.cReset)
		}
	}

	// User-only — local changes preserved as-is.
	if len(userOnly) > 0 && c.verbose {
		somethingPrinted = true
		fmt.Fprintf(&b, "\n   %s%s User modifications (preserved):%s\n",
			c.cBlue, c.iconDoc, c.cReset)
		for _, ch := range userOnly {
			fmt.Fprintf(&b, "      %s %s/etc/%s%s\n",
				c.iconDoc, c.cBlue, ch.Path, c.cReset)
		}
	}

	if !somethingPrinted {
		fmt.Fprintf(&b, "\n   %s%s No changes worth highlighting.%s\n",
			c.cBlue, c.iconPackage, c.cReset)
	}

	// Summary line
	fmt.Fprintf(&b, "\n   %sSummary:%s %d conflict(s), %d update(s), %d add(s), %d remove(s), %d user-only\n",
		c.cBold, c.cReset,
		len(conflicts), len(updates), len(adds), len(removes), len(userOnly))

	return b.String()
}

// writeChangeDetail appends detail lines about what changed for a path.
func (c *UpgradeCommand) writeChangeDetail(b *strings.Builder, ch ostree.EtcChange) {
	if ch.Old != nil && ch.New != nil {
		oldDesc := ch.Old.String()
		newDesc := ch.New.String()
		if oldDesc != newDesc {
			fmt.Fprintf(b, "        %swas:%s %s\n", c.cBold, c.cReset, oldDesc)
			fmt.Fprintf(b, "        %snow:%s %s\n", c.cBold, c.cReset, newDesc)
		}
	} else if ch.New != nil {
		fmt.Fprintf(b, "        %snew:%s %s\n", c.cBold, c.cReset, ch.New.String())
	}
	if ch.User != nil && ch.Old != nil && !ch.User.Equals(ch.Old) {
		fmt.Fprintf(b, "        %slocal:%s %s\n", c.cBold, c.cReset, ch.User.String())
	}
}

// showWithPager pipes the given text through a pager (e.g. less).
func (c *UpgradeCommand) showWithPager(text string) error {
	pager := os.Getenv("PAGER")
	if pager == "" {
		pager = pagerBinary
	}
	cmd := execCommand(pager, "-R")
	cmd.Stdin = strings.NewReader(text)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func (c *UpgradeCommand) getPackageBaseName(pkg string) string {
	parts := strings.SplitN(pkg, "/", 2)
	if len(parts) != 2 {
		return pkg
	}
	category := parts[0]
	rest := parts[1]

	lastHyphen := -1
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == '-' {
			if i+1 < len(rest) && unicode.IsDigit(rune(rest[i+1])) {
				lastHyphen = i
				break
			}
		}
	}

	if lastHyphen != -1 {
		name := rest[:lastHyphen]
		return category + "/" + name
	}
	return pkg
}
