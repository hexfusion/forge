package benchmark

import (
	"fmt"
	"os"
	"time"

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
	cmd.AddCommand(newCaptureCommand())
	return cmd
}

func newCaptureCommand() *cobra.Command {
	var (
		configPath   string
		label        string
		outDir       string
		selectors    []string
		since        time.Duration
		promURL      string
		promSnapshot bool
		ctxFlag      string
		nsFlag       string
		eppFlag      string
	)

	cmd := &cobra.Command{
		Use:   "capture [instance]",
		Short: "Snapshot an arm's logs, pod status, and metrics for review",
		Long: `Capture the evidence for a benchmark arm: pod logs, a structured
manifest (images, restarts, OOM, the loaded EPP scheduling profile), and
optional Prometheus metrics. Run it DURING a run for live sanity and AFTER for
review — always before scale-down, since deleted-pod logs are gone.

Target either a forge instance, or an ad-hoc stack via --namespace (+ optional
--context/--epp) for deployments not managed as instances.

Prometheus: --prom-url does namespace-scoped range-queries (works on shared
clusters). --prom-snapshot additionally triggers the admin TSDB snapshot API
(needs --web.enable-admin-api; whole-TSDB; lands on the Prom pod's disk).

Output: <out>/<name>-<label>/{manifest.json, logs/, metrics/}.

Examples:
  forge benchmark capture adaptive-routing --label encheavy-cmb
  forge benchmark capture --namespace sbatsche-dev --context coreweave-waldof \
    --epp precise-affinity-epp -s llm-d.ai/role=decode -s app=mm-render --label cmb-4k`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			var d *pipeline.DeployConfig
			name := ""
			if nsFlag != "" { // ad-hoc bypass
				d = &pipeline.DeployConfig{KubeContext: ctxFlag, Namespace: nsFlag, EPPDeployment: eppFlag}
				name = nsFlag
				if len(args) == 1 {
					name = args[0]
				}
			} else {
				if len(args) != 1 {
					return fmt.Errorf("provide an instance name or --namespace")
				}
				cfg, err := pipeline.LoadConfig(configPath)
				if err != nil {
					return err
				}
				inst, err := cfg.GetInstance(args[0])
				if err != nil {
					return err
				}
				d = inst.Deploy
				name = args[0]
			}
			_, err := Capture(d, name, CaptureOptions{
				Label:        label,
				OutDir:       outDir,
				Selectors:    selectors,
				Since:        since,
				PromURL:      promURL,
				PromSnapshot: promSnapshot,
			}, os.Stderr)
			return err
		},
	}

	cmd.Flags().StringVar(&configPath, "config", "", "Pipeline config file (default: search standard locations)")
	cmd.Flags().StringVar(&label, "label", "", "Run label for the output dir (default: UTC timestamp)")
	cmd.Flags().StringVar(&outDir, "out", "", "Capture root dir (default: ./forge-captures)")
	cmd.Flags().StringArrayVarP(&selectors, "selector", "s", nil, "Extra pod label selector to log (repeatable)")
	cmd.Flags().DurationVar(&since, "since", time.Hour, "How far back to pull logs")
	cmd.Flags().StringVar(&promURL, "prom-url", "", "Prometheus base URL for namespace-scoped range-queries")
	cmd.Flags().BoolVar(&promSnapshot, "prom-snapshot", false, "Also trigger the admin TSDB snapshot API (needs --web.enable-admin-api)")
	cmd.Flags().StringVar(&ctxFlag, "context", "", "Ad-hoc kube context (bypass instance config)")
	cmd.Flags().StringVar(&nsFlag, "namespace", "", "Ad-hoc namespace (bypass instance config)")
	cmd.Flags().StringVar(&eppFlag, "epp", "", "Ad-hoc EPP deployment name prefix (bypass instance config)")
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
