package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// --- SaveState / LoadState round-trip ---

func TestSaveAndLoadState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	now := time.Now().Truncate(time.Second)
	state := &InstanceState{
		Name:        "test-state",
		Project:     "test-project",
		Status:      "active",
		Description: "test instance",
		Created:     now,
		Repos: map[string]*RepoState{
			"repo-a": {
				Fork:   "user/repo-a",
				Branch: "feat/test",
				Local:  "/tmp/repo-a",
			},
		},
		Images: map[string]*ImageState{
			"myimage": {
				Tag:    "registry.example.com/myimage:test",
				Digest: "sha256:abc123",
				Pushed: true,
			},
		},
		ReplaceDirectives: []ReplaceDirective{
			{Source: "repo-a", Target: "repo-b", GoModLine: "replace ex.com/b => ../b"},
		},
		Proposal: "path/to/PROPOSAL.md",
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	// Verify the file exists
	path := filepath.Join(dir, "instances", "test-state.yaml")
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("state file not created")
	}

	// Load it back
	loaded, err := LoadState("test-state")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Name != "test-state" {
		t.Errorf("name = %q", loaded.Name)
	}
	if loaded.Project != "test-project" {
		t.Errorf("project = %q", loaded.Project)
	}
	if loaded.Status != "active" {
		t.Errorf("status = %q", loaded.Status)
	}
	if loaded.Description != "test instance" {
		t.Errorf("description = %q", loaded.Description)
	}
	if len(loaded.Repos) != 1 {
		t.Errorf("repos = %d, want 1", len(loaded.Repos))
	}
	if loaded.Repos["repo-a"].Branch != "feat/test" {
		t.Errorf("branch = %q", loaded.Repos["repo-a"].Branch)
	}

	img := loaded.Images["myimage"]
	if img == nil {
		t.Fatal("image not found")
	}
	if img.Digest != "sha256:abc123" {
		t.Errorf("digest = %q", img.Digest)
	}
	if !img.Pushed {
		t.Error("pushed = false, want true")
	}
	if len(loaded.ReplaceDirectives) != 1 {
		t.Error("replace directives not preserved")
	}
	if loaded.Proposal != "path/to/PROPOSAL.md" {
		t.Errorf("proposal = %q", loaded.Proposal)
	}
}

