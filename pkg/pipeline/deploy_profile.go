package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// DeployProfile defines a full stack deployment to a cluster.
// It lists components, their install method, and where pipeline-built
// images get injected. Profiles are stored alongside project graphs
// in the forge projects directory.
type DeployProfile struct {
	Name string `yaml:"name"`

	// Cluster target
	KubeContext string `yaml:"kube_context"`
	Namespace   string `yaml:"namespace"`

	// Components are deployed in order.
	Components []Component `yaml:"components"`
}

// Component is a single deployable unit in a stack.
type Component struct {
	// Name identifies the component (e.g., "gateway", "epp", "model-server").
	Name string `yaml:"name"`

	// Type is the deployment method: "helm", "manifest", "kustomize".
	Type string `yaml:"type"`

	// Helm-specific fields
	Chart       string            `yaml:"chart,omitempty"`
	ChartVersion string           `yaml:"chart_version,omitempty"`
	ValuesFile  string            `yaml:"values_file,omitempty"`
	HelmSet     map[string]string `yaml:"helm_set,omitempty"`

	// Manifest-specific fields
	ManifestPath string `yaml:"manifest_path,omitempty"`

	// Kustomize-specific fields
	KustomizeDir string `yaml:"kustomize_dir,omitempty"`

	// ImageOverride maps a pipeline image name (e.g., "epp") to the
	// container name or Helm value path where it should be injected.
	// When the pipeline has a built image with this key, it replaces
	// the default image.
	ImageOverride *ImageOverrideSpec `yaml:"image_override,omitempty"`

	// DependsOn lists component names that must be deployed first.
	DependsOn []string `yaml:"depends_on,omitempty"`

	// WaitReady causes forge to wait for the component to be ready
	// before moving to the next.
	WaitReady bool `yaml:"wait_ready,omitempty"`
}

// ImageOverrideSpec describes how to inject a pipeline image into a component.
type ImageOverrideSpec struct {
	// PipelineImage is the key in the pipeline instance's Images map.
	PipelineImage string `yaml:"pipeline_image"`

	// HelmValue is the Helm --set path (e.g., "inferenceExtension.image.repository").
	// Used when Type is "helm".
	HelmValue string `yaml:"helm_value,omitempty"`

	// ContainerName is the container to patch via kubectl set image.
	// Used when Type is "manifest".
	ContainerName string `yaml:"container_name,omitempty"`
}

// DeployStack deploys a full profile to a cluster, injecting pipeline images.
func DeployStack(profile *DeployProfile, images map[string]*ImageState) error {
	for _, comp := range profile.Components {
		fmt.Printf("=== Deploying %s (%s)\n", comp.Name, comp.Type)

		var err error
		switch comp.Type {
		case "helm":
			err = deployHelm(profile, &comp, images)
		case "manifest":
			err = deployManifest(profile, &comp, images)
		case "kustomize":
			err = deployKustomize(profile, &comp, images)
		default:
			err = fmt.Errorf("unknown component type: %s", comp.Type)
		}

		if err != nil {
			return fmt.Errorf("deploying %s: %w", comp.Name, err)
		}

		if comp.WaitReady {
			fmt.Printf("  Waiting for %s to be ready...\n", comp.Name)
			// Simple wait — could be smarter per component type
			if err := waitForReady(profile, &comp); err != nil {
				return fmt.Errorf("waiting for %s: %w", comp.Name, err)
			}
		}
	}
	return nil
}

func deployHelm(profile *DeployProfile, comp *Component, images map[string]*ImageState) error {
	args := []string{
		"upgrade", "--install", comp.Name,
		comp.Chart,
		"-n", profile.Namespace,
		"--kube-context", profile.KubeContext,
	}

	if comp.ChartVersion != "" {
		args = append(args, "--version", comp.ChartVersion)
	}

	if comp.ValuesFile != "" {
		args = append(args, "-f", comp.ValuesFile)
	}

	// Inject Helm --set values
	for k, v := range comp.HelmSet {
		args = append(args, "--set", k+"="+v)
	}

	// Inject pipeline image if configured
	if comp.ImageOverride != nil {
		if img, ok := images[comp.ImageOverride.PipelineImage]; ok && img.Tag != "" {
			// Split image:tag into repository and tag for Helm
			repo, tag := splitImageTag(img.Tag)
			if comp.ImageOverride.HelmValue != "" {
				args = append(args, "--set", comp.ImageOverride.HelmValue+".repository="+repo)
				args = append(args, "--set", comp.ImageOverride.HelmValue+".tag="+tag)
			}
		}
	}

	return runCmd(".", "helm", args...)
}

func deployManifest(profile *DeployProfile, comp *Component, images map[string]*ImageState) error {
	// Apply the manifest
	if err := runCmd(".", "kubectl", "--context", profile.KubeContext,
		"-n", profile.Namespace, "apply", "-f", comp.ManifestPath); err != nil {
		return err
	}

	// Inject pipeline image if configured
	if comp.ImageOverride != nil {
		if img, ok := images[comp.ImageOverride.PipelineImage]; ok && img.Tag != "" {
			container := comp.ImageOverride.ContainerName
			if container == "" {
				container = comp.Name
			}
			// Find the deployment name from the manifest name
			return runCmd(".", "kubectl", "--context", profile.KubeContext,
				"-n", profile.Namespace, "set", "image",
				"deployment/"+comp.Name, container+"="+img.Tag)
		}
	}
	return nil
}

func deployKustomize(profile *DeployProfile, comp *Component, images map[string]*ImageState) error {
	args := []string{
		"--context", profile.KubeContext,
		"-n", profile.Namespace,
		"apply", "-k", comp.KustomizeDir,
	}
	return runCmd(".", "kubectl", args...)
}

func waitForReady(profile *DeployProfile, comp *Component) error {
	return runCmd(".", "kubectl", "--context", profile.KubeContext,
		"-n", profile.Namespace, "rollout", "status",
		"deployment/"+comp.Name, "--timeout=120s")
}

func splitImageTag(imageRef string) (string, string) {
	if idx := strings.LastIndex(imageRef, ":"); idx != -1 {
		return imageRef[:idx], imageRef[idx+1:]
	}
	return imageRef, "latest"
}

// LoadDeployProfile reads a deploy profile from the projects directory.
func LoadDeployProfile(name string) (*DeployProfile, error) {
	candidates := []string{
		filepath.Join(StateDir(), "profiles", name+".yaml"),
		filepath.Join(mustHomeDir(), "projects", "hexfusion", "forge", "profiles", name+".yaml"),
	}

	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var profile DeployProfile
		if err := yaml.Unmarshal(data, &profile); err != nil {
			return nil, fmt.Errorf("parsing profile %s: %w", path, err)
		}
		return &profile, nil
	}

	return nil, fmt.Errorf("deploy profile %q not found", name)
}
