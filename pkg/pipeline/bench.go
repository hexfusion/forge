package pipeline

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/yaml.v3"
)

// BenchSnapshot captures the results of a single benchmark run.
type BenchSnapshot struct {
	Instance  string        `yaml:"instance"`
	Timestamp time.Time     `yaml:"timestamp"`
	Config    *BenchConfig  `yaml:"config"`
	Model     *ModelConfig  `yaml:"model"`
	Results   *BenchResults `yaml:"results"`
}

// BenchResults holds the computed benchmark metrics.
type BenchResults struct {
	TotalRequests   int           `yaml:"total_requests"`
	SuccessCount    int           `yaml:"success_count"`
	ErrorCount      int           `yaml:"error_count"`
	Duration        time.Duration `yaml:"duration"`
	RequestsPerSec  float64       `yaml:"requests_per_sec"`

	// Latency percentiles (ms)
	LatencyP50  float64 `yaml:"latency_p50_ms"`
	LatencyP95  float64 `yaml:"latency_p95_ms"`
	LatencyP99  float64 `yaml:"latency_p99_ms"`
	LatencyMean float64 `yaml:"latency_mean_ms"`
	LatencyMax  float64 `yaml:"latency_max_ms"`

	// Routing distribution — how evenly were requests spread?
	PodDistribution map[string]int `yaml:"pod_distribution"`
	DistributionSkew float64       `yaml:"distribution_skew"` // std dev / mean

	// ORCA-specific (populated when ORCA headers are present)
	OrcaUpdates       int     `yaml:"orca_updates"`
	OrcaStalenessP50  float64 `yaml:"orca_staleness_p50_ms,omitempty"`
	OrcaStalenessP99  float64 `yaml:"orca_staleness_p99_ms,omitempty"`

	// Per-request data for detailed analysis
	Requests []RequestResult `yaml:"requests,omitempty"`
}

// RequestResult captures a single request's outcome.
type RequestResult struct {
	Index       int           `yaml:"index"`
	LatencyMs   float64       `yaml:"latency_ms"`
	StatusCode  int           `yaml:"status_code"`
	Pod         string        `yaml:"pod,omitempty"`
	OrcaHeader  string        `yaml:"orca_header,omitempty"`
	Error       string        `yaml:"error,omitempty"`
}

// RunBench executes the benchmark workload defined in the instance config.
func RunBench(cfg *Config, instanceName string) (*BenchSnapshot, error) {
	inst, err := cfg.GetInstance(instanceName)
	if err != nil {
		return nil, err
	}

	bench := inst.Bench
	if bench == nil {
		return nil, fmt.Errorf("instance %q has no bench config", instanceName)
	}

	model := inst.Model
	if model == nil {
		return nil, fmt.Errorf("instance %q has no model config", instanceName)
	}

	deploy := inst.Deploy
	if deploy == nil {
		return nil, fmt.Errorf("instance %q has no deploy config", instanceName)
	}

	// Discover gateway endpoint
	gatewayEndpoint := bench.GatewayEndpoint
	if gatewayEndpoint == "" {
		ep, err := discoverGateway(deploy.KubeContext)
		if err != nil {
			return nil, fmt.Errorf("discovering gateway: %w", err)
		}
		gatewayEndpoint = ep
	}

	fmt.Printf("Benchmark: %s\n", instanceName)
	fmt.Printf("  Model:       %s\n", model.Name)
	fmt.Printf("  Gateway:     %s\n", gatewayEndpoint)
	fmt.Printf("  Workload:    %s\n", bench.Workload)
	fmt.Printf("  Concurrency: %d\n", bench.Concurrency)
	fmt.Printf("  Requests:    %d\n", bench.TotalRequests)
	fmt.Printf("  Stream:      %v\n", bench.Stream)
	fmt.Println()

	results := runWorkload(gatewayEndpoint, model, bench)

	snapshot := &BenchSnapshot{
		Instance:  instanceName,
		Timestamp: time.Now(),
		Config:    bench,
		Model:     model,
		Results:   results,
	}

	// Save snapshot
	if err := saveSnapshot(instanceName, snapshot); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to save snapshot: %v\n", err)
	}

	printResults(results)
	return snapshot, nil
}

