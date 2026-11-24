package ostree

import (
	"fmt"

	"matrixos/vector/lib/config"
)

func (o *Ostree) GpgEnabled() (bool, error) {
	return o.cfg.GetBool("Ostree.Gpg")
}

func (o *Ostree) SetGpg(enabled bool) error {
	repoDir, err := o.RepoDir()
	if err != nil {
		return err
	}
	val := "false"
	if enabled {
		val = "true"
	}
	return o.ostreeRun("--repo="+repoDir, "config", "set", "core.gpg-verify", val)
}

func (o *Ostree) GpgPrivateKeyPath() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.GpgPrivateKey")
}

func (o *Ostree) GpgPublicKeyPath() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.GpgPublicKey")
}

func (o *Ostree) GpgOfficialPubKeyPath() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.GpgOfficialPublicKey")
}

func (o *Ostree) OsName() (string, error) {
	return config.GetRequiredItem(o.cfg, "matrixOS.OsName")
}

func (o *Ostree) FancyOsName() (string, error) {
	return config.GetRequiredItem(o.cfg, "matrixOS.FancyOsName")
}

func (o *Ostree) Arch() (string, error) {
	return config.GetRequiredItem(o.cfg, "matrixOS.Arch")
}

func (o *Ostree) RepoDir() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.RepoDir")
}

func (o *Ostree) Sysroot() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.Sysroot")
}

func (o *Ostree) Root() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.Root")
}

func (o *Ostree) Remote() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.Remote")
}

func (o *Ostree) RemoteURL() (string, error) {
	return config.GetRequiredItem(o.cfg, "Ostree.RemoteUrl")
}

func (o *Ostree) AvailableGpgPubKeyPaths() ([]string, error) {
	var candidates []string
	privatePubKeyPath, err := o.GpgPublicKeyPath()
	if err == nil {
		candidates = append(candidates, privatePubKeyPath)
	}
	officialPubKeyPath, err := o.GpgOfficialPubKeyPath()
	if err == nil {
		candidates = append(candidates, officialPubKeyPath)
	}

	var paths []string
	for _, path := range candidates {
		if fileExists(path) {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return paths, fmt.Errorf(
			"unable to find a valid GPG pub key. Neither: %v nor %v exist",
			privatePubKeyPath,
			officialPubKeyPath,
		)
	}

	return paths, nil
}

func (o *Ostree) GpgBestPubKeyPath() (string, error) {
	paths, err := o.AvailableGpgPubKeyPaths()
	if err != nil {
		return "", err
	}
	// pick the first, it's the best always.
	return paths[0], nil
}

func (o *Ostree) ClientSideGpgArgs() ([]string, error) {
	gpgEnabled, err := o.GpgEnabled()
	if err != nil {
		return nil, err
	}
	var pubKeyPath string
	if gpgEnabled {
		pubKeyPath, err = o.GpgBestPubKeyPath()
		if err != nil {
			return nil, err
		}
	}
	return ClientSideGpgArgs(gpgEnabled, pubKeyPath)
}
