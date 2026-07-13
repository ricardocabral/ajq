package ollamabk

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOllamaBackendConformance(t *testing.T) {
	runConformance(t, 0)
}

func TestOllamaBackendConcurrentConformance(t *testing.T) {
	runConformance(t, 2)
}

func runConformance(t *testing.T, maxConcurrency int) {
	t.Helper()
	server := conformance.NewScriptedServer(t, conformance.ProtocolOllama)
	conformance.Run(t, func(serverURL string) backend.Backend {
		return &Backend{BaseURL: serverURL, Model: "llama3.2", MaxConcurrency: maxConcurrency}
	}, conformance.WithScriptedServer(server))
}
