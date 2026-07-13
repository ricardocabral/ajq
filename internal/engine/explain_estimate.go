package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/plan"
)

// ExplainEstimateAvailable statuses describe whether explain estimates were computed.
const (
	ExplainEstimateAvailable              = "available"
	ExplainEstimateUnavailablePureJQ      = "unavailable: pure jq"
	ExplainEstimateUnavailableNoInput     = "unavailable: no input"
	ExplainEstimateUnavailableInvalid     = "unavailable: invalid input"
	ExplainEstimateUnavailableHarvest     = "unavailable: unsupported harvest"
	ExplainEstimateUnavailableInterleaved = "unavailable: interleaved fallback"
	ExplainEstimateUnavailableUserStream  = "unavailable: user stream"
)

// ExplainEstimate reports the exact units surfaced by semantic --explain. It is
// intentionally based on the same mock harvest/resolve path as split execution,
// so post-dedup judgements match the distinct judgements sent to the mock
// resolver for the supplied frames.
type ExplainEstimate struct {
	StaticCallSites     int
	Status              string
	Reason              string
	InputFrames         int
	HarvestedJudgements int
	PostDedupJudgements int
	MockJudgeBatches    int
	SemanticPlan        plan.Plan
	RewrittenQuery      string
}

// ExplainOptions controls execution-mode-sensitive explain estimation.
type ExplainOptions struct {
	InputMode input.Mode
	// Stream reports that the user selected inline execution for a supported
	// semantic plan. Such a run cannot make a window harvest/dedup estimate and
	// must not consume stdin while explaining.
	Stream bool
}

// EstimateExplain runs semantic queries through the deterministic mock
// harvest/resolve path to estimate post-dedup backend judgements. Unsupported or
// invalid-input cases return stable unavailable statuses rather than hiding the
// static semantic plan.
func EstimateExplain(ctx context.Context, query string, stdin io.Reader, mode input.Mode) ExplainEstimate {
	return EstimateExplainWithOptions(ctx, query, stdin, ExplainOptions{InputMode: mode})
}

// EstimateExplainWithOptions runs explain estimation using the selected
// execution mode. Stream mode reports its unavailable estimate without reading
// stdin because inline execution has no cross-frame harvest phase.
func EstimateExplainWithOptions(ctx context.Context, query string, stdin io.Reader, opts ExplainOptions) ExplainEstimate {
	rewrittenQuery, err := rewriteQuery(query)
	if err != nil {
		return ExplainEstimate{Status: ExplainEstimateUnavailableHarvest, Reason: err.Error(), RewrittenQuery: query}
	}
	semanticPlan, diagnostics := plan.Build(rewrittenQuery)
	estimate := ExplainEstimate{
		StaticCallSites: len(semanticPlan.Semantic),
		Status:          ExplainEstimateUnavailablePureJQ,
		SemanticPlan:    semanticPlan,
		RewrittenQuery:  rewrittenQuery,
	}
	if blockingSemanticDiagnostics(diagnostics) {
		estimate.Status = ExplainEstimateUnavailableHarvest
		estimate.Reason = firstDiagnosticMessage(diagnostics)
		return estimate
	}
	if semanticPlan.Deterministic || len(semanticPlan.Semantic) == 0 {
		return estimate
	}
	if semanticPlan.RequiresInterleaved {
		estimate.Status = ExplainEstimateUnavailableInterleaved
		estimate.Reason = "query uses interleaved fallback for gated unbounded value ops"
		return estimate
	}
	if opts.Stream {
		estimate.Status = ExplainEstimateUnavailableUserStream
		estimate.Reason = "user-selected inline execution has no cross-frame batching or pre-resolve dedup estimate"
		return estimate
	}

	be := &backend.MockBackend{}
	program, err := compileThreePhaseWithOptions(rewrittenQuery, be, "", nil)
	if err != nil {
		estimate.Status = ExplainEstimateUnavailableHarvest
		estimate.Reason = err.Error()
		return estimate
	}

	framer := input.NewFramer(stdin, opts.InputMode)
	for {
		frame, err := framer.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			estimate.Status = ExplainEstimateUnavailableInvalid
			estimate.Reason = err.Error()
			return estimate
		}
		estimate.InputFrames++
		if err := program.harvest(frame.Value); err != nil {
			estimate.Status = ExplainEstimateUnavailableHarvest
			estimate.Reason = err.Error()
			return estimate
		}
		estimate.HarvestedJudgements += len(program.runtime.collected)
		if err := program.resolve(ctx); err != nil {
			estimate.Status = ExplainEstimateUnavailableHarvest
			estimate.Reason = err.Error()
			return estimate
		}
	}

	if estimate.InputFrames == 0 {
		estimate.Status = ExplainEstimateUnavailableNoInput
		return estimate
	}
	estimate.Status = ExplainEstimateAvailable
	estimate.PostDedupJudgements = len(be.Inputs())
	estimate.MockJudgeBatches = be.CallCount()
	return estimate
}

func firstDiagnosticMessage(diagnostics []plan.Diagnostic) string {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == plan.SeverityError {
			return diagnostic.Message
		}
	}
	return strings.TrimSpace(fmt.Sprint(diagnostics))
}
