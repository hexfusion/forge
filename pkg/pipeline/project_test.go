package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- WorktreeDir ---

func TestWorktreeDir(t *testing.T) {
	tests := []struct {
		name         string
		repoLocal    string
		repoName     string
		instanceName string
		want         string
	}{
		{
			name:         "standard layout",
			repoLocal:    "/home/user/projects/llm-d/scheduler",
			repoName:     "scheduler",
			instanceName: "orca-metrics",
			want:         "/home/user/projects/llm-d/.worktrees/scheduler/orca-metrics",
		},
		{
			name:         "nested path",
			repoLocal:    "/tmp/deep/nested/repo",
			repoName:     "repo",
			instanceName: "feat-1",
			want:         "/tmp/deep/nested/.worktrees/repo/feat-1",
		},
		{
			name:         "instance name with hyphens",
			repoLocal:    "/tmp/repo",
			repoName:     "repo",
			instanceName: "my-long-feature-name",
			want:         "/tmp/.worktrees/repo/my-long-feature-name",
		},
		{
			name:         "root-level repo",
			repoLocal:    "/repo",
			repoName:     "repo",
			instanceName: "test",
			want:         "/.worktrees/repo/test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WorktreeDir(tt.repoLocal, tt.repoName, tt.instanceName)
			if got != tt.want {
				t.Errorf("WorktreeDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

// --- ResolveInstance ---

func TestResolveInstance(t *testing.T) {
	tests := []struct {
		name         string
		project      *Project
		instanceName string
		targetRepos  []string
		// expected
		wantErr         bool
		errContains     string
		wantRepoCount   int
		wantReplaces    int
		wantBranch      string
		wantWorktreeIn  map[string]string // repoName -> expected substring in Local path
		wantReplaceFrom string
		wantReplaceTo   string
		wantGoModLine   string
		wantImageCount  int
	}{
		{
			name:         "two repos with go-module dependency",
			project:      testProjectFull(),
			instanceName: "my-feature",
			targetRepos:  []string{"scheduler", "framework"},
			wantRepoCount:   2,
			wantReplaces:    1,
			wantBranch:      "feat/my-feature",
			wantWorktreeIn:  map[string]string{"scheduler": ".worktrees/scheduler/my-feature"},
			wantReplaceFrom: "scheduler",
			wantReplaceTo:   "framework",
			wantGoModLine:   "replace example.com/framework => ../../framework/my-feature",
		},
		{
			name:         "all three repos — two replace directives",
			project:      testProjectFull(),
			instanceName: "all",
			targetRepos:  []string{"scheduler", "framework", "cache"},
			wantRepoCount: 3,
			wantReplaces:  2,
			wantBranch:    "feat/all",
		},
		{
			name:         "single repo — no dependencies triggered",
			project:      testProjectFull(),
			instanceName: "single",
			targetRepos:  []string{"framework"},
			wantRepoCount: 1,
			wantReplaces:  0,
			wantBranch:    "feat/single",
		},
		{
			name:         "dependency only one side targeted — no replace",
			project:      testProjectFull(),
			instanceName: "one-side",
			targetRepos:  []string{"cache"},
			wantRepoCount: 1,
			wantReplaces:  0,
		},
		{
			name:         "unknown repo",
			project:      testProjectFull(),
			instanceName: "bad",
			targetRepos:  []string{"nonexistent"},
			wantErr:      true,
			errContains:  "not found",
		},
		{
			name: "empty project — unknown repo",
			project: &Project{
				Name:  "empty",
				Repos: map[string]*ProjectRepo{},
			},
			instanceName: "test",
			targetRepos:  []string{"anything"},
			wantErr:      true,
			errContains:  "not found",
		},
		{
			name:         "repo with image — image tag generated",
			project:      testProjectFull(),
			instanceName: "img-test",
			targetRepos:  []string{"scheduler"},
			wantRepoCount:  1,
			wantImageCount: 1,
		},
		{
			name: "build dependency pulls in builder repo",
			project: &Project{
				Name: "build-dep",
				Repos: map[string]*ProjectRepo{
					"lib":     {Fork: "u/lib", Local: "/tmp/lib"},
					"builder": {Fork: "u/builder", Local: "/tmp/builder", Images: map[string]*ImageDef{"img": {Registry: "r.io"}}},
				},
				Dependencies: []Dependency{
					{From: "builder", To: "lib", Type: "build"},
				},
			},
			instanceName:   "auto-pull",
			targetRepos:    []string{"lib"},
			wantRepoCount:  2, // lib + builder pulled in
			wantImageCount: 1,
		},
		{
			name:         "defaults applied — deploy config",
			project:      testProjectFull(),
			instanceName: "with-defaults",
			targetRepos:  []string{"scheduler"},
			wantRepoCount: 1,
		},
		{
			name: "no defaults — deploy is nil",
			project: &Project{
				Name: "no-defaults",
				Repos: map[string]*ProjectRepo{
					"a": {Fork: "u/a", Local: "/tmp/a"},
				},
			},
			instanceName:  "plain",
			targetRepos:   []string{"a"},
			wantRepoCount: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := tt.project.ResolveInstance(tt.instanceName, tt.targetRepos)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if inst.Status != "active" {
				t.Errorf("status = %q, want %q", inst.Status, "active")
			}

			if len(inst.Repos) != tt.wantRepoCount {
				t.Errorf("repo count = %d, want %d", len(inst.Repos), tt.wantRepoCount)
			}

			if len(inst.ReplaceDirectives) != tt.wantReplaces {
				t.Errorf("replace count = %d, want %d", len(inst.ReplaceDirectives), tt.wantReplaces)
			}

			if tt.wantBranch != "" {
				for repoName, repo := range inst.Repos {
					if repo.Branch != tt.wantBranch {
						t.Errorf("repo %s branch = %q, want %q", repoName, repo.Branch, tt.wantBranch)
					}
				}
			}

			for repoName, wantSubstr := range tt.wantWorktreeIn {
				repo := inst.Repos[repoName]
				if repo == nil {
					t.Errorf("repo %s not found in instance", repoName)
					continue
				}
				if !strings.Contains(repo.Local, wantSubstr) {
					t.Errorf("repo %s local = %q, want containing %q", repoName, repo.Local, wantSubstr)
				}
			}

			if tt.wantReplaceFrom != "" && len(inst.ReplaceDirectives) > 0 {
				rd := inst.ReplaceDirectives[0]
				if rd.Source != tt.wantReplaceFrom {
					t.Errorf("replace source = %q, want %q", rd.Source, tt.wantReplaceFrom)
				}
				if rd.Target != tt.wantReplaceTo {
					t.Errorf("replace target = %q, want %q", rd.Target, tt.wantReplaceTo)
				}
				if tt.wantGoModLine != "" && rd.GoModLine != tt.wantGoModLine {
					t.Errorf("go_mod_line = %q, want %q", rd.GoModLine, tt.wantGoModLine)
				}
			}

			if tt.wantImageCount > 0 && len(inst.Images) != tt.wantImageCount {
				t.Errorf("image count = %d, want %d", len(inst.Images), tt.wantImageCount)
			}

			// Verify all Local paths are worktree paths (contain .worktrees)
			for repoName, repo := range inst.Repos {
				if !strings.Contains(repo.Local, ".worktrees") {
					t.Errorf("repo %s local = %q, expected worktree path containing .worktrees", repoName, repo.Local)
				}
			}
		})
	}
}

func TestResolveInstanceWorktreePathIsolation(t *testing.T) {
	// Two instances from the same project should get different worktree paths
	project := testProjectFull()

	inst1, err := project.ResolveInstance("feature-a", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("ResolveInstance feature-a: %v", err)
	}

	inst2, err := project.ResolveInstance("feature-b", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("ResolveInstance feature-b: %v", err)
	}

	for repoName := range inst1.Repos {
		path1 := inst1.Repos[repoName].Local
		path2 := inst2.Repos[repoName].Local
		if path1 == path2 {
			t.Errorf("repo %s: both instances got same path %q — worktrees should be isolated", repoName, path1)
		}
		if !strings.Contains(path1, "feature-a") {
			t.Errorf("repo %s instance 1: path %q should contain instance name", repoName, path1)
		}
		if !strings.Contains(path2, "feature-b") {
			t.Errorf("repo %s instance 2: path %q should contain instance name", repoName, path2)
		}
	}

	// Branches should also differ
	if inst1.Repos["scheduler"].Branch == inst2.Repos["scheduler"].Branch {
		t.Error("both instances got same branch name")
	}
}

func TestResolveInstanceReplaceDirectiveSymmetry(t *testing.T) {
	// If A depends on B AND B depends on A, both replaces should be generated
	project := &Project{
		Name: "bidirectional",
		Repos: map[string]*ProjectRepo{
			"a": {Fork: "u/a", Module: "ex.com/a", Local: "/tmp/a"},
			"b": {Fork: "u/b", Module: "ex.com/b", Local: "/tmp/b"},
		},
		Dependencies: []Dependency{
			{From: "a", To: "b", Type: "go-module"},
			{From: "b", To: "a", Type: "go-module"},
		},
	}

	inst, err := project.ResolveInstance("bidi", []string{"a", "b"})
	if err != nil {
		t.Fatalf("ResolveInstance: %v", err)
	}

	if len(inst.ReplaceDirectives) != 2 {
		t.Fatalf("replace directives = %d, want 2 (bidirectional)", len(inst.ReplaceDirectives))
	}

	sources := map[string]bool{}
	for _, rd := range inst.ReplaceDirectives {
		sources[rd.Source] = true
	}
	if !sources["a"] || !sources["b"] {
		t.Errorf("expected both a and b as replace sources, got: %v", sources)
	}
}

// --- LoadProject ---

func TestLoadProject(t *testing.T) {
	tests := []struct {
		name        string
		projectName string
		setup       func(t *testing.T) string // returns temp dir to set as FORGE_PROJECTS_DIR
		wantErr     bool
		errContains string
		wantRepos   int
	}{
		{
			name:        "valid project file",
			projectName: "test-proj",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				data := `name: test-proj
repos:
  repo-a:
    upstream: org/repo-a
    fork: user/repo-a
    module: example.com/repo-a
    local: /tmp/repo-a
  repo-b:
    upstream: org/repo-b
    fork: user/repo-b
    local: /tmp/repo-b
`
				if err := os.WriteFile(filepath.Join(dir, "test-proj.yaml"), []byte(data), 0644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantRepos: 2,
		},
		{
			name:        "project not found",
			projectName: "nonexistent",
			setup: func(t *testing.T) string {
				return t.TempDir() // empty dir
			},
			wantErr:     true,
			errContains: "not found",
		},
		{
			name:        "invalid YAML",
			projectName: "broken",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "broken.yaml"), []byte("{{invalid"), 0644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantErr:     true,
			errContains: "parsing",
		},
		{
			name:        "empty file parses to zero repos",
			projectName: "empty",
			setup: func(t *testing.T) string {
				dir := t.TempDir()
				if err := os.WriteFile(filepath.Join(dir, "empty.yaml"), []byte("name: empty\n"), 0644); err != nil {
					t.Fatal(err)
				}
				return dir
			},
			wantRepos: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			t.Setenv("FORGE_PROJECTS_DIR", dir)

			project, err := LoadProject(tt.projectName)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if len(project.Repos) != tt.wantRepos {
				t.Errorf("repo count = %d, want %d", len(project.Repos), tt.wantRepos)
			}
		})
	}
}

func TestLoadProjectExpandsHome(t *testing.T) {
	dir := t.TempDir()
	data := `name: tilde-test
repos:
  myrepo:
    fork: u/myrepo
    local: ~/projects/myrepo
`
	if err := os.WriteFile(filepath.Join(dir, "tilde-test.yaml"), []byte(data), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGE_PROJECTS_DIR", dir)

	project, err := LoadProject("tilde-test")
	if err != nil {
		t.Fatalf("LoadProject: %v", err)
	}

	local := project.Repos["myrepo"].Local
	if strings.HasPrefix(local, "~/") {
		t.Errorf("local path not expanded: %q", local)
	}
	if !strings.HasSuffix(local, "/projects/myrepo") {
		t.Errorf("local path unexpected: %q", local)
	}
}

// --- test helpers ---

func testProjectFull() *Project {
	return &Project{
		Name: "test-project",
		Repos: map[string]*ProjectRepo{
			"scheduler": {
				Upstream: "org/scheduler",
				Fork:     "user/scheduler",
				Module:   "example.com/scheduler",
				Local:    "/tmp/scheduler",
				Images: map[string]*ImageDef{
					"epp": {BuildFile: "Dockerfile.epp", Registry: "registry.example.com", NameOverride: "llm-d-epp"},
				},
			},
			"framework": {
				Upstream: "org/framework",
				Fork:     "user/framework",
				Module:   "example.com/framework",
				Local:    "/tmp/framework",
			},
			"cache": {
				Upstream: "org/cache",
				Fork:     "user/cache",
				Module:   "example.com/cache",
				Local:    "/tmp/cache",
			},
		},
		Dependencies: []Dependency{
			{From: "scheduler", To: "framework", Type: "go-module"},
			{From: "scheduler", To: "cache", Type: "go-module"},
		},
		Defaults: &ProjectDefaults{
			ImageRegistry: "registry.example.com",
			Deploy: &DeployConfig{
				KubeContext:   "test-context",
				Namespace:     "default",
				EPPDeployment: "test-epp",
			},
		},
	}
}
