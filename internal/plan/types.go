// Package plan builds ajq's semantic execution plan from gojq queries.
package plan

import "github.com/ricardocabral/ajq/internal/semantics"

// Source identifies where a planned semantic node came from. gojq does not
// expose byte offsets on AST nodes, so Step 1/2 use Expression with HasRange
// false and StartByte/EndByte set to -1. The shape is intentionally stable so
// later offset recovery can fill the range without changing callers.
type Source struct {
	Expression string
	StartByte  int
	EndByte    int
	HasRange   bool
}

// CallID identifies one semantic call-site in planner walk order. It is
// deterministic for a query and distinguishes duplicate calls with identical
// op/spec/source text.
type CallID int

// Plan is the static semantic plan for one jq query.
type Plan struct {
	Query               string
	Semantic            []SemNode
	Deterministic       bool
	RequiresInterleaved bool
	Summary             Summary
}

// InstrumentedPlan ties the semantic plan to the query produced by the same
// planner AST walk. Semantic calls in InstrumentedQuery are rewritten to unique
// internal callback names that carry the corresponding CallID at runtime.
type InstrumentedPlan struct {
	Plan
	InstrumentedQuery string
}

// Summary captures deterministic/semantic plan metadata used by explain,
// validation, and later cost-estimation work.
type Summary struct {
	SemanticCount      int
	PredicateCount     int
	ValueCount         int
	ModelCallsEstimate int
	Deterministic      bool
}

// ExecutionMode describes how a semantic call site is safe to execute.
type ExecutionMode string

// ExecutionModeThreePhase values describe the supported semantic execution paths.
const (
	ExecutionModeThreePhase          ExecutionMode = "3-phase"
	ExecutionModeGeneratorSuperset   ExecutionMode = "3-phase-generator-superset"
	ExecutionModeInterleavedFallback ExecutionMode = "interleaved-fallback"
)

// SemNode describes one semantic operator call found in a jq query.
type SemNode struct {
	ID            CallID
	InternalName  string
	Arity         int
	Op            string
	Kind          semantics.Kind
	Return        semantics.ReturnType
	ValueExpr     string
	Specs         []string
	Source        Source
	Gated         bool
	ExecutionMode ExecutionMode
}

func unknownSource(expression string) Source {
	return Source{Expression: expression, StartByte: -1, EndByte: -1, HasRange: false}
}
