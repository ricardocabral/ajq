package local

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// captured records the requests a fake daemon received, in arrival order.
type captured struct {
	requests  []completionRequest
	rawBodies []string
	headers   []http.Header
}

// newFakeDaemon returns an httptest.Server that decodes completion requests,
// records them, and serves the provided contents in call order.
func newFakeDaemon(t *testing.T, contents []string) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.URL.Path != DefaultCompletionPath {
			t.Errorf("path = %s, want %s", r.URL.Path, DefaultCompletionPath)
		}
		body, _ := io.ReadAll(r.Body)
		cap.rawBodies = append(cap.rawBodies, string(body))
		cap.headers = append(cap.headers, r.Header.Clone())
		var req completionRequest
		if err := json.Unmarshal(body, &req); err != nil {
			t.Errorf("server failed to decode request: %v", err)
		}
		cap.requests = append(cap.requests, req)
		content := ""
		if idx < len(contents) {
			content = contents[idx]
		}
		idx++
		writeCompletion(w, content)
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func writeCompletion(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(completionResponse{Content: content})
}

func decodeCompletionRequest(t *testing.T, r *http.Request) completionRequest {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		t.Fatalf("server failed to read request: %v", err)
	}
	var req completionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		t.Fatalf("server failed to decode request: %v", err)
	}
	return req
}

func promptValue(prompt string) string {
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "Value: ") {
			return strings.TrimPrefix(line, "Value: ")
		}
	}
	return ""
}

func quotedContent(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func normJudgements(values ...string) []backend.Judgement {
	batch := make([]backend.Judgement, len(values))
	for i, value := range values {
		batch[i] = backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: value}
	}
	return batch
}

func TestLocalBackendRequestShape(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), ModelID: "test-model"}

	batch := []backend.Judgement{{
		Op:      "sem_match",
		Kind:    semantics.KindPredicate,
		Return:  semantics.ReturnBool,
		Schema:  backend.ResultSchema{Type: semantics.ReturnBool},
		Specs:   []string{"urgent"},
		ModelID: "judgement-model",
		Value:   "urgent billing issue",
	}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != 1 || results[0].Value != true {
		t.Fatalf("results = %#v, want [true]", results)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(cap.requests))
	}
	req := cap.requests[0]
	if req.Model != "judgement-model" {
		t.Fatalf("request model = %q, want judgement-model (judgement overrides backend)", req.Model)
	}
	if req.JSONSchema["type"] != "boolean" {
		t.Fatalf("request json_schema = %#v, want boolean", req.JSONSchema)
	}
	if req.NPredict != DefaultNPredict {
		t.Fatalf("request n_predict = %d, want %d", req.NPredict, DefaultNPredict)
	}
	if req.Temperature != 0 {
		t.Fatalf("request temperature = %v, want 0", req.Temperature)
	}
	for _, want := range []string{"sem_match", "urgent", "urgent billing issue", "bool"} {
		if !strings.Contains(req.Prompt, want) {
			t.Fatalf("prompt %q missing %q", req.Prompt, want)
		}
	}
}

func TestLocalBackendSendsBearerAuthorizationWhenAPIKeyConfigured(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), APIKey: "secret-key"}
	_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(cap.headers) != 1 {
		t.Fatalf("captured %d headers, want 1", len(cap.headers))
	}
	if got := cap.headers[0].Get("Authorization"); got != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}

func TestLocalBackendOmitsAuthorizationWhenAPIKeyEmpty(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if got := cap.headers[0].Get("Authorization"); got != "" {
		t.Fatalf("Authorization = %q, want empty", got)
	}
}

func TestLocalBackendCanDisablePromptCache(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), DisablePromptCache: true}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	if _, err := be.Judge(context.Background(), batch); err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("captured %d requests, want 1", len(cap.requests))
	}
	if cap.requests[0].CachePrompt {
		t.Fatal("cache_prompt = true, want false when DisablePromptCache is set")
	}
}

