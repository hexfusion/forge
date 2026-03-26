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

	return cmd
}
