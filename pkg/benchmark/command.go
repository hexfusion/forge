package benchmark

import (
	"fmt"
	"os"

	"github.com/hexfusion/forge/pkg/pipeline"
	"github.com/spf13/cobra"
)

// NewCommand returns the `forge benchmark` command tree.
func NewCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "benchmark",
		Aliases: []string{"bench"},
		Short:   "Run reproducible benchmarks against pipeline instances",
		Long: `Wraps llm-d-benchmark to drive harnesses (inference-perf, guidellm,
vllm-benchmark, etc.) against an existing forge pipeline instance.
Forge resolves the cluster + namespace + pool from the instance config;
llm-d-benchmark owns the harness and workload profiles.

Set FORGE_BENCH_DIR to override the llm-d-benchmark checkout location
(default: ~/projects/llm-d/llm-d-benchmark).`,
		Run: func(cmd *cobra.Command, args []string) { _ = cmd.Help() },
	}

	cmd.AddCommand(newRunCommand())
	return cmd
}

func newRunCommand() *cobra.Command {
	var (
		configPath  string
		workload    string
		harness     string
		methods     string
		overrides   string
		parallelism int
		benchDir    string
		skip        bool
		debug       bool
	)

	cmd := &cobra.Command{
		Use:   "run <instance>",
		Short: "Run a benchmark against an existing instance deployment",
		Long: `Run llm-d-benchmark against the resources already deployed for an
instance. Targeting (--methods) defaults to deploy.epp_deployment when
not supplied. Workload + harness fall back to the instance's bench:
config block if those flags aren't passed.

Examples:
  forge benchmark run adaptive-routing --workload sanity_random
  forge benchmark run adaptive-routing -w shared_prefix_synthetic -j 2
  forge benchmark run adaptive-routing -l guidellm -w chatbot_synthetic`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := pipeline.LoadConfig(configPath)
			if err != nil {
				return err
			}
			inst, err := cfg.GetInstance(args[0])
			if err != nil {
				return err
			}
			opts := RunOptions{
				Workload:       workload,
				Harness:        harness,
				Methods:        methods,
				Overrides:      overrides,
				Parallelism:    parallelism,
				BenchDir:       benchDir,
				SkipExperiment: skip,
				Debug:          debug,
			}
			fmt.Fprintf(os.Stderr, "Running benchmark for instance %q\n", args[0])
			return Run(inst, args[0], opts, os.Stdout, os.Stderr)
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Pipeline config file (default: search standard locations)")
	cmd.Flags().StringVarP(&workload, "workload", "w", "", "Workload profile (e.g. sanity_random, chatbot_synthetic)")
	cmd.Flags().StringVarP(&harness, "harness", "l", "", "Harness (default: inference-perf)")
	cmd.Flags().StringVarP(&methods, "methods", "t", "", "Target selector — pod/service/LLMIS name (default: deploy.epp_deployment)")
	cmd.Flags().StringVarP(&overrides, "overrides", "o", "", "Workload parameter overrides (passed to run.sh -o)")
	cmd.Flags().IntVarP(&parallelism, "parallelism", "j", 0, "Number of harness pods (passed to run.sh -j)")
	cmd.Flags().StringVar(&benchDir, "bench-dir", "", "llm-d-benchmark checkout location (default: $FORGE_BENCH_DIR or ~/projects/llm-d/llm-d-benchmark)")
	cmd.Flags().BoolVarP(&skip, "skip", "z", false, "Skip experiment, only collect data (passed to run.sh -z)")
	cmd.Flags().BoolVarP(&debug, "debug", "d", false, "Run harness in debug mode (passed to run.sh -d)")
	return cmd
}
