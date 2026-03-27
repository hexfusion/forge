package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// --- LoadConfig ---

func TestLoadConfig(t *testing.T) {
	tests := []struct {
		name          string
		yaml          string
		wantErr       bool
		errContains   string
		wantInstances int
		checkInstance func(t *testing.T, inst *Instance)
	}{
		{
			name: "full instance with all fields",
			yaml: `
instances:
  test-instance:
    description: "test pipeline"
    status: active
    repos:
      repo-a:
        fork: user/repo-a
        branch: feat/test
        base_commit: abc123
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
    deploy:
      kube_context: my-cluster
      namespace: default
      epp_deployment: my-epp
    model:
      name: Qwen/Qwen2.5-7B
      quantization: awq
      max_model_len: 1024
    bench:
      workload: burst
      concurrency: 50
      total_requests: 200
      max_tokens: 64
      stream: false
    proposal: path/to/PROPOSAL.md
`,
			wantInstances: 1,
			checkInstance: func(t *testing.T, inst *Instance) {
				if inst.Status != "active" {
					t.Errorf("status = %q, want active", inst.Status)
				}
				if len(inst.Repos) != 2 {
					t.Errorf("repos = %d, want 2", len(inst.Repos))
				}
				if inst.Repos["repo-a"].BaseCommit != "abc123" {
					t.Errorf("base_commit = %q, want abc123", inst.Repos["repo-a"].BaseCommit)
				}
				if inst.Deploy == nil || inst.Deploy.KubeContext != "my-cluster" {
					t.Error("deploy config not loaded")
				}
				if inst.Model == nil || inst.Model.Name != "Qwen/Qwen2.5-7B" {
					t.Error("model config not loaded")
				}
				if inst.Bench == nil || inst.Bench.Concurrency != 50 {
					t.Error("bench config not loaded")
				}
				if inst.Proposal != "path/to/PROPOSAL.md" {
					t.Errorf("proposal = %q", inst.Proposal)
				}
			},
		},
		{
			name: "multiple instances",
			yaml: `
instances:
  alpha:
    status: active
    description: "first"
  beta:
    status: paused
    description: "second"
  gamma:
    status: closed
    description: "third"
`,
			wantInstances: 3,
		},
		{
			name: "minimal instance — only status",
			yaml: `
instances:
  bare:
    status: active
`,
			wantInstances: 1,
			checkInstance: func(t *testing.T, inst *Instance) {
				if inst.Deploy != nil {
					t.Error("expected nil deploy")
				}
				if inst.Model != nil {
					t.Error("expected nil model")
				}
				if inst.Bench != nil {
					t.Error("expected nil bench")
				}
				if len(inst.Repos) != 0 {
					t.Errorf("repos = %d, want 0", len(inst.Repos))
				}
			},
		},
		{
			name:          "empty file",
			yaml:          "",
			wantInstances: 0,
		},
		{
			name:        "invalid YAML",
			yaml:        "{{not yaml",
			wantErr:     true,
			errContains: "parsing",
		},
		{
			name: "tilde expansion in local paths",
			yaml: `
instances:
  tilde:
    status: active
    repos:
      myrepo:
        fork: user/myrepo
        branch: main
        local: ~/projects/myrepo
`,
			wantInstances: 1,
			checkInstance: func(t *testing.T, inst *Instance) {
				local := inst.Repos["myrepo"].Local
				if strings.HasPrefix(local, "~/") {
					t.Errorf("tilde not expanded: %q", local)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pipelines.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfig(path)

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

			if len(cfg.Instances) != tt.wantInstances {
				t.Fatalf("instances = %d, want %d", len(cfg.Instances), tt.wantInstances)
			}

			if tt.checkInstance != nil && tt.wantInstances > 0 {
				// Get first instance
				for _, inst := range cfg.Instances {
					tt.checkInstance(t, inst)
					break
				}
			}
		})
	}
}

// --- GetInstance ---

func TestGetInstance(t *testing.T) {
	cfg := &Config{
		Instances: map[string]*Instance{
			"alpha": {Status: "active", Description: "first"},
			"beta":  {Status: "paused", Description: "second"},
		},
	}

	tests := []struct {
		name        string
		instance    string
		wantErr     bool
		errContains string
		wantStatus  string
	}{
		{name: "found", instance: "alpha", wantStatus: "active"},
		{name: "found paused", instance: "beta", wantStatus: "paused"},
		{name: "not found", instance: "gamma", wantErr: true, errContains: "not found"},
		{name: "empty name", instance: "", wantErr: true, errContains: "not found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inst, err := cfg.GetInstance(tt.instance)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("error = %q, want containing %q", err.Error(), tt.errContains)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if inst.Status != tt.wantStatus {
				t.Errorf("status = %q, want %q", inst.Status, tt.wantStatus)
			}
		})
	}
}

