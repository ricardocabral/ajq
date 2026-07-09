package backend

import (
	"context"
	"encoding/json"
	"fmt"
	"hash/fnv"
	"math"
	"regexp"
	"strings"
	"sync"

	"github.com/ricardocabral/ajq/internal/semantics"
)

var mockNormRE = regexp.MustCompile(`[^a-z0-9]+`)

// MockBackend is a deterministic in-process Backend for tests and golden
// fixtures. It never performs model, network, daemon, or cloud work.
type MockBackend struct {
	mu        sync.Mutex
	warmCount int
	callCount int
	batches   [][]Judgement
	inputs    []Judgement
}

// Warm records that the mock backend was warmed.
func (b *MockBackend) Warm(context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.warmCount++
	return nil
}

// Judge records an immutable snapshot of batch and returns deterministic
// results for supported semantic operators.
func (b *MockBackend) Judge(ctx context.Context, batch []Judgement) ([]Result, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(batch) == 0 {
		return nil, nil
	}
	snapshot := cloneJudgementBatch(batch)

	b.mu.Lock()
	b.callCount++
	b.batches = append(b.batches, snapshot)
	b.inputs = append(b.inputs, snapshot...)
	b.mu.Unlock()

	results := make([]Result, len(snapshot))
	for i, judgement := range snapshot {
		results[i] = mockResult(judgement)
	}
	return results, nil
}

// WarmCount returns how many times Warm was called.
func (b *MockBackend) WarmCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.warmCount
}

// CallCount returns how many Judge calls were recorded.
func (b *MockBackend) CallCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.callCount
}

// BatchCount returns how many Judge batches were recorded.
func (b *MockBackend) BatchCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.batches)
}

// Batches returns immutable snapshots of all recorded Judge batches.
func (b *MockBackend) Batches() [][]Judgement {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([][]Judgement, len(b.batches))
	for i, batch := range b.batches {
		out[i] = cloneJudgementBatch(batch)
	}
	return out
}

// Inputs returns a flattened immutable snapshot of all recorded judgements.
func (b *MockBackend) Inputs() []Judgement {
	b.mu.Lock()
	defer b.mu.Unlock()
	return cloneJudgementBatch(b.inputs)
}

func mockResult(j Judgement) Result {
	specs := normalizedSpecs(j.Specs)
	text := strings.ToLower(mockValueString(j.Value))

	switch j.Op {
	case "sem_match":
		return Result{Value: mockContainsSpec(text, specs)}
	case "sem_classify":
		if len(j.Specs) == 0 {
			return Result{Error: "sem_classify requires labels"}
		}
		for i, spec := range specs {
			if spec != "" && strings.Contains(text, spec) {
				return Result{Value: j.Specs[i]}
			}
		}
		idx := int(mockHash(text) % uint64(len(j.Specs))) //nolint:gosec // modulo bounds the value below len(j.Specs) before converting to int.
		return Result{Value: j.Specs[idx]}
	case "sem_score":
		return Result{Value: mockScore(text, specs)}
	case "sem_norm":
		if len(specs) > 0 {
			for i, spec := range specs {
				if spec != "" && strings.Contains(text, spec) {
					return Result{Value: mockNormalize(j.Specs[i])}
				}
			}
		}
		return Result{Value: mockNormalize(mockValueString(j.Value))}
	default:
		switch j.Return {
		case semantics.ReturnBool:
			return Result{Value: mockContainsSpec(text, specs)}
		case semantics.ReturnNumber:
			return Result{Value: mockScore(text, specs)}
		case semantics.ReturnString:
			return Result{Value: mockNormalize(mockValueString(j.Value))}
		default:
			return Result{Error: fmt.Sprintf("unsupported semantic op %q", j.Op)}
		}
	}
}

func mockContainsSpec(text string, specs []string) bool {
	if len(specs) == 0 {
		return false
	}
	for _, spec := range specs {
		if spec != "" && strings.Contains(text, spec) {
			return true
		}
	}
	return false
}

func mockScore(text string, specs []string) float64 {
	if mockContainsSpec(text, specs) {
		return 1
	}
	// Keep non-matches deterministic but below direct matches.
	return math.Round((float64(mockHash(text)%5000)/10000+0.25)*10000) / 10000
}

func normalizedSpecs(specs []string) []string {
	out := make([]string, len(specs))
	for i, spec := range specs {
		out[i] = strings.ToLower(strings.TrimSpace(spec))
	}
	return out
}

func mockNormalize(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = mockNormRE.ReplaceAllString(normalized, "_")
	normalized = strings.Trim(normalized, "_")
	if normalized == "" {
		return "empty"
	}
	return normalized
}

func mockValueString(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	default:
		encoded, err := json.Marshal(v)
		if err == nil {
			return string(encoded)
		}
		return fmt.Sprint(v)
	}
}

func mockHash(value string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(value))
	return h.Sum64()
}

func cloneJudgementBatch(batch []Judgement) []Judgement {
	out := make([]Judgement, len(batch))
	for i, judgement := range batch {
		out[i] = cloneJudgement(judgement)
	}
	return out
}

func cloneJudgement(j Judgement) Judgement {
	j.Specs = append([]string(nil), j.Specs...)
	j.Schema.Enum = append([]string(nil), j.Schema.Enum...)
	j.Value = cloneValue(j.Value)
	return j
}

func cloneValue(value any) any {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return value
	}
	return decoded
}
