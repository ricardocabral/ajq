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
)

// iterativeProgram runs an AST-recognized linear predicate chain one call site
// at a time. reservation preserves the normal permissive harvest order for
// cap and result-error-prefix parity; overlay keeps stage results private until
// the entire window is known to be executable.
type iterativeProgram struct {
	stageProgram       *jq.Program
	executeProgram     *jq.Program
	reservation        *threePhaseProgram
	runtime            *semanticRuntime
	stages             []plan.CallID
	stageIndex         map[plan.CallID]int
	active             int
	overlay            map[semanticcache.Key]backend.Result
	reservationEntries []iterativeReservation
}

type iterativeReservation struct {
	key       semanticcache.Key
	judgement backend.Judgement
	frame     int64
}

type iterativeStageFailure struct {
	key   semanticcache.Key
	frame int64
	err   error
}

func (e *iterativeStageFailure) Error() string { return e.err.Error() }
func (e *iterativeStageFailure) Unwrap() error { return e.err }

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
	reservation, err := compileThreePhaseWithOptions(query, be, modelID, store)
	if err != nil {
		return nil, err
	}
	rt := newSemanticRuntime(be, modelID, store, instrumented.Plan)
	rt.stats = stats
	p := &iterativeProgram{
		reservation: reservation,
		runtime:     rt,
		stages:      make([]plan.CallID, len(stages.Stages)),
		stageIndex:  make(map[plan.CallID]int, len(stages.Stages)),
		overlay:     make(map[semanticcache.Key]backend.Result),
	}
	for i, stage := range stages.Stages {
		p.stages[i] = stage.CallID
	}
	for i, id := range p.stages {
		p.stageIndex[id] = i
	}
	p.stageProgram, err = jq.CompileWithOptions(instrumented.InstrumentedQuery, p.stageOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
	p.executeProgram, err = jq.CompileWithOptions(instrumented.InstrumentedQuery, p.executeOptions()...)
	if err != nil {
		return nil, &CompileError{Err: err}
	}
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
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, func(any, []any) any {
				return fmt.Errorf("unsupported iterative semantic operator")
			}))
		}
	}
	return opts
}

func (p *iterativeProgram) executeOptions() []gojq.CompilerOption {
	opts := make([]gojq.CompilerOption, 0, len(p.runtime.plannedOrder))
	for _, node := range p.runtime.plannedOrder {
		switch node.Op {
		case "sem_match":
			opts = append(opts, gojq.WithFunction(node.InternalName, node.Arity, node.Arity, p.executeMatch(node)))
		case "sem_classify":
			opts = append(opts, gojq.WithIterFunction(node.InternalName, node.Arity, node.Arity, p.executeClassify(node)))
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
			result, hit, err := p.lookup(judgement)
			if err != nil {
				return gojq.NewIter[any](err)
			}
			if hit {
				p.runtime.stats.CacheHits++
				return gojq.NewIter[any](result.Value)
			}
			p.runtime.appendJudgement(judgement)
			// The active gate is deliberately permissive until its one batch resolves.
			return gojq.NewIter[any](true)
		}
		result, _, err := p.lookup(judgement)
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
			return gojq.NewIter(p.labels(node)...)
		}
		judgement, err := p.runtime.judgementFromPlannedCall(node.ID, node.Op, value, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		if index == p.active {
			result, hit, err := p.lookup(judgement)
			if err != nil {
				return gojq.NewIter[any](err)
			}
			if hit {
				p.runtime.stats.CacheHits++
				return gojq.NewIter[any](result.Value)
			}
			p.runtime.appendJudgement(judgement)
			return gojq.NewIter(p.labels(node)...)
		}
		result, _, err := p.lookup(judgement)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		return gojq.NewIter[any](result.Value)
	}
}

func (p *iterativeProgram) labels(node plan.SemNode) []any {
	values := make([]any, len(node.Specs))
	for i := range node.Specs {
		values[i] = node.Specs[i]
	}
	return values
}

