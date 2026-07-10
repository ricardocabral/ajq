package promptkit

import (
	"strings"
	"testing"
)

func TestSanitizeProviderErrorBody(t *testing.T) {
	const marker = "echoed-value-marker-xyz"
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "OpenAI error envelope",
			body: `{"error":{"type":"invalid_request_error","code":"bad_parameter","message":"` + marker + `"}}`,
			want: "error_type=invalid_request_error error_code=bad_parameter",
		},
		{
			name: "Anthropic error envelope",
			body: `{"type":"error","error":{"type":"authentication_error","message":"` + marker + `"}}`,
			want: "error_type=authentication_error",
		},
		{
			name: "non JSON body",
			body: marker,
			want: "",
		},
		{
			name: "unrecognized JSON envelope",
			body: `{"error":{"message":"` + marker + `"}}`,
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SanitizeProviderErrorBody(tc.body)
			if got != tc.want {
				t.Errorf("SanitizeProviderErrorBody() = %q, want %q", got, tc.want)
			}
			if strings.Contains(got, marker) {
				t.Errorf("SanitizeProviderErrorBody() exposed message value %q", marker)
			}
		})
	}
}
