package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/plan"
)

func TestIterativeHarvestPrunesLaterStages(t *testing.T) {
	backend := &recordingBackend{}
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"first":"yes","second":"second"},{"id":2,"first":"no","second":"pruned"},{"id":3,"first":"yes","second":"no"}]`), &stdout, Options{
		Query:            `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "second")) | .id`,
		InputMode:        input.ModeAuto,
		Output:           output.Options{Compact: true},
		Backend:          backend,
		IterativeHarvest: true,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if result.RunStats.ExecutionMode != ExecutionModeIterativeHarvest {
		t.Fatalf("mode = %q", result.RunStats.ExecutionMode)
	}
	if stdout.String() != "1\n" {
		t.Fatalf("stdout = %q, want 1", stdout.String())
	}
	if len(backend.batches) != 2 {
		t.Fatalf("batches = %#v, want two stages", backend.batches)
	}
	if got := len(backend.batches[0]); got != 2 {
		t.Fatalf("first-stage batch = %d, want one deduplicated batch of 2", got)
	}
	if got := len(backend.batches[1]); got != 2 {
		t.Fatalf("second-stage batch = %d, want 2 survivors", got)
	}
	for _, judgement := range backend.batches[1] {
		if judgement.Value == "pruned" {
			t.Fatalf("pruned value reached stage 2: %#v", judgement)
		}
	}
}

func TestIterativeHarvestIsDefaultOffAndFallsBack(t *testing.T) {
	query := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes")) | .id`
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"first":"yes","second":"yes"}]`), &stdout, Options{
		Query: query, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: &recordingBackend{},
	})
	if err != nil {
		t.Fatalf("default Execute returned error: %v", err)
	}
	if result.RunStats.ExecutionMode != ExecutionModeThreePhaseWindowed {
		t.Fatalf("default mode = %q", result.RunStats.ExecutionMode)
	}

	stdout.Reset()
	result, err = Execute(context.Background(), strings.NewReader(`[{"id":1,"first":"yes","second":"yes"}]`), &stdout, Options{
		Query: `.[] | if sem_match(.first; "yes") then .id else .id end`, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: &recordingBackend{}, IterativeHarvest: true,
	})
	if err != nil {
		t.Fatalf("fallback Execute returned error: %v", err)
	}
	if result.RunStats.ExecutionMode == ExecutionModeIterativeHarvest {
		t.Fatalf("unsupported query selected iterative mode")
	}
}

func TestIterativeHarvestConservativelyRejectsCapBeforeDispatch(t *testing.T) {
	backend := &recordingBackend{}
	_, err := Execute(context.Background(), strings.NewReader(`[{"first":"yes","second":"yes"},{"first":"no","second":"no"}]`), ioDiscard{}, Options{
		Query: `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "yes"))`, InputMode: input.ModeAuto, Backend: backend,
		IterativeHarvest: true, MaxCalls: 1,
	})
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) {
		t.Fatalf("Execute error = %T %[1]v, want MaxCallsExceededError", err)
	}
	if len(backend.batches) != 0 {
		t.Fatalf("cap rejection dispatched %#v", backend.batches)
	}
}

func TestIterativeHarvestResultErrorCompletesAndEmitsReservationPrefix(t *testing.T) {
	backend := &recordingBackend{results: func(batch []backend.Judgement) []backend.Result {
		results := make([]backend.Result, len(batch))
		for i, judgement := range batch {
			if judgement.Value == "boom" {
				results[i].Error = "synthetic failure"
				continue
			}
			results[i].Value = true
		}
		return results
	}}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader("{\"id\":1,\"first\":\"yes\",\"second\":\"second\"}\n{\"id\":2,\"first\":\"boom\",\"second\":\"never\"}\n"), &stdout, Options{
		Query: `. | select(sem_match(.first; "yes")) | select(sem_match(.second; "second")) | .id`, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: backend,
		IterativeHarvest: true,
	})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 {
		t.Fatalf("Execute error = %T %[1]v, want frame-2 RuntimeError", err)
	}
	if stdout.String() != "1\n" {
		t.Fatalf("stdout = %q, want completed prefix", stdout.String())
	}
	if len(backend.batches) != 2 || len(backend.batches[0]) != 2 || len(backend.batches[1]) != 1 {
		t.Fatalf("batches = %#v, want active batch plus one reservation-prefix completion", backend.batches)
	}
	if backend.batches[1][0].Value != "second" || backend.batches[1][0].Op != "sem_match" {
		t.Fatalf("prefix completion = %#v, want first frame's second gate", backend.batches[1][0])
	}
}

func TestIterativeHarvestFailureAndCancellationStopLaterStages(t *testing.T) {
	backend := &recordingBackend{results: func(batch []backend.Judgement) []backend.Result {
		results := make([]backend.Result, len(batch))
		results[0].Error = "synthetic failure"
		return results
	}}
	_, err := Execute(context.Background(), strings.NewReader(`[{"first":"yes","second":"second"}]`), ioDiscard{}, Options{
		Query: `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "second"))`, InputMode: input.ModeAuto, Backend: backend, IterativeHarvest: true,
	})
	if err == nil {
		t.Fatal("Execute succeeded after stage failure")
	}
	if got := len(backend.batches); got != 1 {
		t.Fatalf("batches after stage failure = %d, want no later stage", got)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	backend = &recordingBackend{}
	_, err = Execute(ctx, strings.NewReader(`[{"first":"yes","second":"second"}]`), ioDiscard{}, Options{
		Query: `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "second"))`, InputMode: input.ModeAuto, Backend: backend, IterativeHarvest: true,
	})
	if err == nil {
		t.Fatal("Execute succeeded with cancelled context")
	}
	if got := len(backend.batches); got != 0 {
		t.Fatalf("batches after cancellation = %d, want none", got)
	}
}

// keyedBackend makes each three-path run independent while keeping answers tied
// to semantic identity rather than incidental batching or resolver order.
type keyedBackend struct {
	mu        sync.Mutex
	batches   [][]backend.Judgement
	answer    func(backend.Judgement) backend.Result
	transport func([]backend.Judgement) error
}

func (b *keyedBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	copied := append([]backend.Judgement(nil), batch...)
	b.mu.Lock()
	b.batches = append(b.batches, copied)
	b.mu.Unlock()
	if b.transport != nil {
		if err := b.transport(copied); err != nil {
			return nil, err
		}
	}
	results := make([]backend.Result, len(copied))
	for i, judgement := range copied {
		if b.answer != nil {
			results[i] = b.answer(judgement)
			continue
		}
		results[i] = keyedResult(judgement)
	}
	return results, nil
}

func (b *keyedBackend) Warm(context.Context) error { return nil }

func keyedResult(j backend.Judgement) backend.Result {
	if j.Return == "bool" {
		return backend.Result{Value: len(j.Specs) > 0 && j.Value == j.Specs[0]}
	}
	if j.Return == "number" {
		return backend.Result{Value: 1.0}
	}
	if value, ok := j.Value.(string); ok {
		for _, label := range j.Specs {
			if value == label {
				return backend.Result{Value: value}
			}
		}
	}
	if len(j.Specs) == 0 {
		return backend.Result{}
	}
	return backend.Result{Value: j.Specs[len(j.Specs)-1]}
}

func (b *keyedBackend) values() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	var values []string
	for _, batch := range b.batches {
		for _, judgement := range batch {
			values = append(values, fmt.Sprintf("%s:%v", judgement.Op, judgement.Value))
		}
	}
	return values
}

type iterativePathOutcome struct {
	stdout string
	result Result
	err    error
	calls  []string
}

func runIterativeThreePaths(t *testing.T, ctx context.Context, stdin, query string, base Options, makeBackend func() *keyedBackend) (windowed, iterative, interleaved iterativePathOutcome) {
	t.Helper()
	run := func(mode string) iterativePathOutcome {
		t.Helper()
		be := makeBackend()
		store := semanticcache.NewStore()
		opts := base
		opts.Query, opts.Backend, opts.SemanticCache = query, be, store
		switch mode {
		case "iterative":
			opts.IterativeHarvest = true
		case "interleaved":
			opts.Stream = true
		}
		var stdout bytes.Buffer
		result, err := Execute(ctx, strings.NewReader(stdin), &stdout, opts)
		return iterativePathOutcome{stdout: stdout.String(), result: result, err: err, calls: be.values()}
	}
	return run("windowed"), run("iterative"), run("interleaved")
}

func comparableError(err error) string {
	if err == nil {
		return ""
	}
	var runtimeErr *RuntimeError
	if errors.As(err, &runtimeErr) {
		return fmt.Sprintf("runtime:%d:%T", runtimeErr.Frame, errors.Unwrap(runtimeErr))
	}
	var capErr *MaxCallsExceededError
	if errors.As(err, &capErr) {
		return fmt.Sprintf("cap:%d:%d", capErr.Cap, capErr.Needed)
	}
	return fmt.Sprintf("%T", err)
}

func assertIterativeParity(t *testing.T, want iterativePathOutcome, got iterativePathOutcome) {
	t.Helper()
	if got.stdout != want.stdout || got.result.Emitted != want.result.Emitted || !reflect.DeepEqual(got.result.Last, want.result.Last) || ExitStatusCode(got.result) != ExitStatusCode(want.result) || comparableError(got.err) != comparableError(want.err) {
		t.Fatalf("iterative/interleaved mismatch\niterative: stdout=%q emitted=%v last=%#v exit=%d err=%v\ninterleaved: stdout=%q emitted=%v last=%#v exit=%d err=%v", got.stdout, got.result.Emitted, got.result.Last, ExitStatusCode(got.result), got.err, want.stdout, want.result.Emitted, want.result.Last, ExitStatusCode(want.result), want.err)
	}
}

func TestIterativeHarvestThreePathParityTable(t *testing.T) {
	query2 := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	query3 := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | select(sem_match(.third; "final")) | .id`
	classify := `.[] | select(sem_classify(.kind; "low"; "medium"; "high") == "high") | select("high" == sem_classify(.tier; "a"; "b"; "high")) | .id`
	cases := []struct {
		name, query, stdin, want string
		stages                   int
	}{
		{"two gates zero survivors", query2, `[{"id":1,"first":"no","second":"keep"}]`, "", 2},
		{"two gates all survivors", query2, `[{"id":1,"first":"yes","second":"keep"},{"id":2,"first":"yes","second":"keep"}]`, "1\n2\n", 2},
		{"two gates some survivors and duplicate", query2, `[{"id":1,"first":"yes","second":"keep"},{"id":2,"first":"no","second":"pruned"},{"id":3,"first":"yes","second":"drop"},{"id":4,"first":"yes","second":"keep"}]`, "1\n4\n", 2},
		{"three gates", query3, `[{"id":1,"first":"yes","second":"keep","third":"final"},{"id":2,"first":"yes","second":"keep","third":"drop"},{"id":3,"first":"no","second":"pruned","third":"pruned"}]`, "1\n", 3},
		{"bounded enum equality orientations", classify, `[{"id":1,"kind":"high","tier":"high"},{"id":2,"kind":"low","tier":"high"}]`, "1\n", 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			base := Options{InputMode: input.ModeAuto, Output: output.Options{Compact: true}, WindowBytes: 32}
			windowed, iterative, interleaved := runIterativeThreePaths(t, context.Background(), tc.stdin, tc.query, base, func() *keyedBackend { return &keyedBackend{} })
			if windowed.err != nil || iterative.err != nil || interleaved.err != nil || windowed.stdout != tc.want || iterative.stdout != tc.want || interleaved.stdout != tc.want {
				t.Fatalf("outputs/errors windowed=%q/%v iterative=%q/%v interleaved=%q/%v", windowed.stdout, windowed.err, iterative.stdout, iterative.err, interleaved.stdout, interleaved.err)
			}
			assertIterativeParity(t, interleaved, iterative)
			if iterative.result.RunStats.ExecutionMode != ExecutionModeIterativeHarvest || len(iterative.calls) == 0 || len(iterative.calls) > tc.stages*2 {
				t.Fatalf("iterative mode/calls = %q/%v, want bounded staged calls", iterative.result.RunStats.ExecutionMode, iterative.calls)
			}
			if strings.Contains(strings.Join(iterative.calls, ","), "sem_match:pruned") {
				t.Fatalf("pruned value reached a later iterative stage: %v", iterative.calls)
			}
			if iterative.result.RunStats.PostDedupBackendCalls > windowed.result.RunStats.PostDedupBackendCalls {
				t.Fatalf("iterative spent %d > windowed %d", iterative.result.RunStats.PostDedupBackendCalls, windowed.result.RunStats.PostDedupBackendCalls)
			}
		})
	}
}

