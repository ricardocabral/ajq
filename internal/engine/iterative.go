package engine

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// iterativeProgram runs a recognized linear predicate chain one call site at a
// time. It deliberately shares semanticRuntime with the normal executor so
// cache keys, validation, call IDs, and run-global accounting remain common.
type iterativeProgram struct {
	stageProgram   *jq.Program
	executeProgram *jq.Program
	runtime        *semanticRuntime
	stages         []plan.CallID
	stageIndex     map[plan.CallID]int
	active         int
}

func compileIterative(query string, be backend.Backend, modelID string, store *semanticcache.Store, stats *RunStats, stages plan.IterativePlan) (*iterativeProgram, error) {
	if be == nil {
		return nil, fmt.Errorf("semantic backend is required for iterative execution")
	}
	if modelID == "" {
		modelID = semanticcache.DefaultModelID
	}
	if store == nil {
		store = semanticcache.NewStore()
	}
	instrumented, diagnostics := plan.BuildInstrumented(query)
	if blockingDiagnostics(diagnostics) {
		return nil, &PlanError{Diagnostics: diagnostics}
	}
	rt := newSemanticRuntime(be, modelID, store, instrumented.Plan)
	rt.stats = stats
	p := &iterativeProgram{runtime: rt, stages: append([]plan.CallID(nil), stages.Stages...), stageIndex: map[plan.CallID]int{}}
	for i, id := range p.stages {
		p.stageIndex[id] = i
	}
	stageProgram, err := jq.CompileWithOptions(instrumented.InstrumentedQuery, p.stageOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	executeProgram, err := jq.CompileWithOptions(instrumented.InstrumentedQuery, rt.executeOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	p.stageProgram, p.executeProgram = stageProgram, executeProgram
	return p, nil
}

func (p *iterativeProgram) stageOptions() []gojq.CompilerOption {
	opts := make([]gojq.CompilerOption, 0, len(p.runtime.plannedOrder))
	for _, node := range p.runtime.plannedOrder {
		switch node.Op {
		case "sem_match":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, p.stageMatch(node)))
		case "sem_classify":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, p.stageClassify(node)))
		default:
			// IterativeStages excludes value operators. Keep compilation total so a
			// future analyzer regression fails at runtime rather than calling a backend.
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, func(any, []any) any { return fmt.Errorf("unsupported iterative semantic operator %s", node.Op) }))
		}
	}
	return opts
}