func TestLocalBackendClassifyEnumSchema(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{`"billing"`})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}

	batch := []backend.Judgement{{
		Op:     "sem_classify",
		Kind:   semantics.KindValue,
		Return: semantics.ReturnString,
		Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"billing", "other"}},
		Specs:  []string{"billing", "other"},
		Value:  "urgent billing issue",
	}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if results[0].Value != "billing" {
		t.Fatalf("result = %#v, want billing", results[0])
	}
	enum, ok := cap.requests[0].JSONSchema["enum"].([]any)
	if !ok || len(enum) != 2 || enum[0] != "billing" || enum[1] != "other" {
		t.Fatalf("json_schema enum = %#v, want [billing other]", cap.requests[0].JSONSchema["enum"])
	}
}

// TestLocalBackendRequestSchemaPerOp proves each v1 semantic op sends the
// expected json_schema constraint derived from the shared schema builder.
func TestLocalBackendRequestSchemaPerOp(t *testing.T) {
	cases := []struct {
		name       string
		judgement  backend.Judgement
		content    string
		wantSchema map[string]any
	}{
		{
			name:       "sem_match_bool",
			judgement:  backend.Judgement{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"urgent"}, Value: "x"},
			content:    "true",
			wantSchema: map[string]any{"type": "boolean"},
		},
		{
			name:       "sem_classify_enum",
			judgement:  backend.Judgement{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"billing", "other"}}, Specs: []string{"billing", "other"}, Value: "x"},
			content:    `"billing"`,
			wantSchema: map[string]any{"type": "string", "enum": []any{"billing", "other"}},
		},
		{
			name:       "sem_extract_string",
			judgement:  backend.Judgement{Op: "sem_extract", Return: semantics.ReturnString, Specs: []string{"name"}, Value: "x"},
			content:    `"acme"`,
			wantSchema: map[string]any{"type": "string"},
		},
		{
			name:       "sem_norm_string",
			judgement:  backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "X!!"},
			content:    `"x"`,
			wantSchema: map[string]any{"type": "string"},
		},
		{
			name:       "sem_redact_string",
			judgement:  backend.Judgement{Op: "sem_redact", Return: semantics.ReturnString, Specs: []string{"ssn"}, Value: "x"},
			content:    `"[redacted]"`,
			wantSchema: map[string]any{"type": "string"},
		},
		{
			name:       "sem_score_number",
			judgement:  backend.Judgement{Op: "sem_score", Return: semantics.ReturnNumber, Specs: []string{"urgent"}, Value: "x"},
			content:    "0.5",
			wantSchema: map[string]any{"type": "number"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, cap := newFakeDaemon(t, []string{tc.content})
			be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
			if _, err := be.Judge(context.Background(), []backend.Judgement{tc.judgement}); err != nil {
				t.Fatalf("Judge error = %v", err)
			}
			if len(cap.requests) != 1 {
				t.Fatalf("captured %d requests, want 1", len(cap.requests))
			}
			if got := cap.requests[0].JSONSchema; !reflect.DeepEqual(got, tc.wantSchema) {
				t.Fatalf("json_schema = %#v, want %#v", got, tc.wantSchema)
			}
		})
	}
}

// TestLocalBackendInvalidContractIsPerItemError proves an inconsistent
// op/schema contract surfaces as a per-item error without any HTTP call.
func TestLocalBackendInvalidContractIsPerItemError(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	// Schema type contradicts declared return type: build must fail per-item.
	batch := []backend.Judgement{{
		Op:     "sem_match",
		Return: semantics.ReturnBool,
		Schema: backend.ResultSchema{Type: semantics.ReturnString},
		Value:  "x",
	}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, "does not match return type") {
		t.Fatalf("results = %#v, want per-item schema mismatch error", results)
	}
	if len(cap.requests) != 0 {
		t.Fatalf("invalid contract should short-circuit before HTTP; got %d requests", len(cap.requests))
	}
}

