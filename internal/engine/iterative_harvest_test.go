package engine

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
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
