// Package ollamabk implements a thin client for Ollama's native /api/chat API.
package ollamabk

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

const (
	// DefaultBaseURL is Ollama's default local server address.
	DefaultBaseURL = "http://127.0.0.1:11434"
	// DefaultChatPath is Ollama's native structured chat endpoint.
	DefaultChatPath = "/api/chat"
	// defaultTimeout bounds one request when callers do not inject a client.
	defaultTimeout = 60 * time.Second
	// maxResponseBytes caps reads from failing or incompatible servers.
	maxResponseBytes = 1 << 20
)

// Backend sends semantic judgements to Ollama's native /api/chat endpoint.
// Judge resolves batches sequentially, one POST per judgement, preserving input
// order. Whole-batch transport/system failures are returned from Judge;
// schema/parse/type/enum violations are returned as per-item Result.Error.
type Backend struct {
	// BaseURL is the Ollama server base URL, e.g. http://127.0.0.1:11434.
	// When empty, ResolveBaseURL uses OLLAMA_HOST and then DefaultBaseURL.
	BaseURL string
	// Model is the Ollama model name sent to /api/chat. Required.
	Model string
	// HTTPClient issues requests. When nil, a bounded default client is used.
	HTTPClient *http.Client
	// ChatPath overrides the Ollama chat endpoint path. Defaults to /api/chat.
	ChatPath string
}

var _ backend.Backend = (*Backend)(nil)

type chatRequest struct {
	Model    string         `json:"model"`
	Messages []chatMessage  `json:"messages"`
	Stream   bool           `json:"stream"`
	Options  chatOptions    `json:"options"`
	Format   map[string]any `json:"format"`
}

