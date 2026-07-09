package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSetModelCreatesAndUpdatesConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ajq", "config.toml")
	gotPath, err := SetModel("qwen2.5-3b", WriteOptions{Getenv: func(key string) string {
		if key == "AJQ_CONFIG" {
			return path
		}
		return ""
	}})
	if err != nil {
		t.Fatalf("SetModel create: %v", err)
	}
	if gotPath != path {
		t.Fatalf("path = %q, want %q", gotPath, path)
	}
	data, err := os.ReadFile(path) //nolint:gosec // test reads a temp config path created by the test.
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `model = "qwen2.5-3b"`) {
		t.Fatalf("created config missing model: %q", string(data))
	}
}

func TestSetModelPreservesUnrelatedKeysAndRejectsCredentials(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("backend = \"local\"\nmax_calls = 3\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	getenv := func(key string) string {
		if key == "AJQ_CONFIG" {
			return path
		}
		return ""
	}
	if _, err := SetModel("qwen3-4b", WriteOptions{Getenv: getenv}); err != nil {
		t.Fatalf("SetModel update: %v", err)
	}
	data, err := os.ReadFile(path) //nolint:gosec // test reads a temp config path created by the test.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{`backend = "local"`, `max_calls = 3`, `model = "qwen3-4b"`} {
		if !strings.Contains(text, want) {
			t.Fatalf("config %q missing %q", text, want)
		}
	}

	if err := os.WriteFile(path, []byte("api_key = \"secret\"\nmodel = \"qwen2.5-1.5b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := SetModel("qwen2.5-3b", WriteOptions{Getenv: getenv}); err == nil || !strings.Contains(err.Error(), "API keys are env-only") {
		t.Fatalf("credential rejection error = %v", err)
	}
}
