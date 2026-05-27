package pipeline

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCollectManifests(t *testing.T) {
	dir := t.TempDir()
	// Out-of-order on creation; ReadDir returns them sorted by name.
	for _, n := range []string{"02-gateway.yaml", "01-pool.yaml", "notes.txt", "03-route.yml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0755); err != nil {
		t.Fatal(err)
	}

	deploy := &PipelineDeploy{
		Manifests:   []string{"/explicit/a.yaml", "/explicit/b.yaml"},
		ManifestDir: dir,
	}
	got, err := collectManifests(deploy)
	if err != nil {
		t.Fatalf("collectManifests: %v", err)
	}

	want := []string{
		"/explicit/a.yaml",
		"/explicit/b.yaml",
		filepath.Join(dir, "01-pool.yaml"),
		filepath.Join(dir, "02-gateway.yaml"),
		filepath.Join(dir, "03-route.yml"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("collectManifests =\n  %v\nwant\n  %v", got, want)
	}
}

func TestCollectManifestsExplicitOnly(t *testing.T) {
	deploy := &PipelineDeploy{Manifests: []string{"/a.yaml", "/b.yaml"}}
	got, err := collectManifests(deploy)
	if err != nil {
		t.Fatalf("collectManifests: %v", err)
	}
	if !reflect.DeepEqual(got, []string{"/a.yaml", "/b.yaml"}) {
		t.Errorf("got %v", got)
	}
}

func TestCollectManifestsNone(t *testing.T) {
	got, err := collectManifests(&PipelineDeploy{})
	if err != nil {
		t.Fatalf("collectManifests: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %v, want empty", got)
	}
}

func TestCollectManifestsBadDir(t *testing.T) {
	deploy := &PipelineDeploy{ManifestDir: "/no/such/dir/forge-test"}
	if _, err := collectManifests(deploy); err == nil {
		t.Error("expected error for missing manifest_dir")
	}
}

func TestLoadPipelineDefManifests(t *testing.T) {
	dir := t.TempDir()
	yamlStr := `
name: shapes
images:
  epp:
    source: external
    ref: quay.io/x/y:z
deploy:
  kube_context: c
  namespace: redhat-ods-operator
  target_deployment: rhods-operator
  method: env-patch
  manifests:
    - ~/crs/upstream/inferencepool.yaml
    - ~/crs/upstream/gateway.yaml
  manifest_dir: ~/crs/upstream
`
	path := filepath.Join(dir, "pipeline.yaml")
	if err := os.WriteFile(path, []byte(yamlStr), 0644); err != nil {
		t.Fatal(err)
	}

	def, err := LoadPipelineDef(path)
	if err != nil {
		t.Fatalf("LoadPipelineDef: %v", err)
	}
	if def.Deploy == nil {
		t.Fatal("deploy is nil")
	}
	if len(def.Deploy.Manifests) != 2 {
		t.Fatalf("manifests = %d, want 2", len(def.Deploy.Manifests))
	}
	for _, m := range def.Deploy.Manifests {
		if strings.HasPrefix(m, "~/") {
			t.Errorf("manifest path not home-expanded: %q", m)
		}
	}
	if strings.HasPrefix(def.Deploy.ManifestDir, "~/") {
		t.Errorf("manifest_dir not home-expanded: %q", def.Deploy.ManifestDir)
	}
}
