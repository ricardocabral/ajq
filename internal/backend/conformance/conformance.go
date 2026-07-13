// Package conformance defines the backend acceptance suite shared by every
// ajq semantic backend implementation.
package conformance

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// Factory constructs a backend under test. HTTP backends receive the httptest
// server URL for their wire-protocol fake. In-process backends may ignore it.
type Factory func(serverURL string) backend.Backend

// Option customizes a conformance run.
type Option func(*config)

type config struct {
	server               *ScriptedServer
	caseFilter           func(Case) bool
	expectFailure        bool
	concurrentDispatcher bool
}

// WithScriptedServer supplies the wire-protocol fake used by HTTP backends.
func WithScriptedServer(server *ScriptedServer) Option {
	return func(cfg *config) { cfg.server = server }
}

// WithCaseFilter runs only matching cases. It is used by the opt-in live suite
// to keep provider calls small while sharing the same invariant checks.
func WithCaseFilter(filter func(Case) bool) Option {
	return func(cfg *config) { cfg.caseFilter = filter }
}

// ExpectFailure turns the suite into a negative control: Run succeeds only if
// at least one conformance case detects the deliberately broken backend.
func ExpectFailure() Option {
	return func(cfg *config) { cfg.expectFailure = true }
}

// WithConcurrentDispatcher enables conformance cases that require a backend
// factory configured with more than one in-flight judgement slot.
func WithConcurrentDispatcher() Option {
	return func(cfg *config) { cfg.concurrentDispatcher = true }
}

// Case describes one backend contract invariant.
type Case struct {
	Name string
	Live bool
	run  func(context.Context, backend.Backend) error
}

// LiveCaseFilter selects only deterministic no/low-call cases. Live provider
// tests should prefer RunLiveSmoke, which validates schema invariance without
// asserting exact model text or numeric scores.
func LiveCaseFilter(c Case) bool { return c.Live }

