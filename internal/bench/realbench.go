package bench

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	localbackend "github.com/ricardocabral/ajq/internal/backend/local"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/daemon"
	"github.com/ricardocabral/ajq/internal/semantics"
)

// Environment variables that opt into and configure the real bench mode.
const (
	// EnvRealBench must be set to "1" to run the expensive real-inference
	// benchmark. When unset, the real bench test skips so `go test ./...` never
	// spawns a daemon or loads a multi-gigabyte model.
	EnvRealBench = "AJQ_BENCH_REAL"
	// EnvRealServer overrides the llama-server binary path for the real bench.
	EnvRealServer = "AJQ_BENCH_SERVER"
	// EnvRealModel overrides the GGUF model path for the real bench.
	EnvRealModel = "AJQ_BENCH_MODEL"
	// EnvRealBenchMachine records an operator-supplied hardware label in a
	// real-bench report, for example "Apple M3 Pro (Metal)".
	EnvRealBenchMachine = "AJQ_BENCH_MACHINE"
	// EnvRealBenchGitRevision records the source revision that produced a real
	// benchmark report. The Makefile supplies it for standard runs.
	EnvRealBenchGitRevision = "AJQ_BENCH_GIT_REVISION"
	// EnvRealBenchReportDir optionally receives one JSON report per successful
	// real benchmark run.
	EnvRealBenchReportDir = "AJQ_BENCH_REPORT_DIR"
)

// Default real-bench asset locations. These match the assets provisioned by
// TP-020 on the reference machine and are only used when they actually exist;
// discovery falls back to daemon.Discoverer (PATH + cache) otherwise.
const (
	defaultRealServerPath = "/opt/homebrew/bin/llama-server"
	defaultRealModelName  = "qwen2.5-1.5b-instruct-q5_k_m.gguf"
)

// RealConfig describes the assets and daemon binding for a real-inference
// benchmark run.
type RealConfig struct {
	// ServerBinaryPath is the llama-server executable to spawn.
	ServerBinaryPath string
	// ModelPath is the GGUF model file to load.
	ModelPath string
	// Host and Port bind the daemon (localhost only).
	Host string
	Port int
	// HyperfinePath is the optional hyperfine binary for external command
	// timing. Empty when hyperfine is unavailable.
	HyperfinePath string
}

// candidateModelPaths returns the conventional provisioned model locations in
// precedence order. macOS os.UserCacheDir resolves to ~/Library/Caches, but the
// reference provisioning also uses the XDG-style ~/.cache/ajq location, so both
// are probed.
func candidateModelPaths() []string {
	var paths []string
	cfg := daemon.DefaultConfig()
	if cfg.CacheDir != "" {
		paths = append(paths, filepath.Join(cfg.CacheDir, "models", defaultRealModelName))
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, filepath.Join(home, ".cache", "ajq", "models", defaultRealModelName))
	}
	return paths
}

// defaultModelPath returns the first existing candidate model path, or the
// first candidate when none exist (so the skip reason names a concrete path).
func defaultModelPath() string {
	candidates := candidateModelPaths()
	for _, p := range candidates {
		if fileExists(p) {
			return p
		}
	}
	if len(candidates) > 0 {
		return candidates[0]
	}
	cfg := daemon.DefaultConfig()
	return filepath.Join(cfg.CacheDir, "models", defaultRealModelName)
}

// DetectRealAssets resolves the real-bench configuration and reports whether a
// real run is possible. It honors AJQ_BENCH_SERVER / AJQ_BENCH_MODEL overrides,
// then the reference defaults, then daemon.Discoverer for the binary. When
// assets are missing it returns available=false with an actionable reason so
// callers can skip clearly instead of failing.
func DetectRealAssets() (cfg RealConfig, available bool, reason string) {
	cfg = RealConfig{Host: daemon.DefaultHost, Port: daemon.DefaultPort}

	// Resolve the server binary.
	if p := strings.TrimSpace(os.Getenv(EnvRealServer)); p != "" {
		cfg.ServerBinaryPath = p
	} else if fileExists(defaultRealServerPath) {
		cfg.ServerBinaryPath = defaultRealServerPath
	} else {
		disc := daemon.Discoverer{LookPath: exec.LookPath}
		if p, err := disc.DiscoverServerBinary(daemon.DefaultConfig()); err == nil {
			cfg.ServerBinaryPath = p
		}
	}

	// Resolve the model.
	if p := strings.TrimSpace(os.Getenv(EnvRealModel)); p != "" {
		cfg.ModelPath = p
	} else {
		cfg.ModelPath = defaultModelPath()
	}

	// Resolve optional hyperfine.
	if p, err := exec.LookPath("hyperfine"); err == nil {
		cfg.HyperfinePath = p
	}

	switch {
	case cfg.ServerBinaryPath == "" || !fileExists(cfg.ServerBinaryPath):
		return cfg, false, fmt.Sprintf("llama-server binary not found (set %s to override)", EnvRealServer)
	case !fileExists(cfg.ModelPath):
		return cfg, false, fmt.Sprintf("model file not found at %q (set %s to override); run `ajq provision`", cfg.ModelPath, EnvRealModel)
	default:
		return cfg, true, ""
	}
}

