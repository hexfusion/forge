package release

import (
	"fmt"
	"slices"
	"strings"
)

// Status is the outcome of one check.
type Status int

const (
	// Unknown is the zero value. A check that never set its status has
	// not run, which for a release gate must not read as success.
	Unknown Status = iota
	// Pass means the condition holds.
	Pass
	// Warn means the release can proceed but the operator has to see it.
	Warn
	// Fail means the release is wrong as specified and stops here.
	Fail
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "pass"
	case Warn:
		return "warn"
	case Fail:
		return "FAIL"
	case Unknown:
		return "NOT RUN"
	default:
		return fmt.Sprintf("Status(%d)", int(s))
	}
}

// Check is one validated condition. Every precondition the release
// process depends on is a Check, so nothing is enforced in one code path
// and forgotten in another.
type Check struct {
	Name   string
	Status Status
	Detail string
}

// Checks is the full validation result.
type Checks []Check

// Failed reports whether any check failed or never ran.
func (c Checks) Failed() bool {
	return slices.ContainsFunc(c, func(ch Check) bool {
		return ch.Status == Fail || ch.Status == Unknown
	})
}

// NonPassing returns the details of every check that did not pass.
func (c Checks) NonPassing() []string {
	var out []string
	for _, ch := range c {
		if ch.Status != Pass {
			out = append(out, ch.Detail)
		}
	}
	return out
}

// Render returns the checks as a reviewable list.
func (c Checks) Render() string {
	var b strings.Builder
	for _, ch := range c {
		fmt.Fprintf(&b, "  [%s] %s\n", ch.Status, ch.Name)
		if ch.Detail != "" && ch.Status != Pass {
			fmt.Fprintf(&b, "         %s\n", ch.Detail)
		}
	}
	return b.String()
}

// validateInput is what the checks run against.
type validateInput struct {
	Repo      string
	RepoDir   string
	Remote    string
	Version   *Version
	Predictor string
	AllowSkip bool
}

// validate runs every precondition for a release and returns the result.
// It mutates nothing, so it is safe to run at any time, and cut runs it
// as a gate rather than duplicating the logic.
func validate(in validateInput) (Checks, error) {
	latest, err := LatestRelease(in.RepoDir, in.Remote)
	if err != nil {
		return nil, err
	}
	// Resolved once so every check reads the same observation of the
	// remote rather than each shelling out and possibly disagreeing.
	branchOnRemote, err := remoteBranchExists(in.RepoDir, in.Remote, in.Version.Branch)
	if err != nil {
		return nil, err
	}

	checks := Checks{
		checkVersionLine(in, latest),
		checkTagAvailable(in),
		checkReleaseBranch(in, branchOnRemote),
		checkPredictorTag(in, branchOnRemote),
		checkWorkingTree(in),
	}
	return checks, nil
}

// failed builds a check that could not be evaluated. A gate that cannot
// observe a precondition must not report it as satisfied.
func failed(name string, err error) Check {
	return Check{Name: name, Status: Fail, Detail: "could not be checked: " + err.Error()}
}

// checkVersionLine is the guard against cutting a sibling repo's
// version. Each repo advances independently, so the only authority is
// this remote's own tags.
func checkVersionLine(in validateInput, latest *Version) Check {
	c := Check{
		Name:   fmt.Sprintf("version line: %s follows this repo's latest release", in.Version.Tag),
		Status: Pass,
	}

	if latest == nil {
		c.Status = Warn
		c.Detail = fmt.Sprintf("%s has no releases on %s, so %s cannot be checked against a predecessor.",
			in.Repo, in.Remote, in.Version.Tag)
		return c
	}

	status, detail := ValidateSuccessor(latest, in.Version)
	switch {
	case status == Fail:
		c.Status = Fail
		c.Detail = fmt.Sprintf("%s Latest on %s is %s.", detail, in.Remote, latest.Tag)
	case status == Warn && !in.AllowSkip:
		// Skipping ahead is only advisory once the operator opts in.
		c.Status = Fail
		c.Detail = detail + " Pass --allow-version-skip to cut it anyway."
	case status == Warn:
		c.Status = Warn
		c.Detail = detail + " Allowed by --allow-version-skip."
	default:
		c.Detail = fmt.Sprintf("latest on %s is %s", in.Remote, latest.Tag)
	}
	return c
}

func checkTagAvailable(in validateInput) Check {
	tag := in.Version.Tag
	c := Check{Name: fmt.Sprintf("tag %s is available", tag), Status: Pass}

	onRemote, err := remoteTagExists(in.RepoDir, in.Remote, tag)
	if err != nil {
		return failed(c.Name, err)
	}
	local, err := tagExists(in.RepoDir, tag)
	if err != nil {
		return failed(c.Name, err)
	}

	switch {
	case onRemote:
		c.Status = Fail
		c.Detail = fmt.Sprintf("%s is already published on %s.", tag, in.Remote)
	case local:
		c.Status = Fail
		c.Detail = fmt.Sprintf(
			"%s exists locally but not on %s. Delete the local tag or pick another version.", tag, in.Remote)
	}
	return c
}

// checkReleaseBranch encodes the template's own assumption: only a
// release candidate cuts a new branch, everything else needs one.
func checkReleaseBranch(in validateInput, exists bool) Check {
	v := in.Version
	c := Check{Name: fmt.Sprintf("release branch %s is in the expected state", v.Branch), Status: Pass}

	switch {
	case v.ExpectsExistingBranch() && !exists:
		c.Status = Fail
		c.Detail = fmt.Sprintf(
			"%s is not on %s, but %s is not a release candidate and requires it. Cut a release candidate first, or confirm the branch was deleted.",
			v.Branch, in.Remote, v.Tag)
	case v.IsRC() && exists:
		c.Status = Warn
		c.Detail = fmt.Sprintf(
			"%s already exists on %s, so this release candidate branches from it rather than from main.",
			v.Branch, in.Remote)
	case exists:
		c.Detail = fmt.Sprintf("%s/%s exists", in.Remote, v.Branch)
	default:
		c.Detail = fmt.Sprintf("%s will be created from %s/main", v.Branch, in.Remote)
	}
	return c
}

// checkPredictorTag surfaces what the Makefile resolves rather than what
// the operator assumes. BUILD_REF names the nearest preceding tag, so
// before this release is tagged it names an older one.
func checkPredictorTag(in validateInput, branchOnRemote bool) Check {
	c := Check{Name: "LATENCY_PREDICTOR_TAG resolves as expected", Status: Pass}

	base := in.Remote + "/main"
	if branchOnRemote {
		base = in.Remote + "/" + in.Version.Branch
	}

	prev := describePreceding(in.RepoDir, base)
	switch {
	case prev == "":
		c.Status = Warn
		c.Detail = fmt.Sprintf("no preceding release tag is reachable from %s, so BUILD_REF resolves to empty and the tag falls back to latest.", base)
	case prev != in.Version.Tag:
		c.Status = Warn
		c.Detail = fmt.Sprintf(
			"BUILD_REF resolves to %s from %s until %s is tagged, so the Makefile default names that release, not this one.",
			prev, base, in.Version.Tag)
	default:
		c.Detail = "BUILD_REF already names this release"
	}
	return c
}

func checkWorkingTree(in validateInput) Check {
	c := Check{Name: "checkout is clean", Status: Pass}
	clean, err := isClean(in.RepoDir)
	if err != nil {
		return failed(c.Name, err)
	}
	if !clean {
		c.Status = Warn
		c.Detail = fmt.Sprintf("%s has uncommitted changes. The release runs in its own worktree, so this does not block it.", in.RepoDir)
	}
	return c
}
