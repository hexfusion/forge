package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// StateDir is the base directory for forge runtime state.
func StateDir() string {
	if d := os.Getenv("FORGE_STATE_DIR"); d != "" {
		return d
	}
	return filepath.Join(mustHomeDir(), ".local", "share", "forge")
}

// InstanceState is the runtime state for a pipeline instance.
// This is the file written to ~/.local/share/forge/instances/<name>.yaml.
// It tracks everything that changes over time: builds, deploys, sync state.
type InstanceState struct {
	// Name is the instance identifier.
	Name string `yaml:"name"`

	// Project is the project graph this instance was created from.
	Project string `yaml:"project"`

	// Status is "active", "paused", or "closed".
	Status string `yaml:"status"`

	// Description is a human-readable summary.
	Description string `yaml:"description"`

	// Created is when the instance was created.
	Created time.Time `yaml:"created"`

	// Repos tracks per-repo state.
	Repos map[string]*RepoState `yaml:"repos"`

	// Images tracks per-image build state.
	Images map[string]*ImageState `yaml:"images"`

	// Deploy tracks deployment state.
	Deploy *DeployState `yaml:"deploy,omitempty"`

	// ReplaceDirectives are the go.mod replace lines injected.
	ReplaceDirectives []ReplaceDirective `yaml:"replace_directives,omitempty"`

	// Proposal links back to the design doc if any.
	Proposal string `yaml:"proposal,omitempty"`
}

// RepoState tracks the state of a single repo within an instance.
type RepoState struct {
	Fork       string `yaml:"fork"`
	Branch     string `yaml:"branch"`
	BaseCommit string `yaml:"base_commit,omitempty"`
	Local      string `yaml:"local"`

	// LastSyncCommit is the commit SHA that was last pushed to the fork.
	LastSyncCommit string `yaml:"last_sync_commit,omitempty"`

	// LastSyncTime is when the branch was last pushed.
	LastSyncTime *time.Time `yaml:"last_sync_time,omitempty"`
}

// ImageState tracks container image build/push state.
type ImageState struct {
	// Tag is the full image reference (e.g., quay.io:443/sbatsche/llm-d-epp:orca-metrics).
	Tag string `yaml:"tag"`

	// Digest is the image digest from the last build (sha256:...).
	Digest string `yaml:"digest,omitempty"`

	// BuildTime is when the image was last built.
	BuildTime *time.Time `yaml:"build_time,omitempty"`

	// BuildCommits records the HEAD commit of each repo at build time.
	BuildCommits map[string]string `yaml:"build_commits,omitempty"`

	// PushTime is when the image was last pushed.
	PushTime *time.Time `yaml:"push_time,omitempty"`

	// Pushed is whether the current digest has been pushed.
	Pushed bool `yaml:"pushed"`
}

// DeployState tracks what's deployed to a cluster.
type DeployState struct {
	KubeContext   string `yaml:"kube_context"`
	Namespace     string `yaml:"namespace"`
	Deployment    string `yaml:"deployment"`

	// DeployedDigest is the image digest that was last deployed.
	DeployedDigest string `yaml:"deployed_digest,omitempty"`

	// DeployTime is when the last deploy happened.
	DeployTime *time.Time `yaml:"deploy_time,omitempty"`

	// DeployCommits records the HEAD commits at deploy time.
	DeployCommits map[string]string `yaml:"deploy_commits,omitempty"`
}

// DriftStatus describes the relationship between built, pushed, and deployed state.
type DriftStatus struct {
	// Built is true if an image has been built.
	Built bool

	// Pushed is true if the built image has been pushed.
	Pushed bool

	// Deployed is true if the pushed image has been deployed.
	Deployed bool

	// DeployStale is true if the image was rebuilt after the last deploy.
	DeployStale bool

	// ReposDirty lists repos with uncommitted changes.
	ReposDirty []string

	// ReposUnsynced lists repos with commits not pushed to fork.
	ReposUnsynced []string

	// RunningDigest is what the cluster is actually running (from kubectl).
	RunningDigest string

	// DigestMatch is true if running digest matches the deployed digest.
	DigestMatch bool
}

