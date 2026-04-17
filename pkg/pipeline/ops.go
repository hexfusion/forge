package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// statusAll prints a summary of all instances from the state store.
func statusAll(cfg *Config) error {
	states, err := ListStates()
	if err != nil || len(states) == 0 {
		return statusAllFromConfig(cfg)
	}

	fmt.Printf("%-20s %-8s %-8s %-8s %-10s %s\n",
		"INSTANCE", "STATUS", "BUILT", "PUSHED", "DEPLOYED", "DESCRIPTION")
	fmt.Printf("%-20s %-8s %-8s %-8s %-10s %s\n",
		"--------", "------", "-----", "------", "--------", "-----------")

	for _, state := range states {
		drift, _ := state.CheckDrift()
		built, pushed, deployed := "no", "no", "no"

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
	state, stateErr := LoadState(name)
	if stateErr == nil {
		return statusFromState(state)
	}

	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}
	return statusFromConfig(name, inst)
}

// statusInstanceYAML dumps the full instance config as YAML.
func statusInstanceYAML(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	out := map[string]any{
		name: inst,
		"_meta": map[string]string{
			"config_path": cfg.path,
		},
	}
	data, err := yaml.Marshal(out)
	if err != nil {
		return fmt.Errorf("marshaling instance: %w", err)
	}
	fmt.Print(string(data))
	return nil
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
		fmt.Printf("  %-40s -> %s\n", "", repo.Fork)
	}

	if len(state.ReplaceDirectives) > 0 {
		fmt.Println("\nReplace Directives:")
		for _, rd := range state.ReplaceDirectives {
			fmt.Printf("  %s go.mod: %s\n", rd.Source, rd.GoModLine)
		}
	}

	fmt.Println("\nImages:")
	for imgName, img := range state.Images {
		sourceStr := ""
		if img.Source == "external" {
			sourceStr = " [external]"
		}
		builtStr := "not built"
		if img.Source == "external" {
			builtStr = "external"
		} else if img.BuildTime != nil {
			builtStr = fmt.Sprintf("built %s", img.BuildTime.Format("2006-01-02 15:04"))
		}
		pushedStr := ""
		if img.Pushed && img.Source != "external" {
			pushedStr = " pushed"
		}
		fmt.Printf("  %-20s %s%s\n", imgName, img.Tag, sourceStr)
		fmt.Printf("  %-20s %s%s\n", "", builtStr, pushedStr)
		if img.EnvVar != "" {
			fmt.Printf("  %-20s -> %s\n", "", img.EnvVar)
		}
		if img.Digest != "" {
			fmt.Printf("  %-20s digest: %s\n", "", truncateDigest(img.Digest))
		}
	}

	if state.Deploy != nil {
		fmt.Printf("\nDeploy:\n")
		fmt.Printf("  cluster:    %s\n", state.Deploy.KubeContext)
		fmt.Printf("  namespace:  %s\n", state.Deploy.Namespace)
		if state.Deploy.Method != "" {
			fmt.Printf("  method:     %s\n", state.Deploy.Method)
		}
		if state.Deploy.Deployment != "" {
			fmt.Printf("  deployment: %s\n", state.Deploy.Deployment)
		}
		if state.Deploy.DeployedDigest != "" {
			fmt.Printf("  digest:     %s\n", truncateDigest(state.Deploy.DeployedDigest))
		}
		if len(state.Deploy.DeployedImages) > 0 {
			fmt.Printf("  images:\n")
			for imgName, ref := range state.Deploy.DeployedImages {
				fmt.Printf("    %-20s %s\n", imgName, ref)
			}
		}
		if state.Deploy.DeployTime != nil {
			fmt.Printf("  deployed:   %s\n", state.Deploy.DeployTime.Format("2006-01-02 15:04"))
		}

		drift, err := state.CheckDrift()
		if err == nil && drift.Deployed {
			if drift.DeployStale {
				fmt.Printf("  status:     STALE (rebuilt since last deploy)\n")
			} else if drift.DigestMatch {
				fmt.Printf("  status:     CURRENT\n")
			} else {
				fmt.Printf("  status:     DRIFT (running differs from deployed)\n")
				fmt.Printf("  running:    %s\n", truncateDigest(drift.RunningDigest))
			}
		}
	}

	if state.Validate != nil && len(state.Validate.Results) > 0 {
		fmt.Printf("\nValidate:\n")
		for _, r := range state.Validate.Results {
			status := "PASSED"
			if !r.Passed {
				status = fmt.Sprintf("FAILED (exit %d)", r.ExitCode)
			}
			timeStr := ""
			if r.RunTime != nil {
				timeStr = r.RunTime.Format("2006-01-02 15:04")
			}
			fmt.Printf("  %-20s %-8s %-10s %s\n", r.Name, status, r.Duration, timeStr)
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

// --- create ---

// createInstance creates a new pipeline instance from the project graph.
// For each repo: creates a git worktree with a new feature branch.
// Then writes the instance to the config file and creates initial state.
func createInstance(projectName, instanceName, description string, targetRepos []string) error {
	project, err := LoadProject(projectName)
	if err != nil {
		return err
	}

	// Check if instance already exists in config
	cfg, cfgErr := LoadConfig("")
	if cfgErr == nil {
		if _, err := cfg.GetInstance(instanceName); err == nil {
			return fmt.Errorf("instance %q already exists", instanceName)
		}
	}

	// Resolve the instance from the project graph
	inst, err := project.ResolveInstance(instanceName, targetRepos)
	if err != nil {
		return err
	}
	if description != "" {
		inst.Description = description
	}

	// Create git worktrees for each repo
	for repoName, repo := range inst.Repos {
		pr := project.Repos[repoName]
		mainLocal := expandHome(pr.Local)

		// Verify main checkout exists
		if _, err := os.Stat(mainLocal); os.IsNotExist(err) {
			return fmt.Errorf("repo %s not cloned at %s", repoName, mainLocal)
		}

		worktreePath := repo.Local
		fmt.Printf("  Creating worktree: %s\n", worktreePath)
		fmt.Printf("    from: %s\n", mainLocal)
		fmt.Printf("    branch: %s\n", repo.Branch)

		// Create parent directory
		if err := os.MkdirAll(worktreePath, 0755); err != nil {
			return fmt.Errorf("creating worktree dir for %s: %w", repoName, err)
		}
		// Remove the empty dir — git worktree add needs a non-existent target
		if err := os.Remove(worktreePath); err != nil {
			return fmt.Errorf("removing placeholder dir for %s: %w", repoName, err)
		}

		// Fetch latest from upstream before branching
		fmt.Printf("    fetching upstream...\n")
		if err := runCmd(mainLocal, "git", "fetch", "upstream"); err != nil {
			fmt.Fprintf(os.Stderr, "    warning: upstream fetch failed: %v (using cached code)\n", err)
		}

		// Create the worktree with a new branch from upstream/main
		if err := runCmd(mainLocal, "git", "worktree", "add",
			"-b", repo.Branch, worktreePath, "upstream/main"); err != nil {
			// Branch may already exist — try without -b
			if err2 := runCmd(mainLocal, "git", "worktree", "add",
				worktreePath, repo.Branch); err2 != nil {
				return fmt.Errorf("creating worktree for %s: %w (also tried existing branch: %v)", repoName, err, err2)
			}
		}

		fmt.Printf("    -> %s\n\n", worktreePath)
	}

	// Inject go.mod replace directives
	for _, rd := range inst.ReplaceDirectives {
		repo := inst.Repos[rd.Source]
		if repo == nil {
			continue
		}
		fmt.Printf("  Injecting replace: %s\n", rd.GoModLine)
		parts := strings.SplitN(rd.GoModLine, " => ", 2)
		if len(parts) != 2 {
			return fmt.Errorf("malformed replace directive: %s", rd.GoModLine)
		}
		modulePath := strings.TrimPrefix(parts[0], "replace ")
		localPath := parts[1]

		if err := runCmd(repo.Local, "go", "mod", "edit",
			"-replace", modulePath+"="+localPath); err != nil {
			return fmt.Errorf("injecting replace for %s: %w", rd.Source, err)
		}
	}

	// Write initial state
	state := &InstanceState{
		Name:              instanceName,
		Project:           projectName,
		Status:            "active",
		Description:       inst.Description,
		Created:           time.Now(),
		Repos:             make(map[string]*RepoState),
		Images:            make(map[string]*ImageState),
		ReplaceDirectives: inst.ReplaceDirectives,
		Proposal:          inst.Proposal,
	}
	for repoName, repo := range inst.Repos {
		state.Repos[repoName] = &RepoState{
			Fork:   repo.Fork,
			Branch: repo.Branch,
			Local:  repo.Local,
		}
	}
	for imgName, imgTag := range inst.Images {
		state.Images[imgName] = &ImageState{Tag: imgTag}
	}
	if inst.Deploy != nil {
		state.Deploy = &DeployState{
			KubeContext: inst.Deploy.KubeContext,
			Namespace:   inst.Deploy.Namespace,
			Deployment:  inst.Deploy.EPPDeployment,
		}
	}
	if err := SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	// Append to config file if we have one
	if cfgErr == nil && cfg != nil {
		cfg.Instances[instanceName] = inst
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update config file: %v\n", err)
		}
	}

	fmt.Printf("Instance %q created.\n", instanceName)
	fmt.Printf("  Worktrees ready — cd into any repo and start coding.\n")
	fmt.Printf("  Run 'forge pipeline status %s -o yaml' for full details.\n", instanceName)
	return nil
}

// --- destroy ---

// destroyInstance removes a pipeline instance: worktrees, state file, config entry.
// Branches on the fork remote are preserved as the durable record.
func destroyInstance(name string, force bool) error {
	// Try to load state first (has the worktree paths)
	state, stateErr := LoadState(name)

	// Also try config
	cfg, cfgErr := LoadConfig("")
	var inst *Instance
	if cfgErr == nil {
		inst, _ = cfg.GetInstance(name)
	}

	if stateErr != nil && inst == nil {
		return fmt.Errorf("instance %q not found in state or config", name)
	}

	if !force {
		fmt.Printf("This will destroy instance %q:\n", name)
		if state != nil {
			for repoName, repo := range state.Repos {
				fmt.Printf("  Remove worktree: %s (%s)\n", repo.Local, repoName)
			}
		} else if inst != nil {
			for repoName, repo := range inst.Repos {
				fmt.Printf("  Remove worktree: %s (%s)\n", repo.Local, repoName)
			}
		}
		fmt.Printf("  Delete state file\n")
		fmt.Printf("  Remove from config\n")
		fmt.Printf("  Branches on fork are NOT deleted.\n\n")
		fmt.Printf("Run with --force to confirm.\n")
		return nil
	}

	// Collect repo info from whichever source we have
	type repoInfo struct {
		local  string
		branch string
	}
	repos := make(map[string]repoInfo)
	if state != nil {
		for name, r := range state.Repos {
			repos[name] = repoInfo{local: r.Local, branch: r.Branch}
		}
	} else if inst != nil {
		for name, r := range inst.Repos {
			repos[name] = repoInfo{local: r.Local, branch: r.Branch}
		}
	}

	// Remove git worktrees
	for repoName, repo := range repos {
		if repo.local == "" {
			continue
		}
		if _, err := os.Stat(repo.local); os.IsNotExist(err) {
			fmt.Printf("  SKIP: %s worktree already gone (%s)\n", repoName, repo.local)
			continue
		}

		// Find the main repo that owns this worktree.
		// Worktree path: <parent>/.worktrees/<repoName>/<instance>
		// Main repo: <parent>/<repoName>
		mainRepo := findMainRepoForWorktree(repo.local, repoName)
		if mainRepo == "" {
			// Fallback: just remove the directory
			fmt.Printf("  Removing directory: %s\n", repo.local)
			if err := os.RemoveAll(repo.local); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not remove %s: %v\n", repo.local, err)
			}
			continue
		}

		fmt.Printf("  Removing worktree: %s (%s)\n", repoName, repo.local)
		if err := runCmd(mainRepo, "git", "worktree", "remove", repo.local, "--force"); err != nil {
			// Fallback: force remove directory and prune
			fmt.Fprintf(os.Stderr, "  warning: git worktree remove failed, cleaning up: %v\n", err)
			os.RemoveAll(repo.local)
			runCmd(mainRepo, "git", "worktree", "prune")
		}
	}

	// Clean up empty .worktrees directories
	for _, repo := range repos {
		if repo.local == "" {
			continue
		}
		// Walk up: <parent>/.worktrees/<repoName>/ then <parent>/.worktrees/
		instanceDir := repo.local
		repoWorktreeDir := filepath.Dir(instanceDir)
		worktreeBase := filepath.Dir(repoWorktreeDir)

		removeIfEmpty(repoWorktreeDir)
		removeIfEmpty(worktreeBase)
	}

	// Delete state file
	statePath := instanceStatePath(name)
	if _, err := os.Stat(statePath); err == nil {
		fmt.Printf("  Deleting state: %s\n", statePath)
		os.Remove(statePath)
	}

	// Remove from config
	if cfgErr == nil && cfg != nil {
		if _, exists := cfg.Instances[name]; exists {
			delete(cfg.Instances, name)
			if err := SaveConfig(cfg); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not update config: %v\n", err)
			} else {
				fmt.Printf("  Removed from config\n")
			}
		}
	}

	fmt.Printf("Instance %q destroyed.\n", name)
	return nil
}

