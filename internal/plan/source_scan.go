package plan

import (
	"sort"
	"strings"
)

// semCallOccurrence is one semantic call site located directly in the original
// query text. Start is the byte offset of the first character of the operator
// name; End is the exclusive byte offset just past the matching close paren.
// Text is the exact source slice src[Start:End].
type semCallOccurrence struct {
	Op    string
	Start int
	End   int
	Text  string
}

// scanSemanticCalls performs a jq-aware lexical scan of src and returns every
// semantic (`sem_*`) call site with a concrete byte range. gojq does not expose
// byte offsets on AST nodes, so this scanner recovers them deterministically
// from the source text.
//
// The scanner understands the three jq lexical contexts that can hide or reveal
// a call site: string literals (`"..."`), string interpolation (`\(...)`, which
// contains real query code that may itself hold semantic calls and nested
// strings), and `#` line comments. Identifiers matching `sem_*` that are
// immediately followed by `(` (ignoring whitespace) are treated as call sites,
// and their extent is found by balancing parens within the same code frame.
//
// Occurrences are returned sorted by Start (source order). Nested calls (e.g.
// `sem_a(sem_b("x"))`) yield one occurrence each; duplicate identical calls
// yield distinct occurrences with distinct ranges.
func scanSemanticCalls(src string) []semCallOccurrence {
	// A frame represents either a code context or a string-literal context.
	// Code frames created by string interpolation (`\(`) are marked interp and
	// are closed by the `)` that returns their local paren depth below zero.
	type frame struct {
		isString bool
		interp   bool
		depth    int
		serial   int
	}
	// A pending call site waits for the `)` that balances its opening `(`.
	type pending struct {
		op          string
		identStart  int
		serial      int
		depthAtOpen int
	}

	var occs []semCallOccurrence
	frames := []frame{{}} // top-level code frame, serial 0
	pendings := []pending{}
	serialCounter := 0
	n := len(src)

	for i := 0; i < n; {
		f := &frames[len(frames)-1]

		if f.isString {
			c := src[i]
			switch c {
			case '"':
				frames = frames[:len(frames)-1]
				i++
			case '\\':
				if i+1 < n && src[i+1] == '(' {
					serialCounter++
					frames = append(frames, frame{interp: true, serial: serialCounter})
					i += 2
				} else {
					// Escaped char (\", \\, \n, \uXXXX, ...): skip the backslash
					// and the following byte if present.
					i += 2
				}
			default:
				i++
			}
			continue
		}

		// Code frame.
		c := src[i]
		switch c {
		case '"':
			frames = append(frames, frame{isString: true})
			i++
		case '#':
			for i < n && src[i] != '\n' {
				i++
			}
		case '(':
			f.depth++
			i++
		case ')':
			if f.interp && f.depth == 0 {
				// Close the interpolation code frame.
				frames = frames[:len(frames)-1]
				i++
				continue
			}
			f.depth--
			for len(pendings) > 0 {
				p := pendings[len(pendings)-1]
				if p.serial != f.serial || p.depthAtOpen != f.depth {
					break
				}
				pendings = pendings[:len(pendings)-1]
				occs = append(occs, semCallOccurrence{
					Op:    p.op,
					Start: p.identStart,
					End:   i + 1,
					Text:  src[p.identStart : i+1],
				})
			}
			i++
		default:
			if isIdentStart(c) {
				j := i
				for j < n && isIdentChar(src[j]) {
					j++
				}
				ident := src[i:j]
				k := j
				for k < n && isSpaceByte(src[k]) {
					k++
				}
				if strings.HasPrefix(ident, "sem_") && k < n && src[k] == '(' {
					pendings = append(pendings, pending{
						op:          ident,
						identStart:  i,
						serial:      f.serial,
						depthAtOpen: f.depth,
					})
					i = k // point at '('; next iteration increments frame depth
					continue
				}
				i = j
				continue
			}
			i++
		}
	}

	sort.SliceStable(occs, func(a, b int) bool { return occs[a].Start < occs[b].Start })
	return occs
}

// attachSourceRanges matches planned semantic nodes and diagnostics to scanned
// source occurrences and populates concrete byte ranges. Matching is
// deterministic: pass 1 pairs each node (in planner walk order) with the
// earliest unused occurrence whose op and whitespace-normalized text match;
// pass 2 assigns any remaining node the earliest unused occurrence with the
// same op (order-based fallback). Diagnostics are matched best-effort by
// normalized text against occurrences still unused after nodes are assigned.
func attachSourceRanges(p *Plan, diagnostics []Diagnostic, src string) {
	occs := scanSemanticCalls(src)
	if len(occs) == 0 {
		return
	}
	used := make([]bool, len(occs))

	// Pass 1: op + normalized-text match for nodes.
	for i := range p.Semantic {
		node := &p.Semantic[i]
		want := normalizeSemExpr(node.Source.Expression)
		for j := range occs {
			if used[j] || occs[j].Op != node.Op {
				continue
			}
			if normalizeSemExpr(occs[j].Text) == want {
				used[j] = true
				setSourceRange(&node.Source, occs[j])
				break
			}
		}
	}
	// Pass 2: order-based fallback per op for still-unmatched nodes.
	for i := range p.Semantic {
		node := &p.Semantic[i]
		if node.Source.HasRange {
			continue
		}
		for j := range occs {
			if used[j] || occs[j].Op != node.Op {
				continue
			}
			used[j] = true
			setSourceRange(&node.Source, occs[j])
			break
		}
	}

	// Best-effort ranges for diagnostics using remaining occurrences.
	for i := range diagnostics {
		d := &diagnostics[i]
		if d.Source.HasRange || d.Source.Expression == "" {
			continue
		}
		want := normalizeSemExpr(d.Source.Expression)
		for j := range occs {
			if used[j] {
				continue
			}
			if occs[j].Op == d.Op && normalizeSemExpr(occs[j].Text) == want {
				used[j] = true
				setSourceRange(&d.Source, occs[j])
				break
			}
		}
	}
}

func setSourceRange(s *Source, occ semCallOccurrence) {
	s.StartByte = occ.Start
	s.EndByte = occ.End
	s.HasRange = true
	// Keep Expression equal to the exact matched query slice so it aligns with
	// the reported range.
	s.Expression = occ.Text
}

// normalizeSemExpr strips all whitespace so source slices (which may contain
// arbitrary spacing) compare equal to gojq's canonical String() rendering.
func normalizeSemExpr(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

func isSpaceByte(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	default:
		return false
	}
}
