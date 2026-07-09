package plan

import (
	"strings"
	"testing"
)

// assertConcreteRange verifies a Source carries a valid byte range that slices
// into query and whose sliced text matches the recorded Expression (modulo
// whitespace normalization).
func assertConcreteRange(t *testing.T, query string, s Source) {
	t.Helper()
	if !s.HasRange {
		t.Fatalf("source has no range: %#v", s)
	}
	if s.StartByte < 0 || s.EndByte > len(query) || s.StartByte >= s.EndByte {
		t.Fatalf("source range out of bounds for query %q: %#v", query, s)
	}
	slice := query[s.StartByte:s.EndByte]
	if normalizeSemExpr(slice) != normalizeSemExpr(s.Expression) {
		t.Fatalf("range slice %q does not match expression %q (query %q, source %#v)", slice, s.Expression, query, s)
	}
	if !strings.HasPrefix(slice, "sem_") {
		t.Fatalf("range slice %q does not start at a sem_ call (query %q)", slice, query)
	}
}

func TestScanSemanticCallsBasic(t *testing.T) {
	src := `sem_match(.a; "x")`
	occs := scanSemanticCalls(src)
	if len(occs) != 1 {
		t.Fatalf("occurrences = %#v, want 1", occs)
	}
	occ := occs[0]
	if occ.Op != "sem_match" || occ.Start != 0 || occ.End != len(src) || occ.Text != src {
		t.Fatalf("occurrence = %#v, want full-source sem_match", occ)
	}
}

func TestScanSemanticCallsIgnoresStringsAndComments(t *testing.T) {
	// A sem_match token that lives inside a string literal or a comment must
	// not be picked up as a call site.
	src := `"sem_match(nope)" | sem_norm(.a; "keep") # sem_extract(.b; "c")`
	occs := scanSemanticCalls(src)
	if len(occs) != 1 {
		t.Fatalf("occurrences = %#v, want exactly the sem_norm call", occs)
	}
	if occs[0].Op != "sem_norm" || src[occs[0].Start:occs[0].End] != `sem_norm(.a; "keep")` {
		t.Fatalf("occurrence = %#v, want sem_norm(.a; \"keep\")", occs[0])
	}
}

func TestScanSemanticCallsInterpolation(t *testing.T) {
	// The call lives inside string interpolation; the scanner must descend into
	// \( ... ) and locate it.
	src := `"prefix \(sem_match(.a; "keep")) suffix"`
	occs := scanSemanticCalls(src)
	if len(occs) != 1 {
		t.Fatalf("occurrences = %#v, want 1 in interpolation", occs)
	}
	if got := src[occs[0].Start:occs[0].End]; got != `sem_match(.a; "keep")` {
		t.Fatalf("interpolated occurrence text = %q", got)
	}
}

func TestScanSemanticCallsNested(t *testing.T) {
	src := `sem_match(sem_extract(.a; "k"); "outer")`
	occs := scanSemanticCalls(src)
	if len(occs) != 2 {
		t.Fatalf("occurrences = %#v, want 2 (outer+inner)", occs)
	}
	// Sorted by start: outer first, inner second.
	if occs[0].Op != "sem_match" || occs[0].Start != 0 || occs[0].End != len(src) {
		t.Fatalf("outer occurrence = %#v", occs[0])
	}
	if occs[1].Op != "sem_extract" || src[occs[1].Start:occs[1].End] != `sem_extract(.a; "k")` {
		t.Fatalf("inner occurrence = %#v", occs[1])
	}
}

func TestScanSemanticCallsDuplicatesDistinctRanges(t *testing.T) {
	src := `sem_match("x"), sem_match("x")`
	occs := scanSemanticCalls(src)
	if len(occs) != 2 {
		t.Fatalf("occurrences = %#v, want 2 duplicates", occs)
	}
	if occs[0].Start >= occs[1].Start {
		t.Fatalf("duplicate occurrences not in source order: %#v", occs)
	}
	if occs[0].Start == occs[1].Start || occs[0].End == occs[1].End {
		t.Fatalf("duplicate occurrences share a range: %#v", occs)
	}
}
