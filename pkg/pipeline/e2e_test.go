package pipeline

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// --- e2e test helpers ---

// testGitRepo creates a bare git repo and a clone of it in temp dirs.
// Returns the clone path (the "local" checkout).
func testGitRepo(t *testing.T, baseDir, name string) string {
	t.Helper()

	// Create a bare "upstream" repo
	bare := filepath.Join(baseDir, name+".git")
	if err := os.MkdirAll(bare, 0755); err != nil {
		t.Fatal(err)
	}
	mustGit(t, bare, "init", "--bare")

	// Clone it into the local path
	local := filepath.Join(baseDir, name)
	mustGit(t, baseDir, "clone", bare, local)

	// Create an initial commit so branches can be created
	dummy := filepath.Join(local, "README.md")
	if err := os.WriteFile(dummy, []byte("# "+name+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, local, "add", ".")
	mustGit(t, local, "commit", "-m", "initial commit")

	// Add the bare repo as "upstream" remote (simulating fork workflow)
	mustGit(t, local, "remote", "rename", "origin", "upstream")

	return local
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test",
		"GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test",
		"GIT_COMMITTER_EMAIL=test@test.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// testProject creates a Project with real git repos in temp dirs.
func testProject(t *testing.T) (*Project, string) {
	t.Helper()
	baseDir := t.TempDir()

	schedulerLocal := testGitRepo(t, baseDir, "scheduler")
	frameworkLocal := testGitRepo(t, baseDir, "framework")

	// Create go.mod in scheduler so go mod edit works
	goMod := "module example.com/scheduler\n\ngo 1.22\n"
	if err := os.WriteFile(filepath.Join(schedulerLocal, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, schedulerLocal, "add", "go.mod")
	mustGit(t, schedulerLocal, "commit", "-m", "add go.mod")
	mustGit(t, schedulerLocal, "push", "upstream", "main")

	// Push framework too
	mustGit(t, frameworkLocal, "push", "upstream", "main")

	project := &Project{
		Name: "test-project",
		Repos: map[string]*ProjectRepo{
			"scheduler": {
				Upstream: "org/scheduler",
				Fork:     "user/scheduler",
				Module:   "example.com/scheduler",
				Local:    schedulerLocal,
			},
			"framework": {
				Upstream: "org/framework",
				Fork:     "user/framework",
				Module:   "example.com/framework",
				Local:    frameworkLocal,
			},
		},
		Dependencies: []Dependency{
			{From: "scheduler", To: "framework", Type: "go-module"},
		},
		Defaults: &ProjectDefaults{
			ImageRegistry: "localhost:5001",
			Deploy: &DeployConfig{
				KubeContext:   "test-ctx",
				Namespace:     "default",
				EPPDeployment: "test-epp",
			},
		},
	}

	return project, baseDir
}

// writeProjectFile writes a Project as YAML and sets FORGE_PROJECTS_DIR.
func writeProjectFile(t *testing.T, project *Project) {
	t.Helper()
	dir := t.TempDir()
	data := "name: " + project.Name + "\nrepos:\n"
	for name, repo := range project.Repos {
		data += "  " + name + ":\n"
		data += "    upstream: " + repo.Upstream + "\n"
		data += "    fork: " + repo.Fork + "\n"
		if repo.Module != "" {
			data += "    module: " + repo.Module + "\n"
		}
		data += "    local: " + repo.Local + "\n"
	}
	if len(project.Dependencies) > 0 {
		data += "dependencies:\n"
		for _, dep := range project.Dependencies {
			data += "  - from: " + dep.From + "\n"
			data += "    to: " + dep.To + "\n"
			data += "    type: " + dep.Type + "\n"
		}
	}
	if project.Defaults != nil {
		data += "defaults:\n"
		if project.Defaults.ImageRegistry != "" {
			data += "  image_registry: " + project.Defaults.ImageRegistry + "\n"
		}
		if project.Defaults.Deploy != nil {
			data += "  deploy:\n"
			data += "    kube_context: " + project.Defaults.Deploy.KubeContext + "\n"
			data += "    namespace: " + project.Defaults.Deploy.Namespace + "\n"
			data += "    epp_deployment: " + project.Defaults.Deploy.EPPDeployment + "\n"
		}
	}

	if err := os.WriteFile(filepath.Join(dir, project.Name+".yaml"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGE_PROJECTS_DIR", dir)
}

// writeEmptyConfig writes a minimal config with no instances and sets it up.
func writeEmptyConfig(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "pipelines.yaml")
	if err := os.WriteFile(path, []byte("instances: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGE_PIPELINE_CONFIG", path)
	return path
}

// --- CREATE tests ---

func TestCreateInstance_E2E(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	configPath := writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	err := createInstance("test-project", "test-feature", "e2e test instance", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Verify worktrees exist
	for _, repoName := range []string{"scheduler", "framework"} {
		wtPath := WorktreeDir(project.Repos[repoName].Local, repoName, "test-feature")
		if _, err := os.Stat(wtPath); os.IsNotExist(err) {
			t.Errorf("worktree not created: %s", wtPath)
		}

		// Verify it's on the right branch
		branch := mustGit(t, wtPath, "branch", "--show-current")
		if branch != "feat/test-feature" {
			t.Errorf("repo %s branch = %q, want feat/test-feature", repoName, branch)
		}
	}

	// Verify state file was created
	state, err := LoadState("test-feature")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Status != "active" {
		t.Errorf("state status = %q", state.Status)
	}
	if state.Description != "e2e test instance" {
		t.Errorf("state description = %q", state.Description)
	}
	if len(state.Repos) != 2 {
		t.Errorf("state repos = %d, want 2", len(state.Repos))
	}

	// Verify config was updated
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	inst, err := cfg.GetInstance("test-feature")
	if err != nil {
		t.Fatalf("instance not in config: %v", err)
	}
	if inst.Status != "active" {
		t.Errorf("config status = %q", inst.Status)
	}
}

func TestCreateInstance_DuplicateFails(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create first time
	if err := createInstance("test-project", "dupe-test", "", []string{"scheduler"}); err != nil {
		t.Fatalf("first create: %v", err)
	}

	// Create same name again — should fail
	err := createInstance("test-project", "dupe-test", "", []string{"scheduler"})
	if err == nil {
		t.Fatal("expected error for duplicate instance")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want 'already exists'", err.Error())
	}
}

func TestCreateInstance_UnknownProject(t *testing.T) {
	t.Setenv("FORGE_PROJECTS_DIR", t.TempDir())
	writeEmptyConfig(t)

	err := createInstance("nonexistent", "test", "", []string{"a"})
	if err == nil {
		t.Fatal("expected error for unknown project")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCreateInstance_UnknownRepo(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	t.Setenv("FORGE_STATE_DIR", t.TempDir())

	err := createInstance("test-project", "bad-repo", "", []string{"nonexistent"})
	if err == nil {
		t.Fatal("expected error for unknown repo")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestCreateInstance_ReplaceDirectiveInjected(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	err := createInstance("test-project", "replace-test", "", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Read go.mod from the scheduler worktree
	schedulerWT := WorktreeDir(project.Repos["scheduler"].Local, "scheduler", "replace-test")
	goModPath := filepath.Join(schedulerWT, "go.mod")
	data, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}

	goMod := string(data)
	if !strings.Contains(goMod, "replace") {
		t.Errorf("go.mod missing replace directive:\n%s", goMod)
	}
	if !strings.Contains(goMod, "example.com/framework") {
		t.Errorf("go.mod replace doesn't reference framework module:\n%s", goMod)
	}
}

// --- READ (status) tests ---

func TestStatusInstance_FromConfig(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create an instance first
	if err := createInstance("test-project", "status-test", "status check", []string{"scheduler"}); err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// statusInstance should not error
	cfg, _ := LoadConfig("")
	if err := statusInstance(cfg, "status-test"); err != nil {
		t.Fatalf("statusInstance: %v", err)
	}
}

func TestStatusInstanceYAML_IncludesLocalPaths(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	configPath := writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	if err := createInstance("test-project", "yaml-test", "", []string{"scheduler"}); err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Capture YAML output by calling the config directly
	cfg, _ := LoadConfig(configPath)
	inst, _ := cfg.GetInstance("yaml-test")

	// Verify the instance has a local path with .worktrees
	for _, repo := range inst.Repos {
		if !strings.Contains(repo.Local, ".worktrees") {
			t.Errorf("YAML local path should contain .worktrees: %q", repo.Local)
		}
	}
}

func TestStatusInstance_NotFound(t *testing.T) {
	writeEmptyConfig(t)
	t.Setenv("FORGE_STATE_DIR", t.TempDir())

	cfg, _ := LoadConfig("")
	err := statusInstance(cfg, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

// --- DESTROY tests ---

func TestDestroyInstance_E2E(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	configPath := writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create
	if err := createInstance("test-project", "destroy-test", "", []string{"scheduler", "framework"}); err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Verify worktrees exist before destroy
	schedulerWT := WorktreeDir(project.Repos["scheduler"].Local, "scheduler", "destroy-test")
	frameworkWT := WorktreeDir(project.Repos["framework"].Local, "framework", "destroy-test")
	for _, wt := range []string{schedulerWT, frameworkWT} {
		if _, err := os.Stat(wt); os.IsNotExist(err) {
			t.Fatalf("worktree should exist before destroy: %s", wt)
		}
	}

	// Destroy with --force
	if err := destroyInstance("destroy-test", true); err != nil {
		t.Fatalf("destroyInstance: %v", err)
	}

	// Verify worktrees are gone
	for _, wt := range []string{schedulerWT, frameworkWT} {
		if _, err := os.Stat(wt); !os.IsNotExist(err) {
			t.Errorf("worktree should be gone after destroy: %s", wt)
		}
	}

	// Verify state file is gone
	_, err := LoadState("destroy-test")
	if err == nil {
		t.Error("state file should be deleted after destroy")
	}

	// Verify removed from config
	cfg, _ := LoadConfig(configPath)
	_, err = cfg.GetInstance("destroy-test")
	if err == nil {
		t.Error("instance should be removed from config after destroy")
	}
}

func TestDestroyInstance_NotFound(t *testing.T) {
	writeEmptyConfig(t)
	t.Setenv("FORGE_STATE_DIR", t.TempDir())

	err := destroyInstance("nonexistent", true)
	if err == nil {
		t.Fatal("expected error for destroying nonexistent instance")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestDestroyInstance_DryRunWithoutForce(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	if err := createInstance("test-project", "dryrun-test", "", []string{"scheduler"}); err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Destroy WITHOUT force — should print plan but not actually destroy
	if err := destroyInstance("dryrun-test", false); err != nil {
		t.Fatalf("destroyInstance (dry run): %v", err)
	}

	// Worktree should still exist
	schedulerWT := WorktreeDir(project.Repos["scheduler"].Local, "scheduler", "dryrun-test")
	if _, err := os.Stat(schedulerWT); os.IsNotExist(err) {
		t.Error("worktree removed without --force")
	}

	// State should still exist
	if _, err := LoadState("dryrun-test"); err != nil {
		t.Error("state deleted without --force")
	}
}

func TestDestroyInstance_Idempotent(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	if err := createInstance("test-project", "idem-test", "", []string{"scheduler"}); err != nil {
		t.Fatalf("createInstance: %v", err)
	}

	// Destroy first time
	if err := destroyInstance("idem-test", true); err != nil {
		t.Fatalf("first destroy: %v", err)
	}

	// Destroy again — should fail gracefully (not found)
	err := destroyInstance("idem-test", true)
	if err == nil {
		t.Fatal("expected error on second destroy")
	}
}

// --- FULL LIFECYCLE: create -> status -> destroy ---

func TestFullLifecycle_E2E(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	configPath := writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	instanceName := "lifecycle-e2e"

	// 1. CREATE
	err := createInstance("test-project", instanceName, "full lifecycle test", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// 2. STATUS — verify it can read back
	cfg, _ := LoadConfig(configPath)
	if err := statusInstance(cfg, instanceName); err != nil {
		t.Fatalf("status: %v", err)
	}

	// 3. STATUS -o yaml — verify all fields present
	inst, _ := cfg.GetInstance(instanceName)
	if inst.Description != "full lifecycle test" {
		t.Errorf("description = %q", inst.Description)
	}
	if len(inst.Repos) != 2 {
		t.Errorf("repos = %d, want 2", len(inst.Repos))
	}
	for _, repo := range inst.Repos {
		if !strings.Contains(repo.Local, ".worktrees") {
			t.Errorf("local should be worktree path: %q", repo.Local)
		}
	}
	if len(inst.ReplaceDirectives) != 1 {
		t.Errorf("replace directives = %d, want 1", len(inst.ReplaceDirectives))
	}

	// 4. Verify state matches
	state, err := LoadState(instanceName)
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}
	if state.Project != "test-project" {
		t.Errorf("state project = %q", state.Project)
	}

	// 5. Verify worktrees are functional (can make commits)
	schedulerWT := state.Repos["scheduler"].Local
	testFile := filepath.Join(schedulerWT, "new_file.go")
	if err := os.WriteFile(testFile, []byte("package main\n"), 0644); err != nil {
		t.Fatalf("writing to worktree: %v", err)
	}
	mustGit(t, schedulerWT, "add", "new_file.go")
	mustGit(t, schedulerWT, "commit", "-m", "test commit in worktree")

	// Verify the main checkout is unaffected
	mainLocal := project.Repos["scheduler"].Local
	mainBranch := mustGit(t, mainLocal, "branch", "--show-current")
	if mainBranch != "main" {
		t.Errorf("main checkout branch changed to %q", mainBranch)
	}
	if _, err := os.Stat(filepath.Join(mainLocal, "new_file.go")); !os.IsNotExist(err) {
		t.Error("worktree commit leaked to main checkout")
	}

	// 6. DESTROY
	if err := destroyInstance(instanceName, true); err != nil {
		t.Fatalf("destroy: %v", err)
	}

	// 7. Verify everything is cleaned up
	if _, err := os.Stat(schedulerWT); !os.IsNotExist(err) {
		t.Error("worktree still exists after destroy")
	}
	if _, err := LoadState(instanceName); err == nil {
		t.Error("state still exists after destroy")
	}
	cfg2, _ := LoadConfig(configPath)
	if _, err := cfg2.GetInstance(instanceName); err == nil {
		t.Error("config entry still exists after destroy")
	}

	// 8. Main checkout still works
	mainBranch = mustGit(t, mainLocal, "branch", "--show-current")
	if mainBranch != "main" {
		t.Errorf("main checkout branch = %q after destroy", mainBranch)
	}
}

// --- Multiple concurrent instances ---

func TestMultipleInstances_Isolation(t *testing.T) {
	project, _ := testProject(t)
	writeProjectFile(t, project)
	writeEmptyConfig(t)
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create two instances targeting the same repos
	if err := createInstance("test-project", "feature-a", "first", []string{"scheduler", "framework"}); err != nil {
		t.Fatalf("create feature-a: %v", err)
	}
	if err := createInstance("test-project", "feature-b", "second", []string{"scheduler", "framework"}); err != nil {
		t.Fatalf("create feature-b: %v", err)
	}

	// Verify they have different worktree paths
	stateA, _ := LoadState("feature-a")
	stateB, _ := LoadState("feature-b")

	for repoName := range stateA.Repos {
		pathA := stateA.Repos[repoName].Local
		pathB := stateB.Repos[repoName].Local
		if pathA == pathB {
			t.Errorf("repo %s: both instances share worktree path %q", repoName, pathA)
		}
	}

	// Verify they're on different branches
	schedulerA := stateA.Repos["scheduler"].Local
	schedulerB := stateB.Repos["scheduler"].Local
	branchA := mustGit(t, schedulerA, "branch", "--show-current")
	branchB := mustGit(t, schedulerB, "branch", "--show-current")
	if branchA == branchB {
		t.Errorf("both instances on same branch: %q", branchA)
	}

	// Write to one, verify it doesn't appear in the other
	if err := os.WriteFile(filepath.Join(schedulerA, "a_only.txt"), []byte("a"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(schedulerB, "a_only.txt")); !os.IsNotExist(err) {
		t.Error("file from instance A leaked to instance B")
	}

	// Destroy one, verify the other is unaffected
	if err := destroyInstance("feature-a", true); err != nil {
		t.Fatalf("destroy feature-a: %v", err)
	}
	if _, err := os.Stat(schedulerB); os.IsNotExist(err) {
		t.Error("destroying feature-a removed feature-b's worktree")
	}
	branchB2 := mustGit(t, schedulerB, "branch", "--show-current")
	if branchB2 != branchB {
		t.Errorf("feature-b branch changed from %q to %q after destroying feature-a", branchB, branchB2)
	}

	// Clean up
	if err := destroyInstance("feature-b", true); err != nil {
		t.Fatalf("destroy feature-b: %v", err)
	}
}