func runWorkload(gateway string, model *ModelConfig, bench *BenchConfig) *BenchResults {
	results := make([]RequestResult, bench.TotalRequests)
	var wg sync.WaitGroup
	var completed atomic.Int32

	sem := make(chan struct{}, bench.Concurrency)
	start := time.Now()

	for i := 0; i < bench.TotalRequests; i++ {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int) {
			defer wg.Done()
			defer func() { <-sem }()

			result := sendRequest(gateway, model, bench, idx)
			results[idx] = result

			done := completed.Add(1)
			if int(done)%10 == 0 {
				fmt.Printf("  %d/%d requests complete\n", done, bench.TotalRequests)
			}
		}(i)
	}

	wg.Wait()
	duration := time.Since(start)

	return computeResults(results, duration)
}

func sendRequest(gateway string, model *ModelConfig, bench *BenchConfig, idx int) RequestResult {
	prompt := bench.PromptTemplate
	if prompt == "" {
		prompt = fmt.Sprintf("Hello, request number %d", idx)
	}

	body := map[string]any{
		"model":      model.Name,
		"prompt":     prompt,
		"max_tokens": bench.MaxTokens,
		"stream":     bench.Stream,
	}
	bodyJSON, _ := json.Marshal(body)

	start := time.Now()

	out, err := cmdOutput(".", "curl", "-s",
		"-w", "\n%{http_code}",
		"-H", "Content-Type: application/json",
		"-H", "endpoint-load-metrics-format: TEXT",
		"-D", "/dev/stderr",
		"-d", string(bodyJSON),
		fmt.Sprintf("http://%s/v1/completions", gateway),
	)

	latency := time.Since(start)
	result := RequestResult{
		Index:     idx,
		LatencyMs: float64(latency.Milliseconds()),
	}

	if err != nil {
		result.Error = err.Error()
		result.StatusCode = 0
		return result
	}

	// Parse status code from last line
	lines := splitLines(out)
	if len(lines) > 0 {
		fmt.Sscanf(lines[len(lines)-1], "%d", &result.StatusCode)
	}

	if result.StatusCode == 200 {
		// Success
	} else {
		result.Error = fmt.Sprintf("HTTP %d", result.StatusCode)
	}

	return result
}

func computeResults(requests []RequestResult, duration time.Duration) *BenchResults {
	r := &BenchResults{
		TotalRequests:   len(requests),
		Duration:        duration,
		PodDistribution: make(map[string]int),
		Requests:        requests,
	}

	var latencies []float64
	for _, req := range requests {
		if req.Error == "" && req.StatusCode == 200 {
			r.SuccessCount++
			latencies = append(latencies, req.LatencyMs)
		} else {
			r.ErrorCount++
		}
		if req.Pod != "" {
			r.PodDistribution[req.Pod]++
		}
		if req.OrcaHeader != "" {
			r.OrcaUpdates++
		}
	}

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		r.LatencyP50 = percentile(latencies, 50)
		r.LatencyP95 = percentile(latencies, 95)
		r.LatencyP99 = percentile(latencies, 99)
		r.LatencyMax = latencies[len(latencies)-1]

		sum := 0.0
		for _, l := range latencies {
			sum += l
		}
		r.LatencyMean = sum / float64(len(latencies))
	}

	if duration.Seconds() > 0 {
		r.RequestsPerSec = float64(r.SuccessCount) / duration.Seconds()
	}

	// Compute distribution skew
	if len(r.PodDistribution) > 1 {
		counts := make([]float64, 0, len(r.PodDistribution))
		for _, c := range r.PodDistribution {
			counts = append(counts, float64(c))
		}
		mean := 0.0
		for _, c := range counts {
			mean += c
		}
		mean /= float64(len(counts))
		variance := 0.0
		for _, c := range counts {
			variance += (c - mean) * (c - mean)
		}
		variance /= float64(len(counts))
		if mean > 0 {
			r.DistributionSkew = math.Sqrt(variance) / mean
		}
	}

	return r
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p / 100)
	return sorted[idx]
}

func splitLines(s string) []string {
	var lines []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			lines = append(lines, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		lines = append(lines, s[start:])
	}
	return lines
}

// --- snapshot persistence ---

func saveSnapshot(instanceName string, snap *BenchSnapshot) error {
	dir := filepath.Join(StateDir(), "bench", instanceName)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	filename := snap.Timestamp.Format("20060102-150405") + ".yaml"
	data, err := yaml.Marshal(snap)
	if err != nil {
		return err
	}

	path := filepath.Join(dir, filename)
	fmt.Printf("Snapshot saved: %s\n", path)
	return os.WriteFile(path, data, 0644)
}

