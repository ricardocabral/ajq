package local

import (
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend/conformance"
)

func TestLocalBackendLiveConformance(t *testing.T) {
	if os.Getenv("AJQ_CONFORMANCE_LIVE") != "1" {
		t.Skip("set AJQ_CONFORMANCE_LIVE=1 to run live backend conformance")
	}
	baseURL := strings.TrimSpace(os.Getenv("AJQ_LOCAL_BASE_URL"))
	if baseURL == "" {
		t.Skip("set AJQ_LOCAL_BASE_URL to run live local backend conformance")
	}
	model := strings.TrimSpace(os.Getenv("AJQ_LOCAL_MODEL"))
	if model == "" {
		t.Skip("set AJQ_LOCAL_MODEL to run live local backend conformance")
	}
	be := &Backend{BaseURL: baseURL, ModelID: model}
	conformance.RunLiveSmoke(t, be)
}