type chatOptions struct {
	Temperature float64 `json:"temperature"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResponse struct {
	Message chatMessage `json:"message"`
}

type statusError struct {
	Code int
	Body string
}

func (e *statusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("ollama returned status %d", e.Code)
	}
	return fmt.Sprintf("ollama returned status %d: %s", e.Code, body)
}

// ResolveBaseURL applies Ollama base URL precedence: explicit flag/config value
// first, then OLLAMA_HOST, then Ollama's default local host. It accepts both
// full URL values and host[:port] forms like Ollama's own CLI.
func ResolveBaseURL(explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return normalizeBaseURL(explicit)
	}
	if host := strings.TrimSpace(os.Getenv("OLLAMA_HOST")); host != "" {
		return normalizeBaseURL(host)
	}
	return DefaultBaseURL, nil
}

func normalizeBaseURL(value string) (string, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(value), "/")
	if trimmed == "" {
		return "", fmt.Errorf("ollama base URL is empty")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("ollama base URL %q is invalid: %w", value, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("ollama base URL %q must use http or https", value)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("ollama base URL %q must include a host", value)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("ollama base URL %q must not include userinfo", value)
	}
	if parsed.RawQuery != "" {
		return "", fmt.Errorf("ollama base URL %q must not include a query string", value)
	}
	if parsed.Fragment != "" {
		return "", fmt.Errorf("ollama base URL %q must not include a fragment", value)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

// Warm sends a tiny structured /api/chat request so Ollama loads the model
// before the batch. Failures are mapped through the same actionable diagnostics
// Judge uses for per-judgement requests.
func (b *Backend) Warm(ctx context.Context) error {
	if err := b.validateConfig(); err != nil {
		return err
	}
	constraint, err := schema.Build("sem_match", semantics.ReturnBool, nil)
	if err != nil {
		return err
	}
	_, err = b.chat(ctx, backend.Judgement{Op: "sem_match", Return: semantics.ReturnBool, Specs: []string{"warmup"}, Value: "warmup"}, constraint)
	if err != nil {
		return fmt.Errorf("ollama warm probe failed: %w", err)
	}
	return nil
}

// Judge sends each judgement to Ollama sequentially and returns results in
// batch order.
func (b *Backend) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	if err := b.validateConfig(); err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, nil
	}
	results := make([]backend.Result, len(batch))
	for i, judgement := range batch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := b.judgeOne(ctx, judgement)
		if err != nil {
			return nil, fmt.Errorf("ollama backend judgement %d (%s): %w", i, judgement.Op, err)
		}
		results[i] = result
	}
	return results, nil
}

func (b *Backend) validateConfig() error {
	if strings.TrimSpace(b.BaseURL) == "" {
		return fmt.Errorf("ollama backend base URL is empty")
	}
	if strings.TrimSpace(b.Model) == "" {
		return fmt.Errorf("ollama backend requires a model; pass --model llama3.2 and check installed models with `ollama list`")
	}
	return nil
}

func (b *Backend) judgeOne(ctx context.Context, j backend.Judgement) (backend.Result, error) {
	constraint, err := schema.ForJudgement(j)
	if err != nil {
		return backend.Result{Error: err.Error()}, nil
	}
	content, err := b.chat(ctx, j, constraint)
	if err != nil {
		return backend.Result{}, err
	}
	value, verr := coerceResult(constraint, content)
	if verr != nil {
		return backend.Result{Error: verr.Error()}, nil
	}
	return backend.Result{Value: value}, nil
}

func (b *Backend) chat(ctx context.Context, j backend.Judgement, constraint schema.Constraint) (string, error) {
	body, err := json.Marshal(b.buildRequest(j, constraint))
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpointURL(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")

	resp, err := b.client().Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", ctxErr
		}
		return "", actionableTransportError(err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		st := &statusError{Code: resp.StatusCode, Body: string(respBody)}
		return "", actionableStatusError(st, b.Model)
	}

	var completion chatResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return "", fmt.Errorf("decode ollama chat response: %w", err)
	}
	return completion.Message.Content, nil
}

func (b *Backend) buildRequest(j backend.Judgement, constraint schema.Constraint) chatRequest {
	return chatRequest{
		Model:    b.Model,
		Messages: []chatMessage{{Role: "user", Content: buildPrompt(j, constraint)}},
		Stream:   false,
		Options:  chatOptions{Temperature: 0},
		Format:   constraint.JSONSchema(),
	}
}

func (b *Backend) endpointURL() string { return strings.TrimRight(b.BaseURL, "/") + b.chatPath() }

func (b *Backend) chatPath() string {
	if strings.TrimSpace(b.ChatPath) == "" {
		return DefaultChatPath
	}
	if strings.HasPrefix(b.ChatPath, "/") {
		return b.ChatPath
	}
	return "/" + b.ChatPath
}

func (b *Backend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return &http.Client{Timeout: defaultTimeout}
}

func actionableTransportError(err error) error {
	if isConnectionRefused(err) {
		return fmt.Errorf("ollama does not appear to be running; start it with `ollama serve` or install Ollama from https://ollama.com/download: %w", err)
	}
	return fmt.Errorf("request failed: %w", err)
}

func isConnectionRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && strings.Contains(strings.ToLower(opErr.Err.Error()), "connection refused") {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "connection refused")
}

func actionableStatusError(st *statusError, model string) error {
	if st.Code == http.StatusNotFound && mentionsModelNotFound(st.Body) {
		return fmt.Errorf("ollama model %q was not found; run `ollama pull %s` and check installed models with `ollama list`: %w", model, model, st)
	}
	return st
}

func mentionsModelNotFound(body string) bool {
	lower := strings.ToLower(body)
	return strings.Contains(lower, "model") && strings.Contains(lower, "not found")
}

func buildPrompt(j backend.Judgement, constraint schema.Constraint) string {
	var sb strings.Builder
	sb.WriteString("You are a deterministic semantic judgement engine for the ajq query tool.\n")
	sb.WriteString("Operation: ")
	sb.WriteString(j.Op)
	sb.WriteString("\n")
	sb.WriteString("Return type: ")
	sb.WriteString(string(constraint.Type))
	sb.WriteString("\n")
	if len(j.Specs) > 0 {
		sb.WriteString("Specs: ")
		sb.WriteString(strings.Join(j.Specs, " | "))
		sb.WriteString("\n")
	}
	if len(constraint.Enum) > 0 {
		sb.WriteString("Allowed labels: ")
		sb.WriteString(strings.Join(constraint.Enum, ", "))
		sb.WriteString("\n")
	}
	sb.WriteString("Value: ")
	sb.WriteString(canonicalValueString(j.Value))
	sb.WriteString("\n")
	sb.WriteString(opInstruction(j.Op, constraint.Type))
	sb.WriteString("\nRespond with only the JSON result and nothing else.")
	return sb.String()
}

func opInstruction(op string, rt semantics.ReturnType) string {
	switch op {
	case "sem_match":
		return "Decide whether the value satisfies the spec. Answer true or false."
	case "sem_classify":
		return "Choose exactly one allowed label that best fits the value."
	case "sem_score":
		return "Rate how strongly the value matches the spec as a number between 0 and 1."
	case "sem_norm":
		return "Return a normalized string form of the value."
	case "sem_extract":
		return "Extract the requested information from the value as a string."
	case "sem_redact":
		return "Return the value with the requested information redacted as a string."
	default:
		switch rt {
		case semantics.ReturnBool:
			return "Answer true or false."
		case semantics.ReturnNumber:
			return "Answer with a number."
		default:
			return "Answer with a string."
		}
	}
}

func coerceResult(constraint schema.Constraint, content string) (any, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("%s: empty completion content", constraint.Op)
	}
	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		if constraint.Type == semantics.ReturnString {
			decoded = trimmed
		} else {
			return nil, fmt.Errorf("%s: invalid JSON result %q: %w", constraint.Op, trimmed, err)
		}
	}
	if err := constraint.Validate(decoded); err != nil {
		return nil, err
	}
	return decoded, nil
}

func canonicalValueString(value any) string {
	if value == nil {
		return "null"
	}
	if s, ok := value.(string); ok {
		return s
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(encoded)
}