// LoadSnapshots returns all benchmark snapshots for an instance, sorted by time.
func LoadSnapshots(instanceName string) ([]*BenchSnapshot, error) {
	dir := filepath.Join(StateDir(), "bench", instanceName)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}

	var snapshots []*BenchSnapshot
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			continue
		}
		var snap BenchSnapshot
		if err := yaml.Unmarshal(data, &snap); err != nil {
			continue
		}
		snapshots = append(snapshots, &snap)
	}

	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Timestamp.Before(snapshots[j].Timestamp)
	})

	return snapshots, nil
}

// CompareSnapshots prints a diff between the two most recent snapshots.
func CompareSnapshots(instanceName string) error {
	snapshots, err := LoadSnapshots(instanceName)
	if err != nil {
		return err
	}
	if len(snapshots) < 2 {
		return fmt.Errorf("need at least 2 snapshots to compare (have %d)", len(snapshots))
	}

	before := snapshots[len(snapshots)-2]
	after := snapshots[len(snapshots)-1]

	fmt.Printf("Comparing: %s vs %s\n\n",
		before.Timestamp.Format("2006-01-02 15:04:05"),
		after.Timestamp.Format("2006-01-02 15:04:05"))

	printDiff("Requests/sec", before.Results.RequestsPerSec, after.Results.RequestsPerSec, true)
	printDiff("Latency p50 (ms)", before.Results.LatencyP50, after.Results.LatencyP50, false)
	printDiff("Latency p95 (ms)", before.Results.LatencyP95, after.Results.LatencyP95, false)
	printDiff("Latency p99 (ms)", before.Results.LatencyP99, after.Results.LatencyP99, false)
	printDiff("Error rate", float64(before.Results.ErrorCount)/float64(before.Results.TotalRequests)*100,
		float64(after.Results.ErrorCount)/float64(after.Results.TotalRequests)*100, false)
	printDiff("Distribution skew", before.Results.DistributionSkew, after.Results.DistributionSkew, false)

	if after.Results.OrcaUpdates > 0 || before.Results.OrcaUpdates > 0 {
		fmt.Println("\nORCA:")
		printDiff("ORCA updates", float64(before.Results.OrcaUpdates), float64(after.Results.OrcaUpdates), true)
	}

	return nil
}

func printDiff(label string, before, after float64, higherIsBetter bool) {
	if before == 0 && after == 0 {
		return
	}

	delta := after - before
	pctChange := 0.0
	if before != 0 {
		pctChange = (delta / before) * 100
	}

	direction := ""
	if delta > 0 {
		if higherIsBetter {
			direction = " (better)"
		} else {
			direction = " (worse)"
		}
	} else if delta < 0 {
		if higherIsBetter {
			direction = " (worse)"
		} else {
			direction = " (better)"
		}
	}

	fmt.Printf("  %-25s %8.1f -> %8.1f  (%+.1f%%)%s\n",
		label, before, after, pctChange, direction)
}

func discoverGateway(kubeContext string) (string, error) {
	out, err := cmdOutput(".", "kubectl", "--context", kubeContext,
		"get", "gateway", "inference-gateway",
		"-o", "jsonpath={.status.addresses[0].value}")
	if err != nil {
		return "", err
	}
	addr := trimSpace(out)
	if addr == "" {
		return "", fmt.Errorf("gateway has no address")
	}
	return addr, nil
}

func printResults(r *BenchResults) {
	fmt.Printf("\nResults:\n")
	fmt.Printf("  Total:        %d requests in %s\n", r.TotalRequests, r.Duration.Round(time.Millisecond))
	fmt.Printf("  Success:      %d  Errors: %d\n", r.SuccessCount, r.ErrorCount)
	fmt.Printf("  Throughput:   %.1f req/s\n", r.RequestsPerSec)
	fmt.Printf("  Latency p50:  %.0f ms\n", r.LatencyP50)
	fmt.Printf("  Latency p95:  %.0f ms\n", r.LatencyP95)
	fmt.Printf("  Latency p99:  %.0f ms\n", r.LatencyP99)
	fmt.Printf("  Latency max:  %.0f ms\n", r.LatencyMax)

	if len(r.PodDistribution) > 0 {
		fmt.Printf("  Pod distribution:\n")
		for pod, count := range r.PodDistribution {
			fmt.Printf("    %-40s %d\n", pod, count)
		}
		fmt.Printf("  Distribution skew: %.2f\n", r.DistributionSkew)
	}

	if r.OrcaUpdates > 0 {
		fmt.Printf("  ORCA updates: %d\n", r.OrcaUpdates)
	}
}
