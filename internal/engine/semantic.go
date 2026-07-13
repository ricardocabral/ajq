package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/schema"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// PlanError wraps semantic plan diagnostics that block split execution.
type PlanError struct{ Diagnostics []plan.Diagnostic }

func (e *PlanError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "semantic plan error"
	}
	return fmt.Sprintf("semantic plan error: %s", e.Diagnostics[0].Message)
}

type semanticPhase string

const (
	semanticPhaseHarvest semanticPhase = "harvest"
	semanticPhaseExecute semanticPhase = "execute"
)

type semanticWitness struct {
	ID      plan.CallID
	Planned bool
	Op      string
	Query   string
	Source  plan.Source
	Phase   semanticPhase
}

// SemanticInvariantError reports a semantic operation that fired at runtime but
// was not present in the static plan used to compile the three-phase program.
type SemanticInvariantError struct {
	Witness semanticWitness
}

func (e *SemanticInvariantError) Error() string {
	w := e.Witness
	return fmt.Sprintf("semantic invariant violation: unplanned semantic call during %s: op=%s query=%q source=%q", w.Phase, w.Op, w.Query, w.Source.Expression)
}

// semanticResolveError preserves the zero-based source frame for a
// deterministic error raised while resolving a batch of collected judgements.
// executeBefore is exclusive: frames before it have all values necessary for
// ordered execution; a negative value means no frame from the window is safe
// to execute.
type semanticResolveError struct {
	frame         int64
	executeBefore int64
	err           error
}

func (e *semanticResolveError) Error() string { return e.err.Error() }
func (e *semanticResolveError) Unwrap() error { return e.err }

type semanticRuntime struct {
	backend             backend.Backend
	modelID             string
	collected           []backend.Judgement
	collectedFrames     []int64
	currentFrame        int64
	cache               *semanticcache.Store
	query               string
	planned             map[plan.CallID]plan.SemNode
	plannedOrder        []plan.SemNode
	fired               []semanticWitness
	witnessObserver     func(semanticWitness) // test-only callback retained across execution passes
	stats               *RunStats
	maxCalls            int
	maxCallsDefaultPaid bool
}

type threePhaseProgram struct {
	harvestProgram *jq.Program
	executeProgram *jq.Program
	runtime        *semanticRuntime
}

func compileThreePhase(query string, be backend.Backend) (*threePhaseProgram, error) {
	return compileThreePhaseWithOptions(query, be, "", nil)
}

func compileThreePhaseWithOptions(query string, be backend.Backend, modelID string, store *semanticcache.Store) (*threePhaseProgram, error) {
	return compileThreePhaseWithStats(query, be, modelID, store, nil)
}

func compileThreePhaseWithStats(query string, be backend.Backend, modelID string, store *semanticcache.Store, stats *RunStats) (*threePhaseProgram, error) {
	rewrittenQuery, err := rewriteQuery(query)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	query = rewrittenQuery

	if be == nil {
		return nil, fmt.Errorf("semantic backend is required for split execution")
	}
	if modelID == "" {
		modelID = semanticcache.DefaultModelID
	}
	if store == nil {
		store = semanticcache.NewStore()
	}
	semanticPlan, diagnostics := plan.BuildInstrumented(query)
	if extra := validateStep1SemanticPlan(semanticPlan.Plan); len(extra) > 0 {
		diagnostics = append(diagnostics, extra...)
	}
	if blockingDiagnostics(diagnostics) {
		return nil, &PlanError{Diagnostics: diagnostics}
	}

	rt := newSemanticRuntime(be, modelID, store, semanticPlan.Plan)
	rt.stats = stats
	harvestProgram, err := jq.CompileWithOptions(semanticPlan.InstrumentedQuery, rt.harvestOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	executeProgram, err := jq.CompileWithOptions(semanticPlan.InstrumentedQuery, rt.executeOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	return &threePhaseProgram{harvestProgram: harvestProgram, executeProgram: executeProgram, runtime: rt}, nil
}

func blockingDiagnostics(diagnostics []plan.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == plan.SeverityError {
			return true
		}
	}
	return false
}

func blockingSemanticDiagnostics(diagnostics []plan.Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == plan.SeverityError && d.Code != plan.DiagnosticParseError {
			return true
		}
	}
	return false
}

