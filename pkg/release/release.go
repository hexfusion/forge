// Package release cuts upstream releases for the llm-d repos.
//
// Scope: derive the release identity from one version argument, resolve
// it against the real repo and workflow state, and produce an ordered
// plan. A dry run writes that plan to disk and mutates nothing. A real
// run performs the local git work only. Pushing branches and tags stays
// a human action because tag protection gates it.
//
// The release issue template is the process this replaces. It asks an
// operator to export VERSION and BRANCH_VERSION independently, which
// nothing cross-checks, and it names the published images in prose,
// which drifts from the workflows that build them.
package release

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/hexfusion/forge/pkg/pipeline"
	"github.com/spf13/cobra"
)

const (
	defaultProject  = "llm-d"
	defaultRemote   = "upstream"
	defaultRegistry = "ghcr.io/llm-d"
)

type cutOptions struct {
	project   string
	remote    string
	registry  string
	predictor string
	outDir    string
	dryRun    bool
	allowSkip bool
}

// NewCommand returns the release command tree.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "release",
		Short:         "Cut upstream releases",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return cmd.Help()
		},
	}
	cmd.AddCommand(newValidateCommand())
	cmd.AddCommand(newCutCommand())
	cmd.AddCommand(newArtifactsCommand())
	return cmd
}

