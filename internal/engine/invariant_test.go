package engine

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/jq"
)

func TestSemanticInvariantPassesNestedSemanticQueries(t *testing.T) {
	be := &recordingBackend{}
	program, err := compileThreePhase(`.[] | select(sem_match(.msg; "keep")) | {id, label: sem_classify(.msg; "urgent"; "normal"), again: sem_match(.msg; "keep")}`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if got := len(program.runtime.plannedOrder); got != 3 {
		t.Fatalf("planned semantic nodes = %d, want 3", got)
	}
	seenIDs := make(map[int]bool, len(program.runtime.plannedOrder))
	for _, node := range program.runtime.plannedOrder {
		if node.ID == 0 || node.InternalName == "" {
			t.Fatalf("node missing deterministic identity: %#v", node)
		}
		if seenIDs[int(node.ID)] {
			t.Fatalf("duplicate semantic CallID %d in %#v", node.ID, program.runtime.plannedOrder)
		}
		seenIDs[int(node.ID)] = true
	}

	input := []any{map[string]any{"id": 1, "msg": "keep"}}
	if err := program.harvest(input); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if _, err := program.execute(input, nil); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	for _, witness := range program.runtime.fired {
		if !witness.Planned {
			t.Fatalf("unplanned witness fired in passing query: %#v", witness)
		}
	}
}

func TestSemanticInvariantRejectsMissedPlannerIDBeforeBackend(t *testing.T) {
	be := &recordingBackend{}
	query := `sem_match(.msg; "keep")`
	program, err := compileThreePhase(query, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	missed := program.runtime.plannedOrder[0]
	delete(program.runtime.planned, missed.ID)

	err = program.harvest(map[string]any{"msg": "keep"})
	var invariantErr *SemanticInvariantError
	if !errors.As(err, &invariantErr) {
		t.Fatalf("harvest error = %T %[1]v, want SemanticInvariantError", err)
	}
	if invariantErr.Witness.ID != missed.ID || invariantErr.Witness.Op != "sem_match" || invariantErr.Witness.Query != query || !strings.Contains(invariantErr.Witness.Source.Expression, "sem_match") {
		t.Fatalf("invariant witness = %#v, want id/op/query/source context", invariantErr.Witness)
	}
	if len(be.batches) != 0 {
		t.Fatalf("backend called before invariant abort: %#v", be.batches)
	}
}

func TestSemanticInvariantRejectsReservedInternalCallbackNamespace(t *testing.T) {
	queries := []string{
		`{u: __ajq_sem_0001(.msg; "keep"), s: sem_match(.msg; "keep")}`,
		`def __ajq_sem_0001($x; $y): false; sem_match(.msg; "keep")`,
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			be := &recordingBackend{}
			_, err := compileThreePhase(query, be)
			var planErr *PlanError
			if !errors.As(err, &planErr) {
				t.Fatalf("compileThreePhase error = %T %[1]v, want PlanError", err)
			}
			if len(planErr.Diagnostics) == 0 || !strings.Contains(planErr.Diagnostics[0].Message, "reserved") {
				t.Fatalf("diagnostics = %#v, want reserved namespace diagnostic", planErr.Diagnostics)
			}
			if len(be.batches) != 0 {
				t.Fatalf("backend called while rejecting reserved callback namespace: %#v", be.batches)
			}
		})
	}
}

func TestSemanticInvariantRejectsUnreplacedPublicSemanticCallBeforeBackend(t *testing.T) {
	be := &recordingBackend{}
	query := `sem_match(.msg; "keep")`
	program, err := compileThreePhase(query, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	publicHarvest, err := jq.CompileWithOptions(query, program.runtime.harvestOptions()...)
	if err != nil {
		t.Fatalf("public harvest compile returned error: %v", err)
	}
	program.harvestProgram = publicHarvest

	err = program.harvest(map[string]any{"msg": "keep"})
	var invariantErr *SemanticInvariantError
	if !errors.As(err, &invariantErr) {
		t.Fatalf("harvest error = %T %[1]v, want SemanticInvariantError", err)
	}
	if invariantErr.Witness.ID != 0 || invariantErr.Witness.Op != "sem_match" || invariantErr.Witness.Query != query || !strings.Contains(invariantErr.Witness.Source.Expression, "sem_match") || !strings.Contains(invariantErr.Error(), "op=sem_match") {
		t.Fatalf("invariant error = %v witness=%#v, want op/query/source context", invariantErr, invariantErr.Witness)
	}
	if len(be.batches) != 0 {
		t.Fatalf("backend called before invariant abort: %#v", be.batches)
	}
}

func TestSemanticInvariantPrefersUnplannedWitnessOverJQRunError(t *testing.T) {
	be := &recordingBackend{}
	program, err := compileThreePhase(`sem_match(.msg; "keep")`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	query := `sem_extract(.msg; "summary")`
	program.runtime.query = query
	publicHarvest, err := jq.CompileWithOptions(query, program.runtime.harvestOptions()...)
	if err != nil {
		t.Fatalf("public harvest compile returned error: %v", err)
	}
	program.harvestProgram = publicHarvest

	err = program.harvest(map[string]any{"msg": "keep"})
	var invariantErr *SemanticInvariantError
	if !errors.As(err, &invariantErr) {
		t.Fatalf("harvest error = %T %[1]v, want SemanticInvariantError instead of generic jq error", err)
	}
	if invariantErr.Witness.Op != "sem_extract" || invariantErr.Witness.Query != query {
		t.Fatalf("invariant witness = %#v, want sem_extract query context", invariantErr.Witness)
	}
	if len(be.batches) != 0 {
		t.Fatalf("backend called before invariant abort: %#v", be.batches)
	}
}
