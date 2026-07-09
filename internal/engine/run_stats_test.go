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
	if result.RunStats.InputFrames != 2 || result.RunStats.SemanticCallSites != 0 || result.RunStats.HarvestedJudgements != 0 || result.RunStats.PostDedupBackendCalls != 0 || result.RunStats.CacheHits != 0 {
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
	if first.RunStats.InputFrames != 1 || first.RunStats.SemanticCallSites != 1 || first.RunStats.HarvestedJudgements != 3 || first.RunStats.PostDedupBackendCalls != 2 || first.RunStats.CacheHits != 0 {
		t.Fatalf("first RunStats = %#v, want harvest=3 post-dedup backend judgements=2 cache_hits=0", first.RunStats)
	}
	second := run()
	if second.RunStats.InputFrames != 1 || second.RunStats.SemanticCallSites != 1 || second.RunStats.HarvestedJudgements != 3 || second.RunStats.PostDedupBackendCalls != 0 || second.RunStats.CacheHits != 3 {
		t.Fatalf("second RunStats = %#v, want cached harvest=3 backend=0 cache_hits=3", second.RunStats)
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
	if first.RunStats.InputFrames != 1 || first.RunStats.SemanticCallSites != 1 || first.RunStats.HarvestedJudgements != 0 || first.RunStats.PostDedupBackendCalls != 2 || first.RunStats.CacheHits != 0 {
		t.Fatalf("first RunStats = %#v, want interleaved backend calls without harvest", first.RunStats)
	}
	second := run()
	if second.RunStats.InputFrames != 1 || second.RunStats.SemanticCallSites != 1 || second.RunStats.HarvestedJudgements != 0 || second.RunStats.PostDedupBackendCalls != 0 || second.RunStats.CacheHits != 2 {
		t.Fatalf("second RunStats = %#v, want interleaved cache hits", second.RunStats)
	}
}
