package release

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
)

// Each repo carries its own version line. llm-d and llm-d-router are not
// released in lockstep, so a version legal for one can be a version the
// other already shipped. Everything here is scoped to one remote.

// Compare orders two versions. A release candidate sorts before the
// final release of the same MAJOR.MINOR.PATCH.
func Compare(a, b *Version) int {
	if c := cmp.Or(
		cmp.Compare(a.Major, b.Major),
		cmp.Compare(a.Minor, b.Minor),
		cmp.Compare(a.Patch, b.Patch),
	); c != 0 {
		return c
	}
	switch {
	case a.RC == b.RC:
		return 0
	case a.RC == 0: // final beats any rc
		return 1
	case b.RC == 0:
		return -1
	default:
		return cmp.Compare(a.RC, b.RC)
	}
}

// RemoteVersions returns every parseable release tag on remote, highest
// first. Reading the remote rather than local tags keeps a stale fork
// from contributing versions the upstream repo never released.
func RemoteVersions(dir, remote string) ([]*Version, error) {
	out, err := gitOutput(dir, "ls-remote", "--tags", "--refs", remote)
	if err != nil {
		return nil, fmt.Errorf("listing tags on %s: %w", remote, err)
	}

	var versions []*Version
	for _, line := range strings.Split(out, "\n") {
		_, ref, ok := strings.Cut(line, "refs/tags/")
		if !ok {
			continue
		}
		v, err := ParseVersion(strings.TrimSpace(ref))
		if err != nil {
			continue // tags that are not releases
		}
		versions = append(versions, v)
	}

	slices.SortFunc(versions, func(a, b *Version) int { return Compare(b, a) })
	return versions, nil
}

// LatestRelease returns the highest release tag on remote, or nil when
// the repo has never been released.
func LatestRelease(dir, remote string) (*Version, error) {
	versions, err := RemoteVersions(dir, remote)
	if err != nil {
		return nil, err
	}
	if len(versions) == 0 {
		return nil, nil
	}
	return slices.MaxFunc(versions, Compare), nil
}

// successors returns the versions that may legally follow latest,
// ignoring release-candidate suffixes.
func successors(latest *Version) []string {
	return []string{
		fmt.Sprintf("v%d.%d.%d", latest.Major, latest.Minor, latest.Patch+1),
		fmt.Sprintf("v%d.%d.0", latest.Major, latest.Minor+1),
		fmt.Sprintf("v%d.0.0", latest.Major+1),
	}
}

// target renders the MAJOR.MINOR.PATCH a version is heading for, with
// any release-candidate suffix dropped.
func target(v *Version) string {
	return fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// ValidateSuccessor reports whether want may follow latest on the same
// version line, as a status and an explanation. A nil latest means the
// repo has no releases yet.
//
// Fail means want is already released or older, which is always a
// mistake. Warn means want is newer but skips ahead, which is a
// judgement call the caller applies policy to.
func ValidateSuccessor(latest, want *Version) (Status, string) {
	if latest == nil {
		return Pass, ""
	}

	switch c := Compare(want, latest); {
	case c == 0:
		return Fail, fmt.Sprintf("%s is already released on this remote.", want.Tag)
	case c < 0:
		return Fail, fmt.Sprintf("%s is older than the current release %s.", want.Tag, latest.Tag)
	}

	// A further release candidate, or the final release, of the version
	// already in progress.
	if target(want) == target(latest) {
		return Pass, ""
	}

	allowed := successors(latest)
	if slices.Contains(allowed, target(want)) {
		return Pass, ""
	}

	return Warn, fmt.Sprintf(
		"%s does not immediately follow %s on this remote. Expected one of %s.",
		want.Tag, latest.Tag, strings.Join(allowed, ", "))
}
