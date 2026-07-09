package bench_test

import (
	"context"
	"testing"

	"github.com/ricardocabral/ajq/internal/bench"
	"github.com/ricardocabral/ajq/internal/engine"
)

func TestRunFakeProducesMetrics(t *testing.T) {
	ctx := context.Background()
	workloads, err := bench.StandardWorkloads(32)
	if err != nil {
		t.Fatalf("StandardWorkloads: %v", err)
	}
	for _, w := range workloads {
		w := w
		t.Run(w.Name, func(t *testing.T) {
			m, err := bench.RunFake(ctx, w)
			if err != nil {
				t.Fatalf("RunFake(%s): %v", w.Name, err)
			}
			if m.EstimateStatus != engine.ExplainEstimateAvailable {
				t.Fatalf("estimate status = %q, want %q", m.EstimateStatus, engine.ExplainEstimateAvailable)
			}
			if m.Frames == 0 {
				t.Fatalf("expected at least one frame, got 0")
			}
			if m.HarvestedJudgements == 0 {
				t.Fatalf("expected harvested judgements > 0")
			}
			if m.PostDedupJudgements == 0 {
				t.Fatalf("expected post-dedup judgements > 0")
			}
			if m.PostDedupJudgements > m.HarvestedJudgements {
				t.Fatalf("post-dedup %d exceeds harvested %d", m.PostDedupJudgements, m.HarvestedJudgements)
			}
			if m.WindowBytes == 0 {
				t.Fatalf("expected non-zero window bytes")
			}
			if m.Duration <= 0 {
				t.Fatalf("expected positive duration, got %v", m.Duration)
			}
		})
	}
}

// TestDedupCollapsesRepeatedValues verifies that array workloads drawing from a
// bounded vocabulary collapse to at most the distinct-value count after dedup,
// which is the effect Phase 4 window sizing depends on.
func TestDedupCollapsesRepeatedValues(t *testing.T) {
	// Many records, few distinct messages -> heavy dedup.
	w, err := bench.GenerateArray("dedup", bench.QuerySemMatch, 200)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	m, err := bench.RunFake(context.Background(), w)
	if err != nil {
		t.Fatalf("RunFake: %v", err)
	}
	if m.PostDedupJudgements > w.Distinct {
		t.Fatalf("post-dedup %d exceeds distinct vocabulary %d", m.PostDedupJudgements, w.Distinct)
	}
	if m.HarvestedJudgements <= m.PostDedupJudgements {
		t.Fatalf("expected dedup to reduce judgements: harvested=%d post=%d", m.HarvestedJudgements, m.PostDedupJudgements)
	}
	if m.DedupRatio <= 0 || m.DedupRatio >= 1 {
		t.Fatalf("expected 0 < dedup ratio < 1, got %v", m.DedupRatio)
	}
}

// TestArrayBatchesSingleWindow confirms an array workload resolves in one
// backend batch (a single window). For NDJSON, each frame is its own window but
// the per-run cache persists across frames, so the number of backend batches
// collapses to the distinct-value count rather than the frame count — the
// cross-window cache effect Phase 4 relies on.
func TestArrayBatchesSingleWindow(t *testing.T) {
	array, err := bench.GenerateArray("array", bench.QuerySemMatch, 16)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	am, err := bench.RunFake(context.Background(), array)
	if err != nil {
		t.Fatalf("RunFake array: %v", err)
	}
	if am.BackendBatches != 1 {
		t.Fatalf("array workload backend batches = %d, want 1", am.BackendBatches)
	}

	nd, err := bench.GenerateNDJSON("ndjson", `select(sem_match(.msg; "urgent")) | .id`, 16)
	if err != nil {
		t.Fatalf("GenerateNDJSON: %v", err)
	}
	nm, err := bench.RunFake(context.Background(), nd)
	if err != nil {
		t.Fatalf("RunFake ndjson: %v", err)
	}
	if nm.Frames != 16 {
		t.Fatalf("ndjson frames = %d, want 16", nm.Frames)
	}
	if nm.BackendBatches != nd.Distinct {
		t.Fatalf("ndjson backend batches = %d, want distinct-value count %d (cross-frame cache)", nm.BackendBatches, nd.Distinct)
	}
	if nm.BackendBatches >= nm.Frames {
		t.Fatalf("expected cross-frame cache to reduce batches below frames: batches=%d frames=%d", nm.BackendBatches, nm.Frames)
	}
}

func BenchmarkFakeStandardWorkloads(b *testing.B) {
	ctx := context.Background()
	workloads, err := bench.StandardWorkloads(64)
	if err != nil {
		b.Fatalf("StandardWorkloads: %v", err)
	}
	for _, w := range workloads {
		w := w
		b.Run(w.Name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := bench.RunFake(ctx, w); err != nil {
					b.Fatalf("RunFake: %v", err)
				}
			}
		})
	}
}

// BenchmarkFakeDedupScaling measures how split-execution cost scales with the
// harvested-to-distinct ratio, informing window-size tuning.
func BenchmarkFakeDedupScaling(b *testing.B) {
	ctx := context.Background()
	for _, n := range []int{16, 64, 256} {
		w, err := bench.GenerateArray("sem_match/array", bench.QuerySemMatch, n)
		if err != nil {
			b.Fatalf("GenerateArray(%d): %v", n, err)
		}
		b.Run(workloadSizeName(n), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := bench.RunFake(ctx, w); err != nil {
					b.Fatalf("RunFake: %v", err)
				}
			}
		})
	}
}

func workloadSizeName(n int) string {
	switch {
	case n < 32:
		return "small"
	case n < 128:
		return "medium"
	default:
		return "large"
	}
}
