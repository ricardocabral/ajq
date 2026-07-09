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

// Metrics captures the counters and timing produced by one fake-mode run. The
// judgement counters are the exact inputs Phase 4 needs to tune window sizing:
// how many judgements a window harvests, how many survive dedup, and how many
// backend batches result.
type Metrics struct {
	// Workload is the workload name.
	Workload string
	// Shape records the framing used.
	Shape Shape
	// WindowBytes is the size of the input fed to the engine, a proxy for the
	// byte-budget window the Phase 4 planner will slice against.
	WindowBytes int
	// Frames is the number of input frames processed.
	Frames int
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
	// EstimateStatus is the explain-estimate status string (e.g. "available").
	EstimateStatus string
}

// RunFake executes a workload once through the deterministic mock backend and
// returns its metrics. It never performs real inference. The counters come from
// engine.EstimateExplain (which uses the same mock harvest/resolve path) while
// the duration is measured by executing the full split pipeline including
// output serialization.
func RunFake(ctx context.Context, w Workload) (Metrics, error) {
	if ctx == nil {
		ctx = context.Background()
	}

	estimate := engine.EstimateExplain(ctx, w.Query, bytes.NewReader(w.Input), w.Mode)

	m := Metrics{
		Workload:            w.Name,
		Shape:               w.Shape,
		WindowBytes:         len(w.Input),
		Frames:              estimate.InputFrames,
		HarvestedJudgements: estimate.HarvestedJudgements,
		PostDedupJudgements: estimate.PostDedupJudgements,
		BackendBatches:      estimate.MockJudgeBatches,
		EstimateStatus:      estimate.Status,
	}
	if estimate.HarvestedJudgements > 0 {
		m.DedupRatio = float64(estimate.PostDedupJudgements) / float64(estimate.HarvestedJudgements)
	}

	dur, err := timeExecute(ctx, w)
	if err != nil {
		return Metrics{}, err
	}
	m.Duration = dur
	return m, nil
}

// timeExecute runs the workload through engine.Execute with a fresh mock
// backend and cache, discarding output, and returns the elapsed wall time.
func timeExecute(ctx context.Context, w Workload) (time.Duration, error) {
	be := &backend.MockBackend{}
	opts := engine.Options{
		Query:         w.Query,
		InputMode:     w.Mode,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
	}
	start := time.Now()
	_, err := engine.Execute(ctx, bytes.NewReader(w.Input), io.Discard, opts)
	elapsed := time.Since(start)
	if err != nil {
		return 0, fmt.Errorf("bench workload %q: %w", w.Name, err)
	}
	return elapsed, nil
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
