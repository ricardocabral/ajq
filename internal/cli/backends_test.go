package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/anthropicbk"
	"github.com/ricardocabral/ajq/internal/backend/oai"
	"github.com/ricardocabral/ajq/internal/backend/ollamabk"
	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/provision"
)

func TestBackendRegistryLookupAndUnknownNames(t *testing.T) {
	if _, ok := lookupBackend("mock"); !ok {
		t.Fatal("mock backend not registered")
	}
	if _, ok := lookupBackend("local"); !ok {
		t.Fatal("local backend not registered")
	}
	if _, ok := lookupBackend("ollama"); !ok {
		t.Fatal("ollama backend not registered")
	}
	if _, ok := lookupBackend("anthropic"); !ok {
		t.Fatal("anthropic backend not registered")
	}
	if _, ok := lookupBackend("openai"); !ok {
		t.Fatal("openai backend not registered")
	}
	if _, ok := lookupBackend("openrouter"); !ok {
		t.Fatal("openrouter backend not registered")
	}
	err := unknownBackendError("bogus")
	if err == nil || !strings.Contains(err.Error(), `unknown backend "bogus"`) || !strings.Contains(err.Error(), `"anthropic", "local", "mock", "ollama", "openai", "openrouter"`) {
		t.Fatalf("unexpected unknown backend error: %v", err)
	}
}

func TestBackendHelpListsEveryRegisteredBackend(t *testing.T) {
	cmd := NewRootCommand(Options{})
	flag := cmd.Flags().Lookup("backend")
	if flag == nil {
		t.Fatal("--backend flag not registered")
	}
	help := flag.Usage
	for _, name := range validBackendNames() {
		if !strings.Contains(help, fmt.Sprintf("%q", name)) {
			t.Fatalf("--backend help missing registered backend %q: %q", name, help)
		}
	}
	for _, registration := range backendRegistry {
		if strings.TrimSpace(registration.HelpDescriptor) == "" {
			t.Fatalf("backend %q missing help descriptor", registration.Name)
		}
		if !strings.Contains(help, "("+registration.HelpDescriptor+")") {
			t.Fatalf("--backend help missing descriptor %q for %q: %q", registration.HelpDescriptor, registration.Name, help)
		}
	}
}

func TestOpenAICompatibleConstructorsRequireEnvKeyAndModel(t *testing.T) {
	for _, tt := range []struct {
		backendName string
		envVar      string
	}{
		{backendName: "openai", envVar: "OPENAI_API_KEY"},
		{backendName: "openrouter", envVar: "OPENROUTER_API_KEY"},
	} {
		t.Run(tt.backendName+" missing key", func(t *testing.T) {
			t.Setenv(tt.envVar, "")
			registration, ok := lookupBackend(tt.backendName)
			if !ok {
				t.Fatalf("%s backend not registered", tt.backendName)
			}
			_, _, err := registration.Construct(Options{}, config.Settings{Model: "gpt-test", BaseURL: registration.DefaultBaseURL})
			if err == nil || !strings.Contains(err.Error(), tt.envVar) {
				t.Fatalf("Construct error = %v, want env var %s", err, tt.envVar)
			}
		})
		t.Run(tt.backendName+" missing model", func(t *testing.T) {
			t.Setenv(tt.envVar, "test-key")
			registration, ok := lookupBackend(tt.backendName)
			if !ok {
				t.Fatalf("%s backend not registered", tt.backendName)
			}
			_, _, err := registration.Construct(Options{}, config.Settings{BaseURL: registration.DefaultBaseURL})
			if err == nil || !strings.Contains(err.Error(), "requires a model") || !strings.Contains(err.Error(), "--model") {
				t.Fatalf("Construct error = %v, want missing model guidance", err)
			}
		})
	}
}

