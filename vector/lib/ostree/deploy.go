package ostree

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

type Deployment struct {
	Checksum  string `json:"checksum"`
	Stateroot string `json:"stateroot"`
	// Requires matrixOS ostree-2025.7-r1
	Refspec  string `json:"refspec"`
	Booted   bool   `json:"booted"`
	Pending  bool   `json:"pending"`
	Rollback bool   `json:"rollback"`
	Staged   bool   `json:"staged"`
	Index    int    `json:"index"`
	Serial   int    `json:"serial"`
	Pinned   bool   `json:"pinned"`
}

// BuildDeploymentRootfs builds the path to the deployed rootfs given a sysroot, osName,
// commit, and deployment serial.
func BuildDeploymentRootfs(sysroot, osName, commit string, serial int) string {
	return filepath.Join(
		sysroot,
		"ostree",
		"deploy",
		osName,
		"deploy",
		commit+"."+strconv.Itoa(serial),
	)
}

// listDeploymentsFromSysroot lists deployments using the instance runner.
func (o *Ostree) listDeploymentsFromSysroot(sysroot string) ([]Deployment, error) {
	if sysroot == "" {
		return nil, errors.New("invalid ostree sysroot parameter")
	}
	stdout, err := o.ostreeRunCapture("--sysroot="+sysroot, "admin", "status", "--json")
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(stdout)
	if err != nil {
		return nil, fmt.Errorf("failed to read ostree status: %w", err)
	}
	var deployments struct {
		Deployments []Deployment `json:"deployments"`
	}
	if err := json.Unmarshal(data, &deployments); err != nil {
		return nil, fmt.Errorf("failed to unmarshal ostree status: %w", err)
	}
	return deployments.Deployments, nil
}

func (o *Ostree) BootCommit(sysroot string) (string, error) {
	osName, err := o.OsName()
	if err != nil {
		return "", err
	}
	bootPrefix := filepath.Join(sysroot, "ostree", "boot.1", osName)
	files, err := os.ReadDir(bootPrefix)
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", fmt.Errorf("no commit found in %s", bootPrefix)
	}
	return files[0].Name(), nil
}

func (o *Ostree) ListDeployments() ([]Deployment, error) {
	root, err := o.Root()
	if err != nil {
		return nil, err
	}
	return o.listDeploymentsFromSysroot(root)
}

func (o *Ostree) DeployedRootfs() (string, error) {
	sysroot, err := o.Sysroot()
	if err != nil {
		return "", err
	}

	ref := o.ref
	if ref == "" {
		return "", errors.New("invalid ref parameter")
	}
	osName, err := o.OsName()
	if err != nil {
		return "", err
	}

	ostreeCommit, err := o.LastCommit()
	if err != nil {
		return "", fmt.Errorf("cannot get last ostree commit: %w", err)
	}

	rootfs := BuildDeploymentRootfs(sysroot, osName, ostreeCommit, 0)
	return rootfs, nil
}

func (o *Ostree) BootedRef() (string, error) {
	root, err := o.Root()
	if err != nil {
		return "", err
	}
	deployments, err := o.listDeploymentsFromSysroot(root)
	if err != nil {
		return "", err
	}
	for _, d := range deployments {
		if d.Booted {
			return d.Refspec, nil
		}
	}
	return "", errors.New("no booted deployment found")
}

func (o *Ostree) Switch() error {
	root, err := o.Root()
	if err != nil {
		return err
	}
	return o.ostreeRun("admin", "switch", "--sysroot="+root, o.ref)
}

func (o *Ostree) PostCopy() error {
	sysroot, err := o.Sysroot()
	if err != nil {
		return err
	}
	return o.ostreeRun("admin", "post-copy", fmt.Sprintf("--sysroot=%s", sysroot))
}

func (o *Ostree) Deploy(sysroot string, bootArgs []string) error {
	ref := o.ref
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}
	remote, err := o.Remote()
	if err != nil {
		return err
	}

	o.Print("Creating %s ...\n", sysroot)
	if err := os.MkdirAll(sysroot, 0755); err != nil {
		return err
	}

	osName, err := o.OsName()
	if err != nil {
		return err
	}

	sysrootRepo := filepath.Join(sysroot, "ostree", "repo")
	var ostreeCommit string
	if err := func() error {
		unlock := LockRepo(repoDir)
		defer unlock()

		var lerr error
		ostreeCommit, lerr = o.lastCommitFromRepo(repoDir, ref)
		if lerr != nil {
			return fmt.Errorf("cannot get last ostree commit: %w", lerr)
		}
		o.Print(
			"Last ostree commit for ref %s in %s is %s.\n",
			ref, repoDir, ostreeCommit,
		)

		o.Print("Initializing ostree dir structure into %s ...\n", sysroot)
		if err := o.ostreeRun("admin", "init-fs", sysroot); err != nil {
			return err
		}

		o.Print("Initializing OS %s into %s...\n", osName, sysroot)
		osInitArgs := []string{
			"admin", "os-init",
			osName,
			"--sysroot=" + sysroot,
		}
		if err := o.ostreeRun(osInitArgs...); err != nil {
			return err
		}

		o.Print("Pulling local ostree commit %s into %s ...\n", ostreeCommit, sysrootRepo)
		pullArgs := []string{
			"--repo=" + sysrootRepo,
			"pull-local",
			repoDir,
			ostreeCommit,
		}
		return o.ostreeRun(pullArgs...)
	}(); err != nil {
		return err
	}

	createRefArgs := []string{
		"refs",
		"--repo=" + sysrootRepo,
		"--create=" + remote + ":" + ref,
		ostreeCommit,
	}
	o.Print("ostree creating ref %s in sysroot repo ...\n", remote+":"+ref)
	if err := o.ostreeRun(createRefArgs...); err != nil {
		return err
	}

	o.Print("ostree setting bootloader to none (using blscfg instead) ...")
	blArgs := []string{
		"config",
		"--repo=" + sysrootRepo,
		"set",
		"sysroot.bootloader",
		"none",
	}
	if err := o.ostreeRun(blArgs...); err != nil {
		return err
	}

	o.Print("ostree setting bootprefix = false, given separate boot partition ...")
	bootprefixArgs := []string{
		"config",
		"--repo=" + sysrootRepo,
		"set",
		"sysroot.bootprefix",
		"false",
	}
	if err := o.ostreeRun(bootprefixArgs...); err != nil {
		return err
	}

	o.Print("Deploying %s to %s...\n", ref, sysroot)
	deployArgs := []string{
		"admin", "deploy",
		"--sysroot=" + sysroot,
		"--os=" + osName,
	}
	for _, ba := range bootArgs {
		deployArgs = append(deployArgs, "--karg-append="+ba)
	}
	deployArgs = append(deployArgs, remote+":"+ref)

	if err := o.ostreeRun(deployArgs...); err != nil {
		return err
	}

	o.Print("Deployed filesystem at %s for commit %s.\n", sysroot, ostreeCommit)
	return nil
}

func (o *Ostree) Upgrade(args []string) error {
	root, err := o.Root()
	if err != nil {
		return err
	}

	cmdArgs := []string{"admin", "upgrade", "--sysroot=" + root}
	cmdArgs = append(cmdArgs, args...)

	return o.ostreeRun(cmdArgs...)
}