// The following is an intentional no-go characterization: normal windowed
// harvest observes errors on values that the sequential semantic definition
// never reaches. Iterative must retain interleaved semantics and pruning, not
// make an otherwise unreachable paid request merely to reproduce this bug.
func TestIterativeHarvestCharacterizesPrunedDownstreamWindowedErrorDivergence(t *testing.T) {
	query := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	stdin := `[{"id":1,"first":"no","second":"poison"}]`
	answer := func(j backend.Judgement) backend.Result {
		if j.Value == "poison" {
			return backend.Result{Error: "unreachable downstream error"}
		}
		return keyedResult(j)
	}
	base := Options{InputMode: input.ModeAuto, Output: output.Options{Compact: true}}
	windowed, iterative, interleaved := runIterativeThreePaths(t, context.Background(), stdin, query, base, func() *keyedBackend { return &keyedBackend{answer: answer} })
	if windowed.err == nil || !strings.Contains(strings.Join(windowed.calls, ","), "sem_match:poison") {
		t.Fatalf("windowed did not expose permissive downstream error: calls=%v err=%v", windowed.calls, windowed.err)
	}
	if iterative.err != nil || interleaved.err != nil || iterative.stdout != "" || interleaved.stdout != "" {
		t.Fatalf("reachable semantics must succeed: iterative=%q/%v interleaved=%q/%v", iterative.stdout, iterative.err, interleaved.stdout, interleaved.err)
	}
	assertIterativeParity(t, interleaved, iterative)
	for _, got := range [][]string{iterative.calls, interleaved.calls} {
		if strings.Contains(strings.Join(got, ","), "poison") {
			t.Fatalf("pruned downstream key dispatched: %v", got)
		}
	}
}

