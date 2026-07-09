package engine

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/semantics"
)

func splitOutputs(t *testing.T, query string, be backend.Backend, input any) []any {
	t.Helper()
	program, err := compileThreePhase(query, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest(input); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	var output []any
	if _, err := program.execute(input, func(value any) error {
		output = append(output, value)
		return nil
	}); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	return output
}

func referenceOutputs(t *testing.T, query string, be backend.Backend, input any) []any {
	t.Helper()
	program, err := jq.CompileWithOptions(query, referenceOptions(be)...)
	if err != nil {
		t.Fatalf("reference compile returned error: %v", err)
	}
	var output []any
	if _, err := program.Run(input, func(value any) error {
		output = append(output, value)
		return nil
	}); err != nil {
		t.Fatalf("reference run returned error: %v", err)
	}
	return output
}

func TestSplitCorrectnessMatchesInterleavedReference(t *testing.T) {
	input := []any{
		map[string]any{"id": 1, "kind": "ticket", "msg": "keep", "gate": "yes", "downstream": "needed", "a": "x", "b": "n"},
		map[string]any{"id": 2, "kind": "ticket", "msg": "drop", "gate": "no", "downstream": "needed", "a": "n", "b": "y"},
		map[string]any{"id": 3, "kind": "note", "msg": "keep", "gate": "yes", "downstream": "skip", "a": "x", "b": "y"},
	}
	queries := []string{
		`.[] | select(.kind == "ticket" and sem_match(.msg; "keep")) | {id, label: sem_classify(.msg; "urgent"; "normal")}`,
		`.[] | if sem_match(.gate; "yes") then sem_match(.a; "x") else sem_match(.b; "y") end`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			split := splitOutputs(t, query, &recordingBackend{}, input)
			reference := referenceOutputs(t, query, &recordingBackend{}, input)
			if !reflect.DeepEqual(split, reference) {
				t.Fatalf("split output = %#v, reference = %#v", split, reference)
			}
		})
	}
}

func TestSplitCorrectnessOverHarvestPredicateChains(t *testing.T) {
	input := []any{
		map[string]any{"id": 1, "gate": "yes", "downstream": "needed"},
		map[string]any{"id": 2, "gate": "no", "downstream": "needed"},
		map[string]any{"id": 3, "gate": "yes", "downstream": "skip"},
	}
	query := `.[] | select(sem_match(.gate; "yes")) | select(sem_match(.downstream; "needed")) | .id`
	splitBackend := &recordingBackend{}
	split := splitOutputs(t, query, splitBackend, input)
	reference := referenceOutputs(t, query, &recordingBackend{}, input)
	if !reflect.DeepEqual(split, reference) {
		t.Fatalf("split output = %#v, reference = %#v", split, reference)
	}
	if len(splitBackend.batches) != 1 || len(splitBackend.batches[0]) != 4 {
		t.Fatalf("split harvested batch = %#v, want deduped gate and downstream judgements from over-harvested paths", splitBackend.batches)
	}
	if splitBackend.batches[0][3].Value != "skip" {
		t.Fatalf("split batch = %#v, want downstream judgement from alternate over-harvested path", splitBackend.batches[0])
	}
}

func TestSplitCorrectnessFixedCacheDeterministicOutput(t *testing.T) {
	be := &recordingBackend{}
	store := semanticcache.NewStore()
	query := `.[] | select(sem_match(.msg; "keep")) | .id`
	stdin := `[{"id":1,"msg":"keep"},{"id":2,"msg":"drop"},{"id":3,"msg":"keep"}]`
	run := func() string {
		t.Helper()
		var stdout bytes.Buffer
		_, err := Execute(context.Background(), strings.NewReader(stdin), &stdout, Options{
			Query:         query,
			InputMode:     input.ModeAuto,
			Output:        output.Options{Compact: true},
			Backend:       be,
			SemanticCache: store,
		})
		if err != nil {
			t.Fatalf("Execute returned error: %v", err)
		}
		return stdout.String()
	}
	first := run()
	backendCallsAfterFirst := len(be.batches)
	second := run()
	if first != second {
		t.Fatalf("fixed-cache outputs differ:\nfirst: %q\nsecond: %q", first, second)
	}
	if backendCallsAfterFirst == 0 || len(be.batches) != backendCallsAfterFirst {
		t.Fatalf("second run did not reuse fixed cache; batches before=%d after=%d", backendCallsAfterFirst, len(be.batches))
	}
}

func referenceOptions(be backend.Backend) []gojq.CompilerOption {
	return []gojq.CompilerOption{
		gojq.WithFunction("sem_match", 1, 2, referenceFunction(be, "sem_match")),
		gojq.WithFunction("sem_classify", 2, semantics.MaxJQFunctionArity, referenceFunction(be, "sem_classify")),
		gojq.WithFunction("sem_extract", 1, 2, referenceFunction(be, "sem_extract")),
		gojq.WithFunction("sem_score", 1, 2, referenceFunction(be, "sem_score")),
		gojq.WithFunction("sem_norm", 1, 2, referenceFunction(be, "sem_norm")),
		gojq.WithFunction("sem_redact", 1, 2, referenceFunction(be, "sem_redact")),
	}
}

func referenceFunction(be backend.Backend, op string) func(any, []any) any {
	return func(input any, args []any) any {
		judgement, err := judgementFromCall(op, input, args)
		if err != nil {
			return err
		}
		results, err := be.Judge(context.Background(), []backend.Judgement{judgement})
		if err != nil {
			return err
		}
		if len(results) != 1 {
			return fmt.Errorf("reference backend returned %d results for one judgement", len(results))
		}
		if err := validateResult(judgement, results[0]); err != nil {
			return err
		}
		return results[0].Value
	}
}