func TestLocalBackendNumberResult(t *testing.T) {
	srv, _ := newFakeDaemon(t, []string{"0.87"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{{Op: "sem_score", Return: semantics.ReturnNumber, Specs: []string{"urgent"}, Value: "urgent"}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if results[0].Value != 0.87 {
		t.Fatalf("result = %#v, want 0.87", results[0])
	}
}

func TestLocalBackendStringRawFallback(t *testing.T) {
	// A string result that arrives unquoted must still be accepted.
	srv, _ := newFakeDaemon(t, []string{"billing_issue"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"billing"}, Value: "Billing!!!"}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if results[0].Value != "billing_issue" {
		t.Fatalf("result = %#v, want billing_issue", results[0])
	}
}

// TestLocalBackendMalformedJSONContentIsPerItemError proves that non-string
// results whose content is not valid JSON are rejected per-item (invariance at
// the boundary) without aborting the batch, and string results still fall back
// to the raw text.
func TestLocalBackendMalformedJSONContentIsPerItemError(t *testing.T) {
	srv, _ := newFakeDaemon(t, []string{"not-json", "{oops"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"a"}, Value: "v0"},
		{Op: "sem_score", Return: semantics.ReturnNumber, Specs: []string{"a"}, Value: "v1"},
	}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	for i, r := range results {
		if r.Error == "" || !strings.Contains(r.Error, "invalid JSON result") {
			t.Fatalf("results[%d] = %#v, want invalid JSON per-item error", i, r)
		}
	}
}

func TestLocalBackendTransportFailureAbortsBatch(t *testing.T) {
	srv, _ := newFakeDaemon(t, nil)
	url := srv.URL
	srv.Close() // force a connection failure
	be := &Backend{BaseURL: url, HTTPClient: &http.Client{}}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	if _, err := be.Judge(context.Background(), batch); err == nil {
		t.Fatal("expected transport failure error, got nil")
	}
}

func TestLocalBackendNon2xxAbortsBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	_, err := be.Judge(context.Background(), batch)
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("expected status 500 error, got %v", err)
	}
}

func TestLocalBackendMalformedEnvelopeAbortsBatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{not json")
	}))
	t.Cleanup(srv.Close)
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	_, err := be.Judge(context.Background(), batch)
	if err == nil || !strings.Contains(err.Error(), "decode completion response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLocalBackendCancellation(t *testing.T) {
	srv, _ := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before Judge runs
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	_, err := be.Judge(ctx, batch)
	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
}

func TestLocalBackendWarmDelegates(t *testing.T) {
	called := 0
	be := &Backend{BaseURL: "http://127.0.0.1:1", WarmFunc: func(context.Context) error {
		called++
		return nil
	}}
	if err := be.Warm(context.Background()); err != nil {
		t.Fatalf("Warm returned error: %v", err)
	}
	if called != 1 {
		t.Fatalf("WarmFunc called %d times, want 1", called)
	}
	// No WarmFunc -> no-op.
	noop := &Backend{BaseURL: "http://127.0.0.1:1"}
	if err := noop.Warm(context.Background()); err != nil {
		t.Fatalf("no-op Warm returned error: %v", err)
	}
}

func TestLocalBackendLazyWarmOncePerInstance(t *testing.T) {
	srv, _ := newFakeDaemon(t, []string{"true", "false"})
	warmCalls := 0
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), WarmFunc: func(context.Context) error {
		warmCalls++
		return nil
	}}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	if _, err := be.Judge(context.Background(), batch); err != nil {
		t.Fatalf("first Judge error: %v", err)
	}
	if _, err := be.Judge(context.Background(), batch); err != nil {
		t.Fatalf("second Judge error: %v", err)
	}
	if warmCalls != 1 {
		t.Fatalf("WarmFunc called %d times across two Judge calls, want 1", warmCalls)
	}
}

func TestLocalBackendLazyWarmFailureAbortsBatch(t *testing.T) {
	srv, cap := newFakeDaemon(t, []string{"true"})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), WarmFunc: func(context.Context) error {
		return io.EOF
	}}
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "y"}}
	if _, err := be.Judge(context.Background(), batch); err == nil {
		t.Fatal("expected warm failure error")
	}
	if len(cap.requests) != 0 {
		t.Fatalf("warm failure should short-circuit before HTTP; got %d requests", len(cap.requests))
	}
}

