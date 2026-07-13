// Package engine wires ajq's stdin framing, pure-jq evaluation, and output
// serialization together. Phase 0.2 intentionally has no semantic backend.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/desugar"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/plan"
)

// DefaultWindowBytes is the byte budget used for supported three-phase
// semantic execution when Options.WindowBytes is left at its zero value.
const DefaultWindowBytes int64 = 256 * 1024

// Options controls one pure-jq execution.
type Options struct {
	Query               string
	InputMode           input.Mode
	Output              output.Options
	ExitStatus          bool
	Backend             backend.Backend
	SemanticModelID     string
	SemanticCache       *semanticcache.Store
	MaxCalls            int
	MaxCallsDefaultPaid bool
	// WindowBytes bounds complete-frame windows for supported three-phase
	// semantic execution. Zero selects DefaultWindowBytes; negative values are
	// rejected. Pure-jq and interleaved execution do not use this setting.
	WindowBytes int64
}

// ErrInvalidWindowBytes reports a negative direct engine window budget.
var ErrInvalidWindowBytes = errors.New("window byte budget must not be negative")

func normalizedWindowBytes(windowBytes int64) (int64, error) {
	if windowBytes < 0 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidWindowBytes, windowBytes)
	}
	if windowBytes == 0 {
		return DefaultWindowBytes, nil
	}
	return windowBytes, nil
}

// ExecutionMode identifies the executor path used for a run.
type ExecutionMode string

const (
	ExecutionModePureJQ             ExecutionMode = "pure-jq"
	ExecutionModeThreePhaseWindowed ExecutionMode = "three-phase-windowed"
	ExecutionModeInterleaved        ExecutionMode = "interleaved"
)

// RunStats summarizes per-run accounting. InputFrames counts frames read from
// stdin. SemanticCallSites counts static semantic plan nodes. HarvestedJudgements
// counts semantic judgements collected during split harvest. PostDedupBackendCalls
// counts individual judgements sent to Backend.Judge after cache hits and
// duplicate deduplication. CacheHits counts cache lookups that avoided a backend
// judgement; duplicate dedup skips are not cache hits. WindowBytes, WindowCount,
// and OversizedWindowCount are populated only for three-phase-windowed runs.
// Elapsed is wall time spent in Execute.
type RunStats struct {
	ExecutionMode         ExecutionMode
	WindowBytes           int64
	WindowCount           int64
	OversizedWindowCount  int64
	InputFrames           int64
	SemanticCallSites     int
	HarvestedJudgements   int
	PostDedupBackendCalls int
	CacheHits             int
	Elapsed               time.Duration
}

// Result summarizes all output emitted by an execution.
type Result struct {
	Emitted  bool
	Last     any
	RunStats RunStats
}

// MaxCallsExceededError reports that a semantic run would exceed its configured
// backend judgement cap. It is raised before issuing the judgement that would
// cross the cap.
type MaxCallsExceededError struct {
	Cap         int
	Needed      int
	PaidDefault bool
}

func (e *MaxCallsExceededError) Error() string {
	msg := fmt.Sprintf("max calls cap exceeded: cap %d, run needs %d post-dedup backend judgements; aborting before issuing backend call %d. Run with --explain to estimate calls before spending, or raise --max-calls (0 = unlimited)", e.Cap, e.Needed, e.Cap+1)
	if e.PaidDefault {
		msg += "; this cap is the paid-backend default"
	}
	return msg
}

func checkMaxCalls(maxCalls int, spent int, pending int, paidDefault bool) error {
	if maxCalls <= 0 || pending <= 0 {
		return nil
	}
	needed := spent + pending
	if needed <= maxCalls {
		return nil
	}
	return &MaxCallsExceededError{Cap: maxCalls, Needed: needed, PaidDefault: paidDefault}
}

// CompileError wraps jq parse/compile failures.
type CompileError struct{ Err error }

func (e *CompileError) Error() string { return fmt.Sprintf("compile error: %v", e.Err) }
func (e *CompileError) Unwrap() error { return e.Err }

