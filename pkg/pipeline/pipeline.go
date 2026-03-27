// Package pipeline manages multi-repo development instances.
//
// A pipeline instance tracks branches across multiple Git repos (e.g., GIE +
// llm-d-scheduler), their go.mod replace directives, container image tags,
// and deployment state. Branches are pushed to personal forks as the durable
// store. The instances config file is the index.
package pipeline

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// NewCommand returns the `forge pipeline` command tree.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pipeline",
		Short: "Multi-repo development pipelines",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	cmd.AddCommand(newCreateCommand())
	cmd.AddCommand(newDestroyCommand())
	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newSyncCommand())
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(newPushCommand())
	cmd.AddCommand(newDeployCommand())
	cmd.AddCommand(newShipCommand())
	cmd.AddCommand(newBenchCommand())
	cmd.AddCommand(newCompareCommand())

	return cmd
}

func newCreateCommand() *cobra.Command {
	var (
		project     string
		repos       string
		description string
	)

	cmd := &cobra.Command{
		Use:   "create <instance-name>",
		Short: "Create a new pipeline instance with git worktrees",
		Long: `Create a new pipeline instance from a project graph. For each target repo,
forge creates a git worktree with a new feature branch, resolves go.mod
replace directives, and writes instance config + state.

Example:
  forge pipeline create orca-metrics --project llm-d --repos gateway-api-inference-extension,llm-d-inference-scheduler`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if project == "" {
				return fmt.Errorf("--project is required")
			}
			if repos == "" {
				return fmt.Errorf("--repos is required")
			}

			repoList := splitRepos(repos)
			return createInstance(project, name, description, repoList)
		},
	}

	cmd.Flags().StringVar(&project, "project", "", "Project graph name (e.g., llm-d)")
	cmd.Flags().StringVar(&repos, "repos", "", "Comma-separated repos to include")
	cmd.Flags().StringVar(&description, "description", "", "Instance description")
	return cmd
}

func newDestroyCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "destroy <instance-name>",
		Short: "Destroy a pipeline instance, removing worktrees and state",
		Long: `Destroy a pipeline instance. This removes:
  - Git worktrees created by 'forge pipeline create'
  - The instance state file
  - The instance entry from the config file

Branches on the fork remote are NOT deleted (they are the durable record).
Use --force to skip confirmation.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return destroyInstance(args[0], force)
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Skip confirmation")
	return cmd
}

func splitRepos(s string) []string {
	var result []string
	for _, r := range strings.Split(s, ",") {
		r = strings.TrimSpace(r)
		if r != "" {
			result = append(result, r)
		}
	}
	return result
}

func newStatusCommand() *cobra.Command {
	var output string

	cmd := &cobra.Command{
		Use:   "status [instance]",
		Short: "Show instance state (repos, branches, images, deployments)",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return statusAll(cfg)
			}
			if output == "yaml" {
				return statusInstanceYAML(cfg, args[0])
			}
			return statusInstance(cfg, args[0])
		},
	}

	cmd.Flags().StringVarP(&output, "output", "o", "", "Output format (yaml)")
	return cmd
}

func newSyncCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "sync [instance]",
		Short: "Push all branches to forks",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			if len(args) == 0 {
				return syncAll(cfg)
			}
			return syncInstance(cfg, args[0])
		},
	}
}

func newBuildCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "build <instance>",
		Short: "Build container images for an instance",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			return buildInstance(cfg, args[0])
		},
	}
}

func newPushCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "push <instance>",
		Short: "Push container images to registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			return pushInstance(cfg, args[0])
		},
	}
}

func newDeployCommand() *cobra.Command {
	var profile string

	cmd := &cobra.Command{
		Use:   "deploy <instance>",
		Short: "Deploy instance images to lab cluster",
		Long: `Deploy instance images to a cluster. Without --profile, updates only the
EPP deployment image. With --profile, deploys the full stack using the
named deploy profile (e.g., llm-d-lab-pd).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}

			if profile != "" {
				return deployWithProfile(cfg, args[0], profile)
			}
			return deployInstance(cfg, args[0])
		},
	}

	cmd.Flags().StringVar(&profile, "profile", "", "deploy profile for full stack deployment (e.g., llm-d-lab-pd)")
	return cmd
}

func newShipCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "ship <instance>",
		Short: "Build, push, and deploy in one step",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			name := args[0]
			fmt.Printf("=== Building %s\n", name)
			if err := buildInstance(cfg, name); err != nil {
				return err
			}
			fmt.Printf("=== Pushing %s\n", name)
			if err := pushInstance(cfg, name); err != nil {
				return err
			}
			fmt.Printf("=== Deploying %s\n", name)
			return deployInstance(cfg, name)
		},
	}
}

func newBenchCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "bench <instance>",
		Short: "Run benchmark workload and capture performance snapshot",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := LoadConfig("")
			if err != nil {
				return err
			}
			_, err = RunBench(cfg, args[0])
			return err
		},
	}
}

func newCompareCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "compare <instance>",
		Short: "Compare the two most recent benchmark snapshots",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return CompareSnapshots(args[0])
		},
	}
}