func TestLocalBackendBatchOrderingIsStable(t *testing.T) {
	// Distinct per-index contents confirm result i answers judgement i and that
	// requests arrive in batch order.
	srv, cap := newFakeDaemon(t, []string{`"alpha"`, `"beta"`, `"gamma"`})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	values := []string{"first", "second", "third"}
	batch := make([]backend.Judgement, len(values))
	for i, v := range values {
		batch[i] = backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: v}
	}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != len(batch) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(batch))
	}
	want := []string{"alpha", "beta", "gamma"}
	for i := range want {
		if results[i].Value != want[i] {
			t.Fatalf("results[%d] = %#v, want %q", i, results[i], want[i])
		}
		if results[i].Error != "" {
			t.Fatalf("results[%d] unexpected error %q", i, results[i].Error)
		}
	}
	// Requests must have arrived in batch order, carrying each value.
	for i, v := range values {
		if !strings.Contains(cap.requests[i].Prompt, v) {
			t.Fatalf("request[%d] prompt %q missing value %q", i, cap.requests[i].Prompt, v)
		}
	}
}

func TestLocalBackendMixedSuccessAndPerItemErrors(t *testing.T) {
	// Item 0 succeeds; item 1 returns a wrong-typed value (per-item error); item
	// 2 returns an out-of-enum label (per-item error); item 3 succeeds. Per-item
	// failures must not abort the batch or shift ordering.
	srv, _ := newFakeDaemon(t, []string{`true`, `"not-a-bool"`, `"maybe"`, `false`})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	batch := []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"a"}, Value: "v0"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"a"}, Value: "v1"},
		{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"yes", "no"}}, Specs: []string{"yes", "no"}, Value: "v2"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"a"}, Value: "v3"},
	}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if len(results) != 4 {
		t.Fatalf("len(results) = %d, want 4", len(results))
	}
	if results[0].Value != true || results[0].Error != "" {
		t.Fatalf("results[0] = %#v, want true/no-error", results[0])
	}
	if results[1].Error == "" || !strings.Contains(results[1].Error, "want bool") {
		t.Fatalf("results[1] = %#v, want bool type error", results[1])
	}
	if results[2].Error == "" || !strings.Contains(results[2].Error, "not one of labels") {
		t.Fatalf("results[2] = %#v, want enum error", results[2])
	}
	if results[3].Value != false || results[3].Error != "" {
		t.Fatalf("results[3] = %#v, want false/no-error", results[3])
	}
}

func TestLocalBackendTouchFuncCalledPerJudgement(t *testing.T) {
	// TouchFunc records daemon activity so an active batch keeps the daemon warm
	// and is never reaped mid-run. It must fire once per judgement, in order.
	srv, _ := newFakeDaemon(t, []string{`"a"`, `"b"`, `"c"`})
	var touches int
	be := &Backend{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		TouchFunc:  func(context.Context) { touches++ },
	}
	batch := []backend.Judgement{
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "1"},
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "2"},
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "3"},
	}
	if _, err := be.Judge(context.Background(), batch); err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if touches != len(batch) {
		t.Fatalf("TouchFunc called %d times, want %d (once per judgement)", touches, len(batch))
	}
}

func TestLocalBackendNilTouchFuncIsNoop(t *testing.T) {
	// A nil TouchFunc must be safe (used by tests against a standalone server).
	srv, _ := newFakeDaemon(t, []string{`"a"`})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	if _, err := be.Judge(context.Background(), []backend.Judgement{
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "1"},
	}); err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
}

