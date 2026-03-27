package pipeline

import (
	"strings"
	"testing"
)

func TestParseQuayImage(t *testing.T) {
	tests := []struct {
		name          string
		imageRef      string
		wantNamespace string
		wantRepo      string
		wantErr       bool
		errContains   string
	}{
		{
			name:          "standard quay.io ref",
			imageRef:      "quay.io/sbatsche/llm-d-epp:orca-metrics",
			wantNamespace: "sbatsche",
			wantRepo:      "llm-d-epp",
		},
		{
			name:          "quay.io with port",
			imageRef:      "quay.io:443/sbatsche/llm-d-epp:orca-metrics",
			wantNamespace: "sbatsche",
			wantRepo:      "llm-d-epp",
		},
		{
			name:          "no tag",
			imageRef:      "quay.io/sbatsche/vllm-cuda",
			wantNamespace: "sbatsche",
			wantRepo:      "vllm-cuda",
		},
		{
			name:          "quay.io with port and no tag",
			imageRef:      "quay.io:443/myorg/myrepo",
			wantNamespace: "myorg",
			wantRepo:      "myrepo",
		},
		{
			name:        "not quay.io",
			imageRef:    "docker.io/library/nginx:latest",
			wantErr:     true,
			errContains: "not a quay.io",
		},
		{
			name:        "too few parts",
			imageRef:    "quay.io/onlynamespace",
			wantErr:     true,
			errContains: "cannot parse",
		},
		{
			name:          "with https scheme",
			imageRef:      "https://quay.io/sbatsche/myimage:v1",
			wantNamespace: "sbatsche",
			wantRepo:      "myimage",
		},
		{
			name:        "localhost registry",
			imageRef:    "localhost:5001/test/image:tag",
			wantErr:     true,
			errContains: "not a quay.io",
		},
		{
			name:          "org namespace",
			imageRef:      "quay.io:443/llm-d/inference-scheduler:latest",
			wantNamespace: "llm-d",
			wantRepo:      "inference-scheduler",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ns, repo, err := parseQuayImage(tt.imageRef)

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
			if ns != tt.wantNamespace {
				t.Errorf("namespace = %q, want %q", ns, tt.wantNamespace)
			}
			if repo != tt.wantRepo {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo)
			}
		})
	}
}

func TestEnsureQuayRepo_NoToken(t *testing.T) {
	t.Setenv("QUAY_API_TOKEN", "")

	err := EnsureQuayRepo("quay.io/sbatsche/test-repo:v1")
	if err == nil {
		t.Fatal("expected error when QUAY_API_TOKEN is not set")
	}
	if !strings.Contains(err.Error(), "QUAY_API_TOKEN") {
		t.Errorf("error should mention QUAY_API_TOKEN: %v", err)
	}
}

func TestEnsureQuayRepo_NonQuaySkipped(t *testing.T) {
	// Non-quay images should error (not silently skip)
	err := EnsureQuayRepo("docker.io/library/nginx:latest")
	if err == nil {
		t.Fatal("expected error for non-quay image")
	}
}

func TestQuayHost(t *testing.T) {
	tests := []struct {
		imageRef string
		want     string
	}{
		{"quay.io/ns/repo:tag", "https://quay.io"},
		{"quay.io:443/ns/repo:tag", "https://quay.io"},
	}

	for _, tt := range tests {
		got := quayHost(tt.imageRef)
		if got != tt.want {
			t.Errorf("quayHost(%q) = %q, want %q", tt.imageRef, got, tt.want)
		}
	}
}
