package oai

import (
	"context"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOpenAIBackendConformance(t *testing.T) {
	runConformance(t, 0)
}

func TestOpenAIBackendConcurrentConformance(t *testing.T) {
	runConformance(t, 2, conformance.WithConcurrentDispatcher())
}

func runConformance(t *testing.T, maxConcurrency int, opts ...conformance.Option) {
	t.Helper()
	server := conformance.NewScriptedServer(t, conformance.ProtocolOpenAI)
	conformance.Run(t, func(serverURL string) backend.Backend {
		return &Backend{
			BaseURL:        serverURL + "/v1",
			APIKey:         "test-key",
			APIKeyEnv:      "OPENAI_API_KEY",
			Model:          "test-model",
			MaxConcurrency: maxConcurrency,
			RetrySleep:     func(context.Context, time.Duration) error { return nil },
		}
	}, append(opts, conformance.WithScriptedServer(server))...)
}