func TestOpenAICompatibleBaseURLSchemeValidation(t *testing.T) {
	for _, backendName := range []string{"openai", "openrouter"} {
		t.Run(backendName, func(t *testing.T) {
			registration, ok := lookupBackend(backendName)
			if !ok {
				t.Fatalf("%s backend not registered", backendName)
			}
			t.Setenv(registration.APIKeyEnv, "test-key")

			accepted := []struct {
				name    string
				baseURL string
				want    string
			}{
				{name: "https remote", baseURL: "https://api.example.test/v1/", want: "https://api.example.test/v1"},
				{name: "http IPv4 loopback", baseURL: "http://127.0.0.1:8000/v1/", want: "http://127.0.0.1:8000/v1"},
				{name: "http localhost", baseURL: "http://localhost:8000/v1/", want: "http://localhost:8000/v1"},
				{name: "http IPv6 loopback", baseURL: "http://[::1]:8000/v1/", want: "http://[::1]:8000/v1"},
				{name: "default", baseURL: "", want: registration.DefaultBaseURL},
			}
			for _, tt := range accepted {
				t.Run(tt.name, func(t *testing.T) {
					be, _, err := registration.Construct(Options{}, config.Settings{Model: "gpt-test", BaseURL: tt.baseURL})
					if err != nil {
						t.Fatalf("Construct returned error: %v", err)
					}
					oaiBackend, ok := be.(*oai.Backend)
					if !ok {
						t.Fatalf("Construct returned %T, want *oai.Backend", be)
					}
					if oaiBackend.BaseURL != tt.want {
						t.Fatalf("BaseURL = %q, want %q", oaiBackend.BaseURL, tt.want)
					}
				})
			}

			rejected := []struct {
				name    string
				baseURL string
			}{
				{name: "http remote", baseURL: "http://example.com/v1"},
				{name: "http non-loopback IPv6", baseURL: "http://[2001:db8::1]:8000/v1"},
				{name: "other scheme", baseURL: "ftp://example.com/v1"},
				{name: "garbage", baseURL: "not a url"},
			}
			for _, tt := range rejected {
				t.Run(tt.name, func(t *testing.T) {
					_, _, err := registration.Construct(Options{}, config.Settings{Model: "gpt-test", BaseURL: tt.baseURL})
					if err == nil {
						t.Fatalf("expected %q to be rejected", tt.baseURL)
					}
					for _, want := range []string{tt.baseURL, "HTTPS", "loopback"} {
						if !strings.Contains(err.Error(), want) {
							t.Fatalf("error %q missing %q", err.Error(), want)
						}
					}
				})
			}
		})
	}
}

func TestOpenAIBaseURLOverrideReachesClientAndModelIDUsesBackendPrefix(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("OPENAI_API_KEY", "test-key")

	var gotPath, gotAuth, gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode request body: %v", err)
		}
		gotModel = body.Model
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"true"}}]}`))
	}))
	defer srv.Close()

	stdout, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, "--backend", "openai", "--model", "gpt-test", "--base-url", srv.URL+"/v1/", `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("request path = %q, want /v1/chat/completions", gotPath)
	}
	if gotAuth != "Bearer test-key" {
		t.Fatalf("Authorization = %q, want bearer test-key", gotAuth)
	}
	if gotModel != "gpt-test" {
		t.Fatalf("provider model = %q, want raw model gpt-test", gotModel)
	}

	registration, _ := lookupBackend("openai")
	got, err := registration.ModelIdentity(config.Settings{Model: "gpt-test"})
	if err != nil {
		t.Fatalf("ModelIdentity returned error: %v", err)
	}
	if got != "openai/gpt-test" {
		t.Fatalf("ModelIdentity = %q, want openai/gpt-test", got)
	}
}

func TestModelIdentityIncludesBaseURLForHTTPBackends(t *testing.T) {
	tests := []struct {
		backendName string
		model       string
		wantEmpty   string
	}{
		{backendName: "openai", model: "gpt-test", wantEmpty: "openai/gpt-test"},
		{backendName: "ollama", model: "llama3.2", wantEmpty: "ollama/llama3.2"},
		{backendName: "local", model: "qwen2.5-3b", wantEmpty: "local/qwen2.5-3b"},
	}
	for _, tt := range tests {
		t.Run(tt.backendName, func(t *testing.T) {
			registration, ok := lookupBackend(tt.backendName)
			if !ok {
				t.Fatalf("%s backend not registered", tt.backendName)
			}
			first, err := registration.ModelIdentity(config.Settings{Model: tt.model, BaseURL: "http://endpoint-one.test/v1"})
			if err != nil {
				t.Fatalf("first ModelIdentity returned error: %v", err)
			}
			second, err := registration.ModelIdentity(config.Settings{Model: tt.model, BaseURL: "http://endpoint-two.test/v1"})
			if err != nil {
				t.Fatalf("second ModelIdentity returned error: %v", err)
			}
			if first == second {
				t.Fatalf("ModelIdentity values must differ across base URLs: %q", first)
			}
			empty, err := registration.ModelIdentity(config.Settings{Model: tt.model})
			if err != nil {
				t.Fatalf("empty-base-url ModelIdentity returned error: %v", err)
			}
			if empty != tt.wantEmpty {
				t.Fatalf("empty-base-url ModelIdentity = %q, want %q", empty, tt.wantEmpty)
			}
		})
	}
}

