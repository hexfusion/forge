// Package pipeline manages multi-repo development instances.
//
// A pipeline instance tracks branches across multiple Git repos (e.g., GIE +
// llm-d-scheduler), their go.mod replace directives, container image tags,
// and deployment state. Branches are pushed to personal forks as the durable
// store. The instances config file is the index.
package pipeline

import (
	"fmt"

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

	cmd.AddCommand(newStatusCommand())
	cmd.AddCommand(newSyncCommand())
	cmd.AddCommand(newBuildCommand())
	cmd.AddCommand(newPushCommand())
	cmd.AddCommand(newDeployCommand())
	cmd.AddCommand(newShipCommand())

	return cmd
}

func newStatusCommand() *cobra.Command {
	return &cobra.Command{
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
			return statusInstance(cfg, args[0])
		},
	}
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