// findMainRepoForWorktree derives the main repo path from a worktree path.
// Worktree convention: <parent>/.worktrees/<repoName>/<instance>
// Main repo:           <parent>/<repoName>
func findMainRepoForWorktree(worktreePath, repoName string) string {
	// worktreePath = /home/user/projects/llm-d/.worktrees/scheduler/orca-metrics
	// We want:       /home/user/projects/llm-d/scheduler
	instanceDir := worktreePath               // .../scheduler/orca-metrics
	repoWorktreeDir := filepath.Dir(instanceDir)    // .../scheduler
	worktreeBase := filepath.Dir(repoWorktreeDir)   // .../.worktrees
	parent := filepath.Dir(worktreeBase)             // .../llm-d

	if filepath.Base(worktreeBase) != ".worktrees" {
		return ""
	}

	mainRepo := filepath.Join(parent, repoName)
	if _, err := os.Stat(filepath.Join(mainRepo, ".git")); err == nil {
		return mainRepo
	}
	return ""
}

func removeIfEmpty(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) == 0 {
		os.Remove(dir)
	}
}

// --- sync ---

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

func syncInstance(cfg *Config, name string) error {
	state, err := LoadState(name)
	if err != nil {
		inst, instErr := cfg.GetInstance(name)
		if instErr != nil {
			return instErr
		}
		return syncFromConfig(inst)
	}
	return syncState(state)
}