// RuntimeError wraps gojq iterator runtime failures.
type RuntimeError struct {
	Frame int64
	Err   error
}

func (e *RuntimeError) Error() string {
	return fmt.Sprintf("runtime error in frame %d: %v", e.Frame, e.Err)
}
func (e *RuntimeError) Unwrap() error { return e.Err }

// Execute compiles the query once, then runs it independently for each input
// frame. This preserves NDJSON streaming: each top-level value is processed and
// emitted before the next frame is read.
func Execute(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options) (result Result, err error) {
	start := time.Now()
	defer func() {
		result.RunStats.Elapsed = positiveDurationSince(start)
	}()

	rewrittenQuery, err := rewriteQuery(opts.Query)
	if err != nil {
		return Result{}, &CompileError{Err: err}
	}
	opts.Query = rewrittenQuery

	semanticPlan, diagnostics := plan.Build(opts.Query)
	if blockingSemanticDiagnostics(diagnostics) {
		return Result{}, &PlanError{Diagnostics: diagnostics}
	}
	if len(diagnostics) == 0 && !semanticPlan.Deterministic {
		if semanticPlan.RequiresInterleaved {
			return executeInterleaved(ctx, stdin, stdout, opts, len(semanticPlan.Semantic))
		}
		return executeThreePhase(ctx, stdin, stdout, opts, len(semanticPlan.Semantic))
	}

	program, err := jq.Compile(opts.Query)
	if err != nil {
		return Result{}, &CompileError{Err: err}
	}

	stats := RunStats{ExecutionMode: ExecutionModePureJQ, SemanticCallSites: len(semanticPlan.Semantic)}
	return executeProgram(ctx, stdin, stdout, opts, program, &stats)
}

func rewriteQuery(query string) (string, error) {
	return desugar.Rewrite(query)
}

func executeInterleaved(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, callSites int) (Result, error) {
	stats := RunStats{ExecutionMode: ExecutionModeInterleaved, SemanticCallSites: callSites}
	program, err := compileInterleavedWithStats(ctx, opts.Query, opts.Backend, opts.SemanticModelID, opts.SemanticCache, &stats, opts.MaxCalls, opts.MaxCallsDefaultPaid)
	if err != nil {
		return Result{RunStats: stats}, err
	}
	return executeProgram(ctx, stdin, stdout, opts, program, &stats)
}

func executeThreePhase(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, callSites int) (Result, error) {
	stats := RunStats{ExecutionMode: ExecutionModeThreePhaseWindowed, SemanticCallSites: callSites}
	windowBytes, err := normalizedWindowBytes(opts.WindowBytes)
	if err == nil {
		stats.WindowBytes = windowBytes
	}
	if err != nil {
		return Result{RunStats: stats}, err
	}
	program, err := compileThreePhaseWithStats(opts.Query, opts.Backend, opts.SemanticModelID, opts.SemanticCache, &stats)
	if program != nil {
		program.runtime.maxCalls = opts.MaxCalls
		program.runtime.maxCallsDefaultPaid = opts.MaxCallsDefaultPaid
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
		program.beginWindow()
		window, err := windows.NextWith(ctx, func(frame input.Frame) error {
			return program.harvestAppend(frame.Value, frame.Index)
		})
		var harvestErr *input.WindowFrameError
		if errors.As(err, &harvestErr) {
			// The iterator stops at the failing frame. Its returned window is the
			// already-harvested prefix, which must retain the historical output
			// behavior before that frame's error is reported.
			recordWindowStats(&stats, window, true, harvestErr.Frame.Bytes > windowBytes)
			stats.InputFrames += int64(len(window.Frames) + 1)
			prefixErr := resolveAndExecuteWindow(ctx, stdout, opts, program, &result, window.Frames)
			window.Release()
			program.releaseWindow()
			result.RunStats = stats
			if prefixErr != nil {
				return result, prefixErr
			}
			return result, &RuntimeError{Frame: harvestErr.Frame.Index + 1, Err: harvestErr.Err}
		}
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			program.releaseWindow()
			if ctxErr(ctx) != nil {
				return result, err
			}
			return result, fmt.Errorf("input error: %w", err)
		}

		recordWindowStats(&stats, window, false, false)
		stats.InputFrames += int64(len(window.Frames))
		result.RunStats = stats
		err = resolveAndExecuteWindow(ctx, stdout, opts, program, &result, window.Frames)
		window.Release()
		program.releaseWindow()
		result.RunStats = stats
		if err != nil {
			return result, err
		}
	}
}

