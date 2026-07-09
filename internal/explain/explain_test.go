package explain_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/desugar"
	"github.com/ricardocabral/ajq/internal/explain"
	"github.com/ricardocabral/ajq/internal/plan"
)

const wantExplain = "ajq explain v1\n" +
	"query: \".items[] | select(.active) | .id\"\n" +
	"execution: pure-jq deterministic\n" +
	"deterministic: yes\n" +
	"model_calls: 0\n" +
	"backend_calls: 0\n" +
	"byte_reproducible: yes\n" +
	"stdin: ignored\n"

const wantSemanticExplain = "ajq explain v1\n" +
	"query: \".items[] | select(sem_match(.feedback; \\\"angry/frustrated\\\")) | {mood: sem_classify(.feedback; \\\"angry\\\"; \\\"happy\\\")}\"\n" +
	"execution: semantic split plan\n" +
	"deterministic: no\n" +
	"model_calls: input-dependent\n" +
	"backend_calls: input-dependent\n" +
	"byte_reproducible: cache-dependent\n" +
	"stdin: not harvested\n" +
	"planned_call_sites: 2\n" +
	"semantic_predicates: 1\n" +
	"semantic_values: 1\n" +
	"estimates:\n" +
	"  estimate_status: unavailable: no input\n" +
	"  static_call_sites: 2\n" +
	"  input_frames: unavailable\n" +
	"  harvested_judgements: unavailable\n" +
	"  post_dedup_judgements: unavailable\n" +
	"  mock_judge_batches: unavailable\n" +
	"  over_harvest_bound: unavailable until input can be harvested\n" +
	"subgraphs:\n" +
	"  deterministic: jq outside semantic call sites\n" +
	"  semantic: 2 planned call site(s)\n" +
	"semantic_plan:\n" +
	"  - call_id: 1\n" +
	"    op: \"sem_match\"\n" +
	"    kind: \"predicate\"\n" +
	"    value_expr: \".feedback\"\n" +
	"    specs: [\"angry/frustrated\"]\n" +
	"    source_range: 18:58\n" +
	"    gated: n/a\n" +
	"    execution: 3-phase\n" +
	"    subgraph: semantic\n" +
	"  - call_id: 2\n" +
	"    op: \"sem_classify\"\n" +
	"    kind: \"value\"\n" +
	"    value_expr: \".feedback\"\n" +
	"    specs: [\"angry\", \"happy\"]\n" +
	"    source_range: 69:110\n" +
	"    gated: no\n" +
	"    execution: 3-phase\n" +
	"    subgraph: semantic\n"

func TestWriteStablePureJQPlan(t *testing.T) {
	var buf bytes.Buffer
	if err := explain.Write(&buf, explain.Plan{Query: ".items[] | select(.active) | .id"}); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := buf.String(); got != wantExplain {
		t.Fatalf("explain output mismatch\ngot:\n%s\nwant:\n%s", got, wantExplain)
	}
}

func TestSemanticPlanListsStableNodeDetails(t *testing.T) {
	query := `.items[] | select(sem_match(.feedback; "angry/frustrated")) | {mood: sem_classify(.feedback; "angry"; "happy")}`
	semanticPlan := buildPlan(t, query)
	got := explain.String(explain.Plan{Query: query, SemanticPlan: &semanticPlan})
	if got != wantSemanticExplain {
		t.Fatalf("semantic explain output mismatch\ngot:\n%s\nwant:\n%s", got, wantSemanticExplain)
	}
}

func TestSemanticPlanRendersAvailableEstimates(t *testing.T) {
	query := `.[] | sem_match(.msg; "keep")`
	semanticPlan := buildPlan(t, query)
	got := explain.String(explain.Plan{Query: query, SemanticPlan: &semanticPlan, Estimate: &explain.Estimate{
		Status:              explain.EstimateStatusAvailable,
		StaticCallSites:     1,
		InputFrames:         1,
		HarvestedJudgements: 3,
		PostDedupJudgements: 2,
		MockJudgeBatches:    1,
	}})
	for _, want := range []string{
		`stdin: harvested for estimates`,
		`  estimate_status: available`,
		`  static_call_sites: 1`,
		`  input_frames: 1`,
		`  harvested_judgements: 3`,
		`  post_dedup_judgements: 2`,
		`  mock_judge_batches: 1`,
		`  over_harvest_bound: post_dedup_judgements == mock distinct judgements; may be a safe superset of execute-needed judgements`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("semantic explain missing estimate %q in:\n%s", want, got)
		}
	}
}

func TestSemanticPlanRendersNestedNodeAndOrderedSpecs(t *testing.T) {
	query := `{mood: sem_classify(.text; "bug"; "billing")}`
	semanticPlan := buildPlan(t, query)
	got := explain.String(explain.Plan{Query: query, SemanticPlan: &semanticPlan})
	for _, want := range []string{
		`planned_call_sites: 1`,
		`semantic_values: 1`,
		`    op: "sem_classify"`,
		`    kind: "value"`,
		`    value_expr: ".text"`,
		`    specs: ["bug", "billing"]`,
		`    source_range: 7:44`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("semantic explain missing %q in:\n%s", want, got)
		}
	}
}

func TestSemanticPlanRendersDesugaredInfix(t *testing.T) {
	rewritten, err := desugar.Rewrite(`. =~ "urgent"`)
	if err != nil {
		t.Fatalf("Rewrite returned error: %v", err)
	}
	semanticPlan := buildPlan(t, rewritten)
	got := explain.String(explain.Plan{Query: rewritten, SemanticPlan: &semanticPlan})
	for _, want := range []string{
		`query: "sem_match(\"urgent\")"`,
		`planned_call_sites: 1`,
		`semantic_predicates: 1`,
		`    op: "sem_match"`,
		`    value_expr: "."`,
		`    specs: ["urgent"]`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("desugared explain missing %q in:\n%s", want, got)
		}
	}
}

func TestStringMatchesWrite(t *testing.T) {
	plan := explain.Plan{Query: ".items[] | select(.active) | .id"}
	var buf bytes.Buffer
	if err := explain.Write(&buf, plan); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := explain.String(plan); got != buf.String() {
		t.Fatalf("String() = %q, Write() = %q", got, buf.String())
	}
}

func TestStringMatchesWriteForSemanticPlan(t *testing.T) {
	query := `.items[] | select(sem_match(.feedback; "angry/frustrated")) | {mood: sem_classify(.feedback; "angry"; "happy")}`
	semanticPlan := buildPlan(t, query)
	plan := explain.Plan{Query: query, SemanticPlan: &semanticPlan}
	var buf bytes.Buffer
	if err := explain.Write(&buf, plan); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if got := explain.String(plan); got != buf.String() {
		t.Fatalf("String() = %q, Write() = %q", got, buf.String())
	}
}

func buildPlan(t *testing.T, query string) plan.Plan {
	t.Helper()
	semanticPlan, diagnostics := plan.Build(query)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == plan.SeverityError {
			t.Fatalf("plan.Build(%q) diagnostic: %s", query, diagnostic.Message)
		}
	}
	return semanticPlan
}
