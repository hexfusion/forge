package release

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// gitOutput runs git in dir and returns trimmed stdout. Git's stderr is
// folded into the error, since a release gate that reports "exit status
// 128" without saying why is not actionable.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), withStderr(err))
	}
	return strings.TrimSpace(string(out)), nil
}

// withStderr annotates an exec error with the stderr git produced.
func withStderr(err error) error {
	var ee *exec.ExitError
	if errors.As(err, &ee) && len(ee.Stderr) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(ee.Stderr)))
	}
	return err
}

// gitRun runs git in dir with output streamed to the terminal.
func gitRun(dir string, args ...string) error {
	return runCommand(dir, append([]string{"git"}, args...))
}

// runCommand runs argv in dir with output streamed to the terminal.
func runCommand(dir string, argv []string) error {
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// showFile returns the contents of path at ref.
func showFile(dir, ref, path string) (string, error) {
	return gitOutput(dir, "show", ref+":"+path)
}

// refExists reports whether ref resolves. rev-parse exits 1 for a
// missing ref and something else for a real failure, so a broken repo
// is not silently reported as an absent ref.
func refExists(dir, ref string) (bool, error) {
	_, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", ref)
	if err == nil {
		return true, nil
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && ee.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// isClean reports whether the working tree has no uncommitted changes.
func isClean(dir string) (bool, error) {
	out, err := gitOutput(dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out == "", nil
}

// remoteBranchExists reports whether remote/branch is present after fetch.
func remoteBranchExists(dir, remote, branch string) (bool, error) {
	return refExists(dir, "refs/remotes/"+remote+"/"+branch)
}

// tagExists reports whether tag is present locally.
func tagExists(dir, tag string) (bool, error) {
	return refExists(dir, "refs/tags/"+tag)
}

// remoteTagExists reports whether tag is published on remote. This is
// the authoritative check. A local tag can come from a fork that
// upstream never released.
func remoteTagExists(dir, remote, tag string) (bool, error) {
	out, err := gitOutput(dir, "ls-remote", "--tags", "--refs", remote, "refs/tags/"+tag)
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// describePreceding returns the nearest preceding release tag reachable
// from ref. This is what the Makefile resolves BUILD_REF to, so before
// the release tag exists it names an earlier release. An empty result
// means no release tag is reachable, which is not an error.
func describePreceding(dir, ref string) string {
	out, err := gitOutput(dir, "describe", "--tags", "--match", "v[0-9]*", "--abbrev=0", ref)
	if err != nil {
		return ""
	}
	return out
}
