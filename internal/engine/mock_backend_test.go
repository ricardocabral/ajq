package engine

import (
	"context"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
)

func TestMockBackendReceivesOneDedupedResolveBatch(t *testing.T) {
	be := &backend.MockBackend{}
	program, err := compileThreePhase(`.[] | select(sem_match(.msg; "keep")) | .id`, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	inputValue := []any{
		map[string]any{"id": 1, "msg": "keep"},
		map[string]any{"id": 2, "msg": "drop"},
		map[string]any{"id": 3, "msg": "keep"},
	}
	if err := program.harvest(inputValue); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if be.CallCount() != 0 {
		t.Fatalf("harvest called backend %d times", be.CallCount())
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("CallCount = %d, want 1", got)
	}
	if got := be.BatchCount(); got != 1 {
		t.Fatalf("BatchCount = %d, want 1", got)
	}
	batches := be.Batches()
	if len(batches) != 1 || len(batches[0]) != 2 {
		t.Fatalf("Batches = %#v, want one deduped batch of two", batches)
	}

	var emitted []any
	if _, err := program.execute(inputValue, func(value any) error {
		emitted = append(emitted, value)
		return nil
	}); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("execute called backend; CallCount = %d", got)
	}
	if len(emitted) != 2 || emitted[0] != 1 || emitted[1] != 3 {
		t.Fatalf("emitted = %#v, want [1 3]", emitted)
	}
}
