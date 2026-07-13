package cli

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/anthropicbk"
	"github.com/ricardocabral/ajq/internal/backend/oai"
	"github.com/ricardocabral/ajq/internal/backend/ollamabk"
	"github.com/ricardocabral/ajq/internal/config"
)

func TestBackendConcurrencyHelpAndPrecedence(t *testing.T) {
	cmd := NewRootCommand(Options{})
	flag := cmd.Flags().Lookup("backend-concurrency")
	if flag == nil {
		t.Fatal("--backend-concurrency flag not registered")
	}
	for _, want := range []string{"in-flight semantic requests", "default 1", "maximum 2", "4 for Ollama"} {
		if !strings.Contains(flag.Usage, want) {
			t.Fatalf("flag help %q missing %q", flag.Usage, want)
		}
	}

	for _, tc := range []struct {
		name    string
		file    string
		env     string
		args    []string
		want    int
		maxCall int
	}{
		{name: "provider default", file: "backend = \"anthropic\"\n", want: 1, maxCall: 100},
		{name: "TOML", file: "backend = \"anthropic\"\nbackend_concurrency = 2\nmax_calls = 7\n", want: 2, maxCall: 7},
		{name: "environment overrides TOML", file: "backend = \"anthropic\"\nbackend_concurrency = 2\n", env: "1", want: 1, maxCall: 100},
		{name: "flag overrides environment", file: "backend = \"anthropic\"\nbackend_concurrency = 1\n", env: "1", args: []string{"--backend-concurrency", "2", "--max-calls", "9"}, want: 2, maxCall: 9},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigEnv(t)
			t.Setenv("AJQ_CONFIG", writeTempConfig(t, tc.file))
			t.Setenv("AJQ_BACKEND_CONCURRENCY", tc.env)
			var got config.Settings
			withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
				got = settings
				return &recordingSemanticBackend{}, nil
			})
			args := append([]string{"--cloud"}, tc.args...)
			args = append(args, `.msg =~ "keep"`)
			_, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, args...)
			if err != nil {
				t.Fatalf("Execute returned error: %v; stderr=%q", err, stderr)
			}
			if got.BackendConcurrency != tc.want || got.MaxCalls != tc.maxCall {
				t.Fatalf("settings=%+v, want concurrency=%d max_calls=%d", got, tc.want, tc.maxCall)
			}
		})
	}
}

func TestBackendConcurrencyValidationAndBackendSwitching(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  string
		args []string
		want string
	}{
		{name: "flag zero", args: []string{"--cloud", "--backend-concurrency", "0"}, want: "--backend-concurrency must be positive"},
		{name: "environment invalid", env: "0", args: []string{"--cloud"}, want: "AJQ_BACKEND_CONCURRENCY must be positive"},
		{name: "TOML invalid", file: "backend = \"anthropic\"\nbackend_concurrency = 0\n", args: []string{"--cloud"}, want: "config backend_concurrency must be positive"},
		{name: "Anthropic maximum", args: []string{"--cloud", "--backend-concurrency", "3"}, want: "anthropic backend concurrency 3 exceeds maximum 2"},
		{name: "OpenAI maximum", args: []string{"--backend", "openai", "--model", "test", "--backend-concurrency", "3"}, want: "openai backend concurrency 3 exceeds maximum 2"},
		{name: "Ollama maximum", args: []string{"--backend", "ollama", "--model", "llama3.2", "--backend-concurrency", "5"}, want: "ollama backend concurrency 5 exceeds maximum 4"},
		{name: "switch applies selected provider maximum", file: "backend = \"ollama\"\nmodel = \"llama3.2\"\nbackend_concurrency = 4\n", args: []string{"--backend", "openai", "--model", "test"}, want: "openai backend concurrency 4 exceeds maximum 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateConfigEnv(t)
			if tc.file != "" {
				t.Setenv("AJQ_CONFIG", writeTempConfig(t, tc.file))
			}
			t.Setenv("AJQ_BACKEND_CONCURRENCY", tc.env)
			args := append(tc.args, `.msg =~ "keep"`)
			_, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, args...)
			if err == nil || !strings.Contains(stderr, tc.want) {
				t.Fatalf("error=%v stderr=%q, want %q", err, stderr, tc.want)
			}
		})
	}
}