// --- ActiveInstances ---

func TestActiveInstances(t *testing.T) {
	tests := []struct {
		name      string
		instances map[string]*Instance
		wantCount int
	}{
		{
			name: "mixed statuses",
			instances: map[string]*Instance{
				"a": {Status: "active"},
				"b": {Status: "active"},
				"c": {Status: "paused"},
				"d": {Status: "closed"},
			},
			wantCount: 2,
		},
		{
			name: "all active",
			instances: map[string]*Instance{
				"a": {Status: "active"},
				"b": {Status: "active"},
			},
			wantCount: 2,
		},
		{
			name: "none active",
			instances: map[string]*Instance{
				"a": {Status: "paused"},
				"b": {Status: "closed"},
			},
			wantCount: 0,
		},
		{
			name:      "empty",
			instances: map[string]*Instance{},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Instances: tt.instances}
			got := cfg.ActiveInstances()
			if len(got) != tt.wantCount {
				t.Errorf("active count = %d, want %d", len(got), tt.wantCount)
			}
		})
	}
}

// --- SaveConfig round-trip ---

func TestSaveConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pipelines.yaml")

	original := &Config{
		path: path,
		Instances: map[string]*Instance{
			"test": {
				Description: "round trip test",
				Status:      "active",
				Repos: map[string]*RepoConfig{
					"repo-a": {Fork: "user/repo-a", Branch: "feat/test", Local: "/tmp/a"},
				},
				Images: map[string]string{"img": "registry/img:tag"},
				ReplaceDirectives: []ReplaceDirective{
					{Source: "repo-a", Target: "repo-b", GoModLine: "replace ex.com/b => ../b"},
				},
				Deploy: &DeployConfig{
					KubeContext:   "ctx",
					Namespace:     "ns",
					EPPDeployment: "epp",
				},
				Model: &ModelConfig{
					Name:         "model-name",
					Quantization: "awq",
					MaxModelLen:  2048,
				},
				Proposal: "path/to/PROPOSAL.md",
			},
		},
	}

	if err := SaveConfig(original); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig after save: %v", err)
	}

	inst := loaded.Instances["test"]
	if inst == nil {
		t.Fatal("instance 'test' not found after round-trip")
	}
	if inst.Description != "round trip test" {
		t.Errorf("description = %q", inst.Description)
	}
	if inst.Status != "active" {
		t.Errorf("status = %q", inst.Status)
	}
	if inst.Repos["repo-a"] == nil || inst.Repos["repo-a"].Branch != "feat/test" {
		t.Error("repo config not preserved")
	}
	if inst.Images["img"] != "registry/img:tag" {
		t.Error("images not preserved")
	}
	if len(inst.ReplaceDirectives) != 1 {
		t.Error("replace directives not preserved")
	}
	if inst.Deploy == nil || inst.Deploy.KubeContext != "ctx" {
		t.Error("deploy not preserved")
	}
	if inst.Model == nil || inst.Model.Name != "model-name" {
		t.Error("model not preserved")
	}
	if inst.Proposal != "path/to/PROPOSAL.md" {
		t.Error("proposal not preserved")
	}
}

func TestSaveConfigNoPath(t *testing.T) {
	cfg := &Config{path: ""}
	err := SaveConfig(cfg)
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

// --- expandHome ---

func TestExpandHome(t *testing.T) {
	tests := []struct {
		name  string
		input string
		check func(t *testing.T, result string)
	}{
		{
			name:  "tilde prefix expands",
			input: "~/foo/bar",
			check: func(t *testing.T, result string) {
				if result == "~/foo/bar" {
					t.Error("~ was not expanded")
				}
				if !strings.HasSuffix(result, "/foo/bar") {
					t.Errorf("unexpected result: %q", result)
				}
			},
		},
		{
			name:  "absolute path unchanged",
			input: "/absolute/path",
			check: func(t *testing.T, result string) {
				if result != "/absolute/path" {
					t.Errorf("absolute path changed: %q", result)
				}
			},
		},
		{
			name:  "relative path unchanged",
			input: "relative/path",
			check: func(t *testing.T, result string) {
				if result != "relative/path" {
					t.Errorf("relative path changed: %q", result)
				}
			},
		},
		{
			name:  "empty string unchanged",
			input: "",
			check: func(t *testing.T, result string) {
				if result != "" {
					t.Errorf("empty string changed: %q", result)
				}
			},
		},
		{
			name:  "tilde alone",
			input: "~",
			check: func(t *testing.T, result string) {
				// Should NOT expand — only "~/" prefix triggers expansion
				if result != "~" {
					t.Errorf("bare tilde changed: %q", result)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := expandHome(tt.input)
			tt.check(t, result)
		})
	}
}
