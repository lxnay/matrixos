package releaser

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"matrixos/vector/lib/config"
	"matrixos/vector/lib/filesystems"
	"matrixos/vector/lib/ostree"
	"matrixos/vector/lib/runner"
)

// validServiceName matches systemd unit names and targets.
// Allowed: alphanumeric, dash, underscore, dot, at-sign, backslash.
var validServiceName = regexp.MustCompile(
	`^[a-zA-Z0-9@._\\\-]+$`,
)

func (r *Releaser) SetupHostname() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}
	hostname, err := r.Hostname()
	if err != nil {
		return err
	}

	r.Print("Setting hostname to: %s\n", hostname)
	data := []byte(hostname + "\n")
	return os.WriteFile(filepath.Join(r.imageDir, "etc/hostname"), data, 0644)
}

func (r *Releaser) cleanAndStripRef() (string, error) {
	if r.ref == "" {
		return "", errors.New("missing ref, set Ref in Releaser")
	}
	stripped, err := r.ostree.RemoveFullFromBranch()
	if err != nil {
		return "", err
	}

	stripped = ostree.CleanRemoteFromRef(stripped)
	if stripped == "" {
		return "", errors.New("invalid ref parameter after cleaning")
	}
	return stripped, nil
}

// serviceAction represents a parsed line from a services configuration file.
type serviceAction struct {
	action   string
	services []string
}

// parseServicesFile reads a services configuration file and returns parsed actions.
func parseServicesFile(path string) ([]serviceAction, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var actions []serviceAction
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}

		actions = append(actions, serviceAction{
			action:   fields[0],
			services: fields[1:],
		})
	}
	return actions, scanner.Err()
}

func (r *Releaser) SetupServices() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}

	// Remove full from ref using the functions
	ref, err := r.cleanAndStripRef()
	if err != nil {
		return err
	}

	hooksDir, err := r.HooksDir()
	if err != nil {
		return err
	}

	servicesFile := filepath.Join(hooksDir, ref+".conf")
	if !filesystems.FileExists(servicesFile) {
		r.PrintWarning(
			"Services setup file %s does not exist. Trying to look harder ...\n",
			servicesFile,
		)

		// Fallback: check in the services dir relative to the same parent.
		parent := filepath.Dir(hooksDir)
		altPath := filepath.Join(parent, "services", ref+".conf")
		if !filesystems.FileExists(altPath) {
			r.PrintError(
				"Services setup file %s does not exist. Create an empty file at least ...\n",
				servicesFile,
			)
			return fmt.Errorf("services setup file does not exist: %s", servicesFile)
		}
		servicesFile = altPath
	}

	r.Print("Using services setup file: %s\n", servicesFile)
	actions, err := parseServicesFile(servicesFile)
	if err != nil {
		return fmt.Errorf("failed to parse services file: %w", err)
	}

	// Group services by action type.
	var (
		enable        []string
		disable       []string
		mask          []string
		presetEnable  []string
		presetDisable []string
		presetMask    []string
		defaultTarget string
	)

	for _, a := range actions {
		switch a.action {
		case "enable":
			enable = append(enable, a.services...)
		case "disable":
			disable = append(disable, a.services...)
		case "mask":
			mask = append(mask, a.services...)
		case "preset-enable":
			presetEnable = append(presetEnable, a.services...)
		case "preset-disable":
			presetDisable = append(presetDisable, a.services...)
		case "preset-mask":
			presetMask = append(presetMask, a.services...)
		case "set-default":
			if len(a.services) > 0 {
				defaultTarget = a.services[len(a.services)-1]
			}
		default:
			r.PrintWarning(
				"Unrecognized action in %s: %s\n",
				servicesFile, a.action,
			)
		}
	}

	script, err := buildServicesScript(buildServicesScriptOptions{
		enable:        enable,
		disable:       disable,
		mask:          mask,
		presetEnable:  presetEnable,
		presetDisable: presetDisable,
		presetMask:    presetMask,
		defaultTarget: defaultTarget,
	})
	if err != nil {
		return err
	}
	if script == "" {
		r.Print("No service actions to perform.\n")
		return nil
	}

	r.Print("Setting up services in a single chroot call ...\n")

	// Write the script into the chroot's /tmp so it is accessible
	// inside the chroot at /tmp/_matrixos_services.sh.
	scriptName := "_matrixos_services.sh"
	hostPath := filepath.Join(r.imageDir, "tmp", scriptName)
	if err := os.WriteFile(hostPath, []byte(script), 0o700); err != nil {
		return fmt.Errorf(
			"failed to write services script: %w", err,
		)
	}
	defer os.Remove(hostPath)

	chrootScript := filepath.Join("/tmp", scriptName)
	return r.chroot(
		nil,
		"/bin/bash",
		[]string{chrootScript},
	)
}

