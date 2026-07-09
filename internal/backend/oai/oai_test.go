package oai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

type captured struct {
	requests  []chatRequest
	rawBodies []string
	auth      []string
	paths     []string
}

func openAIResponse(content string) string {
	encoded, _ := json.Marshal(chatResponse{Choices: []chatChoice{{Message: chatMessage{Role: "assistant", Content: content}}}})
	return string(encoded)
}

func newFakeOpenAI(t *testing.T, handler func(int, http.ResponseWriter, *http.Request)) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cap.rawBodies = append(cap.rawBodies, string(body))
		cap.auth = append(cap.auth, r.Header.Get("Authorization"))
		cap.paths = append(cap.paths, r.URL.Path)
		var req chatRequest
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

func baseBackend(srv *httptest.Server) *Backend {
	return &Backend{
		BaseURL:    srv.URL + "/v1/",
		APIKey:     "test-key",
		APIKeyEnv:  "OPENAI_API_KEY",
		Model:      "gpt-test",
		HTTPClient: srv.Client(),
	}
}

func TestOpenAIBackendRequestShapeAndBatchOrdering(t *testing.T) {
	contents := []string{"true", "false"}
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIResponse(contents[idx]))
	})
	be := baseBackend(srv)
	batch := []backend.Judgement{
		{Op: "sem_match", Kind: semantics.KindPredicate, Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"urgent"}, ModelID: "openai/cache-id", Value: "urgent billing issue"},
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
		if cap.paths[i] != "/v1"+DefaultChatCompletionsPath {
			t.Fatalf("path[%d] = %q, want /v1%s", i, cap.paths[i], DefaultChatCompletionsPath)
		}
		if cap.auth[i] != "Bearer test-key" {
			t.Fatalf("auth[%d] = %q, want bearer", i, cap.auth[i])
		}
		if req.Model != "gpt-test" {
			t.Fatalf("request[%d].model = %q, want provider model", i, req.Model)
		}
		if req.MaxTokens != DefaultMaxTokens {
			t.Fatalf("request[%d].max_tokens = %d, want %d", i, req.MaxTokens, DefaultMaxTokens)
		}
		if req.Temperature != 0 {
			t.Fatalf("request[%d].temperature = %v, want 0", i, req.Temperature)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("request[%d].messages = %#v, want single user message", i, req.Messages)
		}
		if !strings.Contains(req.Messages[0].Content, batch[i].Value.(string)) {
			t.Fatalf("request[%d] prompt %q missing value %q", i, req.Messages[0].Content, batch[i].Value)
		}
		if req.ResponseFormat == nil {
			t.Fatalf("request[%d] missing response_format", i)
		}
		wantFormat := &responseFormat{Type: "json_schema", JSONSchema: jsonSchemaFormat{Name: "ajq_result", Strict: true, Schema: map[string]any{"type": "boolean"}}}
		if !reflect.DeepEqual(req.ResponseFormat, wantFormat) {
			t.Fatalf("response_format = %#v, want %#v", req.ResponseFormat, wantFormat)
		}
	}
}

func TestOpenAIBackendFallbackOnResponseFormat400(t *testing.T) {
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx == 0 {
			http.Error(w, "unsupported response_format", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIResponse("true"))
	})
	be := baseBackend(srv)
	results, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if results[0].Value != true {
		t.Fatalf("result = %#v, want true", results[0])
	}
	if len(cap.requests) != 2 {
		t.Fatalf("captured %d requests, want fallback attempt", len(cap.requests))
	}
	if cap.requests[0].ResponseFormat == nil {
		t.Fatal("first request missing response_format")
	}
	if cap.requests[1].ResponseFormat != nil {
		t.Fatalf("fallback request response_format = %#v, want nil", cap.requests[1].ResponseFormat)
	}
}

func TestOpenAIBackendFallbackCriteriaLockedDown(t *testing.T) {
	t.Run("non_matching_400_does_not_fallback", func(t *testing.T) {
		srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
			http.Error(w, "bad request", http.StatusBadRequest)
		})
		be := baseBackend(srv)
		_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
		if err == nil || !strings.Contains(err.Error(), "status 400") {
			t.Fatalf("expected status 400 error, got %v", err)
		}
		if len(cap.requests) != 1 {
			t.Fatalf("captured %d requests, want no fallback", len(cap.requests))
		}
	})

	t.Run("fallback_is_one_shot", func(t *testing.T) {
		srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
			http.Error(w, "still rejects response_format", http.StatusBadRequest)
		})
		be := baseBackend(srv)
		_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
		if err == nil || !strings.Contains(err.Error(), "status 400") {
			t.Fatalf("expected fallback status 400 error, got %v", err)
		}
		if len(cap.requests) != 2 {
			t.Fatalf("captured %d requests, want original + one fallback", len(cap.requests))
		}
		if cap.requests[0].ResponseFormat == nil || cap.requests[1].ResponseFormat != nil {
			t.Fatalf("response_format fallback flags = %#v then %#v", cap.requests[0].ResponseFormat, cap.requests[1].ResponseFormat)
		}
	})
}

