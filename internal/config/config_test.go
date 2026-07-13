package config

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingDefaultFileIsNotError(t *testing.T) {
	values, err := LoadWithOptions(LoadOptions{
		Getenv:  func(string) string { return "" },
		HomeDir: func() (string, error) { return "/home/tester", nil },
		ReadFile: func(path string) ([]byte, error) {
			want := filepath.Join("/home/tester", ".config", "ajq", "config.toml")
			if path != want {
				t.Fatalf("ReadFile path = %q, want %q", path, want)
			}
			return nil, fs.ErrNotExist
		},
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error for missing default file: %v", err)
	}
	if values != (Values{}) {
		t.Fatalf("values = %+v, want zero Values", values)
	}
}

func TestLoadAJQConfigOverrideReadsExplicitPath(t *testing.T) {
	var stderr strings.Builder
	values, err := LoadWithOptions(LoadOptions{
		Getenv: func(key string) string {
			if key == "AJQ_CONFIG" {
				return "/tmp/custom-ajq.toml"
			}
			return ""
		},
		ReadFile: func(path string) ([]byte, error) {
			if path != "/tmp/custom-ajq.toml" {
				t.Fatalf("ReadFile path = %q", path)
			}
			return []byte("backend = \"mock\"\nmodel = \"m-file\"\nbase_url = \"http://file\"\nmax_calls = 7\nbackend_concurrency = 2\nwindow_bytes = 8192\nno_cache = true\n"), nil
		},
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}
	if !values.BackendSet || values.Backend != "mock" || !values.ModelSet || values.Model != "m-file" || !values.BaseURLSet || values.BaseURL != "http://file" || !values.MaxCallsSet || values.MaxCalls != 7 || !values.BackendConcurrencySet || values.BackendConcurrency != 2 || !values.WindowBytesSet || values.WindowBytes != 8192 || !values.NoCacheSet || !values.NoCache {
		t.Fatalf("values not fully decoded: %+v", values)
	}
	if stderr.String() != "" {
		t.Fatalf("unexpected stderr warnings: %q", stderr.String())
	}
}

func TestLoadExplicitMissingFileIsError(t *testing.T) {
	_, err := LoadWithOptions(LoadOptions{
		Getenv: func(key string) string {
			if key == "AJQ_CONFIG" {
				return "/tmp/missing.toml"
			}
			return ""
		},
		ReadFile: func(string) ([]byte, error) { return nil, fs.ErrNotExist },
	})
	if err == nil || !strings.Contains(err.Error(), "read config /tmp/missing.toml") {
		t.Fatalf("expected explicit missing file read error, got %v", err)
	}
}

func TestLoadReadAndParseErrorsSurface(t *testing.T) {
	readErr := errors.New("permission denied")
	_, err := LoadWithOptions(LoadOptions{
		Getenv: func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/xdg"
			}
			return ""
		},
		ReadFile: func(string) ([]byte, error) { return nil, readErr },
	})
	if !errors.Is(err, readErr) {
		t.Fatalf("read error = %v, want wrapping permission error", err)
	}

	_, err = LoadWithOptions(LoadOptions{
		Getenv: func(key string) string {
			if key == "XDG_CONFIG_HOME" {
				return "/xdg"
			}
			return ""
		},
		ReadFile: func(string) ([]byte, error) { return []byte("backend = ["), nil },
	})
	if err == nil || !strings.Contains(err.Error(), "parse config") {
		t.Fatalf("parse error = %v, want parse config", err)
	}
}

func TestResolvePrecedenceForEveryField(t *testing.T) {
	defaults := Values{Backend: "default-backend", BackendSet: true, Model: "default-model", ModelSet: true, BaseURL: "http://default", BaseURLSet: true, MaxCalls: 1, MaxCallsSet: true, BackendConcurrency: 1, BackendConcurrencySet: true, WindowBytes: 1024, WindowBytesSet: true, NoCache: true, NoCacheSet: true}
	file := Values{Backend: "file-backend", BackendSet: true, Model: "file-model", ModelSet: true, BaseURL: "http://file", BaseURLSet: true, MaxCalls: 2, MaxCallsSet: true, BackendConcurrency: 2, BackendConcurrencySet: true, WindowBytes: 2048, WindowBytesSet: true, NoCache: false, NoCacheSet: true}
	env := Values{Backend: "env-backend", BackendSet: true, Model: "env-model", ModelSet: true, BaseURL: "http://env", BaseURLSet: true, MaxCalls: 3, MaxCallsSet: true, BackendConcurrency: 3, BackendConcurrencySet: true, WindowBytes: 4096, WindowBytesSet: true}
	flags := Values{Backend: "flag-backend", BackendSet: true, Model: "flag-model", ModelSet: true, BaseURL: "http://flag", BaseURLSet: true, MaxCalls: 0, MaxCallsSet: true, BackendConcurrency: 4, BackendConcurrencySet: true, WindowBytes: 8192, WindowBytesSet: true, NoCache: false, NoCacheSet: true}

	got := Resolve(flags, env, file, defaults)
	want := Settings{Backend: "flag-backend", Model: "flag-model", BaseURL: "http://flag", BaseURLExplicit: true, MaxCalls: 0, BackendConcurrency: 4, WindowBytes: 8192, NoCache: false}
	if got != want {
		t.Fatalf("Resolve() = %+v, want %+v", got, want)
	}

	got = Resolve(Values{}, env, file, defaults)
	want = Settings{Backend: "env-backend", Model: "env-model", BaseURL: "http://env", BaseURLExplicit: true, MaxCalls: 3, BackendConcurrency: 3, WindowBytes: 4096, NoCache: false}
	if got != want {
		t.Fatalf("Resolve(no flags) = %+v, want %+v", got, want)
	}

	got = Resolve(Values{}, Values{}, Values{}, defaults)
	want = Settings{Backend: "default-backend", Model: "default-model", BaseURL: "http://default", BaseURLExplicit: false, MaxCalls: 1, BackendConcurrency: 1, WindowBytes: 1024, NoCache: true}
	if got != want {
		t.Fatalf("Resolve(no flags) = %+v, want %+v", got, want)
	}
}

