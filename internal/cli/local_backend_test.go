package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	localbackend "github.com/ricardocabral/ajq/internal/backend/local"
	"github.com/ricardocabral/ajq/internal/cli"
)

// runWithLocalBackend executes the CLI with an injected local semantic backend.
func runWithLocalBackend(be backend.Backend, stdin string, args ...string) (stdout, stderr string, err error) {
	cleanup, cleanupErr := isolateLocalBackendCacheEnv()
	if cleanup != nil {
		defer cleanup()
	}
	if cleanupErr != nil {
		return "", "", cleanupErr
	}
	var out, errBuf bytes.Buffer
	err = cli.Execute(context.Background(), cli.Options{
		Stdin:        strings.NewReader(stdin),
		Stdout:       &out,
		Stderr:       &errBuf,
		LocalBackend: be,
	}, args)
	return out.String(), errBuf.String(), err
}

func isolateLocalBackendCacheEnv() (func(), error) {
	old, hadOld := os.LookupEnv("AJQ_CACHE_DIR")
	dir, err := os.MkdirTemp("", "ajq-cli-cache-*")
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("AJQ_CACHE_DIR", dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return func() {
		if hadOld {
			_ = os.Setenv("AJQ_CACHE_DIR", old)
		} else {
			_ = os.Unsetenv("AJQ_CACHE_DIR")
		}
		_ = os.RemoveAll(dir)
	}, nil
}

// fakeCompletionDaemon returns an httptest.Server that answers /completion
// requests deterministically based on the prompt content (so it does not
// depend on judgement ordering). decide maps a prompt to a JSON content string.
func fakeCompletionDaemon(t *testing.T, decide func(prompt string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Prompt string `json:"prompt"`
		}
		_ = json.Unmarshal(body, &req)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"content": decide(req.Prompt)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestBackendLocalRunsSemMatchQuery(t *testing.T) {
	srv := fakeCompletionDaemon(t, func(prompt string) string {
		// Decide on the judged value line, not the spec, so both records are
		// distinguished (the spec "keep" appears in every prompt).
		if strings.Contains(prompt, "keep this") {
			return "true"
		}
		return "false"
	})
	be := &localbackend.Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	stdout, stderr, err := runWithLocalBackend(be,
		`[{"id":1,"msg":"please keep this"},{"id":2,"msg":"drop it"}]`,
		"--backend", "local", "-c", `.[] | select(.msg =~ "keep") | .id`,
	)
	if err != nil {
		t.Fatalf("--backend local sem_match returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "1\n")
	}
}

func TestBackendLocalRunsClassifyQuery(t *testing.T) {
	srv := fakeCompletionDaemon(t, func(prompt string) string {
		if strings.Contains(prompt, "Value: billing issue") {
			return `"billing"`
		}
		return `"other"`
	})
	be := &localbackend.Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	stdout, stderr, err := runWithLocalBackend(be,
		`[{"msg":"billing issue"},{"msg":"other stuff"}]`,
		"--backend", "local", "-c", `.[] | sem_classify(.msg; "billing"; "other")`,
	)
	if err != nil {
		t.Fatalf("--backend local sem_classify returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "\"billing\"\n\"other\"\n" {
		t.Fatalf("stdout = %q, want billing then other", stdout)
	}
}

func TestBackendLocalWarmFailureSurfacesError(t *testing.T) {
	// A warm (daemon spawn) failure must abort the query with a clear error and
	// never reach the HTTP layer.
	be := &localbackend.Backend{
		BaseURL:  "http://127.0.0.1:1",
		WarmFunc: func(context.Context) error { return fmt.Errorf("daemon spawn failed") },
	}
	stdout, stderr, err := runWithLocalBackend(be,
		`[{"msg":"please keep this"}]`,
		"--backend", "local", "-c", `.[] | select(.msg =~ "keep")`,
	)
	if err == nil {
		t.Fatal("expected warm failure to fail the query")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout on warm failure, got %q", stdout)
	}
	if !strings.Contains(stderr, "daemon spawn failed") {
		t.Fatalf("stderr missing warm failure detail: %q", stderr)
	}
}

// judgeGuardBackend fails the test if Judge is ever called. It verifies that a
// deterministic (pure-jq) query never engages the semantic backend even when
// --backend local is set.
type judgeGuardBackend struct{ t *testing.T }

func (g judgeGuardBackend) Warm(context.Context) error { return nil }
func (g judgeGuardBackend) Judge(context.Context, []backend.Judgement) ([]backend.Result, error) {
	g.t.Fatalf("Judge must not be called for a pure-jq query")
	return nil, nil
}

func TestBackendLocalPureJQNeverJudges(t *testing.T) {
	be := judgeGuardBackend{t: t}
	stdout, stderr, err := runWithLocalBackend(be,
		`{"a":42}`,
		"--backend", "local", "-c", ".a",
	)
	if err != nil {
		t.Fatalf("pure-jq query with --backend local returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "42\n" {
		t.Fatalf("stdout = %q, want 42", stdout)
	}
}

func TestUnknownBackendMentionsLocal(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(`{"a":1}`),
		Stdout: &out,
		Stderr: &errBuf,
	}, []string{"--backend", "bogus", "-c", ".a"})
	if err == nil {
		t.Fatal("expected unknown backend to fail")
	}
	if !strings.Contains(errBuf.String(), "local") || !strings.Contains(errBuf.String(), "mock") {
		t.Fatalf("unknown backend error should mention mock and local: %q", errBuf.String())
	}
}
