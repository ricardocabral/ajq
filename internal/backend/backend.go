// Package backend defines the minimal semantic judgment seam used by the
// split executor. Concrete local/cloud model implementations are introduced in
// later phases; Phase 1 uses deterministic tests against this contract.
package backend

import (
	"context"

	"github.com/ricardocabral/ajq/internal/semantics"
)

// Backend is the only interface allowed to cross from the deterministic
// harvest/execute phases into non-deterministic semantic judgment. The split
// executor calls Judge only during resolve.
type Backend interface {
	Judge(ctx context.Context, batch []Judgement) ([]Result, error)
	Warm(ctx context.Context) error
}

// ResultSchema describes the grammar/schema-constrained shape a backend must
// return for a judgement. Type is the jq-visible return type; Enum optionally
// constrains string results such as sem_classify labels.
type ResultSchema struct {
	Type semantics.ReturnType
	Enum []string
}

// Judgement is one semantic decision requested by the resolver. Batch order is
// stable: Result i must answer Judgement i. ModelID is part of the payload so
// backend implementations can select the same model identity used by cache keys.
type Judgement struct {
	Op      string
	Kind    semantics.Kind
	Return  semantics.ReturnType
	Schema  ResultSchema
	Specs   []string
	ModelID string
	Value   any
}

// Result is one backend answer for a Judgement. The resolver validates Value
// against the corresponding Judgement.Return/Schema before it can be used by
// execute. Error is a per-item failure; it aborts resolution without caching the
// failed value. Whole-batch transport/system failures should be returned by
// Backend.Judge as an error instead.
type Result struct {
	Value any
	Error string
}
