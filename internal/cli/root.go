package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/itchyny/gojq"
	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/desugar"
	"github.com/ricardocabral/ajq/internal/engine"
	"github.com/ricardocabral/ajq/internal/explain"
	"github.com/ricardocabral/ajq/internal/input"
	"github.com/ricardocabral/ajq/internal/jq"
	"github.com/ricardocabral/ajq/internal/output"
	"github.com/ricardocabral/ajq/internal/plan"
	"github.com/ricardocabral/ajq/internal/pricing"
	"github.com/ricardocabral/ajq/internal/semantics"
	"github.com/ricardocabral/ajq/internal/version"
	"github.com/spf13/cobra"
)

// Options contains injectable command dependencies and streams.
type Options struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
	// Daemon injects the daemon controller used by `ajq daemon` subcommands.
	// When nil, a default controller backed by daemon.NewManager is used.
	Daemon DaemonController
	// LocalBackend injects the semantic backend used for `--backend local`.
	// When nil, a real localhost backend wired to a daemon.Manager is built.
	// Tests inject a fake (or an httptest-backed LocalBackend) to avoid
	// spawning a real daemon or requiring a model.
	LocalBackend backend.Backend
	// Provision injects the provisioning controller used by `ajq provision` and
	// local backend startup. When nil, a default provision.Provisioner is used.
	// Tests inject a fake to avoid real filesystem/PATH/network access.
	Provision ProvisionController
}

// ExitError carries an intentional process exit code, including jq-compatible
// --exit-status false/null and empty-output outcomes that should not print an
// additional error line.
type ExitError struct {
	Code   int
	Silent bool
	Err    error
}

func (e *ExitError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return fmt.Sprintf("exit status %d", e.Code)
}

func (e *ExitError) Unwrap() error { return e.Err }

// ExitCode returns the process exit code represented by err.
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}
	return 1
}

