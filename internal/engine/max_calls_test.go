package engine

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	localbackend "github.com/ricardocabral/ajq/internal/backend/local"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

func TestMaxCallsThreePhaseAbortsBeforeExceedingCap(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`[{"msg":"keep"},{"msg":"drop"}]`), &stdout, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      1,
	})
	if err == nil {
		t.Fatal("expected max calls error")
	}
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) || capErr.Cap != 1 || capErr.Needed != 2 {
		t.Fatalf("error = %T %[1]v, want cap=1 needed=2", err)
	}
	if len(be.Inputs()) != 0 || result.RunStats.PostDedupBackendCalls != 0 {
		t.Fatalf("backend inputs=%d stats=%#v, want zero calls beyond cap", len(be.Inputs()), result.RunStats)
	}
	for _, want := range []string{"cap 1", "2 post-dedup backend judgements", "--explain", "--max-calls"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("cap error missing %q: %v", want, err)
		}
	}
}

func TestMaxCallsThreePhaseParallelLocalBackendAbortsBeforeDispatch(t *testing.T) {
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&requests, 1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"true"}`))
	}))
	t.Cleanup(srv.Close)

	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`[{"msg":"keep"},{"msg":"drop"}]`), &stdout, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       &localbackend.Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxConcurrency: 4},
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      1,
	})
	if err == nil {
		t.Fatal("expected max calls error")
	}
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) || capErr.Cap != 1 || capErr.Needed != 2 {
		t.Fatalf("error = %T %[1]v, want cap=1 needed=2", err)
	}
	if got := atomic.LoadInt32(&requests); got != 0 || result.RunStats.PostDedupBackendCalls != 0 {
		t.Fatalf("HTTP requests=%d stats=%#v, want reserve-before-dispatch zero spend", got, result.RunStats)
	}
}

func TestMaxCallsThreePhaseEqualCapSucceeds(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`[{"msg":"keep"},{"msg":"drop"}]`), &stdout, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      2,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(be.Inputs()) != 2 {
		t.Fatalf("backend inputs = %d, want 2", len(be.Inputs()))
	}
}

func TestMaxCallsDoesNotCountDuplicateDedupSkips(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`[{"msg":"keep"},{"msg":"keep"}]`), &stdout, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      1,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if len(be.Inputs()) != 1 {
		t.Fatalf("backend inputs = %d, want one post-dedup judgement", len(be.Inputs()))
	}
}

func TestMaxCallsDoesNotCountCachedJudgements(t *testing.T) {
	store := semanticcache.NewStore()
	stdin := `[{"msg":"keep"},{"msg":"drop"}]`
	var warmOut bytes.Buffer
	if _, err := Execute(context.Background(), strings.NewReader(stdin), &warmOut, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       &backend.MockBackend{},
		SemanticCache: store,
	}); err != nil {
		t.Fatalf("warm Execute returned error: %v", err)
	}

	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{
		Query:         `.[] | .msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: store,
		MaxCalls:      1,
	})
	if err != nil {
		t.Fatalf("cached Execute returned error: %v", err)
	}
	if len(be.Inputs()) != 0 || result.RunStats.PostDedupBackendCalls != 0 || result.RunStats.CacheHits != 2 {
		t.Fatalf("backend inputs=%d stats=%#v, want cached judgements to avoid cap spend", len(be.Inputs()), result.RunStats)
	}
}

func TestMaxCallsCumulativeAcrossFrames(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"a\"}\n{\"msg\":\"b\"}\n"), &stdout, Options{
		Query:         `.msg =~ "a"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      1,
	})
	if err == nil {
		t.Fatal("expected cumulative cap error")
	}
	if len(be.Inputs()) != 1 || result.RunStats.PostDedupBackendCalls != 1 {
		t.Fatalf("backend inputs=%d stats=%#v, want one successful judgement before cumulative abort", len(be.Inputs()), result.RunStats)
	}
}

func TestMaxCallsInterleavedAbortsBeforeExceedingCap(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"msg":"urgent"},{"id":2,"msg":"low"}]`), &stdout, Options{
		Query:         `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		MaxCalls:      1,
	})
	if err == nil {
		t.Fatal("expected interleaved cap error")
	}
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) || capErr.Cap != 1 || capErr.Needed != 2 {
		t.Fatalf("error = %T %[1]v, want cap=1 needed=2", err)
	}
	if len(be.Inputs()) != 1 || result.RunStats.PostDedupBackendCalls != 1 {
		t.Fatalf("backend inputs=%d stats=%#v, want abort before second interleaved judgement", len(be.Inputs()), result.RunStats)
	}
}
