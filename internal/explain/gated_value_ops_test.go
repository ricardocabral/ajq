package explain_test

import (
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/explain"
)

func TestGatedValueOpExplainReportsExecutionModes(t *testing.T) {
	query := `.[] | select(sem_score(.review; "positivity") > 0.8)`
	semanticPlan := buildPlan(t, query)
	got := explain.String(explain.Plan{Query: query, SemanticPlan: &semanticPlan})
	for _, want := range []string{
		`execution: semantic interleaved fallback`,
		`gated: yes`,
		`execution: interleaved-fallback`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("explain missing %q in:\n%s", want, got)
		}
	}

	classifyQuery := `.[] | select(sem_classify(.msg; "a"; "b") == "a")`
	classifyPlan := buildPlan(t, classifyQuery)
	got = explain.String(explain.Plan{Query: classifyQuery, SemanticPlan: &classifyPlan})
	for _, want := range []string{
		`gated: yes`,
		`execution: 3-phase-generator-superset`,
	} {
		if !strings.Contains(got, want+"\n") {
			t.Fatalf("classify explain missing %q in:\n%s", want, got)
		}
	}
}
