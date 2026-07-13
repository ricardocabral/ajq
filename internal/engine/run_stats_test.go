package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

func TestRunStatsPureJQCountsInputFrames(t *testing.T) {
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader("{\"n\":1}\n{\"n\":2}\n"), &stdout, Options{
		Query:     `.n`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.RunStats.ExecutionMode != ExecutionModePureJQ || result.RunStats.WindowBytes != 0 || result.RunStats.WindowCount != 0 || result.RunStats.OversizedWindowCount != 0 || result.RunStats.InputFrames != 2 || result.RunStats.SemanticCallSites != 0 || result.RunStats.HarvestedJudgements != 0 || result.RunStats.PostDedupBackendCalls != 0 || result.RunStats.CacheHits != 0 {
		t.Fatalf("RunStats = %#v, want two pure-jq frames and zero semantic counters", result.RunStats)
	}
	if result.RunStats.Elapsed <= 0 {
		t.Fatalf("Elapsed = %s, want positive duration", result.RunStats.Elapsed)
	}
}

func TestRunStatsThreePhaseCountsHarvestDedupAndCacheHits(t *testing.T) {
	store := semanticcache.NewStore()
	stdin := `[{"msg":"keep"},{"msg":"keep"},{"msg":"drop"}]`
	run := func() Result {
		t.Helper()
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{
			Query:         `.[] | select(.msg =~ "keep") | .msg`,
			InputMode:     input.ModeAuto,
			Output:        output.Options{Compact: true},
			Backend:       &backend.MockBackend{},
			SemanticCache: store,
		})
		if err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		return result
	}

	first := run()
	if first.RunStats.ExecutionMode != ExecutionModeThreePhaseWindowed || first.RunStats.WindowBytes != DefaultWindowBytes || first.RunStats.WindowCount != 1 || first.RunStats.OversizedWindowCount != 0 || first.RunStats.InputFrames != 1 || first.RunStats.SemanticCallSites != 1 || first.RunStats.HarvestedJudgements != 3 || first.RunStats.PostDedupBackendCalls != 2 || first.RunStats.CacheHits != 0 {
		t.Fatalf("first RunStats = %#v, want harvest=3 post-dedup backend judgements=2 cache_hits=0", first.RunStats)
	}
	second := run()
	if second.RunStats.ExecutionMode != ExecutionModeThreePhaseWindowed || second.RunStats.WindowCount != 1 || second.RunStats.InputFrames != 1 || second.RunStats.SemanticCallSites != 1 || second.RunStats.HarvestedJudgements != 3 || second.RunStats.PostDedupBackendCalls != 0 || second.RunStats.CacheHits != 3 {
		t.Fatalf("second RunStats = %#v, want cached harvest=3 backend=0 cache_hits=3", second.RunStats)
	}
}

func TestRunStatsUserStreamCountsBackendCallsWithoutHarvest(t *testing.T) {
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`{"id":1,"msg":"keep"}`+"\n"+`{"id":2,"msg":"other"}`+"\n"), &stdout, Options{
		Query:         `select(sem_match(.msg; "keep")) | .id`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       &backend.MockBackend{},
		SemanticCache: semanticcache.NewStore(),
		Stream:        true,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	stats := result.RunStats
	if stats.ExecutionMode != ExecutionModeUserStream || stats.WindowBytes != 0 || stats.WindowCount != 0 || stats.OversizedWindowCount != 0 || stats.InputFrames != 2 || stats.SemanticCallSites != 1 || stats.HarvestedJudgements != 0 || stats.PostDedupBackendCalls != 2 || stats.CacheHits != 0 {
		t.Fatalf("RunStats = %#v, want user-stream inline calls without harvest/window counters", stats)
	}
}

func TestRunStatsInterleavedCountsBackendCallsAndCacheHitsWithoutHarvest(t *testing.T) {
	store := semanticcache.NewStore()
	stdin := `[{"id":1,"msg":"urgent"},{"id":2,"msg":"low"}]`
	run := func() Result {
		t.Helper()
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{
			Query:         `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`,
			InputMode:     input.ModeAuto,
			Output:        output.Options{Compact: true},
			Backend:       &backend.MockBackend{},
			SemanticCache: store,
		})
		if err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		return result
	}

	first := run()
	if first.RunStats.ExecutionMode != ExecutionModeInterleaved || first.RunStats.WindowBytes != 0 || first.RunStats.WindowCount != 0 || first.RunStats.OversizedWindowCount != 0 || first.RunStats.InputFrames != 1 || first.RunStats.SemanticCallSites != 1 || first.RunStats.HarvestedJudgements != 0 || first.RunStats.PostDedupBackendCalls != 2 || first.RunStats.CacheHits != 0 {
		t.Fatalf("first RunStats = %#v, want interleaved backend calls without harvest", first.RunStats)
	}
	second := run()
	if second.RunStats.ExecutionMode != ExecutionModeInterleaved || second.RunStats.WindowCount != 0 || second.RunStats.InputFrames != 1 || second.RunStats.SemanticCallSites != 1 || second.RunStats.HarvestedJudgements != 0 || second.RunStats.PostDedupBackendCalls != 0 || second.RunStats.CacheHits != 2 {
		t.Fatalf("second RunStats = %#v, want interleaved cache hits", second.RunStats)
	}
}

func TestRunStatsWindowCountsOversizedAndFailedWindows(t *testing.T) {
	for _, tc := range []struct {
		name          string
		stdin         string
		budget        int64
		maxCalls      int
		wantWindows   int64
		wantOversized int64
		wantErr       bool
	}{
		{name: "multi-window", stdin: "a\nb\n", budget: 2, wantWindows: 2},
		{name: "oversized", stdin: "oversized\n", budget: 2, wantWindows: 1, wantOversized: 1},
		{name: "cap-rejected", stdin: "a\nb\n", budget: 1024, maxCalls: 1, wantWindows: 1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Execute(context.Background(), strings.NewReader(tc.stdin), ioDiscard{}, Options{
				Query: `. =~ "a"`, InputMode: input.ModeRaw, Backend: &backend.MockBackend{}, SemanticCache: semanticcache.NewStore(), WindowBytes: tc.budget, MaxCalls: tc.maxCalls,
			})
			if (err != nil) != tc.wantErr {
				t.Fatalf("Execute error = %v, wantErr %v", err, tc.wantErr)
			}
			stats := result.RunStats
			if stats.ExecutionMode != ExecutionModeThreePhaseWindowed || stats.WindowBytes != tc.budget || stats.WindowCount != tc.wantWindows || stats.OversizedWindowCount != tc.wantOversized {
				t.Fatalf("RunStats = %#v, want window bytes/count/oversized %d/%d/%d", stats, tc.budget, tc.wantWindows, tc.wantOversized)
			}
		})
	}
}
