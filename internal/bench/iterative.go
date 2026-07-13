package bench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/engine"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

// IterativeMode identifies one executor in the paired iterative-harvest corpus.
type IterativeMode string

const (
	// IterativeModeWindowed is the current default three-phase window executor.
	IterativeModeWindowed IterativeMode = "three-phase-windowed"
	// IterativeModeHarvest is the internal opt-in staged prototype.
	IterativeModeHarvest IterativeMode = "iterative-harvest"
	// IterativeModeInterleaved is the per-value reference executor.
	IterativeModeInterleaved IterativeMode = "interleaved"
	// DefaultIterativeRepetitions is the fixed number of timed paired samples.
	DefaultIterativeRepetitions = 21
)

// IterativeExpected records a deterministic workload oracle. Calls count actual
// post-dedup backend judgements, not harvested callbacks.
type IterativeExpected struct {
	Output  string
	Calls   int
	Batches int
}

// IterativeWorkload is a fixed-input, controlled-fake benchmark row. WarmCache
// intentionally primes an independent shared store before every measured run.
type IterativeWorkload struct {
	Workload
	WarmCache bool
	Expected  map[IterativeMode]IterativeExpected
}

// IterativeSample is one complete executor run. Allocation and retained-memory
// values include compilation, input framing, cache and discarded output.
type IterativeSample struct {
	Mode              IterativeMode   `json:"mode"`
	Duration          time.Duration   `json:"duration"`
	Allocations       uint64          `json:"allocations"`
	AllocationBytes   uint64          `json:"allocation_bytes"`
	PeakRetainedBytes uint64          `json:"peak_retained_bytes"`
	Stats             engine.RunStats `json:"stats"`
	BackendBatches    int             `json:"backend_batches"`
	Output            string          `json:"-"`
}

// IterativeSummary contains deterministic min/median/max summaries for one mode.
type IterativeSummary struct {
	Samples                 int           `json:"samples"`
	MinLatency              time.Duration `json:"min_latency"`
	MedianLatency           time.Duration `json:"median_latency"`
	MaxLatency              time.Duration `json:"max_latency"`
	MedianAllocations       uint64        `json:"median_allocations"`
	MedianAllocationBytes   uint64        `json:"median_allocation_bytes"`
	MedianPeakRetainedBytes uint64        `json:"median_peak_retained_bytes"`
}

// ThresholdResult records the locked prototype decision thresholds. A zero
// baseline has no percentage: equal zero passes, any positive candidate fails.
type ThresholdResult struct {
	Name      string  `json:"name"`
	Actual    float64 `json:"actual"`
	Threshold float64 `json:"threshold"`
	Pass      bool    `json:"pass"`
	ZeroBase  bool    `json:"zero_baseline"`
}

// IterativeReport is a versioned, reproducible paired fake evidence record.
type IterativeReport struct {
	SchemaVersion            string                              `json:"schema_version"`
	Workload                 string                              `json:"workload"`
	Repetitions              int                                 `json:"repetitions"`
	Samples                  map[IterativeMode][]IterativeSample `json:"-"`
	Summaries                map[IterativeMode]IterativeSummary  `json:"summaries"`
	JudgementsAvoided        int                                 `json:"judgements_avoided"`
	JudgementsAvoidedPercent float64                             `json:"judgements_avoided_percent"`
	Thresholds               []ThresholdResult                   `json:"thresholds"`
}

