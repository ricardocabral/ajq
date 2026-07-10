package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/daemon"
	"github.com/ricardocabral/ajq/internal/provision"
)

func TestCapabilitiesJSONContract(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if err := Execute(context.Background(), Options{Stdout: &stdout, Stderr: &stderr}, []string{"capabilities", "--json"}); err != nil {
		t.Fatalf("capabilities --json returned error: %v", err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("capabilities --json stderr = %q", stderr.String())
	}
	if !strings.HasSuffix(stdout.String(), "\n") {
		t.Fatalf("capabilities --json must end with newline: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), ":null") {
		t.Fatalf("capabilities --json must not emit JSON null: %q", stdout.String())
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		t.Fatalf("capabilities --json is invalid JSON: %v", err)
	}
	wantKeys := []string{"schema_version", "ajq_version", "input_modes", "output_modes", "semantic_functions", "backends", "cost", "cache", "provisioning", "safety", "discovery"}
	if len(raw) != len(wantKeys) {
		t.Fatalf("top-level keys = %v, want %v", mapKeys(raw), wantKeys)
	}
	for _, key := range wantKeys {
		if _, ok := raw[key]; !ok {
			t.Fatalf("capabilities JSON missing required key %q", key)
		}
	}

	var got capabilitiesDocument
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode typed capabilities JSON: %v", err)
	}
	if got.SchemaVersion != "1" {
		t.Fatalf("schema_version = %q, want 1", got.SchemaVersion)
	}
	if got.AJQVersion == "" {
		t.Fatal("ajq_version must be populated")
	}
	if !reflect.DeepEqual(got.InputModes, []capabilityInputMode{
		{Name: "json", Selection: "auto", Streaming: true},
		{Name: "null", Selection: "--null-input", Streaming: false},
		{Name: "raw", Selection: "--raw-input", Streaming: true},
	}) {
		t.Fatalf("input_modes = %#v", got.InputModes)
	}
	if !reflect.DeepEqual(got.OutputModes, []capabilityOutputMode{
		{Format: "json", Style: "pretty", Default: true, StringOnly: false},
		{Format: "json", Style: "compact", Flag: "--compact-output", Default: false, StringOnly: false},
		{Format: "string", Style: "raw", Flag: "--raw-output", Default: false, StringOnly: true},
	}) {
		t.Fatalf("output_modes = %#v", got.OutputModes)
	}
	if _, ok := rawOutputFlag(raw["output_modes"], 0); ok {
		t.Fatal("default output mode must omit flag")
	}

	wantFunctions := []capabilitySemanticFunction{
		{Name: "sem_match", Kind: "predicate", ReturnType: "bool", Availability: capabilityAvailability{Status: "shipped", SupportedContexts: []string{"all"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{}}},
		{Name: "sem_classify", Kind: "value", ReturnType: "string", Availability: capabilityAvailability{Status: "shipped", SupportedContexts: []string{"all"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{}}},
		{Name: "sem_extract", Kind: "value", ReturnType: "string", Availability: capabilityAvailability{Status: "limited", SupportedContexts: []string{"interleaved_gated"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{"non_gated_unbounded_fails_loudly"}}},
		{Name: "sem_score", Kind: "value", ReturnType: "number", Availability: capabilityAvailability{Status: "limited", SupportedContexts: []string{"interleaved_gated", "three_phase_sort_by"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{"non_gated_unbounded_fails_loudly"}}},
		{Name: "sem_norm", Kind: "value", ReturnType: "string", Availability: capabilityAvailability{Status: "limited", SupportedContexts: []string{"interleaved_gated", "three_phase_group_by"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{"non_gated_unbounded_fails_loudly"}}},
		{Name: "sem_redact", Kind: "value", ReturnType: "string", Availability: capabilityAvailability{Status: "limited", SupportedContexts: []string{"interleaved_gated"}, UnsupportedContextBehavior: "fails_loudly", Limitations: []string{"non_gated_unbounded_fails_loudly"}}},
	}
	if !reflect.DeepEqual(got.SemanticFunctions, wantFunctions) {
		t.Fatalf("semantic_functions = %#v, want %#v", got.SemanticFunctions, wantFunctions)
	}
	wantBackends := []string{"anthropic", "local", "mock", "ollama", "openai", "openrouter"}
	if names := capabilityBackendNames(got.Backends); !reflect.DeepEqual(names, wantBackends) {
		t.Fatalf("backend names = %v, want %v", names, wantBackends)
	}
	for _, registration := range backendRegistry {
		entry := findCapabilityBackend(t, got.Backends, registration.Name)
		if entry.Description != registration.HelpDescriptor || entry.NeedsModel != registration.NeedsModel || entry.NeedsBaseURL != registration.NeedsBaseURL || entry.Paid != registration.Paid || entry.DefaultMaxCalls != registration.DefaultMaxCalls || entry.APIKeyEnv != registration.APIKeyEnv || entry.DefaultModel != registration.DefaultModel || entry.DefaultBaseURL != registration.DefaultBaseURL || entry.DefaultMaxOutputTokens != registration.DefaultMaxOutputTokens {
			t.Fatalf("backend %q is not its static registry projection: %#v", registration.Name, entry)
		}
	}
	var rawBackends []map[string]any
	if err := json.Unmarshal(raw["backends"], &rawBackends); err != nil {
		t.Fatalf("decode backend fields: %v", err)
	}
	mock := rawBackends[2]
	for _, optional := range []string{"api_key_env", "default_base_url", "default_max_output_tokens"} {
		if _, ok := mock[optional]; ok {
			t.Fatalf("mock backend emitted empty optional field %q: %#v", optional, mock)
		}
	}
	if !reflect.DeepEqual(got.Cost, capabilityCost{PaidDefaultMaxCalls: 100, UnlimitedMaxCalls: 0, OverrideSources: []string{"flag", "environment", "config"}}) {
		t.Fatalf("cost = %#v", got.Cost)
	}
	if !reflect.DeepEqual(got.Cache, capabilityCache{EnabledByDefault: true, IdentityComponents: []string{"backend", "model", "spec", "canonical_value"}}) {
		t.Fatalf("cache = %#v", got.Cache)
	}
	if !reflect.DeepEqual(got.Provisioning, capabilityProvisioning{LocalBackend: "managed_optional", Command: "ajq provision"}) {
		t.Fatalf("provisioning = %#v", got.Provisioning)
	}
	if !reflect.DeepEqual(got.Safety, capabilitySafety{PureJQ: capabilityPureJQSafety{ConstructsBackend: false, StartsDaemon: false, MakesNetworkCall: false}, SemanticExecution: capabilitySemanticSafety{RequiresExplicitOperator: true, RequiresSelectedBackend: true}, MockBackend: capabilityMockBackendSafe{Deterministic: true, InProcess: true, MakesNetworkCall: false}}) {
		t.Fatalf("safety = %#v", got.Safety)
	}
	if !reflect.DeepEqual(got.Discovery, capabilityDiscovery{ExamplesCommand: "ajq examples"}) {
		t.Fatalf("discovery = %#v", got.Discovery)
	}
}

func TestCapabilitiesHumanHelpArgumentsAndWriterFailure(t *testing.T) {
	t.Run("human summary", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Execute(context.Background(), Options{Stdout: &stdout, Stderr: &stderr}, []string{"capabilities"})
		if err != nil || stderr.Len() != 0 {
			t.Fatalf("capabilities = (%v, %q), want success with empty stderr", err, stderr.String())
		}
		for _, want := range []string{"informational", "--json", "semantic functions: 6", "examples: ajq examples"} {
			if !strings.Contains(stdout.String(), want) {
				t.Fatalf("human capabilities output missing %q: %q", want, stdout.String())
			}
		}
	})
	t.Run("help", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		err := Execute(context.Background(), Options{Stdout: &stdout, Stderr: &stderr}, []string{"capabilities", "--help"})
		if err != nil || stderr.Len() != 0 || !strings.Contains(stdout.String(), "--json") {
			t.Fatalf("capabilities --help = (%v, %q, %q)", err, stdout.String(), stderr.String())
		}
	})
	t.Run("arguments rejected", func(t *testing.T) {
		var stderr bytes.Buffer
		err := Execute(context.Background(), Options{Stderr: &stderr}, []string{"capabilities", "unexpected"})
		if err == nil || !strings.Contains(stderr.String(), "unknown command \"unexpected\"") {
			t.Fatalf("capabilities unexpected = (%v, %q)", err, stderr.String())
		}
	})
	t.Run("writer failure", func(t *testing.T) {
		writeErr := errors.New("capabilities writer failed")
		out := failingCapabilitiesWriter{err: writeErr}
		var stderr bytes.Buffer
		err := Execute(context.Background(), Options{Stdout: &out, Stderr: &stderr}, []string{"capabilities", "--json"})
		if err == nil || !errors.Is(err, writeErr) || !strings.Contains(err.Error(), "write capabilities") || !strings.Contains(stderr.String(), "capabilities writer failed") {
			t.Fatalf("writer failure = (%v, %q)", err, stderr.String())
		}
	})
}

func TestCapabilitiesDoesNotReadSettingsOrInitializeDependencies(t *testing.T) {
	configPath := t.TempDir() + "/invalid.toml"
	const sentinel = "capabilities-secret-sentinel"
	if err := os.WriteFile(configPath, []byte("api_key = \""+sentinel+"\"\nbackend = ["), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_MAX_CALLS", "not-an-integer")
	t.Setenv("ANTHROPIC_API_KEY", sentinel)

	oldConstruct := constructAnthropicBackend
	constructAnthropicBackend = func(config.Settings) (backend.Backend, error) {
		t.Fatal("capabilities must not construct an Anthropic backend")
		return nil, nil
	}
	t.Cleanup(func() { constructAnthropicBackend = oldConstruct })

	var stdout, stderr bytes.Buffer
	options := Options{Stdout: &stdout, Stderr: &stderr, Daemon: failingCapabilitiesDaemon{}, Provision: failingCapabilitiesProvision{}}
	if err := Execute(context.Background(), options, []string{"capabilities", "--json"}); err != nil {
		t.Fatalf("capabilities with poisoned settings returned error: %v", err)
	}
	if stderr.Len() != 0 || strings.Contains(stdout.String(), sentinel) {
		t.Fatalf("capabilities exposed settings: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	clean, err := json.Marshal(newCapabilitiesDocument())
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(stdout.String()) != string(clean) {
		t.Fatalf("poisoned settings changed JSON\n got: %s\nwant: %s", stdout.String(), clean)
	}

	// These advertised pure-jq safety values are backed by the existing no-backend
	// execution path: even with a constructor that fails the pure query succeeds.
	var pureOut, pureErr bytes.Buffer
	if err := Execute(context.Background(), Options{Stdout: &pureOut, Stderr: &pureErr, Daemon: failingCapabilitiesDaemon{}, Provision: failingCapabilitiesProvision{}}, []string{"."}); err != nil {
		t.Fatalf("pure jq unexpectedly initialized a backend: %v", err)
	}
	if pureErr.Len() != 0 {
		t.Fatalf("pure jq wrote stderr: %q", pureErr.String())
	}
}

func mapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	return keys
}

func rawOutputFlag(raw json.RawMessage, index int) (string, bool) {
	var modes []map[string]any
	if err := json.Unmarshal(raw, &modes); err != nil || len(modes) <= index {
		return "", false
	}
	value, ok := modes[index]["flag"].(string)
	return value, ok
}

func capabilityBackendNames(backends []capabilityBackend) []string {
	names := make([]string, len(backends))
	for i, backend := range backends {
		names[i] = backend.Name
	}
	return names
}

func findCapabilityBackend(t *testing.T, backends []capabilityBackend, name string) capabilityBackend {
	t.Helper()
	for _, backend := range backends {
		if backend.Name == name {
			return backend
		}
	}
	t.Fatalf("missing backend %q", name)
	return capabilityBackend{}
}

type failingCapabilitiesWriter struct{ err error }

func (w *failingCapabilitiesWriter) Write([]byte) (int, error) { return 0, w.err }

type failingCapabilitiesDaemon struct{}

func (failingCapabilitiesDaemon) Status(context.Context) daemon.Snapshot {
	panic("capabilities must not inspect daemon status")
}

func (failingCapabilitiesDaemon) Stop(context.Context) (bool, error) {
	panic("capabilities must not stop a daemon")
}

type failingCapabilitiesProvision struct{}

func (failingCapabilitiesProvision) Plan() (provision.Plan, error) {
	panic("capabilities must not inspect provisioning")
}

func (failingCapabilitiesProvision) PlanModel(string) (provision.Plan, error) {
	panic("capabilities must not inspect provisioning")
}

func (failingCapabilitiesProvision) PlanModelOnly(string) (provision.Plan, error) {
	panic("capabilities must not inspect provisioning")
}

func (failingCapabilitiesProvision) Install(context.Context, provision.Plan, provision.ProgressFunc) error {
	panic("capabilities must not provision")
}

func (failingCapabilitiesProvision) InstallModel(context.Context, provision.Plan, provision.ProgressFunc) error {
	panic("capabilities must not provision")
}
