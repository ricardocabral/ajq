package explain_test

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/explain"
)

const wantStreamExplain = "ajq explain v1\n" +
	"query: \"sem_match(.msg; \\\"keep\\\")\"\n" +
	"execution: semantic user-stream inline\n" +
	"deterministic: no\n" +
	"model_calls: input-dependent\n" +
	"backend_calls: input-dependent\n" +
	"byte_reproducible: cache-dependent\n" +
	"stdin: not harvested\n" +
	"planned_call_sites: 1\n" +
	"semantic_predicates: 1\n" +
	"semantic_values: 0\n" +
	"estimates:\n" +
	"  estimate_status: unavailable: user stream\n" +
	"  estimate_reason: \"user-selected inline execution has no cross-frame batching or pre-resolve dedup estimate\"\n" +
	"  static_call_sites: 1\n" +
	"  input_frames: unavailable\n" +
	"  harvested_judgements: unavailable\n" +
	"  post_dedup_judgements: unavailable\n" +
	"  mock_judge_batches: unavailable\n" +
	"  over_harvest_bound: unavailable until input can be harvested\n" +
	"subgraphs:\n" +
	"  deterministic: jq outside semantic call sites\n" +
	"  semantic: 1 planned call site(s)\n" +
	"semantic_plan:\n" +
	"  - call_id: 1\n" +
	"    op: \"sem_match\"\n" +
	"    kind: \"predicate\"\n" +
	"    value_expr: \".msg\"\n" +
	"    specs: [\"keep\"]\n" +
	"    source_range: 0:23\n" +
	"    gated: n/a\n" +
	"    execution: user-stream-inline\n" +
	"    subgraph: semantic\n"

func TestStreamExplainGolden(t *testing.T) {
	query := `sem_match(.msg; "keep")`
	semanticPlan := buildPlan(t, query)
	got := explain.String(explain.Plan{
		Query:        query,
		SemanticPlan: &semanticPlan,
		Stream:       true,
		Estimate: &explain.Estimate{
			Status: explain.EstimateStatusUnavailableUserStream,
			Reason: "user-selected inline execution has no cross-frame batching or pre-resolve dedup estimate",
		},
	})
	if got != wantStreamExplain {
		t.Fatalf("stream explain mismatch\ngot:\n%s\nwant:\n%s", got, wantStreamExplain)
	}
}