func (p *iterativeProgram) executeMatch(node plan.SemNode) func(any, []any) any {
	return func(value any, args []any) any {
		p.runtime.recordWitness(node.ID, semanticPhaseExecute, node.Op, node.Source)
		judgement, err := p.runtime.judgementFromCall(node.Op, value, args)
		if err != nil {
			return err
		}
		result, _, err := p.lookup(judgement)
		if err != nil {
			return err
		}
		return result.Value
	}
}

func (p *iterativeProgram) executeClassify(node plan.SemNode) func(any, []any) gojq.Iter {
	return func(value any, args []any) gojq.Iter {
		p.runtime.recordWitness(node.ID, semanticPhaseExecute, node.Op, node.Source)
		judgement, err := p.runtime.judgementFromPlannedCall(node.ID, node.Op, value, args)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		result, _, err := p.lookup(judgement)
		if err != nil {
			return gojq.NewIter[any](err)
		}
		return gojq.NewIter[any](result.Value)
	}
}

func (p *iterativeProgram) lookup(judgement backend.Judgement) (backend.Result, bool, error) {
	key, err := semanticcache.KeyForJudgement(judgement)
	if err != nil {
		return backend.Result{}, false, err
	}
	if result, ok := p.overlay[key]; ok {
		return result, true, nil
	}
	result, ok := p.runtime.cache.Get(key)
	return result, ok, nil
}

func (p *iterativeProgram) resetWindow() {
	p.runtime.releaseCollected()
	p.reservation.releaseWindow()
	for key := range p.overlay {
		delete(p.overlay, key)
	}
	p.reservationEntries = nil
}

// reserve records normal three-phase harvest order without resolving or writing
// cache. This makes max-call rejection conservative and gives result failures
// the same cache-prefix order as the reference executor.
func (p *iterativeProgram) reserve(ctx context.Context, frames []input.Frame) error {
	p.reservationEntries = p.reservationEntries[:0]
	seen := make(map[semanticcache.Key]struct{})
	for i, judgement := range p.reservation.runtime.collected {
		key, err := semanticcache.KeyForJudgement(judgement)
		if err != nil {
			return &semanticResolveError{frame: p.reservation.runtime.frameForCollected(i), executeBefore: -1, err: err}
		}
		if _, ok := p.runtime.cache.Get(key); ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		p.reservationEntries = append(p.reservationEntries, iterativeReservation{key: key, judgement: judgement, frame: p.reservation.runtime.frameForCollected(i)})
	}
	spent := 0
	if p.runtime.stats != nil {
		spent = p.runtime.stats.PostDedupBackendCalls
	}
	if err := checkMaxCalls(p.runtime.maxCalls, spent, len(p.reservationEntries), p.runtime.maxCallsDefaultPaid); err != nil {
		frame := int64(0)
		if len(p.reservationEntries) != 0 {
			frame = p.reservationEntries[0].frame
		}
		return &semanticResolveError{frame: frame, executeBefore: -1, err: err}
	}
	_ = ctx
	_ = frames
	return nil
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
		p.runtime.stats.HarvestedJudgements += len(p.runtime.collected) - before
	}
	return p.resolveStage(ctx)
}

