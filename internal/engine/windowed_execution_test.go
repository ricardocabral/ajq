package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

func TestThreePhaseWindowHarvestsAllFramesBeforeOneResolve(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"keep\"}\n{\"msg\":\"drop\"}\n"), &stdout, Options{
		Query:         `.msg =~ "keep"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       be,
		SemanticCache: semanticcache.NewStore(),
		WindowBytes:   1024,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if got, want := stdout.String(), "true\nfalse\n"; got != want {
		t.Fatalf("stdout = %q, want %q", got, want)
	}
	if got := be.BatchCount(); got != 1 {
		t.Fatalf("backend batches = %d, want one window resolve", got)
	}
	if got := len(be.Batches()[0]); got != 2 {
		t.Fatalf("backend batch length = %d, want two harvested frames", got)
	}
	if result.RunStats.InputFrames != 2 || result.RunStats.HarvestedJudgements != 2 {
		t.Fatalf("RunStats = %#v, want two input frames and judgements", result.RunStats)
	}
}

func TestThreePhaseWindowNormalizesZeroAndRejectsNegativeBudget(t *testing.T) {
	be := &backend.MockBackend{}
	_, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"keep\"}\n{\"msg\":\"drop\"}\n"), ioDiscard{}, Options{
		Query:       `.msg =~ "keep"`,
		InputMode:   input.ModeAuto,
		Backend:     be,
		WindowBytes: 0,
	})
	if err != nil {
		t.Fatalf("zero WindowBytes Execute error = %v", err)
	}
	if got := be.BatchCount(); got != 1 {
		t.Fatalf("zero WindowBytes backend batches = %d, want default one-window batch", got)
	}

	_, err = Execute(context.Background(), strings.NewReader("{\"msg\":\"keep\"}"), ioDiscard{}, Options{
		Query:       `.msg =~ "keep"`,
		InputMode:   input.ModeAuto,
		Backend:     &backend.MockBackend{},
		WindowBytes: -1,
	})
	if !errors.Is(err, ErrInvalidWindowBytes) {
		t.Fatalf("negative WindowBytes error = %v, want ErrInvalidWindowBytes", err)
	}
}

type resultErrorBackend struct{}

func (resultErrorBackend) Warm(context.Context) error { return nil }

func (resultErrorBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	results := make([]backend.Result, len(batch))
	for i := range results {
		results[i] = backend.Result{Value: true}
	}
	if len(results) > 1 {
		results[1] = backend.Result{Error: "synthetic result error"}
	}
	return results, nil
}

func TestThreePhaseWindowAttributesResolveResultErrorToSourceFrame(t *testing.T) {
	_, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"first\"}\n{\"msg\":\"second\"}\n"), ioDiscard{}, Options{
		Query:       `.msg =~ "x"`,
		InputMode:   input.ModeAuto,
		Backend:     resultErrorBackend{},
		WindowBytes: 1024,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 {
		t.Fatalf("error = %T %[1]v, want resolve RuntimeError for frame 2", err)
	}
}

func TestThreePhaseWindowExecutesHarvestedPrefixBeforeFrameError(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"keep\"}\n{\"bad\":true}\n{\"msg\":\"later\"}\n"), &stdout, Options{
		Query:       `if .bad then error("boom") else .msg =~ "keep" end`,
		InputMode:   input.ModeAuto,
		Output:      output.Options{Compact: true},
		Backend:     be,
		WindowBytes: 1024,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 {
		t.Fatalf("error = %T %[1]v, want harvest RuntimeError for frame 2", err)
	}
	if got, want := stdout.String(), "true\n"; got != want {
		t.Fatalf("stdout = %q, want harvested prefix %q", got, want)
	}
	if got := be.BatchCount(); got != 1 || len(be.Batches()[0]) != 1 {
		t.Fatalf("backend batches = %#v, want only first-frame resolve", be.Batches())
	}
}

// ioDiscard avoids importing a concrete writer solely for these executor tests.
type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
