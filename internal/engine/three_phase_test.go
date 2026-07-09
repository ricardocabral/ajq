package engine

import (
	"bytes"
	"context"
	"errors"
	"io"
	"math/big"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/semantics"
)

type recordingBackend struct {
	batches [][]backend.Judgement
	results func([]backend.Judgement) []backend.Result
}

func (b *recordingBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	copied := append([]backend.Judgement(nil), batch...)
	b.batches = append(b.batches, copied)
	if b.results != nil {
		return b.results(copied), nil
	}
	results := make([]backend.Result, len(copied))
	for i, judgement := range copied {
		switch judgement.Return {
		case semantics.ReturnBool:
			want := ""
			if len(judgement.Specs) > 0 {
				want = judgement.Specs[0]
			}
			results[i] = backend.Result{Value: judgement.Value == want}
		case semantics.ReturnString:
			value := ""
			if len(judgement.Specs) > 0 {
				value = judgement.Specs[len(judgement.Specs)-1]
			}
			results[i] = backend.Result{Value: value}
		case semantics.ReturnNumber:
			results[i] = backend.Result{Value: 1.0}
		}
	}
	return results, nil
}

func (b *recordingBackend) Warm(context.Context) error { return nil }

func TestExecuteDesugarsInfixBeforeSemanticPlanAndCompile(t *testing.T) {
	be := &recordingBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"msg":"keep"},{"id":2,"msg":"drop"}]`), &stdout, Options{
		Query:     `.[] | select(.msg =~ "keep") | .id`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
		Backend:   be,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if stdout.String() != "1\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if len(be.batches) != 1 || len(be.batches[0]) != 2 {
		t.Fatalf("backend batches = %#v", be.batches)
	}
}

func TestExecuteDesugaredNonLiteralSpecUsesPlanError(t *testing.T) {
	_, err := Execute(context.Background(), strings.NewReader(`{"msg":"keep"}`), io.Discard, Options{
		Query:     `.msg =~ $spec`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
		Backend:   &recordingBackend{},
	})
	var planErr *PlanError
	if !errors.As(err, &planErr) {
		t.Fatalf("Execute error = %T %[1]v, want PlanError", err)
	}
	if len(planErr.Diagnostics) == 0 || planErr.Diagnostics[0].Code != plan.DiagnosticNonLiteralSpec {
		t.Fatalf("diagnostics = %#v, want non-literal spec", planErr.Diagnostics)
	}
}

func TestThreePhasePredicateResolveOnlyBackendCall(t *testing.T) {
	be := &recordingBackend{}
	program, err := compileThreePhase(`.[] | select(sem_match(.msg; "keep")) | .id`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{
		map[string]any{"id": 1, "msg": "keep"},
		map[string]any{"id": 2, "msg": "drop"},
	}

	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if len(be.batches) != 0 {
		t.Fatalf("harvest called backend %d times", len(be.batches))
	}
	if got := len(program.runtime.collected); got != 2 {
		t.Fatalf("harvest collected %d judgements, want 2", got)
	}

	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if len(be.batches) != 1 || len(be.batches[0]) != 2 {
		t.Fatalf("resolve batches = %#v, want one batch of two", be.batches)
	}

	var emitted []any
	if _, err := program.execute(inputValue, func(value any) error {
		emitted = append(emitted, value)
		return nil
	}); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if len(be.batches) != 1 {
		t.Fatalf("execute called backend; batches = %d", len(be.batches))
	}
	if len(emitted) != 1 || emitted[0] != 1 {
		t.Fatalf("emitted = %#v, want [1]", emitted)
	}
}

func TestThreePhaseResolveDedupsCanonicalKeysInFirstSeenOrder(t *testing.T) {
	be := &recordingBackend{}
	program, err := compileThreePhase(`.[] | sem_match(.; "keep")`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{
		map[string]any{"id": 1, "msg": "a"},
		map[string]any{"id": 2, "msg": "b"},
		map[string]any{"msg": "a", "id": 1},
		map[string]any{"id": 3, "msg": "c"},
		map[string]any{"id": 4, "msg": "d"},
	}

	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := len(program.runtime.collected); got != 5 {
		t.Fatalf("harvest collected %d judgements, want prototype fixture size 5", got)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if len(be.batches) != 1 || len(be.batches[0]) != 4 {
		t.Fatalf("resolve batches = %#v, want one deduped batch of four", be.batches)
	}
	for i, wantID := range []int{1, 2, 3, 4} {
		value, ok := be.batches[0][i].Value.(map[string]any)
		if !ok || value["id"] != wantID {
			t.Fatalf("deduped batch[%d] value = %#v, want first-seen id %d", i, be.batches[0][i].Value, wantID)
		}
	}
	if _, err := program.execute(inputValue, func(any) error { return nil }); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
}

func TestThreePhaseResolveSkipsAlreadyCachedKeys(t *testing.T) {
	be := &recordingBackend{}
	store := semanticcache.NewStore()
	program, err := compileThreePhaseWithOptions(`sem_match(.msg; "keep")`, be, "model-a", store)
	if err != nil {
		t.Fatalf("compileThreePhaseWithOptions returned error: %v", err)
	}
	inputValue := map[string]any{"msg": "keep"}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("first harvest returned error: %v", err)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("first resolve returned error: %v", err)
	}
	if len(be.batches) != 1 || len(be.batches[0]) != 1 {
		t.Fatalf("first resolve batches = %#v, want one batch of one", be.batches)
	}
	if got := be.batches[0][0].ModelID; got != "model-a" {
		t.Fatalf("backend judgement model id = %q, want model-a", got)
	}
	if got := be.batches[0][0].Schema.Type; got != semantics.ReturnBool {
		t.Fatalf("backend judgement schema type = %q, want bool", got)
	}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("second harvest returned error: %v", err)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("second resolve returned error: %v", err)
	}
	if len(be.batches) != 1 {
		t.Fatalf("cached second resolve called backend; batches = %#v", be.batches)
	}
	programOtherModel, err := compileThreePhaseWithOptions(`sem_match(.msg; "keep")`, be, "model-b", store)
	if err != nil {
		t.Fatalf("other model compile returned error: %v", err)
	}
	if err := programOtherModel.harvest(inputValue); err != nil {
		t.Fatalf("other model harvest returned error: %v", err)
	}
	if err := programOtherModel.resolve(context.Background()); err != nil {
		t.Fatalf("other model resolve returned error: %v", err)
	}
	if len(be.batches) != 2 {
		t.Fatalf("different model id reused cached value; batches = %#v", be.batches)
	}
	if got := be.batches[1][0].ModelID; got != "model-b" {
		t.Fatalf("other backend judgement model id = %q, want model-b", got)
	}
}

func TestThreePhaseResolveValidatesDedupedResultCount(t *testing.T) {
	be := &recordingBackend{results: func([]backend.Judgement) []backend.Result { return nil }}
	program, err := compileThreePhase(`.[] | sem_match(.msg; "keep")`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{map[string]any{"msg": "keep"}, map[string]any{"msg": "keep"}}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	err = program.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "for 1 judgements") {
		t.Fatalf("resolve error = %v, want deduped result count validation", err)
	}
}

func TestThreePhaseResolveRejectsWrongReturnType(t *testing.T) {
	be := &recordingBackend{results: func(batch []backend.Judgement) []backend.Result {
		results := make([]backend.Result, len(batch))
		for i := range results {
			results[i] = backend.Result{Value: "not-a-bool"}
		}
		return results
	}}
	program, err := compileThreePhase(`.[] | sem_match(.msg; "keep")`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest([]any{map[string]any{"msg": "x"}}); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	err = program.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "want bool result") {
		t.Fatalf("resolve error = %v, want return type validation", err)
	}
}

func TestThreePhaseResolveRejectsSchemaReturnMismatch(t *testing.T) {
	err := validateResult(backend.Judgement{
		Op:     "sem_match",
		Return: semantics.ReturnBool,
		Schema: backend.ResultSchema{Type: semantics.ReturnString},
	}, backend.Result{Value: true})
	if err == nil || !strings.Contains(err.Error(), "does not match return type") {
		t.Fatalf("validateResult error = %v, want schema/return mismatch", err)
	}
}

// TestValidateResultStructuralInvariance proves the deterministic boundary
// rejects each class of structurally invalid backend result before caching,
// with diagnostics that name the op and expected type.
func TestValidateResultStructuralInvariance(t *testing.T) {
	cases := []struct {
		name    string
		j       backend.Judgement
		r       backend.Result
		wantSub string
	}{
		{
			name:    "enum out of set",
			j:       backend.Judgement{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"yes", "no"}}, Specs: []string{"yes", "no"}},
			r:       backend.Result{Value: "maybe"},
			wantSub: "not one of labels",
		},
		{
			name:    "bool as string",
			j:       backend.Judgement{Op: "sem_match", Return: semantics.ReturnBool},
			r:       backend.Result{Value: "true"},
			wantSub: "want bool result",
		},
		{
			name:    "number as string",
			j:       backend.Judgement{Op: "sem_score", Return: semantics.ReturnNumber},
			r:       backend.Result{Value: "0.5"},
			wantSub: "want number result",
		},
		{
			name:    "string as bool",
			j:       backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString},
			r:       backend.Result{Value: true},
			wantSub: "want string result",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateResult(tc.j, tc.r)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("validateResult error = %v, want substring %q", err, tc.wantSub)
			}
			if !strings.Contains(err.Error(), tc.j.Op) {
				t.Fatalf("validateResult error = %v, want op name %q for debuggability", err, tc.j.Op)
			}
		})
	}
}

// TestValidateResultAcceptsValidValues confirms the boundary passes correctly
// typed values (including all numeric shapes the resolver may produce).
func TestValidateResultAcceptsValidValues(t *testing.T) {
	cases := []struct {
		j backend.Judgement
		r backend.Result
	}{
		{backend.Judgement{Op: "sem_match", Return: semantics.ReturnBool}, backend.Result{Value: true}},
		{backend.Judgement{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"a", "b"}}, Specs: []string{"a", "b"}}, backend.Result{Value: "b"}},
		{backend.Judgement{Op: "sem_score", Return: semantics.ReturnNumber}, backend.Result{Value: 0.5}},
		{backend.Judgement{Op: "sem_score", Return: semantics.ReturnNumber}, backend.Result{Value: big.NewInt(3)}},
		{backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString}, backend.Result{Value: "anything"}},
	}
	for i, tc := range cases {
		if err := validateResult(tc.j, tc.r); err != nil {
			t.Fatalf("case %d validateResult error = %v, want nil", i, err)
		}
	}
}

func TestThreePhaseResolveRejectsPerResultErrorWithoutCaching(t *testing.T) {
	fail := true
	be := &recordingBackend{results: func(batch []backend.Judgement) []backend.Result {
		results := make([]backend.Result, len(batch))
		for i, judgement := range batch {
			if fail {
				results[i] = backend.Result{Error: "model refused item"}
				continue
			}
			results[i] = backend.Result{Value: judgement.Value == "keep"}
		}
		return results
	}}
	program, err := compileThreePhase(`.[] | sem_match(.msg; "keep")`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{map[string]any{"msg": "keep"}}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	err = program.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "model refused item") {
		t.Fatalf("resolve error = %v, want per-result error", err)
	}
	fail = false
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("second resolve returned error: %v", err)
	}
	if len(be.batches) != 2 {
		t.Fatalf("per-result error was cached or skipped; batches = %#v", be.batches)
	}
}

func TestThreePhaseExecuteCacheMissIsLoud(t *testing.T) {
	program, err := compileThreePhase(`.[] | select(sem_match(.msg; "keep"))`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	_, err = program.execute([]any{map[string]any{"msg": "keep"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "cache miss") {
		t.Fatalf("execute error = %v, want cache miss", err)
	}
}

func TestThreePhaseOverHarvestPredicateChainCollectsDownstream(t *testing.T) {
	program, err := compileThreePhase(`.[] | select(sem_match(.gate; "yes")) | sem_match(.downstream; "needed")`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{map[string]any{"gate": "no", "downstream": "needed"}}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := len(program.runtime.collected); got != 2 {
		t.Fatalf("harvest collected %d judgements, want gate plus downstream due permissive predicate", got)
	}
	if program.runtime.collected[1].Value != "needed" {
		t.Fatalf("downstream judgement value = %#v", program.runtime.collected[1].Value)
	}
}

func TestThreePhasePredicateHarvestExploresAlternateBranches(t *testing.T) {
	program, err := compileThreePhase(`if sem_match(.gate; "yes") then sem_match(.a; "x") else sem_match(.b; "y") end`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := map[string]any{"gate": "no", "a": "x", "b": "y"}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := len(program.runtime.collected); got != 3 {
		t.Fatalf("harvest collected %d judgements, want condition plus both branches", got)
	}
	if program.runtime.collected[1].Value != "x" || program.runtime.collected[2].Value != "y" {
		t.Fatalf("branch judgements = %#v", program.runtime.collected)
	}
}

func TestThreePhaseClassifyHarvestUsesGeneratorSuperset(t *testing.T) {
	program, err := compileThreePhase(`.[] | select(sem_classify(.msg; "a"; "b") == "b") | sem_match(.downstream; "needed")`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{map[string]any{"msg": "anything", "downstream": "needed"}}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := len(program.runtime.collected); got != 2 {
		t.Fatalf("harvest collected %d judgements, want classify plus downstream sem_match", got)
	}
	if program.runtime.collected[0].Op != "sem_classify" || program.runtime.collected[1].Op != "sem_match" {
		t.Fatalf("collected = %#v", program.runtime.collected)
	}
}

func TestGatedThreePhaseClassifyImplicitThreeLabelsUsesPlannedLabels(t *testing.T) {
	program, err := compileThreePhase(`sem_classify("bug"; "billing"; "other")`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest("urgent bug"); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := program.runtime.collected[0].Value; got != "urgent bug" {
		t.Fatalf("classify judgement value = %#v, want input", got)
	}
	if got := program.runtime.collected[0].Specs; len(got) != 3 || got[0] != "bug" || got[2] != "other" {
		t.Fatalf("classify specs = %#v, want all implicit labels", got)
	}
}

func TestThreePhaseRejectsUnsupportedUnboundedValueOps(t *testing.T) {
	_, err := compileThreePhase(`try sem_extract(.x; "foo") catch "fallback" | sem_match(. ; "ok")`, &recordingBackend{})
	if err == nil || !strings.Contains(err.Error(), "sem_extract") {
		t.Fatalf("compileThreePhase error = %v, want unsupported sem_extract", err)
	}
}

func TestThreePhaseRejectsOrderSensitiveGeneratorContexts(t *testing.T) {
	queries := []string{
		`if first(sem_match(.gate; "yes")) then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`if first (sem_match(.gate; "yes")) then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`if last(sem_match(.gate; "yes")) then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`if any(sem_match(.gate; "yes"); .) then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`if all(sem_match(.gate; "yes"); .) then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`while(sem_match(.gate; "yes"); .)`,
		`reduce sem_match(.gate; "yes") as $x (false; . or $x)`,
		`([sem_classify(.x; "a"; "b")] | .[0]) == "b"`,
		`if ([(sem_classify(.x; "a"; "b"))] | .[0]) == "b" then sem_match(.a; "x") else sem_match(.b; "y") end`,
		`def g: sem_match(.gate; "yes"); if ([g] | length) == 2 then sem_match(.a; "x") else sem_match(.b; "y") end`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			_, err := compileThreePhase(query, &recordingBackend{})
			if err == nil || !strings.Contains(err.Error(), "order/cardinality-sensitive") {
				t.Fatalf("compileThreePhase error = %v, want order/cardinality rejection", err)
			}
		})
	}
}

