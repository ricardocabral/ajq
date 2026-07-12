package bench

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"
)

// String renders a stable lowercase name for a dataset shape.
func (s Shape) String() string {
	switch s {
	case ShapeArray:
		return "array"
	case ShapeNDJSON:
		return "ndjson"
	default:
		return "unknown"
	}
}

// FormatMetrics renders a set of fake-mode metrics as a stable, aligned text
// table. The columns are exactly the units Phase 4 window sizing tunes against:
// window bytes, frames, harvested vs post-dedup semantic judgements, backend
// batches, the resulting dedup ratio, and per-run latency.
func FormatMetrics(metrics []Metrics) string {
	var b strings.Builder
	w := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "workload\tshape\twindow_bytes\tframes\tharvested\tpost_dedup\tbatches\tdedup_ratio\tduration")
	for _, m := range metrics {
		_, _ = fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%d\t%d\t%d\t%.3f\t%s\n",
			m.Workload, m.Shape, m.WindowBytes, m.Frames,
			m.HarvestedJudgements, m.PostDedupJudgements, m.BackendBatches,
			m.DedupRatio, m.Duration.Round(time.Microsecond))
	}
	_ = w.Flush()
	return b.String()
}

// metricsJSONView is the JSON-serializable projection of Metrics. It uses stable
// snake_case keys and a string shape so downstream comparison tooling is not
// coupled to Go field names or enum ordinals.
type metricsJSONView struct {
	Workload            string  `json:"workload"`
	Shape               string  `json:"shape"`
	WindowBytes         int     `json:"window_bytes"`
	Frames              int     `json:"frames"`
	HarvestedJudgements int     `json:"harvested_judgements"`
	PostDedupJudgements int     `json:"post_dedup_judgements"`
	BackendBatches      int     `json:"backend_batches"`
	DedupRatio          float64 `json:"dedup_ratio"`
	DurationNanos       int64   `json:"duration_nanos"`
	EstimateStatus      string  `json:"estimate_status"`
}

// MetricsJSON serializes fake-mode metrics as indented JSON for run-over-run
// comparison. Durations are emitted in nanoseconds for lossless comparison.
func MetricsJSON(metrics []Metrics) ([]byte, error) {
	views := make([]metricsJSONView, len(metrics))
	for i, m := range metrics {
		views[i] = metricsJSONView{
			Workload:            m.Workload,
			Shape:               m.Shape.String(),
			WindowBytes:         m.WindowBytes,
			Frames:              m.Frames,
			HarvestedJudgements: m.HarvestedJudgements,
			PostDedupJudgements: m.PostDedupJudgements,
			BackendBatches:      m.BackendBatches,
			DedupRatio:          m.DedupRatio,
			DurationNanos:       m.Duration.Nanoseconds(),
			EstimateStatus:      m.EstimateStatus,
		}
	}
	return json.MarshalIndent(views, "", "  ")
}

// realReportJSONView is the JSON-serializable projection of a RealReport.
type realReportJSONView struct {
	SchemaVersion string `json:"schema_version"`
	Provenance    struct {
		RecordedAt  string `json:"recorded_at"`
		GoVersion   string `json:"go_version"`
		Machine     string `json:"machine,omitempty"`
		GitRevision string `json:"git_revision,omitempty"`
		ModelSHA256 string `json:"model_sha256,omitempty"`
		ModelBytes  int64  `json:"model_bytes,omitempty"`
	} `json:"provenance"`
	Environment struct {
		OS             string `json:"os"`
		Arch           string `json:"arch"`
		NumCPU         int    `json:"num_cpu"`
		MetalAvailable bool   `json:"metal_available"`
		ParallelSlots  int    `json:"parallel_slots"`
		ServerBinary   string `json:"server_binary"`
		ServerVersion  string `json:"server_version"`
		ModelFile      string `json:"model_file"`
		DaemonBaseURL  string `json:"daemon_base_url"`
		HyperfinePath  string `json:"hyperfine_path"`
	} `json:"environment"`
	Workload                    string  `json:"workload"`
	ColdStartNanos              int64   `json:"cold_start_nanos"`
	WarmLatencyNanos            int64   `json:"warm_latency_nanos"`
	SequentialBatchLatencyNanos int64   `json:"sequential_batch_latency_nanos"`
	SequentialThroughput        float64 `json:"sequential_throughput_judgements_per_sec"`
	ParallelBatchLatencyNanos   int64   `json:"parallel_batch_latency_nanos"`
	ParallelThroughput          float64 `json:"parallel_throughput_judgements_per_sec"`
	BatchJudgements             int     `json:"batch_judgements"`
	CachedBatchNanos            int64   `json:"cached_batch_nanos"`
	HyperfineMeanSecs           float64 `json:"hyperfine_mean_seconds,omitempty"`
}