func TestIterativeHarvestWarmCacheAcrossWindowsAndCaps(t *testing.T) {
	query := `select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	stdin := "{\"id\":1,\"first\":\"yes\",\"second\":\"keep\"}\n{\"id\":2,\"first\":\"yes\",\"second\":\"keep\"}\n"
	store := semanticcache.NewStore()
	be := &keyedBackend{}
	run := func(max int) (Result, string, error) {
		var stdout bytes.Buffer
		result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{Query: query, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: be, SemanticCache: store, IterativeHarvest: true, WindowBytes: 1, MaxCalls: max})
		return result, stdout.String(), err
	}
	first, out, err := run(2)
	if err != nil || out != "1\n2\n" || first.RunStats.PostDedupBackendCalls != 2 {
		t.Fatalf("equal cap result=%#v output=%q err=%v", first, out, err)
	}
	before := len(be.values())
	second, out, err := run(1)
	if err != nil || out != "1\n2\n" || second.RunStats.PostDedupBackendCalls != 0 || second.RunStats.CacheHits == 0 || len(be.values()) != before {
		t.Fatalf("warm cache identity/cap result=%#v output=%q calls=%v err=%v", second, out, be.values(), err)
	}
	cold := &keyedBackend{}
	_, err = Execute(context.Background(), strings.NewReader(stdin), ioDiscard{}, Options{Query: query, InputMode: input.ModeAuto, Backend: cold, IterativeHarvest: true, WindowBytes: 1, MaxCalls: 1})
	var capErr *MaxCallsExceededError
	if !errors.As(err, &capErr) || len(cold.values()) != 0 {
		t.Fatalf("exceeded cap err=%v calls=%v, want pre-dispatch cap rejection", err, cold.values())
	}
}

type blockingIterativeBackend struct {
	started chan struct{}
	calls   int
}

func (b *blockingIterativeBackend) Warm(context.Context) error { return nil }
func (b *blockingIterativeBackend) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.calls++
	if b.calls == 1 {
		close(b.started)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return nil, errors.New("later stage must not run")
}

func TestIterativeHarvestCancellationDuringActiveStageStopsLaterDispatch(t *testing.T) {
	be := &blockingIterativeBackend{started: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Execute(ctx, strings.NewReader(`[{"first":"yes","second":"keep"}]`), ioDiscard{}, Options{
			Query: `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep"))`, InputMode: input.ModeAuto, Backend: be, IterativeHarvest: true,
		})
		done <- err
	}()
	<-be.started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) || be.calls != 1 {
		t.Fatalf("cancellation error/calls = %v/%d, want one active call and no later stage", err, be.calls)
	}
}