// Run executes the cross-backend contract suite. The conformance package never
// imports concrete backend implementations; callers provide a factory and, for
// HTTP backends, a scripted server matching their wire protocol.
func Run(t *testing.T, factory Factory, opts ...Option) {
	t.Helper()
	cfg := config{}
	for _, opt := range opts {
		opt(&cfg)
	}

	failures := 0
	for _, tc := range Cases() {
		if cfg.caseFilter != nil && !cfg.caseFilter(tc) {
			continue
		}
		t.Run(tc.Name, func(t *testing.T) {
			if cfg.server != nil {
				cfg.server.Reset()
				cfg.server.StartCase(tc.Name)
			}
			be := factory(serverURL(cfg.server))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := tc.run(ctx, be); err != nil {
				if cfg.expectFailure {
					failures++
					t.Logf("negative control observed expected conformance failure: %v", err)
					return
				}
				t.Fatal(err)
			}
			if cfg.expectFailure {
				t.Log("negative control backend unexpectedly passed this case")
			}
		})
	}
	if cfg.expectFailure && failures == 0 {
		t.Fatalf("negative control backend passed all conformance cases")
	}
	if cfg.concurrentDispatcher && !cfg.expectFailure {
		t.Run("concurrent_transport_failure_cancels_admitted_sibling", func(t *testing.T) {
			if cfg.server == nil {
				t.Fatal("concurrent dispatcher conformance requires a scripted server")
			}
			cfg.server.Reset()
			cfg.server.StartCase("concurrent_transport_failure_cancels_admitted_sibling")
			be := factory(serverURL(cfg.server))
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			if err := testConcurrentTransportFailureCancelsAdmittedSibling(ctx, be, cfg.server); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func serverURL(server *ScriptedServer) string {
	if server == nil {
		return ""
	}
	return server.URL()
}

// Cases returns the canonical backend acceptance cases.
func Cases() []Case {
	return []Case{
		{Name: "mixed_batch_order_and_length", run: testMixedBatchOrderAndLength},
		{Name: "bool_string_is_per_item_error", run: testBoolStringIsPerItemError},
		{Name: "classify_out_of_enum_is_per_item_error", run: testClassifyOutOfEnumIsPerItemError},
		{Name: "number_invalid_json_is_per_item_error", run: testNumberInvalidJSONIsPerItemError},
		{Name: "transport_failure_is_whole_batch_error", run: testTransportFailureIsWholeBatchError},
		{Name: "per_item_failure_does_not_poison_sibling", run: testPerItemFailureDoesNotPoisonSibling},
		{Name: "empty_batch_returns_nil_nil", Live: true, run: testEmptyBatchReturnsNilNil},
		{Name: "context_cancellation_aborts_promptly", Live: true, run: testContextCancellationAbortsPromptly},
		{Name: "all_return_types_round_trip", run: testAllReturnTypesRoundTrip},
	}
}

// RunLiveSmoke executes an opt-in live provider smoke suite. It checks shape,
// batch length/order by per-position schema, empty-batch behavior, and prompt
// cancellation without requiring exact semantic text or score values from real
// models.
func RunLiveSmoke(t *testing.T, be backend.Backend) {
	t.Helper()
	t.Run("schema_valid_batch", func(t *testing.T) {
		batch := []backend.Judgement{
			judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket"),
			judgement("sem_classify", semantics.ReturnString, []string{"bug", "feature"}, []string{"bug", "feature"}, "bug report"),
			judgement("sem_norm", semantics.ReturnString, nil, []string{"normalize"}, "Round Trip"),
			judgement("sem_score", semantics.ReturnNumber, nil, []string{"hot"}, "hot"),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		results, err := be.Judge(ctx, batch)
		if err != nil {
			t.Fatalf("Judge returned whole-batch error: %v", err)
		}
		if err := requireSchemaValidResults(batch, results); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("empty_batch_returns_nil_nil", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := testEmptyBatchReturnsNilNil(ctx, be); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("context_cancellation_aborts_promptly", func(t *testing.T) {
		if err := testContextCancellationAbortsPromptly(context.Background(), be); err != nil {
			t.Fatal(err)
		}
	})
}

func testMixedBatchOrderAndLength(ctx context.Context, be backend.Backend) error {
	batch := []backend.Judgement{
		judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket"),
		judgement("sem_classify", semantics.ReturnString, []string{"bug", "feature"}, []string{"bug", "feature"}, "feature request"),
		judgement("sem_norm", semantics.ReturnString, nil, []string{"normalize"}, "Needs Trim"),
		judgement("sem_score", semantics.ReturnNumber, nil, []string{"hot"}, "warm"),
	}
	results, err := be.Judge(ctx, batch)
	if err != nil {
		return fmt.Errorf("Judge returned whole-batch error: %w", err)
	}
	want := []any{true, "feature", "needs_trim", float64(0.75)}
	return requireResults(results, want)
}

func testBoolStringIsPerItemError(ctx context.Context, be backend.Backend) error {
	results, err := be.Judge(ctx, []backend.Judgement{judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket")})
	if err != nil {
		return fmt.Errorf("wrong whole-batch error for bool type violation: %w", err)
	}
	return requireOnlyError(results, 0)
}

func testClassifyOutOfEnumIsPerItemError(ctx context.Context, be backend.Backend) error {
	results, err := be.Judge(ctx, []backend.Judgement{judgement("sem_classify", semantics.ReturnString, []string{"bug", "feature"}, []string{"bug", "feature"}, "unknown")})
	if err != nil {
		return fmt.Errorf("wrong whole-batch error for enum violation: %w", err)
	}
	return requireOnlyError(results, 0)
}

func testNumberInvalidJSONIsPerItemError(ctx context.Context, be backend.Backend) error {
	results, err := be.Judge(ctx, []backend.Judgement{judgement("sem_score", semantics.ReturnNumber, nil, []string{"hot"}, "hot")})
	if err != nil {
		return fmt.Errorf("wrong whole-batch error for invalid numeric JSON: %w", err)
	}
	return requireOnlyError(results, 0)
}

func testConcurrentTransportFailureCancelsAdmittedSibling(ctx context.Context, be backend.Backend, server *ScriptedServer) error {
	batch := []backend.Judgement{
		judgement("sem_match", semantics.ReturnBool, nil, []string{"x"}, "fail"),
		judgement("sem_match", semantics.ReturnBool, nil, []string{"x"}, "block"),
	}
	results, err := be.Judge(ctx, batch)
	if err == nil {
		return fmt.Errorf("Judge returned nil error and results %#v, want whole-batch error", results)
	}
	if len(results) != 0 {
		return fmt.Errorf("Judge returned partial results %#v with whole-batch error", results)
	}
	if err := server.WaitForConcurrentSiblingCancellation(ctx); err != nil {
		return err
	}
	return nil
}

func testTransportFailureIsWholeBatchError(ctx context.Context, be backend.Backend) error {
	results, err := be.Judge(ctx, []backend.Judgement{judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket")})
	if err == nil {
		return fmt.Errorf("Judge returned nil error and results %#v, want whole-batch error", results)
	}
	if len(results) != 0 {
		return fmt.Errorf("Judge returned partial results %#v with whole-batch error", results)
	}
	return nil
}

func testPerItemFailureDoesNotPoisonSibling(ctx context.Context, be backend.Backend) error {
	batch := []backend.Judgement{
		judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket"),
		judgement("sem_classify", semantics.ReturnString, []string{"bug", "feature"}, []string{"bug", "feature"}, "unknown"),
		judgement("sem_norm", semantics.ReturnString, nil, []string{"normalize"}, "Sibling OK"),
	}
	results, err := be.Judge(ctx, batch)
	if err != nil {
		return fmt.Errorf("per-item failure poisoned whole batch: %w", err)
	}
	if len(results) != len(batch) {
		return fmt.Errorf("len(results)=%d, want %d", len(results), len(batch))
	}
	if results[0].Error != "" || !reflect.DeepEqual(results[0].Value, true) {
		return fmt.Errorf("result 0 = %#v, want true without error", results[0])
	}
	if strings.TrimSpace(results[1].Error) == "" {
		return fmt.Errorf("result 1 = %#v, want per-item error", results[1])
	}
	if results[2].Error != "" || !reflect.DeepEqual(results[2].Value, "sibling_ok") {
		return fmt.Errorf("result 2 = %#v, want sibling_ok without error", results[2])
	}
	return nil
}

func testEmptyBatchReturnsNilNil(ctx context.Context, be backend.Backend) error {
	results, err := be.Judge(ctx, nil)
	if err != nil {
		return fmt.Errorf("empty batch error = %v, want nil", err)
	}
	if results != nil {
		return fmt.Errorf("empty batch results = %#v, want nil", results)
	}
	return nil
}

func testContextCancellationAbortsPromptly(_ context.Context, be backend.Backend) error {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	results, err := be.Judge(ctx, []backend.Judgement{judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "urgent ticket")})
	if err == nil {
		return fmt.Errorf("cancelled context returned nil error and results %#v", results)
	}
	if !errors.Is(err, context.Canceled) && !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
		return fmt.Errorf("cancelled context error = %v, want context.Canceled", err)
	}
	if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
		return fmt.Errorf("cancelled Judge took %s, want prompt abort", elapsed)
	}
	return nil
}

func testAllReturnTypesRoundTrip(ctx context.Context, be backend.Backend) error {
	batch := []backend.Judgement{
		judgement("sem_match", semantics.ReturnBool, nil, []string{"urgent"}, "ordinary"),
		judgement("sem_classify", semantics.ReturnString, []string{"bug", "feature"}, []string{"bug", "feature"}, "bug report"),
		judgement("sem_norm", semantics.ReturnString, nil, []string{"normalize"}, "Round Trip"),
		judgement("sem_score", semantics.ReturnNumber, nil, []string{"hot"}, "hot"),
	}
	results, err := be.Judge(ctx, batch)
	if err != nil {
		return fmt.Errorf("Judge returned whole-batch error: %w", err)
	}
	want := []any{false, "bug", "round_trip", float64(1)}
	return requireResults(results, want)
}

func judgement(op string, ret semantics.ReturnType, enum, specs []string, value any) backend.Judgement {
	return backend.Judgement{
		Op:     op,
		Kind:   semantics.KindValue,
		Return: ret,
		Schema: backend.ResultSchema{Type: ret, Enum: append([]string(nil), enum...)},
		Specs:  append([]string(nil), specs...),
		Value:  value,
	}
}

func requireResults(got []backend.Result, want []any) error {
	if len(got) != len(want) {
		return fmt.Errorf("len(results)=%d, want %d; results=%#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Error != "" {
			return fmt.Errorf("result %d unexpected error %q", i, got[i].Error)
		}
		if !valuesEqual(got[i].Value, want[i]) {
			return fmt.Errorf("result %d value = %#v (%T), want %#v (%T)", i, got[i].Value, got[i].Value, want[i], want[i])
		}
	}
	return nil
}

func requireOnlyError(results []backend.Result, index int) error {
	if len(results) != 1 {
		return fmt.Errorf("len(results)=%d, want 1; results=%#v", len(results), results)
	}
	if strings.TrimSpace(results[index].Error) == "" {
		return fmt.Errorf("result %d = %#v, want per-item error", index, results[index])
	}
	return nil
}

func requireSchemaValidResults(batch []backend.Judgement, results []backend.Result) error {
	if len(results) != len(batch) {
		return fmt.Errorf("len(results)=%d, want %d; results=%#v", len(results), len(batch), results)
	}
	for i, result := range results {
		if result.Error != "" {
			return fmt.Errorf("result %d unexpected per-item error %q", i, result.Error)
		}
		constraint, err := schema.ForJudgement(batch[i])
		if err != nil {
			return fmt.Errorf("case judgement %d invalid: %w", i, err)
		}
		if err := constraint.Validate(result.Value); err != nil {
			return fmt.Errorf("result %d failed schema validation: %w", i, err)
		}
	}
	return nil
}

func valuesEqual(got, want any) bool {
	switch w := want.(type) {
	case float64:
		g, ok := got.(float64)
		return ok && g == w
	default:
		return reflect.DeepEqual(got, want)
	}
}
