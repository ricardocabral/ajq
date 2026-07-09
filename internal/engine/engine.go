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
}

// RunStats summarizes per-run accounting. InputFrames counts frames read from
// stdin. SemanticCallSites counts static semantic plan nodes. HarvestedJudgements
// counts semantic judgements collected during split harvest. PostDedupBackendCalls
// counts individual judgements sent to Backend.Judge after cache hits and
// duplicate-in-frame deduplication. CacheHits counts cache lookups that avoided a
// backend judgement; duplicate dedup skips are not cache hits. Elapsed is wall
// time spent in Execute.
type RunStats struct {
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

	stats := RunStats{SemanticCallSites: len(semanticPlan.Semantic)}
	return executeProgram(ctx, stdin, stdout, opts, program, &stats)
}

func rewriteQuery(query string) (string, error) {
	return desugar.Rewrite(query)
}

func executeInterleaved(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, callSites int) (Result, error) {
	stats := RunStats{SemanticCallSites: callSites}
	program, err := compileInterleavedWithStats(ctx, opts.Query, opts.Backend, opts.SemanticModelID, opts.SemanticCache, &stats, opts.MaxCalls, opts.MaxCallsDefaultPaid)
	if err != nil {
		return Result{RunStats: stats}, err
	}
	return executeProgram(ctx, stdin, stdout, opts, program, &stats)
}

func executeThreePhase(ctx context.Context, stdin io.Reader, stdout io.Writer, opts Options, callSites int) (Result, error) {
	stats := RunStats{SemanticCallSites: callSites}
	program, err := compileThreePhaseWithStats(opts.Query, opts.Backend, opts.SemanticModelID, opts.SemanticCache, &stats)
	if program != nil {
		program.runtime.maxCalls = opts.MaxCalls
		program.runtime.maxCallsDefaultPaid = opts.MaxCallsDefaultPaid
	}
	if err != nil {
		return Result{RunStats: stats}, err
	}

	framer := input.NewFramer(stdin, opts.InputMode)
	result := Result{RunStats: stats}
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
		result.RunStats = stats

		if err := program.harvest(frame.Value); err != nil {
			result.RunStats = stats
			return result, &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		result.RunStats = stats
		if err := program.resolve(ctx); err != nil {
			result.RunStats = stats
			return result, &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		result.RunStats = stats
		runResult, err := program.execute(frame.Value, func(value any) error {
			result.Emitted = true
			result.Last = value
			return output.WriteValue(stdout, value, opts.Output)
		})
		if err != nil {
			return result, &RuntimeError{Frame: frame.Index + 1, Err: err}
		}
		if runResult.Emitted {
			result.Emitted = true
			result.Last = runResult.Last
		}
	}
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
