package main

import (
	"fmt"
	"os"

	"github.com/hexfusion/forge/pkg/cluster"
	"github.com/hexfusion/forge/pkg/pipeline"
	"github.com/spf13/cobra"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "forge",
		Short: "Developer workstation tool — clusters, pipelines, builds",
		Run: func(cmd *cobra.Command, args []string) {
			cmd.Help()
		},
	}

	cmd.AddCommand(cluster.NewCommand())
	cmd.AddCommand(pipeline.NewCommand())
	cmd.AddCommand(newCompletionCommand())

	return cmd
}

func newCompletionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "completion [bash|zsh|fish]",
		Short: "Generate shell completion scripts",
		Long: `Generate shell completion scripts for forge.

  # bash
  source <(forge completion bash)

  # zsh
  forge completion zsh > "${fpath[1]}/_forge"

  # fish
  forge completion fish | source`,
		Args:      cobra.ExactArgs(1),
		ValidArgs: []string{"bash", "zsh", "fish"},
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "bash":
				return cmd.Root().GenBashCompletion(os.Stdout)
			case "zsh":
				return cmd.Root().GenZshCompletion(os.Stdout)
			case "fish":
				return cmd.Root().GenFishCompletion(os.Stdout, true)
			default:
				return fmt.Errorf("unsupported shell: %s", args[0])
			}
		},
	}
}