// fileExists reports whether path names an existing, non-directory file.
func fileExists(path string) bool {
	if strings.TrimSpace(path) == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// Environment captures machine and asset facts recorded alongside real-bench
// results so runs are comparable across machines.
type Environment struct {
	OS             string
	Arch           string
	NumCPU         int
	MetalAvailable bool
	ParallelSlots  int
	ServerBinary   string
	ServerVersion  string
	ModelPath      string
	DaemonBaseURL  string
	HyperfinePath  string
}

// DescribeEnvironment gathers environment facts for a real-bench configuration.
// The llama-server version is a best-effort probe; a failure leaves it blank.
func DescribeEnvironment(cfg RealConfig) Environment {
	env := Environment{
		OS:             runtime.GOOS,
		Arch:           runtime.GOARCH,
		NumCPU:         runtime.NumCPU(),
		MetalAvailable: metalLikely(),
		ParallelSlots:  daemon.DefaultParallelSlots,
		ServerBinary:   cfg.ServerBinaryPath,
		ServerVersion:  probeServerVersion(cfg.ServerBinaryPath),
		ModelPath:      cfg.ModelPath,
		HyperfinePath:  cfg.HyperfinePath,
	}
	dcfg := daemon.Config{Host: cfg.Host, Port: cfg.Port}
	env.DaemonBaseURL = dcfg.BaseURL()
	return env
}

// metalLikely reports whether the Metal GPU backend is likely available. Apple
// Silicon (darwin/arm64) ships Metal; other platforms do not.
func metalLikely() bool {
	return runtime.GOOS == "darwin" && runtime.GOARCH == "arm64"
}

// probeServerVersion runs `<binary> --version` and returns a trimmed,
// single-line version string. It is best-effort: any error yields "".
func probeServerVersion(binary string) string {
	if !fileExists(binary) {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--version")
	out, _ := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if text == "" {
		return ""
	}
	// Return the first non-empty line to keep the report stable.
	for _, line := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			return s
		}
	}
	return ""
}

type realDaemonManager interface {
	EnsureRunning(ctx context.Context) error
	APIKey() string
	Stop(ctx context.Context) (bool, error)
}

var newRealDaemonManager = func(cfg daemon.Config) realDaemonManager {
	return daemon.NewManager(cfg)
}

// RealReport is the result of a real-inference benchmark run.
type RealReport struct {
	Environment Environment
	// Provenance identifies the software, model bytes, and optional hardware
	// label that produced this report.
	Provenance RealProvenance
	// ColdStart is the time to bring a stopped daemon to a healthy state.
	ColdStart time.Duration
	// WarmLatency is the time for a single judgement against the warm daemon.
	WarmLatency time.Duration
	// SequentialBatchLatency is the wall time to resolve one array workload batch
	// with local.Backend.MaxConcurrency=1.
	SequentialBatchLatency time.Duration
	// SequentialThroughput is BatchJudgements / SequentialBatchLatency.
	SequentialThroughput float64
	// ParallelBatchLatency is the wall time to resolve the same array workload
	// batch with local.Backend.MaxConcurrency set to daemon.DefaultParallelSlots.
	ParallelBatchLatency time.Duration
	// ParallelThroughput is BatchJudgements / ParallelBatchLatency.
	ParallelThroughput float64
	// BatchLatency is retained for older in-process callers and mirrors
	// SequentialBatchLatency. New reports should use the explicitly named fields.
	BatchLatency time.Duration
	// BatchJudgements is the number of distinct judgements in that batch.
	BatchJudgements int
	// Throughput is retained for older in-process callers and mirrors
	// SequentialThroughput. New reports should use the explicitly named fields.
	Throughput float64
	// CachedBatchLatency is the wall time to resolve the same batch a second
	// time, served from the semantic cache (no inference).
	CachedBatchLatency time.Duration
	// Workload names the array workload used for batch/cache measurement.
	Workload string
	// Hyperfine holds optional external cold/warm command timing.
	Hyperfine *HyperfineResult
}

