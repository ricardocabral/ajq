package oai

import (
	"context"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOpenAIBackendConformance(t *testing.T) {
	server := conformance.NewScriptedServer(t, conformance.ProtocolOpenAI)
	conformance.Run(t, func(serverURL string) backend.Backend {
		return &Backend{
			BaseURL:    serverURL + "/v1",
			APIKey:     "test-key",
			APIKeyEnv:  "OPENAI_API_KEY",
			Model:      "test-model",
			RetrySleep: func(context.Context, time.Duration) error { return nil },
		}
	}, conformance.WithScriptedServer(server))
}