func syncState(state *InstanceState) error {
	var skipped []string
	for repoName, repo := range state.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			return fmt.Errorf("repo %s: directory not found at %s", repoName, repo.Local)
		}
		currentBranch, _, _ := repoState(repo.Local)
		if currentBranch != repo.Branch {
			skipped = append(skipped, fmt.Sprintf("%s (on %q, expected %q)", repoName, currentBranch, repo.Branch))
			continue
		}
		fmt.Printf("  Pushing %s -> origin/%s\n", repoName, repo.Branch)
		if err := gitPush(repo.Local, repo.Branch); err != nil {
			return fmt.Errorf("pushing %s: %w", repoName, err)
		}
		now := time.Now()
		repo.LastSyncCommit = getHeadCommit(repo.Local)
		repo.LastSyncTime = &now
	}
	if err := SaveState(state); err != nil {
		return err
	}
	if len(skipped) > 0 {
		return fmt.Errorf("skipped repos (wrong branch): %s", strings.Join(skipped, ", "))
	}
	return nil
}

func syncFromConfig(inst *Instance) error {
	var skipped []string
	for repoName, repo := range inst.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			return fmt.Errorf("repo %s: directory not found at %s", repoName, repo.Local)
		}
		currentBranch, _, _ := repoState(repo.Local)
		if currentBranch != repo.Branch {
			skipped = append(skipped, fmt.Sprintf("%s (on %q, expected %q)", repoName, currentBranch, repo.Branch))
			continue
		}
		fmt.Printf("  Pushing %s -> origin/%s\n", repoName, repo.Branch)
		if err := gitPush(repo.Local, repo.Branch); err != nil {
			return fmt.Errorf("pushing %s: %w", repoName, err)
		}
	}
	if len(skipped) > 0 {
		return fmt.Errorf("skipped repos (wrong branch): %s", strings.Join(skipped, ", "))
	}
	return nil
}

