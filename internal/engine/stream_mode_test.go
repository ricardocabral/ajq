package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

const streamTestQuery = `select(sem_match(.msg; "keep")) | .id`

type twoFrameReader struct {
	first, second       []byte
	stage               int
	release             chan struct{}
	secondReadRequested chan struct{}
	requestOnce         sync.Once
}

func (r *twoFrameReader) Read(p []byte) (int, error) {
	switch r.stage {
	case 0:
		r.stage++
		return copy(p, r.first), nil
	case 1:
		r.requestOnce.Do(func() { close(r.secondReadRequested) })
		<-r.release
		r.stage++
		return copy(p, r.second), nil
	default:
		return 0, io.EOF
	}
}

type observedWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan struct{}
	once   sync.Once
}

func (w *observedWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	n, err := w.buf.Write(p)
	w.mu.Unlock()
	w.once.Do(func() { close(w.writes) })
	return n, err
}

func (w *observedWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

type observedBackend struct {
	backend.MockBackend
	judged chan struct{}
	once   sync.Once
}

func (b *observedBackend) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.once.Do(func() { close(b.judged) })
	return b.MockBackend.Judge(ctx, batch)
}

func waitStreamSignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for %s", what)
	}
}

func TestStreamEmitsFirstFrameBeforeWindowFills(t *testing.T) {
	run := func(stream bool) (*twoFrameReader, *observedBackend, *observedWriter, chan error) {
		release := make(chan struct{})
		reader := &twoFrameReader{
			first:               []byte(`{"id":1,"msg":"keep"}` + "\n"),
			second:              []byte(`{"id":2,"msg":"keep"}` + "\n"),
			release:             release,
			secondReadRequested: make(chan struct{}),
		}
		be := &observedBackend{judged: make(chan struct{})}
		writer := &observedWriter{writes: make(chan struct{})}
		done := make(chan error, 1)
		go func() {
			_, err := Execute(context.Background(), reader, writer, Options{
				Query:         streamTestQuery,
				InputMode:     input.ModeAuto,
				Output:        output.Options{Compact: true},
				Backend:       be,
				SemanticCache: semanticcache.NewStore(),
				WindowBytes:   1024,
				Stream:        stream,
			})
			done <- err
		}()
		return reader, be, writer, done
	}

	streamReader, streamBackend, streamWriter, streamDone := run(true)
	waitStreamSignal(t, streamBackend.judged, "first streamed backend call")
	waitStreamSignal(t, streamWriter.writes, "first streamed output")
	if got := streamWriter.String(); got != "1\n" {
		t.Fatalf("stream output before second frame = %q, want first frame", got)
	}
	waitStreamSignal(t, streamReader.secondReadRequested, "stream reader waiting for second frame")
	close(streamReader.release)
	if err := <-streamDone; err != nil {
		t.Fatalf("stream Execute: %v", err)
	}
	if got := streamWriter.String(); got != "1\n2\n" {
		t.Fatalf("stream output = %q", got)
	}

	windowReader, windowBackend, windowWriter, windowDone := run(false)
	waitStreamSignal(t, windowReader.secondReadRequested, "windowed reader waiting for second frame")
	select {
	case <-windowBackend.judged:
		t.Fatal("windowed execution resolved before the blocked second frame")
	case <-windowWriter.writes:
		t.Fatal("windowed execution emitted before the blocked second frame")
	default:
	}
	close(windowReader.release)
	if err := <-windowDone; err != nil {
		t.Fatalf("windowed Execute: %v", err)
	}
	if got := windowWriter.String(); got != "1\n2\n" {
		t.Fatalf("windowed output = %q", got)
	}
}

