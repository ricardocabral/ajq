package bench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HyperfineResult holds the parsed timing for a single hyperfine benchmark
// command (mean/min/max in seconds).
type HyperfineResult struct {
	// Command is the shell command hyperfine measured.
	Command string
	// MeanSeconds is the mean wall-clock time.
	MeanSeconds float64
	// MinSeconds and MaxSeconds bound the observed runs.
	MinSeconds float64
	MaxSeconds float64
	// Runs is how many iterations hyperfine performed.
	Runs int
}

// HyperfineAvailable reports whether the hyperfine binary is on PATH.
func HyperfineAvailable() bool {
	_, err := exec.LookPath("hyperfine")
	return err == nil
}

// hyperfineJSON mirrors the subset of hyperfine's --export-json schema we read.
type hyperfineJSON struct {
	Results []struct {
		Command string    `json:"command"`
		Mean    float64   `json:"mean"`
		Min     float64   `json:"min"`
		Max     float64   `json:"max"`
		Times   []float64 `json:"times"`
	} `json:"results"`
}

// RunHyperfine measures a single command with hyperfine and returns its timing.
// hyperfinePath is the resolved hyperfine binary; command is passed verbatim to
// hyperfine (which runs it via the shell). runs bounds the iteration count and
// warmup sets warmup iterations. It is best-effort external tooling: callers
// keep Go-native timings available without it.
func RunHyperfine(ctx context.Context, hyperfinePath, command string, runs, warmup int) (*HyperfineResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(hyperfinePath) == "" {
		return nil, fmt.Errorf("hyperfine path is empty")
	}
	if runs <= 0 {
		runs = 10
	}
	if warmup < 0 {
		warmup = 0
	}

	tmp, err := os.CreateTemp("", "ajq-hyperfine-*.json")
	if err != nil {
		return nil, fmt.Errorf("create hyperfine temp: %w", err)
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	args := []string{
		"--runs", fmt.Sprintf("%d", runs),
		"--warmup", fmt.Sprintf("%d", warmup),
		"--export-json", tmpPath,
		command,
	}
	cmd := exec.CommandContext(ctx, hyperfinePath, args...) //nolint:gosec // hyperfinePath is a caller-resolved benchmark tool path; command execution is explicit opt-in tooling.
	if out, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("hyperfine failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	data, err := os.ReadFile(tmpPath) //nolint:gosec // tmpPath is the temp file created above for hyperfine JSON output.
	if err != nil {
		return nil, fmt.Errorf("read hyperfine json: %w", err)
	}
	var parsed hyperfineJSON
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, fmt.Errorf("parse hyperfine json: %w", err)
	}
	if len(parsed.Results) == 0 {
		return nil, fmt.Errorf("hyperfine returned no results")
	}
	r := parsed.Results[0]
	return &HyperfineResult{
		Command:     r.Command,
		MeanSeconds: r.Mean,
		MinSeconds:  r.Min,
		MaxSeconds:  r.Max,
		Runs:        len(r.Times),
	}, nil
}

// MeanDuration returns the mean timing as a time.Duration.
func (h *HyperfineResult) MeanDuration() time.Duration {
	if h == nil {
		return 0
	}
	return time.Duration(h.MeanSeconds * float64(time.Second))
}
