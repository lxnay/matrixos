// Package main is the main entry point for vector. Vector is the (future) matrixOS
// management toolkit for development, building, releasing, installing and managing
// matrixOS.
package main

import (
	"fmt"
	"matrixos/vector/commands"
	"matrixos/vector/lib/ostree"
	"os"
)

const (
	helpMessage = `matrixos' vector - Your OS handy tool.
Usage:

  help        - this command.
  branch      - vector branch command. Operates on OS ostree branches.
    show            show the currently booted branch and its metadata.
    deployment      show local branch deployments with their associated metadata.
    pin <index>     pin a deployment to preserve it as rollback target at boot.
    unpin <index>   unpin a deployment (inverse of pin).
    remote          list all the remote OS branches available.
    local           list all the local OS branches available.
    switch <ref>    switch to a new OS branch from those available.
  upgrade     - system upgrade tool, wraps ostree.
  kargs       - manage kernel boot arguments in BLS configs.
    add <karg>...   add kernel arguments to all boot entries (skip if already present).
    rm <karg>...    remove kernel arguments from all boot entries.
  setupOS     - setup tool, configures passwords, accounts, languages, etc.
  readwrite   - temporarily (until next upgrade) turn OS into a (mutable) read-write system.
  jailbreak   - permanently turns this system into a regular mutable Gentoo.
  dev 	      - development toolkit command, orchestrates development workflow and tools.
    check           checks that the host has all the required binaries/data to run the build workflow.
    janitor         cleans up development toolkit artifacts, such as old images and downloads.
    vm              runs generated image tests using QEMU.
  flash       - install (flash) the running matrixOS system to a block device or partitions.
  cfg         - shell-script-friendly config access (plain stdout, errors to stderr).
    get <key>...    print config values as key=value lines.
  build       - build toolkit command, orchestrates building OS artifacts.
    all             runs the full build-and-release pipeline (seeds, releases, images, janitor, CDN push).
    seeds           builds chroot filesystems using the configured seeders.
    release         generates a single OS release (ostree commit).
    releases        generates multiple OS releases across all detected seeders.
    image           generates a single OS image.
    images          generates multiple OS images based on released branches.
`
)

func main() {
	if len(os.Args) < 2 {
		fmt.Print(helpMessage)
		os.Exit(1)
	}

	ostree.SetupEnvironment()

	cmds := []commands.ICommand{
		commands.NewBranchCommand(),
		commands.NewUpgradeCommand(),
		commands.NewKargsCommand(),
		commands.NewFlashCommand(),
		commands.NewReadWriteCommand(),
		commands.NewSetupOSCommand(),
		commands.NewJailbreakCommand(),
		commands.NewDevCommand(),
		commands.NewCfgCommand(),
		commands.NewBuildCommand(),
	}

	cmdStr := os.Args[1]
	subcmdArgs := os.Args[2:]

	if cmdStr == "help" || cmdStr == "--help" || cmdStr == "-h" {
		fmt.Print(helpMessage)
		os.Exit(0)
	}

	for _, cmd := range cmds {
		if cmd.Name() == cmdStr {
			if err := cmd.Init(subcmdArgs); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			if err := cmd.Run(); err != nil {
				fmt.Fprintf(os.Stderr, "Error: %v\n", err)
				os.Exit(1)
			}
			os.Exit(0)
		}
	}

	fmt.Printf("Unknown command: %s\n", cmdStr)
	os.Exit(1)
}