func validateStep1SemanticPlan(p plan.Plan) []plan.Diagnostic {
	var diagnostics []plan.Diagnostic
	if len(p.Semantic) > 0 {
		unsafe, err := hasUnsafeGeneratorContext(p.Query)
		if err == nil && unsafe {
			diagnostics = append(diagnostics, plan.Diagnostic{
				Code:     plan.DiagnosticUnsupported,
				Severity: plan.SeverityError,
				Message:  "three-phase semantic generator harvest is unsupported in order/cardinality-sensitive jq contexts",
				Source:   plan.Source{Expression: p.Query, StartByte: -1, EndByte: -1, HasRange: false},
			})
		}
	}
	for _, node := range p.Semantic {
		if node.Kind == semantics.KindValue && node.Op != "sem_classify" && !isSupportedBoundedValueNode(p, node) {
			diagnostics = append(diagnostics, plan.Diagnostic{
				Code:     plan.DiagnosticUnsupported,
				Severity: plan.SeverityError,
				Message:  fmt.Sprintf("three-phase harvest for %s is unsupported until the executor has a safe bounded generator or interleaved fallback", node.Op),
				Op:       node.Op,
				Source:   node.Source,
			})
			continue
		}
	}
	return diagnostics
}

func isSupportedBoundedValueNode(p plan.Plan, node plan.SemNode) bool {
	query := compactSemanticQuery(p.Query)
	expr := compactSemanticQuery(node.Source.Expression)
	switch node.Op {
	case "sem_score":
		return strings.Contains(query, "sort_by("+expr+")")
	case "sem_norm":
		return strings.Contains(query, "group_by("+expr+")")
	default:
		return false
	}
}

func compactSemanticQuery(s string) string {
	return strings.Join(strings.Fields(s), "")
}

func newSemanticRuntime(be backend.Backend, modelID string, store *semanticcache.Store, p plan.Plan) *semanticRuntime {
	planned := make(map[plan.CallID]plan.SemNode, len(p.Semantic))
	plannedOrder := make([]plan.SemNode, 0, len(p.Semantic))
	for _, node := range p.Semantic {
		planned[node.ID] = node
		plannedOrder = append(plannedOrder, node)
	}
	return &semanticRuntime{backend: be, modelID: modelID, cache: store, query: p.Query, planned: planned, plannedOrder: plannedOrder}
}

// beginWindow releases all previous window references before collecting the
// next window. Call harvestAppend once per frame, then resolve once.
func (p *threePhaseProgram) beginWindow() {
	p.runtime.releaseCollected()
}

// releaseWindow drops collected values after resolve and ordered execution.
func (p *threePhaseProgram) releaseWindow() {
	p.runtime.releaseCollected()
}

// harvest preserves the historical one-frame collection contract used by unit
// tests and callers that compile a three-phase program directly.
func (p *threePhaseProgram) harvest(input any) error {
	p.beginWindow()
	return p.harvestAppend(input, 0)
}

// harvestAppend appends one frame transactionally. A failed frame contributes
// neither judgements nor witnesses to the already materialized window prefix.
func (p *threePhaseProgram) harvestAppend(input any, frame int64) error {
	rt := p.runtime
	startCollected := len(rt.collected)
	startFrames := len(rt.collectedFrames)
	rt.currentFrame = frame
	rt.resetWitnesses()
	_, err := p.harvestProgram.Run(input, nil)
	if invariantErr := rt.checkInvariant(semanticPhaseHarvest); invariantErr != nil {
		err = invariantErr
	}
	if err != nil {
		rt.discardCollectedFrom(startCollected, startFrames)
		return err
	}
	if rt.stats != nil {
		rt.stats.HarvestedJudgements += len(rt.collected) - startCollected
	}
	return nil
}

