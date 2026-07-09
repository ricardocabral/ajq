package oai

import (
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestOpenAIBackendLiveConformance(t *testing.T) {
	if os.Getenv("AJQ_CONFORMANCE_LIVE") != "1" {
		t.Skip("set AJQ_CONFORMANCE_LIVE=1 to run live backend conformance")
	}
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	model := strings.TrimSpace(os.Getenv("AJQ_OPENAI_MODEL"))
	if apiKey == "" || model == "" {
		t.Skip("set OPENAI_API_KEY and AJQ_OPENAI_MODEL to run live OpenAI conformance")
	}
	baseURL := strings.TrimSpace(os.Getenv("AJQ_OPENAI_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}
	conformance.RunLiveSmoke(t, &Backend{BaseURL: baseURL, APIKey: apiKey, APIKeyEnv: "OPENAI_API_KEY", Model: model})
}