// --- build ---

// buildInstance builds all images defined in the instance.
// External images (from ExternalImages) are recorded in state but not built.
func buildInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	if len(inst.Images) == 0 && len(inst.ExternalImages) == 0 {
		return fmt.Errorf("instance %q has no images defined", name)
	}

	state := loadOrInitState(name, inst)
	hasReplace := len(inst.ReplaceDirectives) > 0

	// Build locally-sourced images
	for imgName, imageTag := range inst.Images {
		fmt.Printf("=== Building image: %s (%s)\n", imgName, imageTag)

		// For pipeline-def instances, use the local path from the pipeline def
		if inst.PipelineFile != "" {
			def, defErr := LoadPipelineDef(inst.PipelineFile)
			if defErr == nil {
				if pImg, ok := def.Images[imgName]; ok && pImg.Local != "" {
					if err := buildStandalone(pImg.Local, imageTag); err != nil {
						return fmt.Errorf("building %s: %w", imgName, err)
					}
					goto recordBuild
				}
			}
		}

		if hasReplace {
			if err := buildWithReplace(inst, imgName, imageTag); err != nil {
				return fmt.Errorf("building %s: %w", imgName, err)
			}
		} else {
			repoPath := findBuildRepo(inst)
			if repoPath == "" {
				return fmt.Errorf("cannot determine build repo for image %s", imgName)
			}
			if err := buildStandalone(repoPath, imageTag); err != nil {
				return fmt.Errorf("building %s: %w", imgName, err)
			}
		}

	recordBuild:
		now := time.Now()
		digest, err := getImageDigest(imageTag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "  warning: could not retrieve digest for %s: %v\n", imageTag, err)
		}
		buildCommits := collectCommits(inst)

		state.Images[imgName] = &ImageState{
			Tag:          imageTag,
			Digest:       digest,
			Source:       "build",
			BuildTime:    &now,
			BuildCommits: buildCommits,
			Pushed:       false,
		}
	}

	// Record external images in state (no build needed)
	for imgName, ref := range inst.ExternalImages {
		fmt.Printf("  External: %s (%s)\n", imgName, ref)
		envVar := ""
		if inst.PipelineFile != "" {
			if def, err := LoadPipelineDef(inst.PipelineFile); err == nil {
				if pImg, ok := def.Images[imgName]; ok {
					envVar = pImg.EnvVar
				}
			}
		}
		state.Images[imgName] = &ImageState{
			Tag:    ref,
			Source: "external",
			EnvVar: envVar,
			Pushed: true, // already in registry
		}
	}

	return SaveState(state)
}