func newValidateCommand() *cobra.Command {
	o := &cutOptions{}

	cmd := &cobra.Command{
		Use:   "validate <repo> <version>",
		Short: "Check every precondition for a release without touching anything",
		Long: `Check every precondition for a release.

Each repo advances on its own version line, so the version is validated
against this repo's own latest release on the remote, never against a
sibling repo. Exits non-zero when any check fails.

This runs as a gate inside cut, so a plan can never be built on
preconditions that were not checked.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoDir, err := resolveRepoDir(o.project, args[0])
			if err != nil {
				return err
			}
			v, err := ParseVersion(args[1])
			if err != nil {
				return err
			}
			if err := gitRun(repoDir, "fetch", o.remote); err != nil {
				return fmt.Errorf("fetching %s: %w", o.remote, err)
			}

			predictor := cmp.Or(o.predictor, v.Tag)
			checks, err := validate(validateInput{
				Repo: args[0], RepoDir: repoDir, Remote: o.remote,
				Version: v, Predictor: predictor, AllowSkip: o.allowSkip,
			})
			if err != nil {
				return err
			}

			fmt.Printf("Validating %s %s against %s\n\n", args[0], v.Tag, o.remote)
			fmt.Print(checks.Render())
			if checks.Failed() {
				return fmt.Errorf("validation failed for %s %s", args[0], v.Tag)
			}
			fmt.Println("\nAll checks passed.")
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.project, "project", defaultProject, "Project graph to resolve the repo from")
	f.StringVar(&o.remote, "remote", defaultRemote, "Git remote holding the upstream repo")
	f.StringVar(&o.predictor, "predictor-tag", "", "Override LATENCY_PREDICTOR_TAG when it does not track the release")
	f.BoolVar(&o.allowSkip, "allow-version-skip", false, "Allow a version that does not immediately follow the current release")

	return cmd
}

func newCutCommand() *cobra.Command {
	o := &cutOptions{}

	cmd := &cobra.Command{
		Use:   "cut <repo> <version>",
		Short: "Plan or perform the local work for a release",
		Long: `Plan or perform the local git work for a release.

VERSION is the full tag, for example v0.10.0 or v0.10.0-rc.1. Every other
release variable is derived from it, so BRANCH_VERSION cannot disagree
with VERSION.

With --dry-run nothing is mutated. The resolved plan, environment,
published artifacts, and announcement are written to disk for review.

Forge never pushes. Push commands are emitted for you to run.`,
		Args:          cobra.ExactArgs(2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runCut(args[0], args[1], o)
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.project, "project", defaultProject, "Project graph to resolve the repo from")
	f.StringVar(&o.remote, "remote", defaultRemote, "Git remote holding the upstream repo")
	f.StringVar(&o.registry, "registry", defaultRegistry, "Registry the release publishes to")
	f.StringVar(&o.predictor, "predictor-tag", "", "Override LATENCY_PREDICTOR_TAG when it does not track the release")
	f.StringVar(&o.outDir, "out", "", "Directory for dry-run output (default <state>/releases/<repo>-<version>)")
	f.BoolVar(&o.dryRun, "dry-run", false, "Resolve and write the plan without mutating anything")
	f.BoolVar(&o.allowSkip, "allow-version-skip", false, "Cut a version that does not immediately follow the current release")

	return cmd
}

func newArtifactsCommand() *cobra.Command {
	o := &cutOptions{}

	cmd := &cobra.Command{
		Use:   "artifacts <repo> [ref]",
		Short: "List the images and charts a release tag publishes",
		Long: `List the images and charts a release tag publishes, read from the
release workflows at ref (default <remote>/main) rather than from a
maintained list, so a newly added image shows up here on its own.`,
		Args:          cobra.RangeArgs(1, 2),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			repoDir, err := resolveRepoDir(o.project, args[0])
			if err != nil {
				return err
			}
			ref := o.remote + "/main"
			if len(args) == 2 {
				ref = args[1]
			}
			a, err := EnumerateArtifacts(repoDir, args[0], ref)
			if err != nil {
				return err
			}
			fmt.Print(a.Render(o.registry, "<tag>"))
			return nil
		},
	}

	f := cmd.Flags()
	f.StringVar(&o.project, "project", defaultProject, "Project graph to resolve the repo from")
	f.StringVar(&o.remote, "remote", defaultRemote, "Git remote holding the upstream repo")
	f.StringVar(&o.registry, "registry", defaultRegistry, "Registry the release publishes to")

	return cmd
}

// resolveRepoDir maps a repo key in the project graph to its checkout.
func resolveRepoDir(project, repo string) (string, error) {
	p, err := pipeline.LoadProject(project)
	if err != nil {
		return "", err
	}
	r, ok := p.Repos[repo]
	if !ok {
		return "", fmt.Errorf("repo %q not in project %q", repo, project)
	}
	if r.Local == "" {
		return "", fmt.Errorf("repo %q has no local checkout configured", repo)
	}
	if _, err := os.Stat(r.Local); err != nil {
		return "", fmt.Errorf("checkout for %q not found at %s", repo, r.Local)
	}
	return r.Local, nil
}

func runCut(repo, version string, o *cutOptions) error {
	plan, err := buildPlan(repo, version, o)
	if err != nil {
		return err
	}

	// A failed check stops the release in both modes. A dry run that
	// printed a plan built on failed preconditions would be worse than
	// no plan at all.
	if plan.Checks.Failed() {
		fmt.Print(plan.Render())
		return fmt.Errorf("validation failed for %s %s, nothing was done", repo, plan.Version.Tag)
	}

	if o.dryRun {
		dir, err := writeDryRun(plan, o.outDir)
		if err != nil {
			return err
		}
		fmt.Print(plan.Render())
		fmt.Printf("\nDry run. Nothing was mutated. Output written to %s\n", dir)
		return nil
	}

	fmt.Print(plan.Render())
	fmt.Println("\nExecuting local steps.")
	return execute(plan)
}

// buildPlan resolves the release against the repo and workflow state.
func buildPlan(repo, version string, o *cutOptions) (*Plan, error) {
	v, err := ParseVersion(version)
	if err != nil {
		return nil, err
	}
	repoDir, err := resolveRepoDir(o.project, repo)
	if err != nil {
		return nil, err
	}

	// Fetch branches so branch checks reflect the remote. Tags are read
	// with ls-remote instead, because fetching them can collide with
	// tags a fork already placed on the same names.
	if err := gitRun(repoDir, "fetch", o.remote); err != nil {
		return nil, fmt.Errorf("fetching %s: %w", o.remote, err)
	}

	predictor := cmp.Or(o.predictor, v.Tag)

	checks, err := validate(validateInput{
		Repo: repo, RepoDir: repoDir, Remote: o.remote,
		Version: v, Predictor: predictor, AllowSkip: o.allowSkip,
	})
	if err != nil {
		return nil, err
	}

	p := &Plan{
		Repo:      repo,
		RepoDir:   repoDir,
		Worktree:  pipeline.WorktreeDir(repoDir, repo, v.Branch),
		Version:   v,
		Remote:    o.remote,
		Predictor: predictor,
		Registry:  o.registry,
		Env:       v.Env(o.remote, predictor),
		Checks:    checks,
	}

	branchOnRemote, err := remoteBranchExists(repoDir, o.remote, v.Branch)
	if err != nil {
		return nil, err
	}
	base := o.remote + "/main"
	if branchOnRemote {
		base = o.remote + "/" + v.Branch
	}

	p.Steps = buildSteps(p, branchOnRemote)

	a, err := EnumerateArtifacts(repoDir, repo, base)
	if err != nil {
		return nil, err
	}
	p.Artifacts = a

	artifactCheck := Check{Name: "release workflows name the published artifacts", Status: Pass}
	if a.Empty() {
		artifactCheck.Status = Fail
		artifactCheck.Detail = fmt.Sprintf(
			"no images or charts were discovered in %s at %s. The announcement would list nothing, so the release cannot be verified.",
			releaseWorkflow, base)
	} else {
		artifactCheck.Detail = fmt.Sprintf("%d images, %d charts", len(a.Images), len(a.Charts))
	}
	p.Checks = append(p.Checks, artifactCheck)

	return p, nil
}

func buildSteps(p *Plan, branchOnRemote bool) []Step {
	v := p.Version
	var steps []Step

	if branchOnRemote {
		steps = append(steps, Step{
			Desc: fmt.Sprintf("Create worktree on %s from %s/%s", v.Branch, p.Remote, v.Branch),
			Dir:  p.RepoDir,
			Cmd:  []string{"git", "worktree", "add", "-B", v.Branch, p.Worktree, p.Remote + "/" + v.Branch},
		})
	} else {
		steps = append(steps, Step{
			Desc: fmt.Sprintf("Create worktree on new branch %s from %s/main", v.Branch, p.Remote),
			Dir:  p.RepoDir,
			Cmd:  []string{"git", "worktree", "add", "-b", v.Branch, p.Worktree, p.Remote + "/main"},
		})
	}

	// Forge does not edit the Makefile, so the commit cannot run on its
	// own. Without the edit git would find nothing staged and abort.
	pin := Step{
		Desc:   fmt.Sprintf("Pin LATENCY_PREDICTOR_TAG to %s in the Makefile, then commit", p.Predictor),
		Dir:    p.Worktree,
		Cmd:    []string{"git", "commit", "-a", "-s", "-m", "release: set LATENCY_PREDICTOR_TAG to " + p.Predictor},
		Manual: true,
		Note:   fmt.Sprintf("edit LATENCY_PREDICTOR_TAG ?= %s in %s/Makefile first", p.Predictor, p.Worktree),
	}
	if p.Predictor == v.Tag {
		pin.Skip = true
		pin.Why = "predictor tracks the release version"
	}
	steps = append(steps, pin)

	steps = append(steps, Step{
		Desc: fmt.Sprintf("Create signed tag %s", v.Tag),
		Dir:  p.Worktree,
		Cmd:  []string{"git", "tag", "-s", "-a", v.Tag, "-m", fmt.Sprintf("%s %s Release", p.Repo, v.Tag)},
	})

	steps = append(steps, Step{
		Desc:   "Push the release branch",
		Dir:    p.Worktree,
		Cmd:    []string{"git", "push", p.Remote, v.Branch},
		Remote: true,
	})
	steps = append(steps, Step{
		Desc:   "Push the tag, which triggers the release build",
		Dir:    p.Worktree,
		Cmd:    []string{"git", "push", p.Remote, v.Tag},
		Remote: true,
	})

	return steps
}

// execute runs the local steps. Remote and skipped steps are reported
// and left for the operator.
func execute(p *Plan) error {
	for i, s := range p.Steps {
		switch {
		case s.Skip:
			fmt.Printf("  %d. skip: %s (%s)\n", i+1, s.Desc, s.Why)
			continue
		case s.Remote, s.Manual:
			fmt.Printf("  %d. run yourself: %s\n", i+1, s.String())
			if s.Note != "" {
				fmt.Printf("       %s\n", s.Note)
			}
			continue
		}
		fmt.Printf("  %d. %s\n", i+1, s.Desc)
		if len(s.Cmd) == 0 {
			return fmt.Errorf("step %d (%s): no command", i+1, s.Desc)
		}
		if err := runCommand(s.Dir, s.Cmd); err != nil {
			return fmt.Errorf("step %d (%s): %w", i+1, s.Desc, err)
		}
	}
	return nil
}

// writeDryRun records the plan, environment, artifacts, and announcement
// so the release can be reviewed before anything runs.
func writeDryRun(p *Plan, outDir string) (string, error) {
	if outDir == "" {
		outDir = filepath.Join(pipeline.StateDir(), "releases", p.Repo+"-"+p.Version.Tag)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("creating %s: %w", outDir, err)
	}

	// Ordered so a failure part way through leaves a predictable subset.
	files := []struct {
		name string
		body string
		mode os.FileMode
	}{
		{"plan.md", p.Render(), 0o644},
		{"env", strings.Join(p.Env, "\n") + "\n", 0o644},
		{"artifacts.txt", p.Artifacts.Render(p.Registry, p.Version.Tag), 0o644},
		{"announce.txt", p.Announcement(), 0o644},
		{"push.sh", renderPushScript(p), 0o755},
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(outDir, f.name), []byte(f.body), f.mode); err != nil {
			return "", fmt.Errorf("writing %s: %w", f.name, err)
		}
	}
	return outDir, nil
}

// renderPushScript emits the remote steps forge refuses to run.
func renderPushScript(p *Plan) string {
	var b strings.Builder
	b.WriteString("#!/usr/bin/env bash\n")
	b.WriteString("# Remote steps. Forge does not run these.\n")
	b.WriteString("set -euo pipefail\n\n")
	fmt.Fprintf(&b, "cd %s\n", p.Worktree)
	for _, s := range p.Steps {
		if s.Remote {
			fmt.Fprintf(&b, "%s\n", s.String())
		}
	}
	return b.String()
}
