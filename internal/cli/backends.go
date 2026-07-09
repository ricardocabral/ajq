package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/backend/anthropicbk"
	localbackend "github.com/ricardocabral/ajq/internal/backend/local"
	"github.com/ricardocabral/ajq/internal/backend/oai"
	"github.com/ricardocabral/ajq/internal/backend/ollamabk"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/daemon"
	"github.com/ricardocabral/ajq/internal/provision"
)

type backendConstructor func(Options, config.Settings) (backend.Backend, *semanticcache.Store, error)

type backendRegistration struct {
	Name                   string
	HelpDescriptor         string
	NeedsModel             bool
	NeedsBaseURL           bool
	APIKeyEnv              string
	DefaultBaseURL         string
	DefaultModel           string
	Paid                   bool
	DefaultMaxCalls        int
	DefaultMaxOutputTokens int
	ModelIdentity          func(config.Settings) (string, error)
	Construct              backendConstructor
}

func (r backendRegistration) defaults() config.Values {
	values := config.Values{}
	if strings.TrimSpace(r.DefaultModel) != "" {
		values.Model = r.DefaultModel
		values.ModelSet = true
	}
	if strings.TrimSpace(r.DefaultBaseURL) != "" {
		values.BaseURL = r.DefaultBaseURL
		values.BaseURLSet = true
	}
	values.MaxCalls = r.DefaultMaxCalls
	values.MaxCallsSet = true
	return values
}

var constructAnthropicBackend = func(settings config.Settings) (backend.Backend, error) {
	return anthropicbk.New(settings.Model)
}

var backendRegistry = []backendRegistration{
	{
		Name:           "mock",
		HelpDescriptor: "deterministic",
		DefaultModel:   semanticcache.DefaultModelID,
		Construct: func(_ Options, _ config.Settings) (backend.Backend, *semanticcache.Store, error) {
			return &backend.MockBackend{}, nil, nil
		},
	},
	{
		Name:           "local",
		HelpDescriptor: "managed llama-server",
		NeedsModel:     true,
		NeedsBaseURL:   true,
		DefaultModel:   provision.DefaultModelName,
		DefaultBaseURL: daemon.DefaultConfig().BaseURL(),
		ModelIdentity: func(settings config.Settings) (string, error) {
			resolved, err := resolveLocalModelRequest(settings.Model)
			if err != nil {
				return "", err
			}
			return resolved.ModelID, nil
		},
		Construct: func(opts Options, settings config.Settings) (backend.Backend, *semanticcache.Store, error) {
			if opts.LocalBackend != nil {
				return opts.LocalBackend, nil, nil
			}
			be, err := newLocalBackend(opts, settings)
			if err != nil {
				return nil, nil, err
			}
			return be, nil, nil
		},
	},
	{
		Name:           "ollama",
		HelpDescriptor: "local Ollama",
		NeedsModel:     true,
		NeedsBaseURL:   true,
		ModelIdentity: func(settings config.Settings) (string, error) {
			return "ollama/" + strings.TrimSpace(settings.Model), nil
		},
		Construct: func(_ Options, settings config.Settings) (backend.Backend, *semanticcache.Store, error) {
			be, err := newOllamaBackend(settings)
			if err != nil {
				return nil, nil, err
			}
			return be, semanticcache.NewStore(), nil
		},
	},
	{
		Name:                   "anthropic",
		HelpDescriptor:         "cloud Claude",
		APIKeyEnv:              anthropicbk.APIKeyEnv,
		DefaultModel:           anthropicbk.DefaultModel,
		Paid:                   true,
		DefaultMaxCalls:        100,
		DefaultMaxOutputTokens: int(anthropicbk.DefaultMaxTokens),
		ModelIdentity: func(settings config.Settings) (string, error) {
			return anthropicbk.ModelIdentity(settings.Model)
		},
		Construct: func(_ Options, settings config.Settings) (backend.Backend, *semanticcache.Store, error) {
			be, err := constructAnthropicBackend(settings)
			if err != nil {
				return nil, nil, err
			}
			return be, semanticcache.NewStore(), nil
		},
	},
	openAICompatibleRegistration("openai", "https://api.openai.com/v1", "OPENAI_API_KEY"),
	openAICompatibleRegistration("openrouter", "https://openrouter.ai/api/v1", "OPENROUTER_API_KEY"),
}

func openAICompatibleRegistration(name, defaultBaseURL, apiKeyEnv string) backendRegistration {
	return backendRegistration{
		Name:                   name,
		HelpDescriptor:         name,
		NeedsModel:             true,
		NeedsBaseURL:           true,
		APIKeyEnv:              apiKeyEnv,
		DefaultBaseURL:         defaultBaseURL,
		Paid:                   true,
		DefaultMaxCalls:        100,
		DefaultMaxOutputTokens: oai.DefaultMaxTokens,
		ModelIdentity: func(settings config.Settings) (string, error) {
			return name + "/" + strings.TrimSpace(settings.Model), nil
		},
		Construct: func(_ Options, settings config.Settings) (backend.Backend, *semanticcache.Store, error) {
			be, err := newOpenAICompatibleBackend(name, defaultBaseURL, apiKeyEnv, settings)
			if err != nil {
				return nil, nil, err
			}
			return be, semanticcache.NewStore(), nil
		},
	}
}

