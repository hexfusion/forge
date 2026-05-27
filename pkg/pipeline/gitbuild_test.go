package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// initTestRepo creates a git repo at dir with one commit on the default branch
// and a second branch `branchName` carrying an extra commit. It returns the
// short SHA of the branch tip.
func initTestRepo(t *testing.T, dir, branchName string) (mainSHA, branchSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return trimSpace(string(out))
	}

	run("init", "-q")
	run("checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "Containerfile"), []byte("FROM scratch\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "initial")
	mainSHA = run("rev-parse", "HEAD")

	run("checkout", "-q", "-b", branchName)
	if err := os.WriteFile(filepath.Join(dir, "fix.txt"), []byte("fix\n"), 0644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "the fix")
	branchSHA = run("rev-parse", "HEAD")

	run("checkout", "-q", "main")
	return mainSHA, branchSHA
}

func TestResolveRef(t *testing.T) {
	dir := t.TempDir()
	_, branchSHA := initTestRepo(t, dir, "fix-37581")

	ref, sha, err := resolveRef(dir, "", "fix-37581")
	if err != nil {
		t.Fatalf("resolveRef: %v", err)
	}
	if ref != "fix-37581" {
		t.Errorf("ref = %q, want fix-37581", ref)
	}
	if sha != branchSHA {
		t.Errorf("sha = %q, want %q", sha, branchSHA)
	}

	if _, _, err := resolveRef(dir, "", "no-such-branch"); err == nil {
		t.Error("expected error for missing branch")
	}
}

func TestResolveGitBuildWorktree(t *testing.T) {
	dir := t.TempDir()
	_, branchSHA := initTestRepo(t, dir, "fix-37581")
	worktree := filepath.Join(t.TempDir(), "wt")

	img := &PipelineImage{
		Source: "build",
		Local:  dir,
		Git:    &PipelineImageGit{Branch: "fix-37581", Worktree: worktree},
	}

	buildPath, info, err := resolveGitBuild(img)
	if err != nil {
		t.Fatalf("resolveGitBuild: %v", err)
	}
	if buildPath != worktree {
		t.Errorf("buildPath = %q, want %q", buildPath, worktree)
	}
	if info.Branch != "fix-37581" {
		t.Errorf("info.Branch = %q", info.Branch)
	}
	if info.FullSHA != branchSHA {
		t.Errorf("info.FullSHA = %q, want %q", info.FullSHA, branchSHA)
	}
	if len(info.ShortSHA) != 7 {
		t.Errorf("ShortSHA = %q, want 7 chars", info.ShortSHA)
	}

	// The worktree HEAD must point at the branch tip.
	wtHead, err := cmdOutput(worktree, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("worktree rev-parse: %v", err)
	}
	if trimSpace(wtHead) != branchSHA {
		t.Errorf("worktree HEAD = %q, want %q", trimSpace(wtHead), branchSHA)
	}

	// The user's checkout must be untouched (still on main, where the fix
	// file does not exist).
	if _, err := os.Stat(filepath.Join(dir, "fix.txt")); !os.IsNotExist(err) {
		t.Error("user checkout was modified — fix.txt should not exist on main")
	}

	// Re-running reuses the existing worktree without error.
	if _, _, err := resolveGitBuild(img); err != nil {
		t.Fatalf("resolveGitBuild (reuse): %v", err)
	}
}

func TestResolveGitBuildDirtyRefuses(t *testing.T) {
	dir := t.TempDir()
	initTestRepo(t, dir, "fix-37581")

	// Dirty the working tree.
	if err := os.WriteFile(filepath.Join(dir, "dirty.txt"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}

	img := &PipelineImage{
		Source: "build",
		Local:  dir,
		Git:    &PipelineImageGit{Branch: "fix-37581"}, // no worktree -> in-place checkout
	}
	if _, _, err := resolveGitBuild(img); err == nil {
		t.Error("expected error building a dirty checkout without a worktree")
	}
}

func TestResolveGitBuildDefaultsToHead(t *testing.T) {
	dir := t.TempDir()
	mainSHA, _ := initTestRepo(t, dir, "fix-37581")

	// No Git block at all — bare tag_template should resolve current HEAD.
	img := &PipelineImage{Source: "build", Local: dir}
	buildPath, info, err := resolveGitBuild(img)
	if err != nil {
		t.Fatalf("resolveGitBuild: %v", err)
	}
	if buildPath != dir {
		t.Errorf("buildPath = %q, want %q", buildPath, dir)
	}
	if info.Branch != "main" {
		t.Errorf("info.Branch = %q, want main", info.Branch)
	}
	if info.FullSHA != mainSHA {
		t.Errorf("info.FullSHA = %q, want %q", info.FullSHA, mainSHA)
	}
}