// IterativeWorkloads returns the complete deterministic paired corpus. The
// values themselves encode controlled fake decisions (pass/drop and enum
// labels), making pruning independent of MockBackend heuristics.
func IterativeWorkloads() []IterativeWorkload {
	chain := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes")) | .id`
	makeArray := func(name string, rows []string) Workload {
		return Workload{Name: name, Query: chain, Input: []byte("[" + strings.Join(rows, ",") + "]"), Mode: input.ModeAuto, Shape: ShapeArray, Records: len(rows), Distinct: len(rows)}
	}
	rows := func(pass int) []string {
		out := make([]string, 8)
		for i := range out {
			first := "drop"
			if i < pass {
				first = "pass"
			}
			out[i] = fmt.Sprintf(`{"id":%d,"first":"%s-first-%d","second":"pass-second-%d"}`, i+1, first, i+1, i+1)
		}
		return out
	}
	expected := func(output string, windowed, iterative, interleaved IterativeExpected) map[IterativeMode]IterativeExpected {
		return map[IterativeMode]IterativeExpected{IterativeModeWindowed: windowed, IterativeModeHarvest: iterative, IterativeModeInterleaved: interleaved}
	}
	high := makeArray("high-prune", rows(1))
	low := makeArray("low-prune", rows(7))
	none := makeArray("no-prune", rows(8))
	repeated := Workload{Name: "repeated-cache-hit", Query: chain, Input: []byte(`[{"id":1,"first":"pass-first","second":"pass-second"},{"id":2,"first":"pass-first","second":"pass-second"},{"id":3,"first":"drop-first","second":"pass-second"}]`), Mode: input.ModeAuto, Shape: ShapeArray, Records: 3, Distinct: 2}
	enum := Workload{Name: "enum-gate", Query: `.[] | select(sem_classify(.kind; "primary"; "secondary"; "other") == "primary") | select(sem_match(.second; "yes")) | .id`, Input: []byte(`[{"id":1,"kind":"primary-kind-1","second":"pass-second-1"},{"id":2,"kind":"secondary-kind-2","second":"pass-second-2"},{"id":3,"kind":"other-kind-3","second":"pass-second-3"}]`), Mode: input.ModeAuto, Shape: ShapeArray, Records: 3, Distinct: 3}
	multi := Workload{Name: "multi-window", Query: `. | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes")) | .id`, Input: []byte("{\"id\":1,\"first\":\"pass-first-1\",\"second\":\"pass-second-1\"}\n{\"id\":2,\"first\":\"drop-first-2\",\"second\":\"pass-second-2\"}\n{\"id\":3,\"first\":\"pass-first-3\",\"second\":\"pass-second-3\"}\n"), Mode: input.ModeAuto, Shape: ShapeNDJSON, WindowBytes: 1, Records: 3, Distinct: 3}
	return []IterativeWorkload{
		{Workload: high, Expected: expected("1\n", IterativeExpected{"1\n", 16, 1}, IterativeExpected{"1\n", 9, 2}, IterativeExpected{"1\n", 9, 9})},
		{Workload: low, Expected: expected("1\n2\n3\n4\n5\n6\n7\n", IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n", 16, 1}, IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n", 15, 2}, IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n", 15, 15})},
		{Workload: none, Expected: expected("1\n2\n3\n4\n5\n6\n7\n8\n", IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n8\n", 16, 1}, IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n8\n", 16, 2}, IterativeExpected{"1\n2\n3\n4\n5\n6\n7\n8\n", 16, 16})},
		{Workload: repeated, WarmCache: true, Expected: expected("1\n2\n", IterativeExpected{"1\n2\n", 0, 0}, IterativeExpected{"1\n2\n", 0, 0}, IterativeExpected{"1\n2\n", 0, 0})},
		{Workload: enum, Expected: expected("1\n", IterativeExpected{"1\n", 6, 1}, IterativeExpected{"1\n", 4, 2}, IterativeExpected{"1\n", 4, 4})},
		{Workload: multi, Expected: expected("1\n3\n", IterativeExpected{"1\n3\n", 6, 3}, IterativeExpected{"1\n3\n", 5, 5}, IterativeExpected{"1\n3\n", 5, 5})},
	}
}

// RunIterativeFake executes all three paths in round-robin order after one
// unmeasured warm-up per path. It rejects samples whose output, exact selected
// mode, or deterministic call/batch oracle differs.
func RunIterativeFake(ctx context.Context, w IterativeWorkload, repetitions int) (IterativeReport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if repetitions <= 0 {
		repetitions = DefaultIterativeRepetitions
	}
	report := IterativeReport{SchemaVersion: "1", Workload: w.Name, Repetitions: repetitions, Samples: make(map[IterativeMode][]IterativeSample), Summaries: make(map[IterativeMode]IterativeSummary)}
	modes := []IterativeMode{IterativeModeWindowed, IterativeModeHarvest, IterativeModeInterleaved}
	for _, mode := range modes {
		if _, err := runIterativeOnce(ctx, w, mode); err != nil {
			return report, fmt.Errorf("warm-up %s: %w", mode, err)
		}
	}
	for i := 0; i < repetitions; i++ {
		for offset := range modes {
			mode := modes[(i+offset)%len(modes)]
			sample, err := runIterativeOnce(ctx, w, mode)
			if err != nil {
				return report, fmt.Errorf("sample %d %s: %w", i, mode, err)
			}
			report.Samples[mode] = append(report.Samples[mode], sample)
		}
		if err := verifyIterativeParity(report.Samples, i); err != nil {
			return report, err
		}
	}
	for _, mode := range modes {
		report.Summaries[mode] = summarizeIterative(report.Samples[mode])
	}
	windowed := report.Summaries[IterativeModeWindowed]
	iterative := report.Summaries[IterativeModeHarvest]
	if samples := report.Samples[IterativeModeWindowed]; len(samples) > 0 {
		report.JudgementsAvoided = samples[0].Stats.PostDedupBackendCalls - report.Samples[IterativeModeHarvest][0].Stats.PostDedupBackendCalls
		report.JudgementsAvoidedPercent = percentReduction(samples[0].Stats.PostDedupBackendCalls, report.Samples[IterativeModeHarvest][0].Stats.PostDedupBackendCalls)
	}
	if w.Name == "high-prune" {
		report.Thresholds = append(report.Thresholds, atLeast("high-prune judgement reduction", report.JudgementsAvoidedPercent, 25))
	}
	if w.Name == "no-prune" {
		report.Thresholds = append(report.Thresholds,
			atMostDurationIncrease("no-prune median latency overhead", windowed.MedianLatency, iterative.MedianLatency, 15),
			atMostUintIncrease("no-prune peak retained memory overhead", windowed.MedianPeakRetainedBytes, iterative.MedianPeakRetainedBytes, 25),
		)
	}
	return report, nil
}

