package backend

import (
	"context"
	"testing"

	"github.com/ricardocabral/ajq/internal/semantics"
)

func TestMockBackendRecordsWarmCallsAndImmutableInputs(t *testing.T) {
	be := &MockBackend{}
	if err := be.Warm(context.Background()); err != nil {
		t.Fatalf("Warm returned error: %v", err)
	}
	value := map[string]any{"msg": "keep"}
	batch := []Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"keep"}, Value: value}}
	if _, err := be.Judge(context.Background(), batch); err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	batch[0].Specs[0] = "mutated"
	value["msg"] = "mutated"

	if got := be.WarmCount(); got != 1 {
		t.Fatalf("WarmCount = %d, want 1", got)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("CallCount = %d, want 1", got)
	}
	if got := be.BatchCount(); got != 1 {
		t.Fatalf("BatchCount = %d, want 1", got)
	}
	recorded := be.Batches()
	if len(recorded) != 1 || len(recorded[0]) != 1 {
		t.Fatalf("Batches = %#v, want one judgement", recorded)
	}
	if got := recorded[0][0].Specs[0]; got != "keep" {
		t.Fatalf("recorded spec = %q, want keep", got)
	}
	recordedValue, ok := recorded[0][0].Value.(map[string]any)
	if !ok || recordedValue["msg"] != "keep" {
		t.Fatalf("recorded value = %#v, want original map", recorded[0][0].Value)
	}
}

func TestMockBackendDeterministicOutputs(t *testing.T) {
	be := &MockBackend{}
	batch := []Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"urgent"}, Value: "urgent billing issue"},
		{Op: "sem_classify", Return: semantics.ReturnString, Specs: []string{"billing", "other"}, Value: "urgent billing issue"},
		{Op: "sem_score", Return: semantics.ReturnNumber, Specs: []string{"urgent"}, Value: "urgent billing issue"},
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"billing"}, Value: "Billing!!!"},
	}
	first, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("first Judge returned error: %v", err)
	}
	second, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("second Judge returned error: %v", err)
	}
	if len(first) != len(batch) || len(second) != len(batch) {
		t.Fatalf("result lengths = %d/%d, want %d", len(first), len(second), len(batch))
	}
	want := []any{true, "billing", 1.0, "billing"}
	for i := range want {
		if first[i].Value != want[i] || second[i].Value != want[i] {
			t.Fatalf("result[%d] = %#v/%#v, want %#v", i, first[i].Value, second[i].Value, want[i])
		}
	}
}
