package anthropicbk

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

type capturedAnthropic struct {
	requests  []map[string]any
	rawBodies []string
	paths     []string
	headers   []http.Header
}

func anthropicResponse(model, stopReason string, content any) string {
	if stopReason == "" {
		stopReason = "end_turn"
	}
	encoded, _ := json.Marshal(map[string]any{
		"id":            "msg_test",
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  1,
			"output_tokens": 1,
		},
	})
	return string(encoded)
}

func textContent(text string) []map[string]any {
	return []map[string]any{{"type": "text", "text": text}}
}

func newFakeAnthropic(t *testing.T, handler func(int, http.ResponseWriter, *http.Request)) (*httptest.Server, *capturedAnthropic) {
	t.Helper()
	cap := &capturedAnthropic{}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		cap.rawBodies = append(cap.rawBodies, string(body))
		cap.paths = append(cap.paths, r.URL.Path)
		cap.headers = append(cap.headers, r.Header.Clone())
		var req map[string]any
		if len(body) > 0 {
			if err := json.Unmarshal(body, &req); err != nil {
				t.Errorf("server failed to decode request: %v", err)
			}
		}
		cap.requests = append(cap.requests, req)
		handler(idx, w, r)
		idx++
	}))
	t.Cleanup(srv.Close)
	return srv, cap
}

func baseBackend(t *testing.T, srv *httptest.Server) *Backend {
	t.Helper()
	t.Setenv(APIKeyEnv, "test-key")
	be, err := New("haiku", option.WithBaseURL(srv.URL), option.WithHTTPClient(srv.Client()), option.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return be
}

func TestResolveModelAliasesRawAndInvalid(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"", DefaultModel},
		{"haiku", "claude-haiku-4-5"},
		{"sonnet", "claude-sonnet-5"},
		{"opus", "claude-opus-4-8"},
		{"claude-custom-20260708", "claude-custom-20260708"},
	}
	for _, tt := range tests {
		got, err := ResolveModel(tt.in)
		if err != nil {
			t.Fatalf("ResolveModel(%q) returned error: %v", tt.in, err)
		}
		if got != tt.want {
			t.Fatalf("ResolveModel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
	if _, err := ResolveModel("gpt-4o"); err == nil || !strings.Contains(err.Error(), "claude-*") {
		t.Fatalf("ResolveModel invalid error = %v, want claude-* guidance", err)
	}
	identity, err := ModelIdentity("sonnet")
	if err != nil {
		t.Fatalf("ModelIdentity returned error: %v", err)
	}
	if identity != "anthropic/claude-sonnet-5" {
		t.Fatalf("ModelIdentity = %q, want resolved anthropic prefix", identity)
	}
}

func TestNewRequiresAnthropicAPIKeyOnly(t *testing.T) {
	t.Setenv(APIKeyEnv, "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "token-should-not-count")
	_, err := New("haiku")
	if err == nil || !strings.Contains(err.Error(), APIKeyEnv) || !strings.Contains(err.Error(), "console.anthropic.com") {
		t.Fatalf("New missing-key error = %v, want env var and key URL", err)
	}
}

func TestNewEnvAPIKeyWinsOverCallerSDKOption(t *testing.T) {
	srv, cap := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "end_turn", textContent("true")))
	})
	t.Setenv(APIKeyEnv, "env-key")
	be, err := New("haiku", option.WithBaseURL(srv.URL), option.WithHTTPClient(srv.Client()), option.WithMaxRetries(0), option.WithAPIKey("override-key"))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	_, err = be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"x"}, Value: "x"}})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(cap.headers) != 1 {
		t.Fatalf("headers captured = %d, want 1", len(cap.headers))
	}
	if got := cap.headers[0].Get("X-Api-Key"); got != "env-key" {
		t.Fatalf("X-Api-Key = %q, want env-key", got)
	}
}

