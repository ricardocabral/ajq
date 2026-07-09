package ollamabk

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

type captured struct {
	requests  []chatRequest
	rawBodies []string
	paths     []string
}

func ollamaResponse(content string) string {
	encoded, _ := json.Marshal(chatResponse{Message: chatMessage{Role: "assistant", Content: content}})
	return string(encoded)
}

func newFakeOllama(t *testing.T, handler func(int, http.ResponseWriter, *http.Request)) (*httptest.Server, *captured) {
	t.Helper()
	cap := &captured{}
	idx := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		cap.rawBodies = append(cap.rawBodies, string(body))
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
	return &Backend{BaseURL: srv.URL + "/", Model: "llama3.2", HTTPClient: srv.Client()}
}

func TestOllamaBackendRequestShapeAndBatchOrdering(t *testing.T) {
	contents := []string{"true", "false"}
	srv, cap := newFakeOllama(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ollamaResponse(contents[idx]))
	})
	be := baseBackend(srv)
	batch := []backend.Judgement{
		{Op: "sem_match", Kind: semantics.KindPredicate, Return: semantics.ReturnBool, Schema: backend.ResultSchema{Type: semantics.ReturnBool}, Specs: []string{"urgent"}, ModelID: "ollama/cache-id", Value: "urgent billing issue"},
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
		if cap.paths[i] != DefaultChatPath {
			t.Fatalf("path[%d] = %q, want %s", i, cap.paths[i], DefaultChatPath)
		}
		if req.Model != "llama3.2" {
			t.Fatalf("request[%d].model = %q, want configured model", i, req.Model)
		}
		if len(req.Messages) != 1 || req.Messages[0].Role != "user" {
			t.Fatalf("request[%d].messages = %#v, want single user message", i, req.Messages)
		}
		if !strings.Contains(req.Messages[0].Content, batch[i].Value.(string)) {
			t.Fatalf("request[%d] prompt %q missing value %q", i, req.Messages[0].Content, batch[i].Value)
		}
		if req.Stream {
			t.Fatalf("request[%d].stream = true, want false", i)
		}
		if req.Options.Temperature != 0 {
			t.Fatalf("request[%d].temperature = %v, want 0", i, req.Options.Temperature)
		}
		if !reflect.DeepEqual(req.Format, map[string]any{"type": "boolean"}) {
			t.Fatalf("request[%d].format = %#v, want boolean schema", i, req.Format)
		}
	}
}

func TestOllamaBackendEnumViolationIsPerItemError(t *testing.T) {
	srv, _ := newFakeOllama(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ollamaResponse(`"maybe"`))
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

func TestOllamaBackendConnectionRefusedErrorText(t *testing.T) {
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	be := &Backend{BaseURL: "http://" + addr, Model: "llama3.2", HTTPClient: &http.Client{}}
	_, err = be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "ollama does not appear to be running") || !strings.Contains(err.Error(), "ollama serve") {
		t.Fatalf("error = %v, want running/serve guidance", err)
	}
}

func TestOllamaBackendModelMissing404ErrorText(t *testing.T) {
	srv, _ := newFakeOllama(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		http.Error(w, `model "missing" not found`, http.StatusNotFound)
	})
	be := &Backend{BaseURL: srv.URL, Model: "missing", HTTPClient: srv.Client()}
	_, err := be.Judge(context.Background(), []backend.Judgement{{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"x"}, Value: "x"}})
	if err == nil || !strings.Contains(err.Error(), "ollama pull missing") || !strings.Contains(err.Error(), "ollama list") {
		t.Fatalf("error = %v, want pull/list guidance", err)
	}
}

func TestResolveBaseURLPrecedenceAndForms(t *testing.T) {
	t.Setenv("OLLAMA_HOST", "127.0.0.1:15555")
	got, err := ResolveBaseURL("http://flag-host:16666/")
	if err != nil {
		t.Fatalf("ResolveBaseURL explicit returned error: %v", err)
	}
	if got != "http://flag-host:16666" {
		t.Fatalf("explicit base URL = %q, want flag URL", got)
	}
	got, err = ResolveBaseURL("")
	if err != nil {
		t.Fatalf("ResolveBaseURL env host:port returned error: %v", err)
	}
	if got != "http://127.0.0.1:15555" {
		t.Fatalf("env host:port base URL = %q", got)
	}
	t.Setenv("OLLAMA_HOST", "https://ollama.example.test:11434/")
	got, err = ResolveBaseURL("")
	if err != nil {
		t.Fatalf("ResolveBaseURL env URL returned error: %v", err)
	}
	if got != "https://ollama.example.test:11434" {
		t.Fatalf("env URL base URL = %q", got)
	}
	t.Setenv("OLLAMA_HOST", "")
	got, err = ResolveBaseURL("")
	if err != nil {
		t.Fatalf("ResolveBaseURL default returned error: %v", err)
	}
	if got != DefaultBaseURL {
		t.Fatalf("default base URL = %q, want %q", got, DefaultBaseURL)
	}
}

func TestOllamaBackendWarmSendsProbe(t *testing.T) {
	srv, cap := newFakeOllama(t, func(idx int, w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, ollamaResponse("true"))
	})
	be := baseBackend(srv)
	if err := be.Warm(context.Background()); err != nil {
		t.Fatalf("Warm returned error: %v", err)
	}
	if len(cap.requests) != 1 {
		t.Fatalf("Warm requests = %d, want 1", len(cap.requests))
	}
	if cap.requests[0].Model != "llama3.2" || cap.requests[0].Stream || cap.requests[0].Format["type"] != "boolean" {
		t.Fatalf("warm request = %#v, want model/stream false/bool format", cap.requests[0])
	}
}

func TestOllamaBackendInvalidContractIsPerItemErrorWithoutHTTP(t *testing.T) {
	srv, cap := newFakeOllama(t, func(idx int, w http.ResponseWriter, r *http.Request) {
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
