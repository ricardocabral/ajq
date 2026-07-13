package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
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

func TestThreePhaseWindowEmitsResolvedPrefixBeforeResultError(t *testing.T) {
	store := semanticcache.NewStore()
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"x-first\"}\n{\"msg\":\"second\"}\n{\"msg\":\"later\"}\n"), &stdout, Options{
		Query:         `.msg =~ "x"`,
		InputMode:     input.ModeAuto,
		Output:        output.Options{Compact: true},
		Backend:       resultErrorBackend{},
		SemanticCache: store,
		WindowBytes:   1024,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 {
		t.Fatalf("error = %T %[1]v, want resolve RuntimeError for frame 2", err)
	}
	if got, want := stdout.String(), "true\n"; got != want {
		t.Fatalf("stdout = %q, want resolved prefix %q", got, want)
	}
	// The validated first result is reusable, while the failed second result
	// must not enter the cache.
	be := &backend.MockBackend{}
	_, err = Execute(context.Background(), strings.NewReader("{\"msg\":\"x-first\"}\n{\"msg\":\"second\"}\n"), ioDiscard{}, Options{
		Query: `.msg =~ "x"`, InputMode: input.ModeAuto, Backend: be, SemanticCache: store, WindowBytes: 1024,
	})
	if err != nil {
		t.Fatalf("cache probe Execute error: %v", err)
	}
	if got := len(be.Inputs()); got != 1 {
		t.Fatalf("cache probe backend inputs = %d, want only uncached failed frame", got)
	}
}

type shortResultsBackend struct{}

func (shortResultsBackend) Warm(context.Context) error { return nil }
func (shortResultsBackend) Judge(_ context.Context, _ []backend.Judgement) ([]backend.Result, error) {
	return []backend.Result{}, nil
}

func TestThreePhaseWindowDoesNotExecuteBatchFailure(t *testing.T) {
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader("{\"msg\":\"first\"}\n{\"msg\":\"second\"}\n"), &stdout, Options{
		Query: `.msg =~ "x"`, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: shortResultsBackend{}, WindowBytes: 1024,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 1 {
		t.Fatalf("error = %T %[1]v, want batch RuntimeError for frame 1", err)
	}
	if got := stdout.String(); got != "" {
		t.Fatalf("stdout = %q, want no output after unusable batch", got)
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

func TestThreePhaseWindowParityDedupCacheAndOversizedStats(t *testing.T) {
	query := `. =~ "keep"`
	stdin := "keep\nkeep\n"
	run := func(windowBytes int64) (string, Result, *backend.MockBackend) {
		t.Helper()
		be := &backend.MockBackend{}
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{Query: query, InputMode: input.ModeRaw, Output: output.Options{Compact: true}, Backend: be, SemanticCache: semanticcache.NewStore(), WindowBytes: windowBytes})
		if err != nil {
			t.Fatalf("Execute error: %v", err)
		}
		return stdout.String(), result, be
	}
	windowedOut, windowed, be := run(1024)
	perFrameOut, perFrame, _ := run(int64(len("keep\n")))
	if windowedOut != perFrameOut || ExitStatusCode(windowed) != ExitStatusCode(perFrame) {
		t.Fatalf("windowed output/exit = %q/%d, per-frame = %q/%d", windowedOut, ExitStatusCode(windowed), perFrameOut, ExitStatusCode(perFrame))
	}
	if len(be.Batches()) != 1 || len(be.Batches()[0]) != 1 {
		t.Fatalf("same-window dedup batches = %#v, want one batch with one judgement", be.Batches())
	}

	store := semanticcache.NewStore()
	be = &backend.MockBackend{}
	result, err := Execute(context.Background(), strings.NewReader("keep\nkeep\n"), ioDiscard{}, Options{Query: query, InputMode: input.ModeRaw, Backend: be, SemanticCache: store, WindowBytes: int64(len("keep\n"))})
	if err != nil {
		t.Fatalf("cache Execute error: %v", err)
	}
	if be.BatchCount() != 1 || result.RunStats.WindowCount != 2 || result.RunStats.CacheHits != 1 {
		t.Fatalf("cross-window cache stats/batches = %#v/%d, want two windows, one batch, one hit", result.RunStats, be.BatchCount())
	}

	be = &backend.MockBackend{}
	result, err = Execute(context.Background(), strings.NewReader("very-long-keep-record\nok\n"), ioDiscard{}, Options{Query: query, InputMode: input.ModeRaw, Backend: be, SemanticCache: semanticcache.NewStore(), WindowBytes: 3})
	if err != nil {
		t.Fatalf("oversized Execute error: %v", err)
	}
	if result.RunStats.WindowCount != 2 || result.RunStats.OversizedWindowCount != 1 || be.BatchCount() != 2 {
		t.Fatalf("oversized stats/batches = %#v/%d, want two windows, one oversized, two batches", result.RunStats, be.BatchCount())
	}
}

type blockingBackend struct{ started chan struct{} }

func (b blockingBackend) Warm(context.Context) error { return nil }
func (b blockingBackend) Judge(ctx context.Context, _ []backend.Judgement) ([]backend.Result, error) {
	close(b.started)
	<-ctx.Done()
	return nil, ctx.Err()
}

type chunkReader struct {
	chunks [][]byte
	reads  int
}

func (r *chunkReader) Read(p []byte) (int, error) {
	if r.reads == len(r.chunks) {
		return 0, io.EOF
	}
	chunk := r.chunks[r.reads]
	r.reads++
	return copy(p, chunk), nil
}

func TestThreePhaseWindowCancellationStopsBeforeLaterRead(t *testing.T) {
	reader := &chunkReader{chunks: [][]byte{[]byte("keep\n"), []byte("later\n")}}
	be := blockingBackend{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resultCh := make(chan error, 1)
	go func() {
		_, err := Execute(ctx, reader, ioDiscard{}, Options{Query: `. =~ "keep"`, InputMode: input.ModeRaw, Backend: be, WindowBytes: int64(len("keep\n"))})
		resultCh <- err
	}()
	<-be.started
	cancel()
	if err := <-resultCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("Execute error = %v, want context cancellation", err)
	}
	if reader.reads != 1 {
		t.Fatalf("reader reads = %d, want no later-frame read after cancellation", reader.reads)
	}
}

type failOnWrite struct {
	failAt int
	writes int
	buf    bytes.Buffer
}

func (w *failOnWrite) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, errors.New("synthetic writer failure")
	}
	return w.buf.Write(p)
}

