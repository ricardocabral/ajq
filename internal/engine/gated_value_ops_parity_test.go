package engine

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/testutil"
)

type gatedValueCorpus struct {
	Fixtures []gatedValueFixture `json:"fixtures"`
}

type gatedValueFixture struct {
	Name  string `json:"name"`
	Query string `json:"query"`
	Input any    `json:"input"`
}

func TestGatedValueOpsForcedValueCacheMissIsLoud(t *testing.T) {
	program, err := compileThreePhase(`.[] | {label: sem_classify(.msg; "urgent"; "normal")}`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	_, err = program.execute([]any{map[string]any{"msg": "urgent"}}, nil)
	if err == nil || !stringsContains(err.Error(), "cache miss") || !stringsContains(err.Error(), "sem_classify") {
		t.Fatalf("execute error = %v, want sem_classify cache miss", err)
	}
}

func TestGatedValueOpsEnumSafetyHarvestsDownstream(t *testing.T) {
	program, err := compileThreePhase(`.[] | select(sem_classify(.msg; "urgent"; "normal") == "urgent") | sem_match(.downstream; "needed")`, &recordingBackend{})
	if err != nil {
		t.Fatalf("compileThreePhase returned error: %v", err)
	}
	if err := program.harvest([]any{map[string]any{"msg": "normal", "downstream": "needed"}}); err != nil {
		t.Fatalf("harvest returned error: %v", err)
	}
	if got := len(program.runtime.collected); got != 2 {
		t.Fatalf("harvest collected %d judgements, want enum gate plus downstream", got)
	}
}

func TestGatedValueOpsParityWithInterleavedReference(t *testing.T) {
	for _, fixture := range loadGatedValueCorpus(t).Fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			got := runPlannedGatedValueQuery(t, fixture.Query, &recordingBackend{}, fixture.Input)
			want := referenceOutputs(t, fixture.Query, &recordingBackend{}, fixture.Input)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("planned output = %#v, reference = %#v", got, want)
			}
		})
	}
}

func runPlannedGatedValueQuery(t *testing.T, query string, be backend.Backend, input any) []any {
	t.Helper()
	semanticPlan, diagnostics := plan.Build(query)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == plan.SeverityError {
			t.Fatalf("plan diagnostic: %s", diagnostic.Message)
		}
	}
	if semanticPlan.RequiresInterleaved {
		program, err := compileInterleaved(context.Background(), query, be, "", nil)
		if err != nil {
			t.Fatalf("compileInterleaved returned error: %v", err)
		}
		var output []any
		if _, err := program.Run(input, func(value any) error { output = append(output, value); return nil }); err != nil {
			t.Fatalf("interleaved run returned error: %v", err)
		}
		return output
	}
	return splitOutputs(t, query, be, input)
}

func loadGatedValueCorpus(t *testing.T) gatedValueCorpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootForGatedValueCorpus(t), "testdata", "gated", "value_ops_corpus.json"))
	if err != nil {
		t.Fatalf("read gated value corpus: %v", err)
	}
	var corpus gatedValueCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode gated value corpus: %v", err)
	}
	return corpus
}

func repoRootForGatedValueCorpus(t *testing.T) string {
	t.Helper()
	return testutil.RepoRoot(t)
}

func stringsContains(s, substr string) bool { return strings.Contains(s, substr) }
