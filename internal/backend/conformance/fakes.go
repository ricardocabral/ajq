package conformance

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
)

// Protocol identifies the backend wire format a ScriptedServer should emulate.
type Protocol int

const (
	// ProtocolLocal emulates llama-server's /completion response envelope.
	ProtocolLocal Protocol = iota
	// ProtocolOpenAI emulates OpenAI-compatible /chat/completions responses.
	ProtocolOpenAI
	// ProtocolOllama emulates Ollama's native /api/chat response envelope.
	ProtocolOllama
	// ProtocolAnthropic emulates Anthropic Messages API responses.
	ProtocolAnthropic
)

// ScriptedServer is a httptest server that turns each backend request into the
// next scripted model output for the active conformance case.
type ScriptedServer struct {
	protocol Protocol
	server   *httptest.Server

	mu         sync.Mutex
	scripts    map[string][]scriptResponse
	caseIdx    map[string]int
	activeCase string
}

type scriptResponse struct {
	Content string
	Status  int
}

// NewScriptedServer starts a protocol fake for HTTP backend conformance tests.
func NewScriptedServer(t interface{ Cleanup(func()) }, protocol Protocol) *ScriptedServer {
	s := &ScriptedServer{protocol: protocol}
	s.server = httptest.NewServer(http.HandlerFunc(s.serveHTTP))
	t.Cleanup(s.Close)
	return s
}

// URL returns the fake server base URL.
func (s *ScriptedServer) URL() string { return s.server.URL }

// Close shuts down the fake server.
func (s *ScriptedServer) Close() { s.server.Close() }

// Reset installs fresh scripts for each conformance case.
func (s *ScriptedServer) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scripts = defaultScripts()
	s.caseIdx = make(map[string]int, len(s.scripts))
	s.activeCase = ""
}

// StartCase selects the script consumed by subsequent requests. Run calls this
// before each subtest so normal fake operation never depends on request-body
// inference.
func (s *ScriptedServer) StartCase(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeCase = name
}

func (s *ScriptedServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.validPath(r.URL.Path) {
		http.NotFound(w, r)
		return
	}
	body, _ := io.ReadAll(r.Body)
	caseName := s.currentCase()
	if strings.TrimSpace(caseName) == "" {
		caseName = r.Header.Get("X-AJQ-Conformance-Case")
	}
	if strings.TrimSpace(caseName) == "" {
		caseName = detectCase(body)
	}
	if resp, ok := s.requestAwareResponse(caseName, body); ok {
		s.writeResponse(w, resp)
		return
	}
	resp, ok := s.next(caseName)
	if !ok {
		http.Error(w, fmt.Sprintf("no conformance script for %q", caseName), http.StatusInternalServerError)
		return
	}
	s.writeResponse(w, resp)
}

func (s *ScriptedServer) validPath(path string) bool {
	switch s.protocol {
	case ProtocolLocal:
		return path == "/completion"
	case ProtocolOpenAI:
		return path == "/chat/completions" || path == "/v1/chat/completions"
	case ProtocolOllama:
		return path == "/api/chat"
	case ProtocolAnthropic:
		return path == "/v1/messages" || path == "/messages"
	default:
		return false
	}
}

func (s *ScriptedServer) currentCase() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeCase
}

func (s *ScriptedServer) next(caseName string) (scriptResponse, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	scripts := s.scripts[caseName]
	idx := s.caseIdx[caseName]
	if idx >= len(scripts) {
		return scriptResponse{}, false
	}
	s.caseIdx[caseName] = idx + 1
	return scripts[idx], true
}

func (s *ScriptedServer) writeResponse(w http.ResponseWriter, resp scriptResponse) {
	if resp.Status != 0 && (resp.Status < 200 || resp.Status >= 300) {
		http.Error(w, resp.Content, resp.Status)
		return
	}
	s.writeSuccess(w, resp.Content)
}