func buildWithReplace(inst *Instance, imgName, imageTag string) error {
	parentDir := findCommonParent(inst)

	for repoName, repo := range inst.Repos {
		if _, err := os.Stat(repo.Local); os.IsNotExist(err) {
			return fmt.Errorf("repo %s not found at %s", repoName, repo.Local)
		}
	}

	// Build a Containerfile that copies all instance repos into the context.
	// This is generic — it discovers repo directories from the instance config.
	repoNames := make([]string, 0, len(inst.Repos))
	for name := range inst.Repos {
		repoNames = append(repoNames, name)
	}

	// Find which repo has the main.go (the one with replace directives is the builder)
	builderRepo := ""
	for _, rd := range inst.ReplaceDirectives {
		builderRepo = rd.Source
		break
	}
	if builderRepo == "" {
		// Fall back to first repo
		for name := range inst.Repos {
			builderRepo = name
			break
		}
	}

	// Build config — use defaults if not configured
	builderBase := "quay.io/projectquay/golang:1.25"
	runtimeBase := "registry.access.redhat.com/ubi9/ubi-micro:9.7"
	buildTarget := fmt.Sprintf("cmd/%s/main.go", imgName)
	binaryName := imgName

	// Override from env if set (until project graph is threaded through)
	if v := os.Getenv("FORGE_BUILDER_BASE"); v != "" {
		builderBase = v
	}
	if v := os.Getenv("FORGE_RUNTIME_BASE"); v != "" {
		runtimeBase = v
	}
	if v := os.Getenv("FORGE_BUILD_TARGET"); v != "" {
		buildTarget = v
	}
	if v := os.Getenv("FORGE_BINARY_NAME"); v != "" {
		binaryName = v
	}

	// Generate Containerfile dynamically based on instance repos
	var df strings.Builder
	df.WriteString(fmt.Sprintf("FROM %s AS go-builder\n", builderBase))
	df.WriteString("ARG LDFLAGS=\"-s -w\"\n")
	df.WriteString("WORKDIR /workspace\n")

	// Copy go.mod/go.sum for all repos (for dependency caching)
	for _, name := range repoNames {
		df.WriteString(fmt.Sprintf("COPY %s/go.mod %s/go.sum ./%s/\n", name, name, name))
	}

	df.WriteString(fmt.Sprintf("WORKDIR /workspace/%s\n", builderRepo))
	df.WriteString("RUN go mod download\n")

	// Copy source for all repos
	for _, name := range repoNames {
		if name == builderRepo {
			df.WriteString(fmt.Sprintf("COPY %s/cmd/ ./cmd/\n", name))
			df.WriteString(fmt.Sprintf("COPY %s/pkg/ ./pkg/\n", name))
		} else {
			df.WriteString(fmt.Sprintf("COPY %s/ /workspace/%s/\n", name, name))
		}
	}

	df.WriteString(fmt.Sprintf("RUN CGO_ENABLED=0 go build -ldflags=\"${LDFLAGS}\" -o /workspace/bin/%s %s\n", binaryName, buildTarget))
	df.WriteString(fmt.Sprintf("FROM %s\n", runtimeBase))
	df.WriteString("WORKDIR /\n")
	df.WriteString(fmt.Sprintf("COPY --from=go-builder /workspace/bin/%s /app/%s\n", binaryName, binaryName))
	df.WriteString("USER 65532:65532\n")
	df.WriteString(fmt.Sprintf("ENTRYPOINT [\"/app/%s\"]\n", binaryName))

	tmpFile, err := os.CreateTemp("", "Containerfile.forge-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.WriteString(df.String())
	tmpFile.Close()

	fmt.Printf("Building %s (multi-repo from %s)\n", imageTag, parentDir)
	return runCmd(parentDir, "podman", "build", "-t", imageTag, "-f", tmpFile.Name(), ".")
}

func buildStandalone(repoPath, imageTag string) error {
	// Find the container build file — check Containerfile first, then Dockerfile variants
	containerfile := findContainerfile(repoPath)
	if containerfile == "" {
		return fmt.Errorf("no Containerfile or Dockerfile found in %s", repoPath)
	}
	fmt.Printf("Building %s (standalone from %s using %s)\n", imageTag, repoPath, containerfile)
	return runCmd(repoPath, "podman", "build", "-t", imageTag, "-f", containerfile, ".")
}

func findContainerfile(dir string) string {
	candidates := []string{"Containerfile", "Dockerfile", "Containerfile.epp", "Dockerfile.epp"}
	for _, name := range candidates {
		if _, err := os.Stat(dir + "/" + name); err == nil {
			return name
		}
	}
	return ""
}

func findBuildRepo(inst *Instance) string {
	for _, repo := range inst.Repos {
		if findContainerfile(repo.Local) != "" {
			return repo.Local
		}
	}
	return ""
}

// --- push ---

func pushInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	state := loadOrInitState(name, inst)

	for imgName, imageTag := range inst.Images {
		// Skip external images — already in registry
		if img, ok := state.Images[imgName]; ok && img.Source == "external" {
			fmt.Printf("  SKIP (external): %s (%s)\n", imgName, imageTag)
			continue
		}

		// Ensure quay.io repo exists before pushing
		if strings.Contains(imageTag, "quay.io") {
			if err := EnsureQuayRepo(imageTag); err != nil {
				fmt.Fprintf(os.Stderr, "  warning: could not ensure quay repo: %v\n", err)
			}
		}

		fmt.Printf("Pushing %s (%s)\n", imgName, imageTag)
		if err := runCmd(".", "podman", "push", imageTag); err != nil {
			return fmt.Errorf("pushing %s: %w", imgName, err)
		}

		if img, ok := state.Images[imgName]; ok {
			now := time.Now()
			img.PushTime = &now
			img.Pushed = true
		}
	}

	return SaveState(state)
}

