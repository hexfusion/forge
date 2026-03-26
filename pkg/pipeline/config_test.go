package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "pipelines.yaml")

	content := `
instances:
  test-instance:
    description: "test pipeline"
    status: active
    repos:
      repo-a:
        fork: user/repo-a
        branch: feat/test
        local: /tmp/repo-a
      repo-b:
        fork: user/repo-b
        branch: feat/test
        local: /tmp/repo-b
    images:
      myimage: registry.example.com/myimage:test
    replace_directives:
      - source: repo-a
        target: repo-b
        go_mod_line: "replace example.com/repo-b => ../repo-b"
`
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if len(cfg.Instances) != 1 {
		t.Fatalf("expected 1 instance, got %d", len(cfg.Instances))
	}

	inst, err := cfg.GetInstance("test-instance")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	if inst.Status != "active" {
		t.Errorf("status = %q, want %q", inst.Status, "active")
	}

	if len(inst.Repos) != 2 {
		t.Errorf("repos = %d, want 2", len(inst.Repos))
	}

	if inst.Repos["repo-a"].Branch != "feat/test" {
		t.Errorf("repo-a branch = %q, want %q", inst.Repos["repo-a"].Branch, "feat/test")
	}

	if len(inst.ReplaceDirectives) != 1 {
		t.Errorf("replace directives = %d, want 1", len(inst.ReplaceDirectives))
	}

	if inst.Images["myimage"] != "registry.example.com/myimage:test" {
		t.Errorf("image = %q", inst.Images["myimage"])
	}
}

func TestGetInstanceNotFound(t *testing.T) {
	cfg := &Config{
		Instances: map[string]*Instance{
			"exists": {Status: "active"},
		},
	}

	_, err := cfg.GetInstance("nonexistent")
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestActiveInstances(t *testing.T) {
	cfg := &Config{
		Instances: map[string]*Instance{
			"active-one":  {Status: "active"},
			"active-two":  {Status: "active"},
			"paused":      {Status: "paused"},
			"closed":      {Status: "closed"},
		},
	}

	active := cfg.ActiveInstances()
	if len(active) != 2 {
		t.Errorf("active instances = %d, want 2", len(active))
	}
}

func TestExpandHome(t *testing.T) {
	result := expandHome("~/foo/bar")
	if result == "~/foo/bar" {
		t.Error("~ was not expanded")
	}
	if result == "" {
		t.Error("result is empty")
	}

	// Non-home paths should be unchanged
	result = expandHome("/absolute/path")
	if result != "/absolute/path" {
		t.Errorf("absolute path changed: %q", result)
	}
}
