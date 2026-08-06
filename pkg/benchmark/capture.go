// Capture snapshots a benchmark arm's evidence — pod logs, a structured
// manifest (images, restarts, OOM, loaded EPP config), and optional
// Prometheus range-queries — into a run-labeled directory.
//
// Why: GPU runs are expensive and deleted-pod logs/metrics are gone after
// teardown. Capture must run DURING a run (sanity) and AFTER (before any
// scale-down). Output is structured (manifest.json + per-metric JSON) so a
// future assert layer can gate on it without reparsing text.
package benchmark

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/hexfusion/forge/pkg/pipeline"
)

// CaptureOptions are the per-invocation capture parameters.
type CaptureOptions struct {
	// Label names the output dir: <OutDir>/<instance>-<Label>/.
	Label string
	// OutDir is the capture root (default: ./forge-captures).
	OutDir string
	// Selectors are extra label selectors for pods to log beyond the EPP
	// (e.g. "llm-d.ai/role=decode", "app=mm-render").
	Selectors []string
	// Since bounds how far back logs are pulled (default 1h).
	Since time.Duration
	// PromURL, when set, enables Prometheus range-queries over the run window.
	PromURL string
	// PromWindow is the look-back for Prom range-queries (default: Since).
	PromWindow time.Duration
	// PromSnapshot triggers the admin TSDB snapshot API (POST
	// /api/v1/admin/tsdb/snapshot). Full-fidelity but needs
	// --web.enable-admin-api, snapshots the whole TSDB, and lands on the
	// Prometheus pod's disk (retrieve from there). Use when we own the Prom.
	PromSnapshot bool
}

// Manifest is the structured, self-describing record of a captured arm.
// Stable shape: an assert layer consumes this, not the log text.
type Manifest struct {
	Instance    string      `json:"instance"`
	Label       string      `json:"label"`
	CapturedAt  string      `json:"capturedAt"`
	KubeContext string      `json:"kubeContext"`
	Namespace   string      `json:"namespace"`
	Versions    Versions    `json:"versions"`   // searchable metadata for the catalog (RFE #11)
	EPPScorers  string      `json:"eppScorers"` // the loaded profile line: proves which arm ran
	Pods        []PodStatus `json:"pods"`
	Sanity      []string    `json:"sanity"` // failed gates; empty == sane
}

// Versions is the searchable run metadata: what stack actually ran. Derived
// from the live pods + nodes so a catalog can filter ("all runs with vLLM
// 0.23 + H200"). Image tags are the source of truth for the build under test.
type Versions struct {
	Model            string `json:"model"`            // served model (model-server positional arg)
	ModelServerImage string `json:"modelServerImage"` // vLLM image ref (tag carries the version)
	EPPImage         string `json:"eppImage"`         // llm-d-router EPP image ref
	GPUProduct       string `json:"gpuProduct"`       // e.g. H200, from node labels
}

// PodStatus is the per-pod evidence an assert layer gates on.
type PodStatus struct {
	Name     string            `json:"name"`
	Phase    string            `json:"phase"`
	Ready    bool              `json:"ready"`
	Images   map[string]string `json:"images"`   // container -> image
	Restarts map[string]int    `json:"restarts"` // container -> restartCount
	LastTerm map[string]string `json:"lastTerm"` // container -> last terminated reason (OOMKilled etc.)
}