func TestEnvReadsOnlyAJQSettings(t *testing.T) {
	values, err := Env(func(key string) string {
		switch key {
		case "AJQ_BACKEND":
			return "mock"
		case "AJQ_MODEL":
			return "env-model"
		case "AJQ_BASE_URL":
			return "http://env"
		case "AJQ_MAX_CALLS":
			return "7"
		case "AJQ_BACKEND_CONCURRENCY":
			return "2"
		case "AJQ_WINDOW_BYTES":
			return "8192"
		case "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OPENROUTER_API_KEY":
			t.Fatalf("Env should not read provider credential %s", key)
		}
		return ""
	})
	if err != nil {
		t.Fatalf("Env returned error: %v", err)
	}
	if values.Backend != "mock" || values.Model != "env-model" || values.BaseURL != "http://env" || values.MaxCalls != 7 || !values.MaxCallsSet || values.BackendConcurrency != 2 || !values.BackendConcurrencySet || values.WindowBytes != 8192 || !values.WindowBytesSet {
		t.Fatalf("Env() = %+v", values)
	}
}

func TestEnvRejectsInvalidMaxCalls(t *testing.T) {
	for _, value := range []string{"-1", "not-int"} {
		_, err := Env(func(key string) string {
			if key == "AJQ_MAX_CALLS" {
				return value
			}
			return ""
		})
		if err == nil || !strings.Contains(err.Error(), "AJQ_MAX_CALLS") {
			t.Fatalf("Env(AJQ_MAX_CALLS=%q) error = %v, want AJQ_MAX_CALLS validation", value, err)
		}
	}
}

func TestUnknownKeyWarning(t *testing.T) {
	var stderr strings.Builder
	_, err := LoadWithOptions(LoadOptions{
		Getenv: func(key string) string {
			if key == "AJQ_CONFIG" {
				return "/tmp/config.toml"
			}
			return ""
		},
		ReadFile: func(string) ([]byte, error) {
			return []byte("backend = \"mock\"\nfuture_key = true\n[nested]\nfuture = 1\n"), nil
		},
		Stderr: &stderr,
	})
	if err != nil {
		t.Fatalf("LoadWithOptions returned error: %v", err)
	}
	got := stderr.String()
	if !strings.Contains(got, `unknown config key "future_key" ignored`) || !strings.Contains(got, `unknown config key "nested.future" ignored`) {
		t.Fatalf("stderr missing unknown-key warnings: %q", got)
	}
}

func TestCredentialKeyRejectionBeforeUnknownWarnings(t *testing.T) {
	for _, body := range []string{
		"api_key = \"secret\"\nfuture_key = true\n",
		"[provider]\ntoken = \"secret\"\n",
		"apikey = \"secret\"\n",
		"[backend]\napi_key = \"secret\"\n",
		"[[backend]]\napi_key = \"secret\"\n",
		"providers = [{ token = \"secret\" }]\n",
	} {
		t.Run(body, func(t *testing.T) {
			var stderr strings.Builder
			_, err := LoadWithOptions(LoadOptions{
				Getenv: func(key string) string {
					if key == "AJQ_CONFIG" {
						return "/tmp/config.toml"
					}
					return ""
				},
				ReadFile: func(string) ([]byte, error) { return []byte(body), nil },
				Stderr:   &stderr,
			})
			if err == nil || !strings.Contains(err.Error(), "API keys are env-only") || !strings.Contains(err.Error(), "ANTHROPIC_API_KEY") {
				t.Fatalf("credential rejection error = %v", err)
			}
			if stderr.String() != "" {
				t.Fatalf("credential rejection should happen before unknown warnings, got %q", stderr.String())
			}
		})
	}
}

func TestBackendConcurrencyValidation(t *testing.T) {
	for _, value := range []string{"0", "-1", "not-int"} {
		t.Run("environment "+value, func(t *testing.T) {
			_, err := Env(func(key string) string {
				if key == "AJQ_BACKEND_CONCURRENCY" {
					return value
				}
				return ""
			})
			if err == nil || !strings.Contains(err.Error(), "AJQ_BACKEND_CONCURRENCY") {
				t.Fatalf("Env(AJQ_BACKEND_CONCURRENCY=%q) error = %v", value, err)
			}
		})
	}
	for _, value := range []string{"0", "-1"} {
		t.Run("TOML "+value, func(t *testing.T) {
			_, err := parse([]byte("backend_concurrency = "+value+"\n"), nil)
			if err == nil || !strings.Contains(err.Error(), "config backend_concurrency must be positive") {
				t.Fatalf("parse(%q) error = %v", value, err)
			}
		})
	}
}