func semanticStoreForSettings(settings config.Settings) *semanticcache.Store {
	if settings.NoCache {
		return semanticcache.NewStore()
	}
	return semanticcache.NewDefaultPersistentStore()
}

func lookupBackend(name string) (backendRegistration, bool) {
	for _, registration := range backendRegistry {
		if registration.Name == name {
			return registration, true
		}
	}
	return backendRegistration{}, false
}

func validBackendNames() []string {
	names := make([]string, 0, len(backendRegistry))
	for _, registration := range backendRegistry {
		names = append(names, registration.Name)
	}
	sort.Strings(names)
	return names
}

func backendFlagHelp() string {
	entries := make([]string, 0, len(backendRegistry))
	for _, registration := range backendRegistry {
		entry := fmt.Sprintf("%q", registration.Name)
		if descriptor := strings.TrimSpace(registration.HelpDescriptor); descriptor != "" {
			entry += fmt.Sprintf(" (%s)", descriptor)
		}
		entries = append(entries, entry)
	}
	sort.Strings(entries)
	return "semantic backend for semantic queries: " + joinWithOr(entries)
}

func joinWithOr(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	case 2:
		return items[0] + " or " + items[1]
	default:
		return strings.Join(items[:len(items)-1], ", ") + ", or " + items[len(items)-1]
	}
}

func unknownBackendError(name string) error {
	quoted := make([]string, 0, len(backendRegistry))
	for _, valid := range validBackendNames() {
		quoted = append(quoted, fmt.Sprintf("%q", valid))
	}
	return fmt.Errorf("unknown backend %q: valid backends are %s", name, strings.Join(quoted, ", "))
}

func newOpenAICompatibleBackend(name, defaultBaseURL, apiKeyEnv string, settings config.Settings) (backend.Backend, error) {
	model := strings.TrimSpace(settings.Model)
	if model == "" {
		return nil, fmt.Errorf("%s backend requires a model; pass --model", name)
	}
	apiKey := strings.TrimSpace(os.Getenv(apiKeyEnv))
	if apiKey == "" {
		return nil, fmt.Errorf("%s backend API key is empty; set %s", name, apiKeyEnv)
	}
	baseURL := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	if err := validateOpenAICompatibleBaseURL(name, baseURL); err != nil {
		return nil, err
	}
	return &oai.Backend{
		BaseURL:   baseURL,
		APIKey:    apiKey,
		APIKeyEnv: apiKeyEnv,
		Model:     model,
	}, nil
}

func validateOpenAICompatibleBaseURL(name, baseURL string) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("%s backend base URL %q is invalid: provide an absolute HTTPS URL; http:// is allowed only for loopback hosts (127.0.0.1, localhost, [::1])", name, baseURL)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "https" {
		return nil
	}
	if scheme == "http" && isOpenAICompatibleLoopbackHost(u.Hostname()) {
		return nil
	}
	return fmt.Errorf("%s backend base URL %q is rejected: API keys must be sent over HTTPS; http:// is allowed only for loopback hosts (127.0.0.1, localhost, [::1])", name, baseURL)
}

func isOpenAICompatibleLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "127.0.0.1", "localhost", "::1":
		return true
	default:
		return false
	}
}

func newOllamaBackend(settings config.Settings) (backend.Backend, error) {
	model := strings.TrimSpace(settings.Model)
	if model == "" {
		return nil, fmt.Errorf("ollama backend requires a model; pass --model llama3.2 and check installed models with `ollama list`")
	}
	baseURL, err := ollamabk.ResolveBaseURL(settings.BaseURL)
	if err != nil {
		return nil, err
	}
	return &ollamabk.Backend{BaseURL: baseURL, Model: model}, nil
}

type localModelResolution struct {
	Name     string
	Path     string
	PathLike bool
	ModelID  string
}

func resolveLocalModelRequest(raw string) (localModelResolution, error) {
	model := strings.TrimSpace(raw)
	if model == "" || model == semanticcache.DefaultModelID {
		model = provision.DefaultModelName
	}
	if isPathLikeModel(model) {
		abs, err := filepath.Abs(model)
		if err != nil {
			return localModelResolution{}, fmt.Errorf("local model path %q is invalid: %w", model, err)
		}
		clean := filepath.Clean(abs)
		sum := sha256.Sum256([]byte(clean))
		return localModelResolution{Path: clean, PathLike: true, ModelID: "local/path:" + hex.EncodeToString(sum[:])}, nil
	}
	if _, err := provision.DefaultCatalog().ModelFor(model); err != nil {
		return localModelResolution{}, err
	}
	return localModelResolution{Name: model, ModelID: "local/" + model}, nil
}