func TestAnthropicBackendRequestShapeAndBatchOrdering(t *testing.T) {
	contents := []string{"true", "false"}
	srv, cap := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "end_turn", textContent(contents[idx])))
	})
	be := baseBackend(t, srv)
	batch := []backend.Judgement{
		{Op: "sem_match", Kind: semantics.KindPredicate, Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"urgent"}, ModelID: "anthropic/cache-id", Value: "urgent billing issue"},
		{Op: "sem_match", Kind: semantics.KindPredicate, Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"urgent"}, Value: "casual note"},
	}

	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != 2 || results[0].Value != true || results[1].Value != false {
		t.Fatalf("results = %#v, want [true false]", results)
	}
	if len(cap.requests) != 2 {
		t.Fatalf("captured %d requests, want 2", len(cap.requests))
	}
	for i, req := range cap.requests {
		if cap.paths[i] != "/v1/messages" {
			t.Fatalf("path[%d] = %q, want /v1/messages", i, cap.paths[i])
		}
		if req["model"] != DefaultModel {
			t.Fatalf("request[%d].model = %q, want default", i, req["model"])
		}
		if got := int(req["max_tokens"].(float64)); got <= 0 || got > 512 {
			t.Fatalf("request[%d].max_tokens = %d, want 1..512", i, got)
		}
		if _, ok := req["temperature"]; ok {
			t.Fatalf("request[%d] unexpectedly set temperature: %#v", i, req["temperature"])
		}
		assertOutputConfig(t, req)
		messages := req["messages"].([]any)
		if len(messages) != 1 {
			t.Fatalf("request[%d].messages len = %d, want 1", i, len(messages))
		}
		msg := messages[0].(map[string]any)
		if msg["role"] != "user" {
			t.Fatalf("request[%d].role = %q, want user", i, msg["role"])
		}
		blocks := msg["content"].([]any)
		if len(blocks) != 1 {
			t.Fatalf("request[%d].content blocks = %d, want 1", i, len(blocks))
		}
		block := blocks[0].(map[string]any)
		if block["type"] != "text" {
			t.Fatalf("request[%d].content type = %q, want text", i, block["type"])
		}
		prompt := block["text"].(string)
		for _, want := range []string{"Operation: sem_match", "Return type: bool", "Specs: urgent", fmt.Sprintf("Value: %s", batch[i].Value), "Decide whether the value satisfies the spec"} {
			if !strings.Contains(prompt, want) {
				t.Fatalf("request[%d] prompt %q missing %q", i, prompt, want)
			}
		}
	}
}

func assertOutputConfig(t *testing.T, req map[string]any) {
	t.Helper()
	outputConfig, ok := req["output_config"].(map[string]any)
	if !ok {
		t.Fatalf("output_config = %#v, want object", req["output_config"])
	}
	format, ok := outputConfig["format"].(map[string]any)
	if !ok {
		t.Fatalf("output_config.format = %#v, want object", outputConfig["format"])
	}
	if format["type"] != "json_schema" {
		t.Fatalf("format.type = %q, want json_schema", format["type"])
	}
	if !reflect.DeepEqual(format["schema"], map[string]any{"type": "boolean"}) {
		t.Fatalf("format.schema = %#v, want boolean schema", format["schema"])
	}
}

func TestAnthropicBackendEnumViolationIsPerItemError(t *testing.T) {
	srv, _ := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "end_turn", textContent(`"maybe"`)))
	})
	be := baseBackend(t, srv)
	batch := []backend.Judgement{{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"yes", "no"}}, Specs: []string{"yes", "no"}, Value: "unclear"}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, "not one of labels") {
		t.Fatalf("results = %#v, want enum per-item error", results)
	}
}

