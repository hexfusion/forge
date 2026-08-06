// Package benchmark wraps llm-d-benchmark for forge instances.
//
// Scope: subprocess run.sh with the right --harness, --workload, and
// --methods flags resolved from the instance config. Streams output
// live. Does not own harnesses, workload profiles, or result schemas —
// llm-d-benchmark does. This is the thinnest viable integration so
// pipeline instances can run reproducible benchmarks without re-stating
// targeting at every invocation.
package benchmark

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/hexfusion/forge/pkg/pipeline"
)

// envBenchDir overrides the default llm-d-benchmark checkout location.
const envBenchDir = "FORGE_BENCH_DIR"

// defaultBenchSubdir is the default repo location relative to $HOME.
var defaultBenchSubdir = filepath.Join("projects", "llm-d", "llm-d-benchmark")

// defaultHarness is what gets used when BenchConfig.Harness is empty.
const defaultHarness = "inference-perf"

// RunOptions are the per-invocation parameters to merge with the
// instance's BenchConfig. Flag values on the command line override
// what's in the instance config.
type RunOptions struct {
	// Workload overrides BenchConfig.Workload when non-empty.
	Workload string
	// Harness overrides BenchConfig.Harness when non-empty.
	Harness string
	// Methods overrides BenchConfig.Methods when non-empty.
	Methods string
	// Overrides overrides BenchConfig.Overrides when non-empty.
	Overrides string
	// Parallelism overrides BenchConfig.Parallelism when > 0.
	Parallelism int
	// BenchDir overrides FORGE_BENCH_DIR + default location when non-empty.
	BenchDir string
	// SkipExperiment passes -z/--skip to run.sh (data-collection-only mode).
	SkipExperiment bool
	// Debug passes -d to run.sh.
	Debug bool
}

// Run executes llm-d-benchmark/run.sh against the given instance.
// Stdout/stderr stream to the supplied writers (use os.Stdout/Stderr
// for live tailing). Returns the subprocess exit error unchanged.
func Run(inst *pipeline.Instance, instName string, opts RunOptions, stdout, stderr *os.File) error {
	if inst == nil {
		return errors.New("instance is nil")
	}
	resolved, err := resolve(inst, opts)
	if err != nil {
		return err
	}
	benchDir, err := findBenchDir(opts.BenchDir)
	if err != nil {
		return err
	}
	args := buildArgs(resolved, opts)

	cmd := exec.Command(filepath.Join(benchDir, "run.sh"), args...)
	cmd.Dir = benchDir
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if inst.Deploy != nil && inst.Deploy.KubeContext != "" {
		// Surface the targeting in the env in case run.sh inspects it
		// (it consumes oc/kubectl from the calling shell's context).
		cmd.Env = append(cmd.Env, fmt.Sprintf("FORGE_INSTANCE=%s", instName))
		cmd.Env = append(cmd.Env, fmt.Sprintf("FORGE_KUBE_CONTEXT=%s", inst.Deploy.KubeContext))
	}
	return cmd.Run()
}

// resolved holds the merged BenchConfig + RunOptions, with defaults applied.
type resolved struct {
	Harness     string
	Workload    string
	Methods     string
	Overrides   string
	Parallelism int
}

// resolve merges options over instance config and applies defaults.
// Methods falls back to Deploy.EPPDeployment as the target when not set.
func resolve(inst *pipeline.Instance, opts RunOptions) (*resolved, error) {
	bc := inst.Bench
	if bc == nil {
		bc = &pipeline.BenchConfig{}
	}

	r := &resolved{
		Harness:     firstNonEmpty(opts.Harness, bc.Harness, defaultHarness),
		Workload:    firstNonEmpty(opts.Workload, bc.Workload),
		Methods:     firstNonEmpty(opts.Methods, bc.Methods),
		Overrides:   firstNonEmpty(opts.Overrides, bc.Overrides),
		Parallelism: bc.Parallelism,
	}
	if opts.Parallelism > 0 {
		r.Parallelism = opts.Parallelism
	}

	if r.Workload == "" {
		return nil, errors.New("workload required (set bench.workload in config or pass --workload)")
	}
	if r.Methods == "" {
		if inst.Deploy != nil && inst.Deploy.EPPDeployment != "" {
			r.Methods = inst.Deploy.EPPDeployment
		} else {
			return nil, errors.New("methods required (set bench.methods, or deploy.epp_deployment, or pass --methods)")
		}
	}
	return r, nil
}

func buildArgs(r *resolved, opts RunOptions) []string {
	args := []string{
		"--harness", r.Harness,
		"--workload", r.Workload,
		"--methods", r.Methods,
	}
	if r.Overrides != "" {
		args = append(args, "--overrides", r.Overrides)
	}
	if r.Parallelism > 0 {
		args = append(args, "--parallelism", fmt.Sprintf("%d", r.Parallelism))
	}
	if opts.SkipExperiment {
		args = append(args, "--skip")
	}
	if opts.Debug {
		args = append(args, "--debug")
	}
	return args
}

// findBenchDir resolves the llm-d-benchmark checkout to invoke.
// Authoritative priority: explicit override > FORGE_BENCH_DIR > $HOME default.
// A non-empty explicit/env value that doesn't contain run.sh errors
// rather than silently falling through, so users don't get a surprise
// dir different from what they asked for.
func findBenchDir(override string) (string, error) {
	if override != "" {
		return checkBenchDir(override, "--bench-dir")
	}
	if env := os.Getenv(envBenchDir); env != "" {
		return checkBenchDir(env, envBenchDir)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot resolve $HOME for default llm-d-benchmark location: %w", err)
	}
	def := filepath.Join(home, defaultBenchSubdir)
	if _, err := os.Stat(filepath.Join(def, "run.sh")); err == nil {
		return def, nil
	}
	return "", fmt.Errorf("llm-d-benchmark not found at %s; clone it or set %s", def, envBenchDir)
}

func checkBenchDir(dir, source string) (string, error) {
	if _, err := os.Stat(filepath.Join(dir, "run.sh")); err != nil {
		return "", fmt.Errorf("%s=%q has no run.sh: %w", source, dir, err)
	}
	return dir, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
