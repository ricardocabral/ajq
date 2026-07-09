// Package oai implements a thin OpenAI-compatible chat/completions backend.
package oai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

const (
	// DefaultChatCompletionsPath is the OpenAI-compatible endpoint used by this
	// backend. BaseURL values are expected to include the API version prefix
	// (for example, https://api.openai.com/v1), so this path appends only the
	// endpoint suffix.
	DefaultChatCompletionsPath = "/chat/completions"
	// DefaultMaxTokens bounds per-judgement generation. Semantic answers are tiny.
	DefaultMaxTokens = 64
	// MaxAttempts bounds retryable HTTP attempts for a single request variant.
	MaxAttempts = 3
	// defaultTimeout bounds a single request when the caller context has no
	// deadline and the caller did not inject a client.
	defaultTimeout = 60 * time.Second
	// defaultInitialBackoff is the base exponential retry delay when Retry-After
	// is absent or invalid.
	defaultInitialBackoff = 100 * time.Millisecond
	// maxResponseBytes caps reads from incompatible or failing servers.
	maxResponseBytes = 1 << 20
)

// SleepFunc sleeps for a retry delay while respecting context cancellation.
type SleepFunc func(context.Context, time.Duration) error

// Backend is a backend.Backend implementation for OpenAI-compatible
// /chat/completions APIs (OpenAI, OpenRouter, vLLM, llama.cpp /v1, LM Studio).
//
// Judge resolves a batch sequentially, one POST per judgement, preserving input
// order. Whole-batch transport, status, and envelope errors are returned from
// Judge. Per-item schema/parse/type/enum failures are returned in Result.Error.
type Backend struct {
	// BaseURL is the API base URL including version prefix, e.g.
	// "https://api.openai.com/v1" or "https://openrouter.ai/api/v1". Required.
	BaseURL string
	// APIKey is sent as Authorization: Bearer <APIKey>. Required.
	APIKey string
	// APIKeyEnv names the environment variable that should contain APIKey. It is
	// included in actionable authentication errors (OPENAI_API_KEY,
	// OPENROUTER_API_KEY, etc.).
	APIKeyEnv string
	// Model is the provider model id sent in the request body. Required.
	Model string
	// HTTPClient issues requests. When nil, a bounded default client is used.
	HTTPClient *http.Client
	// ExtraHeaders are copied onto every request after standard headers. This is
	// primarily for OpenRouter-compatible deployments that may require metadata.
	ExtraHeaders map[string]string
	// ChatCompletionsPath overrides the endpoint path. Defaults to
	// DefaultChatCompletionsPath.
	ChatCompletionsPath string
	// MaxTokens overrides the per-judgement token budget. Defaults to
	// DefaultMaxTokens.
	MaxTokens int
	// RetrySleep is used between retryable attempts. Tests may inject a
	// deterministic hook; nil uses a real timer that aborts on context cancel.
	RetrySleep SleepFunc
}

// Ensure Backend satisfies the backend.Backend interface.
var _ backend.Backend = (*Backend)(nil)