func TestAnthropicBackendStatusErrors(t *testing.T) {
	tests := []struct {
		status int
		want   []string
	}{
		{http.StatusUnauthorized, []string{APIKeyEnv, "console.anthropic.com"}},
		{http.StatusTooManyRequests, []string{"cached", "resumed cheaply"}},
		{http.StatusInternalServerError, []string{"cached", "resumed cheaply"}},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("status_%d", tt.status), func(t *testing.T) {
			srv, _ := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, `{"type":"error","error":{"type":"authentication_error","message":"boom"}}`)
			})
			be := baseBackend(t, srv)
			_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"x"}, Value: "x"}})
			if err == nil {
				t.Fatal("Judge returned nil error, want status error")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want substring %q", err, want)
				}
			}
		})
	}
}

func TestAnthropicBackendStatusErrorDoesNotExposeResponseBody(t *testing.T) {
	const marker = "echoed-value-marker-xyz"
	srv, _ := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"`+marker+`"}}`)
	})
	be := baseBackend(t, srv)
	_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"x"}, Value: "x"}})
	if err == nil {
		t.Fatal("Judge returned nil error, want status error")
	}
	if strings.Contains(err.Error(), marker) {
		t.Fatalf("error exposed response body marker: %v", err)
	}
	if !strings.Contains(err.Error(), "error_type=invalid_request_error") {
		t.Fatalf("error = %v, want structured error type", err)
	}
}

func TestMapSDKErrorLeavesNonAPIErrorsUntouched(t *testing.T) {
	err := context.Canceled
	if got := mapSDKError(err); got != err {
		t.Fatalf("mapSDKError() = %v, want original error %v", got, err)
	}
}

func TestAnthropicBackendResponseEdgeCasesArePerItemErrors(t *testing.T) {
	tests := []struct {
		name       string
		stopReason string
		content    any
		want       string
	}{
		{"refusal", "refusal", textContent("true"), "refused by model safety system"},
		{"missing content", "end_turn", []map[string]any{}, "no content"},
		{"non text", "end_turn", []map[string]any{{"type": "tool_use", "id": "toolu_1", "name": "noop", "input": map[string]any{}}}, "not text"},
		{"malformed json", "end_turn", textContent("not-json"), "invalid JSON result"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, _ := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, anthropicResponse(DefaultModel, tt.stopReason, tt.content))
			})
			be := baseBackend(t, srv)
			results, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"x"}, Value: "x"}})
			if err != nil {
				t.Fatalf("Judge returned batch error: %v", err)
			}
			if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, tt.want) {
				t.Fatalf("results = %#v, want per-item error containing %q", results, tt.want)
			}
		})
	}
}

func TestAnthropicBackendConcurrentOrderBoundAndItemError(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak := 0, 0
	startedSlow := make(chan struct{})
	releaseSlow := make(chan struct{})
	var startOnce, releaseOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		prompt := string(body)
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
		switch {
		case strings.Contains(prompt, "Value: slow"):
			startOnce.Do(func() { close(startedSlow) })
			<-releaseSlow
			_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "", textContent("true")))
		case strings.Contains(prompt, "Value: item-error"):
			<-startedSlow
			releaseOnce.Do(func() { close(releaseSlow) })
			_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "", textContent(`"not-a-bool"`)))
		case strings.Contains(prompt, "Value: fast"):
			_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "", textContent("true")))
		default:
			t.Errorf("unexpected prompt")
		}
	}))
	t.Cleanup(srv.Close)
	be := baseBackend(t, srv)
	be.MaxConcurrency = 2
	results, err := be.Judge(context.Background(), []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "slow"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "item-error"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "fast"},
	})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != 3 || results[0].Value != true || results[1].Error == "" || results[2].Value != true {
		t.Fatalf("ordered results = %#v, want [true item-error true]", results)
	}
	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak != 2 {
		t.Fatalf("peak requests = %d, want exact bound 2", gotPeak)
	}
}

func TestAnthropicBackendConcurrentRetriesRemainPerJudgement(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak, firstAttempts := 0, 0, 0
	attempts := map[string]int{}
	firstAttemptsReady := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		prompt := string(body)
		kind := ""
		switch {
		case strings.Contains(prompt, "Value: retry-429"):
			kind = "retry-429"
		case strings.Contains(prompt, "Value: retry-5xx"):
			kind = "retry-5xx"
		default:
			t.Errorf("unexpected prompt")
			return
		}
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		attempts[kind]++
		attempt := attempts[kind]
		mu.Unlock()
		defer func() {
			mu.Lock()
			inFlight--
			mu.Unlock()
		}()
		if attempt == 1 {
			mu.Lock()
			firstAttempts++
			if firstAttempts == 2 {
				close(firstAttemptsReady)
			}
			mu.Unlock()
			<-firstAttemptsReady
			w.Header().Set("Retry-After-Ms", "0")
			if kind == "retry-429" {
				http.Error(w, `{"type":"error","error":{"type":"rate_limit_error"}}`, http.StatusTooManyRequests)
				return
			}
			http.Error(w, `{"type":"error","error":{"type":"api_error"}}`, http.StatusInternalServerError)
			return
		}
		_, _ = io.WriteString(w, anthropicResponse(DefaultModel, "", textContent("true")))
	}))
	t.Cleanup(srv.Close)
	t.Setenv(APIKeyEnv, "test-key")
	be, err := New("haiku", option.WithBaseURL(srv.URL), option.WithHTTPClient(srv.Client()), option.WithMaxRetries(2))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	be.MaxConcurrency = 2
	results, err := be.Judge(context.Background(), []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "retry-429"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "retry-5xx"},
	})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if len(results) != 2 || results[0].Value != true || results[1].Value != true {
		t.Fatalf("results = %#v, want ordered successful retries", results)
	}
	mu.Lock()
	gotPeak, got429, got5xx := peak, attempts["retry-429"], attempts["retry-5xx"]
	mu.Unlock()
	if gotPeak != 2 || got429 != 2 || got5xx != 2 {
		t.Fatalf("peak/attempts = %d/%d/%d, want 2/2/2", gotPeak, got429, got5xx)
	}
}

func TestAnthropicBackendConcurrentTransportFailureCancelsSibling(t *testing.T) {
	waitStarted := make(chan struct{})
	waitCancelled := make(chan struct{})
	var waitOnce sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "Value: wait") {
			waitOnce.Do(func() { close(waitStarted) })
			<-r.Context().Done()
			close(waitCancelled)
			return
		}
		<-waitStarted
		http.Error(w, `{"type":"error","error":{"type":"invalid_request_error","message":"provider-secret-response"}}`, http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)
	be := baseBackend(t, srv)
	be.MaxConcurrency = 2
	results, err := be.Judge(context.Background(), []backend.Judgement{
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "fail"},
		{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "wait"},
	})
	if err == nil || len(results) != 0 {
		t.Fatalf("Judge = (%#v, %v), want nil results and whole-batch error", results, err)
	}
	if !strings.Contains(err.Error(), "judgement 0 (sem_match)") || strings.Contains(err.Error(), "provider-secret-response") || strings.Contains(err.Error(), "test-key") {
		t.Fatalf("unsafe or unindexed error: %v", err)
	}
	select {
	case <-waitCancelled:
	case <-time.After(time.Second):
		t.Fatal("admitted sibling did not receive cancellation")
	}
}

func TestAnthropicBackendContextCancellation(t *testing.T) {
	srv, cap := newFakeAnthropic(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		t.Fatal("cancelled context should not issue HTTP request")
	})
	be := baseBackend(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := be.Judge(ctx, []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"x"}, Value: "x"}})
	if err == nil || !strings.Contains(err.Error(), context.Canceled.Error()) {
		t.Fatalf("Judge error = %v, want context canceled", err)
	}
	if len(cap.requests) != 0 {
		t.Fatalf("requests = %d, want none", len(cap.requests))
	}
}
