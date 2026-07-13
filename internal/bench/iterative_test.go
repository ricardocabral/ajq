package bench_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/bench"
)

func TestIterativeWorkloadsHaveControlledPairedOracles(t *testing.T) {
	workloads := bench.IterativeWorkloads()
	if len(workloads) != 6 {
		t.Fatalf("workloads = %d, want six", len(workloads))
	}
	seen := map[string]bool{}
	for _, workload := range workloads {
		seen[workload.Name] = true
		for _, mode := range []bench.IterativeMode{bench.IterativeModeWindowed, bench.IterativeModeHarvest, bench.IterativeModeInterleaved} {
			if _, ok := workload.Expected[mode]; !ok {
				t.Fatalf("%s missing %s oracle", workload.Name, mode)
			}
		}
		report, err := bench.RunIterativeFake(context.Background(), workload, 1)
		if err != nil {
			t.Fatalf("RunIterativeFake(%s): %v", workload.Name, err)
		}
		if len(report.Samples[bench.IterativeModeWindowed]) != 1 || len(report.Samples[bench.IterativeModeHarvest]) != 1 || len(report.Samples[bench.IterativeModeInterleaved]) != 1 {
			t.Fatalf("incomplete samples: %+v", report.Samples)
		}
	}
	for _, name := range []string{"high-prune", "low-prune", "no-prune", "repeated-cache-hit", "enum-gate", "multi-window"} {
		if !seen[name] {
			t.Errorf("missing %q", name)
		}
	}
}

// TestIterativeFakeEvidence is the deterministic decision-evidence command:
// go test -count=1 -run TestIterativeFakeEvidence -v ./internal/bench.
func TestIterativeFakeEvidence(t *testing.T) {
	var reports []bench.IterativeReport
	for _, workload := range bench.IterativeWorkloads() {
		report, err := bench.RunIterativeFake(context.Background(), workload, bench.DefaultIterativeRepetitions)
		if err != nil {
			t.Fatalf("RunIterativeFake(%s): %v", workload.Name, err)
		}
		reports = append(reports, report)
		t.Log("\n" + bench.FormatIterativeReport(report))
	}
	data, err := bench.IterativeReportsJSON(reports)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("iterative fake evidence JSON:\n%s", data)
}

func TestIterativeReportSerializationAndThresholdMath(t *testing.T) {
	var high, noPrune bench.IterativeReport
	for _, workload := range bench.IterativeWorkloads() {
		switch workload.Name {
		case "high-prune":
			var err error
			high, err = bench.RunIterativeFake(context.Background(), workload, 3)
			if err != nil {
				t.Fatal(err)
			}
		case "no-prune":
			var err error
			noPrune, err = bench.RunIterativeFake(context.Background(), workload, 3)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	if high.JudgementsAvoided != 7 || high.JudgementsAvoidedPercent < 25 || len(high.Thresholds) != 1 || !high.Thresholds[0].Pass {
		t.Fatalf("high prune report = %+v", high)
	}
	if len(noPrune.Thresholds) != 2 {
		t.Fatalf("no-prune thresholds = %+v", noPrune.Thresholds)
	}
	data, err := bench.IterativeReportJSON(high)
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		SchemaVersion string            `json:"schema_version"`
		Modes         []json.RawMessage `json:"modes"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.SchemaVersion != "1" || len(parsed.Modes) != 3 {
		t.Fatalf("serialized report = %s", data)
	}
	text := bench.FormatIterativeReport(high)
	if !strings.Contains(text, "judgements_avoided=7") || !strings.Contains(text, "threshold high-prune judgement reduction") {
		t.Fatalf("formatted report = %q", text)
	}
	all, err := bench.IterativeReportsJSON([]bench.IterativeReport{noPrune, high})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(all), `"reports"`) || !strings.Contains(string(all), `"schema_version": "1"`) {
		t.Fatalf("report collection = %s", all)
	}
}
