package schema

import (
	"encoding/json"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// TestBuildForEveryV1Op asserts each v1 semantic operator maps to its
// deterministic return-type contract and json_schema shape.
func TestBuildForEveryV1Op(t *testing.T) {
	cases := []struct {
		op         string
		ret        semantics.ReturnType
		enum       []string
		wantSchema map[string]any
	}{
		{"sem_match", semantics.ReturnBool, nil, map[string]any{"type": "boolean"}},
		{"sem_classify", semantics.ReturnString, []string{"billing", "other"}, map[string]any{"type": "string", "enum": []any{"billing", "other"}}},
		{"sem_extract", semantics.ReturnString, nil, map[string]any{"type": "string"}},
		{"sem_norm", semantics.ReturnString, nil, map[string]any{"type": "string"}},
		{"sem_redact", semantics.ReturnString, nil, map[string]any{"type": "string"}},
		{"sem_score", semantics.ReturnNumber, nil, map[string]any{"type": "number"}},
	}
	for _, tc := range cases {
		t.Run(tc.op, func(t *testing.T) {
			c, err := Build(tc.op, tc.ret, tc.enum)
			if err != nil {
				t.Fatalf("Build(%s) error = %v", tc.op, err)
			}
			if c.Type != tc.ret {
				t.Fatalf("Type = %q, want %q", c.Type, tc.ret)
			}
			if got := c.JSONSchema(); !reflect.DeepEqual(got, tc.wantSchema) {
				t.Fatalf("JSONSchema = %#v, want %#v", got, tc.wantSchema)
			}
		})
	}
}

// TestBuildOwnsEnumCopy proves mutating the caller slice cannot alter the
// constraint's enum.
func TestBuildOwnsEnumCopy(t *testing.T) {
	labels := []string{"a", "b"}
	c, err := Build("sem_classify", semantics.ReturnString, labels)
	if err != nil {
		t.Fatalf("Build error = %v", err)
	}
	labels[0] = "mutated"
	if c.Enum[0] != "a" {
		t.Fatalf("Enum[0] = %q, want a (constraint must own a copy)", c.Enum[0])
	}
}

func TestBuildRejectsInvalidCombinations(t *testing.T) {
	cases := []struct {
		name    string
		op      string
		ret     semantics.ReturnType
		enum    []string
		wantSub string
	}{
		{"empty return", "sem_match", "", nil, "missing return type"},
		{"unknown return", "sem_match", semantics.ReturnType("weird"), nil, "unknown return type"},
		{"enum on bool", "sem_match", semantics.ReturnBool, []string{"x"}, "only valid for string"},
		{"enum on number", "sem_score", semantics.ReturnNumber, []string{"x"}, "only valid for string"},
		{"empty label", "sem_classify", semantics.ReturnString, []string{"ok", "  "}, "label 2 is empty"},
		{"duplicate label", "sem_classify", semantics.ReturnString, []string{"dup", "dup"}, "duplicate label"},
		{"classify without labels", "sem_classify", semantics.ReturnString, nil, "requires at least one label"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Build(tc.op, tc.ret, tc.enum)
			if err == nil || !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("Build error = %v, want substring %q", err, tc.wantSub)
			}
		})
	}
}

func TestForJudgementResolvesEnumFromSpecs(t *testing.T) {
	j := backend.Judgement{
		Op:     "sem_classify",
		Return: semantics.ReturnString,
		Schema: backend.ResultSchema{Type: semantics.ReturnString},
		Specs:  []string{"billing", "other"},
	}
	c, err := ForJudgement(j)
	if err != nil {
		t.Fatalf("ForJudgement error = %v", err)
	}
	if !reflect.DeepEqual(c.Enum, []string{"billing", "other"}) {
		t.Fatalf("Enum = %#v, want billing/other from specs", c.Enum)
	}
}

func TestForJudgementSchemaReturnMismatch(t *testing.T) {
	j := backend.Judgement{
		Op:     "sem_match",
		Return: semantics.ReturnBool,
		Schema: backend.ResultSchema{Type: semantics.ReturnString},
	}
	_, err := ForJudgement(j)
	if err == nil || !strings.Contains(err.Error(), "does not match return type") {
		t.Fatalf("ForJudgement error = %v, want schema/return mismatch", err)
	}
}

func TestValidateHappyPaths(t *testing.T) {
	boolC, _ := Build("sem_match", semantics.ReturnBool, nil)
	if err := boolC.Validate(true); err != nil {
		t.Fatalf("bool Validate error = %v", err)
	}
	strC, _ := Build("sem_norm", semantics.ReturnString, nil)
	if err := strC.Validate("anything"); err != nil {
		t.Fatalf("string Validate error = %v", err)
	}
	enumC, _ := Build("sem_classify", semantics.ReturnString, []string{"a", "b"})
	if err := enumC.Validate("b"); err != nil {
		t.Fatalf("enum Validate error = %v", err)
	}
	numC, _ := Build("sem_score", semantics.ReturnNumber, nil)
	for _, v := range []any{int(1), int64(2), float64(0.5), json.Number("0.9"), big.NewInt(3)} {
		if err := numC.Validate(v); err != nil {
			t.Fatalf("number Validate(%T) error = %v", v, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	boolC, _ := Build("sem_match", semantics.ReturnBool, nil)
	if err := boolC.Validate("not-a-bool"); err == nil || !strings.Contains(err.Error(), "want bool result") {
		t.Fatalf("bool Validate error = %v, want bool type error", err)
	}
	numC, _ := Build("sem_score", semantics.ReturnNumber, nil)
	if err := numC.Validate("0.5"); err == nil || !strings.Contains(err.Error(), "want number result") {
		t.Fatalf("number Validate error = %v, want number type error", err)
	}
	strC, _ := Build("sem_norm", semantics.ReturnString, nil)
	if err := strC.Validate(true); err == nil || !strings.Contains(err.Error(), "want string result") {
		t.Fatalf("string Validate error = %v, want string type error", err)
	}
	enumC, _ := Build("sem_classify", semantics.ReturnString, []string{"a", "b"})
	err := enumC.Validate("c")
	if err == nil || !strings.Contains(err.Error(), "not one of labels") {
		t.Fatalf("enum Validate error = %v, want enum membership error", err)
	}
	if !strings.Contains(err.Error(), "sem_classify") {
		t.Fatalf("enum Validate error = %v, want op name in message", err)
	}
}
