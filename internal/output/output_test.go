package output_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/ricardocabral/ajq/internal/output"
)

func TestMarshalPrettyByDefault(t *testing.T) {
	got, err := output.Marshal(map[string]any{"a": json.Number("1"), "b": []any{true}}, output.Options{})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	want := "{\n  \"a\": 1,\n  \"b\": [\n    true\n  ]\n}"
	if string(got) != want {
		t.Fatalf("pretty JSON = %q, want %q", got, want)
	}
}

func TestMarshalCompact(t *testing.T) {
	got, err := output.Marshal(map[string]any{"a": json.Number("1")}, output.Options{Compact: true})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(got) != `{"a":1}` {
		t.Fatalf("compact JSON = %q", got)
	}
}

func TestRawOutputOnlyUnquotesStrings(t *testing.T) {
	got, err := output.Marshal("hello", output.Options{Raw: true})
	if err != nil {
		t.Fatalf("Marshal string returned error: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("raw string = %q", got)
	}

	got, err = output.Marshal(map[string]any{"a": "b"}, output.Options{Raw: true, Compact: true})
	if err != nil {
		t.Fatalf("Marshal object returned error: %v", err)
	}
	if string(got) != `{"a":"b"}` {
		t.Fatalf("raw object = %q", got)
	}
}

func TestMarshalUsesJQCompatibleEscaping(t *testing.T) {
	got, err := output.Marshal("<tag>&\b\f", output.Options{Compact: true})
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	if string(got) != `"<tag>&\b\f"` {
		t.Fatalf("jq-compatible JSON = %q", got)
	}
}

func TestWriteValueAddsNewline(t *testing.T) {
	var buf bytes.Buffer
	if err := output.WriteValue(&buf, "hello", output.Options{Raw: true}); err != nil {
		t.Fatalf("WriteValue returned error: %v", err)
	}
	if buf.String() != "hello\n" {
		t.Fatalf("WriteValue = %q", buf.String())
	}
}