func TestOpenAIBackendEnumViolationIsPerItemError(t *testing.T) {
	srv, _ := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIResponse(`"maybe"`))
	})
	be := baseBackend(srv)
	batch := []backend.Judgement{{Op: "sem_classify", Return: semantics.ReturnString, Schema: backend.ResultSchema{Type: semantics.ReturnString, Enum: []string{"yes", "no"}}, Specs: []string{"yes", "no"}, Value: "unclear"}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, "not one of labels") {
		t.Fatalf("results = %#v, want enum per-item error", results)
	}
}

func TestOpenAIBackend429RetryAfterRetriedThenSucceeds(t *testing.T) {
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		if idx == 0 {
			w.Header().Set("Retry-After", "2")
			http.Error(w, "slow down", http.StatusTooManyRequests)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, openAIResponse("true"))
	})
	var delays []time.Duration
	be := baseBackend(srv)
	be.RetrySleep = func(ctx context.Context, d time.Duration) error {
		delays = append(delays, d)
		return ctx.Err()
	}
	results, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err != nil {
		t.Fatalf("Judge returned error: %v", err)
	}
	if results[0].Value != true {
		t.Fatalf("result = %#v, want true", results[0])
	}
	if len(cap.requests) != 2 {
		t.Fatalf("requests = %d, want 2", len(cap.requests))
	}
	if len(delays) != 1 || delays[0] != 2*time.Second {
		t.Fatalf("delays = %v, want [2s]", delays)
	}
}

func TestOpenAIBackend401FastFailsWithEnvVar(t *testing.T) {
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid key", http.StatusUnauthorized)
	})
	be := baseBackend(srv)
	be.RetrySleep = func(context.Context, time.Duration) error {
		t.Fatal("401 must not retry")
		return nil
	}
	_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err == nil || !strings.Contains(err.Error(), "OPENAI_API_KEY") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("expected 401 env-var error, got %v", err)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("requests = %d, want fast-fail single request", len(cap.requests))
	}
}

func TestOpenAIBackendCancellationDuringRetryBackoff(t *testing.T) {
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "10")
		http.Error(w, "busy", http.StatusTooManyRequests)
	})
	ctx, cancel := context.WithCancel(context.Background())
	be := baseBackend(srv)
	be.RetrySleep = func(ctx context.Context, d time.Duration) error {
		if d != 10*time.Second {
			t.Fatalf("delay = %v, want 10s", d)
		}
		cancel()
		return ctx.Err()
	}
	_, err := be.Judge(ctx, []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("requests = %d, want no retry after cancellation", len(cap.requests))
	}
}

func TestOpenAIBackendMalformedEnvelopeAbortsBatch(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{name: "invalid_json", body: "{not-json", want: "decode chat completion response"},
		{name: "empty_choices", body: `{"choices":[]}`, want: "empty choices"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.body)
			})
			be := baseBackend(srv)
			_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestOpenAIBackendInvalidContractIsPerItemErrorWithoutHTTP(t *testing.T) {
	srv, cap := newFakeOpenAI(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		t.Fatal("invalid schema contract should not make an HTTP request")
	})
	be := baseBackend(srv)
	batch := []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnString}, Value: "x"}}
	results, err := be.Judge(context.Background(), batch)
	if err != nil {
		t.Fatalf("Judge returned batch error: %v", err)
	}
	if len(results) != 1 || results[0].Error == "" || !strings.Contains(results[0].Error, "does not match return type") {
		t.Fatalf("results = %#v, want schema mismatch per-item error", results)
	}
	if len(cap.requests) != 0 {
		t.Fatalf("requests = %d, want none", len(cap.requests))
	}
}

func TestOpenAIBackendWarmAndEmptyBatch(t *testing.T) {
	be := &Backend{BaseURL: "http://127.0.0.1:1/v1", APIKey: "k", Model: "m"}
	if err := be.Warm(context.Background()); err != nil {
		t.Fatalf("Warm returned error: %v", err)
	}
	results, err := be.Judge(context.Background(), nil)
	if err != nil {
		t.Fatalf("empty Judge returned error: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("len(results) = %d, want 0", len(results))
	}
}
