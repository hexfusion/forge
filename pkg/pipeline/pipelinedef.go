package pipeline

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

// PipelineDef is a self-contained pipeline definition. Unlike a project
// graph + instance pair, it declares images (build or external), deploy
// target, and validation steps in one file. Created via:
//
//	forge pipeline create <name> --from <path>
type PipelineDef struct {
	Name     string                    `yaml:"name"`
	Images   map[string]*PipelineImage `yaml:"images"`
	Deploy   *PipelineDeploy           `yaml:"deploy,omitempty"`
	Validate []ValidateStep            `yaml:"validate,omitempty"`
}

// PipelineImage describes a container image — either built from local
// source or referenced from an external registry.
type PipelineImage struct {
	// Source is "build" or "external".
	Source string `yaml:"source"`

	// --- External fields (source: external) ---

	// Ref is the full image reference (e.g., quay.io/rhoai/scheduler:latest).
	Ref string `yaml:"ref,omitempty"`

	// --- Build fields (source: build) ---

	// Local is the path to the repo checkout to build from.
	Local string `yaml:"local,omitempty"`

	// BuildFile is the Containerfile/Dockerfile relative to Local.
	BuildFile string `yaml:"build_file,omitempty"`

	// Registry is the push target (e.g., quay.io:443/sbatsche).
	Registry string `yaml:"registry,omitempty"`

	// NameOverride overrides the image name (default: image key).
	NameOverride string `yaml:"name_override,omitempty"`

	// BuildTarget is the Go build target (e.g., cmd/epp/main.go).
	BuildTarget string `yaml:"build_target,omitempty"`

	// BinaryName is the output binary name.
	BinaryName string `yaml:"binary_name,omitempty"`

	// --- Shared ---

	// EnvVar is the RELATED_IMAGE_* env var name for operator injection.
	EnvVar string `yaml:"env_var,omitempty"`
}

// PipelineDeploy describes how to push images to a cluster.
type PipelineDeploy struct {
	// KubeContext is the kubectl/oc context.
	KubeContext string `yaml:"kube_context"`

	// Namespace is the target namespace.
	Namespace string `yaml:"namespace"`

	// TargetDeployment is the deployment to patch.
	TargetDeployment string `yaml:"target_deployment"`

	// Method is the deploy strategy. Currently only "env-patch".
	Method string `yaml:"method"`
}

// ValidateStep is a post-deploy validation command.
type ValidateStep struct {
	Name       string `yaml:"name"`
	Command    string `yaml:"command"`
	WorkingDir string `yaml:"working_dir,omitempty"`
	Timeout    int    `yaml:"timeout,omitempty"` // seconds, default 300
}

// ValidateState tracks validation results for an instance.
type ValidateState struct {
	Results []ValidateResult `yaml:"results,omitempty"`
}

// ValidateResult is the outcome of a single validation step.
type ValidateResult struct {
	Name     string     `yaml:"name"`
	Passed   bool       `yaml:"passed"`
	ExitCode int        `yaml:"exit_code"`
	Duration string     `yaml:"duration,omitempty"`
	RunTime  *time.Time `yaml:"run_time,omitempty"`
}

// IsExternal returns true if this image comes from an external registry.
func (img *PipelineImage) IsExternal() bool {
	return img.Source == "external"
}

// ImageTag returns the full image tag for this image. For external images,
// this is Ref. For build images, it's constructed from registry + name.
func (img *PipelineImage) ImageTag(key, instanceName string) string {
	if img.IsExternal() {
		return img.Ref
	}
	name := key
	if img.NameOverride != "" {
		name = img.NameOverride
	}
	registry := img.Registry
	if registry == "" {
		registry = "localhost"
	}
	return fmt.Sprintf("%s/%s:%s", registry, name, instanceName)
}

// LoadPipelineDef reads a pipeline definition from a YAML file.
func LoadPipelineDef(path string) (*PipelineDef, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading pipeline def %s: %w", path, err)
	}

	var def PipelineDef
	if err := yaml.Unmarshal(data, &def); err != nil {
		return nil, fmt.Errorf("parsing pipeline def %s: %w", path, err)
	}

	// Expand ~ in paths
	for _, img := range def.Images {
		img.Local = expandHome(img.Local)
	}
	for i := range def.Validate {
		def.Validate[i].WorkingDir = expandHome(def.Validate[i].WorkingDir)
	}

	return &def, nil
}

// ToInstance converts a PipelineDef into an Instance for use with
// existing pipeline operations (build, push, deploy, status).
func (def *PipelineDef) ToInstance(name string) *Instance {
	images := make(map[string]string)
	externalImages := make(map[string]string)

	for key, img := range def.Images {
		tag := img.ImageTag(key, name)
		if img.IsExternal() {
			externalImages[key] = tag
		} else {
			images[key] = tag
		}
	}

	var deploy *DeployConfig
	if def.Deploy != nil {
		deploy = &DeployConfig{
			KubeContext:   def.Deploy.KubeContext,
			Namespace:     def.Deploy.Namespace,
			EPPDeployment: def.Deploy.TargetDeployment,
		}
	}

	return &Instance{
		Description:    fmt.Sprintf("From pipeline def: %s", def.Name),
		Status:         "active",
		Images:         images,
		ExternalImages: externalImages,
		Deploy:         deploy,
		PipelineFile:   "", // set by caller
	}
}