// RealProvenance captures the reproducibility facts for one real benchmark
// report. ModelSHA256 is calculated from the actual GGUF file, not inferred
// from a catalog entry.
type RealProvenance struct {
	RecordedAt  time.Time
	GoVersion   string
	Machine     string
	GitRevision string
	ModelSHA256 string
	ModelBytes  int64
}

// RunReal executes the real-inference benchmark against a warm local
// llama-server. It measures cold start, warm single-judgement latency,
// sequential vs bounded-parallel batch throughput, and the repeated-value cache
// effect. The daemon is always stopped on return. The workload's array input
// drives both batch measurements so the comparison uses identical judgements.
func RunReal(ctx context.Context, cfg RealConfig, w Workload) (report RealReport, err error) {
	if ctx == nil {
		ctx = context.Background()
	}
	report = RealReport{
		Environment: DescribeEnvironment(cfg),
		Workload:    w.Name,
	}
	provenance, provenanceErr := captureRealProvenance(cfg)
	if provenanceErr != nil {
		return report, fmt.Errorf("capture benchmark provenance: %w", provenanceErr)
	}
	report.Provenance = provenance

	dcfg := daemon.Config{
		Host:             cfg.Host,
		Port:             cfg.Port,
		ServerBinaryPath: cfg.ServerBinaryPath,
		ModelPath:        cfg.ModelPath,
		ParallelSlots:    daemon.DefaultParallelSlots,
	}
	mgr := newRealDaemonManager(dcfg)

	// Ensure a clean baseline, then measure cold start.
	if _, err := mgr.Stop(ctx); err != nil {
		return report, fmt.Errorf("initial stop: %w", err)
	}
	coldStart := time.Now()
	if err := mgr.EnsureRunning(ctx); err != nil {
		return report, fmt.Errorf("cold start: %w", err)
	}
	report.ColdStart = positiveDurationSince(coldStart)
	apiKey := mgr.APIKey()
	defer func() {
		if _, stopErr := mgr.Stop(context.Background()); stopErr != nil {
			cleanupErr := fmt.Errorf("cleanup stop: %w", stopErr)
			if err == nil {
				err = cleanupErr
				return
			}
			err = errors.Join(err, cleanupErr)
		}
	}()

	sequentialBE := &localbackend.Backend{
		BaseURL:            dcfg.BaseURL(),
		ModelID:            semanticcache.DefaultModelID,
		APIKey:             apiKey,
		MaxConcurrency:     1,
		DisablePromptCache: true,
		// Daemon is already warm; no WarmFunc needed.
	}
	parallelBE := &localbackend.Backend{
		BaseURL:            dcfg.BaseURL(),
		ModelID:            semanticcache.DefaultModelID,
		APIKey:             apiKey,
		MaxConcurrency:     daemon.DefaultParallelSlots,
		DisablePromptCache: true,
		// Daemon is already warm; no WarmFunc needed.
	}

	// Warm single-judgement latency.
	single := []backend.Judgement{sampleJudgement("urgent")}
	warmStart := time.Now()
	if _, err := sequentialBE.Judge(ctx, single); err != nil {
		return report, fmt.Errorf("warm judgement: %w", err)
	}
	report.WarmLatency = positiveDurationSince(warmStart)

	// Sequential and bounded-parallel throughput over the same distinct values.
	batch := distinctBatch(w)
	report.BatchJudgements = len(batch)
	sequentialStart := time.Now()
	if _, err := sequentialBE.Judge(ctx, batch); err != nil {
		return report, fmt.Errorf("sequential batch judge: %w", err)
	}
	report.SequentialBatchLatency = positiveDurationSince(sequentialStart)
	if report.SequentialBatchLatency > 0 {
		report.SequentialThroughput = float64(report.BatchJudgements) / report.SequentialBatchLatency.Seconds()
	}
	report.BatchLatency = report.SequentialBatchLatency
	report.Throughput = report.SequentialThroughput

	parallelStart := time.Now()
	if _, err := parallelBE.Judge(ctx, batch); err != nil {
		return report, fmt.Errorf("parallel batch judge: %w", err)
	}
	report.ParallelBatchLatency = positiveDurationSince(parallelStart)
	if report.ParallelBatchLatency > 0 {
		report.ParallelThroughput = float64(report.BatchJudgements) / report.ParallelBatchLatency.Seconds()
	}

	// Repeated-value cache effect: resolving the same distinct batch through a
	// semantic cache should be served without any inference.
	store := semanticcache.NewStore()
	if err := resolveThroughCache(ctx, parallelBE, store, batch); err != nil {
		return report, fmt.Errorf("cache warm: %w", err)
	}
	// Second pass is fully cache-served.
	cacheStart := time.Now()
	if err := resolveThroughCache(ctx, parallelBE, store, batch); err != nil {
		return report, fmt.Errorf("cache replay: %w", err)
	}
	report.CachedBatchLatency = positiveDurationSince(cacheStart)

	return report, nil
}

