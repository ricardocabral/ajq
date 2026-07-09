package plan

import (
	"slices"
	"testing"

	"github.com/ricardocabral/ajq/internal/semantics"
)

func TestBuildImplicitAndExplicitSemanticNodes(t *testing.T) {
	tests := []struct {
		query     string
		op        string
		kind      semantics.Kind
		ret       semantics.ReturnType
		valueExpr string
		specs     []string
	}{
		{`sem_match("angry")`, "sem_match", semantics.KindPredicate, semantics.ReturnBool, ".", []string{"angry"}},
		{`sem_match(.feedback; "angry")`, "sem_match", semantics.KindPredicate, semantics.ReturnBool, ".feedback", []string{"angry"}},
		{`sem_classify("bug"; "billing")`, "sem_classify", semantics.KindValue, semantics.ReturnString, ".", []string{"bug", "billing"}},
		{`sem_classify(.text; "bug"; "billing")`, "sem_classify", semantics.KindValue, semantics.ReturnString, ".text", []string{"bug", "billing"}},
		{`sem_extract(.text; "case id")`, "sem_extract", semantics.KindValue, semantics.ReturnString, ".text", []string{"case id"}},
		{`sem_score(.text; "severity")`, "sem_score", semantics.KindValue, semantics.ReturnNumber, ".text", []string{"severity"}},
		{`sem_norm(.name; "company")`, "sem_norm", semantics.KindValue, semantics.ReturnString, ".name", []string{"company"}},
		{`sem_redact(.body; "pii")`, "sem_redact", semantics.KindValue, semantics.ReturnString, ".body", []string{"pii"}},
	}
	for _, tt := range tests {
		plan, diags := Build(tt.query)
		if len(diags) != 0 {
			t.Fatalf("Build(%q) diagnostics = %#v", tt.query, diags)
		}
		if plan.Deterministic {
			t.Fatalf("Build(%q) deterministic = true", tt.query)
		}
		if len(plan.Semantic) != 1 {
			t.Fatalf("Build(%q) semantic count = %d", tt.query, len(plan.Semantic))
		}
		node := plan.Semantic[0]
		if node.Op != tt.op || node.Kind != tt.kind || node.Return != tt.ret || node.ValueExpr != tt.valueExpr || !slices.Equal(node.Specs, tt.specs) {
			t.Fatalf("Build(%q) node = %#v", tt.query, node)
		}
		if node.Source.Expression == "" {
			t.Fatalf("Build(%q) source = %#v", tt.query, node.Source)
		}
		assertConcreteRange(t, tt.query, node.Source)
	}
}

func TestBuildDiagnostics(t *testing.T) {
	tests := []struct {
		name  string
		query string
		code  DiagnosticCode
	}{
		{"unknown semantic op", `sem_unknown(.x; "spec")`, DiagnosticUnknownSemOp},
		{"wrong arity", `sem_match(.x; "a"; "b")`, DiagnosticArity},
		{"non literal spec", `sem_match(.x; .spec)`, DiagnosticNonLiteralSpec},
		{"classify non literal label", `sem_classify("a"; .label)`, DiagnosticNonLiteralSpec},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, diags := Build(tt.query)
			if plan.Deterministic {
				t.Fatalf("Build(%q) deterministic = true", tt.query)
			}
			if len(plan.Semantic) != 0 {
				t.Fatalf("Build(%q) semantic nodes = %#v", tt.query, plan.Semantic)
			}
			if len(diags) != 1 {
				t.Fatalf("Build(%q) diagnostics = %#v", tt.query, diags)
			}
			if diags[0].Code != tt.code || diags[0].Severity != SeverityError {
				t.Fatalf("Build(%q) diagnostic = %#v", tt.query, diags[0])
			}
			if diags[0].Source.Expression == "" {
				t.Fatalf("Build(%q) diagnostic source = %#v", tt.query, diags[0].Source)
			}
			// Every diagnostic in this table is an ordinary sem_* call site, so a
			// best-effort byte range must be recovered.
			assertConcreteRange(t, tt.query, diags[0].Source)
		})
	}
}

func TestBuildPartialPlanAndSummary(t *testing.T) {
	plan, diags := Build(`sem_match(.feedback; "angry"), sem_unknown(.x; "y"), sem_norm(.name; "company")`)
	if len(diags) != 1 || diags[0].Code != DiagnosticUnknownSemOp {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(plan.Semantic) != 2 {
		t.Fatalf("semantic nodes = %#v", plan.Semantic)
	}
	if plan.Deterministic || plan.Summary.Deterministic {
		t.Fatalf("summary deterministic = plan %v summary %v", plan.Deterministic, plan.Summary.Deterministic)
	}
	if plan.Summary.SemanticCount != 2 || plan.Summary.PredicateCount != 1 || plan.Summary.ValueCount != 1 || plan.Summary.ModelCallsEstimate != 2 {
		t.Fatalf("summary = %#v", plan.Summary)
	}
}

func TestBuildDeterministicQuerySummary(t *testing.T) {
	plan, diags := Build(`.users[] | .id`)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if !plan.Deterministic || !plan.Summary.Deterministic {
		t.Fatalf("deterministic summary = plan %v summary %v", plan.Deterministic, plan.Summary.Deterministic)
	}
	if len(plan.Semantic) != 0 || plan.Summary.SemanticCount != 0 || plan.Summary.ModelCallsEstimate != 0 {
		t.Fatalf("semantic summary = nodes %#v summary %#v", plan.Semantic, plan.Summary)
	}
}