// NewRootCommand builds the ajq root command.
func NewRootCommand(opts Options) *cobra.Command {
	var nullInput bool
	var rawInput bool
	var compactOutput bool
	var rawOutput bool
	var exitStatus bool
	var explainMode bool
	var statsMode bool
	var backendName string
	var modelID string
	var baseURL string
	var maxCalls int
	var cloud bool
	var noCache bool

	cmd := &cobra.Command{
		Use:   "ajq [query]",
		Short: "jq-like stream processor for adaptive structured data queries",
		Long: `ajq runs ordinary jq queries deterministically and uses a semantic backend
only for explicit semantic operators. Start with --backend mock to safely
exercise semantic query syntax without a model, network access, or API key.`,
		Example: `  # Pure jq: deterministic and no backend required.
  printf '{"users":[{"name":"Ada"}]}' | ajq -r '.users[].name'

  # Safe semantic probe: mock is deterministic and needs no model or network.
  printf '[{"id":1,"msg":"please keep this"}]' | ajq --backend mock -c '.[] | select(.msg =~ "keep") | .id'

  # Inspect the semantic plan and estimated calls before running a query.
  printf '[{"msg":"refund demanded"}]' | ajq --backend mock --explain '.[] | select(.msg =~ "angry/frustrated") | .msg'`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args: func(cmd *cobra.Command, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("missing query: provide a jq-like query argument")
			}
			if len(args) > 1 {
				return fmt.Errorf("too many arguments for query %q: expected exactly one query", args[0])
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			query := args[0]
			if strings.TrimSpace(query) == "" {
				return fmt.Errorf("query %q is empty", query)
			}

			flagValues, err := backendFlagValues(cmd, backendName, modelID, baseURL, maxCalls, cloud, noCache)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			if err := validateExplicitBackend(flagValues); err != nil {
				return &ExitError{Code: 2, Err: err}
			}

			if explainMode {
				rewrittenQuery, err := desugar.Rewrite(query)
				if err != nil {
					return &ExitError{Code: 3, Err: fmt.Errorf("query %q compile error: %w", query, err)}
				}
				semanticPlan, diagnostics := plan.Build(rewrittenQuery)
				for _, diagnostic := range diagnostics {
					if diagnostic.Severity == plan.SeverityError {
						if diagnostic.Code == plan.DiagnosticParseError {
							return &ExitError{Code: 3, Err: fmt.Errorf("query %q compile error: %s", query, diagnostic.Message)}
						}
						return &ExitError{Code: 3, Err: fmt.Errorf("query %q plan error: %s", query, diagnostic.Message)}
					}
				}
				if err := validateExplainCompile(rewrittenQuery, semanticPlan.Deterministic); err != nil {
					return &ExitError{Code: 3, Err: fmt.Errorf("query %q compile error: %w", query, err)}
				}
				explainPlan := explain.Plan{Query: rewrittenQuery, SemanticPlan: &semanticPlan}
				if !semanticPlan.Deterministic {
					mode := input.ModeAuto
					if nullInput {
						mode = input.ModeNull
					} else if rawInput {
						mode = input.ModeRaw
					}
					estimateInput := cmd.InOrStdin()
					if mode != input.ModeNull {
						data, err := io.ReadAll(cmd.InOrStdin())
						if err != nil {
							return &ExitError{Code: 3, Err: fmt.Errorf("query %q explain input error: %w", query, err)}
						}
						estimateInput = strings.NewReader(string(data))
					}
					estimate := engine.EstimateExplain(cmd.Context(), rewrittenQuery, estimateInput, mode)
					explainPlan.Estimate, err = explainEstimateFromEngine(cmd, flagValues, rewrittenQuery, estimate)
					if err != nil {
						return &ExitError{Code: 2, Err: err}
					}
				}
				return explain.Write(cmd.OutOrStdout(), explainPlan)
			}

			mode := input.ModeAuto
			if nullInput {
				mode = input.ModeNull
			} else if rawInput {
				mode = input.ModeRaw
			}

			resolution, err := resolveBackendForQuery(cmd, query, flagValues, opts)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}

			result, err := engine.Execute(cmd.Context(), cmd.InOrStdin(), cmd.OutOrStdout(), engine.Options{
				Query:     query,
				InputMode: mode,
				Output: output.Options{
					Compact: compactOutput,
					Raw:     rawOutput,
				},
				ExitStatus:          exitStatus,
				Backend:             resolution.Backend,
				SemanticModelID:     resolution.Model,
				SemanticCache:       resolution.Cache,
				MaxCalls:            resolution.MaxCalls,
				MaxCallsDefaultPaid: resolution.MaxCallsDefaultPaid,
			})
			if err != nil {
				code := 1
				var compileErr *engine.CompileError
				var runtimeErr *engine.RuntimeError
				switch {
				case errors.As(err, &compileErr):
					code = 3
				case errors.As(err, &runtimeErr):
					code = 5
				}
				return &ExitError{Code: code, Err: fmt.Errorf("query %q %w", query, err)}
			}
			if statsMode {
				if err := printRunStats(cmd.ErrOrStderr(), result.RunStats, resolution.Paid, resolution.Model, len(query), resolution.DefaultMaxOutputTokens); err != nil {
					return &ExitError{Code: 1, Err: err}
				}
			}
			if exitStatus {
				if code := engine.ExitStatusCode(result); code != 0 {
					return &ExitError{Code: code, Silent: true}
				}
			}
			return nil
		},
	}

	cmd.SetIn(defaultReader(opts.Stdin))
	cmd.SetOut(defaultWriter(opts.Stdout, io.Discard))
	cmd.SetErr(defaultWriter(opts.Stderr, io.Discard))
	cmd.Version = version.Version
	cmd.SetVersionTemplate("ajq {{.Version}}\n")
	cmd.Flags().BoolP("version", "v", false, "print the version and exit")
	cmd.Flags().BoolVarP(&nullInput, "null-input", "n", false, "use null as the single input value instead of reading stdin")
	cmd.Flags().BoolVarP(&rawInput, "raw-input", "R", false, "read each input line as a string")
	cmd.Flags().BoolVarP(&compactOutput, "compact-output", "c", false, "emit compact JSON output")
	cmd.Flags().BoolVarP(&rawOutput, "raw-output", "r", false, "emit strings without JSON quoting")
	cmd.Flags().BoolVarP(&exitStatus, "exit-status", "e", false, "set exit status based on the last output value")
	cmd.Flags().BoolVar(&explainMode, "explain", false, "print deterministic/semantic execution plan and exit without executing the query")
	cmd.Flags().BoolVar(&statsMode, "stats", false, "print run statistics to stderr after a successful run")
	cmd.Flags().StringVar(&backendName, "backend", "", backendFlagHelp())
	cmd.Flags().StringVar(&modelID, "model", "", "semantic model id or alias for the selected backend")
	cmd.Flags().StringVar(&baseURL, "base-url", "", "base URL for HTTP semantic backends")
	cmd.Flags().IntVar(&maxCalls, "max-calls", 0, "maximum post-dedup backend judgements before aborting (0 = unlimited; paid backends default to 100, local/ollama/mock default to unlimited)")
	cmd.Flags().BoolVar(&cloud, "cloud", false, "select the Anthropic cloud semantic backend (equivalent to --backend anthropic)")
	cmd.Flags().BoolVar(&noCache, "no-cache", false, "disable persistent on-disk judgement cache for this run")

	cmd.AddCommand(newCacheCommand())
	cmd.AddCommand(newCapabilitiesCommand())
	cmd.AddCommand(newExamplesCommand())
	cmd.AddCommand(newDaemonCommand(opts))
	cmd.AddCommand(newModelsCommand(opts))
	cmd.AddCommand(newProvisionCommand(opts))

	return cmd
}