// Capture writes a snapshot to <OutDir>/<name>-<label>/. The deploy config
// comes from either a forge instance or ad-hoc --context/--namespace flags.
// Returns the output dir. Best-effort: a failure capturing one target does
// not abort the others (partial evidence beats none).
func Capture(d *pipeline.DeployConfig, name string, opts CaptureOptions, stderr io.Writer) (string, error) {
	if d == nil || d.Namespace == "" {
		return "", fmt.Errorf("capture needs a namespace (instance deploy config or --namespace)")
	}
	if opts.Since == 0 {
		opts.Since = time.Hour
	}
	if opts.PromWindow == 0 {
		opts.PromWindow = opts.Since
	}
	label := opts.Label
	if label == "" {
		label = time.Now().UTC().Format("20060102-150405")
	}
	root := opts.OutDir
	if root == "" {
		root = "forge-captures"
	}
	outDir := filepath.Join(root, fmt.Sprintf("%s-%s", name, label))
	for _, sub := range []string{"logs", "metrics"} {
		if err := os.MkdirAll(filepath.Join(outDir, sub), 0o755); err != nil {
			return "", err
		}
	}

	m := Manifest{
		Instance:    name,
		Label:       label,
		CapturedAt:  time.Now().UTC().Format(time.RFC3339),
		KubeContext: d.KubeContext,
		Namespace:   d.Namespace,
	}

	pods, err := capturePods(d, &m)
	if err != nil {
		fmt.Fprintf(stderr, "warn: pod status capture failed: %v\n", err)
	}
	m.Versions = captureVersions(d, pods)
	m.EPPScorers = captureEPPScorers(d, pods)

	// Logs: EPP pods (all containers) + any pod matching the extra selectors.
	logTargets := filterPods(pods, "app.kubernetes.io/name="+d.EPPDeployment, d.EPPDeployment)
	for _, sel := range opts.Selectors {
		sp, err := listPods(d, sel)
		if err != nil {
			fmt.Fprintf(stderr, "warn: selector %q: %v\n", sel, err)
			continue
		}
		logTargets = append(logTargets, sp...)
	}
	captureLogs(d, logTargets, opts.Since, filepath.Join(outDir, "logs"), stderr)

	if opts.PromURL != "" {
		capturePrometheus(opts.PromURL, d.Namespace, opts.PromWindow, filepath.Join(outDir, "metrics"), stderr)
		if opts.PromSnapshot {
			capturePromSnapshot(opts.PromURL, filepath.Join(outDir, "metrics"), stderr)
		}
	}

	m.Sanity = sanityGates(&m, d.EPPDeployment)
	writeJSON(filepath.Join(outDir, "manifest.json"), m, stderr)

	fmt.Fprintf(stderr, "capture: %s\n", outDir)
	if len(m.Sanity) == 0 {
		fmt.Fprintf(stderr, "  sanity: OK\n")
	} else {
		for _, g := range m.Sanity {
			fmt.Fprintf(stderr, "  SANITY FAIL: %s\n", g)
		}
	}
	return outDir, nil
}

// kubectl runs `kubectl --context <ctx> -n <ns> <args...>` and returns stdout.
func kubectl(d *pipeline.DeployConfig, args ...string) ([]byte, error) {
	base := []string{}
	if d.KubeContext != "" {
		base = append(base, "--context", d.KubeContext)
	}
	if d.Namespace != "" {
		base = append(base, "-n", d.Namespace)
	}
	return exec.Command("kubectl", append(base, args...)...).Output()
}

// podList is the slice of `kubectl get pods -o json` we care about.
type podList struct {
	Items []struct {
		Metadata struct {
			Name   string            `json:"name"`
			Labels map[string]string `json:"labels"`
		} `json:"metadata"`
		Spec struct {
			Containers []struct {
				Name  string   `json:"name"`
				Image string   `json:"image"`
				Args  []string `json:"args"`
			} `json:"containers"`
		} `json:"spec"`
		Status struct {
			Phase             string `json:"phase"`
			ContainerStatuses []struct {
				Name         string `json:"name"`
				Ready        bool   `json:"ready"`
				RestartCount int    `json:"restartCount"`
				LastState    struct {
					Terminated *struct {
						Reason string `json:"reason"`
					} `json:"terminated"`
				} `json:"lastState"`
			} `json:"containerStatuses"`
		} `json:"status"`
	} `json:"items"`
}

func capturePods(d *pipeline.DeployConfig, m *Manifest) (*podList, error) {
	out, err := kubectl(d, "get", "pods", "-o", "json")
	if err != nil {
		return nil, err
	}
	var pl podList
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil, err
	}
	for _, p := range pl.Items {
		ps := PodStatus{
			Name:     p.Metadata.Name,
			Phase:    p.Status.Phase,
			Ready:    true,
			Images:   map[string]string{},
			Restarts: map[string]int{},
			LastTerm: map[string]string{},
		}
		for _, c := range p.Spec.Containers {
			ps.Images[c.Name] = c.Image
		}
		for _, cs := range p.Status.ContainerStatuses {
			ps.Restarts[cs.Name] = cs.RestartCount
			if !cs.Ready {
				ps.Ready = false
			}
			if cs.LastState.Terminated != nil {
				ps.LastTerm[cs.Name] = cs.LastState.Terminated.Reason
			}
		}
		m.Pods = append(m.Pods, ps)
	}
	return &pl, nil
}

// captureEPPScorers extracts the scheduling-profile scorers+weights from the
// EPP ConfigMap — the "which arm ran" signature. The CM is the reliable source:
// under load the startup log line rotates out of kubelet retention, and our
// workflow restarts the EPP to change config so the CM == the loaded profile.
// The CM often holds several profile keys; read the exact one the EPP loads
// (its --config-file basename), not a random key.
func captureEPPScorers(d *pipeline.DeployConfig, pods *podList) string {
	if d.EPPDeployment == "" {
		return ""
	}
	out, err := kubectl(d, "get", "cm", d.EPPDeployment, "-o", "json")
	if err != nil {
		return ""
	}
	var cm struct {
		Data map[string]string `json:"data"`
	}
	if json.Unmarshal(out, &cm) != nil {
		return ""
	}
	if key := eppConfigKey(pods, d.EPPDeployment); key != "" {
		if v, ok := cm.Data[key]; ok {
			return scanProfileScorers(v)
		}
	}
	for _, v := range cm.Data { // fallback: any profile (less reliable with multiple keys)
		if strings.Contains(v, "schedulingProfiles") {
			return scanProfileScorers(v)
		}
	}
	return ""
}