func TestSaveAndLoadStateWithDeploy(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	now := time.Now().Truncate(time.Second)
	state := &InstanceState{
		Name:   "deploy-test",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
		Deploy: &DeployState{
			KubeContext:    "my-cluster",
			Namespace:      "prod",
			Deployment:     "my-epp",
			DeployedDigest: "sha256:deployed",
			DeployTime:     &now,
			DeployCommits:  map[string]string{"repo-a": "abc123"},
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState("deploy-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Deploy == nil {
		t.Fatal("deploy is nil after round-trip")
	}
	if loaded.Deploy.KubeContext != "my-cluster" {
		t.Errorf("kube_context = %q", loaded.Deploy.KubeContext)
	}
	if loaded.Deploy.DeployedDigest != "sha256:deployed" {
		t.Errorf("deployed_digest = %q", loaded.Deploy.DeployedDigest)
	}
	if loaded.Deploy.DeployCommits["repo-a"] != "abc123" {
		t.Error("deploy_commits not preserved")
	}
}

func TestSaveAndLoadStateMinimal(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	state := &InstanceState{
		Name:   "minimal",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState("minimal")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Deploy != nil {
		t.Error("expected nil deploy on minimal state")
	}
	if loaded.Name != "minimal" {
		t.Errorf("name = %q", loaded.Name)
	}
}

func TestSaveStateCreatesDirectory(t *testing.T) {
	dir := t.TempDir()
	// Point to a non-existent subdirectory
	t.Setenv("FORGE_STATE_DIR", filepath.Join(dir, "deep", "nested"))

	state := &InstanceState{
		Name:   "auto-dir",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState should create dirs: %v", err)
	}

	// Verify it was actually saved
	loaded, err := LoadState("auto-dir")
	if err != nil {
		t.Fatalf("LoadState after auto-dir creation: %v", err)
	}
	if loaded.Name != "auto-dir" {
		t.Errorf("name = %q", loaded.Name)
	}
}

// --- LoadState errors ---

func TestLoadStateNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	_, err := LoadState("does-not-exist")
	if err == nil {
		t.Fatal("expected error for missing state file")
	}
	if !strings.Contains(err.Error(), "does-not-exist") {
		t.Errorf("error should mention instance name: %v", err)
	}
}

// --- ListStates ---

func TestListStates(t *testing.T) {
	tests := []struct {
		name       string
		stateNames []string
		wantCount  int
	}{
		{name: "two states", stateNames: []string{"a", "b"}, wantCount: 2},
		{name: "one state", stateNames: []string{"single"}, wantCount: 1},
		{name: "no states", stateNames: nil, wantCount: 0},
		{name: "five states", stateNames: []string{"a", "b", "c", "d", "e"}, wantCount: 5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("FORGE_STATE_DIR", dir)

			for _, name := range tt.stateNames {
				state := &InstanceState{
					Name:   name,
					Status: "active",
					Repos:  map[string]*RepoState{},
					Images: map[string]*ImageState{},
				}
				if err := SaveState(state); err != nil {
					t.Fatalf("SaveState(%s): %v", name, err)
				}
			}

			states, err := ListStates()
			if err != nil {
				t.Fatalf("ListStates: %v", err)
			}
			if len(states) != tt.wantCount {
				t.Errorf("count = %d, want %d", len(states), tt.wantCount)
			}
		})
	}
}

func TestListStatesIgnoresNonYAML(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	instanceDir := filepath.Join(dir, "instances")
	if err := os.MkdirAll(instanceDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Write a valid state
	state := &InstanceState{
		Name: "valid", Status: "active",
		Repos: map[string]*RepoState{}, Images: map[string]*ImageState{},
	}
	if err := SaveState(state); err != nil {
		t.Fatal(err)
	}

	// Write non-YAML files that should be ignored
	for _, name := range []string{"notes.txt", "backup.bak", ".hidden"} {
		if err := os.WriteFile(filepath.Join(instanceDir, name), []byte("junk"), 0644); err != nil {
			t.Fatal(err)
		}
	}

	states, err := ListStates()
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != 1 {
		t.Errorf("count = %d, want 1 (should ignore non-YAML)", len(states))
	}
}

// --- CheckDrift ---

func TestCheckDrift(t *testing.T) {
	tests := []struct {
		name           string
		state          *InstanceState
		wantBuilt      bool
		wantPushed     bool
		wantDeployed   bool
		wantDeployStale bool
	}{
		{
			name: "no deploy state",
			state: &InstanceState{
				Name:   "test",
				Repos:  map[string]*RepoState{},
				Images: map[string]*ImageState{},
			},
			wantDeployed: false,
		},
		{
			name: "built but not pushed",
			state: &InstanceState{
				Name:  "test",
				Repos: map[string]*RepoState{},
				Images: map[string]*ImageState{
					"img": {Digest: "sha256:abc", Pushed: false},
				},
			},
			wantBuilt:  true,
			wantPushed: false,
		},
		{
			name: "built and pushed",
			state: &InstanceState{
				Name:  "test",
				Repos: map[string]*RepoState{},
				Images: map[string]*ImageState{
					"img": {Digest: "sha256:abc", Pushed: true},
				},
			},
			wantBuilt:  true,
			wantPushed: true,
		},
		{
			name: "stale deploy — digest mismatch",
			state: &InstanceState{
				Name:  "test",
				Repos: map[string]*RepoState{},
				Images: map[string]*ImageState{
					"img": {Digest: "sha256:new"},
				},
				Deploy: &DeployState{
					DeployedDigest: "sha256:old",
					KubeContext:    "nonexistent",
					Deployment:     "test",
				},
			},
			wantBuilt:       true,
			wantDeployed:    true,
			wantDeployStale: true,
		},
		{
			name: "current deploy — digests match",
			state: &InstanceState{
				Name:  "test",
				Repos: map[string]*RepoState{},
				Images: map[string]*ImageState{
					"img": {Digest: "sha256:same"},
				},
				Deploy: &DeployState{
					DeployedDigest: "sha256:same",
					KubeContext:    "nonexistent",
					Deployment:     "test",
				},
			},
			wantBuilt:       true,
			wantDeployed:    true,
			wantDeployStale: false,
		},
		{
			name: "no images built — not built",
			state: &InstanceState{
				Name:   "test",
				Repos:  map[string]*RepoState{},
				Images: map[string]*ImageState{},
			},
			wantBuilt: false,
		},
		{
			name: "image with empty digest — not built",
			state: &InstanceState{
				Name:  "test",
				Repos: map[string]*RepoState{},
				Images: map[string]*ImageState{
					"img": {Digest: "", Pushed: false},
				},
			},
			wantBuilt: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			drift, err := tt.state.CheckDrift()
			if err != nil {
				t.Fatalf("CheckDrift: %v", err)
			}
			if drift.Built != tt.wantBuilt {
				t.Errorf("built = %v, want %v", drift.Built, tt.wantBuilt)
			}
			if drift.Pushed != tt.wantPushed {
				t.Errorf("pushed = %v, want %v", drift.Pushed, tt.wantPushed)
			}
			if drift.Deployed != tt.wantDeployed {
				t.Errorf("deployed = %v, want %v", drift.Deployed, tt.wantDeployed)
			}
			if drift.DeployStale != tt.wantDeployStale {
				t.Errorf("deployStale = %v, want %v", drift.DeployStale, tt.wantDeployStale)
			}
		})
	}
}

// --- helper functions ---

func TestStartsWith(t *testing.T) {
	tests := []struct {
		s, prefix string
		want      bool
	}{
		{"abc123", "abc", true},
		{"abc123", "abc123", true},
		{"abc123", "abc1234", false},
		{"", "", true},
		{"a", "", true},
		{"", "a", false},
	}

	for _, tt := range tests {
		got := startsWith(tt.s, tt.prefix)
		if got != tt.want {
			t.Errorf("startsWith(%q, %q) = %v, want %v", tt.s, tt.prefix, got, tt.want)
		}
	}
}

func TestTruncateDigest(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"sha256:abcdef1234567890abcdef", "sha256:abcdef123456..."},
		{"short", "short"},
		{"exactly-nineteen!", "exactly-nineteen!"},
		{"", ""},
	}

	for _, tt := range tests {
		got := truncateDigest(tt.input)
		if got != tt.want {
			t.Errorf("truncateDigest(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
