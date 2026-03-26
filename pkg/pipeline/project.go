package pipeline

import "fmt"

// Project defines a dependency graph for a set of related repos.
// It is the schema that forge walks when creating new pipeline instances.
// The graph has two kinds of edges:
//   - go-module: repo A imports repo B via Go modules (becomes a replace directive)
//   - build: an image is built from a repo's source
//
// When a user creates an instance targeting specific repos, forge resolves
// the transitive dependencies and auto-generates replace directives, image
// tags, and Containerfile selection.
type Project struct {
	// Name identifies the project (e.g., "llm-d").
	Name string `yaml:"name"`

	// Repos defines all repositories in the ecosystem.
	Repos map[string]*ProjectRepo `yaml:"repos"`

	// Dependencies defines the edges in the graph.
	Dependencies []Dependency `yaml:"dependencies"`

	// Defaults for instances created from this project.
	Defaults *ProjectDefaults `yaml:"defaults,omitempty"`
}

// ProjectRepo defines a single repo in the project graph.
type ProjectRepo struct {
	// Upstream is the canonical repo (e.g., "kubernetes-sigs/gateway-api-inference-extension").
	Upstream string `yaml:"upstream"`

	// Fork is the developer's fork (e.g., "hexfusion/gateway-api-inference-extension").
	Fork string `yaml:"fork"`

	// Module is the Go module path (e.g., "sigs.k8s.io/gateway-api-inference-extension").
	// Empty for non-Go repos (e.g., vLLM).
	Module string `yaml:"module,omitempty"`

	// Local is the default local checkout path.
	Local string `yaml:"local"`

	// Images defines container images built from this repo.
	Images map[string]*ImageDef `yaml:"images,omitempty"`
}

// ImageDef defines how to build a container image from a repo.
type ImageDef struct {
	// BuildFile is the container build file path relative to the repo root
	// (e.g., "Containerfile", "Dockerfile.epp"). Forge passes this to
	// podman build -f. The name matches whatever the upstream project uses.
	BuildFile string `yaml:"build_file"`

	// Registry is the default push target (e.g., "quay.io:443/sbatsche").
	Registry string `yaml:"registry"`

	// NameOverride overrides the image name (default: repo name).
	NameOverride string `yaml:"name_override,omitempty"`
}

// Dependency is an edge in the project graph.
type Dependency struct {
	// From is the dependent repo (the one whose go.mod gets the replace).
	From string `yaml:"from"`

	// To is the dependency repo (the one being replaced with a local path).
	To string `yaml:"to"`

	// Type is the kind of dependency.
	//   "go-module": From imports To via Go modules. When both are in an
	//                instance, a replace directive is injected.
	//   "build":     From's image is built from To's source.
	Type string `yaml:"type"`
}

// ProjectDefaults holds default values for new instances.
type ProjectDefaults struct {
	// ImageRegistry is the default registry for image tags.
	ImageRegistry string `yaml:"image_registry"`

	// Deploy holds default deployment target.
	Deploy *DeployConfig `yaml:"deploy,omitempty"`
}

// ResolveInstance creates a new Instance from a project graph and a set
// of target repos the developer wants to modify.
//
// It walks the dependency graph to determine:
//   - Which repos need branches (the targets + any repos that depend on them)
//   - Which replace directives to inject
//   - Which images to build
//   - What tags to use
func (p *Project) ResolveInstance(name string, targetRepos []string) (*Instance, error) {
	// Build a set of repos in this instance
	repoSet := make(map[string]bool)
	for _, r := range targetRepos {
		if _, ok := p.Repos[r]; !ok {
			return nil, fmt.Errorf("repo %q not found in project %q", r, p.Name)
		}
		repoSet[r] = true
	}

	// Walk the graph: if repo A depends on repo B and both are in the
	// target set, we need a replace directive. If only B is targeted but
	// A builds an image from B, A also needs to be in the instance.
	var replaces []ReplaceDirective
	for _, dep := range p.Dependencies {
		fromIn := repoSet[dep.From]
		toIn := repoSet[dep.To]

		if dep.Type == "go-module" && fromIn && toIn {
			fromRepo := p.Repos[dep.From]
			toRepo := p.Repos[dep.To]
			replaces = append(replaces, ReplaceDirective{
				Source: dep.From,
				Target: dep.To,
				GoModLine: fmt.Sprintf("replace %s => ../%s",
					toRepo.Module, dep.To),
			})
			_ = fromRepo // used for validation
		}

		// If a build dependency target is modified, include the builder repo
		if dep.Type == "build" && toIn && !fromIn {
			repoSet[dep.From] = true
		}
	}

	// Build the instance
	repos := make(map[string]*RepoConfig)
	images := make(map[string]string)
	branchName := "feat/" + name

	for repoName := range repoSet {
		pr := p.Repos[repoName]
		repos[repoName] = &RepoConfig{
			Fork:   pr.Fork,
			Branch: branchName,
			Local:  pr.Local,
		}

		// Resolve images
		for imgName, imgDef := range pr.Images {
			registry := imgDef.Registry
			if registry == "" && p.Defaults != nil {
				registry = p.Defaults.ImageRegistry
			}
			imageName := imgDef.NameOverride
			if imageName == "" {
				imageName = "llm-d-" + imgName
			}
			images[imgName] = fmt.Sprintf("%s/%s:%s", registry, imageName, name)
		}
	}

	inst := &Instance{
		Description:       fmt.Sprintf("Auto-generated from project %s", p.Name),
		Status:            "active",
		Repos:             repos,
		Images:            images,
		ReplaceDirectives: replaces,
	}

	if p.Defaults != nil && p.Defaults.Deploy != nil {
		inst.Deploy = p.Defaults.Deploy
	}

	return inst, nil
}
