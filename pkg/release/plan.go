package release

import (
	"fmt"
	"strings"
)

// Step is one action in a release. Cmd is the exact argv, so the plan a
// dry run prints is the argv a real run executes.
type Step struct {
	Desc string
	Dir  string
	Cmd  []string

	// Remote marks a step that writes to the remote. Forge never runs
	// these. Tag protection makes pushing a human action.
	Remote bool
	// Manual marks a step forge cannot perform, such as an edit it has
	// no opinion about. Note carries the instruction.
	Manual bool
	Note   string
	// Skip marks a step resolved as unnecessary, with Why explaining.
	Skip bool
	Why  string
}

// String renders the step as a shell line.
func (s *Step) String() string { return strings.Join(s.Cmd, " ") }

// Plan is the ordered work for one release plus the state it was
// resolved against.
type Plan struct {
	Repo      string
	RepoDir   string
	Worktree  string
	Version   *Version
	Remote    string
	Predictor string
	Registry  string

	Env       []string
	Steps     []Step
	Artifacts *Artifacts

	// Checks is the validation result the plan was built against. A plan
	// carries its own preconditions so a reviewed plan and an executed
	// plan cannot disagree about what was verified.
	Checks Checks
}

// Render returns the reviewable plan document.
func (p *Plan) Render() string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Release plan: %s %s\n\n", p.Repo, p.Version.Tag)
	fmt.Fprintf(&b, "Repo       %s\n", p.RepoDir)
	fmt.Fprintf(&b, "Worktree   %s\n", p.Worktree)
	fmt.Fprintf(&b, "Branch     %s\n", p.Version.Branch)
	fmt.Fprintf(&b, "Remote     %s\n", p.Remote)
	if p.Version.IsRC() {
		fmt.Fprintf(&b, "Kind       release candidate rc.%d (cuts a new branch)\n", p.Version.RC)
	} else if p.Version.IsPatch() {
		b.WriteString("Kind       patch release (branch must exist)\n")
	} else {
		b.WriteString("Kind       release (branch must exist)\n")
	}

	b.WriteString("\n## Resolved environment\n\n")
	for _, e := range p.Env {
		fmt.Fprintf(&b, "  %s\n", e)
	}

	b.WriteString("\n## Validation\n\n")
	b.WriteString(p.Checks.Render())

	b.WriteString("\n## Steps\n\n")
	for i, s := range p.Steps {
		status := ""
		switch {
		case s.Skip:
			status = " [skip: " + s.Why + "]"
		case s.Remote, s.Manual:
			status = " [not run by forge]"
		}
		fmt.Fprintf(&b, "%d. %s%s\n", i+1, s.Desc, status)
		if s.Note != "" {
			fmt.Fprintf(&b, "     %s\n", s.Note)
		}
		if len(s.Cmd) > 0 {
			fmt.Fprintf(&b, "     %s\n", s.String())
		}
	}

	if p.Artifacts != nil {
		b.WriteString("\n## Published artifacts\n\n")
		b.WriteString(indent(p.Artifacts.Render(p.Registry, p.Version.Tag), "  "))
	}

	return b.String()
}

// Announcement renders the release email from the discovered artifacts
// rather than a hardcoded list.
func (p *Plan) Announcement() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Subject: [ANNOUNCE] %s %s is released\n\n", p.Repo, p.Version.Tag)
	b.WriteString("Hi all,\n\n")
	fmt.Fprintf(&b, "We are pleased to announce the release of %s %s!\n\n", p.Repo, p.Version.Tag)

	if len(p.Artifacts.Images) > 0 {
		b.WriteString("Container Images\n")
		for _, img := range p.Artifacts.Images {
			fmt.Fprintf(&b, "  %s/%s:%s\n", p.Registry, img, p.Version.Tag)
		}
		b.WriteString("\n")
	}
	if len(p.Artifacts.Charts) > 0 {
		b.WriteString("Helm Charts (OCI)\n")
		for _, c := range p.Artifacts.Charts {
			fmt.Fprintf(&b, "  oci://%s/charts/%s (version %s)\n", p.Registry, c, p.Version.Tag)
		}
		b.WriteString("\n")
	}

	b.WriteString("Release Notes\n")
	fmt.Fprintf(&b, "  https://github.com/llm-d/%s/releases/tag/%s\n", p.Repo, p.Version.Tag)
	return b.String()
}

func indent(s, pad string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		if l != "" {
			lines[i] = pad + l
		}
	}
	return strings.Join(lines, "\n") + "\n"
}
