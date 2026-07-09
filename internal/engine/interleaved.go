package engine

import (
	"context"
	"fmt"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/semantics"
)

func compileInterleaved(ctx context.Context, query string, be backend.Backend, modelID string, store *semanticcache.Store) (*jq.Program, error) {
	return compileInterleavedWithStats(ctx, query, be, modelID, store, nil, 0, false)
}

func compileInterleavedWithStats(ctx context.Context, query string, be backend.Backend, modelID string, store *semanticcache.Store, stats *RunStats, maxCalls int, maxCallsDefaultPaid bool) (*jq.Program, error) {
	if be == nil {
		return nil, fmt.Errorf("semantic backend is required for interleaved execution")
	}
	if modelID == "" {
		modelID = semanticcache.DefaultModelID
	}
	if store == nil {
		store = semanticcache.NewStore()
	}
	semanticPlan, diagnostics := plan.BuildInstrumented(query)
	if blockingDiagnostics(diagnostics) {
		return nil, &PlanError{Diagnostics: diagnostics}
	}
	rt := &interleavedRuntime{ctx: ctx, backend: be, modelID: modelID, cache: store, planned: map[plan.CallID]plan.SemNode{}, stats: stats, maxCalls: maxCalls, maxCallsDefaultPaid: maxCallsDefaultPaid}
	for _, node := range semanticPlan.Semantic {
		rt.planned[node.ID] = node
	}
	program, err := jq.CompileWithOptions(semanticPlan.InstrumentedQuery, rt.options(semanticPlan.Semantic)...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	return program, nil
}

type interleavedRuntime struct {
	ctx                 context.Context
	backend             backend.Backend
	modelID             string
	cache               *semanticcache.Store
	planned             map[plan.CallID]plan.SemNode
	stats               *RunStats
	maxCalls            int
	maxCallsDefaultPaid bool
}

func (rt *interleavedRuntime) options(nodes []plan.SemNode) []gojq.CompilerOption {
	opts := []gojq.CompilerOption{
		gojq.WithFunction("sem_match", 1, 2, rt.function("sem_match")),
		gojq.WithFunction("sem_classify", 2, semantics.MaxJQFunctionArity, rt.function("sem_classify")),
		gojq.WithFunction("sem_extract", 1, 2, rt.function("sem_extract")),
		gojq.WithFunction("sem_score", 1, 2, rt.function("sem_score")),
		gojq.WithFunction("sem_norm", 1, 2, rt.function("sem_norm")),
		gojq.WithFunction("sem_redact", 1, 2, rt.function("sem_redact")),
	}
	for _, node := range nodes {
		opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, rt.plannedFunction(node.ID, node.Op)))
	}
	return opts
}

func (rt *interleavedRuntime) plannedFunction(id plan.CallID, op string) func(any, []any) any {
	return func(input any, args []any) any {
		judgement, err := rt.judgementFromPlannedCall(id, op, input, args)
		if err != nil {
			return err
		}
		return rt.resolveOne(judgement)
	}
}

func (rt *interleavedRuntime) function(op string) func(any, []any) any {
	return func(input any, args []any) any {
		judgement, err := rt.judgement(op, input, args)
		if err != nil {
			return err
		}
		return rt.resolveOne(judgement)
	}
}

func (rt *interleavedRuntime) resolveOne(judgement backend.Judgement) any {
	key, err := semanticcache.KeyForJudgement(judgement)
	if err != nil {
		return err
	}
	if result, ok := rt.cache.Get(key); ok {
		if rt.stats != nil {
			rt.stats.CacheHits++
		}
		return result.Value
	}
	ctx := rt.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	spent := 0
	if rt.stats != nil {
		spent = rt.stats.PostDedupBackendCalls
	}
	if err := checkMaxCalls(rt.maxCalls, spent, 1, rt.maxCallsDefaultPaid); err != nil {
		return err
	}
	if rt.stats != nil {
		rt.stats.PostDedupBackendCalls++
	}
	results, err := rt.backend.Judge(ctx, []backend.Judgement{judgement})
	if err != nil {
		return err
	}
	if len(results) != 1 {
		return fmt.Errorf("interleaved backend returned %d results for one judgement", len(results))
	}
	if results[0].Error != "" {
		return fmt.Errorf("backend result for %s: %s", judgement.Op, results[0].Error)
	}
	if err := validateResult(judgement, results[0]); err != nil {
		return err
	}
	rt.cache.Set(key, results[0])
	return results[0].Value
}

func (rt *interleavedRuntime) judgement(op string, input any, args []any) (backend.Judgement, error) {
	judgement, err := judgementFromCall(op, input, args)
	if err != nil {
		return backend.Judgement{}, err
	}
	judgement.ModelID = rt.modelID
	return judgement, nil
}

func (rt *interleavedRuntime) judgementFromPlannedCall(id plan.CallID, op string, input any, args []any) (backend.Judgement, error) {
	if node, ok := rt.planned[id]; ok && node.Op == "sem_classify" {
		value := input
		if node.ValueExpr != "." || node.Arity != len(node.Specs) {
			if len(args) == 0 {
				return backend.Judgement{}, fmt.Errorf("sem_classify explicit-value call missing value argument")
			}
			value = args[0]
		}
		return backend.Judgement{Op: node.Op, Kind: node.Kind, Return: node.Return, Schema: backend.ResultSchema{Type: node.Return, Enum: append([]string(nil), node.Specs...)}, Specs: append([]string(nil), node.Specs...), Value: value, ModelID: rt.modelID}, nil
	}
	return rt.judgement(op, input, args)
}
