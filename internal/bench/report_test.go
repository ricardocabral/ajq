package bench_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/bench"
)

func TestFormatMetricsIncludesTuningFields(t *testing.T) {
	workloads, err := bench.StandardWorkloads(32)
	if err != nil {
		t.Fatalf("StandardWorkloads: %v", err)
	}
	metrics, err := bench.RunFakeSet(context.Background(), workloads)
	if err != nil {
		t.Fatalf("RunFakeSet: %v", err)
	}
	text := bench.FormatMetrics(metrics)
	// The window-tuning field labels Phase 4 depends on must be present.
	for _, field := range []string{"window_bytes", "frames", "harvested", "post_dedup", "batches", "dedup_ratio", "duration"} {
		if !strings.Contains(text, field) {
			t.Fatalf("formatted metrics missing field %q:\n%s", field, text)
		}
	}
	// Each workload name should appear on its own row.
	for _, w := range workloads {
		if !strings.Contains(text, w.Name) {
			t.Fatalf("formatted metrics missing workload %q", w.Name)
		}
	}
}

func TestMetricsJSONRoundTrips(t *testing.T) {
	workloads, err := bench.StandardWorkloads(32)
	if err != nil {
		t.Fatalf("StandardWorkloads: %v", err)
	}
	metrics, err := bench.RunFakeSet(context.Background(), workloads)
	if err != nil {
		t.Fatalf("RunFakeSet: %v", err)
	}
	data, err := bench.MetricsJSON(metrics)
	if err != nil {
		t.Fatalf("MetricsJSON: %v", err)
	}
	var decoded []map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal metrics json: %v", err)
	}
	if len(decoded) != len(metrics) {
		t.Fatalf("json has %d entries, want %d", len(decoded), len(metrics))
	}
	first := decoded[0]
	for _, key := range []string{"workload", "shape", "window_bytes", "frames", "harvested_judgements", "post_dedup_judgements", "backend_batches", "dedup_ratio", "duration_nanos"} {
		if _, ok := first[key]; !ok {
			t.Fatalf("json entry missing key %q: %v", key, first)
		}
	}
}

func TestRealReportJSONHasEnvelope(t *testing.T) {
	// A zero report still serializes with a stable envelope so comparison tools
	// can rely on the schema even when a real run was skipped.
	report := bench.RealReport{Workload: "sem_match/array"}
	report.Environment.OS = "darwin"
	report.Environment.Arch = "arm64"
	data, err := bench.RealReportJSON(report)
	if err != nil {
		t.Fatalf("RealReportJSON: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal real report json: %v", err)
	}
	for _, key := range []string{"schema_version", "provenance", "environment", "workload", "cold_start_nanos", "warm_latency_nanos", "sequential_batch_latency_nanos", "sequential_throughput_judgements_per_sec", "parallel_batch_latency_nanos", "parallel_throughput_judgements_per_sec", "cached_batch_nanos"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("real report json missing key %q: %v", key, decoded)
		}
	}
}

func TestWriteRealReportCreatesVersionedJSON(t *testing.T) {
	recordedAt := time.Date(2026, time.July, 12, 13, 14, 15, 0, time.UTC)
	report := bench.RealReport{
		Workload: "sem_match/array",
		Provenance: bench.RealProvenance{
			RecordedAt:  recordedAt,
			GoVersion:   "go1.test",
			GitRevision: "test-revision",
		},
	}
	path, err := bench.WriteRealReport(t.TempDir(), report)
	if err != nil {
		t.Fatalf("WriteRealReport: %v", err)
	}
	if base := filepath.Base(path); !strings.Contains(base, "20260712T131415Z") || !strings.Contains(base, "sem_match-array") {
		t.Fatalf("report filename = %q, want timestamp and workload", base)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is returned by the trusted WriteRealReport call above.
	if err != nil {
		t.Fatalf("read report: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal report: %v", err)
	}
	if got, want := decoded["schema_version"], "1"; got != want {
		t.Fatalf("schema_version = %v, want %q", got, want)
	}
}

func TestRealReportJSONUsesPortableAssetNames(t *testing.T) {
	report := bench.RealReport{}
	report.Environment.ServerBinary = "/private/tmp/llama/bin/llama-server"
	report.Environment.ModelPath = "/private/tmp/models/model.gguf"
	report.Environment.HyperfinePath = "/opt/homebrew/bin/hyperfine"
	data, err := bench.RealReportJSON(report)
	if err != nil {
		t.Fatalf("RealReportJSON: %v", err)
	}
	text := string(data)
	if strings.Contains(text, "/private/tmp") {
		t.Fatalf("report must not expose absolute local paths:\n%s", text)
	}
	for _, want := range []string{"llama-server", "model.gguf", "hyperfine"} {
		if !strings.Contains(text, want) {
			t.Fatalf("report missing portable asset name %q:\n%s", want, text)
		}
	}
}