func isPathLikeModel(model string) bool {
	return filepath.IsAbs(model) || strings.ContainsAny(model, `/\\`) || strings.HasSuffix(strings.ToLower(model), ".gguf")
}

// newLocalBackend builds the production localhost backend wired to a
// daemon.Manager. The warm hook is lazy (invoked only when a query has real
// semantic work): on first use it verifies the engine/model are provisioned,
// wires the discovered asset paths onto the daemon config, and spawns the
// daemon. If assets are missing it returns an actionable error directing the
// user to provision the missing assets rather than silently downloading
// gigabytes.
func newLocalBackend(opts Options, settings config.Settings) (backend.Backend, error) {
	resolved, err := resolveLocalModelRequest(settings.Model)
	if err != nil {
		return nil, err
	}
	if settings.BaseURLExplicit {
		baseURL, err := explicitLocalBaseURL(settings.BaseURL)
		if err != nil {
			return nil, err
		}
		return &localbackend.Backend{
			BaseURL:        baseURL,
			ModelID:        resolved.ModelID,
			MaxConcurrency: daemon.DefaultParallelSlots,
		}, nil
	}
	daemonConfig, baseURL, err := localDaemonConfig(settings)
	if err != nil {
		return nil, err
	}
	mgr := daemon.NewManager(daemonConfig)
	provisioner := resolveProvisionController(opts)
	be := &localbackend.Backend{
		BaseURL:        baseURL,
		ModelID:        resolved.ModelID,
		MaxConcurrency: daemonConfig.ParallelSlots,
	}
	warm := func(ctx context.Context) error {
		controller := provisioner
		modelName := resolved.Name
		if resolved.PathLike {
			if production, ok := provisioner.(*provision.Provisioner); ok {
				clone := *production
				clone.ModelOverride = resolved.Path
				controller = &clone
			}
			modelName = ""
		}
		plan, err := controller.PlanModel(modelName)
		if err != nil {
			return fmt.Errorf("local backend provisioning check failed: %w", err)
		}
		if plan.NeedsProvisioning() {
			return provisioningRequiredError(plan)
		}
		// Point the daemon at the discovered engine and model so it spawns with
		// the exact assets the provisioner verified.
		mgr.Config.ServerBinaryPath = plan.Engine.Path
		mgr.Config.ModelPath = plan.Model.Path
		if err := mgr.EnsureRunning(ctx); err != nil {
			return err
		}
		be.APIKey = mgr.APIKey()
		return nil
	}
	be.WarmFunc = warm
	// Record activity per judgement so the detached idle-reaper keeps the daemon
	// warm during an active batch and only reaps it once genuinely idle.
	be.TouchFunc = func(context.Context) { _ = mgr.TouchActivity() }
	return be, nil
}

func explicitLocalBaseURL(raw string) (string, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(raw), "/")
	if baseURL == "" {
		return "", fmt.Errorf("local backend explicit base URL is empty")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("local backend base URL %q is invalid: %w", raw, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("local backend base URL %q must use http or https", raw)
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("local backend base URL %q must include a host", raw)
	}
	if parsed.User != nil {
		return "", fmt.Errorf("local backend base URL %q must not include userinfo", raw)
	}
	if parsed.Port() == "0" {
		return "", fmt.Errorf("local backend base URL %q must not use port 0", raw)
	}
	return baseURL, nil
}

func localDaemonConfig(settings config.Settings) (daemon.Config, string, error) {
	cfg := daemon.DefaultConfig()
	baseURL := strings.TrimRight(strings.TrimSpace(settings.BaseURL), "/")
	if baseURL == "" {
		return cfg, cfg.BaseURL(), nil
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q is invalid: %w", settings.BaseURL, err)
	}
	if parsed.Scheme != "http" {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must use http", settings.BaseURL)
	}
	if parsed.Hostname() == "" {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must include a host", settings.BaseURL)
	}
	if parsed.User != nil {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must not include userinfo", settings.BaseURL)
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must not include a path", settings.BaseURL)
	}
	if parsed.RawQuery != "" {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must not include a query string", settings.BaseURL)
	}
	if parsed.Fragment != "" {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must not include a fragment", settings.BaseURL)
	}
	port := 80
	if parsed.Port() != "" {
		parsedPort, err := strconv.Atoi(parsed.Port())
		if err != nil {
			return daemon.Config{}, "", fmt.Errorf("local backend base URL %q has invalid port: %w", settings.BaseURL, err)
		}
		if parsedPort == 0 {
			return daemon.Config{}, "", fmt.Errorf("local backend base URL %q must not use port 0", settings.BaseURL)
		}
		port = parsedPort
	}
	cfg.Host = parsed.Hostname()
	cfg.Port = port
	if err := cfg.Validate(); err != nil {
		return daemon.Config{}, "", fmt.Errorf("local backend base URL %q is not usable: %w", settings.BaseURL, err)
	}
	return cfg, baseURL, nil
}
