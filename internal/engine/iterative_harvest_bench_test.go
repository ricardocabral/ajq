package engine

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

// BenchmarkIterativeHarvestPaired reports allocation-aware full-engine costs
// for the same supported chain. The deterministic fake makes high-prune and
// no-prune differences executor overhead rather than model latency.
func BenchmarkIterativeHarvestPaired(b *testing.B) {
	for _, tc := range []struct {
		name      string
		input     string
		iterative bool
		wantMode  ExecutionMode
	}{
		{name: "high-prune/windowed", input: benchmarkChainInput(1), wantMode: ExecutionModeThreePhaseWindowed},
		{name: "high-prune/iterative", input: benchmarkChainInput(1), iterative: true, wantMode: ExecutionModeIterativeHarvest},
		{name: "no-prune/windowed", input: benchmarkChainInput(32), wantMode: ExecutionModeThreePhaseWindowed},
		{name: "no-prune/iterative", input: benchmarkChainInput(32), iterative: true, wantMode: ExecutionModeIterativeHarvest},
	} {
		tc := tc
		b.Run(tc.name, func(b *testing.B) {
			ctx := context.Background()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				be := &benchmarkControlledBackend{}
				var stdout bytes.Buffer
				result, err := Execute(ctx, strings.NewReader(tc.input), &stdout, Options{Query: `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes")) | .id`, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: be, SemanticCache: semanticcache.NewStore(), IterativeHarvest: tc.iterative})
				if err != nil {
					b.Fatal(err)
				}
				if result.RunStats.ExecutionMode != tc.wantMode {
					b.Fatalf("mode = %q, want %q", result.RunStats.ExecutionMode, tc.wantMode)
				}
				if result.RunStats.PostDedupBackendCalls != be.calls {
					b.Fatalf("stats calls = %d, backend calls = %d", result.RunStats.PostDedupBackendCalls, be.calls)
				}
			}
		})
	}
}

func benchmarkChainInput(passing int) string {
	var rows []string
	for i := 0; i < 32; i++ {
		first := "drop"
		if i < passing {
			first = "pass"
		}
		rows = append(rows, fmt.Sprintf(`{"id":%d,"first":"%s-%d","second":"pass-%d"}`, i+1, first, i, i))
	}
	return "[" + strings.Join(rows, ",") + "]"
}

type benchmarkControlledBackend struct{ calls int }

func (b *benchmarkControlledBackend) Warm(context.Context) error { return nil }
func (b *benchmarkControlledBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.calls += len(batch)
	out := make([]backend.Result, len(batch))
	for i, judgement := range batch {
		out[i] = backend.Result{Value: strings.HasPrefix(fmt.Sprint(judgement.Value), "pass-")}
	}
	return out, nil
}
