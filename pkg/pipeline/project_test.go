package pipeline

import (
	"testing"
)

func TestResolveInstance(t *testing.T) {
	project := &Project{
		Name: "test-project",
		Repos: map[string]*ProjectRepo{
			"scheduler": {
				Upstream: "org/scheduler",
				Fork:     "user/scheduler",
				Module:   "example.com/scheduler",
				Local:    "/tmp/scheduler",
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
		},
	}

	// Targeting scheduler + framework should produce a replace directive
	inst, err := project.ResolveInstance("my-feature", []string{"scheduler", "framework"})
	if err != nil {
		t.Fatalf("ResolveInstance: %v", err)
	}

	if inst.Status != "active" {
		t.Errorf("status = %q, want %q", inst.Status, "active")
	}

	if len(inst.Repos) != 2 {
		t.Errorf("repos = %d, want 2", len(inst.Repos))
	}

	if inst.Repos["scheduler"].Branch != "feat/my-feature" {
		t.Errorf("branch = %q, want %q", inst.Repos["scheduler"].Branch, "feat/my-feature")
	}

	// Should have one replace directive (scheduler -> framework)
	if len(inst.ReplaceDirectives) != 1 {
		t.Fatalf("replace directives = %d, want 1", len(inst.ReplaceDirectives))
	}

	rd := inst.ReplaceDirectives[0]
	if rd.Source != "scheduler" || rd.Target != "framework" {
		t.Errorf("replace = %s -> %s, want scheduler -> framework", rd.Source, rd.Target)
	}

	expected := "replace example.com/framework => ../framework"
	if rd.GoModLine != expected {
		t.Errorf("go_mod_line = %q, want %q", rd.GoModLine, expected)
	}
}

func TestResolveInstanceAllThreeRepos(t *testing.T) {
	project := &Project{
		Name: "test",
		Repos: map[string]*ProjectRepo{
			"a": {Fork: "u/a", Module: "ex.com/a", Local: "/tmp/a"},
			"b": {Fork: "u/b", Module: "ex.com/b", Local: "/tmp/b"},
			"c": {Fork: "u/c", Module: "ex.com/c", Local: "/tmp/c"},
		},
		Dependencies: []Dependency{
			{From: "a", To: "b", Type: "go-module"},
			{From: "a", To: "c", Type: "go-module"},
		},
	}

	inst, err := project.ResolveInstance("all", []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("ResolveInstance: %v", err)
	}

	// Both dependencies are active
	if len(inst.ReplaceDirectives) != 2 {
		t.Errorf("replace directives = %d, want 2", len(inst.ReplaceDirectives))
	}
}

func TestResolveInstanceUnknownRepo(t *testing.T) {
	project := &Project{
		Name:  "test",
		Repos: map[string]*ProjectRepo{},
	}

	_, err := project.ResolveInstance("test", []string{"nonexistent"})
	if err == nil {
		t.Error("expected error for unknown repo")
	}
}

func TestResolveInstanceNoReplace(t *testing.T) {
	project := &Project{
		Name: "test",
		Repos: map[string]*ProjectRepo{
			"a": {Fork: "u/a", Module: "ex.com/a", Local: "/tmp/a"},
			"b": {Fork: "u/b", Module: "ex.com/b", Local: "/tmp/b"},
		},
		Dependencies: []Dependency{
			{From: "a", To: "b", Type: "go-module"},
		},
	}

	// Only targeting "b" — no replace needed since "a" isn't in the instance
	inst, err := project.ResolveInstance("single", []string{"b"})
	if err != nil {
		t.Fatalf("ResolveInstance: %v", err)
	}

	if len(inst.ReplaceDirectives) != 0 {
		t.Errorf("replace directives = %d, want 0 (only one repo targeted)", len(inst.ReplaceDirectives))
	}
}