func (p *threePhaseProgram) resolve(ctx context.Context) error {
	return p.runtime.resolve(ctx)
}

func (rt *semanticRuntime) releaseCollected() {
	for i := range rt.collected {
		rt.collected[i].Value = nil
		rt.collected[i].Specs = nil
	}
	rt.collected = nil
	rt.collectedFrames = nil
}

func (rt *semanticRuntime) discardCollectedFrom(collected, frames int) {
	for i := collected; i < len(rt.collected); i++ {
		rt.collected[i].Value = nil
		rt.collected[i].Specs = nil
	}
	rt.collected = rt.collected[:collected]
	rt.collectedFrames = rt.collectedFrames[:frames]
}

func (p *threePhaseProgram) execute(input any, emit func(any) error) (jq.RunResult, error) {
	p.runtime.resetWitnesses()
	runResult, err := p.executeProgram.Run(input, emit)
	if invariantErr := p.runtime.checkInvariant(semanticPhaseExecute); invariantErr != nil {
		return runResult, invariantErr
	}
	return runResult, err
}

func (rt *semanticRuntime) harvestOptions() []gojq.CompilerOption {
	opts := []gojq.CompilerOption{
		gojq.WithIterFunction("sem_match", 1, 2, rt.harvestPredicate(0, "sem_match", publicSemanticSource("sem_match"))),
		gojq.WithIterFunction("sem_classify", 2, semantics.MaxJQFunctionArity, rt.harvestClassify(0, publicSemanticSource("sem_classify"))),
		gojq.WithFunction("sem_extract", 1, 2, rt.unsupportedHarvestValueFunction(0, "sem_extract", publicSemanticSource("sem_extract"))),
		gojq.WithFunction("sem_score", 1, 2, rt.unsupportedHarvestValueFunction(0, "sem_score", publicSemanticSource("sem_score"))),
		gojq.WithFunction("sem_norm", 1, 2, rt.unsupportedHarvestValueFunction(0, "sem_norm", publicSemanticSource("sem_norm"))),
		gojq.WithFunction("sem_redact", 1, 2, rt.unsupportedHarvestValueFunction(0, "sem_redact", publicSemanticSource("sem_redact"))),
	}
	for _, node := range rt.plannedOrder {
		switch node.Op {
		case "sem_classify":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, rt.harvestClassify(node.ID, node.Source)))
		case "sem_match":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, rt.harvestPredicate(node.ID, node.Op, node.Source)))
		case "sem_score", "sem_norm":
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, rt.harvestValueFunction(node.ID, node.Op, node.Source)))
		default:
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, rt.unsupportedHarvestValueFunction(node.ID, node.Op, node.Source)))
		}
	}
	return opts
}

func (rt *semanticRuntime) executeOptions() []gojq.CompilerOption {
	opts := []gojq.CompilerOption{
		gojq.WithFunction("sem_match", 1, 2, rt.executeFunction(0, "sem_match", publicSemanticSource("sem_match"))),
		gojq.WithIterFunction("sem_classify", 2, semantics.MaxJQFunctionArity, rt.executeClassify(0, publicSemanticSource("sem_classify"))),
		gojq.WithFunction("sem_extract", 1, 2, rt.executeFunction(0, "sem_extract", publicSemanticSource("sem_extract"))),
		gojq.WithFunction("sem_score", 1, 2, rt.executeFunction(0, "sem_score", publicSemanticSource("sem_score"))),
		gojq.WithFunction("sem_norm", 1, 2, rt.executeFunction(0, "sem_norm", publicSemanticSource("sem_norm"))),
		gojq.WithFunction("sem_redact", 1, 2, rt.executeFunction(0, "sem_redact", publicSemanticSource("sem_redact"))),
	}
	for _, node := range rt.plannedOrder {
		switch node.Op {
		case "sem_classify":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, rt.executeClassify(node.ID, node.Source)))
		default:
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, rt.executeFunction(node.ID, node.Op, node.Source)))
		}
	}
	return opts
}