func TestOpenAIBaseURLSeparatesPersistentSemanticCache(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "test-key")

	newServer := func(response string) (*httptest.Server, *int) {
		calls := new(int)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			*calls++
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"` + response + `"}}]}`))
		}))
		return server, calls
	}
	firstServer, firstCalls := newServer("true")
	defer firstServer.Close()
	secondServer, secondCalls := newServer("false")
	defer secondServer.Close()

	run := func(baseURL string) (string, string, error) {
		var out, errBuf bytes.Buffer
		err := Execute(context.Background(), Options{
			Stdin:  strings.NewReader(`{"msg":"keep"}`),
			Stdout: &out,
			Stderr: &errBuf,
		}, []string{"--backend", "openai", "--model", "gpt-test", "--base-url", baseURL + "/v1", `.msg =~ "keep"`})
		return out.String(), errBuf.String(), err
	}

	stdout, stderr, err := run(firstServer.URL)
	if err != nil {
		t.Fatalf("first semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" || stderr != "" {
		t.Fatalf("first stdout=%q stderr=%q", stdout, stderr)
	}
	stdout, stderr, err = run(secondServer.URL)
	if err != nil {
		t.Fatalf("second semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "false\n" || stderr != "" {
		t.Fatalf("second stdout=%q stderr=%q", stdout, stderr)
	}
	if *firstCalls != 1 || *secondCalls != 1 {
		t.Fatalf("endpoint calls = (%d, %d), want (1, 1)", *firstCalls, *secondCalls)
	}
}

func TestAnthropicRegistrationRequiresEnvKeyAndUsesResolvedModelID(t *testing.T) {
	registration, ok := lookupBackend("anthropic")
	if !ok {
		t.Fatal("anthropic backend not registered")
	}
	if registration.APIKeyEnv != anthropicbk.APIKeyEnv {
		t.Fatalf("anthropic APIKeyEnv = %q, want %s", registration.APIKeyEnv, anthropicbk.APIKeyEnv)
	}
	if registration.DefaultModel != anthropicbk.DefaultModel {
		t.Fatalf("anthropic DefaultModel = %q, want %s", registration.DefaultModel, anthropicbk.DefaultModel)
	}
	got, err := registration.ModelIdentity(config.Settings{Model: "sonnet"})
	if err != nil {
		t.Fatalf("ModelIdentity returned error: %v", err)
	}
	if got != "anthropic/claude-sonnet-5" {
		t.Fatalf("ModelIdentity = %q, want anthropic/claude-sonnet-5", got)
	}
	t.Setenv(anthropicbk.APIKeyEnv, "")
	_, _, err = registration.Construct(Options{}, config.Settings{Model: anthropicbk.DefaultModel})
	if err == nil || !strings.Contains(err.Error(), anthropicbk.APIKeyEnv) || !strings.Contains(err.Error(), "console.anthropic.com") {
		t.Fatalf("Construct error = %v, want env var and key guidance", err)
	}
}

func TestOllamaRegistrationRequiresModelAndUsesPrefixedModelID(t *testing.T) {
	registration, ok := lookupBackend("ollama")
	if !ok {
		t.Fatal("ollama backend not registered")
	}
	if registration.APIKeyEnv != "" {
		t.Fatalf("ollama APIKeyEnv = %q, want empty", registration.APIKeyEnv)
	}
	_, _, err := registration.Construct(Options{}, config.Settings{})
	if err == nil || !strings.Contains(err.Error(), "requires a model") || !strings.Contains(err.Error(), "--model llama3.2") || !strings.Contains(err.Error(), "ollama list") {
		t.Fatalf("Construct error = %v, want missing model guidance", err)
	}
	got, err := registration.ModelIdentity(config.Settings{Model: "llama3.2"})
	if err != nil {
		t.Fatalf("ModelIdentity returned error: %v", err)
	}
	if got != "ollama/llama3.2" {
		t.Fatalf("ModelIdentity = %q, want ollama/llama3.2", got)
	}
}

func TestOllamaBaseURLResolutionOrder(t *testing.T) {
	registration, ok := lookupBackend("ollama")
	if !ok {
		t.Fatal("ollama backend not registered")
	}
	t.Setenv("OLLAMA_HOST", "127.0.0.1:15555")
	be, _, err := registration.Construct(Options{}, config.Settings{Model: "llama3.2", BaseURL: "http://flag-host:16666/"})
	if err != nil {
		t.Fatalf("Construct explicit base URL returned error: %v", err)
	}
	ollama, ok := be.(*ollamabk.Backend)
	if !ok {
		t.Fatalf("Construct returned %T, want *ollamabk.Backend", be)
	}
	if ollama.BaseURL != "http://flag-host:16666" {
		t.Fatalf("explicit BaseURL = %q, want flag URL", ollama.BaseURL)
	}

	be, _, err = registration.Construct(Options{}, config.Settings{Model: "llama3.2"})
	if err != nil {
		t.Fatalf("Construct OLLAMA_HOST host:port returned error: %v", err)
	}
	ollama = be.(*ollamabk.Backend)
	if ollama.BaseURL != "http://127.0.0.1:15555" {
		t.Fatalf("OLLAMA_HOST host:port BaseURL = %q", ollama.BaseURL)
	}

	t.Setenv("OLLAMA_HOST", "https://ollama.example.test:11434/")
	be, _, err = registration.Construct(Options{}, config.Settings{Model: "llama3.2"})
	if err != nil {
		t.Fatalf("Construct OLLAMA_HOST URL returned error: %v", err)
	}
	ollama = be.(*ollamabk.Backend)
	if ollama.BaseURL != "https://ollama.example.test:11434" {
		t.Fatalf("OLLAMA_HOST URL BaseURL = %q", ollama.BaseURL)
	}

	t.Setenv("OLLAMA_HOST", "")
	be, _, err = registration.Construct(Options{}, config.Settings{Model: "llama3.2"})
	if err != nil {
		t.Fatalf("Construct default base URL returned error: %v", err)
	}
	ollama = be.(*ollamabk.Backend)
	if ollama.BaseURL != ollamabk.DefaultBaseURL {
		t.Fatalf("default BaseURL = %q, want %q", ollama.BaseURL, ollamabk.DefaultBaseURL)
	}
}

func TestOllamaLiveSmokeOptIn(t *testing.T) {
	if os.Getenv("AJQ_OLLAMA_LIVE") != "1" {
		t.Skip("set AJQ_OLLAMA_LIVE=1 to run live Ollama smoke test")
	}
	baseURL, err := ollamabk.ResolveBaseURL("")
	if err != nil {
		t.Fatalf("resolve Ollama base URL: %v", err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, strings.TrimRight(baseURL, "/")+"/api/tags", nil)
	if err != nil {
		t.Fatalf("build /api/tags request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Skipf("Ollama server did not answer at %s: %v", baseURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		t.Skipf("Ollama server at %s returned status %d", baseURL, resp.StatusCode)
	}
	var tags struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tags); err != nil {
		t.Fatalf("decode /api/tags response: %v", err)
	}
	model := strings.TrimSpace(os.Getenv("AJQ_OLLAMA_MODEL"))
	if model == "" && len(tags.Models) > 0 {
		model = tags.Models[0].Name
	}
	if model == "" {
		t.Skip("Ollama server answered but no models are installed; run `ollama pull llama3.2`")
	}
	be := &ollamabk.Backend{BaseURL: baseURL, Model: model, HTTPClient: client}
	if err := be.Warm(context.Background()); err != nil {
		t.Fatalf("Ollama live Warm failed: %v", err)
	}
}

func TestLocalBaseURLDrivesDaemonConfig(t *testing.T) {
	valid := []struct {
		name    string
		baseURL string
		host    string
		port    int
		trimmed string
	}{
		{
			name:    "IPv4 loopback",
			baseURL: "http://127.0.0.1:9000/",
			host:    "127.0.0.1",
			port:    9000,
			trimmed: "http://127.0.0.1:9000",
		},
		{
			name:    "IPv6 loopback",
			baseURL: "http://[::1]:9000/",
			host:    "::1",
			port:    9000,
			trimmed: "http://[::1]:9000",
		},
	}
	for _, tt := range valid {
		t.Run(tt.name, func(t *testing.T) {
			cfg, baseURL, err := localDaemonConfig(config.Settings{BaseURL: tt.baseURL})
			if err != nil {
				t.Fatalf("localDaemonConfig returned error: %v", err)
			}
			if cfg.Host != tt.host || cfg.Port != tt.port || baseURL != tt.trimmed {
				t.Fatalf("cfg=%+v baseURL=%q, want %s:%d %q", cfg, baseURL, tt.host, tt.port, tt.trimmed)
			}
		})
	}

	invalid := map[string]string{
		"https://127.0.0.1:9000":         "non-http local base URL",
		"http://example.com:9000":        "non-loopback local base URL",
		"http://[2001:db8::1]:9000":      "non-loopback IPv6 base URL",
		"http://127.0.0.1:9000?x=1":      "query string",
		"http://127.0.0.1:9000#fragment": "fragment",
		"http://user@127.0.0.1:9000":     "userinfo",
		"http://127.0.0.1:0":             "port 0",
	}
	for baseURL, name := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, _, err := localDaemonConfig(config.Settings{BaseURL: baseURL}); err == nil {
				t.Fatalf("expected %s to be rejected", baseURL)
			}
		})
	}
}

func TestCloudConflictsWithExplicitDifferentBackend(t *testing.T) {
	_, stderr, err := executeForBackendTest(t, nil, "{}", "--cloud", "--backend", "mock", ".")
	if err == nil {
		t.Fatal("expected --cloud/--backend conflict")
	}
	if got := ExitCode(err); got != 2 {
		t.Fatalf("ExitCode = %d, want 2", got)
	}
	if !strings.Contains(stderr, `--cloud conflicts with --backend "mock"`) {
		t.Fatalf("stderr missing conflict detail: %q", stderr)
	}
}

func TestBackendRegistryDefaultMaxCalls(t *testing.T) {
	for _, name := range []string{"anthropic", "openai", "openrouter"} {
		registration, ok := lookupBackend(name)
		if !ok {
			t.Fatalf("%s backend not registered", name)
		}
		if !registration.Paid || registration.DefaultMaxCalls != 100 || registration.defaults().MaxCalls != 100 {
			t.Fatalf("%s registration paid=%v defaultMaxCalls=%d defaults=%+v, want paid default cap 100", name, registration.Paid, registration.DefaultMaxCalls, registration.defaults())
		}
	}
	for _, name := range []string{"mock", "local", "ollama"} {
		registration, ok := lookupBackend(name)
		if !ok {
			t.Fatalf("%s backend not registered", name)
		}
		if registration.Paid || registration.DefaultMaxCalls != 0 || registration.defaults().MaxCalls != 0 {
			t.Fatalf("%s registration paid=%v defaultMaxCalls=%d defaults=%+v, want unlimited default", name, registration.Paid, registration.DefaultMaxCalls, registration.defaults())
		}
	}
}

func TestCloudSelectsAnthropicDefaultModel(t *testing.T) {
	isolateConfigEnv(t)
	be := &recordingSemanticBackend{}
	var gotSettings config.Settings
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		gotSettings = settings
		return be, nil
	})

	stdout, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, "--cloud", `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	if gotSettings.Backend != "anthropic" || gotSettings.Model != anthropicbk.DefaultModel {
		t.Fatalf("settings=%+v, want anthropic default model", gotSettings)
	}
	calls, modelIDs := be.snapshot()
	if calls != 1 || len(modelIDs) == 0 || modelIDs[0] != "anthropic/"+anthropicbk.DefaultModel {
		t.Fatalf("calls=%d modelIDs=%v, want anthropic default model ID", calls, modelIDs)
	}
}