func backendFlagValues(cmd *cobra.Command, backendName, modelID, baseURL string, maxCalls int, cloud, noCache bool) (config.Values, error) {
	var values config.Values
	flags := cmd.Flags()
	backendChanged := flags.Changed("backend")
	if backendChanged {
		values.Backend = backendName
		values.BackendSet = true
	}
	if flags.Changed("model") {
		values.Model = modelID
		values.ModelSet = true
	}
	if flags.Changed("base-url") {
		values.BaseURL = baseURL
		values.BaseURLSet = true
	}
	if flags.Changed("max-calls") {
		if maxCalls < 0 {
			return config.Values{}, fmt.Errorf("--max-calls must be non-negative")
		}
		values.MaxCalls = maxCalls
		values.MaxCallsSet = true
	}
	if cloud {
		if backendChanged && backendName != "" && backendName != "anthropic" {
			return config.Values{}, fmt.Errorf("--cloud conflicts with --backend %q", backendName)
		}
		values.Backend = "anthropic"
		values.BackendSet = true
	}
	if flags.Changed("no-cache") {
		values.NoCache = noCache
		values.NoCacheSet = true
	}
	return values, nil
}

func validateExplicitBackend(flags config.Values) error {
	if !flags.BackendSet || flags.Backend == "" {
		return nil
	}
	if _, ok := lookupBackend(flags.Backend); !ok {
		return unknownBackendError(flags.Backend)
	}
	return nil
}

type backendResolution struct {
	Backend                backend.Backend
	Cache                  *semanticcache.Store
	Model                  string
	MaxCalls               int
	MaxCallsDefaultPaid    bool
	Paid                   bool
	DefaultMaxOutputTokens int
}