func (rt *semanticRuntime) harvestPredicate(id plan.CallID, op string, source plan.Source) func(any, []any) gojq.Iter {
	return func(input any, args []any) gojq.Iter {
		rt.recordWitness(id, semanticPhaseHarvest, op, source)
		judgement, err := rt.judgementFromCall(op, input, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		rt.appendJudgement(judgement)
		return gojq.NewIter[any](true, false)
	}
}

func (rt *semanticRuntime) unsupportedHarvestValueFunction(id plan.CallID, op string, source plan.Source) func(any, []any) any {
	return func(input any, args []any) any {
		rt.recordWitness(id, semanticPhaseHarvest, op, source)
		judgement, err := rt.judgementFromCall(op, input, args)
		if err != nil {
			return err
		}
		rt.appendJudgement(judgement)
		return fmt.Errorf("%s is not yet safe for three-phase harvest; use a supported bounded value op", op)
	}
}

func (rt *semanticRuntime) harvestValueFunction(id plan.CallID, op string, source plan.Source) func(any, []any) any {
	return func(input any, args []any) any {
		rt.recordWitness(id, semanticPhaseHarvest, op, source)
		judgement, err := rt.judgementFromCall(op, input, args)
		if err != nil {
			return err
		}
		rt.appendJudgement(judgement)
		switch judgement.Return {
		case semantics.ReturnNumber:
			return 0.0
		case semantics.ReturnString:
			return ""
		case semantics.ReturnBool:
			return false
		default:
			return nil
		}
	}
}

func (rt *semanticRuntime) harvestClassify(id plan.CallID, source plan.Source) func(any, []any) gojq.Iter {
	return func(input any, args []any) gojq.Iter {
		rt.recordWitness(id, semanticPhaseHarvest, "sem_classify", source)
		judgement, err := rt.judgementFromPlannedCall(id, "sem_classify", input, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		rt.appendJudgement(judgement)
		values := make([]any, len(judgement.Specs))
		for i, spec := range judgement.Specs {
			values[i] = spec
		}
		return gojq.NewIter(values...)
	}
}

func (rt *semanticRuntime) executeFunction(id plan.CallID, op string, source plan.Source) func(any, []any) any {
	return func(input any, args []any) any {
		rt.recordWitness(id, semanticPhaseExecute, op, source)
		judgement, err := rt.judgementFromCall(op, input, args)
		if err != nil {
			return err
		}
		key, err := semanticcache.KeyForJudgement(judgement)
		if err != nil {
			return err
		}
		result, ok := rt.cache.Get(key)
		if !ok {
			return fmt.Errorf("semantic execute cache miss for %s", op)
		}
		return result.Value
	}
}

func (rt *semanticRuntime) executeClassify(id plan.CallID, source plan.Source) func(any, []any) gojq.Iter {
	return func(input any, args []any) gojq.Iter {
		rt.recordWitness(id, semanticPhaseExecute, "sem_classify", source)
		judgement, err := rt.judgementFromPlannedCall(id, "sem_classify", input, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		key, err := semanticcache.KeyForJudgement(judgement)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		result, ok := rt.cache.Get(key)
		if !ok {
			return gojq.NewIter[any](fmt.Errorf("semantic execute cache miss for sem_classify"))
		}
		return gojq.NewIter(result.Value)
	}
}

func publicSemanticSource(op string) plan.Source {
	return plan.Source{Expression: op, StartByte: -1, EndByte: -1, HasRange: false}
}

func (rt *semanticRuntime) appendJudgement(judgement backend.Judgement) {
	rt.collected = append(rt.collected, judgement)
	rt.collectedFrames = append(rt.collectedFrames, rt.currentFrame)
}

func (rt *semanticRuntime) resetWitnesses() {
	rt.fired = nil
}

func (rt *semanticRuntime) recordWitness(id plan.CallID, phase semanticPhase, fallbackOp string, fallbackSource plan.Source) {
	node, planned := rt.planned[id]
	op := fallbackOp
	source := fallbackSource
	if planned {
		op = node.Op
		source = node.Source
	}
	witness := semanticWitness{
		ID:      id,
		Planned: planned,
		Op:      op,
		Query:   rt.query,
		Source:  source,
		Phase:   phase,
	}
	rt.fired = append(rt.fired, witness)
	if rt.witnessObserver != nil {
		rt.witnessObserver(witness)
	}
}

func (rt *semanticRuntime) checkInvariant(phase semanticPhase) error {
	for _, witness := range rt.fired {
		if !witness.Planned {
			witness.Phase = phase
			return &SemanticInvariantError{Witness: witness}
		}
	}
	return nil
}

func (rt *semanticRuntime) resolve(ctx context.Context) error {
	if len(rt.collected) == 0 {
		return nil
	}
	seen := make(map[semanticcache.Key]struct{}, len(rt.collected))
	var pending []backend.Judgement
	var pendingKeys []semanticcache.Key
	var pendingFrames []int64
	for i, judgement := range rt.collected {
		key, err := semanticcache.KeyForJudgement(judgement)
		if err != nil {
			// No batch has been dispatched. Only frames before the first uncached
			// judgement can be known to be executable from cache.
			prefix := rt.frameForCollected(i)
			if len(pendingFrames) != 0 && pendingFrames[0] < prefix {
				prefix = pendingFrames[0]
			}
			return &semanticResolveError{frame: rt.frameForCollected(i), executeBefore: prefix, err: err}
		}
		if _, ok := rt.cache.Get(key); ok {
			if rt.stats != nil {
				rt.stats.CacheHits++
			}
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pending = append(pending, judgement)
		pendingKeys = append(pendingKeys, key)
		pendingFrames = append(pendingFrames, rt.frameForCollected(i))
	}
	if len(pending) == 0 {
		return nil
	}
	spent := 0
	if rt.stats != nil {
		spent = rt.stats.PostDedupBackendCalls
	}
	if err := checkMaxCalls(rt.maxCalls, spent, len(pending), rt.maxCallsDefaultPaid); err != nil {
		return &semanticResolveError{frame: pendingFrames[0], executeBefore: -1, err: err}
	}
	if rt.stats != nil {
		rt.stats.PostDedupBackendCalls += len(pending)
	}
	results, err := rt.backend.Judge(ctx, append([]backend.Judgement(nil), pending...))
	if err != nil {
		return &semanticResolveError{frame: pendingFrames[0], executeBefore: -1, err: err}
	}
	if len(results) != len(pending) {
		return &semanticResolveError{frame: pendingFrames[0], executeBefore: -1, err: fmt.Errorf("backend returned %d results for %d judgements", len(results), len(pending))}
	}
	for i, judgement := range pending {
		result := results[i]
		if result.Error != "" {
			return rt.resultResolveError(i, fmt.Errorf("backend result %d for %s: %s", i, judgement.Op, result.Error), pendingFrames, pendingKeys, results)
		}
		if err := validateResult(judgement, result); err != nil {
			return rt.resultResolveError(i, fmt.Errorf("backend result %d for %s: %w", i, judgement.Op, err), pendingFrames, pendingKeys, results)
		}
	}
	for i, result := range results {
		rt.cache.Set(pendingKeys[i], result)
	}
	return nil
}

// resultResolveError commits every result validated before the failing item.
// Execution still stops before the failing frame, but keeping same-frame
// validated values preserves the resolver's prior cache contract.
func (rt *semanticRuntime) resultResolveError(failing int, err error, frames []int64, keys []semanticcache.Key, results []backend.Result) error {
	failingFrame := frames[failing]
	for i := 0; i < failing; i++ {
		rt.cache.Set(keys[i], results[i])
	}
	return &semanticResolveError{frame: failingFrame, executeBefore: failingFrame, err: err}
}

func (rt *semanticRuntime) frameForCollected(index int) int64 {
	if index >= 0 && index < len(rt.collectedFrames) {
		return rt.collectedFrames[index]
	}
	return 0
}

func (rt *semanticRuntime) judgementFromCall(opName string, input any, args []any) (backend.Judgement, error) {
	judgement, err := judgementFromCall(opName, input, args)
	if err != nil {
		return backend.Judgement{}, err
	}
	judgement.ModelID = rt.modelID
	return judgement, nil
}

func (rt *semanticRuntime) judgementFromPlannedCall(id plan.CallID, opName string, input any, args []any) (backend.Judgement, error) {
	if node, ok := rt.planned[id]; ok && node.Op == "sem_classify" {
		value := input
		if node.ValueExpr != "." || node.Arity != len(node.Specs) {
			if len(args) == 0 {
				return backend.Judgement{}, fmt.Errorf("sem_classify explicit-value call missing value argument")
			}
			value = args[0]
		}
		judgement := backend.Judgement{Op: node.Op, Kind: node.Kind, Return: node.Return, Schema: backend.ResultSchema{Type: node.Return, Enum: append([]string(nil), node.Specs...)}, Specs: append([]string(nil), node.Specs...), Value: value, ModelID: rt.modelID}
		return judgement, nil
	}
	return rt.judgementFromCall(opName, input, args)
}

func judgementFromCall(opName string, input any, args []any) (backend.Judgement, error) {
	op, ok := semantics.Lookup(opName)
	if !ok {
		return backend.Judgement{}, fmt.Errorf("unknown semantic operator %q", opName)
	}
	value, specs, err := valueAndSpecs(op.Name, input, args)
	if err != nil {
		return backend.Judgement{}, err
	}
	return backend.Judgement{Op: op.Name, Kind: op.Kind, Return: op.Return, Schema: schemaFor(op, specs), Specs: specs, Value: value}, nil
}

func schemaFor(op semantics.OpSpec, specs []string) backend.ResultSchema {
	schema := backend.ResultSchema{Type: op.Return}
	if op.Name == "sem_classify" {
		schema.Enum = append([]string(nil), specs...)
	}
	return schema
}

func valueAndSpecs(op string, input any, args []any) (any, []string, error) {
	if op == "sem_classify" {
		if len(args) < 2 {
			return nil, nil, fmt.Errorf("sem_classify expects at least two labels")
		}
		if len(args) == 2 {
			specs, err := stringSpecs(args)
			return input, specs, err
		}
		specs, err := stringSpecs(args[1:])
		return args[0], specs, err
	}
	if len(args) == 1 {
		specs, err := stringSpecs(args)
		return input, specs, err
	}
	if len(args) == 2 {
		specs, err := stringSpecs(args[1:])
		return args[0], specs, err
	}
	return nil, nil, fmt.Errorf("%s expects one implicit-dot spec or explicit value plus one spec", op)
}

func stringSpecs(args []any) ([]string, error) {
	specs := make([]string, len(args))
	for i, arg := range args {
		spec, ok := arg.(string)
		if !ok {
			return nil, fmt.Errorf("semantic spec argument %d must be a string", i+1)
		}
		specs[i] = spec
	}
	return specs, nil
}

// validateResult enforces the schema-invariance guarantee at the deterministic
// boundary: it is called by resolve before a backend result may be cached and
// re-enter execution, so a structurally invalid value (wrong type, out-of-set
// enum label, or an inconsistent op/schema contract) is rejected here and never
// cached. It delegates to the centralized schema builder so the contract that
// shapes local backend requests is the exact contract enforced on their
// results.
func validateResult(j backend.Judgement, r backend.Result) error {
	constraint, err := schema.ForJudgement(j)
	if err != nil {
		return err
	}
	return constraint.Validate(r.Value)
}