// eppConfigKey returns the CM key the EPP loads, from its --config-file arg.
func eppConfigKey(pods *podList, eppPrefix string) string {
	if pods == nil {
		return ""
	}
	for _, p := range pods.Items {
		if !strings.HasPrefix(p.Metadata.Name, eppPrefix) {
			continue
		}
		for _, c := range p.Spec.Containers {
			if c.Name != "epp" {
				continue
			}
			for i, a := range c.Args {
				if strings.HasPrefix(a, "--config-file=") {
					return filepath.Base(strings.TrimPrefix(a, "--config-file="))
				}
				if a == "--config-file" && i+1 < len(c.Args) {
					return filepath.Base(c.Args[i+1])
				}
			}
		}
	}
	return ""
}

// scanProfileScorers pulls `pluginRef: X` + `weight: N` pairs out of the
// schedulingProfiles block (weighted scorers only; filters/pickers have no
// weight). Avoids a YAML dependency for a single shallow scan.
func scanProfileScorers(s string) string {
	in := false
	ref := ""
	var out []string
	for _, ln := range strings.Split(s, "\n") {
		t := strings.TrimSpace(ln)
		switch {
		case strings.HasPrefix(t, "schedulingProfiles"):
			in = true
		case !in:
			continue
		case strings.HasPrefix(t, "- pluginRef:"):
			ref = strings.TrimSpace(strings.TrimPrefix(t, "- pluginRef:"))
		case strings.HasPrefix(t, "pluginRef:"):
			ref = strings.TrimSpace(strings.TrimPrefix(t, "pluginRef:"))
		case strings.HasPrefix(t, "weight:") && ref != "":
			out = append(out, ref+":"+strings.TrimSpace(strings.TrimPrefix(t, "weight:")))
			ref = ""
		}
	}
	return strings.Join(out, ", ")
}

// captureVersions derives searchable stack metadata from the live pods + nodes.
func captureVersions(d *pipeline.DeployConfig, pods *podList) Versions {
	var v Versions
	if pods != nil {
		for _, p := range pods.Items {
			isDecode := strings.Contains(p.Metadata.Name, "decode")
			for _, c := range p.Spec.Containers {
				if c.Name == "epp" {
					v.EPPImage = c.Image
					continue
				}
				if c.Name == "modelserver" || strings.Contains(c.Image, "vllm") {
					if v.ModelServerImage != "" && !isDecode {
						continue // a decode match wins over render (vllm-openai-cpu)
					}
					v.ModelServerImage = c.Image
					for _, a := range c.Args { // first positional (non-flag) arg is the model
						if !strings.HasPrefix(a, "-") {
							v.Model = a
							break
						}
					}
				}
			}
		}
	}
	v.GPUProduct = gpuProduct(d)
	return v
}

// gpuProduct reads nvidia.com/gpu.product off the first GPU node (cluster-scoped
// read; best-effort, blank if not permitted).
func gpuProduct(d *pipeline.DeployConfig) string {
	args := []string{}
	if d.KubeContext != "" {
		args = append(args, "--context", d.KubeContext)
	}
	args = append(args, "get", "nodes", "-o", "json")
	out, err := exec.Command("kubectl", args...).Output()
	if err != nil {
		return ""
	}
	var nl struct {
		Items []struct {
			Metadata struct {
				Labels map[string]string `json:"labels"`
			} `json:"metadata"`
		} `json:"items"`
	}
	if json.Unmarshal(out, &nl) != nil {
		return ""
	}
	for _, n := range nl.Items {
		for _, key := range []string{"nvidia.com/gpu.product", "gpu.nvidia.com/class"} {
			if p := n.Metadata.Labels[key]; p != "" {
				return p
			}
		}
	}
	return ""
}

type podRef struct {
	name       string
	containers []string
}

func filterPods(pl *podList, _, namePrefix string) []podRef {
	var out []podRef
	if pl == nil {
		return out
	}
	for _, p := range pl.Items {
		if !strings.HasPrefix(p.Metadata.Name, namePrefix) {
			continue
		}
		pr := podRef{name: p.Metadata.Name}
		for _, c := range p.Spec.Containers {
			pr.containers = append(pr.containers, c.Name)
		}
		out = append(out, pr)
	}
	return out
}