func TestPaidBackendDefaultMaxCallsAbortIsDiscoverableAndOverrideable(t *testing.T) {
	isolateConfigEnv(t)
	be := &recordingSemanticBackend{}
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		if settings.MaxCalls != 100 {
			t.Fatalf("settings.MaxCalls = %d, want paid default 100", settings.MaxCalls)
		}
		return be, nil
	})

	stdout, stderr, err := executeForBackendTest(t, nil, maxCallsInput(101), "--cloud", `.[] | .msg =~ "needle"`)
	if err == nil {
		t.Fatal("expected paid default max-calls abort")
	}
	if stdout != "" {
		t.Fatalf("stdout = %q, want empty on pre-spend abort", stdout)
	}
	for _, want := range []string{"max calls cap exceeded", "cap 100", "101 post-dedup backend judgements", "--explain", "--max-calls", "paid-backend default"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q in %q", want, stderr)
		}
	}
	calls, _ := be.snapshot()
	if calls != 0 {
		t.Fatalf("backend calls = %d, want pre-spend abort", calls)
	}

	be = &recordingSemanticBackend{}
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		if settings.MaxCalls != 0 {
			t.Fatalf("settings.MaxCalls = %d, want flag override unlimited", settings.MaxCalls)
		}
		return be, nil
	})
	stdout, stderr, err = executeForBackendTest(t, nil, maxCallsInput(101), "--cloud", "--max-calls", "0", `.[] | .msg =~ "needle"`)
	if err != nil {
		t.Fatalf("override unlimited returned error: %v; stderr=%q", err, stderr)
	}
	if stdout == "" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q, want data on stdout only", stdout, stderr)
	}
	calls, _ = be.snapshot()
	if calls != 1 {
		t.Fatalf("backend batch calls = %d, want one batch after override", calls)
	}
}

