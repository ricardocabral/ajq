package anthropicbk

import (
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestAnthropicBackendLiveConformance(t *testing.T) {
	if os.Getenv("AJQ_CONFORMANCE_LIVE") != "1" {
		t.Skip("set AJQ_CONFORMANCE_LIVE=1 to run live backend conformance")
	}
	if strings.TrimSpace(os.Getenv(APIKeyEnv)) == "" {
		t.Skip("set ANTHROPIC_API_KEY to run live Anthropic conformance")
	}
	be, err := New(strings.TrimSpace(os.Getenv("AJQ_ANTHROPIC_MODEL")))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	conformance.RunLiveSmoke(t, be)
}
