package ostree

import (
	"errors"
	"fmt"
	"strings"
)

// BranchContainsRemote checks if a branch ref contains a remote.
// A remote is present if the ref contains a ':'.
// The original shell implementation had a bug and was checking for `.*` at the end, not for a colon.
// This implementation follows the function's name intent.
func BranchContainsRemote(branch string) bool {
	return strings.Contains(branch, ":")
}

// ExtractRemoteFromRef extracts the remote name from a ref.
// E.g. "origin:matrixos/dev" -> "origin".
// If no remote is present, returns an empty string.
func ExtractRemoteFromRef(ref string) string {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 {
		return parts[0]
	}
	return ""
}

// CleanRemoteFromRef cleans a ref from its remote part.
// E.g. "origin:matrixos/dev" -> "matrixos/dev".
func CleanRemoteFromRef(ref string) string {
	parts := strings.SplitN(ref, ":", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return ref
}

// CleanAndStripRef strips the full-branch suffix from ot's current ref and
// removes any leading remote prefix (e.g. "origin:matrixos/amd64/gnome-full"
// -> "matrixos/amd64/gnome").
func CleanAndStripRef(ot IOstree) (string, error) {
	if ot == nil {
		return "", errors.New("missing ostree parameter")
	}
	stripped, err := ot.RemoveFullFromBranch()
	if err != nil {
		return "", err
	}
	stripped = CleanRemoteFromRef(stripped)
	if stripped == "" {
		return "", errors.New("invalid ref parameter after cleaning")
	}
	return stripped, nil
}

// IsBranchShortName returns true if the branch is a short name.
// E.g. "gnome" -> true, "matrixos/dev/gnome" -> false.
func IsBranchShortName(branch string) bool {
	return !strings.Contains(branch, "/")
}

// BranchShortnameToNormal converts a short branch name to a normal one.
func BranchShortnameToNormal(relStage, shortname, osName, arch string) (string, error) {
	if relStage == "" {
		return "", errors.New("invalid rel stage parameter")
	}
	if shortname == "" {
		return "", errors.New("invalid branch parameter")
	}
	if osName == "" {
		return "", errors.New("invalid os name parameter")
	}
	if arch == "" {
		return "", errors.New("invalid arch parameter")
	}

	nameArch := fmt.Sprintf("%s/%s", osName, arch)
	if relStage == "prod" {
		return fmt.Sprintf("%s/%s", nameArch, shortname), nil
	}
	return fmt.Sprintf("%s/%s/%s", nameArch, relStage, shortname), nil
}

func (o *Ostree) FullBranchSuffix() (string, error) {
	suffix, err := o.cfg.GetItem("Ostree.FullBranchSuffix")
	if err != nil {
		return "", err
	}
	if suffix == "" {
		return "", errors.New("missing full branch suffix")
	}
	return suffix, nil
}

// isBranchFullSuffixed checks if a given ref name is a "full" branch.
func (o *Ostree) isBranchFullSuffixed(ref string) (bool, error) {
	if ref == "" {
		return false, errors.New("missing ref parameter")
	}
	val, err := o.FullBranchSuffix()
	if err != nil {
		return false, err
	}
	return strings.HasSuffix(ref, "-"+val), nil
}

func (o *Ostree) IsBranchFullSuffixed() (bool, error) {
	return o.isBranchFullSuffixed(o.ref)
}

func (o *Ostree) BranchToFull() (string, error) {
	ref := o.ref
	if ref == "" {
		return "", errors.New("invalid ref parameter")
	}

	suffix, err := o.FullBranchSuffix()
	if err != nil {
		return "", err
	}
	if strings.HasSuffix(ref, "-"+suffix) {
		// Support idempotency.
		return ref, nil
	}
	return fmt.Sprintf("%s-%s", ref, suffix), nil
}

func (o *Ostree) RemoveFullFromBranch() (string, error) {
	ref := o.ref
	if ref == "" {
		return "", errors.New("invalid ref parameter")
	}

	suffix, err := o.FullBranchSuffix()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(ref, "-"+suffix), nil
}
