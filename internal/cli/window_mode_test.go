package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
)

const windowQuery = `select(sem_match(.msg; "urgent")) | .id`

func runWindowCommand(t *testing.T, stdin string, args ...string) (string, string, error) {
	t.Helper()
	cleanup, err := isolateCacheEnvForRun()
	if err != nil {
		t.Fatalf("isolate cache: %v", err)
	}
	defer cleanup()
	var stdout, stderr bytes.Buffer
	err = cli.Execute(context.Background(), cli.Options{
		Stdin: strings.NewReader(stdin), Stdout: &stdout, Stderr: &stderr,
	}, args)
	return stdout.String(), stderr.String(), err
}

func TestWindowBytesHelpAndConfigPrecedence(t *testing.T) {
	help, stderr, err := run("--help")
	if err != nil || stderr != "" {
		t.Fatalf("--help stdout=%q stderr=%q err=%v", help, stderr, err)
	}
	if !strings.Contains(help, "--window-bytes") || !strings.Contains(help, "maximum source bytes per supported three-phase semantic window") {
		t.Fatalf("help missing window budget: %q", help)
	}

	configPath := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(configPath, []byte("backend = \"mock\"\nwindow_bytes = 1024\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_WINDOW_BYTES", "2048")
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{name: "flag", args: []string{"--backend", "mock", "--window-bytes", "4096", "--stats", windowQuery}, want: "  window_bytes: 4096\n"},
		{name: "env", args: []string{"--backend", "mock", "--stats", windowQuery}, want: "  window_bytes: 2048\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stdout, stderr, err := runWindowCommand(t, `{"id":1,"msg":"urgent"}`+"\n", tc.args...)
			if err != nil {
				t.Fatalf("Execute: %v; stderr=%q", err, stderr)
			}
			if stdout != "1\n" {
				t.Fatalf("stdout = %q, want semantic output", stdout)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Fatalf("stats missing %q: %q", tc.want, stderr)
			}
		})
	}
	t.Setenv("AJQ_WINDOW_BYTES", "")
	stdout, stderr, err := runWindowCommand(t, `{"id":1,"msg":"urgent"}`+"\n", "--backend", "mock", "--stats", windowQuery)
	if err != nil || stdout != "1\n" || !strings.Contains(stderr, "  window_bytes: 1024\n") {
		t.Fatalf("file config stdout=%q stderr=%q err=%v", stdout, stderr, err)
	}
}

func TestWindowBytesValidationAndPureJQPolicy(t *testing.T) {
	for _, value := range []string{"0", "-1", "9223372036854775808"} {
		t.Run("flag_"+value, func(t *testing.T) {
			_, stderr, err := runWindowCommand(t, "null\n", "--window-bytes", value, ".")
			if err == nil || !strings.Contains(stderr, "--window-bytes") {
				t.Fatalf("invalid flag stderr=%q err=%v", stderr, err)
			}
		})
	}
	for _, value := range []string{"0", "-1", "invalid", "9223372036854775808"} {
		t.Run("env_semantic_"+value, func(t *testing.T) {
			t.Setenv("AJQ_WINDOW_BYTES", value)
			_, stderr, err := runWindowCommand(t, `{"msg":"urgent"}`+"\n", "--backend", "mock", windowQuery)
			if err == nil || !strings.Contains(stderr, "AJQ_WINDOW_BYTES") {
				t.Fatalf("invalid env stderr=%q err=%v", stderr, err)
			}
		})
	}

	configPath := filepath.Join(t.TempDir(), "invalid.toml")
	if err := os.WriteFile(configPath, []byte("window_bytes = 0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("file_semantic", func(t *testing.T) {
		t.Setenv("AJQ_CONFIG", configPath)
		_, stderr, err := runWindowCommand(t, `{"msg":"urgent"}`+"\n", "--backend", "mock", windowQuery)
		if err == nil || !strings.Contains(stderr, "window_bytes") {
			t.Fatalf("invalid config stderr=%q err=%v", stderr, err)
		}
	})
	t.Run("env_and_file_ignored_for_pure_jq", func(t *testing.T) {
		t.Setenv("AJQ_CONFIG", configPath)
		t.Setenv("AJQ_WINDOW_BYTES", "invalid")
		stdout, stderr, err := runWindowCommand(t, "1\n", ".")
		if err != nil || stderr != "" || stdout != "1\n" {
			t.Fatalf("pure jq stdout=%q stderr=%q err=%v", stdout, stderr, err)
		}
	})
}

func TestWindowModeStatsAndOutputParity(t *testing.T) {
	input := `{"id":1,"msg":"urgent"}` + "\n" + `{"id":2,"msg":"routine"}` + "\n"
	windowedOut, windowedStats, err := runWindowCommand(t, input, "--backend", "mock", "--window-bytes", "1024", "--stats", windowQuery)
	if err != nil {
		t.Fatalf("windowed Execute: %v; stats=%q", err, windowedStats)
	}
	plainOut, _, err := runWindowCommand(t, input, "--backend", "mock", windowQuery)
	if err != nil || windowedOut != plainOut {
		t.Fatalf("output parity windowed=%q plain=%q err=%v", windowedOut, plainOut, err)
	}
	for _, line := range []string{
		"  execution_mode: three-phase-windowed\n",
		"  window_bytes: 1024\n",
		"  window_count: 1\n",
		"  oversized_window_count: 0\n",
	} {
		if !strings.Contains(windowedStats, line) {
			t.Fatalf("windowed stats missing %q: %q", line, windowedStats)
		}
	}

	overInput := `{"id":1,"msg":"urgent` + strings.Repeat("x", 256) + `"}` + "\n"
	_, oversizedStats, err := runWindowCommand(t, overInput, "--backend", "mock", "--window-bytes", "64", "--stats", windowQuery)
	if err != nil {
		t.Fatalf("oversized Execute: %v; stats=%q", err, oversizedStats)
	}
	for _, line := range []string{"  execution_mode: three-phase-windowed\n", "  window_bytes: 64\n", "  window_count: 1\n", "  oversized_window_count: 1\n"} {
		if !strings.Contains(oversizedStats, line) {
			t.Fatalf("oversized stats missing %q: %q", line, oversizedStats)
		}
	}

	pureOut, pureStats, err := runWindowCommand(t, "1\n", "--stats", ".")
	if err != nil || pureOut != "1\n" {
		t.Fatalf("pure jq stdout=%q stats=%q err=%v", pureOut, pureStats, err)
	}
	for _, line := range []string{"  execution_mode: pure-jq\n", "  window_bytes: 0\n", "  window_count: 0\n", "  oversized_window_count: 0\n"} {
		if !strings.Contains(pureStats, line) {
			t.Fatalf("pure jq stats missing %q: %q", line, pureStats)
		}
	}
}
