package pipeline

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

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
		t.Errorf("name = %q, want %q", loaded.Name, "test-state")
	}
	if loaded.Project != "test-project" {
		t.Errorf("project = %q, want %q", loaded.Project, "test-project")
	}
	if loaded.Status != "active" {
		t.Errorf("status = %q, want %q", loaded.Status, "active")
	}
	if len(loaded.Repos) != 1 {
		t.Errorf("repos = %d, want 1", len(loaded.Repos))
	}
	if loaded.Repos["repo-a"].Branch != "feat/test" {
		t.Errorf("branch = %q, want %q", loaded.Repos["repo-a"].Branch, "feat/test")
	}

	img := loaded.Images["myimage"]
	if img == nil {
		t.Fatal("image not found")
	}
	if img.Digest != "sha256:abc123" {
		t.Errorf("digest = %q, want %q", img.Digest, "sha256:abc123")
	}
	if !img.Pushed {
		t.Error("pushed = false, want true")
	}
}

func TestListStates(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	// Save two states
	for _, name := range []string{"inst-a", "inst-b"} {
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

	if len(states) != 2 {
		t.Errorf("states = %d, want 2", len(states))
	}
}

func TestListStatesEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	states, err := ListStates()
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != 0 {
		t.Errorf("states = %d, want 0", len(states))
	}
}

func TestCheckDriftNoDeployment(t *testing.T) {
	state := &InstanceState{
		Name:   "test",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
	}

	drift, err := state.CheckDrift()
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}

	if drift.Deployed {
		t.Error("deployed = true, want false (no deploy state)")
	}
}

func TestCheckDriftStaleImage(t *testing.T) {
	state := &InstanceState{
		Name:  "test",
		Repos: map[string]*RepoState{},
		Images: map[string]*ImageState{
			"myimage": {
				Digest: "sha256:new-digest",
			},
		},
		Deploy: &DeployState{
			DeployedDigest: "sha256:old-digest",
			KubeContext:    "nonexistent-context",
			Deployment:     "test-deploy",
		},
	}

	drift, err := state.CheckDrift()
	if err != nil {
		t.Fatalf("CheckDrift: %v", err)
	}

	if !drift.Deployed {
		t.Error("deployed = false, want true")
	}
	if !drift.DeployStale {
		t.Error("deployStale = false, want true (digests differ)")
	}
}