func (s *ScriptedServer) writeSuccess(w http.ResponseWriter, content string) {
	w.Header().Set("Content-Type", "application/json")
	switch s.protocol {
	case ProtocolLocal:
		_ = json.NewEncoder(w).Encode(map[string]any{"content": content})
	case ProtocolOpenAI:
		_ = json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{{"message": map[string]any{"role": "assistant", "content": content}}}})
	case ProtocolOllama:
		_ = json.NewEncoder(w).Encode(map[string]any{"message": map[string]any{"role": "assistant", "content": content}})
	case ProtocolAnthropic:
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_conformance", "type": "message", "role": "assistant", "model": "claude-haiku-4-5", "stop_reason": "end_turn", "stop_sequence": nil, "content": []map[string]any{{"type": "text", "text": content}}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1}})
	}
}

func defaultScripts() map[string][]scriptResponse {
	return map[string][]scriptResponse{
		"mixed_batch_order_and_length": {
			{Content: "true"},
			{Content: `"feature"`},
			{Content: `"needs_trim"`},
			{Content: "0.75"},
		},
		"bool_string_is_per_item_error": {
			{Content: `"not a bool"`},
		},
		"classify_out_of_enum_is_per_item_error": {
			{Content: `"surprise"`},
		},
		"number_invalid_json_is_per_item_error": {
			{Content: "not-json"},
		},
		"transport_failure_is_whole_batch_error": {
			{Status: http.StatusInternalServerError, Content: "scripted transport failure"},
			{Status: http.StatusInternalServerError, Content: "scripted transport failure"},
			{Status: http.StatusInternalServerError, Content: "scripted transport failure"},
		},
		"per_item_failure_does_not_poison_sibling": {
			{Content: "true"},
			{Content: `"surprise"`},
			{Content: `"sibling_ok"`},
		},
		"context_cancellation_aborts_promptly": {
			{Content: "true"},
		},
		"all_return_types_round_trip": {
			{Content: "false"},
			{Content: `"bug"`},
			{Content: `"round_trip"`},
			{Content: "1"},
		},
	}
}

// requestAwareResponse maps the judgement value embedded in every supported
// protocol's prompt to a scripted response. Concurrent requests may arrive in
// any order, so protocol conformance cannot assign responses by arrival order.
func (s *ScriptedServer) requestAwareResponse(caseName string, body []byte) (scriptResponse, bool) {
	text := string(body)
	responses := map[string][]struct {
		contains string
		resp     scriptResponse
	}{
		"mixed_batch_order_and_length": {
			{contains: "urgent ticket", resp: scriptResponse{Content: "true"}},
			{contains: "feature request", resp: scriptResponse{Content: `"feature"`}},
			{contains: "Needs Trim", resp: scriptResponse{Content: `"needs_trim"`}},
			{contains: "Value: warm", resp: scriptResponse{Content: "0.75"}},
		},
		"per_item_failure_does_not_poison_sibling": {
			{contains: "urgent ticket", resp: scriptResponse{Content: "true"}},
			{contains: "unknown", resp: scriptResponse{Content: `"surprise"`}},
			{contains: "Sibling OK", resp: scriptResponse{Content: `"sibling_ok"`}},
		},
		"all_return_types_round_trip": {
			{contains: "ordinary", resp: scriptResponse{Content: "false"}},
			{contains: "bug report", resp: scriptResponse{Content: `"bug"`}},
			{contains: "Round Trip", resp: scriptResponse{Content: `"round_trip"`}},
			{contains: "Value: hot", resp: scriptResponse{Content: "1"}},
		},
	}
	for _, candidate := range responses[caseName] {
		if strings.Contains(text, candidate.contains) {
			return candidate.resp, true
		}
	}
	return scriptResponse{}, false
}

func detectCase(body []byte) string {
	var payload any
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	text := fmt.Sprint(payload)
	for _, marker := range []struct {
		contains string
		name     string
	}{
		{"Needs Trim", "mixed_batch_order_and_length"},
		{"not a bool", "bool_string_is_per_item_error"},
		{"unknown", "classify_out_of_enum_is_per_item_error"},
		{"Sibling OK", "per_item_failure_does_not_poison_sibling"},
		{"ordinary", "all_return_types_round_trip"},
		{"hot", "number_invalid_json_is_per_item_error"},
		{"urgent ticket", "transport_failure_is_whole_batch_error"},
	} {
		if strings.Contains(text, marker.contains) {
			return marker.name
		}
	}
	return ""
}