// RealReportJSON serializes a real-inference report as indented JSON.
func RealReportJSON(r RealReport) ([]byte, error) {
	var v realReportJSONView
	v.SchemaVersion = "1"
	v.Provenance.RecordedAt = r.Provenance.RecordedAt.UTC().Format(time.RFC3339Nano)
	v.Provenance.GoVersion = r.Provenance.GoVersion
	v.Provenance.Machine = r.Provenance.Machine
	v.Provenance.GitRevision = r.Provenance.GitRevision
	v.Provenance.ModelSHA256 = r.Provenance.ModelSHA256
	v.Provenance.ModelBytes = r.Provenance.ModelBytes
	v.Environment.OS = r.Environment.OS
	v.Environment.Arch = r.Environment.Arch
	v.Environment.NumCPU = r.Environment.NumCPU
	v.Environment.MetalAvailable = r.Environment.MetalAvailable
	v.Environment.ParallelSlots = r.Environment.ParallelSlots
	v.Environment.ServerBinary = reportPathBase(r.Environment.ServerBinary)
	v.Environment.ServerVersion = r.Environment.ServerVersion
	v.Environment.ModelFile = reportPathBase(r.Environment.ModelPath)
	v.Environment.DaemonBaseURL = r.Environment.DaemonBaseURL
	v.Environment.HyperfinePath = reportPathBase(r.Environment.HyperfinePath)
	v.Workload = r.Workload
	v.ColdStartNanos = r.ColdStart.Nanoseconds()
	v.WarmLatencyNanos = r.WarmLatency.Nanoseconds()
	v.SequentialBatchLatencyNanos = r.SequentialBatchLatency.Nanoseconds()
	v.SequentialThroughput = r.SequentialThroughput
	v.ParallelBatchLatencyNanos = r.ParallelBatchLatency.Nanoseconds()
	v.ParallelThroughput = r.ParallelThroughput
	v.BatchJudgements = r.BatchJudgements
	v.CachedBatchNanos = r.CachedBatchLatency.Nanoseconds()
	if r.Hyperfine != nil {
		v.HyperfineMeanSecs = r.Hyperfine.MeanSeconds
	}
	return json.MarshalIndent(v, "", "  ")
}

// WriteRealReport writes a versioned JSON report to dir and returns its path.
// The report timestamp and workload become part of the filename, and an
// existing report is never overwritten.
func WriteRealReport(dir string, r RealReport) (string, error) {
	if strings.TrimSpace(dir) == "" {
		return "", fmt.Errorf("real benchmark report directory is empty")
	}
	if r.Provenance.RecordedAt.IsZero() {
		return "", fmt.Errorf("real benchmark report has no recorded_at timestamp")
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create real benchmark report directory: %w", err)
	}

	data, err := RealReportJSON(r)
	if err != nil {
		return "", fmt.Errorf("serialize real benchmark report: %w", err)
	}
	stamp := r.Provenance.RecordedAt.UTC()
	workload := strings.NewReplacer("/", "-", "\\", "-", " ", "-").Replace(strings.TrimSpace(r.Workload))
	if workload == "" {
		workload = "workload"
	}
	name := fmt.Sprintf("real-%s-%s-%09d.json", stamp.Format("20060102T150405Z"), workload, stamp.Nanosecond())
	path := filepath.Join(dir, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) //nolint:gosec // The filename is sanitized and the directory is an explicit benchmark-runner input.
	if err != nil {
		return "", fmt.Errorf("create real benchmark report %q: %w", path, err)
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Write(append(data, '\n')); err != nil {
		return "", fmt.Errorf("write real benchmark report %q: %w", path, err)
	}
	return path, nil
}

func reportPathBase(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	return filepath.Base(path)
}

// FormatRealReport renders a real-inference benchmark report as stable,
// human-readable text suitable for logging in STATUS.md or CI output.
func FormatRealReport(r RealReport) string {
	var b strings.Builder
	e := r.Environment
	fmt.Fprintf(&b, "environment: os=%s arch=%s cpus=%d metal=%t\n", e.OS, e.Arch, e.NumCPU, e.MetalAvailable)
	fmt.Fprintf(&b, "server:      %s\n", nonEmpty(e.ServerBinary))
	if e.ServerVersion != "" {
		fmt.Fprintf(&b, "version:     %s\n", e.ServerVersion)
	}
	fmt.Fprintf(&b, "model:       %s\n", nonEmpty(e.ModelPath))
	fmt.Fprintf(&b, "daemon:      %s\n", nonEmpty(e.DaemonBaseURL))
	fmt.Fprintf(&b, "workload:    %s\n", nonEmpty(r.Workload))
	fmt.Fprintf(&b, "cold_start:  %s\n", r.ColdStart)
	fmt.Fprintf(&b, "warm_latency: %s\n", r.WarmLatency)
	fmt.Fprintf(&b, "batch_sequential: %d judgements in %s (%.2f judgements/sec, MaxConcurrency=1)\n", r.BatchJudgements, r.SequentialBatchLatency, r.SequentialThroughput)
	fmt.Fprintf(&b, "batch_parallel:   %d judgements in %s (%.2f judgements/sec, MaxConcurrency=4)\n", r.BatchJudgements, r.ParallelBatchLatency, r.ParallelThroughput)
	fmt.Fprintf(&b, "cached_batch: %s (repeated-value cache effect)\n", r.CachedBatchLatency)
	if r.Hyperfine != nil {
		fmt.Fprintf(&b, "hyperfine:   mean=%.4fs min=%.4fs max=%.4fs runs=%d cmd=%q\n",
			r.Hyperfine.MeanSeconds, r.Hyperfine.MinSeconds, r.Hyperfine.MaxSeconds, r.Hyperfine.Runs, r.Hyperfine.Command)
	}
	return b.String()
}

func nonEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(unknown)"
	}
	return s
}