func TestThreePhaseWindowWriterFailureIsFrameSpecificAndStopsExecution(t *testing.T) {
	writer := &failOnWrite{failAt: 2}
	_, err := Execute(context.Background(), strings.NewReader("keep\nkeep\nlater\n"), writer, Options{Query: `. =~ "keep"`, InputMode: input.ModeRaw, Output: output.Options{Compact: true}, Backend: &backend.MockBackend{}, SemanticCache: semanticcache.NewStore(), WindowBytes: 1024})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 {
		t.Fatalf("error = %T %[1]v, want writer RuntimeError for frame 2", err)
	}
	if got, want := writer.buf.String(), "true\n"; got != want {
		t.Fatalf("written prefix = %q, want %q", got, want)
	}
	if writer.writes != 2 {
		t.Fatalf("writer calls = %d, want later frame suppressed", writer.writes)
	}
}

type observingBackend struct {
	mu       sync.Mutex
	calls    int
	maxBatch int
}

func (b *observingBackend) Warm(context.Context) error { return nil }
func (b *observingBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.mu.Lock()
	b.calls++
	if len(batch) > b.maxBatch {
		b.maxBatch = len(batch)
	}
	b.mu.Unlock()
	results := make([]backend.Result, len(batch))
	for i := range results {
		results[i] = backend.Result{Value: true}
	}
	return results, nil
}

func TestThreePhaseWindowLongStreamKeepsBackendBatchesBounded(t *testing.T) {
	const frames = 200
	be := &observingBackend{}
	var stdin strings.Builder
	for i := 0; i < frames; i++ {
		fmt.Fprintf(&stdin, "keep-%03d\n", i)
	}
	result, err := Execute(context.Background(), strings.NewReader(stdin.String()), ioDiscard{}, Options{Query: `. =~ "keep"`, InputMode: input.ModeRaw, Backend: be, SemanticCache: semanticcache.NewStore(), WindowBytes: int64(len("keep-000\n"))})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if be.calls != frames || be.maxBatch != 1 || result.RunStats.WindowCount != frames {
		t.Fatalf("calls/max/window = %d/%d/%d, want %d/1/%d", be.calls, be.maxBatch, result.RunStats.WindowCount, frames, frames)
	}
}

// ioDiscard avoids importing a concrete writer solely for these executor tests.
type ioDiscard struct{}

func (ioDiscard) Write(p []byte) (int, error) { return len(p), nil }