// resolveStage dispatches one cache-deduplicated active-stage batch. Successful
// values enter only the overlay; shared-cache writes happen at the window commit.
func (p *iterativeProgram) resolveStage(ctx context.Context) error {
	if len(p.runtime.collected) == 0 {
		return nil
	}
	seen := make(map[semanticcache.Key]struct{}, len(p.runtime.collected))
	pending := make([]backend.Judgement, 0, len(p.runtime.collected))
	keys := make([]semanticcache.Key, 0, len(p.runtime.collected))
	frames := make([]int64, 0, len(p.runtime.collected))
	for i, judgement := range p.runtime.collected {
		key, err := semanticcache.KeyForJudgement(judgement)
		if err != nil {
			return &semanticResolveError{frame: p.runtime.frameForCollected(i), executeBefore: -1, err: err}
		}
		if _, ok := p.overlay[key]; ok {
			continue
		}
		if _, ok := p.runtime.cache.Get(key); ok {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		pending, keys, frames = append(pending, judgement), append(keys, key), append(frames, p.runtime.frameForCollected(i))
	}
	if len(pending) == 0 {
		return nil
	}
	if p.runtime.stats != nil {
		p.runtime.stats.PostDedupBackendCalls += len(pending)
	}
	results, err := p.runtime.backend.Judge(ctx, append([]backend.Judgement(nil), pending...))
	if err != nil {
		return &semanticResolveError{frame: frames[0], executeBefore: -1, err: err}
	}
	if len(results) != len(pending) {
		return &semanticResolveError{frame: frames[0], executeBefore: -1, err: fmt.Errorf("backend returned %d results for %d judgements", len(results), len(pending))}
	}
	for i, result := range results {
		if result.Error != "" {
			return &iterativeStageFailure{key: keys[i], frame: frames[i], err: fmt.Errorf("backend result %d for %s: %s", i, pending[i].Op, result.Error)}
		}
		if err := validateResult(pending[i], result); err != nil {
			return &iterativeStageFailure{key: keys[i], frame: frames[i], err: fmt.Errorf("backend result %d for %s: %w", i, pending[i].Op, err)}
		}
		p.overlay[keys[i]] = result
	}
	return nil
}

// completePrefix resolves only reservation keys before a stage-result failure.
// It intentionally uses the permissive reservation list, not actual survivors,
// because the normal three-phase harvest caches rejected-candidate descendants.
func (p *iterativeProgram) completePrefix(ctx context.Context, failing semanticcache.Key) (int, error) {
	cutoff := -1
	for i, entry := range p.reservationEntries {
		if entry.key == failing {
			cutoff = i
			break
		}
	}
	if cutoff < 0 {
		return -1, fmt.Errorf("iterative reservation missing failed judgement")
	}
	var pending []iterativeReservation
	for _, entry := range p.reservationEntries[:cutoff] {
		if _, ok := p.overlay[entry.key]; ok {
			continue
		}
		if _, ok := p.runtime.cache.Get(entry.key); ok {
			continue
		}
		pending = append(pending, entry)
	}
	if len(pending) == 0 {
		return cutoff, nil
	}
	if p.runtime.stats != nil {
		p.runtime.stats.PostDedupBackendCalls += len(pending)
	}
	judgements := make([]backend.Judgement, len(pending))
	for i := range pending {
		judgements[i] = pending[i].judgement
	}
	results, err := p.runtime.backend.Judge(ctx, judgements)
	if err != nil {
		return -1, &semanticResolveError{frame: pending[0].frame, executeBefore: -1, err: err}
	}
	if len(results) != len(pending) {
		return -1, &semanticResolveError{frame: pending[0].frame, executeBefore: -1, err: fmt.Errorf("backend returned %d results for %d judgements", len(results), len(pending))}
	}
	for i, result := range results {
		if result.Error != "" {
			return p.reservationIndex(pending[i].key), &iterativeStageFailure{key: pending[i].key, frame: pending[i].frame, err: fmt.Errorf("backend result %d for %s: %s", i, pending[i].judgement.Op, result.Error)}
		}
		if err := validateResult(pending[i].judgement, result); err != nil {
			return p.reservationIndex(pending[i].key), &iterativeStageFailure{key: pending[i].key, frame: pending[i].frame, err: fmt.Errorf("backend result %d for %s: %w", i, pending[i].judgement.Op, err)}
		}
		p.overlay[pending[i].key] = result
	}
	return cutoff, nil
}

func (p *iterativeProgram) reservationIndex(key semanticcache.Key) int {
	for i, entry := range p.reservationEntries {
		if entry.key == key {
			return i
		}
	}
	return -1
}

func (p *iterativeProgram) flush(cutoff int) {
	if cutoff < 0 || cutoff > len(p.reservationEntries) {
		cutoff = len(p.reservationEntries)
	}
	for _, entry := range p.reservationEntries[:cutoff] {
		if result, ok := p.overlay[entry.key]; ok {
			p.runtime.cache.Set(entry.key, result)
		}
	}
}

func (p *iterativeProgram) execute(frame input.Frame, emit func(any) error) (jq.RunResult, error) {
	p.runtime.resetWitnesses()
	result, err := p.executeProgram.Run(frame.Value, emit)
	if invariant := p.runtime.checkInvariant(semanticPhaseExecute); invariant != nil {
		err = invariant
	}
	return result, err
}

func (p *iterativeProgram) executePrefix(ctx context.Context, stdout io.Writer, opts Options, result *Result, frames []input.Frame, before int64) error {
	for i := range frames {
		if before >= 0 && frames[i].Index >= before {
			break
		}
		if err := ctxErr(ctx); err != nil {
			return err
		}
		frame := &frames[i]
		runResult, err := p.execute(*frame, func(value any) error {
			result.Emitted, result.Last = true, value
			return output.WriteValue(stdout, value, opts.Output)
		})
		if err != nil {
			return &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		if runResult.Emitted {
			result.Emitted, result.Last = true, runResult.Last
		}
		frame.Value = nil
	}
	return nil
}

func (p *iterativeProgram) processWindow(ctx context.Context, stdout io.Writer, opts Options, result *Result, frames []input.Frame) error {
	if err := p.reserve(ctx, frames); err != nil {
		return err
	}
	for stage := range p.stages {
		err := p.runStage(ctx, frames, stage)
		if err == nil {
			continue
		}
		var failed *iterativeStageFailure
		if !errors.As(err, &failed) {
			return err
		}
		cutoff, prefixErr := p.completePrefix(ctx, failed.key)
		if prefixErr != nil {
			if cutoff < 0 {
				return prefixErr
			}
			p.flush(cutoff)
			var prefixFailure *iterativeStageFailure
			if errors.As(prefixErr, &prefixFailure) {
				if err := p.executePrefix(ctx, stdout, opts, result, frames, prefixFailure.frame); err != nil {
					return err
				}
				return &semanticResolveError{frame: prefixFailure.frame, executeBefore: prefixFailure.frame, err: prefixFailure.err}
			}
			return prefixErr
		}
		p.flush(cutoff)
		if err := p.executePrefix(ctx, stdout, opts, result, frames, failed.frame); err != nil {
			return err
		}
		return &semanticResolveError{frame: failed.frame, executeBefore: failed.frame, err: failed.err}
	}
	p.flush(-1)
	return p.executePrefix(ctx, stdout, opts, result, frames, -1)
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
		program.resetWindow()
		program.reservation.beginWindow()
		window, nextErr := windows.NextWith(ctx, func(frame input.Frame) error {
			return program.reservation.harvestAppend(frame.Value, frame.Index)
		})
		var frameErr *input.WindowFrameError
		if errors.As(nextErr, &frameErr) {
			recordWindowStats(&stats, window, true, frameErr.Frame.Bytes > windowBytes)
			stats.InputFrames += int64(len(window.Frames) + 1)
		} else if errors.Is(nextErr, io.EOF) {
			return result, nil
		} else if nextErr != nil {
			program.resetWindow()
			return result, nextErr
		} else {
			recordWindowStats(&stats, window, false, false)
			stats.InputFrames += int64(len(window.Frames))
		}
		prefixErr := program.processWindow(ctx, stdout, opts, &result, window.Frames)
		prefixFrame := resolveErrorFrame(prefixErr, window.Frames)
		window.Release()
		program.resetWindow()
		result.RunStats = stats
		if prefixErr != nil {
			var runtimeErr *RuntimeError
			if errors.As(prefixErr, &runtimeErr) {
				return result, prefixErr
			}
			return result, &RuntimeError{Frame: prefixFrame, Err: prefixErr}
		}
		if frameErr != nil {
			return result, &RuntimeError{Frame: frameErr.Frame.Index + 1, Err: frameErr.Err}
		}
	}
}