func runIterativeOnce(ctx context.Context, w IterativeWorkload, mode IterativeMode) (IterativeSample, error) {
	store := semanticcache.NewStore()
	if w.WarmCache {
		if _, err := executeIterativeFake(ctx, w, mode, store, false, false); err != nil {
			return IterativeSample{}, fmt.Errorf("warm cache: %w", err)
		}
	}
	return executeIterativeFake(ctx, w, mode, store, true, true)
}

func executeIterativeFake(ctx context.Context, w IterativeWorkload, mode IterativeMode, store *semanticcache.Store, measure, checkOracle bool) (IterativeSample, error) {
	be := &controlledFake{}
	opts := engine.Options{Query: w.Query, InputMode: w.Mode, Output: output.Options{Compact: true}, Backend: be, SemanticCache: store, WindowBytes: w.WindowBytes}
	switch mode {
	case IterativeModeHarvest:
		opts.IterativeHarvest = true
	case IterativeModeInterleaved:
		opts.Stream = true
	}
	var stdout bytes.Buffer
	var result engine.Result
	var err error
	var duration time.Duration
	var allocations, allocationBytes, retained uint64
	if measure {
		duration, allocations, allocationBytes, retained = measureIterative(func() { result, err = engine.Execute(ctx, bytes.NewReader(w.Input), &stdout, opts) })
	} else {
		result, err = engine.Execute(ctx, bytes.NewReader(w.Input), &stdout, opts)
	}
	if err != nil {
		return IterativeSample{}, err
	}
	want, ok := w.Expected[mode]
	if !ok {
		return IterativeSample{}, fmt.Errorf("no expected oracle for %s", mode)
	}
	if result.RunStats.ExecutionMode != expectedExecutionMode(mode) {
		return IterativeSample{}, fmt.Errorf("selected mode %q, want %q", result.RunStats.ExecutionMode, expectedExecutionMode(mode))
	}
	if stdout.String() != want.Output {
		return IterativeSample{}, fmt.Errorf("oracle output = %q, want %q", stdout.String(), want.Output)
	}
	if checkOracle && (result.RunStats.PostDedupBackendCalls != want.Calls || be.batches != want.Batches) {
		return IterativeSample{}, fmt.Errorf("oracle calls/batches = %d/%d, want %d/%d", result.RunStats.PostDedupBackendCalls, be.batches, want.Calls, want.Batches)
	}
	return IterativeSample{Mode: mode, Duration: duration, Allocations: allocations, AllocationBytes: allocationBytes, PeakRetainedBytes: retained, Stats: result.RunStats, BackendBatches: be.batches, Output: stdout.String()}, nil
}

func expectedExecutionMode(mode IterativeMode) engine.ExecutionMode {
	if mode == IterativeModeHarvest {
		return engine.ExecutionModeIterativeHarvest
	}
	if mode == IterativeModeInterleaved {
		return engine.ExecutionModeUserStream
	}
	return engine.ExecutionModeThreePhaseWindowed
}

