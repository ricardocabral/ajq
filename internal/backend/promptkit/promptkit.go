// Package promptkit provides shared deterministic prompt rendering and result
// coercion for semantic backends.
package promptkit

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// BuildPrompt renders a deterministic prompt embedding the judgement's op,
// return type, specs, allowed labels, and canonical value. Determinism keeps
// cache keys and golden expectations stable across runs.
func BuildPrompt(j backend.Judgement, constraint schema.Constraint) string {
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
	sb.WriteString(CanonicalValueString(j.Value))
	sb.WriteString("\n")
	sb.WriteString(OpInstruction(j.Op, constraint.Type))
	sb.WriteString("\nRespond with only the JSON result and nothing else.")
	return sb.String()
}

// OpInstruction returns a stable natural-language instruction for the operator.
func OpInstruction(op string, rt semantics.ReturnType) string {
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

// CoerceResult parses textual content into the constraint's return type and
// validates it against the constraint, including classify enum membership.
func CoerceResult(constraint schema.Constraint, content string) (any, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, fmt.Errorf("%s: empty completion content", constraint.Op)
	}

	var decoded any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		// A string result may arrive unquoted; fall back to the raw text.
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

// CanonicalValueString renders a value deterministically for prompt inclusion.
func CanonicalValueString(value any) string {
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
