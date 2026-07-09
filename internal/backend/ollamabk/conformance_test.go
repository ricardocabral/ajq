package ollamabk

import (
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOllamaBackendConformance(t *testing.T) {
	server := conformance.NewScriptedServer(t, conformance.ProtocolOllama)
	conformance.Run(t, func(serverURL string) backend.Backend {
		return &Backend{BaseURL: serverURL, Model: "llama3.2"}
	}, conformance.WithScriptedServer(server))
}
