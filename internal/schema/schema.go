// Package schema turns semantic operator contracts into deterministic,
// grammar/schema-constrained output shapes for local inference. It is the
// single source of truth for the cross-backend structure guarantee: which jq
// return type each semantic op produces, what enum labels constrain string
// results, how those constraints are expressed as a llama-server json_schema,
// and how a decoded result is validated before it is allowed back into the
// deterministic execution path.
//
// It imports backend and semantics but is never imported by the backend
// package itself, so the deterministic mock backend stays independent of local
// schema transport (and no import cycle is introduced).
package schema

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// Constraint is the deterministic output contract for a single semantic
// judgement: the jq-visible return type plus, for enum-constrained string
// results such as sem_classify labels, the allowed label set. A Constraint is
// produced by Build/ForJudgement only after its op/type/enum combination has
// been validated, so a zero-error Constraint is always internally consistent.
type Constraint struct {
	// Op is the semantic operator name, used only for diagnostics.
	Op string
	// Type is the jq-visible return type the backend must produce.
	Type semantics.ReturnType
	// Enum, when non-empty, constrains a string result to this exact label set.
	Enum []string
}

// ForJudgement resolves the deterministic output constraint for a judgement.
// It prefers an explicit schema type over the declared return type and rejects
// a schema/return mismatch; the enum is taken from the schema or, for
// sem_classify with no explicit enum, from the judgement specs. The resulting
// constraint is fully validated by Build.
func ForJudgement(j backend.Judgement) (Constraint, error) {
	ret := j.Return
	if j.Schema.Type != "" {
		if j.Return != "" && j.Schema.Type != j.Return {
			return Constraint{}, fmt.Errorf("%s: schema type %q does not match return type %q", opLabel(j.Op), j.Schema.Type, j.Return)
		}
		ret = j.Schema.Type
	}
	enum := j.Schema.Enum
	if len(enum) == 0 && j.Op == "sem_classify" {
		enum = j.Specs
	}
	return Build(j.Op, ret, enum)
}

// Build validates an op/return-type/enum combination and produces a Constraint.
// It rejects an empty or unknown return type, an enum on a non-string result
// (an invalid op/schema combination), empty or duplicate labels, and a
// sem_classify contract with no labels. The returned Constraint owns a private
// copy of the enum so later mutation of the caller's slice cannot change it.
func Build(op string, ret semantics.ReturnType, enum []string) (Constraint, error) {
	switch ret {
	case semantics.ReturnBool, semantics.ReturnNumber, semantics.ReturnString:
		// supported
	case "":
		return Constraint{}, fmt.Errorf("%s: missing return type", opLabel(op))
	default:
		return Constraint{}, fmt.Errorf("%s: unknown return type %q", opLabel(op), ret)
	}

	if len(enum) > 0 {
		if ret != semantics.ReturnString {
			return Constraint{}, fmt.Errorf("%s: enum labels are only valid for string results, not %q", opLabel(op), ret)
		}
		if err := validateLabels(op, enum); err != nil {
			return Constraint{}, err
		}
	} else if op == "sem_classify" {
		return Constraint{}, fmt.Errorf("%s: requires at least one label", opLabel(op))
	}

	return Constraint{Op: op, Type: ret, Enum: append([]string(nil), enum...)}, nil
}

// validateLabels rejects empty (or whitespace-only) and duplicate labels so a
// classify/enum contract is always a clean, deterministic set.
func validateLabels(op string, labels []string) error {
	seen := make(map[string]struct{}, len(labels))
	for i, label := range labels {
		if strings.TrimSpace(label) == "" {
			return fmt.Errorf("%s: label %d is empty", opLabel(op), i+1)
		}
		if _, dup := seen[label]; dup {
			return fmt.Errorf("%s: duplicate label %q", opLabel(op), label)
		}
		seen[label] = struct{}{}
	}
	return nil
}

// JSONSchema renders the constraint as the json_schema object accepted by
// llama-server so the daemon constrains generation to a structurally valid
// result. It returns nil for an unset/unknown type.
func (c Constraint) JSONSchema() map[string]any {
	switch c.Type {
	case semantics.ReturnBool:
		return map[string]any{"type": "boolean"}
	case semantics.ReturnNumber:
		return map[string]any{"type": "number"}
	case semantics.ReturnString:
		out := map[string]any{"type": "string"}
		if len(c.Enum) > 0 {
			enum := make([]any, len(c.Enum))
			for i, label := range c.Enum {
				enum[i] = label
			}
			out["enum"] = enum
		}
		return out
	default:
		return nil
	}
}

// Validate checks a decoded result value against the constraint, enforcing the
// schema-invariance guarantee before a value may re-enter deterministic
// execution or be cached. Errors name the operator and the expected type (and,
// for enums, the allowed label set) so failures are debuggable without dumping
// the input value.
func (c Constraint) Validate(value any) error {
	switch c.Type {
	case semantics.ReturnBool:
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: want bool result, got %T", opLabel(c.Op), value)
		}
	case semantics.ReturnNumber:
		switch value.(type) {
		case int, int64, float64, json.Number, *big.Int:
			// supported numeric shapes
		default:
			return fmt.Errorf("%s: want number result, got %T", opLabel(c.Op), value)
		}
	case semantics.ReturnString:
		s, ok := value.(string)
		if !ok {
			return fmt.Errorf("%s: want string result, got %T", opLabel(c.Op), value)
		}
		if len(c.Enum) > 0 && !containsLabel(c.Enum, s) {
			return fmt.Errorf("%s: result %q is not one of labels %v", opLabel(c.Op), s, c.Enum)
		}
	default:
		return fmt.Errorf("%s: unknown return type %q", opLabel(c.Op), c.Type)
	}
	return nil
}

// containsLabel reports whether value is present in labels.
func containsLabel(labels []string, value string) bool {
	for _, label := range labels {
		if label == value {
			return true
		}
	}
	return false
}

// opLabel returns a stable diagnostic label for an operator, tolerating an
// empty op name so schema errors are still readable.
func opLabel(op string) string {
	if strings.TrimSpace(op) == "" {
		return "semantic result"
	}
	return op
}
