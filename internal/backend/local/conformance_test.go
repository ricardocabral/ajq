package local

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestLocalBackendConformance(t *testing.T) {
	for _, tc := range []struct {
		name           string
		maxConcurrency int
	}{
		{name: "sequential", maxConcurrency: 0},
		{name: "parallel", maxConcurrency: 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := conformance.NewScriptedServer(t, conformance.ProtocolLocal)
			conformance.Run(t, func(serverURL string) backend.Backend {
				return &Backend{BaseURL: serverURL, MaxConcurrency: tc.maxConcurrency}
			}, conformance.WithScriptedServer(server))
		})
	}
}
