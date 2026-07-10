// Package local implements a semantic backend that talks to a warm localhost
// llama-server daemon over HTTP. It is deliberately separate from the core
// backend package so that the deterministic pure-jq execution path (which
// imports backend for its interface types) never gains a net/http dependency.
package local

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/promptkit"
	"github.com/ricardocabral/ajq/internal/schema"
)

const (
	// DefaultCompletionPath is the llama-server completion endpoint used to
	// obtain grammar/schema-constrained results.
	DefaultCompletionPath = "/completion"
	// DefaultNPredict bounds how many tokens the daemon may generate per
	// judgement. Semantic results are tiny (a bool, a label, a number, or a
	// short string) so a small budget is sufficient and keeps latency low.
	DefaultNPredict = 64
	// defaultTimeout bounds a single per-judgement HTTP request when the
	// caller-provided context has no deadline.
	defaultTimeout = 60 * time.Second
	// maxResponseBytes caps how much of a daemon response we read to avoid
	// unbounded memory use on a misbehaving server.
	maxResponseBytes = 1 << 20
)

// Backend is a backend.Backend implementation that sends judgements to a warm
// localhost llama-server daemon over HTTP and returns schema-validated results.
//
// It holds no global mutable client state: all request state is derived from
// the judgement and the immutable configuration fields below. One Judge call
// issues one localhost /completion request per judgement. When MaxConcurrency
// is greater than one, requests are dispatched through a bounded worker pool so
// a multi-slot llama-server can decode several prompts at once; result ordering
// still exactly mirrors the input batch (result i answers judgement i).
// MaxConcurrency <= 1 preserves the original sequential transport path.
// Whole-batch transport/system failures are returned as an error from Judge;
// per-item validation failures are reported via backend.Result.Error.
type Backend struct {
	// BaseURL is the localhost base URL of the llama-server daemon, e.g.
	// "http://127.0.0.1:8081". Required.
	BaseURL string
	// HTTPClient issues requests. When nil, a client with defaultTimeout is
	// used. Callers may inject a client (e.g. httptest) for tests.
	HTTPClient *http.Client
	// ModelID is the fallback model identity used when a judgement carries none.
	// It is included in the request payload so the daemon can (in future)
	// select the same model identity used by cache keys.
	ModelID string
	// APIKey, when set, is sent as Authorization: Bearer <APIKey> to
	// authenticated managed llama-server daemons.
	APIKey string
	// WarmFunc, when set, is invoked by Warm to ensure the daemon is running
	// (e.g. daemon.Manager.EnsureRunning). When nil, Warm is a no-op.
	WarmFunc func(ctx context.Context) error
	// TouchFunc, when set, records daemon activity for a single judgement so the
	// detached idle-reaper does not shut the daemon down mid-run (e.g.
	// daemon.Manager.TouchActivity). When nil, no activity is recorded (used in
	// tests against a standalone httptest server).
	TouchFunc func(ctx context.Context)
	// CompletionPath overrides the completion endpoint path. Defaults to
	// DefaultCompletionPath.
	CompletionPath string
	// NPredict overrides the per-judgement token budget. Defaults to
	// DefaultNPredict.
	NPredict int
	// MaxConcurrency bounds concurrent /completion requests within one Judge
	// batch. Values <= 1 use the exact sequential path.
	MaxConcurrency int
	// DisablePromptCache sends cache_prompt=false for callers that need isolated
	// timing without llama-server prompt/KV cache reuse. Normal production traffic
	// leaves this false so the daemon may reuse prompt cache state.
	DisablePromptCache bool

	// warmOnce guards a single lazy Warm invocation on the first Judge so the
	// daemon is spawned only when there is real semantic work. It is per-instance
	// state, not global.
	warmOnce sync.Once
	warmErr  error
}

// Ensure Backend satisfies the backend.Backend interface.
var _ backend.Backend = (*Backend)(nil)

// completionRequest is the JSON body sent to the llama-server /completion
// endpoint. It carries the deterministic prompt plus a json_schema constraint
// so the daemon returns a structurally valid result of the expected type.
type completionRequest struct {
	Model       string         `json:"model,omitempty"`
	Prompt      string         `json:"prompt"`
	NPredict    int            `json:"n_predict"`
	Temperature float64        `json:"temperature"`
	JSONSchema  map[string]any `json:"json_schema,omitempty"`
	CachePrompt bool           `json:"cache_prompt"`
}

// completionResponse is the subset of the llama-server /completion response we
// consume. The daemon returns the model's text in the "content" field.
type completionResponse struct {
	Content string `json:"content"`
}

// Warm ensures the underlying daemon is running by delegating to WarmFunc. When
// no WarmFunc is configured it is a no-op so the backend is usable in tests
// against an httptest.Server that is already listening.
func (b *Backend) Warm(ctx context.Context) error {
	if b.WarmFunc != nil {
		return b.WarmFunc(ctx)
	}
	return nil
}

// ensureWarm invokes Warm at most once per backend instance. The result is
// memoized so repeated Judge calls do not repeatedly spawn/health-check the
// daemon. A warm failure aborts the batch.
func (b *Backend) ensureWarm(ctx context.Context) error {
	if b.WarmFunc == nil {
		return nil
	}
	b.warmOnce.Do(func() {
		b.warmErr = b.WarmFunc(ctx)
	})
	return b.warmErr
}