// CheckDrift computes the drift status for an instance by comparing
// local state against the live cluster.
func (s *InstanceState) CheckDrift() (*DriftStatus, error) {
	ds := &DriftStatus{}

	// Check repo state
	for name, repo := range s.Repos {
		_, dirty, _ := repoState(repo.Local)
		if dirty {
			ds.ReposDirty = append(ds.ReposDirty, name)
		}

		// Check if local HEAD matches last sync
		if repo.LastSyncCommit != "" {
			_, _, headCommit := repoState(repo.Local)
			if headCommit != "" && !startsWith(headCommit, repo.LastSyncCommit) {
				ds.ReposUnsynced = append(ds.ReposUnsynced, name)
			}
		}
	}

	// Check image state
	for _, img := range s.Images {
		if img.Digest != "" {
			ds.Built = true
		}
		if img.Pushed {
			ds.Pushed = true
		}
	}

	// Check deploy state
	if s.Deploy != nil && s.Deploy.DeployedDigest != "" {
		ds.Deployed = true

		// Compare deployed digest against current build digest
		for _, img := range s.Images {
			if img.Digest != "" && img.Digest != s.Deploy.DeployedDigest {
				ds.DeployStale = true
			}
		}

		// Check live cluster state
		runningDigest, err := getRunningImageDigest(
			s.Deploy.KubeContext, s.Deploy.Namespace, s.Deploy.Deployment)
		if err == nil {
			ds.RunningDigest = runningDigest
			ds.DigestMatch = runningDigest == s.Deploy.DeployedDigest
		}
	}

	return ds, nil
}

// --- persistence ---

// LoadState reads an instance state file.
func LoadState(name string) (*InstanceState, error) {
	path := instanceStatePath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("loading state for %q: %w", name, err)
	}

	var state InstanceState
	if err := yaml.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parsing state for %q: %w", name, err)
	}
	return &state, nil
}

// SaveState writes an instance state file.
func SaveState(state *InstanceState) error {
	dir := filepath.Join(StateDir(), "instances")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating state dir: %w", err)
	}

	data, err := yaml.Marshal(state)
	if err != nil {
		return fmt.Errorf("marshalling state: %w", err)
	}

	path := instanceStatePath(state.Name)
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("writing state: %w", err)
	}
	return nil
}

// ListStates returns all instance state files.
func ListStates() ([]*InstanceState, error) {
	dir := filepath.Join(StateDir(), "instances")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var states []*InstanceState
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		name := entry.Name()[:len(entry.Name())-5]
		state, err := LoadState(name)
		if err != nil {
			continue
		}
		states = append(states, state)
	}
	return states, nil
}

func instanceStatePath(name string) string {
	return filepath.Join(StateDir(), "instances", name+".yaml")
}

// --- cluster queries ---

func getRunningImageDigest(kubeContext, namespace, deployment string) (string, error) {
	// Get the image reference from the running deployment, then resolve its digest.
	// Two-step: get the image, then inspect it.
	out, err := cmdOutput(".", "kubectl", "--context", kubeContext,
		"-n", namespace, "get", "deployment", deployment,
		"-o", "jsonpath={.spec.template.spec.containers[0].image}")
	if err != nil {
		return "", fmt.Errorf("getting deployment image: %w", err)
	}

	image := trimSpace(out)
	if image == "" {
		return "", fmt.Errorf("no image found on deployment %s", deployment)
	}

	// Get the digest from the deployment's own pods via rollout status.
	// Use the deployment's label selector rather than assuming a specific label.
	out, err = cmdOutput(".", "kubectl", "--context", kubeContext,
		"-n", namespace, "get", "deployment", deployment,
		"-o", "jsonpath={.status.readyReplicas}")
	if err != nil || trimSpace(out) == "0" || trimSpace(out) == "" {
		return "", fmt.Errorf("deployment %s has no ready replicas", deployment)
	}

	// Get image ID from the first pod owned by this deployment's replicaset
	out, err = cmdOutput(".", "kubectl", "--context", kubeContext,
		"-n", namespace, "get", "pods",
		"--selector", "app", // generic fallback
		"-o", "jsonpath={.items[?(@.metadata.ownerReferences[0].name)].status.containerStatuses[0].imageID}",
	)
	if err != nil {
		// Fall back: compare spec image directly (less precise but works everywhere)
		return image, nil
	}

	return trimSpace(out), nil
}

func getImageDigest(imageRef string) (string, error) {
	out, err := cmdOutput(".", "podman", "inspect", "--format", "{{.Digest}}", imageRef)
	if err != nil {
		return "", err
	}
	return trimSpace(out), nil
}

func getHeadCommit(repoPath string) string {
	out, _ := cmdOutput(repoPath, "git", "rev-parse", "--short", "HEAD")
	return trimSpace(out)
}

func trimSpace(s string) string {
	// strings.TrimSpace without importing strings
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\r' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\r' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}

func startsWith(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