type buildServicesScriptOptions struct {
	enable, disable, mask,
	presetEnable, presetDisable, presetMask []string
	defaultTarget string
}

// buildServicesScript generates a bash script that performs all
// systemctl operations in one shot. Every service name is validated
// against validServiceName to prevent command injection.
func buildServicesScript(opts buildServicesScriptOptions) (string, error) {
	allNames := make([]string, 0,
		len(opts.enable)+len(opts.disable)+len(opts.mask)+
			len(opts.presetEnable)+len(opts.presetDisable)+
			len(opts.presetMask))
	allNames = append(allNames, opts.enable...)
	allNames = append(allNames, opts.disable...)
	allNames = append(allNames, opts.mask...)
	allNames = append(allNames, opts.presetEnable...)
	allNames = append(allNames, opts.presetDisable...)
	allNames = append(allNames, opts.presetMask...)
	if opts.defaultTarget != "" {
		allNames = append(allNames, opts.defaultTarget)
	}

	for _, n := range allNames {
		if !validServiceName.MatchString(n) {
			return "", fmt.Errorf(
				"invalid service/target name %q", n,
			)
		}
	}

	if len(allNames) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("#!/bin/bash\nset -e\n\n")

	writeBlock := func(cmd string, global bool, svcs []string) {
		if len(svcs) == 0 {
			return
		}
		args := "systemctl"
		if global {
			args += " --global"
		}
		args += " " + cmd
		for _, s := range svcs {
			args += " " + s
		}
		fmt.Fprintf(&b, "echo '%s %s ...'\n", cmd, strings.Join(svcs, " "))
		b.WriteString(args + "\n\n")
	}

	writeBlock("enable", false, opts.enable)
	writeBlock("disable", false, opts.disable)
	writeBlock("mask", false, opts.mask)
	writeBlock("enable", true, opts.presetEnable)
	writeBlock("disable", true, opts.presetDisable)
	writeBlock("mask", true, opts.presetMask)

	if opts.defaultTarget != "" {
		fmt.Fprintf(&b,
			"echo 'set-default %s ...'\n"+
				"systemctl set-default %s\n",
			opts.defaultTarget, opts.defaultTarget,
		)
	}

	return b.String(), nil
}

func (r *Releaser) ReleaseHook() error {
	if err := checkImageDir(r.imageDir); err != nil {
		return err
	}

	// Remove full from ref using the functions
	ref, err := r.cleanAndStripRef()
	if err != nil {
		return err
	}

	hooksDir, err := r.HooksDir()
	if err != nil {
		return err
	}

	devDir, err := r.DevDir()
	if err != nil {
		return err
	}

	defaultPrivPath, err := r.DefaultPrivateGitRepoPath()
	if err != nil {
		return err
	}

	defaultUsername, err := r.configItem("matrixOS.DefaultUsername")
	if err != nil {
		return err
	}
	OSName, err := r.configItem("matrixOS.OsName")
	if err != nil {
		return err
	}

	hookPath := filepath.Join(hooksDir, ref+".sh")
	if !filesystems.FileExists(hookPath) {
		r.PrintWarning(
			"Release hook %s does not exist. Create an empty executable file at least ...\n",
			hookPath,
		)
		return fmt.Errorf("release hook does not exist: %s", hookPath)
	}

	r.Print("Running release hook %s ...\n", hookPath)

	env := os.Environ()
	env = config.FilterEnvKey(env, "MATRIXOS_DEV_DIR")
	env = config.FilterEnvKey(env, "REF")
	env = config.FilterEnvKey(env, "DEFAULT_PRIVATE_GIT_REPO_PATH")
	env = append(
		env,
		"REF="+ref,
		"CHROOT_DIR="+r.imageDir,
		"MATRIXOS_DEV_DIR="+devDir,
		"DEFAULT_PRIVATE_GIT_REPO_PATH="+defaultPrivPath,
		"RELEASER_DEFAULT_USERNAME="+defaultUsername,
		"RELEASER_OSNAME="+OSName,
	)
	run := r.runner
	if run == nil {
		run = runner.Run
	}
	return run(&runner.Cmd{
		Name:   hookPath,
		Env:    env,
		Stdout: r.Stdout(),
		Stderr: r.Stderr(),
	})
}