// --- deploy ---

// deployInstance deploys images using the instance's deploy config.
// If the instance was created from a pipeline def with method "env-patch",
// it uses deployEnvPatch. Otherwise uses the original kubectl set image path.
func deployInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	deploy := inst.Deploy
	if deploy == nil {
		return fmt.Errorf("instance %q has no deploy config; add a 'deploy' section or use --profile", name)
	}

	state := loadOrInitState(name, inst)

	// Check if this is a pipeline-def instance with env-patch method
	if inst.PipelineFile != "" {
		def, defErr := LoadPipelineDef(inst.PipelineFile)
		if defErr == nil && def.Deploy != nil && def.Deploy.Method == "env-patch" {
			return deployEnvPatch(name, inst, def, state)
		}
	}

	// Original deploy path: kubectl set image
	for imgName, imageTag := range inst.Images {
		if deploy.EPPDeployment == "" {
			return fmt.Errorf("instance %q deploy config missing 'epp_deployment' for image %s", name, imgName)
		}

		fmt.Printf("Deploying %s to %s/%s\n", imageTag, deploy.KubeContext, deploy.EPPDeployment)

		// Annotate with forge metadata
		annotations := fmt.Sprintf(
			"forge.hexfusion.io/instance=%s,forge.hexfusion.io/image=%s,forge.hexfusion.io/deployed-at=%s",
			name, imgName, time.Now().Format(time.RFC3339))

		runCmd(".", "kubectl", "--context", deploy.KubeContext,
			"-n", deploy.Namespace, "annotate",
			"deployment/"+deploy.EPPDeployment, annotations, "--overwrite")

		if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
			"-n", deploy.Namespace, "set", "image",
			"deployment/"+deploy.EPPDeployment, imgName+"="+imageTag); err != nil {
			return err
		}
	}

	fmt.Println("Waiting for rollout...")
	if err := runCmd(".", "kubectl", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "rollout", "status",
		"deployment/"+deploy.EPPDeployment, "--timeout=120s"); err != nil {
		return err
	}

	// Record deploy state
	now := time.Now()
	firstDigest := ""
	for _, img := range state.Images {
		if img.Digest != "" {
			firstDigest = img.Digest
			break
		}
	}

	state.Deploy = &DeployState{
		KubeContext:    deploy.KubeContext,
		Namespace:      deploy.Namespace,
		Deployment:     deploy.EPPDeployment,
		DeployedDigest: firstDigest,
		DeployTime:     &now,
		DeployCommits:  collectCommits(inst),
	}

	return SaveState(state)
}

