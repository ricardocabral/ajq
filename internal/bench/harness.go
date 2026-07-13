package bench

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/engine"
	"github.com/ricardocabral/ajq/internal/output"
)

// Metrics captures counters and timing from one actual fake-mode engine run.
// The judgement counters are the exact inputs Phase 4 needs to tune window
// sizing: how many judgements a window harvests, how many survive dedup, and
// how many backend batches result.
type Metrics struct {
	// Workload is the workload name.
	Workload string
	// Shape records the framing used.
	Shape Shape
	// WindowBytes is the configured semantic window byte budget used by this run.
	WindowBytes int64
	// WindowCount is the number of actual three-phase windows formed by this run.
	WindowCount int64
	// OversizedWindowCount is the number of one-frame windows exceeding WindowBytes.
	OversizedWindowCount int64
	// Frames is the number of input frames processed.
	Frames int64
	// HarvestedJudgements is the total number of semantic calls collected during
	// harvest, before dedup.
	HarvestedJudgements int
	// PostDedupJudgements is the number of distinct judgements actually sent to
	// the backend after cache/dedup collapsing.
	PostDedupJudgements int
	// BackendBatches is the number of Judge calls issued to the backend.
	BackendBatches int
	// DedupRatio is PostDedupJudgements / HarvestedJudgements (1.0 when nothing
	// was deduplicated, lower is better). Zero when nothing was harvested.
	DedupRatio float64
	// Duration is the wall-clock time to execute the full workload once.
	Duration time.Duration
}

// RunFake executes a workload once through the deterministic mock backend and
// returns metrics from that same engine.Execute call. It never performs real
// inference and does not use explain estimates or input-length proxies.
func RunFake(ctx context.Context, w Workload) (Metrics, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	return executeFake(ctx, w)
}

// executeFake runs the workload through engine.Execute with a fresh mock
// backend and cache, discarding output, and returns actual counters and elapsed time.
func executeFake(ctx context.Context, w Workload) (Metrics, error) {
	be := &backend.MockBackend{}
	opts := engine.Options{
		Query:         w.Query,
		InputMode:     w.Mode,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		WindowBytes:   w.WindowBytes,
	}
	start := time.Now()
	result, err := engine.Execute(ctx, bytes.NewReader(w.Input), io.Discard, opts)
	elapsed := positiveDurationSince(start)
	if err != nil {
		return Metrics{}, fmt.Errorf("bench workload %q: %w", w.Name, err)
	}
	stats := result.RunStats
	m := Metrics{
		Workload:             w.Name,
		Shape:                w.Shape,
		WindowBytes:          stats.WindowBytes,
		WindowCount:          stats.WindowCount,
		OversizedWindowCount: stats.OversizedWindowCount,
		Frames:               stats.InputFrames,
		HarvestedJudgements:  stats.HarvestedJudgements,
		PostDedupJudgements:  stats.PostDedupBackendCalls,
		BackendBatches:       be.BatchCount(),
		Duration:             elapsed,
	}
	if m.HarvestedJudgements > 0 {
		m.DedupRatio = float64(m.PostDedupJudgements) / float64(m.HarvestedJudgements)
	}
	return m, nil
}

// RunFakeSet runs each workload once and returns their metrics in order.
func RunFakeSet(ctx context.Context, workloads []Workload) ([]Metrics, error) {
	out := make([]Metrics, 0, len(workloads))
	for _, w := range workloads {
		m, err := RunFake(ctx, w)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, nil
}

func positiveDurationSince(start time.Time) time.Duration {
	elapsed := time.Since(start)
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}
