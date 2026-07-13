package bench_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/bench"
)

func TestRunFakeProducesActualWindowMetrics(t *testing.T) {
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
			if m.Frames == 0 || m.HarvestedJudgements == 0 || m.PostDedupJudgements == 0 {
				t.Fatalf("missing actual execution counters: %+v", m)
			}
			if m.PostDedupJudgements > m.HarvestedJudgements || m.BackendBatches <= 0 {
				t.Fatalf("invalid actual execution counters: %+v", m)
			}
			if m.WindowBytes == 0 || m.WindowCount == 0 || m.Duration <= 0 {
				t.Fatalf("missing window/duration metrics: %+v", m)
			}
		})
	}
}

func TestNDJSONWindowEvidenceUsesActualExecution(t *testing.T) {
	// The smaller budget creates several windows while the larger one allows all
	// frames into one. The repeated values prove dedup within the first window;
	// later windows reuse the same run's cache and need no extra Judge batches.
	input := []byte(strings.Repeat(`{"id":1,"msg":"urgent"}`+"\n", 6))
	for _, tc := range []struct {
		name       string
		budget     int64
		wantWindow int64
	}{
		{name: "small", budget: 64, wantWindow: 3},
		{name: "large", budget: 1024, wantWindow: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, err := bench.RunFake(context.Background(), bench.Workload{
				Name: "ndjson-" + tc.name, Query: `select(sem_match(.msg; "urgent")) | .id`,
				Input: input, Shape: bench.ShapeNDJSON, WindowBytes: tc.budget, Records: 6, Distinct: 1,
			})
			if err != nil {
				t.Fatalf("RunFake: %v", err)
			}
			if m.WindowBytes != tc.budget || m.WindowCount != tc.wantWindow {
				t.Fatalf("budget/windows = %d/%d, want %d/%d", m.WindowBytes, m.WindowCount, tc.budget, tc.wantWindow)
			}
			if m.HarvestedJudgements != 6 || m.PostDedupJudgements != 1 || m.BackendBatches != 1 {
				t.Fatalf("dedup/batches = harvested %d post %d batches %d, want 6/1/1", m.HarvestedJudgements, m.PostDedupJudgements, m.BackendBatches)
			}
		})
	}
}

func TestStandardWorkloadsIncludeOversizedWindowEvidence(t *testing.T) {
	workloads, err := bench.StandardWorkloads(8)
	if err != nil {
		t.Fatal(err)
	}
	var oversized bench.Workload
	for _, w := range workloads {
		if w.Name == "sem_match/ndjson/oversized" {
			oversized = w
			break
		}
	}
	if oversized.Name == "" {
		t.Fatalf("standard workloads omit oversized NDJSON scenario")
	}
	m, err := bench.RunFake(context.Background(), oversized)
	if err != nil {
		t.Fatal(err)
	}
	if m.WindowBytes != 64 || m.WindowCount != 1 || m.OversizedWindowCount != 1 || m.BackendBatches != 1 {
		t.Fatalf("oversized metrics = %+v, want one 64-byte oversized window and one batch", m)
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