// deployEnvPatch patches RELATED_IMAGE_* env vars on the OLM-managed CSV.
// Patching the CSV instead of the deployment prevents OLM from reverting
// our changes. OLM propagates the CSV's env vars to the deployment.
// Uses `oc` instead of `kubectl` since this targets OCP/OLM clusters.
func deployEnvPatch(name string, inst *Instance, def *PipelineDef, state *InstanceState) error {
	deploy := def.Deploy
	fmt.Printf("=== env-patch: %s/%s (deploy/%s)\n",
		deploy.KubeContext, deploy.Namespace, deploy.TargetDeployment)

	// Collect desired env var overrides
	overrides := make(map[string]string) // envVarName -> imageRef
	deployedImages := make(map[string]string)

	for imgName, pImg := range def.Images {
		if pImg.EnvVar == "" {
			continue
		}

		imgState, ok := state.Images[imgName]
		if !ok {
			fmt.Printf("  SKIP: %s (not in state — run build first)\n", imgName)
			continue
		}

		ref := imgState.Tag
		fmt.Printf("  %s = %s\n", pImg.EnvVar, ref)
		overrides[pImg.EnvVar] = ref
		deployedImages[imgName] = ref
	}

	if len(overrides) == 0 {
		return fmt.Errorf("no images with env_var to inject for %s", name)
	}

	// Find the CSV name for this operator
	csvName, err := findCSV(deploy.KubeContext, deploy.Namespace, deploy.TargetDeployment)
	if err != nil {
		return fmt.Errorf("finding CSV: %w", err)
	}
	fmt.Printf("  CSV: %s\n", csvName)

	// Read the current env array from the CSV to find indices
	envJSON, err := cmdOutput(".", "oc", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "get", "csv", csvName,
		"-o", "jsonpath={.spec.install.spec.deployments[0].spec.template.spec.containers[0].env}")
	if err != nil {
		return fmt.Errorf("reading CSV env: %w", err)
	}

	// Parse the env array to find indices for each var we want to patch
	type envEntry struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var envs []envEntry
	if err := json.Unmarshal([]byte(envJSON), &envs); err != nil {
		return fmt.Errorf("parsing CSV env array: %w", err)
	}

	// Build JSON patch operations
	var patches []string
	for envVar, ref := range overrides {
		found := false
		for i, e := range envs {
			if e.Name == envVar {
				patches = append(patches,
					fmt.Sprintf(`{"op":"replace","path":"/spec/install/spec/deployments/0/spec/template/spec/containers/0/env/%d/value","value":"%s"}`, i, ref))
				found = true
				break
			}
		}
		if !found {
			// Append new env var if not found
			patches = append(patches,
				fmt.Sprintf(`{"op":"add","path":"/spec/install/spec/deployments/0/spec/template/spec/containers/0/env/-","value":{"name":"%s","value":"%s"}}`, envVar, ref))
		}
	}

	patchJSON := "[" + strings.Join(patches, ",") + "]"

	// Apply the JSON patch to the CSV
	fmt.Printf("  Patching CSV %s with %d env var overrides...\n", csvName, len(overrides))
	if err := runCmd(".", "oc", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "patch", "csv", csvName,
		"--type=json", "-p", patchJSON); err != nil {
		return fmt.Errorf("patching CSV: %w", err)
	}

	// OLM will now reconcile the deployment — wait for rollout
	fmt.Println("  Waiting for OLM to reconcile deployment...")
	if err := runCmd(".", "oc", "--context", deploy.KubeContext,
		"-n", deploy.Namespace, "rollout", "status",
		"deployment/"+deploy.TargetDeployment, "--timeout=180s"); err != nil {
		return err
	}

	// Record deploy state
	now := time.Now()
	state.Deploy = &DeployState{
		KubeContext:    deploy.KubeContext,
		Namespace:      deploy.Namespace,
		Deployment:     deploy.TargetDeployment,
		DeployedImages: deployedImages,
		DeployTime:     &now,
		DeployCommits:  collectCommits(inst),
		Method:         "env-patch",
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

	state := loadOrInitState(name, inst)

	fmt.Printf("Deploying full stack: profile=%s instance=%s cluster=%s\n\n",
		profileName, name, profile.KubeContext)

	if err := DeployStack(profile, state.Images); err != nil {
		return err
	}

	// Record deploy state — find the primary deployment from the profile
	// (the first component with an image override).
	now := time.Now()
	primaryDeployment := ""
	primaryDigest := ""
	for _, comp := range profile.Components {
		if comp.ImageOverride != nil {
			primaryDeployment = comp.Name
			if img, ok := state.Images[comp.ImageOverride.PipelineImage]; ok {
				primaryDigest = img.Digest
			}
			break
		}
	}

	state.Deploy = &DeployState{
		KubeContext:    profile.KubeContext,
		Namespace:      profile.Namespace,
		Deployment:     primaryDeployment,
		DeployedDigest: primaryDigest,
		DeployTime:     &now,
		DeployCommits:  collectCommits(inst),
	}

	return SaveState(state)
}

// --- validate ---

// validateInstance runs post-deploy validation steps from the pipeline def.
func validateInstance(cfg *Config, name string) error {
	inst, err := cfg.GetInstance(name)
	if err != nil {
		return err
	}

	if inst.PipelineFile == "" {
		return fmt.Errorf("instance %q has no pipeline file; validate requires --from pipeline def", name)
	}

	def, err := LoadPipelineDef(inst.PipelineFile)
	if err != nil {
		return err
	}

	if len(def.Validate) == 0 {
		return fmt.Errorf("pipeline def %q has no validate steps", def.Name)
	}

	state := loadOrInitState(name, inst)
	if state.Validate == nil {
		state.Validate = &ValidateState{}
	}
	// Reset results for this run
	state.Validate.Results = nil

	for _, step := range def.Validate {
		fmt.Printf("=== Validate: %s\n", step.Name)
		fmt.Printf("  %s\n", step.Command)

		result := runValidateStep(&step)
		state.Validate.Results = append(state.Validate.Results, result)

		if result.Passed {
			fmt.Printf("  PASSED (%s)\n\n", result.Duration)
		} else {
			fmt.Printf("  FAILED (exit %d, %s)\n\n", result.ExitCode, result.Duration)
		}
	}

	return SaveState(state)
}

func runValidateStep(step *ValidateStep) ValidateResult {
	timeout := step.Timeout
	if timeout == 0 {
		timeout = 300
	}

	dir := step.WorkingDir
	if dir == "" {
		dir = "."
	}

	start := time.Now()
	cmd := exec.Command("bash", "-c", step.Command)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	err := cmd.Run()
	duration := time.Since(start)
	now := time.Now()

	exitCode := 0
	passed := true
	if err != nil {
		passed = false
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = 1
		}
	}

	return ValidateResult{
		Name:     step.Name,
		Passed:   passed,
		ExitCode: exitCode,
		Duration: duration.Truncate(time.Second).String(),
		RunTime:  &now,
	}
}