type chatRequest struct {
	Model          string          `json:"model"`
	Messages       []chatMessage   `json:"messages"`
	Temperature    float64         `json:"temperature"`
	MaxTokens      int             `json:"max_tokens"`
	ResponseFormat *responseFormat `json:"response_format,omitempty"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type       string           `json:"type"`
	JSONSchema jsonSchemaFormat `json:"json_schema"`
}

type jsonSchemaFormat struct {
	Name   string         `json:"name"`
	Strict bool           `json:"strict"`
	Schema map[string]any `json:"schema"`
}

type chatResponse struct {
	Choices []chatChoice `json:"choices"`
}

type chatChoice struct {
	Message chatMessage `json:"message"`
}

type statusError struct {
	Code int
	Body string
}

func (e *statusError) Error() string {
	body := strings.TrimSpace(e.Body)
	if body == "" {
		return fmt.Sprintf("provider returned status %d", e.Code)
	}
	return fmt.Sprintf("provider returned status %d: %s", e.Code, body)
}

// Warm is a no-op for remote OpenAI-compatible backends.
func (b *Backend) Warm(context.Context) error { return nil }

// Judge sends each judgement to /chat/completions sequentially and returns
// results in batch order.
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
			return nil, fmt.Errorf("openai-compatible backend judgement %d (%s): %w", i, judgement.Op, err)
		}
		results[i] = result
	}
	return results, nil
}

func (b *Backend) validateConfig() error {
	if strings.TrimSpace(b.BaseURL) == "" {
		return fmt.Errorf("openai-compatible backend base URL is empty")
	}
	if strings.TrimSpace(b.Model) == "" {
		return fmt.Errorf("openai-compatible backend model is empty; pass --model")
	}
	if strings.TrimSpace(b.APIKey) == "" {
		if strings.TrimSpace(b.APIKeyEnv) != "" {
			return fmt.Errorf("openai-compatible backend API key is empty; set %s", b.APIKeyEnv)
		}
		return fmt.Errorf("openai-compatible backend API key is empty")
	}
	return nil
}

func (b *Backend) judgeOne(ctx context.Context, j backend.Judgement) (backend.Result, error) {
	constraint, err := schema.ForJudgement(j)
	if err != nil {
		return backend.Result{Error: err.Error()}, nil
	}

	content, err := b.chat(ctx, j, constraint, true)
	if err != nil {
		var st *statusError
		if asStatusError(err, &st) && st.Code == http.StatusBadRequest && mentionsResponseFormat(st.Body) {
			content, err = b.chat(ctx, j, constraint, false)
		}
		if err != nil {
			return backend.Result{}, err
		}
	}

	value, verr := coerceResult(constraint, content)
	if verr != nil {
		return backend.Result{Error: verr.Error()}, nil
	}
	return backend.Result{Value: value}, nil
}

func asStatusError(err error, target **statusError) bool {
	if se, ok := err.(*statusError); ok {
		*target = se
		return true
	}
	return false
}

func mentionsResponseFormat(body string) bool {
	return strings.Contains(strings.ToLower(body), "response_format")
}

func (b *Backend) chat(ctx context.Context, j backend.Judgement, constraint schema.Constraint, includeResponseFormat bool) (string, error) {
	var lastStatus *statusError
	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		content, status, retryAfter, err := b.postChatCompletion(ctx, j, constraint, includeResponseFormat)
		if err == nil {
			return content, nil
		}
		if status != nil {
			lastStatus = status
			if status.Code == http.StatusUnauthorized || status.Code == http.StatusForbidden {
				return "", b.authError(status)
			}
			if isRetryableStatus(status.Code) && attempt < MaxAttempts {
				delay := retryDelay(attempt, retryAfter)
				if err := b.sleep(ctx, delay); err != nil {
					return "", err
				}
				continue
			}
		}
		return "", err
	}
	if lastStatus != nil {
		return "", lastStatus
	}
	return "", fmt.Errorf("request failed after %d attempts", MaxAttempts)
}

func (b *Backend) postChatCompletion(ctx context.Context, j backend.Judgement, constraint schema.Constraint, includeResponseFormat bool) (string, *statusError, string, error) {
	body, err := json.Marshal(b.buildRequest(j, constraint, includeResponseFormat))
	if err != nil {
		return "", nil, "", fmt.Errorf("marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.endpointURL(), bytes.NewReader(body))
	if err != nil {
		return "", nil, "", fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+b.APIKey)
	for name, value := range b.ExtraHeaders {
		httpReq.Header.Set(name, value)
	}

	resp, err := b.client().Do(httpReq)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return "", nil, "", ctxErr
		}
		return "", nil, "", fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return "", nil, "", fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		st := &statusError{Code: resp.StatusCode, Body: string(respBody)}
		return "", st, resp.Header.Get("Retry-After"), st
	}

	var completion chatResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return "", nil, "", fmt.Errorf("decode chat completion response: %w", err)
	}
	if len(completion.Choices) == 0 {
		return "", nil, "", fmt.Errorf("decode chat completion response: empty choices")
	}
	return completion.Choices[0].Message.Content, nil, "", nil
}

func (b *Backend) buildRequest(j backend.Judgement, constraint schema.Constraint, includeResponseFormat bool) chatRequest {
	maxTokens := b.MaxTokens
	if maxTokens <= 0 {
		maxTokens = DefaultMaxTokens
	}
	req := chatRequest{
		Model:       b.Model,
		Messages:    []chatMessage{{Role: "user", Content: buildPrompt(j, constraint)}},
		Temperature: 0,
		MaxTokens:   maxTokens,
	}
	if includeResponseFormat {
		req.ResponseFormat = &responseFormat{
			Type: "json_schema",
			JSONSchema: jsonSchemaFormat{
				Name:   "ajq_result",
				Strict: true,
				Schema: constraint.JSONSchema(),
			},
		}
	}
	return req
}

func (b *Backend) endpointURL() string {
	return strings.TrimRight(b.BaseURL, "/") + b.chatCompletionsPath()
}

func (b *Backend) chatCompletionsPath() string {
	if strings.TrimSpace(b.ChatCompletionsPath) == "" {
		return DefaultChatCompletionsPath
	}
	if strings.HasPrefix(b.ChatCompletionsPath, "/") {
		return b.ChatCompletionsPath
	}
	return "/" + b.ChatCompletionsPath
}

func (b *Backend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (b *Backend) authError(st *statusError) error {
	body := strings.TrimSpace(st.Body)
	if body != "" {
		body = ": " + body
	}
	if strings.TrimSpace(b.APIKeyEnv) != "" {
		return fmt.Errorf("provider authentication failed with status %d%s; check %s", st.Code, body, b.APIKeyEnv)
	}
	return fmt.Errorf("provider authentication failed with status %d%s; check API key", st.Code, body)
}

func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || (code >= 500 && code <= 599)
}

func retryDelay(attempt int, retryAfter string) time.Duration {
	if delay, ok := parseRetryAfter(retryAfter); ok {
		return delay
	}
	if attempt < 1 {
		attempt = 1
	}
	return defaultInitialBackoff << (attempt - 1)
}

func parseRetryAfter(value string) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if seconds, err := strconv.Atoi(value); err == nil {
		if seconds < 0 {
			return 0, false
		}
		return time.Duration(seconds) * time.Second, true
	}
	if when, err := http.ParseTime(value); err == nil {
		delay := time.Until(when)
		if delay < 0 {
			return 0, false
		}
		return delay, true
	}
	return 0, false
}

func (b *Backend) sleep(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	if b.RetrySleep != nil {
		return b.RetrySleep(ctx, delay)
	}
	if delay == 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// buildPrompt renders a deterministic prompt embedding the judgement's op,
// return type, specs, allowed labels, and canonical value. It intentionally
// mirrors internal/backend/local so local and OpenAI-compatible transports ask
// for equivalent outputs.
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
