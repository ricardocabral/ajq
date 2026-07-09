package conformance

import (
	"context"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
)

type brokenBackend struct{}

func (brokenBackend) Warm(context.Context) error { return nil }

func (brokenBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	if len(batch) == 0 {
		return []backend.Result{}, nil
	}
	results := make([]backend.Result, len(batch))
	for i := range results {
		results[i] = backend.Result{Value: true}
	}
	return results, nil
}

func TestNegativeControlBrokenBackendFailsConformance(t *testing.T) {
	Run(t, func(string) backend.Backend { return brokenBackend{} }, ExpectFailure())
}
