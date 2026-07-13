package bench_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/bench"
)

// TestDetectRealAssetsIsSafe verifies asset detection never panics and returns
// a clear reason when assets are absent. It runs in every environment.
func TestDetectRealAssetsIsSafe(t *testing.T) {
	cfg, available, reason := bench.DetectRealAssets()
	if !available && reason == "" {
		t.Fatalf("unavailable assets must carry a reason")
	}
	if available && (cfg.ServerBinaryPath == "" || cfg.ModelPath == "") {
		t.Fatalf("available config must have server and model paths, got %+v", cfg)
	}
	// Environment description must always be populated.
	env := bench.DescribeEnvironment(cfg)
	if env.OS == "" || env.Arch == "" || env.NumCPU == 0 {
		t.Fatalf("environment not populated: %+v", env)
	}
}

// TestRealBench runs the real-inference benchmark. It is gated behind both the
// AJQ_BENCH_REAL=1 opt-in and the presence of provisioned assets, so the normal
// `go test ./...` run never spawns a daemon or loads a model.
func TestIterativeLocalBench(t *testing.T) {
	if os.Getenv(bench.EnvRealBench) != "1" {
		t.Skipf("iterative local bench disabled: set %s=1 to enable", bench.EnvRealBench)
	}
	cfg, available, reason := bench.DetectRealAssets()
	if !available {
		t.Skipf("iterative local assets unavailable: %s", reason)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var workload bench.IterativeWorkload
	for _, candidate := range bench.IterativeWorkloads() {
		if candidate.Name == "high-prune" {
			workload = candidate
			break
		}
	}
	if workload.Name == "" {
		t.Fatal("high-prune workload missing")
	}
	report, err := bench.RunIterativeLocal(ctx, cfg, workload, 3)
	if err != nil {
		t.Fatalf("RunIterativeLocal: %v", err)
	}
	if len(report.Samples[bench.IterativeModeWindowed]) != 3 || len(report.Samples[bench.IterativeModeHarvest]) != 3 {
		t.Fatalf("incomplete local comparison: %+v", report.Samples)
	}
	t.Logf("iterative local comparison (informational; fake gate remains authoritative): %+v", report)
}

func TestRealBench(t *testing.T) {
	if os.Getenv(bench.EnvRealBench) != "1" {
		t.Skipf("real bench disabled: set %s=1 to enable", bench.EnvRealBench)
	}
	cfg, available, reason := bench.DetectRealAssets()
	if !available {
		t.Skipf("real bench assets unavailable: %s", reason)
	}

	// Cold start plus model load can take tens of seconds; give it room.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	w, err := bench.GenerateArray("sem_match/array", bench.QuerySemMatch, 64)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	report, err := bench.RunReal(ctx, cfg, w)
	if err != nil {
		t.Fatalf("RunReal: %v", err)
	}

	if report.ColdStart <= 0 {
		t.Fatalf("expected positive cold-start duration")
	}
	if report.WarmLatency <= 0 {
		t.Fatalf("expected positive warm latency")
	}
	if report.BatchJudgements == 0 {
		t.Fatalf("expected a non-empty batch")
	}
	if report.SequentialBatchLatency <= 0 || report.SequentialThroughput <= 0 {
		t.Fatalf("expected positive sequential batch metrics, got latency=%s throughput=%.2f", report.SequentialBatchLatency, report.SequentialThroughput)
	}
	if report.ParallelBatchLatency <= 0 || report.ParallelThroughput <= 0 {
		t.Fatalf("expected positive parallel batch metrics, got latency=%s throughput=%.2f", report.ParallelBatchLatency, report.ParallelThroughput)
	}
	if dir := strings.TrimSpace(os.Getenv(bench.EnvRealBenchReportDir)); dir != "" {
		path, err := bench.WriteRealReport(dir, report)
		if err != nil {
			t.Fatalf("WriteRealReport: %v", err)
		}
		t.Logf("wrote real bench report: %s", path)
	}

	t.Logf("real bench report:\n%s", bench.FormatRealReport(report))
}
