package jq_test

import (
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/jq"
)

func TestProgramRunsPureJQ(t *testing.T) {
	program, err := jq.Compile(`.users[] | select(.active) | .name`)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}

	var got []string
	result, err := program.Run(map[string]any{
		"users": []any{
			map[string]any{"name": "Ada", "active": true},
			map[string]any{"name": "Grace", "active": false},
		},
	}, func(value any) error {
		got = append(got, value.(string))
		return nil
	})
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if !result.Emitted || result.Last != "Ada" {
		t.Fatalf("result = %#v", result)
	}
	if strings.Join(got, ",") != "Ada" {
		t.Fatalf("emitted = %#v", got)
	}
}

func TestCompileReportsInvalidQuery(t *testing.T) {
	_, err := jq.Compile(`.foo[`)
	if err == nil {
		t.Fatal("expected invalid query to fail")
	}
}

func TestRunReturnsIteratorRuntimeErrors(t *testing.T) {
	program, err := jq.Compile(`.[10]`)
	if err != nil {
		t.Fatalf("Compile returned error: %v", err)
	}
	_, err = program.Run(1, nil)
	if err == nil {
		t.Fatal("expected runtime error")
	}
}
