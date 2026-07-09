package plan

import "testing"

func TestWalkerCoverage(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  int
	}{
		{"function select arg", `select(sem_match("x"))`, 1},
		{"function sort group args", `sort_by(sem_score("rank")) | group_by(sem_classify("a"; "b"))`, 2},
		{"object value", `{mood: sem_classify("happy"; "sad")}`, 1},
		{"object key query", `{(sem_extract("key")): .value}`, 1},
		{"object key string interpolation", `{"\(sem_extract("key"))": .value}`, 1},
		{"array element", `[sem_norm("lowercase")]`, 1},
		{"if elif else", `if sem_match("x") then sem_norm("a") elif sem_match("y") then sem_redact("b") else sem_extract("c") end`, 5},
		{"try catch", `try sem_match("x") catch sem_match("y")`, 2},
		{"reduce clauses", `reduce sem_extract("item") as $x (sem_norm("start"); sem_redact("update"))`, 3},
		{"foreach clauses", `foreach sem_extract("item") as $x (sem_norm("start"); sem_redact("update"); sem_score("out"))`, 4},
		{"string interpolation", `"\(sem_match("x"))"`, 1},
		{"format string interpolation", `@uri "\(sem_match("x"))"`, 1},
		{"index expression", `.[sem_extract("key")]`, 1},
		{"slice expressions", `.[sem_score("start"):sem_score("end")]`, 2},
		{"suffix index position", `.items[sem_extract("key")]?`, 1},
		{"as destructuring pattern key query", `. as {(sem_match("x")): $v} | $v`, 1},
		{"label body", `label $out | sem_match("x")`, 1},
		{"user defined function body", `def tagged: sem_match("x"); tagged`, 1},
		{"reduce destructuring pattern", `reduce .[] as {(sem_match("x")): $v} (0; .)`, 1},
		{"foreach destructuring pattern", `foreach .[] as {(sem_match("x")): $v} (0; .; .)`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, diags := Build(tt.query)
			if len(diags) != 0 {
				t.Fatalf("Build diagnostics = %#v", diags)
			}
			if len(plan.Semantic) != tt.want {
				t.Fatalf("semantic count = %d, want %d; nodes %#v", len(plan.Semantic), tt.want, plan.Semantic)
			}
			for i, node := range plan.Semantic {
				if node.Source.Expression == "" {
					t.Fatalf("node %d has empty source: %#v", i, node)
				}
				assertConcreteRange(t, tt.query, node.Source)
			}
		})
	}
}

func TestWalkerUnsupportedDiagnostics(t *testing.T) {
	_, diags := Build(`import "math" as math; .`)
	if len(diags) != 1 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if diags[0].Code != DiagnosticUnsupported || diags[0].Severity != SeverityError {
		t.Fatalf("diagnostic = %#v", diags[0])
	}
	// Import diagnostics are not sem_* call sites, so no concrete range is
	// recovered.
	if diags[0].Source.Expression == "" || diags[0].Source.HasRange {
		t.Fatalf("diagnostic source = %#v", diags[0].Source)
	}
}
