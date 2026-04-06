package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadPipelineDef(t *testing.T) {
	tests := []struct {
		name           string
		yaml           string
		wantErr        bool
		errContains    string
		wantImages     int
		wantValidate   int
		wantDeployNil  bool
		checkDef       func(t *testing.T, def *PipelineDef)
	}{
		{
			name: "full pipeline def",
			yaml: `
name: rhoai-ci-test
images:
  epp:
    source: external
    ref: quay.io/rhoai/scheduler:latest
    env_var: RELATED_IMAGE_SCHEDULER
  kvcache:
    source: build
    local: /tmp/kvcache
    build_file: Dockerfile
    registry: quay.io:443/sbatsche
    name_override: llm-d-kvcache
    env_var: RELATED_IMAGE_KVCACHE
deploy:
  kube_context: my-cluster
  namespace: my-ns
  target_deployment: my-operator
  method: env-patch
validate:
  - name: smoke-test
    command: pytest test/ -v
    working_dir: /tmp/tests
    timeout: 300
`,
			wantImages:   2,
			wantValidate: 1,
			checkDef: func(t *testing.T, def *PipelineDef) {
				if def.Name != "rhoai-ci-test" {
					t.Errorf("name = %q", def.Name)
				}

				epp := def.Images["epp"]
				if epp == nil {
					t.Fatal("epp image not found")
				}
				if !epp.IsExternal() {
					t.Error("epp should be external")
				}
				if epp.Ref != "quay.io/rhoai/scheduler:latest" {
					t.Errorf("epp.Ref = %q", epp.Ref)
				}
				if epp.EnvVar != "RELATED_IMAGE_SCHEDULER" {
					t.Errorf("epp.EnvVar = %q", epp.EnvVar)
				}

				kv := def.Images["kvcache"]
				if kv == nil {
					t.Fatal("kvcache image not found")
				}
				if kv.IsExternal() {
					t.Error("kvcache should not be external")
				}
				if kv.Local != "/tmp/kvcache" {
					t.Errorf("kvcache.Local = %q", kv.Local)
				}
				if kv.NameOverride != "llm-d-kvcache" {
					t.Errorf("kvcache.NameOverride = %q", kv.NameOverride)
				}

				if def.Deploy == nil {
					t.Fatal("deploy is nil")
				}
				if def.Deploy.Method != "env-patch" {
					t.Errorf("deploy.Method = %q", def.Deploy.Method)
				}
				if def.Deploy.TargetDeployment != "my-operator" {
					t.Errorf("deploy.TargetDeployment = %q", def.Deploy.TargetDeployment)
				}

				if len(def.Validate) != 1 {
					t.Fatalf("validate = %d, want 1", len(def.Validate))
				}
				if def.Validate[0].Name != "smoke-test" {
					t.Errorf("validate[0].Name = %q", def.Validate[0].Name)
				}
				if def.Validate[0].Timeout != 300 {
					t.Errorf("validate[0].Timeout = %d", def.Validate[0].Timeout)
				}
			},
		},
		{
			name: "images only — no deploy or validate",
			yaml: `
name: minimal
images:
  myimg:
    source: external
    ref: registry.example.com/img:v1
`,
			wantImages:    1,
			wantValidate:  0,
			wantDeployNil: true,
		},
		{
			name:        "invalid YAML",
			yaml:        "{{not yaml",
			wantErr:     true,
			errContains: "parsing",
		},
		{
			name: "tilde expansion",
			yaml: `
name: tilde
images:
  myimg:
    source: build
    local: ~/projects/myrepo
validate:
  - name: test
    command: echo ok
    working_dir: ~/projects/tests
`,
			wantImages:   1,
			wantValidate: 1,
			checkDef: func(t *testing.T, def *PipelineDef) {
				if strings.HasPrefix(def.Images["myimg"].Local, "~/") {
					t.Error("tilde not expanded in image local path")
				}
				if strings.HasPrefix(def.Validate[0].WorkingDir, "~/") {
					t.Error("tilde not expanded in validate working_dir")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "pipeline.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0644); err != nil {
				t.Fatal(err)
			}

			def, err := LoadPipelineDef(path)

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

			if len(def.Images) != tt.wantImages {
				t.Errorf("images = %d, want %d", len(def.Images), tt.wantImages)
			}
			if len(def.Validate) != tt.wantValidate {
				t.Errorf("validate = %d, want %d", len(def.Validate), tt.wantValidate)
			}
			if tt.wantDeployNil && def.Deploy != nil {
				t.Error("expected nil deploy")
			}
			if tt.checkDef != nil {
				tt.checkDef(t, def)
			}
		})
	}
}

