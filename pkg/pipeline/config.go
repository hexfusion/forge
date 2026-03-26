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
	Proposal          string                 `yaml:"proposal,omitempty"`
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