// TestLocalBackendSequentialOneRequestPerJudgement locks in the compatibility
// path: MaxConcurrency <= 1 resolves a post-dedup batch as exactly one
// /completion request per judgement, issued sequentially in batch order, rather
// than a single whole-batch request or concurrent requests.
func TestLocalBackendSequentialOneRequestPerJudgement(t *testing.T) {
	// The fake serves content by arrival order; distinct per-value prompts let us
	// confirm each request arrived in batch order (which only holds because the
	// transport is sequential, not concurrent).
	srv, cap := newFakeDaemon(t, []string{`"a"`, `"b"`, `"c"`, `"d"`})
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	values := []string{"one", "two", "three", "four"}
	batch := make([]backend.Judgement, len(values))
	for i, v := range values {
		batch[i] = backend.Judgement{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: v}
	}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	// One request per judgement — not fewer (no whole-batch single request) and
	// not more.
	if len(cap.requests) != len(batch) {
		t.Fatalf("issued %d requests, want exactly one per judgement (%d)", len(cap.requests), len(batch))
	}
	if len(results) != len(batch) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(batch))
	}
	// Requests must have arrived in strict batch order — the signature of
	// sequential transport.
	for i, v := range values {
		if !strings.Contains(cap.requests[i].Prompt, v) {
			t.Fatalf("request[%d] prompt %q missing value %q; arrival order != batch order", i, cap.requests[i].Prompt, v)
		}
	}
}

func TestLocalBackendParallelBoundedPeak(t *testing.T) {
	var current int32
	var peak int32
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeCompletionRequest(t, r)
		inFlight := atomic.AddInt32(&current, 1)
		atomic.AddInt32(&requests, 1)
		for {
			old := atomic.LoadInt32(&peak)
			if inFlight <= old || atomic.CompareAndSwapInt32(&peak, old, inFlight) {
				break
			}
		}
		defer atomic.AddInt32(&current, -1)
		time.Sleep(30 * time.Millisecond)
		writeCompletion(w, quotedContent("out-"+promptValue(req.Prompt)))
	}))
	t.Cleanup(srv.Close)

	values := []string{"0", "1", "2", "3", "4", "5"}
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxConcurrency: 2}
	results, err := be.Judge(context.Background(), normJudgements(values...))
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != len(values) {
		t.Fatalf("len(results) = %d, want %d", len(results), len(values))
	}
	if got := int(atomic.LoadInt32(&requests)); got != len(values) {
		t.Fatalf("requests = %d, want %d", got, len(values))
	}
	if got := atomic.LoadInt32(&peak); got > 2 || got < 2 {
		t.Fatalf("peak in-flight = %d, want exactly configured bound 2", got)
	}
}

func TestLocalBackendParallelPreservesOrderUnderJitter(t *testing.T) {
	delays := map[string]time.Duration{"slow": 45 * time.Millisecond, "medium": 25 * time.Millisecond, "fast": 5 * time.Millisecond, "instant": 0}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeCompletionRequest(t, r)
		value := promptValue(req.Prompt)
		time.Sleep(delays[value])
		writeCompletion(w, quotedContent("out-"+value))
	}))
	t.Cleanup(srv.Close)

	values := []string{"slow", "medium", "fast", "instant"}
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxConcurrency: 4}
	results, err := be.Judge(context.Background(), normJudgements(values...))
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	for i, value := range values {
		want := "out-" + value
		if results[i].Value != want || results[i].Error != "" {
			t.Fatalf("results[%d] = %#v, want %q without error", i, results[i], want)
		}
	}
}

