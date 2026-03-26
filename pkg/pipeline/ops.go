package pipeline

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// statusAll prints a summary of all instances.
func statusAll(cfg *Config) error {
	fmt.Printf("%-20s %-10s %s\n", "INSTANCE", "STATUS", "DESCRIPTION")
	fmt.Printf("%-20s %-10s %s\n", "--------", "------", "-----------")
	for name, inst := range cfg.Instances {
		fmt.Printf("%-20s %-10s %s\n", name, inst.Status, inst.Description)
	}
	return nil
}

// statusInstance prints detailed state for one instance.
func statusInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	fmt.Printf("Instance: %s\n", name)
	fmt.Printf("Status:   %s\n", inst.Status)
	fmt.Printf("Description: %s\n\n", inst.Description)

	fmt.Println("Repos:")
	for repoName, repo := range inst.Repos {
		branch, dirty, commit := repoState(repo.Local)
		dirtyStr := ""
		if dirty {
			dirtyStr = " [dirty]"
		}
		fmt.Printf("  %-40s %-30s %s%s\n", repoName, branch, commit, dirtyStr)
	}

	fmt.Println("\nImages:")
	for component, image := range inst.Images {
		fmt.Printf("  %-10s %s\n", component, image)
	}

	if inst.Deploy != nil {
		fmt.Printf("\nDeploy target: context=%s deployment=%s\n",
			inst.Deploy.KubeContext, inst.Deploy.EPPDeployment)
	}

	return nil
}

// syncAll syncs all active instances.
func syncAll(cfg *Config) error {
	for name, inst := range cfg.ActiveInstances() {
		fmt.Printf("=== Syncing %s\n", name)
		if err := syncInstanceRepos(inst); err != nil {
			fmt.Fprintf(os.Stderr, "  error syncing %s: %v\n", name, err)
		}
	}
	return nil
}

// syncInstance syncs one instance's branches to forks.
func syncInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}
	return syncInstanceRepos(inst)
}

func syncInstanceRepos(inst *Instance) error {
	for repoName, repo := range inst.Repos {
		localPath := repo.Local
		if _, err := os.Stat(localPath); os.IsNotExist(err) {
			fmt.Printf("  SKIP: %s not cloned at %s\n", repoName, localPath)
			continue
		}

		currentBranch, _, _ := repoState(localPath)
		if currentBranch != repo.Branch {
			fmt.Printf("  SKIP: %s on %q, expected %q\n", repoName, currentBranch, repo.Branch)
			continue
		}

		fmt.Printf("  Pushing %s -> origin/%s\n", repoName, repo.Branch)
		if err := gitPush(localPath, repo.Branch); err != nil {
			return fmt.Errorf("pushing %s: %w", repoName, err)
		}
	}
	return nil
}

// buildInstance builds container images for an instance.
func buildInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	eppImage, ok := inst.Images["epp"]
	if !ok {
		return fmt.Errorf("instance %q has no 'epp' image defined", name)
	}

	// Find the scheduler repo (the one that builds the EPP binary)
	schedulerRepo, ok := inst.Repos["llm-d-inference-scheduler"]
	if !ok {
		return fmt.Errorf("instance %q has no llm-d-inference-scheduler repo", name)
	}

	// Check if we need a multi-repo build (replace directive exists)
	hasReplace := false
	for _, rd := range inst.ReplaceDirectives {
		if rd.Source == "llm-d-inference-scheduler" {
			hasReplace = true
			break
		}
	}

	if hasReplace {
		return buildWithReplace(inst, schedulerRepo, eppImage)
	}
	return buildStandalone(schedulerRepo, eppImage)
}