func listPods(d *pipeline.DeployConfig, selector string) ([]podRef, error) {
	out, err := kubectl(d, "get", "pods", "-l", selector, "-o", "json")
	if err != nil {
		return nil, err
	}
	var pl podList
	if err := json.Unmarshal(out, &pl); err != nil {
		return nil, err
	}
	return filterPods(&pl, "", ""), nil // namePrefix "" => all in this selected list
}

func captureLogs(d *pipeline.DeployConfig, targets []podRef, since time.Duration, dir string, stderr io.Writer) {
	sinceArg := fmt.Sprintf("--since=%s", since.String())
	for _, t := range targets {
		for _, c := range t.containers {
			out, err := kubectl(d, "logs", t.name, "-c", c, sinceArg)
			if err != nil {
				// container may have no logs yet, or be the only container; try without -c
				out, err = kubectl(d, "logs", t.name, sinceArg)
				if err != nil {
					fmt.Fprintf(stderr, "warn: logs %s/%s: %v\n", t.name, c, err)
					continue
				}
			}
			fn := filepath.Join(dir, fmt.Sprintf("%s.%s.log", t.name, c))
			if err := os.WriteFile(fn, out, 0o644); err != nil {
				fmt.Fprintf(stderr, "warn: write %s: %v\n", fn, err)
			}
		}
	}
}

// promQueries are infra metrics Prometheus reliably has (filtered to our
// namespace). App metrics (llm_d_epp_*, vllm:*) need a ServiceMonitor and are
// better captured by direct /metrics scrape (a v2 follow-on).
var promQueries = map[string]string{
	"dcgm_gpu_util": `DCGM_FI_DEV_GPU_UTIL{namespace=%q}`,
	"container_cpu": `rate(container_cpu_usage_seconds_total{namespace=%q}[1m])`,
}

func capturePrometheus(promURL, ns string, window time.Duration, dir string, stderr io.Writer) {
	end := time.Now()
	start := end.Add(-window)
	for name, tmpl := range promQueries {
		q := fmt.Sprintf(tmpl, ns)
		u := fmt.Sprintf("%s/api/v1/query_range?query=%s&start=%d&end=%d&step=15s",
			strings.TrimRight(promURL, "/"), url.QueryEscape(q), start.Unix(), end.Unix())
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			fmt.Fprintf(stderr, "warn: prom %s: %v\n", name, err)
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		fn := filepath.Join(dir, name+".json")
		if err := os.WriteFile(fn, body, 0o644); err != nil {
			fmt.Fprintf(stderr, "warn: write %s: %v\n", fn, err)
		}
	}
}

// capturePromSnapshot triggers the admin TSDB snapshot API and records the
// returned snapshot name + how to retrieve it. Full-fidelity, but the snapshot
// lives on the Prometheus pod's disk (storage.tsdb.path/snapshots/<name>) and
// needs --web.enable-admin-api. Fails cleanly (404/405) when the admin API is
// off — that's the signal to rely on the range-query JSON instead.
func capturePromSnapshot(promURL, dir string, stderr io.Writer) {
	u := strings.TrimRight(promURL, "/") + "/api/v1/admin/tsdb/snapshot"
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintf(stderr, "warn: prom snapshot: %v\n", err)
		return
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	out := map[string]any{"httpStatus": resp.StatusCode, "response": json.RawMessage(body)}
	if resp.StatusCode != http.StatusOK {
		out["note"] = "snapshot unavailable — Prometheus needs --web.enable-admin-api; use the range-query JSON instead"
	} else {
		out["note"] = "snapshot is on the Prometheus pod at storage.tsdb.path/snapshots/<data.name>; retrieve with kubectl cp from the prom pod"
	}
	writeJSON(filepath.Join(dir, "prom-snapshot.json"), out, stderr)
}

// sanityGates returns the human-readable failed gates (empty == sane). These
// are the checks that, this session, we had to run by hand after the fact.
func sanityGates(m *Manifest, eppPrefix string) []string {
	var fails []string
	if m.EPPScorers == "" {
		fails = append(fails, "EPP scheduling profile not found in logs (which arm ran?)")
	}
	for _, p := range m.Pods {
		for c, reason := range p.LastTerm {
			if reason == "OOMKilled" {
				fails = append(fails, fmt.Sprintf("%s/%s previously OOMKilled", p.Name, c))
			}
		}
		for c, n := range p.Restarts {
			if n > 0 {
				fails = append(fails, fmt.Sprintf("%s/%s restarted %d time(s)", p.Name, c, n))
			}
		}
	}
	return fails
}

func writeJSON(path string, v any, stderr io.Writer) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "warn: marshal %s: %v\n", path, err)
		return
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		fmt.Fprintf(stderr, "warn: write %s: %v\n", path, err)
	}
}
