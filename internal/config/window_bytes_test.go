package config

import (
	"strings"
	"testing"
)

func TestWindowBytesDefaultAndPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		flags Values
		env   Values
		file  Values
		want  int64
	}{
		{name: "default", want: DefaultWindowBytes},
		{name: "file", file: Values{WindowBytes: 1024, WindowBytesSet: true}, want: 1024},
		{name: "env", env: Values{WindowBytes: 2048, WindowBytesSet: true}, file: Values{WindowBytes: 1024, WindowBytesSet: true}, want: 2048},
		{name: "flag", flags: Values{WindowBytes: 4096, WindowBytesSet: true}, env: Values{WindowBytes: 2048, WindowBytesSet: true}, file: Values{WindowBytes: 1024, WindowBytesSet: true}, want: 4096},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Resolve(tc.flags, tc.env, tc.file, Values{}).WindowBytes; got != tc.want {
				t.Fatalf("WindowBytes = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestEnvRejectsInvalidWindowBytes(t *testing.T) {
	for _, value := range []string{"0", "-1", "not-an-int", "9223372036854775808"} {
		t.Run(value, func(t *testing.T) {
			_, err := Env(func(key string) string {
				if key == "AJQ_WINDOW_BYTES" {
					return value
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), "AJQ_WINDOW_BYTES") {
				t.Fatalf("Env(AJQ_WINDOW_BYTES=%q) error = %v, want window validation", value, err)
			}
		})
	}
}

func TestLoadRejectsInvalidWindowBytes(t *testing.T) {
	for _, body := range []string{
		"window_bytes = 0\n",
		"window_bytes = -1\n",
		"window_bytes = \"not-an-int\"\n",
		"window_bytes = 9223372036854775808\n",
	} {
		t.Run(body, func(t *testing.T) {
			_, err := LoadWithOptions(LoadOptions{
				Getenv: func(key string) string {
					if key == "AJQ_CONFIG" {
						return "/tmp/window.toml"
					}
					return ""
				},
				ReadFile: func(string) ([]byte, error) { return []byte(body), nil },
			})
			if err == nil || !strings.Contains(err.Error(), "window_bytes") {
				t.Fatalf("LoadWithOptions(%q) error = %v, want window_bytes validation", body, err)
			}
		})
	}
}
