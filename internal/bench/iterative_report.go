package bench

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

// iterativeReportJSONView makes duration units explicit and fixes executor-mode
// order so JSON remains stable despite Go map iteration.
type iterativeReportJSONView struct {
	SchemaVersion            string                  `json:"schema_version"`
	Workload                 string                  `json:"workload"`
	Repetitions              int                     `json:"repetitions"`
	JudgementsAvoided        int                     `json:"judgements_avoided"`
	JudgementsAvoidedPercent float64                 `json:"judgements_avoided_percent"`
	Modes                    []iterativeModeJSONView `json:"modes"`
	Thresholds               []ThresholdResult       `json:"thresholds"`
}

type iterativeModeJSONView struct {
	Mode    IterativeMode             `json:"mode"`
	Summary iterativeSummaryJSONView  `json:"summary"`
	Samples []iterativeSampleJSONView `json:"samples"`
}

type iterativeSummaryJSONView struct {
	Samples                 int    `json:"samples"`
	MinLatencyNanos         int64  `json:"min_latency_nanos"`
	MedianLatencyNanos      int64  `json:"median_latency_nanos"`
	MaxLatencyNanos         int64  `json:"max_latency_nanos"`
	MedianAllocations       uint64 `json:"median_allocations"`
	MedianAllocationBytes   uint64 `json:"median_allocation_bytes"`
	MedianPeakRetainedBytes uint64 `json:"median_peak_retained_bytes"`
}

type iterativeSampleJSONView struct {
	DurationNanos     int64  `json:"duration_nanos"`
	Allocations       uint64 `json:"allocations"`
	AllocationBytes   uint64 `json:"allocation_bytes"`
	PeakRetainedBytes uint64 `json:"peak_retained_bytes"`
	ExecutionMode     string `json:"execution_mode"`
	PostDedupCalls    int    `json:"post_dedup_backend_calls"`
	BackendBatches    int    `json:"backend_batches"`
	CacheHits         int    `json:"cache_hits"`
	WindowCount       int64  `json:"window_count"`
}

// IterativeReportJSON serializes raw paired samples and their summaries with a
// stable v1 schema. It is the reproducible fake-evidence artifact, not a
// replacement for Go benchmark output.
func IterativeReportJSON(r IterativeReport) ([]byte, error) {
	view := iterativeReportJSONView{SchemaVersion: r.SchemaVersion, Workload: r.Workload, Repetitions: r.Repetitions, JudgementsAvoided: r.JudgementsAvoided, JudgementsAvoidedPercent: r.JudgementsAvoidedPercent, Thresholds: append([]ThresholdResult(nil), r.Thresholds...)}
	for _, mode := range []IterativeMode{IterativeModeWindowed, IterativeModeHarvest, IterativeModeInterleaved} {
		summary := r.Summaries[mode]
		mv := iterativeModeJSONView{Mode: mode, Summary: iterativeSummaryJSONView{Samples: summary.Samples, MinLatencyNanos: summary.MinLatency.Nanoseconds(), MedianLatencyNanos: summary.MedianLatency.Nanoseconds(), MaxLatencyNanos: summary.MaxLatency.Nanoseconds(), MedianAllocations: summary.MedianAllocations, MedianAllocationBytes: summary.MedianAllocationBytes, MedianPeakRetainedBytes: summary.MedianPeakRetainedBytes}}
		for _, sample := range r.Samples[mode] {
			mv.Samples = append(mv.Samples, iterativeSampleJSONView{DurationNanos: sample.Duration.Nanoseconds(), Allocations: sample.Allocations, AllocationBytes: sample.AllocationBytes, PeakRetainedBytes: sample.PeakRetainedBytes, ExecutionMode: string(sample.Stats.ExecutionMode), PostDedupCalls: sample.Stats.PostDedupBackendCalls, BackendBatches: sample.BackendBatches, CacheHits: sample.Stats.CacheHits, WindowCount: sample.Stats.WindowCount})
		}
		view.Modes = append(view.Modes, mv)
	}
	return json.MarshalIndent(view, "", "  ")
}

// FormatIterativeReport renders the paired timing range, allocation medians,
// memory high-water medians, and locked threshold decisions as stable text.
func FormatIterativeReport(r IterativeReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "schema=%s workload=%s repetitions=%d judgements_avoided=%d (%.2f%%)\n", r.SchemaVersion, r.Workload, r.Repetitions, r.JudgementsAvoided, r.JudgementsAvoidedPercent)
	w := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(w, "mode\tsamples\tlatency_min\tlatency_median\tlatency_max\tallocs_median\talloc_bytes_median\tpeak_retained_median")
	for _, mode := range []IterativeMode{IterativeModeWindowed, IterativeModeHarvest, IterativeModeInterleaved} {
		s := r.Summaries[mode]
		_, _ = fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%d\t%d\t%d\n", mode, s.Samples, s.MinLatency.Round(time.Microsecond), s.MedianLatency.Round(time.Microsecond), s.MaxLatency.Round(time.Microsecond), s.MedianAllocations, s.MedianAllocationBytes, s.MedianPeakRetainedBytes)
	}
	_ = w.Flush()
	for _, threshold := range r.Thresholds {
		fmt.Fprintf(&b, "threshold %s: actual=%.2f limit=%.2f pass=%t\n", threshold.Name, threshold.Actual, threshold.Threshold, threshold.Pass)
	}
	return b.String()
}

// IterativeReportsJSON serializes a deterministic workload-name ordering for
// command output that contains the complete fake corpus.
func IterativeReportsJSON(reports []IterativeReport) ([]byte, error) {
	ordered := append([]IterativeReport(nil), reports...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Workload < ordered[j].Workload })
	views := make([]json.RawMessage, 0, len(ordered))
	for _, report := range ordered {
		data, err := IterativeReportJSON(report)
		if err != nil {
			return nil, err
		}
		views = append(views, data)
	}
	return json.MarshalIndent(struct {
		SchemaVersion string            `json:"schema_version"`
		Reports       []json.RawMessage `json:"reports"`
	}{SchemaVersion: "1", Reports: views}, "", "  ")
}

var _ = time.Nanosecond
