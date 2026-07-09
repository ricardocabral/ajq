package backend_test

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestMockBackendConformance(t *testing.T) {
	// MockBackend is deterministic in-process code, not a model wire transport;
	// scripted invalid-output and transport-failure cases are covered by the HTTP
	// backend fakes. This subset still exercises the real MockBackend contract for
	// batch shape/order, valid typed outputs, empty batches, and cancellation.
	conformance.Run(t, func(string) backend.Backend { return &backend.MockBackend{} }, conformance.WithCaseFilter(mockBackendCase))
}

func mockBackendCase(tc conformance.Case) bool {
	switch tc.Name {
	case "all_return_types_round_trip", "empty_batch_returns_nil_nil", "context_cancellation_aborts_promptly":
		return true
	default:
		return false
	}
}
