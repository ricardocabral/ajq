package plan

import (
	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// IterativePlan describes the deliberately small semantic predicate pipeline
// accepted by the internal iterative-harvest prototype. Stages are in jq pipe
// order and refer to the normal planner CallIDs.
type IterativePlan struct {
	// Stages is AST-backed gate metadata in jq pipe order. Keeping the call ID
	// with its planned operation prevents the executor from inferring stage
	// shape from source text or planner walk order alone.
	Stages []IterativeStage
}

// IterativeStage identifies one admitted select gate.
type IterativeStage struct {
	CallID CallID
	Op     string
	Source Source
}

// IterativeStages recognizes the prototype's safe linear predicate corpus from
// gojq's parsed AST. It deliberately returns false rather than trying to
// partially interpret jq control flow; callers must use the normal executor
// when it does not recognize the whole query.
func IterativeStages(src string, semantic Plan) (IterativePlan, bool) {
	q, err := gojq.Parse(src)
	if err != nil || q == nil || q.Meta != nil || len(q.Imports) != 0 || len(q.FuncDefs) != 0 || len(semantic.Semantic) == 0 {
		return IterativePlan{}, false
	}
	parts := flattenPipe(q)
	if len(parts) < 3 || !prototypePrefix(parts[0]) || !prototypeTerminal(parts[len(parts)-1]) {
		return IterativePlan{}, false
	}
	stages := make([]IterativeStage, 0, len(parts)-2)
	next := 0
	for _, part := range parts[1 : len(parts)-1] {
		id, ok := prototypeGate(part, semantic.Semantic, &next)
		if !ok {
			return IterativePlan{}, false
		}
		node := semantic.Semantic[next-1]
		stages = append(stages, IterativeStage{CallID: id, Op: node.Op, Source: node.Source})
	}
	if len(stages) == 0 || next != len(semantic.Semantic) {
		return IterativePlan{}, false
	}
	return IterativePlan{Stages: stages}, true
}

func flattenPipe(q *gojq.Query) []*gojq.Query {
	if q != nil && q.Op == gojq.OpPipe && len(q.Patterns) == 0 && q.Term == nil {
		return append(flattenPipe(q.Left), flattenPipe(q.Right)...)
	}
	return []*gojq.Query{q}
}

func singleTerm(q *gojq.Query) *gojq.Term {
	if q == nil || q.Term == nil || q.Left != nil || q.Right != nil || q.Op != 0 || len(q.Patterns) != 0 || len(q.FuncDefs) != 0 || q.Meta != nil || len(q.Imports) != 0 {
		return nil
	}
	return q.Term
}

func prototypePrefix(q *gojq.Query) bool {
	t := singleTerm(q)
	if t == nil || (t.Type != gojq.TermTypeIdentity && (t.Type != gojq.TermTypeIndex || !literalField(t.Index))) {
		return false
	}
	// A bare identity is the safe NDJSON counterpart to a single trailing []
	// iterator; both produce one stable candidate per input frame/value.
	if t.Type == gojq.TermTypeIdentity && len(t.SuffixList) == 0 {
		return true
	}
	if len(t.SuffixList) == 0 {
		return false
	}
	iter := 0
	for i, suffix := range t.SuffixList {
		if suffix == nil || suffix.Optional {
			return false
		}
		if suffix.Iter {
			iter++
			if iter != 1 || i != len(t.SuffixList)-1 {
				return false
			}
			continue
		}
		if suffix.Index == nil || !literalField(suffix.Index) {
			return false
		}
	}
	return iter == 1
}

func prototypeTerminal(q *gojq.Query) bool {
	t := singleTerm(q)
	if t == nil || (t.Type != gojq.TermTypeIdentity && (t.Type != gojq.TermTypeIndex || !literalField(t.Index))) {
		return false
	}
	for _, suffix := range t.SuffixList {
		if suffix == nil || suffix.Optional || suffix.Iter || suffix.Index == nil || !literalField(suffix.Index) {
			return false
		}
	}
	return true
}

func literalField(index *gojq.Index) bool {
	return index != nil && !index.IsSlice && index.Name != "" && index.Str == nil && index.Start == nil && index.End == nil
}

func prototypeGate(q *gojq.Query, nodes []SemNode, next *int) (CallID, bool) {
	t := singleTerm(q)
	if t == nil || t.Type != gojq.TermTypeFunc || t.Func == nil || t.Func.Name != "select" || len(t.Func.Args) != 1 || len(t.SuffixList) != 0 {
		return 0, false
	}
	gate := t.Func.Args[0]
	if id, ok := prototypeMatch(gate, nodes, next); ok {
		return id, true
	}
	return prototypeClassify(gate, nodes, next)
}

func prototypeMatch(q *gojq.Query, nodes []SemNode, next *int) (CallID, bool) {
	t := singleTerm(q)
	if t == nil || t.Type != gojq.TermTypeFunc || t.Func == nil || t.Func.Name != "sem_match" || *next >= len(nodes) {
		return 0, false
	}
	node := nodes[*next]
	if node.Op != "sem_match" || node.Kind != semantics.KindPredicate || !prototypeSemanticValue(t.Func.Args, node) {
		return 0, false
	}
	*next++
	return node.ID, true
}

func prototypeClassify(q *gojq.Query, nodes []SemNode, next *int) (CallID, bool) {
	if q == nil || q.Op != gojq.OpEq || q.Term != nil || q.Left == nil || q.Right == nil || *next >= len(nodes) {
		return 0, false
	}
	var call, label *gojq.Query
	if isClassify(q.Left) && literalString(q.Right) != "" {
		call, label = q.Left, q.Right
	} else if isClassify(q.Right) && literalString(q.Left) != "" {
		call, label = q.Right, q.Left
	} else {
		return 0, false
	}
	node := nodes[*next]
	if node.Op != "sem_classify" || len(node.Specs) < 2 || !prototypeSemanticValue(singleTerm(call).Func.Args, node) {
		return 0, false
	}
	found := false
	for _, spec := range node.Specs {
		found = found || spec == literalString(label)
	}
	if !found {
		return 0, false
	}
	*next++
	return node.ID, true
}

func isClassify(q *gojq.Query) bool {
	t := singleTerm(q)
	return t != nil && t.Type == gojq.TermTypeFunc && t.Func != nil && t.Func.Name == "sem_classify"
}

func prototypeSemanticValue(args []*gojq.Query, node SemNode) bool {
	if len(args) != node.Arity || len(node.Specs) == 0 {
		return false
	}
	for _, arg := range args[len(args)-len(node.Specs):] {
		if literalString(arg) == "" {
			return false
		}
	}
	if node.Arity == len(node.Specs) {
		return true
	}
	return stableValue(args[0])
}

func stableValue(q *gojq.Query) bool {
	t := singleTerm(q)
	if t == nil || (t.Type != gojq.TermTypeIdentity && (t.Type != gojq.TermTypeIndex || !literalField(t.Index))) {
		return false
	}
	for _, s := range t.SuffixList {
		if s == nil || s.Optional || s.Iter || s.Index == nil || !literalField(s.Index) {
			return false
		}
	}
	return true
}

func literalString(q *gojq.Query) string {
	t := singleTerm(q)
	if t == nil || t.Type != gojq.TermTypeString || t.Str == nil || t.Str.Queries != nil || len(t.SuffixList) != 0 {
		return ""
	}
	return t.Str.Str
}