func TestIterativeHarvestUnsupportedShapesNeverPartiallySelect(t *testing.T) {
	cases := []struct {
		name, query, stdin string
	}{
		{"control flow", `.[] | if sem_match(.first; "yes") then .id else .id end`, `[{"id":1,"first":"yes"}]`},
		{"alternate generator", `.[] | (select(sem_match(.first; "yes")), .id)`, `[{"id":1,"first":"yes"}]`},
		{"binding", `.[] as $row | select(sem_match($row.first; "yes")) | $row.id`, `[{"id":1,"first":"yes"}]`},
		{"construction", `.[] | select(sem_match(.first; "yes")) | {id}`, `[{"id":1,"first":"yes"}]`},
		{"value operation", `.[] | sem_extract(.first; "field")`, `[{"first":"yes"}]`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := func(iterative bool) (Result, string, error) {
				var stdout bytes.Buffer
				result, err := Execute(context.Background(), strings.NewReader(tc.stdin), &stdout, Options{Query: tc.query, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: &keyedBackend{}, IterativeHarvest: iterative})
				return result, stdout.String(), err
			}
			plain, plainOut, plainErr := run(false)
			candidate, candidateOut, candidateErr := run(true)
			if candidate.RunStats.ExecutionMode == ExecutionModeIterativeHarvest || plainOut != candidateOut || comparableError(plainErr) != comparableError(candidateErr) || plain.Emitted != candidate.Emitted || !reflect.DeepEqual(plain.Last, candidate.Last) {
				t.Fatalf("unsupported fallback plain=(%q,%#v,%v) candidate=(%q,%#v,%v)", plainOut, plain, plainErr, candidateOut, candidate, candidateErr)
			}
		})
	}
}