func TestLoadPipelineDefNotFound(t *testing.T) {
	_, err := LoadPipelineDef("/nonexistent/path.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestPipelineImageIsExternal(t *testing.T) {
	tests := []struct {
		source string
		want   bool
	}{
		{"external", true},
		{"build", false},
		{"", false},
	}

	for _, tt := range tests {
		img := &PipelineImage{Source: tt.source}
		if got := img.IsExternal(); got != tt.want {
			t.Errorf("IsExternal(%q) = %v, want %v", tt.source, got, tt.want)
		}
	}
}

func TestPipelineImageImageTag(t *testing.T) {
	tests := []struct {
		name         string
		img          *PipelineImage
		key          string
		instanceName string
		want         string
	}{
		{
			name:         "external returns ref",
			img:          &PipelineImage{Source: "external", Ref: "quay.io/rhoai/scheduler:latest"},
			key:          "epp",
			instanceName: "test",
			want:         "quay.io/rhoai/scheduler:latest",
		},
		{
			name:         "build with name override",
			img:          &PipelineImage{Source: "build", Registry: "quay.io:443/sbatsche", NameOverride: "llm-d-epp"},
			key:          "epp",
			instanceName: "my-test",
			want:         "quay.io:443/sbatsche/llm-d-epp:my-test",
		},
		{
			name:         "build without name override uses key",
			img:          &PipelineImage{Source: "build", Registry: "quay.io:443/sbatsche"},
			key:          "kvcache",
			instanceName: "my-test",
			want:         "quay.io:443/sbatsche/kvcache:my-test",
		},
		{
			name:         "build with no registry uses localhost",
			img:          &PipelineImage{Source: "build"},
			key:          "img",
			instanceName: "test",
			want:         "localhost/img:test",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.img.ImageTag(tt.key, tt.instanceName)
			if got != tt.want {
				t.Errorf("ImageTag = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPipelineDefToInstance(t *testing.T) {
	def := &PipelineDef{
		Name: "test-pipeline",
		Images: map[string]*PipelineImage{
			"epp": {
				Source: "external",
				Ref:    "quay.io/rhoai/scheduler:latest",
				EnvVar: "RELATED_IMAGE_SCHEDULER",
			},
			"kvcache": {
				Source:       "build",
				Local:        "/tmp/kvcache",
				Registry:     "quay.io:443/sbatsche",
				NameOverride: "llm-d-kvcache",
				EnvVar:       "RELATED_IMAGE_KVCACHE",
			},
		},
		Deploy: &PipelineDeploy{
			KubeContext:      "my-cluster",
			Namespace:        "my-ns",
			TargetDeployment: "my-operator",
			Method:           "env-patch",
		},
	}

	inst := def.ToInstance("my-test")

	// Build images should be in Images
	if len(inst.Images) != 1 {
		t.Errorf("Images = %d, want 1", len(inst.Images))
	}
	if _, ok := inst.Images["kvcache"]; !ok {
		t.Error("kvcache not in Images")
	}
	if inst.Images["kvcache"] != "quay.io:443/sbatsche/llm-d-kvcache:my-test" {
		t.Errorf("kvcache tag = %q", inst.Images["kvcache"])
	}

	// External images should be in ExternalImages
	if len(inst.ExternalImages) != 1 {
		t.Errorf("ExternalImages = %d, want 1", len(inst.ExternalImages))
	}
	if _, ok := inst.ExternalImages["epp"]; !ok {
		t.Error("epp not in ExternalImages")
	}
	if inst.ExternalImages["epp"] != "quay.io/rhoai/scheduler:latest" {
		t.Errorf("epp ref = %q", inst.ExternalImages["epp"])
	}

	// Deploy should be converted
	if inst.Deploy == nil {
		t.Fatal("deploy is nil")
	}
	if inst.Deploy.KubeContext != "my-cluster" {
		t.Errorf("kube_context = %q", inst.Deploy.KubeContext)
	}
	if inst.Deploy.EPPDeployment != "my-operator" {
		t.Errorf("epp_deployment = %q", inst.Deploy.EPPDeployment)
	}

	if inst.Status != "active" {
		t.Errorf("status = %q", inst.Status)
	}
}

func TestCreateFromPipelineDefAndState(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	// Write pipeline def
	pipelineYAML := `
name: test-pipeline
images:
  epp:
    source: external
    ref: quay.io/rhoai/scheduler:latest
    env_var: RELATED_IMAGE_SCHEDULER
  kvcache:
    source: build
    local: ` + dir + `
    build_file: Dockerfile
    registry: quay.io:443/sbatsche
    name_override: llm-d-kvcache
    env_var: RELATED_IMAGE_KVCACHE
deploy:
  kube_context: test-cluster
  namespace: test-ns
  target_deployment: test-operator
  method: env-patch
`
	pipelinePath := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(pipelinePath, []byte(pipelineYAML), 0644); err != nil {
		t.Fatal(err)
	}

	// Write a minimal config so createFromPipelineDef can append
	configPath := filepath.Join(dir, "pipelines.yaml")
	if err := os.WriteFile(configPath, []byte("instances: {}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FORGE_PIPELINE_CONFIG", configPath)

	// Create instance
	if err := createFromPipelineDef(pipelinePath, "my-test"); err != nil {
		t.Fatalf("createFromPipelineDef: %v", err)
	}

	// Verify state was written
	state, err := LoadState("my-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if state.Name != "my-test" {
		t.Errorf("name = %q", state.Name)
	}
	if state.Status != "active" {
		t.Errorf("status = %q", state.Status)
	}

	// Check epp is external
	eppImg := state.Images["epp"]
	if eppImg == nil {
		t.Fatal("epp image not in state")
	}
	if eppImg.Source != "external" {
		t.Errorf("epp source = %q, want external", eppImg.Source)
	}
	if eppImg.EnvVar != "RELATED_IMAGE_SCHEDULER" {
		t.Errorf("epp env_var = %q", eppImg.EnvVar)
	}
	if !eppImg.Pushed {
		t.Error("external image should be marked as pushed")
	}

	// Check kvcache is build
	kvImg := state.Images["kvcache"]
	if kvImg == nil {
		t.Fatal("kvcache image not in state")
	}
	if kvImg.Source != "build" {
		t.Errorf("kvcache source = %q, want build", kvImg.Source)
	}
	if kvImg.EnvVar != "RELATED_IMAGE_KVCACHE" {
		t.Errorf("kvcache env_var = %q", kvImg.EnvVar)
	}

	// Verify config was updated
	cfg, err := LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	inst, err := cfg.GetInstance("my-test")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}
	if inst.PipelineFile != pipelinePath {
		t.Errorf("pipeline_file = %q", inst.PipelineFile)
	}
}

func TestValidateStateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	now := func() *time.Time { t := time.Now().Truncate(time.Second); return &t }()
	state := &InstanceState{
		Name:   "validate-test",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
		Validate: &ValidateState{
			Results: []ValidateResult{
				{Name: "smoke", Passed: true, ExitCode: 0, Duration: "4m32s", RunTime: now},
				{Name: "perf", Passed: false, ExitCode: 1, Duration: "28m15s", RunTime: now},
			},
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState("validate-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Validate == nil {
		t.Fatal("validate is nil after round-trip")
	}
	if len(loaded.Validate.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(loaded.Validate.Results))
	}
	if loaded.Validate.Results[0].Name != "smoke" || !loaded.Validate.Results[0].Passed {
		t.Error("first result not preserved")
	}
	if loaded.Validate.Results[1].Name != "perf" || loaded.Validate.Results[1].Passed {
		t.Error("second result not preserved")
	}
	if loaded.Validate.Results[1].ExitCode != 1 {
		t.Errorf("exit code = %d", loaded.Validate.Results[1].ExitCode)
	}
}

func TestImageStateWithSourceAndEnvVar(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	state := &InstanceState{
		Name:   "source-test",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{
			"epp": {
				Tag:    "quay.io/rhoai/scheduler:latest",
				Source: "external",
				EnvVar: "RELATED_IMAGE_SCHEDULER",
				Pushed: true,
			},
			"kvcache": {
				Tag:    "quay.io:443/sbatsche/llm-d-kvcache:test",
				Source: "build",
				EnvVar: "RELATED_IMAGE_KVCACHE",
				Digest: "sha256:abc123",
			},
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState("source-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	epp := loaded.Images["epp"]
	if epp.Source != "external" {
		t.Errorf("epp source = %q", epp.Source)
	}
	if epp.EnvVar != "RELATED_IMAGE_SCHEDULER" {
		t.Errorf("epp env_var = %q", epp.EnvVar)
	}

	kv := loaded.Images["kvcache"]
	if kv.Source != "build" {
		t.Errorf("kvcache source = %q", kv.Source)
	}
	if kv.EnvVar != "RELATED_IMAGE_KVCACHE" {
		t.Errorf("kvcache env_var = %q", kv.EnvVar)
	}
}

func TestDeployStateWithImages(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", dir)

	now := func() *time.Time { t := time.Now().Truncate(time.Second); return &t }()
	state := &InstanceState{
		Name:   "deploy-images-test",
		Status: "active",
		Repos:  map[string]*RepoState{},
		Images: map[string]*ImageState{},
		Deploy: &DeployState{
			KubeContext: "my-cluster",
			Namespace:   "my-ns",
			Deployment:  "my-operator",
			Method:      "env-patch",
			DeployedImages: map[string]string{
				"epp":     "quay.io/rhoai/scheduler:latest",
				"kvcache": "quay.io:443/sbatsche/llm-d-kvcache:test",
			},
			DeployTime: now,
		},
	}

	if err := SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := LoadState("deploy-images-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Deploy.Method != "env-patch" {
		t.Errorf("method = %q", loaded.Deploy.Method)
	}
	if len(loaded.Deploy.DeployedImages) != 2 {
		t.Fatalf("deployed_images = %d, want 2", len(loaded.Deploy.DeployedImages))
	}
	if loaded.Deploy.DeployedImages["epp"] != "quay.io/rhoai/scheduler:latest" {
		t.Errorf("epp = %q", loaded.Deploy.DeployedImages["epp"])
	}
}
