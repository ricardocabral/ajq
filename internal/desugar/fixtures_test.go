package desugar_test

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/desugar"
	"github.com/ricardocabral/ajq/internal/jq"
)

type hazardFixture struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Want    string `json:"want"`
	Compile bool   `json:"compile"`
}

func TestDesugarHazardFixtures(t *testing.T) {
	data, err := os.ReadFile("../../testdata/desugar/hazards.json")
	if err != nil {
		t.Fatalf("read hazards fixture: %v", err)
	}
	var fixtures []hazardFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatalf("decode hazards fixture: %v", err)
	}
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			got, err := desugar.Rewrite(fixture.Source)
			if err != nil {
				t.Fatalf("Rewrite returned error: %v", err)
			}
			if got != fixture.Want {
				t.Fatalf("Rewrite() = %q, want %q", got, fixture.Want)
			}
			if fixture.Compile {
				mustParse(t, got)
			}
		})
	}
}

func TestDesugarDifferentialSkeleton(t *testing.T) {
	infix := `.[] | select(.msg =~ "keep") | {id: .id}`
	rewritten, err := desugar.Rewrite(infix)
	if err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	semanticProgram, err := jq.CompileWithOptions(rewritten, gojq.WithFunction("sem_match", 1, 2, deterministicSemMatch))
	if err != nil {
		t.Fatalf("compile desugared query: %v", err)
	}
	referenceProgram, err := jq.Compile(`.[] | select(.msg == "keep") | {id: .id}`)
	if err != nil {
		t.Fatalf("compile reference query: %v", err)
	}
	input := []any{
		map[string]any{"id": 1, "msg": "keep"},
		map[string]any{"id": 2, "msg": "drop"},
		map[string]any{"id": 3, "msg": "keep"},
	}
	got := collectProgram(t, semanticProgram, input)
	want := collectProgram(t, referenceProgram, input)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("desugared outputs = %#v, want %#v", got, want)
	}
}

func deterministicSemMatch(input any, args []any) any {
	value := input
	specIndex := 0
	if len(args) == 2 {
		value = args[0]
		specIndex = 1
	}
	if specIndex >= len(args) {
		return false
	}
	spec, ok := args[specIndex].(string)
	return ok && value == spec
}

func collectProgram(t *testing.T, program *jq.Program, input any) []any {
	t.Helper()
	var values []any
	_, err := program.Run(input, func(value any) error {
		values = append(values, value)
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	return values
}
