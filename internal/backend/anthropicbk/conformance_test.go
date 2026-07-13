package anthropicbk

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestAnthropicBackendConformance(t *testing.T) {
	runConformance(t, 0)
}

func TestAnthropicBackendConcurrentConformance(t *testing.T) {
	runConformance(t, 2, conformance.WithConcurrentDispatcher())
}

func runConformance(t *testing.T, maxConcurrency int, opts ...conformance.Option) {
	t.Helper()
	t.Setenv(APIKeyEnv, "test-key")
	server := conformance.NewScriptedServer(t, conformance.ProtocolAnthropic)
	conformance.Run(t, func(serverURL string) backend.Backend {
		be, err := New("haiku", option.WithBaseURL(serverURL), option.WithMaxRetries(0))
		if err != nil {
			t.Fatalf("New returned error: %v", err)
		}
		be.MaxConcurrency = maxConcurrency
		return be
	}, append(opts, conformance.WithScriptedServer(server))...)
}
