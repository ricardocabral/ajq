// Package anthropicbk implements the official Anthropic Messages API backend.
package anthropicbk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

const (
	// APIKeyEnv is the only supported source for Anthropic credentials.
	APIKeyEnv = "ANTHROPIC_API_KEY" //nolint:gosec // This is an environment variable name, not a credential value.
	// DefaultModel is the cloud default from the model sizing table.
	DefaultModel = "claude-haiku-4-5"
	// DefaultMaxTokens bounds per-judgement generation; semantic answers are tiny.
	DefaultMaxTokens int64 = 512
)

var modelAliases = map[string]string{
	"haiku":  DefaultModel,
	"sonnet": "claude-sonnet-5",
	"opus":   "claude-opus-4-8",
}

// Backend sends semantic judgements to Anthropic's Messages API using the
// official SDK. Judge resolves a batch sequentially, one Messages.New call per
// judgement, preserving input order. Whole-batch provider/system failures are
// returned from Judge; per-item schema/parse/refusal failures are returned in
// backend.Result.Error.
type Backend struct {
	// Model is the resolved Claude model id sent to Anthropic. Required.
	Model string
	// Client is the official SDK client. Tests may inject a client configured with
	// option.WithBaseURL/WithHTTPClient/WithMaxRetries; production callers should
	// use New so credentials are read from ANTHROPIC_API_KEY only.
	Client anthropic.Client
	// MaxTokens overrides the per-judgement generation budget. Defaults to
	// DefaultMaxTokens and must remain <= 512 for the ajq contract.
	MaxTokens int64
}

// Ensure Backend satisfies the backend.Backend interface.
var _ backend.Backend = (*Backend)(nil)

// ResolveModel maps supported aliases to locked Claude IDs and accepts raw
// claude-* IDs verbatim. Empty input resolves to the default cloud model.
func ResolveModel(model string) (string, error) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return DefaultModel, nil
	}
	if resolved, ok := modelAliases[trimmed]; ok {
		return resolved, nil
	}
	if strings.HasPrefix(trimmed, "claude-") {
		return trimmed, nil
	}
	return "", fmt.Errorf("anthropic backend model %q is invalid; use one of haiku, sonnet, opus, or a raw claude-* model id", model)
}

// ModelIdentity returns the cache identity for a model after alias resolution.
func ModelIdentity(model string) (string, error) {
	resolved, err := ResolveModel(model)
	if err != nil {
		return "", err
	}
	return "anthropic/" + resolved, nil
}

// New constructs an Anthropic backend using ANTHROPIC_API_KEY as the only
// credential source. SDK request options are accepted for trusted transport
// injection (for example httptest base URLs), not for API-key/config plumbing.
func New(model string, opts ...option.RequestOption) (*Backend, error) {
	resolved, err := ResolveModel(model)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(os.Getenv(APIKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("anthropic backend API key is empty; set %s (create a key at https://console.anthropic.com/settings/keys)", APIKeyEnv)
	}
	clientOpts := []option.RequestOption{option.WithoutEnvironmentDefaults()}
	clientOpts = append(clientOpts, opts...)
	// Apply the env-derived API key last so transport-only test options cannot
	// override the task contract that credentials come from ANTHROPIC_API_KEY.
	clientOpts = append(clientOpts, option.WithAPIKey(apiKey))
	return &Backend{Model: resolved, Client: anthropic.NewClient(clientOpts...)}, nil
}

// Warm is a no-op: New has already verified that the required API key exists,
// and the SDK performs provider retries on actual judgement calls.
func (b *Backend) Warm(context.Context) error { return nil }

// Judge sends each judgement to Anthropic sequentially and returns results in
// batch order.
func (b *Backend) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	if strings.TrimSpace(b.Model) == "" {
		return nil, fmt.Errorf("anthropic backend model is empty")
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
			return nil, fmt.Errorf("anthropic backend judgement %d (%s): %w", i, judgement.Op, err)
		}
		results[i] = result
	}
	return results, nil
}

func (b *Backend) judgeOne(ctx context.Context, j backend.Judgement) (backend.Result, error) {
	constraint, err := schema.ForJudgement(j)
	if err != nil {
		return backend.Result{Error: err.Error()}, nil
	}

	msg, err := b.Client.Messages.New(ctx, b.buildRequest(j, constraint))
	if err != nil {
		return backend.Result{}, mapSDKError(err)
	}
	content, err := extractText(msg)
	if err != nil {
		return backend.Result{Error: err.Error()}, nil
	}
	value, verr := coerceResult(constraint, content)
	if verr != nil {
		return backend.Result{Error: verr.Error()}, nil
	}
	return backend.Result{Value: value}, nil
}

func mapSDKError(err error) error {
	var apiErr *anthropic.Error
	if errors.As(err, &apiErr) {
		switch {
		case apiErr.StatusCode == http.StatusUnauthorized:
			return fmt.Errorf("anthropic authentication failed with status %d; check %s (create a key at https://console.anthropic.com/settings/keys): %w", apiErr.StatusCode, APIKeyEnv, err)
		case apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500:
			return fmt.Errorf("anthropic provider returned status %d after SDK retries; judgements are cached so the run can be resumed cheaply: %w", apiErr.StatusCode, err)
		}
	}
	return err
}

func (b *Backend) buildRequest(j backend.Judgement, constraint schema.Constraint) anthropic.MessageNewParams {
	maxTokens := b.MaxTokens
	if maxTokens <= 0 || maxTokens > DefaultMaxTokens {
		maxTokens = DefaultMaxTokens
	}
	return anthropic.MessageNewParams{
		MaxTokens: maxTokens,
		Model:     anthropic.Model(b.Model),
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(buildPrompt(j, constraint))),
		},
		OutputConfig: anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{Schema: constraint.JSONSchema()},
		},
	}
}

func extractText(msg *anthropic.Message) (string, error) {
	if msg == nil {
		return "", fmt.Errorf("anthropic response is empty")
	}
	if msg.StopReason == anthropic.StopReasonRefusal {
		return "", fmt.Errorf("judgement refused by model safety system")
	}
	if len(msg.Content) == 0 {
		return "", fmt.Errorf("anthropic response has no content")
	}
	if len(msg.Content) != 1 {
		return "", fmt.Errorf("anthropic response has %d content blocks; want exactly one text block", len(msg.Content))
	}
	block := msg.Content[0]
	if block.Type != "text" {
		return "", fmt.Errorf("anthropic response content block type %q is not text", block.Type)
	}
	return block.Text, nil
}

// buildPrompt renders the same deterministic judgement layout as the local
// backend: op, return type, specs, allowed labels, canonical value, and the
// operation-specific instruction.
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
