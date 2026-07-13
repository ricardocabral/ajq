// Package explain renders ajq's stable query explanation output.
package explain

import (
	"bytes"
	"fmt"
	"io"
	"strings"

	planpkg "github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/semantics"
)

const version = 1

// EstimateStatusAvailable statuses describe whether explain estimates were computed.
const (
	EstimateStatusAvailable              = "available"
	EstimateStatusUnavailablePureJQ      = "unavailable: pure jq"
	EstimateStatusUnavailableNoInput     = "unavailable: no input"
	EstimateStatusUnavailableInvalid     = "unavailable: invalid input"
	EstimateStatusUnavailableHarvest     = "unavailable: unsupported harvest"
	EstimateStatusUnavailableInterleaved = "unavailable: interleaved fallback"
	EstimateStatusUnavailableUserStream  = "unavailable: user stream"
)

// Estimate contains semantic --explain call-estimate fields. Units are explicit:
// call sites are static plan nodes, judgements are distinct semantic values after
// cache/dedup, and mock batches are Backend.Judge invocations.
type Estimate struct {
	Status               string
	Reason               string
	StaticCallSites      int
	InputFrames          int
	HarvestedJudgements  int
	PostDedupJudgements  int
	MockJudgeBatches     int
	EstimatedCostUSD     float64
	EstimatedCostKnown   bool
	EstimatedCostModelID string
}

// Plan describes the stable explain contract for a query. SemanticPlan is
// optional so the Phase 0 pure-jq rendering remains byte-for-byte compatible
// for callers that only know the rewritten query string.
type Plan struct {
	Query        string
	SemanticPlan *planpkg.Plan
	Estimate     *Estimate
	// Stream reports user-selected inline execution for a supported semantic
	// plan. Planner-required interleaving takes precedence and leaves Stream
	// false at the rendering boundary.
	Stream bool
}

// Write renders a byte-stable explanation for an ajq query.
func Write(w io.Writer, plan Plan) error {
	if plan.SemanticPlan == nil || plan.SemanticPlan.Deterministic || len(plan.SemanticPlan.Semantic) == 0 {
		return writePure(w, plan.Query)
	}
	return writeSemantic(w, plan)
}

