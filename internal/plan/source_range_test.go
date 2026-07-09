package plan

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/desugar"
)

// TestBuildAttachesDistinctRangesForDuplicateCalls verifies duplicate identical
// semantic calls receive deterministic, distinct byte ranges assigned in
// planner walk / source order.
func TestBuildAttachesDistinctRangesForDuplicateCalls(t *testing.T) {
	query := `sem_match(.a; "x"), sem_match(.a; "x")`
	p, diags := Build(query)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(p.Semantic) != 2 {
		t.Fatalf("semantic nodes = %#v, want 2", p.Semantic)
	}
	for i, node := range p.Semantic {
		assertConcreteRange(t, query, node.Source)
		if i > 0 {
			prev := p.Semantic[i-1].Source
			if prev.StartByte == node.Source.StartByte || prev.EndByte == node.Source.EndByte {
				t.Fatalf("duplicate nodes share a range: %#v and %#v", prev, node.Source)
			}
			if prev.StartByte >= node.Source.StartByte {
				t.Fatalf("duplicate ranges not assigned in source order: %#v then %#v", prev, node.Source)
			}
		}
	}
}

// TestBuildAttachesRangesInNestedObjectConstructor covers nested object
// constructors, ensuring each value's semantic call gets its own range.
func TestBuildAttachesRangesInNestedObjectConstructor(t *testing.T) {
	query := `{outer: {mood: sem_classify(.text; "a"; "b"), tag: sem_extract(.text; "id")}}`
	p, diags := Build(query)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(p.Semantic) != 2 {
		t.Fatalf("semantic nodes = %#v, want 2", p.Semantic)
	}
	for _, node := range p.Semantic {
		assertConcreteRange(t, query, node.Source)
	}
}

// TestBuildAttachesRangeInStringInterpolation ensures a semantic call embedded
// in string interpolation gets a range pointing into the interpolation body.
func TestBuildAttachesRangeInStringInterpolation(t *testing.T) {
	query := `"mood=\(sem_classify(.text; "a"; "b"))"`
	p, diags := Build(query)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(p.Semantic) != 1 {
		t.Fatalf("semantic nodes = %#v, want 1", p.Semantic)
	}
	assertConcreteRange(t, query, p.Semantic[0].Source)
	if got := query[p.Semantic[0].Source.StartByte:p.Semantic[0].Source.EndByte]; got != `sem_classify(.text; "a"; "b")` {
		t.Fatalf("interpolated range slice = %q", got)
	}
}

// TestBuildAttachesRangeForDesugaredMatch covers the ajq `=~` infix operator,
// which desugars to sem_match before planning; the recovered range must point
// into the rewritten source.
func TestBuildAttachesRangeForDesugaredMatch(t *testing.T) {
	rewritten, err := desugar.Rewrite(`. =~ "urgent"`)
	if err != nil {
		t.Fatalf("Rewrite: %v", err)
	}
	p, diags := Build(rewritten)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(p.Semantic) != 1 {
		t.Fatalf("semantic nodes = %#v, want 1 for %q", p.Semantic, rewritten)
	}
	assertConcreteRange(t, rewritten, p.Semantic[0].Source)
	if p.Semantic[0].Op != "sem_match" {
		t.Fatalf("op = %q, want sem_match", p.Semantic[0].Op)
	}
}

// TestBuildAttachesRangesInUserDefinedFunction ensures semantic calls inside a
// user-defined function body get ranges that slice into the def body.
func TestBuildAttachesRangesInUserDefinedFunction(t *testing.T) {
	query := `def tagged: sem_match(.msg; "keep"); .items[] | select(tagged)`
	p, diags := Build(query)
	if len(diags) != 0 {
		t.Fatalf("diagnostics = %#v", diags)
	}
	if len(p.Semantic) != 1 {
		t.Fatalf("semantic nodes = %#v, want 1", p.Semantic)
	}
	assertConcreteRange(t, query, p.Semantic[0].Source)
	if got := query[p.Semantic[0].Source.StartByte:p.Semantic[0].Source.EndByte]; got != `sem_match(.msg; "keep")` {
		t.Fatalf("function-body range slice = %q", got)
	}
}

// TestBuildAttachesRangesToDiagnostics verifies best-effort ranges are attached
// to non-literal-spec and arity diagnostics.
func TestBuildAttachesRangesToDiagnostics(t *testing.T) {
	tests := []struct {
		name  string
		query string
		code  DiagnosticCode
	}{
		{"non literal spec", `sem_match(.x; .spec)`, DiagnosticNonLiteralSpec},
		{"arity", `sem_match(.x; "a"; "b")`, DiagnosticArity},
		{"unknown op", `sem_unknown(.x; "y")`, DiagnosticUnknownSemOp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, diags := Build(tt.query)
			if len(diags) != 1 || diags[0].Code != tt.code {
				t.Fatalf("diagnostics = %#v", diags)
			}
			assertConcreteRange(t, tt.query, diags[0].Source)
		})
	}
}
