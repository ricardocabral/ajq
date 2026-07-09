package plan

import (
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// Build parses src and returns a partial semantic Plan plus any plan-time
// diagnostics. Any error-severity diagnostic means callers must reject the plan.
func Build(src string) (Plan, []Diagnostic) {
	instrumented, diagnostics := BuildInstrumented(src)
	return instrumented.Plan, diagnostics
}

// BuildInstrumented parses src once, walks that AST once, and returns both the
// semantic plan and the query generated from the same walk with semantic calls
// rewritten to unique internal callbacks.
func BuildInstrumented(src string) (InstrumentedPlan, []Diagnostic) {
	p := Plan{Query: src, Deterministic: true, Summary: Summary{Deterministic: true}}
	query, err := gojq.Parse(src)
	if err != nil {
		return InstrumentedPlan{Plan: p, InstrumentedQuery: src}, []Diagnostic{errorDiagnostic(DiagnosticParseError, "", src, fmt.Sprintf("parse query: %v", err))}
	}

	b := builder{plan: p, instrument: true, funcGates: map[string]bool{}, funcSemNodes: map[string][]int{}, funcCalls: map[string][]string{}, gatedFuncs: map[string]bool{}}
	b.walkQuery(query)
	b.applyFunctionGatedMarks()
	b.finish()
	// Recover concrete byte ranges from the original source text. This must run
	// on src (not the instrumented query.String()) so ranges point at the query
	// the user wrote / that explain renders.
	attachSourceRanges(&b.plan, b.diagnostics, src)
	return InstrumentedPlan{Plan: b.plan, InstrumentedQuery: query.String()}, b.diagnostics
}

const internalSemanticPrefix = "__ajq_sem_"

type builder struct {
	plan         Plan
	diagnostics  []Diagnostic
	seenSem      bool
	instrument   bool
	nextCallID   CallID
	gateDepth    int
	funcDefDepth int
	funcStack    []string
	funcGates    map[string]bool
	funcSemNodes map[string][]int
	funcCalls    map[string][]string
	gatedFuncs   map[string]bool
}

func (b *builder) finish() {
	b.plan.Deterministic = !b.seenSem
	b.plan.Summary.SemanticCount = len(b.plan.Semantic)
	b.plan.Summary.Deterministic = b.plan.Deterministic
	b.plan.Summary.ModelCallsEstimate = len(b.plan.Semantic)
	for _, node := range b.plan.Semantic {
		if node.ExecutionMode == ExecutionModeInterleavedFallback {
			b.plan.RequiresInterleaved = true
		}
		switch node.Kind {
		case semantics.KindPredicate:
			b.plan.Summary.PredicateCount++
		case semantics.KindValue:
			b.plan.Summary.ValueCount++
		}
	}
}

func (b *builder) addDiagnostic(d Diagnostic) {
	b.diagnostics = append(b.diagnostics, d)
}

func (b *builder) inGate() bool {
	return b.gateDepth > 0
}

func (b *builder) walkGateQuery(q *gojq.Query) {
	b.gateDepth++
	b.walkQuery(q)
	b.gateDepth--
}

func (b *builder) walkOutputGateQuery(q *gojq.Query) {
	b.walkOutputGateQueryDemand(q, "", false)
}

func (b *builder) walkOutputGateQueryDemand(q *gojq.Query, field string, hasField bool) {
	if q == nil {
		return
	}
	if q.Op == gojq.OpPipe {
		b.walkQuery(q.Left)
		b.walkOutputGateQueryDemand(q.Right, field, hasField)
		return
	}
	if hasField && b.walkObjectFieldUnderGate(q, field) {
		return
	}
	b.walkGateQuery(q)
}

func (b *builder) walkObjectFieldUnderGate(q *gojq.Query, field string) bool {
	if q == nil || q.Op != 0 || q.Term == nil || q.Term.Object == nil || q.Left != nil || q.Right != nil {
		return false
	}
	for _, kv := range q.Term.Object.KeyVals {
		if kv == nil {
			continue
		}
		b.walkString(kv.KeyString)
		b.walkQuery(kv.KeyQuery)
		if kv.Key == field {
			b.walkGateQuery(kv.Val)
		} else {
			b.walkQuery(kv.Val)
		}
	}
	return true
}

func (b *builder) queryContainsGate(q *gojq.Query) bool {
	return queryContainsGateWithFuncs(q, b.funcGates)
}

func (b *builder) pipeGateDemand(q *gojq.Query) (bool, string, bool) {
	return pipeGateDemandWithFuncs(q, b.funcGates)
}

func (b *builder) refreshFunctionGateSummaries(defs []*gojq.FuncDef) {
	if len(defs) == 0 {
		return
	}
	if b.funcGates == nil {
		b.funcGates = map[string]bool{}
	}
	changed := true
	for changed {
		changed = false
		for _, fd := range defs {
			if fd == nil {
				continue
			}
			gated := queryContainsGateWithFuncs(fd.Body, b.funcGates)
			if b.funcGates[fd.Name] != gated {
				b.funcGates[fd.Name] = gated
				changed = true
			}
		}
	}
}

func (b *builder) markFunctionValueNodesGated(name string, seen map[string]bool) {
	if b.gatedFuncs == nil {
		b.gatedFuncs = map[string]bool{}
	}
	b.gatedFuncs[name] = true
	if seen[name] {
		return
	}
	seen[name] = true
	for _, idx := range b.funcSemNodes[name] {
		node := &b.plan.Semantic[idx]
		if node.Kind != semantics.KindValue || node.Gated {
			continue
		}
		node.Gated = true
		node.ExecutionMode = executionModeFor(semantics.OpSpec{Name: node.Op, Kind: node.Kind}, true)
	}
	for _, callee := range b.funcCalls[name] {
		b.markFunctionValueNodesGated(callee, seen)
	}
}

func (b *builder) applyFunctionGatedMarks() {
	for name := range b.gatedFuncs {
		b.markFunctionValueNodesGated(name, map[string]bool{})
	}
}

func isComparisonOp(op gojq.Operator) bool {
	switch op {
	case gojq.OpEq, gojq.OpNe, gojq.OpGt, gojq.OpLt, gojq.OpGe, gojq.OpLe:
		return true
	default:
		return false
	}
}

func isIdentityQuery(q *gojq.Query) bool {
	return q != nil && q.Op == 0 && q.Left == nil && q.Right == nil && q.Term != nil && q.Term.Type == gojq.TermTypeIdentity && len(q.Term.SuffixList) == 0
}

func fieldProjectionName(q *gojq.Query) (string, bool) {
	if q == nil || q.Op != 0 || q.Left != nil || q.Right != nil || q.Term == nil {
		return "", false
	}
	s := q.String()
	if len(s) < 2 || s[0] != '.' {
		return "", false
	}
	for _, r := range s[1:] {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return "", false
	}
	return s[1:], true
}

func executionModeFor(op semantics.OpSpec, gated bool) ExecutionMode {
	if op.Kind != semantics.KindValue {
		return ExecutionModeThreePhase
	}
	if !gated {
		return ExecutionModeThreePhase
	}
	if op.Name == "sem_classify" {
		return ExecutionModeGeneratorSuperset
	}
	return ExecutionModeInterleavedFallback
}

func pipeGateDemandWithFuncs(q *gojq.Query, funcGates map[string]bool) (bool, string, bool) {
	if q == nil {
		return false, "", false
	}
	if field, ok := fieldDemandFromGate(q); ok {
		return true, field, true
	}
	if q.Op == gojq.OpPipe {
		if field, ok := fieldProjectionName(q.Left); ok && queryConsumesInputInGateWithFuncs(q.Right, funcGates) {
			return true, field, true
		}
		if isIdentityQuery(q.Left) {
			return pipeGateDemandWithFuncs(q.Right, funcGates)
		}
		if queryConsumesInputInGateWithFuncs(q.Right, funcGates) {
			return true, "", false
		}
		return false, "", false
	}
	if queryConsumesInputInGateWithFuncs(q, funcGates) {
		return true, "", false
	}
	return false, "", false
}

func fieldDemandFromGate(q *gojq.Query) (string, bool) {
	if q == nil {
		return "", false
	}
	if isComparisonOp(q.Op) {
		if field, ok := fieldProjectionName(q.Left); ok {
			return field, true
		}
		if field, ok := fieldProjectionName(q.Right); ok {
			return field, true
		}
	}
	if q.Term != nil {
		if q.Term.Type == gojq.TermTypeQuery {
			return fieldDemandFromGate(q.Term.Query)
		}
		if q.Term.Func != nil && q.Term.Func.Name == "select" && len(q.Term.Func.Args) > 0 {
			return fieldDemandFromGate(q.Term.Func.Args[0])
		}
		if q.Term.If != nil {
			return fieldDemandFromGate(q.Term.If.Cond)
		}
	}
	return "", false
}

func queryConsumesInputInGateWithFuncs(q *gojq.Query, funcGates map[string]bool) bool {
	if q == nil {
		return false
	}
	if isComparisonOp(q.Op) {
		return true
	}
	if q.Op == gojq.OpPipe {
		return queryConsumesInputInGateWithFuncs(q.Right, funcGates)
	}
	if q.Term != nil {
		if q.Term.Type == gojq.TermTypeIf || q.Term.If != nil {
			return true
		}
		if q.Term.Type == gojq.TermTypeQuery {
			return queryConsumesInputInGateWithFuncs(q.Term.Query, funcGates)
		}
		if q.Term.Func != nil && (q.Term.Func.Name == "select" || funcGates[q.Term.Func.Name]) {
			return true
		}
	}
	return false
}

func queryContainsGateWithFuncs(q *gojq.Query, funcGates map[string]bool) bool {
	if q == nil {
		return false
	}
	if isComparisonOp(q.Op) {
		return true
	}
	if termContainsGate(q.Term, funcGates) || queryContainsGateWithFuncs(q.Left, funcGates) || queryContainsGateWithFuncs(q.Right, funcGates) {
		return true
	}
	for _, pattern := range q.Patterns {
		if patternContainsGate(pattern, funcGates) {
			return true
		}
	}
	return false
}

func termContainsGate(t *gojq.Term, funcGates map[string]bool) bool {
	if t == nil {
		return false
	}
	if t.Func != nil {
		if t.Func.Name == "select" || funcGates[t.Func.Name] {
			return true
		}
		for _, arg := range t.Func.Args {
			if queryContainsGateWithFuncs(arg, funcGates) {
				return true
			}
		}
	}
	if t.If != nil {
		return true
	}
	if t.Object != nil {
		for _, kv := range t.Object.KeyVals {
			if kv == nil {
				continue
			}
			if stringContainsGate(kv.KeyString, funcGates) || queryContainsGateWithFuncs(kv.KeyQuery, funcGates) || queryContainsGateWithFuncs(kv.Val, funcGates) {
				return true
			}
		}
	}
	if t.Array != nil && queryContainsGateWithFuncs(t.Array.Query, funcGates) {
		return true
	}
	if t.Unary != nil && termContainsGate(t.Unary.Term, funcGates) {
		return true
	}
	if t.Query != nil && queryContainsGateWithFuncs(t.Query, funcGates) {
		return true
	}
	if stringContainsGate(t.Str, funcGates) || indexContainsGate(t.Index, funcGates) {
		return true
	}
	if t.Try != nil && (queryContainsGateWithFuncs(t.Try.Body, funcGates) || queryContainsGateWithFuncs(t.Try.Catch, funcGates)) {
		return true
	}
	if t.Reduce != nil && (queryContainsGateWithFuncs(t.Reduce.Query, funcGates) || patternContainsGate(t.Reduce.Pattern, funcGates) || queryContainsGateWithFuncs(t.Reduce.Start, funcGates) || queryContainsGateWithFuncs(t.Reduce.Update, funcGates)) {
		return true
	}
	if t.Foreach != nil && (queryContainsGateWithFuncs(t.Foreach.Query, funcGates) || patternContainsGate(t.Foreach.Pattern, funcGates) || queryContainsGateWithFuncs(t.Foreach.Start, funcGates) || queryContainsGateWithFuncs(t.Foreach.Update, funcGates) || queryContainsGateWithFuncs(t.Foreach.Extract, funcGates)) {
		return true
	}
	if t.Label != nil && queryContainsGateWithFuncs(t.Label.Body, funcGates) {
		return true
	}
	for _, suffix := range t.SuffixList {
		if suffix != nil && indexContainsGate(suffix.Index, funcGates) {
			return true
		}
	}
	return false
}

func stringContainsGate(s *gojq.String, funcGates map[string]bool) bool {
	if s == nil {
		return false
	}
	for _, q := range s.Queries {
		if queryContainsGateWithFuncs(q, funcGates) {
			return true
		}
	}
	return false
}

func indexContainsGate(i *gojq.Index, funcGates map[string]bool) bool {
	if i == nil {
		return false
	}
	return stringContainsGate(i.Str, funcGates) || queryContainsGateWithFuncs(i.Start, funcGates) || queryContainsGateWithFuncs(i.End, funcGates)
}

func patternContainsGate(p *gojq.Pattern, funcGates map[string]bool) bool {
	if p == nil {
		return false
	}
	for _, elem := range p.Array {
		if patternContainsGate(elem, funcGates) {
			return true
		}
	}
	for _, obj := range p.Object {
		if obj == nil {
			continue
		}
		if stringContainsGate(obj.KeyString, funcGates) || queryContainsGateWithFuncs(obj.KeyQuery, funcGates) || patternContainsGate(obj.Val, funcGates) {
			return true
		}
	}
	return false
}

func patternsReferencedByQuery(patterns []*gojq.Pattern, q *gojq.Query) bool {
	vars := map[string]bool{}
	for _, pattern := range patterns {
		collectPatternVars(pattern, vars)
	}
	return queryReferencesVars(q, vars)
}

func collectPatternVars(pattern *gojq.Pattern, vars map[string]bool) {
	if pattern == nil {
		return
	}
	if pattern.Name != "" {
		vars[pattern.Name] = true
	}
	for _, elem := range pattern.Array {
		collectPatternVars(elem, vars)
	}
	for _, obj := range pattern.Object {
		if obj != nil {
			collectPatternVars(obj.Val, vars)
		}
	}
}

func queryReferencesVars(q *gojq.Query, vars map[string]bool) bool {
	if q == nil || len(vars) == 0 {
		return false
	}
	if termReferencesVars(q.Term, vars) || queryReferencesVars(q.Left, vars) || queryReferencesVars(q.Right, vars) {
		return true
	}
	for _, pattern := range q.Patterns {
		shadowed := copyVarSet(vars)
		removePatternVars(pattern, shadowed)
		if !queryReferencesVars(q.Right, shadowed) {
			continue
		}
		return true
	}
	return false
}

func termReferencesVars(t *gojq.Term, vars map[string]bool) bool {
	if t == nil {
		return false
	}
	if t.Func != nil {
		if vars[t.Func.Name] {
			return true
		}
		for _, arg := range t.Func.Args {
			if queryReferencesVars(arg, vars) {
				return true
			}
		}
	}
	if t.If != nil && (queryReferencesVars(t.If.Cond, vars) || queryReferencesVars(t.If.Then, vars) || queryReferencesVars(t.If.Else, vars)) {
		return true
	}
	if t.Object != nil {
		for _, kv := range t.Object.KeyVals {
			if kv != nil && (queryReferencesVars(kv.KeyQuery, vars) || queryReferencesVars(kv.Val, vars)) {
				return true
			}
		}
	}
	if t.Array != nil && queryReferencesVars(t.Array.Query, vars) {
		return true
	}
	if t.Unary != nil && termReferencesVars(t.Unary.Term, vars) {
		return true
	}
	if t.Query != nil && queryReferencesVars(t.Query, vars) {
		return true
	}
	if t.Try != nil && (queryReferencesVars(t.Try.Body, vars) || queryReferencesVars(t.Try.Catch, vars)) {
		return true
	}
	for _, suffix := range t.SuffixList {
		if suffix != nil && suffix.Index != nil && (queryReferencesVars(suffix.Index.Start, vars) || queryReferencesVars(suffix.Index.End, vars)) {
			return true
		}
	}
	return false
}

func copyVarSet(vars map[string]bool) map[string]bool {
	out := make(map[string]bool, len(vars))
	for name, ok := range vars {
		out[name] = ok
	}
	return out
}

func removePatternVars(pattern *gojq.Pattern, vars map[string]bool) {
	if pattern == nil {
		return
	}
	delete(vars, pattern.Name)
	for _, elem := range pattern.Array {
		removePatternVars(elem, vars)
	}
	for _, obj := range pattern.Object {
		if obj != nil {
			removePatternVars(obj.Val, vars)
		}
	}
}

func (b *builder) walkQuery(q *gojq.Query) {
	if q == nil {
		return
	}
	if q.Meta != nil {
		b.addDiagnostic(errorDiagnostic(DiagnosticUnsupported, "", q.String(), "jq modules are unsupported by the semantic planner"))
	}
	for _, im := range q.Imports {
		b.addDiagnostic(errorDiagnostic(DiagnosticUnsupported, "", im.String(), "jq imports/includes are unsupported by the semantic planner"))
	}
	b.refreshFunctionGateSummaries(q.FuncDefs)
	for _, fd := range q.FuncDefs {
		if fd != nil && strings.HasPrefix(fd.Name, internalSemanticPrefix) {
			b.addDiagnostic(errorDiagnostic(DiagnosticUnsupported, fd.Name, fd.String(), fmt.Sprintf("semantic internal callback namespace %q is reserved", internalSemanticPrefix)))
		}
		if fd != nil {
			b.funcDefDepth++
			b.funcStack = append(b.funcStack, fd.Name)
			b.walkQuery(fd.Body)
			b.funcStack = b.funcStack[:len(b.funcStack)-1]
			b.funcDefDepth--
		}
	}
	b.walkTerm(q.Term)
	leftFeedsDownstreamGate, demandField, hasDemandField := false, "", false
	if q.Op == gojq.OpPipe && len(q.Patterns) == 0 {
		leftFeedsDownstreamGate, demandField, hasDemandField = b.pipeGateDemand(q.Right)
	}
	bindingFeedsDownstreamGate := len(q.Patterns) > 0 && b.queryContainsGate(q.Right) && patternsReferencedByQuery(q.Patterns, q.Right)
	if isComparisonOp(q.Op) {
		b.walkGateQuery(q.Left)
	} else if leftFeedsDownstreamGate {
		b.walkOutputGateQueryDemand(q.Left, demandField, hasDemandField)
	} else if bindingFeedsDownstreamGate {
		b.walkOutputGateQuery(q.Left)
	} else {
		b.walkQuery(q.Left)
	}
	for _, pattern := range q.Patterns {
		b.walkPattern(pattern)
	}
	if isComparisonOp(q.Op) {
		b.walkGateQuery(q.Right)
	} else {
		b.walkQuery(q.Right)
	}
}

func (b *builder) walkTerm(t *gojq.Term) {
	if t == nil {
		return
	}
	switch t.Type {
	case gojq.TermTypeIdentity, gojq.TermTypeRecurse, gojq.TermTypeNull, gojq.TermTypeTrue, gojq.TermTypeFalse, gojq.TermTypeNumber, gojq.TermTypeBreak:
		// Supported terminals with no nested query fields.
	case gojq.TermTypeIndex:
		b.walkIndex(t.Index)
	case gojq.TermTypeFunc:
		b.walkFunc(t.Func)
	case gojq.TermTypeObject:
		b.walkObject(t.Object)
	case gojq.TermTypeArray:
		if t.Array != nil {
			b.walkQuery(t.Array.Query)
		}
	case gojq.TermTypeUnary:
		if t.Unary != nil {
			b.walkTerm(t.Unary.Term)
		}
	case gojq.TermTypeFormat, gojq.TermTypeString:
		b.walkString(t.Str)
	case gojq.TermTypeIf:
		b.walkIf(t.If)
	case gojq.TermTypeTry:
		if t.Try != nil {
			b.walkQuery(t.Try.Body)
			b.walkQuery(t.Try.Catch)
		}
	case gojq.TermTypeReduce:
		if t.Reduce != nil {
			b.walkQuery(t.Reduce.Query)
			b.walkPattern(t.Reduce.Pattern)
			b.walkQuery(t.Reduce.Start)
			b.walkQuery(t.Reduce.Update)
		}
	case gojq.TermTypeForeach:
		if t.Foreach != nil {
			b.walkQuery(t.Foreach.Query)
			b.walkPattern(t.Foreach.Pattern)
			b.walkQuery(t.Foreach.Start)
			b.walkQuery(t.Foreach.Update)
			b.walkQuery(t.Foreach.Extract)
		}
	case gojq.TermTypeLabel:
		if t.Label != nil {
			b.walkQuery(t.Label.Body)
		}
	case gojq.TermTypeQuery:
		b.walkQuery(t.Query)
	default:
		b.addDiagnostic(errorDiagnostic(DiagnosticUnsupported, "", t.String(), fmt.Sprintf("unsupported gojq term type %v", t.Type)))
	}
	for _, suffix := range t.SuffixList {
		b.walkSuffix(suffix)
	}
}

func (b *builder) walkFunc(f *gojq.Func) {
	if f == nil {
		return
	}
	if strings.HasPrefix(f.Name, internalSemanticPrefix) {
		b.addDiagnostic(errorDiagnostic(DiagnosticUnsupported, f.Name, f.String(), fmt.Sprintf("semantic internal callback namespace %q is reserved", internalSemanticPrefix)))
	}
	if strings.HasPrefix(f.Name, "sem_") {
		b.seenSem = true
		b.planSemanticFunc(f)
	} else {
		if len(b.funcStack) > 0 {
			caller := b.funcStack[len(b.funcStack)-1]
			b.funcCalls[caller] = append(b.funcCalls[caller], f.Name)
		}
		if b.inGate() {
			b.markFunctionValueNodesGated(f.Name, map[string]bool{})
		}
	}
	for _, arg := range f.Args {
		if f.Name == "select" || b.funcGates[f.Name] {
			b.walkGateQuery(arg)
			continue
		}
		b.walkQuery(arg)
	}
}

func (b *builder) planSemanticFunc(f *gojq.Func) {
	op, ok := semantics.Lookup(f.Name)
	if !ok {
		b.addDiagnostic(errorDiagnostic(DiagnosticUnknownSemOp, f.Name, f.String(), fmt.Sprintf("unknown semantic operator %q", f.Name)))
		return
	}
	if len(f.Args) > semantics.MaxJQFunctionArity {
		b.addDiagnostic(errorDiagnostic(DiagnosticMaxArity, f.Name, f.String(), fmt.Sprintf("%s has %d arguments; maximum supported by gojq custom functions is %d", f.Name, len(f.Args), semantics.MaxJQFunctionArity)))
		return
	}

	valueExpr, specs, ok := b.splitArgs(op, f)
	if !ok {
		return
	}
	b.nextCallID++
	id := b.nextCallID
	internalName := fmt.Sprintf("%s%04d", internalSemanticPrefix, id)
	source := unknownSource(f.String())
	gated := op.Kind == semantics.KindValue && b.inGate()
	b.plan.Semantic = append(b.plan.Semantic, SemNode{
		ID:            id,
		InternalName:  internalName,
		Arity:         len(f.Args),
		Op:            op.Name,
		Kind:          op.Kind,
		Return:        op.Return,
		ValueExpr:     valueExpr,
		Specs:         specs,
		Source:        source,
		Gated:         gated,
		ExecutionMode: executionModeFor(op, gated),
	})
	if len(b.funcStack) > 0 {
		fn := b.funcStack[len(b.funcStack)-1]
		b.funcSemNodes[fn] = append(b.funcSemNodes[fn], len(b.plan.Semantic)-1)
	}
	if b.instrument {
		f.Name = internalName
	}
}

func (b *builder) splitArgs(op semantics.OpSpec, f *gojq.Func) (string, []string, bool) {
	if op.Name == "sem_classify" {
		return b.splitClassify(op, f)
	}
	args := f.Args
	switch len(args) {
	case op.ImplicitMinArity:
		spec, ok := b.requireLiteralSpec(op.Name, f.String(), args[0], 0)
		if !ok {
			return "", nil, false
		}
		return ".", []string{spec}, true
	case op.ExplicitMinArity:
		spec, ok := b.requireLiteralSpec(op.Name, f.String(), args[1], 1)
		if !ok {
			return "", nil, false
		}
		return args[0].String(), []string{spec}, true
	default:
		b.addDiagnostic(errorDiagnostic(DiagnosticArity, op.Name, f.String(), fmt.Sprintf("%s expects arity %d (implicit dot) or %d (explicit value); got %d", op.Name, op.ImplicitMinArity, op.ExplicitMinArity, len(args))))
		return "", nil, false
	}
}

func (b *builder) splitClassify(op semantics.OpSpec, f *gojq.Func) (string, []string, bool) {
	args := f.Args
	if len(args) < op.ImplicitMinArity {
		b.addDiagnostic(errorDiagnostic(DiagnosticArity, op.Name, f.String(), fmt.Sprintf("%s expects at least %d labels for implicit dot or value plus at least %d labels; got %d arguments", op.Name, op.ImplicitMinSpecs, op.ExplicitMinSpecs, len(args))))
		return "", nil, false
	}

	allLiteral := true
	firstLiteral := false
	for i, arg := range args {
		_, ok := staticString(arg)
		if i == 0 {
			firstLiteral = ok
		}
		if !ok {
			allLiteral = false
		}
	}
	if allLiteral || firstLiteral {
		specs, ok := b.requireLiteralSpecs(op.Name, f.String(), args, 0)
		if !ok {
			return "", nil, false
		}
		return ".", specs, true
	}

	if len(args) < op.ExplicitMinArity {
		b.addDiagnostic(errorDiagnostic(DiagnosticArity, op.Name, f.String(), fmt.Sprintf("%s explicit-value form expects value plus at least %d labels; got %d arguments", op.Name, op.ExplicitMinSpecs, len(args))))
		return "", nil, false
	}
	specs, ok := b.requireLiteralSpecs(op.Name, f.String(), args[1:], 1)
	if !ok {
		return "", nil, false
	}
	return args[0].String(), specs, true
}

func (b *builder) requireLiteralSpecs(op, expr string, args []*gojq.Query, start int) ([]string, bool) {
	specs := make([]string, 0, len(args))
	ok := true
	for i, arg := range args {
		spec, argOK := b.requireLiteralSpec(op, expr, arg, start+i)
		if !argOK {
			ok = false
			continue
		}
		specs = append(specs, spec)
	}
	return specs, ok
}

func (b *builder) requireLiteralSpec(op, expr string, arg *gojq.Query, index int) (string, bool) {
	spec, ok := staticString(arg)
	if ok {
		return spec, true
	}
	b.addDiagnostic(errorDiagnostic(DiagnosticNonLiteralSpec, op, expr, fmt.Sprintf("%s argument %d is a spec/label and must be a static string literal", op, index+1)))
	return "", false
}

func staticString(q *gojq.Query) (string, bool) {
	if q == nil || q.Left != nil || q.Right != nil || q.Term == nil {
		return "", false
	}
	return staticStringTerm(q.Term)
}

func staticStringTerm(t *gojq.Term) (string, bool) {
	if t == nil || len(t.SuffixList) > 0 {
		return "", false
	}
	if t.Type == gojq.TermTypeQuery {
		return staticString(t.Query)
	}
	if t.Type != gojq.TermTypeString || t.Str == nil || t.Str.Queries != nil {
		return "", false
	}
	return t.Str.Str, true
}

func (b *builder) walkPattern(p *gojq.Pattern) {
	if p == nil {
		return
	}
	for _, elem := range p.Array {
		b.walkPattern(elem)
	}
	for _, obj := range p.Object {
		if obj == nil {
			continue
		}
		b.walkString(obj.KeyString)
		b.walkQuery(obj.KeyQuery)
		b.walkPattern(obj.Val)
	}
}

func (b *builder) walkObject(o *gojq.Object) {
	if o == nil {
		return
	}
	for _, kv := range o.KeyVals {
		if kv == nil {
			continue
		}
		b.walkString(kv.KeyString)
		b.walkQuery(kv.KeyQuery)
		b.walkQuery(kv.Val)
	}
}

func (b *builder) walkString(s *gojq.String) {
	if s == nil {
		return
	}
	for _, q := range s.Queries {
		b.walkQuery(q)
	}
}

func (b *builder) walkIndex(i *gojq.Index) {
	if i == nil {
		return
	}
	b.walkString(i.Str)
	b.walkQuery(i.Start)
	b.walkQuery(i.End)
}

func (b *builder) walkSuffix(s *gojq.Suffix) {
	if s != nil {
		b.walkIndex(s.Index)
	}
}

func (b *builder) walkIf(i *gojq.If) {
	if i == nil {
		return
	}
	b.walkGateQuery(i.Cond)
	b.walkQuery(i.Then)
	for _, elif := range i.Elif {
		if elif != nil {
			b.walkGateQuery(elif.Cond)
			b.walkQuery(elif.Then)
		}
	}
	b.walkQuery(i.Else)
}