func TestLocalBackendParallelFirstTransportErrorCancelsSiblings(t *testing.T) {
	var requests int32
	var canceledSiblings int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeCompletionRequest(t, r)
		value := promptValue(req.Prompt)
		atomic.AddInt32(&requests, 1)
		if value == "fail" {
			deadline := time.Now().Add(200 * time.Millisecond)
			for atomic.LoadInt32(&requests) < 4 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		select {
		case <-r.Context().Done():
			atomic.AddInt32(&canceledSiblings, 1)
			return
		case <-time.After(2 * time.Second):
			writeCompletion(w, quotedContent("late-"+value))
		}
	}))
	t.Cleanup(srv.Close)

	values := []string{"fail", "slow-1", "slow-2", "slow-3", "queued-4", "queued-5", "queued-6", "queued-7"}
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxConcurrency: 4}
	results, err := be.Judge(context.Background(), normJudgements(values...))
	if err == nil || !strings.Contains(err.Error(), "status 500") {
		t.Fatalf("Judge error = %v, want status 500", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %#v, want no partial results on transport error", results)
	}
	if got := int(atomic.LoadInt32(&requests)); got >= len(values) {
		t.Fatalf("requests = %d, want cancellation to stop queued dispatch before all %d", got, len(values))
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for atomic.LoadInt32(&canceledSiblings) == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if got := atomic.LoadInt32(&canceledSiblings); got == 0 {
		t.Fatal("expected at least one in-flight sibling request to observe cancellation")
	}
}

func TestLocalBackendParallelPerItemErrorsAndTouchIsolation(t *testing.T) {
	var touches int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := decodeCompletionRequest(t, r)
		switch promptValue(req.Prompt) {
		case "v0":
			writeCompletion(w, "true")
		case "v1":
			writeCompletion(w, quotedContent("not-a-bool"))
		case "v2":
			writeCompletion(w, quotedContent("ok"))
		case "v3":
			writeCompletion(w, "false")
		default:
			http.Error(w, "unexpected value", http.StatusBadRequest)
		}
	}))
	t.Cleanup(srv.Close)

	batch := []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "v0"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "v1"},
		{Op: "sem_norm", Return: semantics.ReturnString, Specs: []string{"x"}, Value: "v2"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "v3"},
	}
	be := &Backend{
		BaseURL:        srv.URL,
		HTTPClient:     srv.Client(),
		MaxConcurrency: 4,
		TouchFunc:      func(context.Context) { atomic.AddInt32(&touches, 1) },
	}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if results[0].Value != true || results[0].Error != "" {
		t.Fatalf("results[0] = %#v, want true/no-error", results[0])
	}
	if results[1].Error == "" || !strings.Contains(results[1].Error, "want bool") {
		t.Fatalf("results[1] = %#v, want bool per-item error", results[1])
	}
	if results[2].Value != "ok" || results[2].Error != "" {
		t.Fatalf("results[2] = %#v, want ok/no-error", results[2])
	}
	if results[3].Value != false || results[3].Error != "" {
		t.Fatalf("results[3] = %#v, want false/no-error", results[3])
	}
	if got := int(atomic.LoadInt32(&touches)); got != len(batch) {
		t.Fatalf("TouchFunc called %d times, want %d", got, len(batch))
	}
}

func TestLocalBackendParallelCancellationAbortsPromptly(t *testing.T) {
	started := make(chan struct{})
	var closeStarted sync.Once
	var requests int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = decodeCompletionRequest(t, r)
		atomic.AddInt32(&requests, 1)
		closeStarted.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithCancel(context.Background())
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client(), MaxConcurrency: 4}
	done := make(chan error, 1)
	go func() {
		_, err := be.Judge(ctx, normJudgements("0", "1", "2", "3", "4", "5"))
		done <- err
	}()

	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for in-flight request")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), "context canceled") {
			t.Fatalf("Judge error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Judge did not return promptly after cancellation")
	}
	if got := atomic.LoadInt32(&requests); got > 4 {
		t.Fatalf("requests = %d, want no more than MaxConcurrency dispatched before cancellation", got)
	}
}

func TestLocalBackendEmptyBatch(t *testing.T) {
	srv, cap := newFakeDaemon(t, nil)
	be := &Backend{BaseURL: srv.URL, HTTPClient: srv.Client()}
	results, err := be.Judge(context.Background(), nil)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len(results) = %d, want 0", len(results))
	}
	if len(cap.requests) != 0 {
		t.Fatalf("empty batch issued %d requests, want 0", len(cap.requests))
	}
}

func TestLocalBackendEmptyBaseURL(t *testing.T) {
	be := &Backend{}
	if _, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match"}}); err == nil {
		t.Fatal("expected error for empty base URL")
	}
}