func maxCallsInput(n int) string {
	var b strings.Builder
	b.WriteByte('[')
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `{"msg":"item-%d"}`, i)
	}
	b.WriteByte(']')
	return b.String()
}

func TestStatsPaidBackendIncludesEstimatedCostOnStderr(t *testing.T) {
	isolateConfigEnv(t)
	be := &recordingSemanticBackend{}
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		return be, nil
	})

	stdout, stderr, err := executeForBackendTest(t, nil, `[{"msg":"keep"},{"msg":"drop"}]`, "--cloud", "--stats", `.[] | .msg =~ "keep"`)
	if err != nil {
		t.Fatalf("paid --stats query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\ntrue\n" {
		t.Fatalf("stdout = %q, want data only", stdout)
	}
	for _, want := range []string{"ajq stats:\n", "  post_dedup_backend_calls: 2\n", "  estimated_cost_usd: ~$0.01 (2 calls × model anthropic/claude-haiku-4-5)\n"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q in %q", want, stderr)
		}
	}
}

func TestCloudModelAliasResolvesCacheIdentity(t *testing.T) {
	isolateConfigEnv(t)
	be := &recordingSemanticBackend{}
	var gotSettings config.Settings
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		gotSettings = settings
		return be, nil
	})

	stdout, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, "--cloud", "--model", "sonnet", `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	if gotSettings.Model != "sonnet" {
		t.Fatalf("settings.Model = %q, want raw flag alias sonnet", gotSettings.Model)
	}
	calls, modelIDs := be.snapshot()
	if calls != 1 || len(modelIDs) == 0 || modelIDs[0] != "anthropic/claude-sonnet-5" {
		t.Fatalf("calls=%d modelIDs=%v, want resolved sonnet cache ID", calls, modelIDs)
	}
}

func TestAnthropicLiveSmokeOptIn(t *testing.T) {
	if os.Getenv("AJQ_ANTHROPIC_LIVE") != "1" {
		t.Skip("set AJQ_ANTHROPIC_LIVE=1 to run live Anthropic smoke test")
	}
	if strings.TrimSpace(os.Getenv(anthropicbk.APIKeyEnv)) == "" {
		t.Skipf("set %s to run live Anthropic smoke test", anthropicbk.APIKeyEnv)
	}
	isolateConfigEnv(t)
	stdout, stderr, err := executeForBackendTest(t, nil, `{"msg":"refund request"}`, "--cloud", `.msg =~ "asks for a refund"`)
	if err != nil {
		t.Fatalf("live Anthropic semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if strings.TrimSpace(stdout) != "true" && strings.TrimSpace(stdout) != "false" {
		t.Fatalf("stdout=%q, want one boolean result", stdout)
	}
}

func withAnthropicConstructor(t *testing.T, fn func(config.Settings) (backend.Backend, error)) {
	t.Helper()
	old := constructAnthropicBackend
	constructAnthropicBackend = fn
	t.Cleanup(func() { constructAnthropicBackend = old })
}

type recordingSemanticBackend struct {
	mu       sync.Mutex
	modelIDs []string
	calls    int
}

func (b *recordingSemanticBackend) Warm(context.Context) error { return nil }

func (b *recordingSemanticBackend) Judge(_ context.Context, batch []backend.Judgement) ([]backend.Result, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls++
	results := make([]backend.Result, len(batch))
	for i, judgement := range batch {
		b.modelIDs = append(b.modelIDs, judgement.ModelID)
		results[i] = backend.Result{Value: true}
	}
	return results, nil
}

func (b *recordingSemanticBackend) snapshot() (calls int, modelIDs []string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.calls, append([]string(nil), b.modelIDs...)
}

type fakePlanProvisioner struct{ plan provision.Plan }

func (f fakePlanProvisioner) Plan() (provision.Plan, error)            { return f.plan, nil }
func (f fakePlanProvisioner) PlanModel(string) (provision.Plan, error) { return f.plan, nil }
func (f fakePlanProvisioner) PlanModelOnly(string) (provision.Plan, error) {
	return f.plan, nil
}
func (f fakePlanProvisioner) Install(context.Context, provision.Plan, provision.ProgressFunc) error {
	return nil
}
func (f fakePlanProvisioner) InstallModel(context.Context, provision.Plan, provision.ProgressFunc) error {
	return nil
}

func TestFlagEnvConfigPrecedenceEndToEnd(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"local\"\nmodel = \"file-model\"\nbase_url = \"http://file\"\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "local")
	t.Setenv("AJQ_MODEL", "env-model")
	t.Setenv("AJQ_BASE_URL", "http://env")

	be := &recordingSemanticBackend{}
	stdout, stderr, err := executeForBackendTest(t, be, `{"msg":"keep"}`, "--model", "qwen2.5-3b", `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	calls, modelIDs := be.snapshot()
	if calls != 1 || len(modelIDs) == 0 || modelIDs[0] != "local/qwen2.5-3b@http://env" {
		t.Fatalf("calls=%d modelIDs=%v, want local/qwen2.5-3b@http://env", calls, modelIDs)
	}
}

