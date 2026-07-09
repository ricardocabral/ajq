package cache

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
)

func withModel(j backend.Judgement, modelID string) backend.Judgement {
	j.ModelID = modelID
	return j
}

func TestCanonicalKeyObjectOrderAndNestedValues(t *testing.T) {
	left := backend.Judgement{
		Op:    "sem_match",
		Specs: []string{"keep"},
		Value: map[string]any{
			"b": []any{json.Number("1.0"), nil, map[string]any{"z": true, "a": "x"}},
			"a": "same",
		},
	}
	right := backend.Judgement{
		Op:    "sem_match",
		Specs: []string{"keep"},
		Value: map[string]any{
			"a": "same",
			"b": []any{1, nil, map[string]any{"a": "x", "z": true}},
		},
	}
	leftKey, err := KeyForJudgement(withModel(left, "model-a"))
	if err != nil {
		t.Fatalf("left key: %v", err)
	}
	rightKey, err := KeyForJudgement(withModel(right, "model-a"))
	if err != nil {
		t.Fatalf("right key: %v", err)
	}
	if leftKey != rightKey {
		t.Fatalf("keys differ for canonically equal values:\n%s\n%s", leftKey, rightKey)
	}
}

func TestCanonicalKeyPreservesScalarTypesAndModelID(t *testing.T) {
	stringKey, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"x"}, ModelID: "model-a", Value: "1"})
	if err != nil {
		t.Fatalf("string key: %v", err)
	}
	numberKey, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"x"}, ModelID: "model-a", Value: 1})
	if err != nil {
		t.Fatalf("number key: %v", err)
	}
	differentModelKey, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"x"}, ModelID: "model-b", Value: 1})
	if err != nil {
		t.Fatalf("different model key: %v", err)
	}
	if stringKey == numberKey {
		t.Fatalf("string and number values collided: %s", stringKey)
	}
	if numberKey == differentModelKey {
		t.Fatalf("different model ids collided: %s", numberKey)
	}
}

func TestCanonicalKeyKeepsLargeJSONNumbersDistinctExactly(t *testing.T) {
	left, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"x"}, ModelID: "model", Value: json.Number("9007199254740992.0")})
	if err != nil {
		t.Fatalf("left key: %v", err)
	}
	right, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"x"}, ModelID: "model", Value: json.Number("9007199254740993.0")})
	if err != nil {
		t.Fatalf("right key: %v", err)
	}
	if left == right {
		t.Fatalf("large decimal json.Number values collided: %s", left)
	}
	normalized, err := CanonicalValue(json.Number("1.2300e+3"))
	if err != nil {
		t.Fatalf("canonical value: %v", err)
	}
	if string(normalized) != "123e1" {
		t.Fatalf("normalized json.Number = %s, want 123e1", normalized)
	}
}

func TestCanonicalKeyDoesNotExpandHugeJSONNumberExponents(t *testing.T) {
	value, err := CanonicalValue(json.Number("1e1000000000"))
	if err != nil {
		t.Fatalf("canonical value: %v", err)
	}
	if string(value) != "1e1000000000" {
		t.Fatalf("canonical huge positive exponent = %s", value)
	}
	value, err = CanonicalValue(json.Number("1e-1000000000"))
	if err != nil {
		t.Fatalf("canonical negative exponent value: %v", err)
	}
	if string(value) != "1e-1000000000" {
		t.Fatalf("canonical huge negative exponent = %s", value)
	}
}

func TestCanonicalKeyDoesNotOverflowExponentNormalization(t *testing.T) {
	cases := []struct {
		left  json.Number
		right json.Number
	}{
		{left: json.Number("10e9223372036854775807"), right: json.Number("1e-9223372036854775808")},
		{left: json.Number("1.1e-9223372036854775808"), right: json.Number("11e9223372036854775807")},
	}
	for _, tc := range cases {
		left, err := CanonicalValue(tc.left)
		if err != nil {
			t.Fatalf("CanonicalValue(%s): %v", tc.left, err)
		}
		right, err := CanonicalValue(tc.right)
		if err != nil {
			t.Fatalf("CanonicalValue(%s): %v", tc.right, err)
		}
		if string(left) == string(right) {
			t.Fatalf("%s and %s collided as %s", tc.left, tc.right, left)
		}
	}
}

func TestCanonicalKeyUsesUnambiguousTupleEncoding(t *testing.T) {
	left, err := KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"a\x00b"}, ModelID: "model", Value: "c"})
	if err != nil {
		t.Fatalf("left key: %v", err)
	}
	right, err := KeyForJudgement(backend.Judgement{Op: "sem_match\x00a", Specs: []string{"b"}, ModelID: "model", Value: "c"})
	if err != nil {
		t.Fatalf("right key: %v", err)
	}
	if left == right || !strings.HasPrefix(string(left), "{") || !strings.HasPrefix(string(right), "{") {
		t.Fatalf("tuple encoding is ambiguous: %q vs %q", left, right)
	}
}

func TestCanonicalValueRejectsUnsupportedValues(t *testing.T) {
	_, err := CanonicalValue(map[string]any{"bad": make(chan int)})
	if err == nil || !strings.Contains(err.Error(), "unsupported cache key value type") {
		t.Fatalf("CanonicalValue error = %v, want unsupported type", err)
	}
}
