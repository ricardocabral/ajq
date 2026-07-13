package bench

import (
	"bytes"
	"context"
	"fmt"
	"time"

	localbackend "github.com/ricardocabral/ajq/internal/backend/local"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/daemon"
	"github.com/ricardocabral/ajq/internal/engine"
	"github.com/ricardocabral/ajq/internal/output"
)

// IterativeLocalReport is informational opt-in local-model evidence. Unlike
// the controlled fake report it has no threshold verdict: model latency and
// answers are deliberately excluded from the deterministic decision gate.
type IterativeLocalReport struct {
	Environment Environment
	Workload    string
	Repetitions int
	Samples     map[IterativeMode][]IterativeSample
}

// RunIterativeLocal compares the real local backend through complete windowed
// and iterative engine executions. Callers must first require EnvRealBench and
// DetectRealAssets; this function never runs as part of default tests.
func RunIterativeLocal(ctx context.Context, cfg RealConfig, w IterativeWorkload, repetitions int) (report IterativeLocalReport, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if repetitions <= 0 {
		repetitions = DefaultIterativeRepetitions
	}
	report = IterativeLocalReport{Environment: DescribeEnvironment(cfg), Workload: w.Name, Repetitions: repetitions, Samples: make(map[IterativeMode][]IterativeSample)}
	dcfg := daemon.Config{Host: cfg.Host, Port: cfg.Port, ServerBinaryPath: cfg.ServerBinaryPath, ModelPath: cfg.ModelPath, ParallelSlots: daemon.DefaultParallelSlots}
	mgr := daemon.NewManager(dcfg)
	if _, err = mgr.Stop(ctx); err != nil {
		return report, fmt.Errorf("initial stop: %w", err)
	}
	if err = mgr.EnsureRunning(ctx); err != nil {
		return report, fmt.Errorf("start local daemon: %w", err)
	}
	defer func() {
		if _, stopErr := mgr.Stop(context.Background()); stopErr != nil {
			if err == nil {
				err = fmt.Errorf("cleanup stop: %w", stopErr)
			}
		}
	}()
	modes := []IterativeMode{IterativeModeWindowed, IterativeModeHarvest}
	for i := 0; i < repetitions; i++ {
		for offset := range modes {
			mode := modes[(i+offset)%len(modes)]
			sample, runErr := runIterativeLocalOnce(ctx, dcfg, mgr.APIKey(), w, mode)
			if runErr != nil {
				return report, fmt.Errorf("sample %d %s: %w", i, mode, runErr)
			}
			report.Samples[mode] = append(report.Samples[mode], sample)
		}
		left, right := report.Samples[IterativeModeWindowed][i], report.Samples[IterativeModeHarvest][i]
		if left.Output != right.Output {
			return report, fmt.Errorf("sample %d output parity failure", i)
		}
	}
	return report, nil
}

func runIterativeLocalOnce(ctx context.Context, cfg daemon.Config, apiKey string, w IterativeWorkload, mode IterativeMode) (IterativeSample, error) {
	be := &localbackend.Backend{BaseURL: cfg.BaseURL(), ModelID: semanticcache.DefaultModelID, APIKey: apiKey, MaxConcurrency: daemon.DefaultParallelSlots}
	opts := engine.Options{Query: w.Query, InputMode: w.Mode, Output: output.Options{Compact: true}, Backend: be, SemanticCache: semanticcache.NewStore(), WindowBytes: w.WindowBytes, IterativeHarvest: mode == IterativeModeHarvest}
	var stdout bytes.Buffer
	start := time.Now()
	result, err := engine.Execute(ctx, bytes.NewReader(w.Input), &stdout, opts)
	if err != nil {
		return IterativeSample{}, err
	}
	if result.RunStats.ExecutionMode != expectedExecutionMode(mode) {
		return IterativeSample{}, fmt.Errorf("selected mode %q, want %q", result.RunStats.ExecutionMode, expectedExecutionMode(mode))
	}
	return IterativeSample{Mode: mode, Duration: positiveDurationSince(start), Stats: result.RunStats, Output: stdout.String()}, nil
}