func recordWindowStats(stats *RunStats, window input.Window, harvestFailed, failedOversized bool) {
	if stats == nil || (len(window.Frames) == 0 && !harvestFailed) {
		return
	}
	stats.WindowCount++
	if window.Oversized || failedOversized {
		stats.OversizedWindowCount++
	}
}

func resolveAndExecuteWindow(ctx context.Context, stdout io.Writer, opts Options, program *threePhaseProgram, result *Result, frames []input.Frame) error {
	err := program.resolve(ctx)
	limit := len(frames)
	if err != nil {
		var resolveErr *semanticResolveError
		if !errors.As(err, &resolveErr) || resolveErr.executeBefore < 0 {
			return &RuntimeError{Frame: resolveErrorFrame(err, frames), Err: err}
		}
		limit = 0
		for limit < len(frames) && frames[limit].Index < resolveErr.executeBefore {
			limit++
		}
	}
	for i := 0; i < limit; i++ {
		frame := &frames[i]
		runResult, err := program.execute(frame.Value, func(value any) error {
			result.Emitted = true
			result.Last = value
			return output.WriteValue(stdout, value, opts.Output)
		})
		if err != nil {
			return &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		if runResult.Emitted {
			result.Emitted = true
			result.Last = runResult.Last
		}
		// A window may contain a large frame; drop its value as soon as ordered
		// execution is done rather than retaining it until the next window.
		frame.Value = nil
	}
	if err != nil {
		return &RuntimeError{Frame: resolveErrorFrame(err, frames), Err: err}
	}
	return nil
}

func resolveErrorFrame(err error, frames []input.Frame) int64 {
	var resolveErr *semanticResolveError
	if errors.As(err, &resolveErr) {
		return resolveErr.frame + 1
	}
	if len(frames) != 0 {
		return frames[0].Index + 1
	}
	return 1
}

func executeProgram(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, program *jq.Program, stats *RunStats) (Result, error) {
	if stats == nil {
		stats = &RunStats{}
	}
	framer := input.NewFramer(stdin, opts.InputMode)
	result := Result{RunStats: *stats}
	for {
		if err := ctxErr(ctx); err != nil {
			return result, err
		}

		frame, err := framer.Next()
		if errors.Is(err, io.EOF) {
			return result, nil
		}
		if err != nil {
			return result, fmt.Errorf("input error: %w", err)
		}
		stats.InputFrames++
		result.RunStats = *stats

		runResult, err := program.Run(frame.Value, func(value any) error {
			result.Emitted = true
			result.Last = value
			return output.WriteValue(stdout, value, opts.Output)
		})
		result.RunStats = *stats
		if err != nil {
			return result, &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		if runResult.Emitted {
			result.Emitted = true
			result.Last = runResult.Last
		}
	}
}

// ExitStatusCode returns jq-compatible --exit-status results for a successful
// execution: 0 for a truthy last output, 1 for false/null, and 4 for no output.
func ExitStatusCode(result Result) int {
	if !result.Emitted {
		return 4
	}
	if result.Last == nil {
		return 1
	}
	if b, ok := result.Last.(bool); ok && !b {
		return 1
	}
	return 0
}

func ctxErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func positiveDurationSince(start time.Time) time.Duration {
	elapsed := time.Since(start)
	if elapsed <= 0 {
		return time.Nanosecond
	}
	return elapsed
}