func TestBackendConcurrencyConstructorsAndPaidDispatchBound(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	openAI, err := newOpenAICompatibleBackend("openai", "https://api.openai.com/v1", "OPENAI_API_KEY", config.Settings{Model: "test", BackendConcurrency: 2})
	if err != nil {
		t.Fatalf("newOpenAICompatibleBackend: %v", err)
	}
	if got := openAI.(*oai.Backend).MaxConcurrency; got != 2 {
		t.Fatalf("OpenAI MaxConcurrency = %d, want 2", got)
	}
	ollama, err := newOllamaBackend(config.Settings{Model: "llama3.2", BackendConcurrency: 4})
	if err != nil {
		t.Fatalf("newOllamaBackend: %v", err)
	}
	if got := ollama.(*ollamabk.Backend).MaxConcurrency; got != 4 {
		t.Fatalf("Ollama MaxConcurrency = %d, want 4", got)
	}
	t.Setenv(anthropicbk.APIKeyEnv, "test-key")
	anthropic, err := constructAnthropicBackend(config.Settings{Model: anthropicbk.DefaultModel, BackendConcurrency: 2})
	if err != nil {
		t.Fatalf("constructAnthropicBackend: %v", err)
	}
	if got := anthropic.(*anthropicbk.Backend).MaxConcurrency; got != 2 {
		t.Fatalf("Anthropic MaxConcurrency = %d, want 2", got)
	}

	for _, concurrency := range []int32{1, 2} {
		t.Run(fmt.Sprintf("OpenAI limit %d", concurrency), func(t *testing.T) {
			runOpenAIConcurrencyScenario(t, concurrency)
		})
	}
}

func runOpenAIConcurrencyScenario(t *testing.T, concurrency int32) {
	t.Helper()
	isolateConfigEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	var active, peak atomic.Int32
	started := make(chan struct{}, 3)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		current := active.Add(1)
		for {
			old := peak.Load()
			if current <= old || peak.CompareAndSwap(old, current) {
				break
			}
		}
		defer active.Add(-1)
		started <- struct{}{}
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"true"}}]}`))
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- Execute(context.Background(), Options{Stdin: strings.NewReader(`[{"msg":"one"},{"msg":"two"},{"msg":"three"}]`), Stdout: &stdout, Stderr: &stderr}, []string{
			"--backend", "openai", "--base-url", server.URL, "--model", "test", "--backend-concurrency", fmt.Sprint(concurrency), "--max-calls", "3", `.[] | .msg =~ "keep"`,
		})
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first request did not start")
	}
	if concurrency == 1 {
		select {
		case <-started:
			t.Fatal("default sequential mode dispatched a second request before the first completed")
		case <-time.After(100 * time.Millisecond):
		}
	} else {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("second request did not start at explicit concurrency limit")
		}
	}
	close(release)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Execute returned error: %v; stderr=%q", err, stderr.String())
		}
	case <-time.After(time.Second):
		t.Fatal("execution did not finish after requests were released")
	}
	if got := peak.Load(); got != concurrency {
		t.Fatalf("peak in-flight OpenAI requests = %d, want %d", got, concurrency)
	}
}

func TestBackendConcurrencyMaxCallsFailurePrecedesPaidDispatch(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		t.Fatal("request dispatched despite max-calls failure")
	}))
	t.Cleanup(server.Close)

	var stdout, stderr bytes.Buffer
	err := Execute(context.Background(), Options{Stdin: strings.NewReader(`[{"msg":"one"},{"msg":"two"}]`), Stdout: &stdout, Stderr: &stderr}, []string{
		"--backend", "openai", "--base-url", server.URL, "--model", "test", "--backend-concurrency", "2", "--max-calls", "1", `.[] | .msg =~ "keep"`,
	})
	if err == nil || !strings.Contains(stderr.String(), "max calls cap exceeded") {
		t.Fatalf("error=%v stderr=%q, want max-calls failure", err, stderr.String())
	}
	if got := requests.Load(); got != 0 {
		t.Fatalf("paid requests = %d, want zero before max-calls failure", got)
	}
}

func TestBackendConcurrencyExplainValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		file string
		env  string
		args []string
		want string
	}{
		{name: "flag", args: []string{"--cloud", "--backend-concurrency", "3"}, want: "anthropic backend concurrency 3 exceeds maximum 2"},
		{name: "environment", env: "3", args: []string{"--cloud"}, want: "anthropic backend concurrency 3 exceeds maximum 2"},
		{name: "TOML", file: "backend = \"anthropic\"\nbackend_concurrency = 3\n", want: "anthropic backend concurrency 3 exceeds maximum 2"},
		{name: "selected backend after switch", file: "backend = \"ollama\"\nmodel = \"llama3.2\"\nbackend_concurrency = 4\n", args: []string{"--backend", "openai", "--model", "test"}, want: "openai backend concurrency 4 exceeds maximum 2"},
	} {
		for _, stdin := range []string{`{"msg":"keep"}`, ""} {
			name := tc.name + "/available"
			if stdin == "" {
				name = tc.name + "/unavailable"
			}
			t.Run(name, func(t *testing.T) {
				isolateConfigEnv(t)
				if tc.file != "" {
					t.Setenv("AJQ_CONFIG", writeTempConfig(t, tc.file))
				}
				t.Setenv("AJQ_BACKEND_CONCURRENCY", tc.env)
				args := append(tc.args, "--explain", `.msg =~ "keep"`)
				_, stderr, err := executeForBackendTest(t, nil, stdin, args...)
				if err == nil || !strings.Contains(stderr, tc.want) {
					t.Fatalf("error=%v stderr=%q, want %q", err, stderr, tc.want)
				}
			})
		}
	}
}