func captureRealProvenance(cfg RealConfig) (RealProvenance, error) {
	provenance := RealProvenance{
		RecordedAt:  time.Now().UTC(),
		GoVersion:   runtime.Version(),
		Machine:     strings.TrimSpace(os.Getenv(EnvRealBenchMachine)),
		GitRevision: strings.TrimSpace(os.Getenv(EnvRealBenchGitRevision)),
	}
	if strings.TrimSpace(cfg.ModelPath) == "" {
		return provenance, nil
	}

	info, err := os.Stat(cfg.ModelPath)
	if err != nil {
		return RealProvenance{}, fmt.Errorf("stat model %q: %w", cfg.ModelPath, err)
	}
	if info.IsDir() {
		return RealProvenance{}, fmt.Errorf("model path %q is a directory", cfg.ModelPath)
	}
	file, err := os.Open(cfg.ModelPath)
	if err != nil {
		return RealProvenance{}, fmt.Errorf("open model %q: %w", cfg.ModelPath, err)
	}
	defer func() { _ = file.Close() }()

	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return RealProvenance{}, fmt.Errorf("hash model %q: %w", cfg.ModelPath, err)
	}
	provenance.ModelSHA256 = fmt.Sprintf("%x", hash.Sum(nil))
	provenance.ModelBytes = info.Size()
	return provenance, nil
}

// sampleJudgement builds a single sem_match judgement over a representative
// message drawn from the workload vocabulary.
func sampleJudgement(spec string) backend.Judgement {
	return backend.Judgement{
		Op:      "sem_match",
		Kind:    semantics.KindPredicate,
		Return:  semantics.ReturnBool,
		Schema:  backend.ResultSchema{Type: semantics.ReturnBool},
		Specs:   []string{spec},
		Value:   messageAt(0),
		ModelID: semanticcache.DefaultModelID,
	}
}

// distinctBatch builds one sem_match judgement per distinct message in the
// vocabulary, modelling a post-dedup window batch.
func distinctBatch(w Workload) []backend.Judgement {
	n := w.Distinct
	if n <= 0 || n > msgVocabularyLen {
		n = msgVocabularyLen
	}
	batch := make([]backend.Judgement, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, backend.Judgement{
			Op:      "sem_match",
			Kind:    semantics.KindPredicate,
			Return:  semantics.ReturnBool,
			Schema:  backend.ResultSchema{Type: semantics.ReturnBool},
			Specs:   []string{"urgent"},
			Value:   messageAt(i),
			ModelID: semanticcache.DefaultModelID,
		})
	}
	return batch
}

// resolveThroughCache resolves a batch through a semantic cache, only calling
// the backend for keys that miss. This mirrors the engine resolve dedup path
// and lets the second call demonstrate the cache effect.
func resolveThroughCache(ctx context.Context, be backend.Backend, store *semanticcache.Store, batch []backend.Judgement) error {
	var pending []backend.Judgement
	var keys []semanticcache.Key
	for _, j := range batch {
		key, err := semanticcache.KeyForJudgement(j)
		if err != nil {
			return err
		}
		if _, ok := store.Get(key); ok {
			continue
		}
		pending = append(pending, j)
		keys = append(keys, key)
	}
	if len(pending) == 0 {
		return nil
	}
	results, err := be.Judge(ctx, pending)
	if err != nil {
		return err
	}
	for i := range results {
		store.Set(keys[i], results[i])
	}
	return nil
}
