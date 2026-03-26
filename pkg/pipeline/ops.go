package pipeline

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// statusAll prints a summary of all instances from the state store.
func statusAll(cfg *Config) error {
	states, err := ListStates()
	if err != nil || len(states) == 0 {
		// Fall back to config file if no state files yet
		return statusAllFromConfig(cfg)
	}

	fmt.Printf("%-20s %-8s %-8s %-8s %-10s %s\n",
		"INSTANCE", "STATUS", "BUILT", "PUSHED", "DEPLOYED", "DESCRIPTION")
	fmt.Printf("%-20s %-8s %-8s %-8s %-10s %s\n",
		"--------", "------", "-----", "------", "--------", "-----------")

	for _, state := range states {
		drift, _ := state.CheckDrift()
		built := "no"
		pushed := "no"
		deployed := "no"

		if drift != nil {
			if drift.Built {
				built = "yes"
			}
			if drift.Pushed {
				pushed = "yes"
			}
			if drift.Deployed {
				if drift.DeployStale {
					deployed = "stale"
				} else if drift.DigestMatch {
					deployed = "current"
				} else {
					deployed = "drift"
				}
			}
		}

		fmt.Printf("%-20s %-8s %-8s %-8s %-10s %s\n",
			state.Name, state.Status, built, pushed, deployed, state.Description)
	}
	return nil
}

func statusAllFromConfig(cfg *Config) error {
	fmt.Printf("%-20s %-10s %s\n", "INSTANCE", "STATUS", "DESCRIPTION")
	fmt.Printf("%-20s %-10s %s\n", "--------", "------", "-----------")
	for name, inst := range cfg.Instances {
		fmt.Printf("%-20s %-10s %s\n", name, inst.Status, inst.Description)
	}
	return nil
}

// statusInstance prints detailed state for one instance.
func statusInstance(cfg *Config, name string) error {
	// Try state file first
	state, stateErr := LoadState(name)
	if stateErr == nil {
		return statusFromState(state)
	}

	// Fall back to config
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}
	return statusFromConfig(name, inst)
}

func statusFromState(state *InstanceState) error {
	fmt.Printf("Instance:    %s\n", state.Name)
	fmt.Printf("Project:     %s\n", state.Project)
	fmt.Printf("Status:      %s\n", state.Status)
	fmt.Printf("Created:     %s\n\n", state.Created.Format("2006-01-02"))

	fmt.Println("Repos:")
	for repoName, repo := range state.Repos {
		branch, dirty, commit := repoState(repo.Local)
		dirtyStr := "[clean]"
		if dirty {
			dirtyStr = "[dirty]"
		}
		syncStr := ""
		if repo.LastSyncCommit != "" {
			if startsWith(commit, repo.LastSyncCommit) {
				syncStr = " synced"
			} else {
				syncStr = " unsynced"
			}
		}
		fmt.Printf("  %-40s %-30s %s %s%s\n",
			repoName, branch, commit, dirtyStr, syncStr)
		fmt.Printf("  %-40s → %s\n", "", repo.Fork)
	}

	if len(state.ReplaceDirectives) > 0 {
		fmt.Println("\nReplace Directives:")
		for _, rd := range state.ReplaceDirectives {
			fmt.Printf("  %s go.mod: %s\n", rd.Source, rd.GoModLine)
		}
	}

	fmt.Println("\nImages:")
	for imgName, img := range state.Images {
		builtStr := "not built"
		if img.BuildTime != nil {
			builtStr = fmt.Sprintf("built %s", img.BuildTime.Format("2006-01-02 15:04"))
		}
		pushedStr := ""
		if img.Pushed {
			pushedStr = " pushed"
		}
		fmt.Printf("  %-10s %s\n", imgName, img.Tag)
		fmt.Printf("  %-10s %s%s\n", "", builtStr, pushedStr)
		if img.Digest != "" {
			fmt.Printf("  %-10s digest: %s\n", "", truncateDigest(img.Digest))
		}
	}

	if state.Deploy != nil {
		fmt.Printf("\nDeploy:\n")
		fmt.Printf("  cluster:    %s\n", state.Deploy.KubeContext)
		fmt.Printf("  namespace:  %s\n", state.Deploy.Namespace)
		fmt.Printf("  deployment: %s\n", state.Deploy.Deployment)

		if state.Deploy.DeployedDigest != "" {
			fmt.Printf("  digest:     %s\n", truncateDigest(state.Deploy.DeployedDigest))
		}
		if state.Deploy.DeployTime != nil {
			fmt.Printf("  deployed:   %s\n", state.Deploy.DeployTime.Format("2006-01-02 15:04"))
		}

		// Live verification
		drift, err := state.CheckDrift()
		if err == nil && drift.Deployed {
			if drift.DeployStale {
				fmt.Printf("  status:     STALE (rebuilt since last deploy)\n")
			} else if drift.DigestMatch {
				fmt.Printf("  status:     CURRENT (running matches deployed)\n")
			} else {
				fmt.Printf("  status:     DRIFT (running digest differs from deployed)\n")
				fmt.Printf("  running:    %s\n", truncateDigest(drift.RunningDigest))
			}
		}
	}

	return nil
}

