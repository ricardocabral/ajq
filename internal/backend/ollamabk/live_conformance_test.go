package ollamabk

import (
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOllamaBackendLiveConformance(t *testing.T) {
	if os.Getenv("AJQ_CONFORMANCE_LIVE") != "1" {
		t.Skip("set AJQ_CONFORMANCE_LIVE=1 to run live backend conformance")
	}
	model := strings.TrimSpace(os.Getenv("AJQ_OLLAMA_MODEL"))
	if model == "" {
		t.Skip("set AJQ_OLLAMA_MODEL to run live Ollama conformance")
	}
	baseURL, err := ResolveBaseURL("")
	if err != nil {
		t.Fatalf("ResolveBaseURL returned error: %v", err)
	}
	conformance.RunLiveSmoke(t, &Backend{BaseURL: baseURL, Model: model})
}
