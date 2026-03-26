//go:build integration

// Package integration contains end-to-end tests for the forge pipeline.
// These tests require:
//   - A running Kind cluster (KUBE_CONTEXT env var)
//   - A local container registry (FORGE_REGISTRY env var, default localhost:5001)
//   - podman or docker available
//
// Run with: go test ./test/integration/ -tags=integration -v
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hexfusion/forge/pkg/pipeline"
)

func TestPipelineConfigRoundTrip(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create a test config
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, "pipelines.yaml")

	registry := os.Getenv("FORGE_REGISTRY")
	if registry == "" {
		registry = "localhost:5001"
	}

	config := fmt.Sprintf(`
instances:
  integration-test:
    description: "forge integration test"
    status: active
    repos: {}
    images:
      test-image: %s/forge-test:integration
`, registry)

	if err := os.WriteFile(configPath, []byte(config), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := pipeline.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	inst, err := cfg.GetInstance("integration-test")
	if err != nil {
		t.Fatalf("GetInstance: %v", err)
	}

	if inst.Status != "active" {
		t.Errorf("status = %q, want active", inst.Status)
	}

	if inst.Images["test-image"] != registry+"/forge-test:integration" {
		t.Errorf("image = %q", inst.Images["test-image"])
	}
}

func TestPipelineStateLifecycle(t *testing.T) {
	stateDir := t.TempDir()
	t.Setenv("FORGE_STATE_DIR", stateDir)

	// Create -> Save -> Load -> Verify
	state := &pipeline.InstanceState{
		Name:        "lifecycle-test",
		Status:      "active",
		Description: "integration lifecycle test",
		Repos:       map[string]*pipeline.RepoState{},
		Images:      map[string]*pipeline.ImageState{},
	}

	if err := pipeline.SaveState(state); err != nil {
		t.Fatalf("SaveState: %v", err)
	}

	loaded, err := pipeline.LoadState("lifecycle-test")
	if err != nil {
		t.Fatalf("LoadState: %v", err)
	}

	if loaded.Name != "lifecycle-test" {
		t.Errorf("name = %q", loaded.Name)
	}

	// List should find it
	states, err := pipeline.ListStates()
	if err != nil {
		t.Fatalf("ListStates: %v", err)
	}
	if len(states) != 1 {
		t.Fatalf("states = %d, want 1", len(states))
	}
}

func TestLocalRegistryAvailable(t *testing.T) {
	registry := os.Getenv("FORGE_REGISTRY")
	if registry == "" {
		registry = "localhost:5001"
	}

	// Check if registry responds
	out, err := exec.Command("curl", "-s", fmt.Sprintf("http://%s/v2/", registry)).Output()
	if err != nil {
		t.Skipf("local registry not available at %s: %v", registry, err)
	}

	if !strings.Contains(string(out), "{}") {
		t.Logf("registry response: %s", out)
	}
}

func TestKindClusterAvailable(t *testing.T) {
	kubeContext := os.Getenv("KUBE_CONTEXT")
	if kubeContext == "" {
		t.Skip("KUBE_CONTEXT not set")
	}

	out, err := exec.Command("kubectl", "--context", kubeContext, "cluster-info").CombinedOutput()
	if err != nil {
		t.Skipf("cluster not available: %v\n%s", err, out)
	}

	t.Logf("cluster info: %s", out)
}

func TestBuildAndPushToLocalRegistry(t *testing.T) {
	registry := os.Getenv("FORGE_REGISTRY")
	if registry == "" {
		t.Skip("FORGE_REGISTRY not set")
	}

	// Build a minimal test image
	tmpDir := t.TempDir()
	containerfile := filepath.Join(tmpDir, "Containerfile")
	if err := os.WriteFile(containerfile, []byte("FROM scratch\nCOPY Containerfile /test\n"), 0644); err != nil {
		t.Fatal(err)
	}

	imageTag := fmt.Sprintf("%s/forge-test:ci-%d", registry, os.Getpid())

	// Build
	cmd := exec.Command("podman", "build", "-t", imageTag, "-f", containerfile, tmpDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// Try docker as fallback (CI uses docker)
		cmd = exec.Command("docker", "build", "-t", imageTag, "-f", containerfile, tmpDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("build failed: %v", err)
		}
	}

	// Push to local registry
	pushCmd := exec.Command("podman", "push", imageTag, "--tls-verify=false")
	pushCmd.Stdout = os.Stdout
	pushCmd.Stderr = os.Stderr
	if err := pushCmd.Run(); err != nil {
		pushCmd = exec.Command("docker", "push", imageTag)
		pushCmd.Stdout = os.Stdout
		pushCmd.Stderr = os.Stderr
		if err := pushCmd.Run(); err != nil {
			t.Fatalf("push failed: %v", err)
		}
	}

	// Verify image is in registry
	catalogURL := fmt.Sprintf("http://%s/v2/_catalog", registry)
	out, err := exec.Command("curl", "-s", catalogURL).Output()
	if err != nil {
		t.Fatalf("catalog check: %v", err)
	}

	if !strings.Contains(string(out), "forge-test") {
		t.Errorf("image not found in registry catalog: %s", out)
	}
}