func TestStreamPreservesInlineCacheOrderingAndCaps(t *testing.T) {
	store := semanticcache.NewStore()
	stdin := `{"id":1,"msg":"keep"}` + "\n" + `{"id":2,"msg":"keep"}` + "\n"
	run := func(be backend.Backend, maxCalls int) (Result, string, error) {
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{
			Query: streamTestQuery, InputMode: input.ModeAuto, Output: output.Options{Compact: true},
			Backend: be, SemanticCache: store, MaxCalls: maxCalls, Stream: true,
		})
		return result, stdout.String(), err
	}

	firstBackend := &backend.MockBackend{}
	first, firstOutput, err := run(firstBackend, 0)
	if err != nil || first.RunStats.ExecutionMode != ExecutionModeUserStream || firstOutput != "1\n2\n" || len(firstBackend.Inputs()) != 1 || first.RunStats.CacheHits != 1 {
		t.Fatalf("stream miss result/output/calls = %#v/%q/%d: %v", first, firstOutput, len(firstBackend.Inputs()), err)
	}
	cachedBackend := &backend.MockBackend{}
	cached, cachedOutput, err := run(cachedBackend, 1)
	if err != nil || cachedOutput != "1\n2\n" || len(cachedBackend.Inputs()) != 0 || cached.RunStats.CacheHits != 2 || cached.RunStats.PostDedupBackendCalls != 0 {
		t.Fatalf("stream cache result/output/calls = %#v/%q/%d: %v", cached, cachedOutput, len(cachedBackend.Inputs()), err)
	}

	capBackend := &backend.MockBackend{}
	_, err = Execute(context.Background(), strings.NewReader(`{"id":3,"msg":"other"}`+"\n"+`{"id":4,"msg":"new"}`+"\n"), io.Discard, Options{
		Query: streamTestQuery, InputMode: input.ModeAuto, Backend: capBackend, SemanticCache: store, MaxCalls: 1, Stream: true,
	})
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) || capErr.Cap != 1 || capErr.Needed != 2 || len(capBackend.Inputs()) != 1 {
		t.Fatalf("stream cap error/calls = %v/%d, want pre-call N+1 cap", err, len(capBackend.Inputs()))
	}
}

func TestStreamPreservesPlannerInterleavingPureJQAndExitStatus(t *testing.T) {
	plannerQuery := `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`
	plannerInput := `[{"id":1,"msg":"urgent"},{"id":2,"msg":"low"}]`
	runPlanner := func(stream bool) (Result, string, error) {
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(plannerInput), &stdout, Options{
			Query: plannerQuery, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: &backend.MockBackend{}, Stream: stream,
		})
		return result, stdout.String(), err
	}
	plain, plainOut, plainErr := runPlanner(false)
	stream, streamOut, streamErr := runPlanner(true)
	if plainErr != nil || streamErr != nil || plainOut != streamOut || plain.RunStats.ExecutionMode != ExecutionModePlannerInterleaved || stream.RunStats.ExecutionMode != ExecutionModePlannerInterleaved {
		t.Fatalf("planner parity plain=(%#v,%q,%v) stream=(%#v,%q,%v)", plain, plainOut, plainErr, stream, streamOut, streamErr)
	}

	var plainPure, streamPure bytes.Buffer
	plainResult, plainErr := Execute(context.Background(), strings.NewReader("true\n"), &plainPure, Options{Query: ".", InputMode: input.ModeAuto, Output: output.Options{Compact: true}})
	streamResult, streamErr := Execute(context.Background(), strings.NewReader("true\n"), &streamPure, Options{Query: ".", InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Stream: true})
	if plainErr != nil || streamErr != nil || plainPure.String() != streamPure.String() || plainResult.RunStats.ExecutionMode != ExecutionModePureJQ || streamResult.RunStats.ExecutionMode != ExecutionModePureJQ || ExitStatusCode(plainResult) != ExitStatusCode(streamResult) || ExitStatusCode(streamResult) != 0 {
		t.Fatalf("pure jq stream parity plain=(%q,%#v,%v) stream=(%q,%#v,%v)", plainPure.String(), plainResult, plainErr, streamPure.String(), streamResult, streamErr)
	}
}

type contextBackend struct{}

func (contextBackend) Warm(context.Context) error { return nil }
func (contextBackend) Judge(ctx context.Context, _ []backend.Judgement) ([]backend.Result, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

type failingStreamWriter struct{}

func (failingStreamWriter) Write([]byte) (int, error) { return 0, errors.New("stream writer failed") }

func TestStreamPropagatesCancellationAndWriterErrors(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Execute(ctx, strings.NewReader(`{"msg":"keep"}`), io.Discard, Options{Query: streamTestQuery, InputMode: input.ModeAuto, Backend: contextBackend{}, Stream: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("stream cancellation error = %v, want context canceled", err)
	}

	_, err = Execute(context.Background(), strings.NewReader(`{"id":1,"msg":"keep"}`), failingStreamWriter{}, Options{Query: streamTestQuery, InputMode: input.ModeAuto, Backend: &backend.MockBackend{}, Stream: true})
	if err == nil || !strings.Contains(err.Error(), "stream writer failed") {
		t.Fatalf("stream writer error = %v", err)
	}
}
