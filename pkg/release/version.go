package release

import (
	"fmt"
	"regexp"
	"strconv"
)

// semverRE matches vMAJOR.MINOR.PATCH with an optional -rc.N suffix.
// Components are bounded so they always fit an int, which lets the
// parse below rely on the pattern instead of re-checking every field.
var semverRE = regexp.MustCompile(`^v(\d{1,9})\.(\d{1,9})\.(\d{1,9})(?:-rc\.(\d{1,9}))?$`)

// Version is the full release identity derived from a single tag string.
// Everything the release process needs is computed here so no two values
// can disagree.
type Version struct {
	Tag   string
	Major int
	Minor int
	Patch int
	RC    int // 0 when not a release candidate

	// BranchVersion is the MAJOR.MINOR the release branch is named for.
	BranchVersion string
	// Branch is the release branch name.
	Branch string
}

// IsRC reports whether this is a release candidate.
func (v *Version) IsRC() bool { return v.RC > 0 }

// IsPatch reports whether this is a patch release.
func (v *Version) IsPatch() bool { return v.Patch > 0 && !v.IsRC() }

// ExpectsExistingBranch reports whether the release branch must already
// exist. Only a release candidate cuts a new branch.
func (v *Version) ExpectsExistingBranch() bool { return !v.IsRC() }

// ParseVersion derives the release identity from a tag such as
// v0.10.0-rc.1. Returns an error rather than guessing at a malformed tag.
func ParseVersion(tag string) (*Version, error) {
	m := semverRE.FindStringSubmatch(tag)
	if m == nil {
		return nil, fmt.Errorf("malformed version %q: want vMAJOR.MINOR.PATCH or vMAJOR.MINOR.PATCH-rc.N", tag)
	}

	// Errors are dropped because semverRE bounds every component to nine
	// digits, so each one parses.
	v := &Version{Tag: tag}
	v.Major, _ = strconv.Atoi(m[1])
	v.Minor, _ = strconv.Atoi(m[2])
	v.Patch, _ = strconv.Atoi(m[3])
	if m[4] != "" {
		v.RC, _ = strconv.Atoi(m[4])
		if v.RC == 0 {
			return nil, fmt.Errorf("release candidate number must be positive in %q", tag)
		}
	}

	v.BranchVersion = fmt.Sprintf("%d.%d", v.Major, v.Minor)
	v.Branch = "release-" + v.BranchVersion
	return v, nil
}

// Env returns the release variables as KEY=VALUE pairs. These replace the
// block an operator would otherwise export by hand.
func (v *Version) Env(remote, predictorTag string) []string {
	return []string{
		"VERSION=" + v.Tag,
		"BRANCH_VERSION=" + v.BranchVersion,
		"RELEASE_BRANCH=" + v.Branch,
		"REMOTE=" + remote,
		"LATENCY_PREDICTOR_TAG=" + predictorTag,
	}
}