func statusFromConfig(name string, inst *Instance) error {
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
	fmt.Println("\n(no state file — run 'forge pipeline build' to start tracking)")
	return nil
}

// syncAll syncs all active instances.
func syncAll(cfg *Config) error {
	states, _ := ListStates()
	for _, state := range states {
		if state.Status != "active" {
			continue
		}
		fmt.Printf("=== Syncing %s\n", state.Name)
		if err := syncState(state); err != nil {
			fmt.Fprintf(os.Stderr, "  error: %v\n", err)
		}
	}
	return nil
}

// syncInstance syncs one instance.
func syncInstance(cfg *Config, name string) error {
	state, err := LoadState(name)
	if err != nil {
		// Fall back to config-based sync
		inst, instErr := cfg.GetInstance(name)
		if instErr != nil {
			return instErr
		}
		return syncFromConfig(inst)
	}
	return syncState(state)
}

func syncState(state *InstanceState) error {
	for repoName, repo := range state.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			fmt.Printf("  SKIP: %s not cloned\n", repoName)
			continue
		}

		currentBranch, _, _ := repoState(repo.Local)
		if currentBranch != repo.Branch {
			fmt.Printf("  SKIP: %s on %q, expected %q\n", repoName, currentBranch, repo.Branch)
			continue
		}

		fmt.Printf("  Pushing %s -> origin/%s\n", repoName, repo.Branch)
		if err := gitPush(repo.Local, repo.Branch); err != nil {
			return fmt.Errorf("pushing %s: %w", repoName, err)
		}

		// Update sync state
		now := time.Now()
		repo.LastSyncCommit = getHeadCommit(repo.Local)
		repo.LastSyncTime = &now
	}

	return SaveState(state)
}

func syncFromConfig(inst *Instance) error {
	for repoName, repo := range inst.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			continue
		}
		currentBranch, _, _ := repoState(repo.Local)
		if currentBranch != repo.Branch {
			continue
		}
		fmt.Printf("  Pushing %s -> origin/%s\n", repoName, repo.Branch)
		if err := gitPush(repo.Local, repo.Branch); err != nil {
			return fmt.Errorf("pushing %s: %w", repoName, err)
		}
	}
	return nil
}

// buildInstance builds container images and records state.
func buildInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	eppImage, ok := inst.Images["epp"]
	if !ok {
		return fmt.Errorf("instance %q has no 'epp' image defined", name)
	}

	schedulerRepo, ok := inst.Repos["llm-d-inference-scheduler"]
	if !ok {
		return fmt.Errorf("instance %q has no llm-d-inference-scheduler repo", name)
	}

	hasReplace := len(inst.ReplaceDirectives) > 0

	if hasReplace {
		err = buildWithReplace(inst, schedulerRepo, eppImage)
	} else {
		err = buildStandalone(schedulerRepo, eppImage)
	}
	if err != nil {
		return err
	}

	// Record build state
	state := loadOrInitState(name, inst)
	now := time.Now()
	digest, _ := getImageDigest(eppImage)

	buildCommits := make(map[string]string)
	for repoName, repo := range inst.Repos {
		buildCommits[repoName] = getHeadCommit(repo.Local)
	}

	state.Images["epp"] = &ImageState{
		Tag:          eppImage,
		Digest:       digest,
		BuildTime:    &now,
		BuildCommits: buildCommits,
		Pushed:       false,
	}

	return SaveState(state)
}

func buildWithReplace(inst *Instance, schedulerRepo *RepoConfig, imageTag string) error {
	parentDir := findCommonParent(inst)

	for repoName, repo := range inst.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			return fmt.Errorf("repo %s not found at %s", repoName, repo.Local)
		}
	}

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
		return err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(dockerfile)
	tmpFile.Close()

	fmt.Printf("Building %s (multi-repo, context: %s)\n", imageTag, parentDir)
	return runCmd(parentDir, "podman", "build", "-t", imageTag, "-f", tmpFile.Name(), ".")
}

func buildStandalone(schedulerRepo *RepoConfig, imageTag string) error {
	fmt.Printf("Building %s (standalone)\n", imageTag)
	return runCmd(schedulerRepo.Local, "podman", "build", "-t", imageTag, "-f", "Dockerfile.epp", ".")
}