func resolveBackendForQuery(cmd *cobra.Command, query string, flags config.Values, opts Options) (backendResolution, error) {
	requiresBackend := queryRequiresBackend(query)
	if !requiresBackend {
		return backendResolution{}, nil
	}

	fileValues, err := config.LoadWithOptions(config.LoadOptions{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return backendResolution{}, err
	}
	envValues, err := config.Env(os.Getenv)
	if err != nil {
		return backendResolution{}, err
	}
	settings := config.Resolve(flags, envValues, fileValues, config.Values{})
	if settings.Backend == "" {
		return backendResolution{}, noBackendError()
	}
	registration, ok := lookupBackend(settings.Backend)
	if !ok {
		return backendResolution{}, unknownBackendError(settings.Backend)
	}
	settings = config.Resolve(flags, envValues, fileValues, registration.defaults())
	if settings.MaxCalls < 0 {
		return backendResolution{}, fmt.Errorf("max_calls must be non-negative")
	}
	semanticBackend, _, err := registration.Construct(opts, settings)
	if err != nil {
		return backendResolution{}, err
	}
	semanticCache := semanticStoreForSettings(settings)
	semanticModel := settings.Model
	if registration.ModelIdentity != nil {
		semanticModel, err = registration.ModelIdentity(settings)
		if err != nil {
			return backendResolution{}, err
		}
	}
	defaultPaidCap := registration.Paid && registration.DefaultMaxCalls > 0 && !flags.MaxCallsSet && !envValues.MaxCallsSet && !fileValues.MaxCallsSet
	return backendResolution{
		Backend:                semanticBackend,
		Cache:                  semanticCache,
		Model:                  semanticModel,
		MaxCalls:               settings.MaxCalls,
		MaxCallsDefaultPaid:    defaultPaidCap,
		Paid:                   registration.Paid,
		DefaultMaxOutputTokens: registration.DefaultMaxOutputTokens,
	}, nil
}

func queryRequiresBackend(query string) bool {
	rewrittenQuery, err := desugar.Rewrite(query)
	if err != nil {
		return false
	}
	semanticPlan, diagnostics := plan.Build(rewrittenQuery)
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == plan.SeverityError {
			return false
		}
	}
	return !semanticPlan.Deterministic
}

func noBackendError() error {
	return fmt.Errorf("semantic operators require a backend: run with --cloud (Anthropic Claude), --backend local (real model; run 'ajq provision' first), --backend ollama --model llama3.2, or --backend mock (deterministic, no model), or set backend in ~/.config/ajq/config.toml")
}

// Execute runs the root command and writes a conventional stderr error line.
func Execute(ctx context.Context, opts Options, args []string) error {
	cmd := NewRootCommand(opts)
	cmd.SetArgs(args)
	if ctx != nil {
		cmd.SetContext(ctx)
	}

	if err := cmd.Execute(); err != nil {
		var exitErr *ExitError
		if errors.As(err, &exitErr) && exitErr.Silent {
			return err
		}
		stderr := defaultWriter(opts.Stderr, io.Discard)
		if _, writeErr := fmt.Fprintf(stderr, "ajq: error: %v\n", err); writeErr != nil {
			return errors.Join(err, fmt.Errorf("write stderr error line: %w", writeErr))
		}
		return err
	}
	return nil
}

func explainEstimateFromEngine(cmd *cobra.Command, flags config.Values, query string, estimate engine.ExplainEstimate) (*explain.Estimate, error) {
	out := &explain.Estimate{
		Status:              estimate.Status,
		Reason:              estimate.Reason,
		StaticCallSites:     estimate.StaticCallSites,
		InputFrames:         estimate.InputFrames,
		HarvestedJudgements: estimate.HarvestedJudgements,
		PostDedupJudgements: estimate.PostDedupJudgements,
		MockJudgeBatches:    estimate.MockJudgeBatches,
	}
	if estimate.Status != engine.ExplainEstimateAvailable {
		return out, nil
	}
	registration, modelID, maxOutputTokens, ok, err := explainPaidBackend(cmd, flags)
	if err != nil || !ok || !registration.Paid {
		return out, err
	}
	usd, known := pricing.Estimate(modelID, estimate.PostDedupJudgements, len(query), maxOutputTokens)
	out.EstimatedCostModelID = modelID
	out.EstimatedCostUSD = usd
	out.EstimatedCostKnown = known
	return out, nil
}