// measureIterative resets the GC baseline then samples HeapAlloc while the full
// engine execution runs. PeakRetainedBytes is the sampled HeapAlloc high-water
// above that baseline, not final heap size; automatic GC remains enabled.
func measureIterative(run func()) (time.Duration, uint64, uint64, uint64) {
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var mu sync.Mutex
	peak := before.HeapAlloc
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(100 * time.Microsecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				mu.Lock()
				if m.HeapAlloc > peak {
					peak = m.HeapAlloc
				}
				mu.Unlock()
			}
		}
	}()
	start := time.Now()
	run()
	elapsed := positiveDurationSince(start)
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	close(done)
	<-stopped
	mu.Lock()
	if after.HeapAlloc > peak {
		peak = after.HeapAlloc
	}
	mu.Unlock()
	retained := uint64(0)
	if peak > before.HeapAlloc {
		retained = peak - before.HeapAlloc
	}
	return elapsed, after.Mallocs - before.Mallocs, after.TotalAlloc - before.TotalAlloc, retained
}

func verifyIterativeParity(samples map[IterativeMode][]IterativeSample, i int) error {
	a := samples[IterativeModeWindowed][i]
	b := samples[IterativeModeHarvest][i]
	c := samples[IterativeModeInterleaved][i]
	if a.Output != b.Output || a.Output != c.Output {
		return fmt.Errorf("sample %d output parity failure", i)
	}
	return nil
}
func summarizeIterative(samples []IterativeSample) IterativeSummary {
	if len(samples) == 0 {
		return IterativeSummary{}
	}
	durations := make([]time.Duration, len(samples))
	allocs := make([]uint64, len(samples))
	bytes := make([]uint64, len(samples))
	peaks := make([]uint64, len(samples))
	for i, s := range samples {
		durations[i] = s.Duration
		allocs[i] = s.Allocations
		bytes[i] = s.AllocationBytes
		peaks[i] = s.PeakRetainedBytes
	}
	sort.Slice(durations, func(i, j int) bool { return durations[i] < durations[j] })
	sort.Slice(allocs, func(i, j int) bool { return allocs[i] < allocs[j] })
	sort.Slice(bytes, func(i, j int) bool { return bytes[i] < bytes[j] })
	sort.Slice(peaks, func(i, j int) bool { return peaks[i] < peaks[j] })
	mid := len(samples) / 2
	return IterativeSummary{Samples: len(samples), MinLatency: durations[0], MedianLatency: durations[mid], MaxLatency: durations[len(samples)-1], MedianAllocations: allocs[mid], MedianAllocationBytes: bytes[mid], MedianPeakRetainedBytes: peaks[mid]}
}
func percentReduction(base, candidate int) float64 {
	if base == 0 {
		if candidate == 0 {
			return 0
		}
		return -100
	}
	return float64(base-candidate) * 100 / float64(base)
}
func percentIncrease(base, candidate time.Duration) float64 {
	if base == 0 {
		if candidate == 0 {
			return 0
		}
		return 100
	}
	return float64(candidate-base) * 100 / float64(base)
}
func percentIncreaseUint(base, candidate uint64) float64 {
	if base == 0 {
		if candidate == 0 {
			return 0
		}
		return 100
	}
	if candidate >= base {
		return float64(candidate-base) * 100 / float64(base)
	}
	return -float64(base-candidate) * 100 / float64(base)
}
func atLeast(name string, actual, threshold float64) ThresholdResult {
	return ThresholdResult{Name: name, Actual: actual, Threshold: threshold, Pass: actual >= threshold}
}
func atMostDurationIncrease(name string, base, candidate time.Duration, threshold float64) ThresholdResult {
	zero := base == 0
	pass := candidate == 0
	if !zero {
		pass = percentIncrease(base, candidate) <= threshold
	}
	return ThresholdResult{Name: name, Actual: percentIncrease(base, candidate), Threshold: threshold, Pass: pass, ZeroBase: zero}
}
func atMostUintIncrease(name string, base, candidate uint64, threshold float64) ThresholdResult {
	zero := base == 0
	pass := candidate == 0
	if !zero {
		pass = percentIncreaseUint(base, candidate) <= threshold
	}
	return ThresholdResult{Name: name, Actual: percentIncreaseUint(base, candidate), Threshold: threshold, Pass: pass, ZeroBase: zero}
}

type controlledFake struct{ batches int }

func (b *controlledFake) Warm(context.Context) error { return nil }
func (b *controlledFake) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	b.batches++
	out := make([]backend.Result, len(batch))
	for i, j := range batch {
		value := fmt.Sprint(j.Value)
		if j.Op == "sem_classify" {
			for _, label := range j.Specs {
				if strings.HasPrefix(value, label+"-") {
					out[i] = backend.Result{Value: label}
					break
				}
			}
			continue
		}
		out[i] = backend.Result{Value: strings.HasPrefix(value, "pass-")}
	}
	return out, nil
}

// Ensure io remains part of the documented output-sink contract without
// exposing the benchmark's discarded writer implementation.
var _ io.Writer = io.Discard
