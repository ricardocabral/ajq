package promptkit

import (
	"encoding/json"
	"strings"
)

// SanitizeProviderErrorBody extracts structured error type and code fields from a
// provider response body without exposing provider-supplied free-text content.
func SanitizeProviderErrorBody(body string) string {
	var envelope struct {
		Error struct {
			Type string `json:"type"`
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		return ""
	}

	parts := make([]string, 0, 2)
	if errorType := strings.TrimSpace(envelope.Error.Type); errorType != "" {
		parts = append(parts, "error_type="+errorType)
	}
	if code := strings.TrimSpace(envelope.Error.Code); code != "" {
		parts = append(parts, "error_code="+code)
	}
	return strings.Join(parts, " ")
}