func explainPaidBackend(cmd *cobra.Command, flags config.Values) (backendRegistration, string, int, bool, error) {
	fileValues, err := config.LoadWithOptions(config.LoadOptions{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return backendRegistration{}, "", 0, false, err
	}
	envValues, err := config.Env(os.Getenv)
	if err != nil {
		return backendRegistration{}, "", 0, false, err
	}
	settings := config.Resolve(flags, envValues, fileValues, config.Values{})
	if settings.Backend == "" {
		return backendRegistration{}, "", 0, false, nil
	}
	registration, ok := lookupBackend(settings.Backend)
	if !ok {
		return backendRegistration{}, "", 0, false, unknownBackendError(settings.Backend)
	}
	settings = config.Resolve(flags, envValues, fileValues, registration.defaults())
	modelID := settings.Model
	if registration.ModelIdentity != nil {
		modelID, err = registration.ModelIdentity(settings)
		if err != nil {
			return backendRegistration{}, "", 0, false, err
		}
	}
	return registration, modelID, registration.DefaultMaxOutputTokens, true, nil
}

func printRunStats(w io.Writer, stats engine.RunStats, paid bool, modelID string, promptChars int, maxOutputTokens int) error {
	if _, err := fmt.Fprintln(w, "ajq stats:"); err != nil {
		return err
	}
	lines := []string{
		fmt.Sprintf("  input_frames: %d", stats.InputFrames),
		fmt.Sprintf("  semantic_call_sites: %d", stats.SemanticCallSites),
		fmt.Sprintf("  harvested_judgements: %d", stats.HarvestedJudgements),
		fmt.Sprintf("  post_dedup_backend_calls: %d", stats.PostDedupBackendCalls),
		fmt.Sprintf("  cache_hits: %d", stats.CacheHits),
		fmt.Sprintf("  elapsed: %s", stats.Elapsed),
	}
	if paid {
		usd, known := pricing.Estimate(modelID, stats.PostDedupBackendCalls, promptChars, maxOutputTokens)
		if known {
			lines = append(lines, fmt.Sprintf("  estimated_cost_usd: ~$%.2f (%d calls × model %s)", usd, stats.PostDedupBackendCalls, modelID))
		} else {
			lines = append(lines, fmt.Sprintf("  estimated_cost_usd: unknown (%d calls × model %s; model not in pricing table)", stats.PostDedupBackendCalls, modelID))
		}
	}
	for _, line := range lines {
		if _, err := fmt.Fprintln(w, line); err != nil {
			return err
		}
	}
	return nil
}

func validateExplainCompile(query string, deterministic bool) error {
	if deterministic {
		_, err := jq.Compile(query)
		return err
	}
	_, err := jq.CompileWithOptions(query, semanticCompileOptions()...)
	return err
}

func semanticCompileOptions() []gojq.CompilerOption {
	stub := func(any, []any) any { return nil }
	return []gojq.CompilerOption{
		gojq.WithFunction("sem_match", 1, 2, stub),
		gojq.WithFunction("sem_classify", 2, semantics.MaxJQFunctionArity, stub),
		gojq.WithFunction("sem_extract", 1, 2, stub),
		gojq.WithFunction("sem_score", 1, 2, stub),
		gojq.WithFunction("sem_norm", 1, 2, stub),
		gojq.WithFunction("sem_redact", 1, 2, stub),
	}
}

func defaultReader(r io.Reader) io.Reader {
	if r == nil {
		return strings.NewReader("")
	}
	return r
}

func defaultWriter(w io.Writer, fallback io.Writer) io.Writer {
	if w == nil {
		return fallback
	}
	return w
}