// pushInstance pushes images and records state.
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
	if err := runCmd(".", "podman", "push", eppImage); err != nil {
		return err
	}

	// Update state
	state := loadOrInitState(name, inst)
	if img, ok := state.Images["epp"]; ok {
		now := time.Now()
		img.PushTime = &now
		img.Pushed = true
	}

	return SaveState(state)
}

// deployInstance deploys and records state with digest for verification.
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
		deploy = &DeployConfig{
			KubeContext:   "labctl-endor",
			Namespace:     "default",
			EPPDeployment: "vllm-pool-epp",
		}
	}

	fmt.Printf("Deploying %s to %s/%s\n", eppImage, deploy.KubeContext, deploy.EPPDeployment)

	// Annotate the deployment with forge metadata
	annotations := fmt.Sprintf("forge.hexfusion.io/instance=%s,forge.hexfusion.io/deployed-at=%s",
		name, time.Now().Format(time.RFC3339))

	if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "annotate", "deployment/"+deploy.EPPDeployment,
		annotations, "--overwrite"); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to annotate deployment: %v\n", err)
	}

	if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "set", "image",
		"deployment/"+deploy.EPPDeployment, "epp="+eppImage); err != nil {
		return err
	}

	fmt.Println("Waiting for rollout...")
	if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "rollout", "status",
		"deployment/"+deploy.EPPDeployment, "--timeout=120s"); err != nil {
		return err
	}

	// Record deploy state
	state := loadOrInitState(name, inst)
	now := time.Now()
	digest := ""
	if img, ok := state.Images["epp"]; ok {
		digest = img.Digest
	}

	deployCommits := make(map[string]string)
	for repoName, repo := range inst.Repos {
		deployCommits[repoName] = getHeadCommit(repo.Local)
	}

	state.Deploy = &DeployState{
		KubeContext:    deploy.KubeContext,
		Namespace:      deploy.Namespace,
		Deployment:     deploy.EPPDeployment,
		DeployedDigest: digest,
		DeployTime:     &now,
		DeployCommits:  deployCommits,
	}

	return SaveState(state)
}

// deployWithProfile deploys a full stack using a deploy profile.
func deployWithProfile(cfg *Config, name, profileName string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	profile, err := LoadDeployProfile(profileName)
	if err != nil {
		return err
	}

	// Load instance state to get built image info
	state := loadOrInitState(name, inst)

	fmt.Printf("Deploying full stack: profile=%s instance=%s cluster=%s\n\n",
		profileName, name, profile.KubeContext)

	if err := DeployStack(profile, state.Images); err != nil {
		return err
	}

	// Record deploy state
	now := time.Now()
	digest := ""
	if img, ok := state.Images["epp"]; ok {
		digest = img.Digest
	}

	deployCommits := make(map[string]string)
	for repoName, repo := range inst.Repos {
		deployCommits[repoName] = getHeadCommit(repo.Local)
	}

	state.Deploy = &DeployState{
		KubeContext:    profile.KubeContext,
		Namespace:      profile.Namespace,
		Deployment:     "vllm-pool-epp",
		DeployedDigest: digest,
		DeployTime:     &now,
		DeployCommits:  deployCommits,
	}

	return SaveState(state)
}

// --- helpers ---

func loadOrInitState(name string, inst *Instance) *InstanceState {
	state, err := LoadState(name)
	if err == nil {
		return state
	}

	// Initialize from config
	now := time.Now()
	repos := make(map[string]*RepoState)
	for repoName, repo := range inst.Repos {
		repos[repoName] = &RepoState{
			Fork:   repo.Fork,
			Branch: repo.Branch,
			Local:  repo.Local,
		}
	}

	return &InstanceState{
		Name:              name,
		Status:            inst.Status,
		Description:       inst.Description,
		Created:           now,
		Repos:             repos,
		Images:            make(map[string]*ImageState),
		ReplaceDirectives: inst.ReplaceDirectives,
		Proposal:          inst.Proposal,
	}
}

func findCommonParent(inst *Instance) string {
	// Find the common parent directory of all repos
	for _, repo := range inst.Repos {
		// Assume all repos are siblings under the same parent
		parts := strings.Split(repo.Local, "/")
		if len(parts) > 1 {
			return strings.Join(parts[:len(parts)-1], "/")
		}
	}
	return "."
}

func repoState(localPath string) (branch string, dirty bool, commit string) {
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		return "(not cloned)", false, ""
	}

	out, err := cmdOutput(localPath, "git", "branch", "--show-current")
	if err != nil {
		return "(unknown)", false, ""
	}
	branch = trimSpace(out)

	out, _ = cmdOutput(localPath, "git", "status", "--porcelain")
	dirty = trimSpace(out) != ""

	out, _ = cmdOutput(localPath, "git", "log", "--oneline", "-1")
	commit = trimSpace(out)

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

func truncateDigest(digest string) string {
	if len(digest) > 19 {
		return digest[:19] + "..."
	}
	return digest
}