func buildWithReplace(inst *Instance, schedulerRepo *RepoConfig, imageTag string) error {
	// When a replace directive exists, the Dockerfile needs both repos
	// in the build context. Build from the parent directory with a
	// multi-repo Dockerfile.
	parentDir := filepath.Dir(schedulerRepo.Local)

	// Verify all referenced repos exist
	for repoName, repo := range inst.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			return fmt.Errorf("repo %s not found at %s", repoName, repo.Local)
		}
	}

	// Write a temporary multi-repo Dockerfile
	dockerfile := `FROM quay.io/projectquay/golang:1.25 AS go-builder
ARG LDFLAGS="-s -w"
WORKDIR /workspace
COPY llm-d-inference-scheduler/go.mod llm-d-inference-scheduler/go.sum ./llm-d-inference-scheduler/
COPY gateway-api-inference-extension/go.mod gateway-api-inference-extension/go.sum ./gateway-api-inference-extension/
WORKDIR /workspace/llm-d-inference-scheduler
RUN go mod download
COPY llm-d-inference-scheduler/cmd/ ./cmd/
COPY llm-d-inference-scheduler/pkg/ ./pkg/
COPY gateway-api-inference-extension/ /workspace/gateway-api-inference-extension/
RUN CGO_ENABLED=0 go build -ldflags="${LDFLAGS}" -o /workspace/bin/epp cmd/epp/main.go
FROM registry.access.redhat.com/ubi9/ubi-micro:9.7
WORKDIR /
COPY --from=go-builder /workspace/bin/epp /app/epp
USER 65532:65532
EXPOSE 9002 9003 9090 5557
ENTRYPOINT ["/app/epp"]
`
	tmpFile, err := os.CreateTemp("", "Dockerfile.forge-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp dockerfile: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(dockerfile); err != nil {
		return fmt.Errorf("writing temp dockerfile: %w", err)
	}
	tmpFile.Close()

	fmt.Printf("Building %s (multi-repo, context: %s)\n", imageTag, parentDir)
	return runCmd(parentDir, "podman", "build", "-t", imageTag, "-f", tmpFile.Name(), ".")
}

func buildStandalone(schedulerRepo *RepoConfig, imageTag string) error {
	fmt.Printf("Building %s (standalone)\n", imageTag)
	return runCmd(schedulerRepo.Local, "podman", "build", "-t", imageTag, "-f", "Dockerfile.epp", ".")
}

// pushInstance pushes container images for an instance.
func pushInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	eppImage, ok := inst.Images["epp"]
	if !ok {
		return fmt.Errorf("instance %q has no 'epp' image defined", name)
	}

	fmt.Printf("Pushing %s\n", eppImage)
	return runCmd(".", "podman", "push", eppImage)
}

// deployInstance deploys the instance's EPP image to a cluster.
func deployInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	eppImage, ok := inst.Images["epp"]
	if !ok {
		return fmt.Errorf("instance %q has no 'epp' image defined", name)
	}

	deploy := inst.Deploy
	if deploy == nil {
		// Defaults for the lab cluster
		deploy = &DeployConfig{
			KubeContext:   "labctl-endor",
			Namespace:     "default",
			EPPDeployment: "vllm-pool-epp",
		}
	}

	fmt.Printf("Deploying %s to %s/%s\n", eppImage, deploy.KubeContext, deploy.EPPDeployment)

	if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"set", "image", "deployment/"+deploy.EPPDeployment,
		"epp="+eppImage, "-n", deploy.Namespace); err != nil {
		return err
	}

	fmt.Println("Waiting for rollout...")
	return runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"rollout", "status", "deployment/"+deploy.EPPDeployment,
		"-n", deploy.Namespace, "--timeout=120s")
}

// --- helpers ---

func repoState(localPath string) (branch string, dirty bool, commit string) {
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return "(not cloned)", false, ""
	}

	out, err := cmdOutput(localPath, "git", "branch", "--show-current")
	if err != nil {
		return "(unknown)", false, ""
	}
	branch = strings.TrimSpace(out)

	out, _ = cmdOutput(localPath, "git", "status", "--porcelain")
	dirty = strings.TrimSpace(out) != ""

	out, _ = cmdOutput(localPath, "git", "log", "--oneline", "-1")
	commit = strings.TrimSpace(out)

	return branch, dirty, commit
}

func gitPush(dir, branch string) error {
	return runCmd(dir, "git", "push", "origin", branch)
}

func runCmd(dir string, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdOutput(dir string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	return string(out), err
}