func TestLocalBackendExplicitBaseURLSkipsManagedProvisionAndAuth(t *testing.T) {
	isolateConfigEnv(t)
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path != "/completion" {
			t.Errorf("path = %s, want /completion", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"content":"true"}`))
	}))
	defer srv.Close()
	var out, errBuf bytes.Buffer
	err := Execute(context.Background(), Options{
		Stdin:     strings.NewReader(`{"msg":"keep"}`),
		Stdout:    &out,
		Stderr:    &errBuf,
		Provision: fakePlanProvisioner{plan: provision.Plan{}},
	}, []string{"--backend", "local", "--base-url", srv.URL, "--model", "qwen2.5-3b", `.msg =~ "keep"`})
	if err != nil {
		t.Fatalf("explicit local base-url returned error: %v; stderr=%q", err, errBuf.String())
	}
	if out.String() != "true\n" {
		t.Fatalf("stdout = %q, want true", out.String())
	}
	if gotAuth != "" {
		t.Fatalf("explicit external server received Authorization %q", gotAuth)
	}
}

func TestLocalBackendDefaultModelUsesCatalogIdentityThroughCLI(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"local\"\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")

	be := &recordingSemanticBackend{}
	_, stderr, err := executeForBackendTest(t, be, `{"msg":"keep"}`, `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	calls, modelIDs := be.snapshot()
	if calls != 1 || len(modelIDs) == 0 || modelIDs[0] != "local/qwen2.5-1.5b@http://127.0.0.1:8081" {
		t.Fatalf("calls=%d modelIDs=%v, want local/qwen2.5-1.5b@http://127.0.0.1:8081", calls, modelIDs)
	}
}

func TestLocalBackendUnknownCatalogModelErrors(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"local\"\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")

	_, stderr, err := executeForBackendTest(t, &recordingSemanticBackend{}, `{"msg":"keep"}`, "--model", "bogus", `.msg =~ "keep"`)
	if err == nil {
		t.Fatal("expected unknown model error")
	}
	for _, want := range []string{"unknown model \"bogus\"", "qwen2.5-1.5b", "qwen2.5-3b", "qwen3-4b"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr %q missing %q", stderr, want)
		}
	}
}

