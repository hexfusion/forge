package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the top-level pipeline configuration.
type Config struct {
	Instances map[string]*Instance `yaml:"instances"`

	// path is the location the config was loaded from.
	path string
}

// Instance tracks a concurrent development effort across multiple repos.
type Instance struct {
	Description       string                 `yaml:"description"`
	Status            string                 `yaml:"status"`
	Repos             map[string]*RepoConfig `yaml:"repos"`
	Images            map[string]string      `yaml:"images"`
	ReplaceDirectives []ReplaceDirective     `yaml:"replace_directives"`
	Deploy            *DeployConfig          `yaml:"deploy,omitempty"`
	Model             *ModelConfig           `yaml:"model,omitempty"`
	Bench             *BenchConfig           `yaml:"bench,omitempty"`
	Proposal          string                 `yaml:"proposal,omitempty"`
}

// ModelConfig defines the model serving target for this instance.
type ModelConfig struct {
	// Name is the model identifier used in API requests (e.g., "Qwen/Qwen2.5-7B-Instruct-AWQ").
	Name string `yaml:"name"`

	// Quantization is the quantization method (e.g., "awq", "gptq", "fp16").
	Quantization string `yaml:"quantization,omitempty"`

	// MaxModelLen is the maximum context length.
	MaxModelLen int `yaml:"max_model_len,omitempty"`

	// Source is where the model weights come from (e.g., HuggingFace repo, S3 path).
	Source string `yaml:"source,omitempty"`
}

// BenchConfig defines how to benchmark this instance.
type BenchConfig struct {
	// Workload is the benchmark workload type (e.g., "burst", "sustained", "mixed").
	Workload string `yaml:"workload"`

	// Concurrency is the number of concurrent requests.
	Concurrency int `yaml:"concurrency"`

	// TotalRequests is the total number of requests to send.
	TotalRequests int `yaml:"total_requests"`

	// PromptTemplate is the prompt format for the workload.
	PromptTemplate string `yaml:"prompt_template,omitempty"`

	// MaxTokens is the max tokens per request.
	MaxTokens int `yaml:"max_tokens"`

	// Stream controls whether to use streaming responses.
	Stream bool `yaml:"stream"`

	// Duration is the benchmark duration for sustained workloads (e.g., "60s", "5m").
	Duration string `yaml:"duration,omitempty"`

	// GatewayEndpoint overrides the gateway URL (default: auto-discover from cluster).
	GatewayEndpoint string `yaml:"gateway_endpoint,omitempty"`
}

// RepoConfig tracks a single repo's branch and fork within an instance.
type RepoConfig struct {
	Fork       string `yaml:"fork"`
	Branch     string `yaml:"branch"`
	BaseCommit string `yaml:"base_commit,omitempty"`
	Local      string `yaml:"local"`
}

// ReplaceDirective describes a go.mod replace between two repos in the instance.
type ReplaceDirective struct {
	Source    string `yaml:"source"`
	Target    string `yaml:"target"`
	GoModLine string `yaml:"go_mod_line"`
}

// DeployConfig holds deployment target configuration.
type DeployConfig struct {
	KubeContext   string `yaml:"kube_context"`
	Namespace     string `yaml:"namespace"`
	EPPDeployment string `yaml:"epp_deployment"`
}

// LoadConfig reads the pipeline instances config file.
// If path is empty, it searches standard locations.
func LoadConfig(path string) (*Config, error) {
	if path == "" {
		path = findConfig()
	}
	if path == "" {
		return nil, fmt.Errorf("no pipeline config found; create one at ~/.config/forge/pipelines.yaml or set --config")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config %s: %w", path, err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config %s: %w", path, err)
	}
	cfg.path = path

	// Expand ~ in local paths
	for _, inst := range cfg.Instances {
		for _, repo := range inst.Repos {
			repo.Local = expandHome(repo.Local)
		}
	}

	return &cfg, nil
}

// GetInstance returns a named instance or an error.
func (c *Config) GetInstance(name string) (*Instance, error) {
	inst, ok := c.Instances[name]
	if !ok {
		names := make([]string, 0, len(c.Instances))
		for k := range c.Instances {
			names = append(names, k)
		}
		return nil, fmt.Errorf("instance %q not found; available: %s", name, strings.Join(names, ", "))
	}
	return inst, nil
}

// ActiveInstances returns instances with status "active".
func (c *Config) ActiveInstances() map[string]*Instance {
	active := make(map[string]*Instance)
	for name, inst := range c.Instances {
		if inst.Status == "active" {
			active[name] = inst
		}
	}
	return active
}

// SaveConfig writes the pipeline config back to disk.
func SaveConfig(cfg *Config) error {
	if cfg.path == "" {
		return fmt.Errorf("no config path set")
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshaling config: %w", err)
	}

	if err := os.WriteFile(cfg.path, data, 0644); err != nil {
		return fmt.Errorf("writing config %s: %w", cfg.path, err)
	}
	return nil
}

func findConfig() string {
	candidates := []string{
		os.Getenv("FORGE_PIPELINE_CONFIG"),
		filepath.Join(mustHomeDir(), ".config", "forge", "pipelines.yaml"),
	}

	// Also check the design repo location
	designRepo := filepath.Join(mustHomeDir(), "projects", "hexfusion", "design",
		"work", "llm-d", "dev-environment", "dev-instances.yaml")
	candidates = append(candidates, designRepo)

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return ""
}

func expandHome(path string) string {
	if strings.HasPrefix(path, "~/") {
		return filepath.Join(mustHomeDir(), path[2:])
	}
	return path
}

func mustHomeDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "/tmp"
	}
	return home
}