// Judge sends each judgement to the daemon and returns results in batch order.
// Result i answers Judgement i. A transport/system failure aborts the whole
// batch with an error and no partial results; per-item validation failures are
// reported in backend.Result.Error for the corresponding index.
func (b *Backend) Judge(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	if strings.TrimSpace(b.BaseURL) == "" {
		return nil, fmt.Errorf("local backend base URL is empty")
	}
	if len(batch) == 0 {
		return nil, nil
	}
	// Lazily ensure the daemon is running only when there is real work to do.
	if err := b.ensureWarm(ctx); err != nil {
		return nil, err
	}
	if b.MaxConcurrency <= 1 {
		return b.judgeSequential(ctx, batch)
	}
	return b.judgeParallel(ctx, batch)
}

func (b *Backend) judgeSequential(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	results := make([]backend.Result, len(batch))
	for i, judgement := range batch {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		// Record activity per judgement so an active batch keeps the daemon warm
		// and is never reaped mid-run by the detached idle-reaper.
		if b.TouchFunc != nil {
			b.TouchFunc(ctx)
		}
		result, err := b.judgeOne(ctx, judgement)
		if err != nil {
			return nil, fmt.Errorf("local backend judgement %d (%s): %w", i, judgement.Op, err)
		}
		results[i] = result
	}
	return results, nil
}

func (b *Backend) judgeParallel(ctx context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	results := make([]backend.Result, len(batch))
	jobs := make(chan int)
	workers := b.MaxConcurrency
	if workers > len(batch) {
		workers = len(batch)
	}

	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	setErr := func(i int, judgement backend.Judgement, err error) {
		errMu.Lock()
		defer errMu.Unlock()
		if firstErr != nil {
			return
		}
		firstErr = fmt.Errorf("local backend judgement %d (%s): %w", i, judgement.Op, err)
		cancel()
	}

	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case i, ok := <-jobs:
					if !ok {
						return
					}
					judgement := batch[i]
					if err := ctx.Err(); err != nil {
						setErr(i, judgement, err)
						return
					}
					// Record activity per judgement so an active batch keeps the daemon warm
					// and is never reaped mid-run by the detached idle-reaper.
					if b.TouchFunc != nil {
						b.TouchFunc(ctx)
					}
					result, err := b.judgeOne(ctx, judgement)
					if err != nil {
						setErr(i, judgement, err)
						return
					}
					results[i] = result
				}
			}
		}()
	}

sendJobs:
	for i := range batch {
		select {
		case <-ctx.Done():
			break sendJobs
		case jobs <- i:
		}
	}
	close(jobs)
	wg.Wait()

	errMu.Lock()
	defer errMu.Unlock()
	if firstErr != nil {
		return nil, firstErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return results, nil
}

// judgeOne issues a single localhost completion request for one judgement. It
// returns an error only for whole-batch transport/system failures; malformed
// or type-invalid model output is surfaced as a per-item backend.Result.Error.
func (b *Backend) judgeOne(ctx context.Context, j backend.Judgement) (backend.Result, error) {
	// Resolve the deterministic output constraint once; reuse it for both the
	// request json_schema and post-decode validation so the request sent and
	// the value accepted share a single source of truth. An inconsistent
	// op/schema contract is a per-item failure, not a transport error.
	constraint, err := schema.ForJudgement(j)
	if err != nil {
		return backend.Result{Error: err.Error()}, nil
	}

	body, err := json.Marshal(b.buildRequest(j, constraint))
	if err != nil {
		return backend.Result{}, fmt.Errorf("marshal request: %w", err)
	}

	url := strings.TrimRight(b.BaseURL, "/") + b.completionPath()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return backend.Result{}, fmt.Errorf("build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(b.APIKey); key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	}
	httpReq.Header.Set("Accept", "application/json")

	resp, err := b.client().Do(httpReq)
	if err != nil {
		return backend.Result{}, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return backend.Result{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return backend.Result{}, fmt.Errorf("daemon returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var completion completionResponse
	if err := json.Unmarshal(respBody, &completion); err != nil {
		return backend.Result{}, fmt.Errorf("decode completion response: %w", err)
	}

	value, verr := promptkit.CoerceResult(constraint, completion.Content)
	if verr != nil {
		return backend.Result{Error: verr.Error()}, nil
	}
	return backend.Result{Value: value}, nil
}

// buildRequest constructs the deterministic completion request for a judgement,
// carrying op, return type, schema constraints, specs, model id, and canonical
// value context.
func (b *Backend) buildRequest(j backend.Judgement, constraint schema.Constraint) completionRequest {
	model := j.ModelID
	if strings.TrimSpace(model) == "" {
		model = b.ModelID
	}
	npredict := b.NPredict
	if npredict <= 0 {
		npredict = DefaultNPredict
	}
	return completionRequest{
		Model:       model,
		Prompt:      promptkit.BuildPrompt(j, constraint),
		NPredict:    npredict,
		Temperature: 0,
		JSONSchema:  constraint.JSONSchema(),
		CachePrompt: !b.DisablePromptCache,
	}
}

// completionPath returns the configured completion endpoint path or the default.
func (b *Backend) completionPath() string {
	if strings.TrimSpace(b.CompletionPath) == "" {
		return DefaultCompletionPath
	}
	if !strings.HasPrefix(b.CompletionPath, "/") {
		return "/" + b.CompletionPath
	}
	return b.CompletionPath
}

// client returns the configured HTTP client or a default with a bounded timeout.
func (b *Backend) client() *http.Client {
	if b.HTTPClient != nil {
		return b.HTTPClient
	}
	return &http.Client{Timeout: defaultTimeout}
}