func TestThreePhaseSemanticCompileErrorUsesCompileError(t *testing.T) {
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`{"x":"y"}`), &stdout, Options{
		Query:     `sem_match($missing; "x")`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
		Backend:   &recordingBackend{},
	})
	var compileErr *CompileError
	if !errors.As(err, &compileErr) {
		t.Fatalf("Execute error = %T %[1]v, want CompileError", err)
	}
}

func TestThreePhaseRejectsClassifyResultOutsideLabels(t *testing.T) {
	be := &recordingBackend{results: func(batch []backend.Judgement) []backend.Result {
		results := make([]backend.Result, len(batch))
		for i, judgement := range batch {
			if judgement.Op == "sem_classify" {
				results[i] = backend.Result{Value: "c"}
				continue
			}
			results[i] = backend.Result{Value: true}
		}
		return results
	}}
	program, err := compileThreePhase(`if sem_classify(.x; "a"; "b") == "c" then sem_match(.a; "x") else sem_match(.b; "y") end`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest(map[string]any{"x": "v", "a": "x", "b": "y"}); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	err = program.resolve(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not one of labels") {
		t.Fatalf("resolve error = %v, want classify label validation", err)
	}
}

func TestExecutePureJQDoesNotCallBackend(t *testing.T) {
	be := &recordingBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`[{"id":1},{"id":2}]`), &stdout, Options{
		Query:     `.[] | .id`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
		Backend:   be,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if stdout.String() != "1\n2\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if len(be.batches) != 0 {
		t.Fatalf("pure jq called backend %d times", len(be.batches))
	}
}
