package benchmark

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hexfusion/forge/pkg/pipeline"
)

func TestResolve(t *testing.T) {
	cases := []struct {
		name    string
		inst    *pipeline.Instance
		opts    RunOptions
		want    *resolved
		wantErr string
	}{
		{
			name:    "missing workload errors",
			inst:    &pipeline.Instance{Bench: &pipeline.BenchConfig{}},
			opts:    RunOptions{},
			wantErr: "workload required",
		},
		{
			name: "missing methods + no deploy errors",
			inst: &pipeline.Instance{Bench: &pipeline.BenchConfig{Workload: "sanity"}},
			opts: RunOptions{},
			wantErr: "methods required",
		},
		{
			name: "methods falls back to deploy.epp_deployment",
			inst: &pipeline.Instance{
				Bench:  &pipeline.BenchConfig{Workload: "sanity"},
				Deploy: &pipeline.DeployConfig{EPPDeployment: "adaptive-epp"},
			},
			opts: RunOptions{},
			want: &resolved{Harness: "inference-perf", Workload: "sanity", Methods: "adaptive-epp"},
		},
		{
			name: "options override config",
			inst: &pipeline.Instance{
				Bench: &pipeline.BenchConfig{
					Harness: "inference-perf", Workload: "sanity",
					Methods: "default-pod", Parallelism: 1,
				},
			},
			opts: RunOptions{
				Harness: "guidellm", Workload: "chatbot",
				Methods: "override-pod", Parallelism: 4,
			},
			want: &resolved{
				Harness: "guidellm", Workload: "chatbot",
				Methods: "override-pod", Parallelism: 4,
			},
		},
		{
			name: "default harness when nothing set",
			inst: &pipeline.Instance{Bench: &pipeline.BenchConfig{Workload: "sanity", Methods: "x"}},
			opts: RunOptions{},
			want: &resolved{Harness: "inference-perf", Workload: "sanity", Methods: "x"},
		},
		{
			name: "nil bench config + deploy fallback works",
			inst: &pipeline.Instance{
				Deploy: &pipeline.DeployConfig{EPPDeployment: "x"},
			},
			opts:    RunOptions{Workload: "sanity"},
			want:    &resolved{Harness: "inference-perf", Workload: "sanity", Methods: "x"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolve(tc.inst, tc.opts)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("want err containing %q, got %v", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if *got != *tc.want {
				t.Fatalf("resolved mismatch:\n got %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestBuildArgs(t *testing.T) {
	cases := []struct {
		name string
		r    *resolved
		opts RunOptions
		want []string
	}{
		{
			name: "minimum",
			r:    &resolved{Harness: "inference-perf", Workload: "sanity", Methods: "p"},
			want: []string{"--harness", "inference-perf", "--workload", "sanity", "--methods", "p"},
		},
		{
			name: "with overrides + parallelism",
			r: &resolved{
				Harness: "inference-perf", Workload: "x", Methods: "p",
				Overrides: "rate=10", Parallelism: 2,
			},
			want: []string{
				"--harness", "inference-perf", "--workload", "x", "--methods", "p",
				"--overrides", "rate=10", "--parallelism", "2",
			},
		},
		{
			name: "skip + debug flags",
			r:    &resolved{Harness: "inference-perf", Workload: "x", Methods: "p"},
			opts: RunOptions{SkipExperiment: true, Debug: true},
			want: []string{
				"--harness", "inference-perf", "--workload", "x", "--methods", "p",
				"--skip", "--debug",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := buildArgs(tc.r, tc.opts)
			if !equalSlices(got, tc.want) {
				t.Fatalf("args mismatch:\n got %v\nwant %v", got, tc.want)
			}
		})
	}
}

func TestFindBenchDir_PrefersOverride(t *testing.T) {
	dir := t.TempDir()
	if err := touchExecutable(filepath.Join(dir, "run.sh")); err != nil {
		t.Fatalf("setup: %v", err)
	}
	got, err := findBenchDir(dir)
	if err != nil || got != dir {
		t.Fatalf("want %q, got %q (err=%v)", dir, got, err)
	}
}

func TestFindBenchDir_ExplicitOverrideWithoutRunShErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, err := findBenchDir(t.TempDir()); err == nil {
		t.Fatal("explicit override missing run.sh must error, not fall through")
	}
}

func TestFindBenchDir_EnvWithoutRunShErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envBenchDir, t.TempDir())
	if _, err := findBenchDir(""); err == nil {
		t.Fatal("env override missing run.sh must error")
	}
}

func TestFindBenchDir_DefaultMissingErrors(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(envBenchDir, "")
	if _, err := findBenchDir(""); err == nil {
		t.Fatal("no override + no default = error")
	}
}

func touchExecutable(p string) error {
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Chmod(0o755)
}

func equalSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
