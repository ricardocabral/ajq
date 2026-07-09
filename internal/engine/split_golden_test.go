package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/testutil"
)

type splitGoldenCorpus struct {
	Fixtures []splitGoldenFixture `json:"fixtures"`
}

type splitGoldenFixture struct {
	Name          string   `json:"name"`
	Query         string   `json:"query"`
	Stdin         string   `json:"stdin"`
	WantStdout    string   `json:"want_stdout"`
	WantBatchSize int      `json:"want_batch_size"`
	WantOps       []string `json:"want_ops"`
}

func TestSplitGoldenMockBackendFixtures(t *testing.T) {
	for _, fixture := range loadSplitGoldenCorpus(t).Fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			be := &backend.MockBackend{}
			var stdout bytes.Buffer
			_, err := Execute(context.Background(), strings.NewReader(fixture.Stdin), &stdout, Options{
				Query:     fixture.Query,
				InputMode: input.ModeAuto,
				Output:    output.Options{Compact: true},
				Backend:   be,
			})
			if err != nil {
				t.Fatalf("Execute returned error: %v", err)
			}
			if got := stdout.String(); got != fixture.WantStdout {
				t.Fatalf("stdout = %q, want %q", got, fixture.WantStdout)
			}
			assertSplitGoldenBackendRecords(t, be, fixture)
			assertSplitGoldenPhaseCalls(t, fixture)
		})
	}
}

func TestSplitGoldenRejectsGatedValueOps(t *testing.T) {
	queries := []string{
		`.[] | select(sem_score(.msg; "urgent") > 0.8) | .id`,
		`.[] | select(sem_norm(.company; "acme") == "acme") | .id`,
		`if sem_score(.msg; "urgent") then .id else empty end`,
	}
	for _, query := range queries {
		query := query
		t.Run(query, func(t *testing.T) {
			_, err := compileThreePhase(query, &backend.MockBackend{})
			var planErr *PlanError
			if !errors.As(err, &planErr) {
				t.Fatalf("compileThreePhase error = %T %[1]v, want PlanError", err)
			}
			if len(planErr.Diagnostics) == 0 || !strings.Contains(planErr.Diagnostics[0].Message, "unsupported") {
				t.Fatalf("diagnostics = %#v, want unsupported value-op diagnostic", planErr.Diagnostics)
			}
		})
	}
}

func assertSplitGoldenBackendRecords(t *testing.T, be *backend.MockBackend, fixture splitGoldenFixture) {
	t.Helper()
	if got := be.WarmCount(); got != 0 {
		t.Fatalf("WarmCount = %d, want 0 (no daemon/model warmup)", got)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("CallCount = %d, want one resolve-time backend call", got)
	}
	if got := be.BatchCount(); got != 1 {
		t.Fatalf("BatchCount = %d, want one resolve-time backend batch", got)
	}
	batches := be.Batches()
	if len(batches) != 1 || len(batches[0]) != fixture.WantBatchSize {
		t.Fatalf("Batches = %#v, want one batch of %d", batches, fixture.WantBatchSize)
	}
	inputs := be.Inputs()
	if len(inputs) != len(fixture.WantOps) {
		t.Fatalf("Inputs = %#v, want %d ops", inputs, len(fixture.WantOps))
	}
	for i, wantOp := range fixture.WantOps {
		if inputs[i].Op != wantOp {
			t.Fatalf("input op[%d] = %q, want %q; inputs=%#v", i, inputs[i].Op, wantOp, inputs)
		}
	}
}

func assertSplitGoldenPhaseCalls(t *testing.T, fixture splitGoldenFixture) {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(fixture.Stdin), &value); err != nil {
		t.Fatalf("decode fixture stdin: %v", err)
	}
	be := &backend.MockBackend{}
	program, err := compileThreePhase(fixture.Query, be)
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest(value); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := be.CallCount(); got != 0 {
		t.Fatalf("harvest called backend %d times", got)
	}
	if err := program.resolve(context.Background()); err != nil {
		t.Fatalf("resolve returned error: %v", err)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("resolve CallCount = %d, want 1", got)
	}
	if _, err := program.execute(value, func(any) error { return nil }); err != nil {
		t.Fatalf("execute returned error: %v", err)
	}
	if got := be.CallCount(); got != 1 {
		t.Fatalf("execute called backend; CallCount = %d", got)
	}
}

func loadSplitGoldenCorpus(t *testing.T) splitGoldenCorpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootForSplitGolden(t), "testdata", "split", "fixtures.json"))
	if err != nil {
		t.Fatalf("read split golden corpus: %v", err)
	}
	var corpus splitGoldenCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode split golden corpus: %v", err)
	}
	return corpus
}

func repoRootForSplitGolden(t *testing.T) string {
	t.Helper()
	return testutil.RepoRoot(t)
}
