package bench

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
)

type countingCacheBackend struct {
	calls      int
	batchSizes []int
}

func (b *countingCacheBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.calls++
	b.batchSizes = append(b.batchSizes, len(batch))
	results := make([]backend.Result, len(batch))
	for i := range results {
		results[i] = backend.Result{Value: true}
	}
	return results, nil
}

func (b *countingCacheBackend) Warm(context.Context) error { return nil }

func TestResolveThroughCacheWarmsThenReplaysWithoutBackend(t *testing.T) {
	be := &countingCacheBackend{}
	store := semanticcache.NewStore()
	batch := distinctBatch(Workload{Distinct: 3})

	if err := resolveThroughCache(context.Background(), be, store, batch); err != nil {
		t.Fatalf("cache warm: %v", err)
	}
	if be.calls != 1 {
		t.Fatalf("backend calls after warm = %d, want 1", be.calls)
	}
	if got := be.batchSizes[0]; got != len(batch) {
		t.Fatalf("warm batch size = %d, want %d", got, len(batch))
	}

	if err := resolveThroughCache(context.Background(), be, store, batch); err != nil {
		t.Fatalf("cache replay: %v", err)
	}
	if be.calls != 1 {
		t.Fatalf("backend calls after replay = %d, want warm call only", be.calls)
	}
}

func TestRunRealReportsCachedReplayLatencyAndCleansUp(t *testing.T) {
	mgr := &fakeRealDaemonManager{}
	installFakeRealDaemonManager(t, mgr)
	var requests int32
	var promptCacheEnabled int32
	cfg := startRealBenchCompletionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		var payload struct {
			CachePrompt bool `json:"cache_prompt"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode completion request: %v", err)
		}
		if payload.CachePrompt {
			atomic.AddInt32(&promptCacheEnabled, 1)
		}
		successfulCompletionHandler(t).ServeHTTP(w, r)
	}))

	w, err := GenerateArray("test", QuerySemMatch, 2)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	report, err := RunReal(context.Background(), cfg, w)
	if err != nil {
		t.Fatalf("RunReal: %v", err)
	}
	if report.BatchJudgements != 2 {
		t.Fatalf("BatchJudgements = %d, want 2", report.BatchJudgements)
	}
	if report.SequentialBatchLatency <= 0 || report.SequentialThroughput <= 0 {
		t.Fatalf("sequential metrics = %s %.2f, want positive", report.SequentialBatchLatency, report.SequentialThroughput)
	}
	if report.ParallelBatchLatency <= 0 || report.ParallelThroughput <= 0 {
		t.Fatalf("parallel metrics = %s %.2f, want positive", report.ParallelBatchLatency, report.ParallelThroughput)
	}
	if report.CachedBatchLatency <= 0 {
		t.Fatalf("CachedBatchLatency = %s, want positive replay duration", report.CachedBatchLatency)
	}
	if mgr.ensureCalls != 1 {
		t.Fatalf("EnsureRunning called %d times, want 1", mgr.ensureCalls)
	}
	if mgr.stopCalls != 2 {
		t.Fatalf("Stop called %d times, want initial and cleanup stops", mgr.stopCalls)
	}
	// warm single (1) + sequential batch (2) + parallel batch (2) + cache warm (2);
	// cache replay itself should not issue backend requests.
	if got := atomic.LoadInt32(&requests); got != 7 {
		t.Fatalf("completion requests = %d, want 7", got)
	}
	if got := atomic.LoadInt32(&promptCacheEnabled); got != 0 {
		t.Fatalf("cache_prompt=true requests = %d, want 0 for isolated transport timings", got)
	}
}
