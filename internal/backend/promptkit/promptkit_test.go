package promptkit

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

func TestBuildPrompt(t *testing.T) {
	tests := []struct {
		name       string
		judgement  backend.Judgement
		constraint schema.Constraint
		want       string
	}{
		{
			name:       "match",
			judgement:  backend.Judgement{Op: "sem_match", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnBool},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_match\nReturn type: bool\nValue: input\n" +
				"Decide whether the value satisfies the spec. Answer true or false.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "classify with specs and enum",
			judgement:  backend.Judgement{Op: "sem_classify", Specs: []string{"urgent", "routine"}, Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnString, Enum: []string{"urgent", "routine"}},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_classify\nReturn type: string\nSpecs: urgent | routine\nAllowed labels: urgent, routine\nValue: input\n" +
				"Choose exactly one allowed label that best fits the value.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "score",
			judgement:  backend.Judgement{Op: "sem_score", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnNumber},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_score\nReturn type: number\nValue: input\n" +
				"Rate how strongly the value matches the spec as a number between 0 and 1.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "norm",
			judgement:  backend.Judgement{Op: "sem_norm", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnString},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_norm\nReturn type: string\nValue: input\n" +
				"Return a normalized string form of the value.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "extract",
			judgement:  backend.Judgement{Op: "sem_extract", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnString},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_extract\nReturn type: string\nValue: input\n" +
				"Extract the requested information from the value as a string.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "redact",
			judgement:  backend.Judgement{Op: "sem_redact", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnString},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: sem_redact\nReturn type: string\nValue: input\n" +
				"Return the value with the requested information redacted as a string.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "default bool",
			judgement:  backend.Judgement{Op: "custom", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnBool},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: custom\nReturn type: bool\nValue: input\n" +
				"Answer true or false.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "default number",
			judgement:  backend.Judgement{Op: "custom", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnNumber},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: custom\nReturn type: number\nValue: input\n" +
				"Answer with a number.\nRespond with only the JSON result and nothing else.",
		},
		{
			name:       "default string",
			judgement:  backend.Judgement{Op: "custom", Value: "input"},
			constraint: schema.Constraint{Type: semantics.ReturnString},
			want: "You are a deterministic semantic judgement engine for the ajq query tool.\nOperation: custom\nReturn type: string\nValue: input\n" +
				"Answer with a string.\nRespond with only the JSON result and nothing else.",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := BuildPrompt(tc.judgement, tc.constraint); got != tc.want {
				t.Errorf("BuildPrompt() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestOpInstruction(t *testing.T) {
	tests := []struct {
		op   string
		rt   semantics.ReturnType
		want string
	}{
		{"sem_match", semantics.ReturnBool, "Decide whether the value satisfies the spec. Answer true or false."},
		{"sem_classify", semantics.ReturnString, "Choose exactly one allowed label that best fits the value."},
		{"sem_score", semantics.ReturnNumber, "Rate how strongly the value matches the spec as a number between 0 and 1."},
		{"sem_norm", semantics.ReturnString, "Return a normalized string form of the value."},
		{"sem_extract", semantics.ReturnString, "Extract the requested information from the value as a string."},
		{"sem_redact", semantics.ReturnString, "Return the value with the requested information redacted as a string."},
		{"unknown", semantics.ReturnBool, "Answer true or false."},
		{"unknown", semantics.ReturnNumber, "Answer with a number."},
		{"unknown", semantics.ReturnString, "Answer with a string."},
		{"unknown", "unknown", "Answer with a string."},
	}
	for _, tc := range tests {
		t.Run(tc.op+string(tc.rt), func(t *testing.T) {
			if got := OpInstruction(tc.op, tc.rt); got != tc.want {
				t.Errorf("OpInstruction(%q, %q) = %q, want %q", tc.op, tc.rt, got, tc.want)
			}
		})
	}
}

func TestCoerceResult(t *testing.T) {
	tests := []struct {
		name       string
		constraint schema.Constraint
		content    string
		want       any
		wantErr    bool
	}{
		{"valid bool", schema.Constraint{Op: "sem_match", Type: semantics.ReturnBool}, " true ", true, false},
		{"valid number", schema.Constraint{Op: "sem_score", Type: semantics.ReturnNumber}, "1.25", 1.25, false},
		{"valid quoted string", schema.Constraint{Op: "sem_norm", Type: semantics.ReturnString}, `"normal"`, "normal", false},
		{"valid enum string", schema.Constraint{Op: "sem_classify", Type: semantics.ReturnString, Enum: []string{"yes"}}, `"yes"`, "yes", false},
		{"empty content", schema.Constraint{Op: "sem_match", Type: semantics.ReturnBool}, " \n\t", nil, true},
		{"string fallback after invalid JSON", schema.Constraint{Op: "sem_norm", Type: semantics.ReturnString}, " raw text ", "raw text", false},
		{"invalid JSON bool", schema.Constraint{Op: "sem_match", Type: semantics.ReturnBool}, "true trailing", nil, true},
		{"invalid JSON number", schema.Constraint{Op: "sem_score", Type: semantics.ReturnNumber}, "1.2 trailing", nil, true},
		{"wrong bool type", schema.Constraint{Op: "sem_match", Type: semantics.ReturnBool}, `"true"`, nil, true},
		{"wrong number type", schema.Constraint{Op: "sem_score", Type: semantics.ReturnNumber}, `"1"`, nil, true},
		{"wrong string type", schema.Constraint{Op: "sem_norm", Type: semantics.ReturnString}, "true", nil, true},
		{"enum violation", schema.Constraint{Op: "sem_classify", Type: semantics.ReturnString, Enum: []string{"yes"}}, `"no"`, nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := CoerceResult(tc.constraint, tc.content)
			if (err != nil) != tc.wantErr {
				t.Fatalf("CoerceResult() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && !reflect.DeepEqual(got, tc.want) {
				t.Errorf("CoerceResult() = %#v, want %#v", got, tc.want)
			}
		})
	}
}

func TestCanonicalValueString(t *testing.T) {
	unmarshalable := struct{ Channel chan int }{}
	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"string", "plain text", "plain text"},
		{"composite", map[string]any{"key": []int{1, 2}}, `{"key":[1,2]}`},
		{"marshal failure fallback", unmarshalable, fmt.Sprint(unmarshalable)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := CanonicalValueString(tc.value); got != tc.want {
				t.Errorf("CanonicalValueString() = %q, want %q", got, tc.want)
			}
		})
	}
}