// --- create from pipeline def ---

// createFromPipelineDef creates an instance from a standalone pipeline YAML.
// No project graph required. External images are recorded; build images get
// their local paths validated.
func createFromPipelineDef(path, name string) error {
	def, err := LoadPipelineDef(path)
	if err != nil {
		return err
	}

	if name == "" {
		name = def.Name
	}

	// Check if instance already exists
	cfg, cfgErr := LoadConfig("")
	if cfgErr == nil {
		if _, err := cfg.GetInstance(name); err == nil {
			return fmt.Errorf("instance %q already exists", name)
		}
	}

	inst := def.ToInstance(name)
	inst.PipelineFile = path

	// Also populate EnvVar in build images from the pipeline def
	for imgName, pImg := range def.Images {
		if !pImg.IsExternal() && pImg.Local != "" {
			// Validate the local path exists
			if _, err := os.Stat(pImg.Local); os.IsNotExist(err) {
				return fmt.Errorf("image %s: local path not found: %s", imgName, pImg.Local)
			}
		}
	}

	// Write initial state
	now := time.Now()
	state := &InstanceState{
		Name:        name,
		Status:      "active",
		Description: inst.Description,
		Created:     now,
		Repos:       make(map[string]*RepoState),
		Images:      make(map[string]*ImageState),
	}

	// Pre-populate image state entries
	for imgName, pImg := range def.Images {
		tag := pImg.ImageTag(imgName, name)
		if pImg.IsExternal() {
			state.Images[imgName] = &ImageState{
				Tag:    tag,
				Source: "external",
				EnvVar: pImg.EnvVar,
				Pushed: true,
			}
		} else {
			state.Images[imgName] = &ImageState{
				Tag:    tag,
				Source: "build",
				EnvVar: pImg.EnvVar,
			}
		}
	}

	if err := SaveState(state); err != nil {
		return fmt.Errorf("saving state: %w", err)
	}

	// Append to config file
	if cfgErr == nil && cfg != nil {
		cfg.Instances[name] = inst
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not update config file: %v\n", err)
		}
	} else {
		// Create a new config if none exists
		configPath := findConfig()
		if configPath == "" {
			configPath = expandHome("~/.config/forge/pipelines.yaml")
			os.MkdirAll(expandHome("~/.config/forge"), 0755)
		}
		cfg = &Config{
			path:      configPath,
			Instances: map[string]*Instance{name: inst},
		}
		if err := SaveConfig(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not create config file: %v\n", err)
		}
	}

	fmt.Printf("Instance %q created from %s\n", name, path)
	fmt.Printf("  Images: %d build, %d external\n", len(inst.Images), len(inst.ExternalImages))
	fmt.Printf("  Run 'forge pipeline build %s' to build local images.\n", name)
	if def.Deploy != nil {
		fmt.Printf("  Deploy method: %s -> %s\n", def.Deploy.Method, def.Deploy.TargetDeployment)
	}
	if len(def.Validate) > 0 {
		fmt.Printf("  Validate steps: %d\n", len(def.Validate))
	}

	return nil
}

// findCSV finds the ClusterServiceVersion that owns a given deployment.
func findCSV(kubeContext, namespace, deploymentName string) (string, error) {
	// List CSVs and find the one whose deployment matches
	out, err := cmdOutput(".", "oc", "--context", kubeContext,
		"-n", namespace, "get", "csv",
		"-o", "jsonpath={range .items[*]}{.metadata.name}{\"\\t\"}{.spec.install.spec.deployments[0].name}{\"\\n\"}{end}")
	if err != nil {
		return "", fmt.Errorf("listing CSVs: %w", err)
	}

	for _, line := range strings.Split(trimSpace(out), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		if len(parts) == 2 && trimSpace(parts[1]) == deploymentName {
			return trimSpace(parts[0]), nil
		}
	}

	return "", fmt.Errorf("no CSV found owning deployment %q in %s", deploymentName, namespace)
}

// --- helpers ---

func loadOrInitState(name string, inst *Instance) *InstanceState {
	state, err := LoadState(name)
	if err == nil {
		return state
	}

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

func collectCommits(inst *Instance) map[string]string {
	commits := make(map[string]string)
	for repoName, repo := range inst.Repos {
		commits[repoName] = getHeadCommit(repo.Local)
	}
	return commits
}

func findCommonParent(inst *Instance) string {
	for _, repo := range inst.Repos {
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