func (p *iterativeProgram) stageMatch(node plan.SemNode) func(any, []any) gojq.Iter {
	return func(value any, args []any) gojq.Iter {
		p.runtime.recordWitness(node.ID, semanticPhaseHarvest, node.Op, node.Source)
		index := p.stageIndex[node.ID]
		if index > p.active {
			return gojq.NewIter[any](true)
		}
		judgement, err := p.runtime.judgementFromCall(node.Op, value, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		if index == p.active {
			key, err := semanticcache.KeyForJudgement(judgement)
			if err != nil {
				return gojq.NewIter[any](err)
			}
			if result, ok := p.runtime.cache.Get(key); ok {
				if p.runtime.stats != nil {
					p.runtime.stats.CacheHits++
				}
				return gojq.NewIter[any](result.Value)
			}
			p.runtime.appendJudgement(judgement)
			return gojq.NewIter[any](true)
		}
		result, err := p.cached(judgement)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		return gojq.NewIter[any](result.Value)
	}
}

func (p *iterativeProgram) stageClassify(node plan.SemNode) func(any, []any) gojq.Iter {
	return func(value any, args []any) gojq.Iter {
		p.runtime.recordWitness(node.ID, semanticPhaseHarvest, node.Op, node.Source)
		index := p.stageIndex[node.ID]
		if index > p.active {
			values := make([]any, len(node.Specs))
			for i := range node.Specs {
				values[i] = node.Specs[i]
			}
			return gojq.NewIter(values...)
		}
		judgement, err := p.runtime.judgementFromPlannedCall(node.ID, node.Op, value, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		if index == p.active {
			key, err := semanticcache.KeyForJudgement(judgement)
			if err != nil {
				return gojq.NewIter[any](err)
			}
			if result, ok := p.runtime.cache.Get(key); ok {
				if p.runtime.stats != nil {
					p.runtime.stats.CacheHits++
				}
				return gojq.NewIter[any](result.Value)
			}
			p.runtime.appendJudgement(judgement)
			values := make([]any, len(node.Specs))
			for i := range node.Specs {
				values[i] = node.Specs[i]
			}
			return gojq.NewIter(values...)
		}
		result, err := p.cached(judgement)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		return gojq.NewIter[any](result.Value)
	}
}

func (p *iterativeProgram) cached(judgement backend.Judgement) (backend.Result, error) {
	key, err := semanticcache.KeyForJudgement(judgement)
	if err != nil {
		return backend.Result{}, err
	}
	result, ok := p.runtime.cache.Get(key)
	if !ok {
		return backend.Result{}, fmt.Errorf("iterative stage cache miss for %s", judgement.Op)
	}
	return result, nil
}

func (p *iterativeProgram) runStage(ctx context.Context, frames []input.Frame, active int) error {
	p.active = active
	p.runtime.releaseCollected()
	for i := range frames {
		if err := ctxErr(ctx); err != nil {
			return err
		}
		p.runtime.currentFrame = frames[i].Index
		p.runtime.resetWitnesses()
		before := len(p.runtime.collected)
		_, err := p.stageProgram.Run(frames[i].Value, nil)
		if invariant := p.runtime.checkInvariant(semanticPhaseHarvest); invariant != nil {
			err = invariant
		}
		if err != nil {
			p.runtime.discardCollectedFrom(before, before)
			return &RuntimeError{Frame: frames[i].Index + 1, Err: err}
		}
		if p.runtime.stats != nil {
			p.runtime.stats.HarvestedJudgements += len(p.runtime.collected) - before
		}
	}
	return p.runtime.resolve(ctx)
}

func (p *iterativeProgram) execute(frame input.Frame, emit func(any) error) (jq.RunResult, error) {
	p.runtime.resetWitnesses()
	result, err := p.executeProgram.Run(frame.Value, emit)
	if invariant := p.runtime.checkInvariant(semanticPhaseExecute); invariant != nil {
		err = invariant
	}
	return result, err
}

func executeIterative(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, callSites int, stages plan.IterativePlan) (Result, error) {
	stats := RunStats{ExecutionMode: ExecutionModeIterativeHarvest, SemanticCallSites: callSites}
	windowBytes, err := normalizedWindowBytes(opts.WindowBytes)
	if err != nil {
		return Result{RunStats: stats}, err
	}
	stats.WindowBytes = windowBytes
	program, err := compileIterative(opts.Query, opts.Backend, opts.SemanticModelID, opts.SemanticCache, &stats, stages)
	if program != nil {
		program.runtime.maxCalls, program.runtime.maxCallsDefaultPaid = opts.MaxCalls, opts.MaxCallsDefaultPaid
	}
	if err != nil {
		return Result{RunStats: stats}, err
	}
	windows, err := input.NewWindowIterator(input.NewFramer(stdin, opts.InputMode), windowBytes)
	if err != nil {
		return Result{RunStats: stats}, err
	}
	result := Result{RunStats: stats}
	for {
		window, err := windows.Next(ctx)
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, err
		}
		recordWindowStats(&stats, window, false, false)
		stats.InputFrames += int64(len(window.Frames))
		for stage := range program.stages {
			if err := program.runStage(ctx, window.Frames, stage); err != nil {
				window.Release()
				program.runtime.releaseCollected()
				result.RunStats = stats
				return result, &RuntimeError{Frame: resolveErrorFrame(err, window.Frames), Err: err}
			}
		}
		for i := range window.Frames {
			frame := &window.Frames[i]
			runResult, err := program.execute(*frame, func(value any) error {
				result.Emitted, result.Last = true, value
				return output.WriteValue(stdout, value, opts.Output)
			})
			if err != nil {
				window.Release()
				program.runtime.releaseCollected()
				result.RunStats = stats
				return result, &RuntimeError{Frame: frame.Index + 1, Err: err}
			}
			if runResult.Emitted {
				result.Emitted, result.Last = true, runResult.Last
			}
			frame.Value = nil
		}
		window.Release()
		program.runtime.releaseCollected()
		result.RunStats = stats
	}
}

var _ = semantics.ReturnBool
