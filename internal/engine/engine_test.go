package engine_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/engine"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/output"
)

func TestExecuteRunsNDJSONFramesIndependently(t *testing.T) {
	var stdout bytes.Buffer
	result, err := engine.Execute(context.Background(), strings.NewReader("{\"n\":1}\n{\"n\":2}\n"), &stdout, engine.Options{
		Query:     `.n, (.n + 10)`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if stdout.String() != "1\n11\n2\n12\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !result.Emitted || result.Last != 12 {
		t.Fatalf("result = %#v", result)
	}
}

func TestExecuteSupportsRawInputAndRawOutput(t *testing.T) {
	var stdout bytes.Buffer
	_, err := engine.Execute(context.Background(), strings.NewReader("alpha\nbeta\n"), &stdout, engine.Options{
		Query:     `. + "!"`,
		InputMode: input.ModeRaw,
		Output:    output.Options{Raw: true},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if stdout.String() != "alpha!\nbeta!\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestExecuteEmptyOutput(t *testing.T) {
	var stdout bytes.Buffer
	result, err := engine.Execute(context.Background(), strings.NewReader("{\"ok\":false}\n"), &stdout, engine.Options{
		Query:     `select(.ok)`,
		InputMode: input.ModeAuto,
		Output:    output.Options{Compact: true},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if stdout.String() != "" || result.Emitted {
		t.Fatalf("stdout=%q result=%#v", stdout.String(), result)
	}
	if code := engine.ExitStatusCode(result); code != 4 {
		t.Fatalf("ExitStatusCode = %d, want 4", code)
	}
}

func TestExitStatusCode(t *testing.T) {
	cases := []struct {
		name   string
		result engine.Result
		want   int
	}{
		{name: "empty", result: engine.Result{}, want: 4},
		{name: "false", result: engine.Result{Emitted: true, Last: false}, want: 1},
		{name: "null", result: engine.Result{Emitted: true, Last: nil}, want: 1},
		{name: "zero is truthy", result: engine.Result{Emitted: true, Last: 0}, want: 0},
		{name: "true", result: engine.Result{Emitted: true, Last: true}, want: 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := engine.ExitStatusCode(tc.result); got != tc.want {
				t.Fatalf("ExitStatusCode() = %d, want %d", got, tc.want)
			}
		})
	}
}
