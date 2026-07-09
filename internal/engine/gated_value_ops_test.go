package engine

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

func TestGatedUnboundedValueOpUsesInterleavedFallback(t *testing.T) {
	be := &backend.MockBackend{}
	var stdout bytes.Buffer
	_, err := Execute(context.Background(), strings.NewReader(`[{"id":1,"msg":"urgent"},{"id":2,"msg":"low"}]`), &stdout, Options{
		Query:     `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
		Backend:   be,
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if be.CallCount() == 0 {
		t.Fatalf("interleaved fallback did not call backend")
	}
	if stdout.String() == "" {
		t.Fatalf("stdout was empty; want fallback to produce jq output")
	}
}