func TestLocalBackendKnownModelNotInstalledSuggestsPull(t *testing.T) {
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	configPath := writeTempConfig(t, "backend = \"local\"\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")

	plan := provision.Plan{
		Engine: provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindEngine, Name: provision.EngineBinaryName}, Present: true, Source: "PATH", Path: "/opt/homebrew/bin/llama-server"},
		Model:  provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindModel, Name: "qwen2.5-3b"}, Present: false, Path: "/cache/models/qwen2.5-3b.gguf"},
	}
	var out, errBuf bytes.Buffer
	err := Execute(context.Background(), Options{Stdin: strings.NewReader(`{"msg":"keep"}`), Stdout: &out, Stderr: &errBuf, Provision: fakePlanProvisioner{plan: plan}}, []string{"--model", "qwen2.5-3b", `.msg =~ "keep"`})
	if err == nil {
		t.Fatal("expected not-installed model error")
	}
	if !strings.Contains(errBuf.String(), "ajq models pull qwen2.5-3b") {
		t.Fatalf("stderr should suggest model pull, got %q", errBuf.String())
	}
}

func TestConfigWarningsUseCLIStderr(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"mock\"\nfuture_key = true\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")

	stdout, stderr, err := executeForBackendTest(t, nil, `{"msg":"keep"}`, `.msg =~ "keep"`)
	if err != nil {
		t.Fatalf("semantic query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\n" {
		t.Fatalf("stdout = %q, want true", stdout)
	}
	if !strings.Contains(stderr, `ajq: warning: unknown config key "future_key" ignored`) {
		t.Fatalf("stderr missing config warning: %q", stderr)
	}
}

func TestPureJQDoesNotLoadConfigBackend(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"local\"\nfuture_key = true\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")

	be := &recordingSemanticBackend{}
	stdout, stderr, err := executeForBackendTest(t, be, `{"a":42}`, "-c", ".a")
	if err != nil {
		t.Fatalf("pure jq returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "42\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	calls, _ := be.snapshot()
	if calls != 0 {
		t.Fatalf("backend Judge calls = %d, want 0", calls)
	}
}

func TestPureJQDoesNotConstructAnthropicCloudBackend(t *testing.T) {
	configPath := writeTempConfig(t, "backend = \"anthropic\"\n")
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")
	withAnthropicConstructor(t, func(settings config.Settings) (backend.Backend, error) {
		t.Fatalf("pure jq should not construct anthropic backend; settings=%+v", settings)
		return nil, nil
	})

	stdout, stderr, err := executeForBackendTest(t, nil, `{"a":42}`, "-c", ".a")
	if err != nil {
		t.Fatalf("pure jq returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "42\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}

func isolateConfigEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")
	t.Setenv("AJQ_MAX_CALLS", "")
	t.Setenv("AJQ_BACKEND_CONCURRENCY", "")
}

func executeForBackendTest(t *testing.T, local backend.Backend, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	var out, errBuf bytes.Buffer
	err = Execute(context.Background(), Options{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errBuf, LocalBackend: local}, args)
	return out.String(), errBuf.String(), err
}

func writeTempConfig(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/config.toml"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write temp config: %v", err)
	}
	return path
}