func TestIterativeHarvestStreamAndPlannerPrecedence(t *testing.T) {
	query := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	result, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"first":"yes","second":"keep"}]`), ioDiscard{}, Options{Query: query, InputMode: input.ModeAuto, Backend: &keyedBackend{}, Stream: true, IterativeHarvest: true})
	if err != nil || result.RunStats.ExecutionMode != ExecutionModeUserStream {
		t.Fatalf("stream precedence result=%#v err=%v", result, err)
	}
	plannerQuery := `.[] | select(sem_score(.first; "yes") > 0.5) | .id`
	result, err = Execute(context.Background(), strings.NewReader(`[{"id":1,"first":"yes"}]`), ioDiscard{}, Options{Query: plannerQuery, InputMode: input.ModeAuto, Backend: &keyedBackend{}, Stream: true, IterativeHarvest: true})
	if err != nil || result.RunStats.ExecutionMode != ExecutionModePlannerInterleaved {
		t.Fatalf("planner precedence result=%#v err=%v", result, err)
	}
}

func TestIterativeHarvestDefersFrameDiagnosticAfterResolvedPrefix(t *testing.T) {
	query := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	stdin := "[{\"id\":1,\"first\":\"yes\",\"second\":\"keep\"}]\n1\n"
	var stdout bytes.Buffer
	result, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{Query: query, InputMode: input.ModeAuto, Output: output.Options{Compact: true}, Backend: &keyedBackend{}, IterativeHarvest: true, WindowBytes: 1024})
	var runtimeErr *RuntimeError
	if !errors.As(err, &runtimeErr) || runtimeErr.Frame != 2 || stdout.String() != "1\n" || result.RunStats.ExecutionMode != ExecutionModeIterativeHarvest {
		t.Fatalf("deferred frame diagnostic output/result/error = %q/%#v/%v", stdout.String(), result, err)
	}
}

func TestIterativeHarvestFiredCallsArePlannedAndStatsCountStages(t *testing.T) {
	query := `.[] | select(sem_match(.first; "yes")) | select(sem_match(.second; "keep")) | .id`
	semanticPlan, diagnostics := plan.Build(query)
	if len(diagnostics) != 0 {
		t.Fatalf("plan diagnostics = %#v", diagnostics)
	}
	stages, ok := plan.IterativeStages(query, semanticPlan)
	if !ok {
		t.Fatal("supported query was not staged")
	}
	stats := RunStats{}
	be := &keyedBackend{}
	program, err := compileIterative(query, be, "", semanticcache.NewStore(), &stats, stages)
	if err != nil {
		t.Fatalf("compileIterative: %v", err)
	}
	frames := []input.Frame{{Index: 0, Value: []any{map[string]any{"id": 1, "first": "yes", "second": "keep"}, map[string]any{"id": 2, "first": "no", "second": "pruned"}}}}
	var stdout bytes.Buffer
	result := Result{}
	if err := program.processWindow(context.Background(), &stdout, Options{Output: output.Options{Compact: true}}, &result, frames); err != nil {
		t.Fatalf("processWindow: %v", err)
	}
	if stdout.String() != "1\n" || stats.HarvestedJudgements != 3 || stats.PostDedupBackendCalls != 3 || len(be.batches) != 2 || len(be.batches[0]) != 2 || len(be.batches[1]) != 1 {
		t.Fatalf("output/stats/batches = %q/%#v/%#v", stdout.String(), stats, be.batches)
	}
	for _, witness := range program.runtime.fired {
		if !witness.Planned {
			t.Fatalf("fired call not in plan: %#v", witness)
		}
	}
	if strings.Contains(strings.Join(be.values(), ","), "pruned") {
		t.Fatalf("pruned downstream judgement was dispatched: %v", be.values())
	}
}