func writePure(w io.Writer, query string) error {
	_, err := fmt.Fprintf(w, "ajq explain v%d\n", version)
	if err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("query: %q", query),
		"execution: pure-jq deterministic",
		"deterministic: yes",
		"model_calls: 0",
		"backend_calls: 0",
		"byte_reproducible: yes",
		"stdin: ignored",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func writeSemantic(w io.Writer, plan Plan) error {
	semanticPlan := plan.SemanticPlan
	lines := []string{
		fmt.Sprintf("ajq explain v%d", version),
		fmt.Sprintf("query: %q", plan.Query),
		executionLine(semanticPlan, plan.Stream),
		"deterministic: no",
		"model_calls: input-dependent",
		"backend_calls: input-dependent",
		"byte_reproducible: cache-dependent",
		semanticStdinLine(plan.Estimate),
		fmt.Sprintf("planned_call_sites: %d", semanticPlan.Summary.SemanticCount),
		fmt.Sprintf("semantic_predicates: %d", semanticPlan.Summary.PredicateCount),
		fmt.Sprintf("semantic_values: %d", semanticPlan.Summary.ValueCount),
		"estimates:",
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	if err := writeEstimate(w, semanticPlan, plan.Estimate); err != nil {
		return err
	}
	summaryLines := []string{
		"subgraphs:",
		"  deterministic: jq outside semantic call sites",
		fmt.Sprintf("  semantic: %d planned call site(s)", semanticPlan.Summary.SemanticCount),
		"semantic_plan:",
	}
	for _, line := range summaryLines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	for _, node := range semanticPlan.Semantic {
		nodeLines := []string{
			fmt.Sprintf("  - call_id: %d", node.ID),
			fmt.Sprintf("    op: %q", node.Op),
			fmt.Sprintf("    kind: %q", string(node.Kind)),
			fmt.Sprintf("    value_expr: %q", node.ValueExpr),
			fmt.Sprintf("    specs: %s", formatSpecs(node.Specs)),
			fmt.Sprintf("    source_range: %s", formatSourceRange(node.Source)),
			fmt.Sprintf("    gated: %s", gatedPlaceholder(node)),
			fmt.Sprintf("    execution: %s", executionMode(*semanticPlan, node, plan.Stream)),
			"    subgraph: semantic",
		}
		for _, line := range nodeLines {
			if _, err := fmt.Fprintln(w, line); err != nil {
				return err
			}
		}
	}
	return nil
}

func executionLine(semanticPlan *planpkg.Plan, stream bool) string {
	if semanticPlan != nil && semanticPlan.RequiresInterleaved {
		return "execution: semantic interleaved fallback"
	}
	if stream {
		return "execution: semantic user-stream inline"
	}
	return "execution: semantic split plan"
}

func semanticStdinLine(estimate *Estimate) string {
	if estimate == nil || estimate.Status != EstimateStatusAvailable {
		return "stdin: not harvested"
	}
	return "stdin: harvested for estimates"
}

func writeEstimate(w io.Writer, semanticPlan *planpkg.Plan, estimate *Estimate) error {
	staticCallSites := semanticPlan.Summary.SemanticCount
	status := EstimateStatusUnavailableNoInput
	if estimate != nil {
		status = estimate.Status
		if estimate.StaticCallSites != 0 || staticCallSites == 0 {
			staticCallSites = estimate.StaticCallSites
		}
	}
	if _, err := fmt.Fprintf(w, "  estimate_status: %s\n", status); err != nil {
		return err
	}
	if estimate != nil && estimate.Reason != "" {
		if _, err := fmt.Fprintf(w, "  estimate_reason: %q\n", estimate.Reason); err != nil {
			return err
		}
	}
	lines := []string{fmt.Sprintf("  static_call_sites: %d", staticCallSites)}
	if estimate != nil && estimate.Status == EstimateStatusUnavailableUserStream {
		lines = append(lines,
			"  execution_selection: user-selected --stream interleaving",
			"  semantic_batching: inline per uncached judgement",
			"  cross_frame_pre_resolve_dedup: disabled",
		)
	}
	if estimate != nil && estimate.Status == EstimateStatusAvailable {
		lines = append(lines,
			fmt.Sprintf("  input_frames: %d", estimate.InputFrames),
			fmt.Sprintf("  harvested_judgements: %d", estimate.HarvestedJudgements),
			fmt.Sprintf("  post_dedup_judgements: %d", estimate.PostDedupJudgements),
			fmt.Sprintf("  mock_judge_batches: %d", estimate.MockJudgeBatches),
		)
		if estimate.EstimatedCostModelID != "" {
			if estimate.EstimatedCostKnown {
				lines = append(lines, fmt.Sprintf("  estimated_cost_usd: ~$%.2f (%d calls × model %s)", estimate.EstimatedCostUSD, estimate.PostDedupJudgements, estimate.EstimatedCostModelID))
			} else {
				lines = append(lines, fmt.Sprintf("  estimated_cost_usd: unknown (%d calls × model %s; model not in pricing table)", estimate.PostDedupJudgements, estimate.EstimatedCostModelID))
			}
		}
		lines = append(lines,
			"  over_harvest_bound: post_dedup_judgements == mock distinct judgements; may be a safe superset of execute-needed judgements",
		)
	} else {
		lines = append(lines,
			"  input_frames: unavailable",
			"  harvested_judgements: unavailable",
			"  post_dedup_judgements: unavailable",
			"  mock_judge_batches: unavailable",
			"  over_harvest_bound: unavailable until input can be harvested",
		)
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func formatSpecs(specs []string) string {
	if len(specs) == 0 {
		return "[]"
	}
	quoted := make([]string, len(specs))
	for i, spec := range specs {
		quoted[i] = fmt.Sprintf("%q", spec)
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func formatSourceRange(source planpkg.Source) string {
	if !source.HasRange {
		return "unavailable"
	}
	return fmt.Sprintf("%d:%d", source.StartByte, source.EndByte)
}

func gatedPlaceholder(node planpkg.SemNode) string {
	if node.Kind != semantics.KindValue {
		return "n/a"
	}
	if node.Gated {
		return "yes"
	}
	return "no"
}

func executionMode(p planpkg.Plan, node planpkg.SemNode, stream bool) string {
	if stream {
		return "user-stream-inline"
	}
	if node.ExecutionMode != "" {
		return string(node.ExecutionMode)
	}
	if node.Kind == semantics.KindPredicate || node.Op == "sem_classify" || isSupportedBoundedValueNode(p, node) {
		return "3-phase"
	}
	return "interleaved-fallback"
}

func isSupportedBoundedValueNode(p planpkg.Plan, node planpkg.SemNode) bool {
	query := compactSemanticQuery(p.Query)
	expr := compactSemanticQuery(node.Source.Expression)
	switch node.Op {
	case "sem_score":
		return strings.Contains(query, "sort_by("+expr+")")
	case "sem_norm":
		return strings.Contains(query, "group_by("+expr+")")
	default:
		return false
	}
}

func compactSemanticQuery(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// String returns the stable explanation text for tests and callers that need a
// string representation.
func String(plan Plan) string {
	var buf bytes.Buffer
	_ = Write(&buf, plan)
	return buf.String()
}
