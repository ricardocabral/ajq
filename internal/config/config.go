// Package config loads ajq's optional TOML configuration file and resolves
// settings precedence across command-line flags, environment variables, config
// file values, and built-in defaults.
package config

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
)

// DefaultWindowBytes is the byte budget used for supported three-phase semantic windows.
const DefaultWindowBytes int64 = 262144

// Settings are the resolved user-facing knobs shared by CLI and backend setup.
type Settings struct {
	Backend            string
	Model              string
	BaseURL            string
	BaseURLExplicit    bool
	MaxCalls           int
	BackendConcurrency int
	WindowBytes        int64
	NoCache            bool
}

// Values is a presence-aware settings patch. Zero values are meaningful only
// when the corresponding Has* field is true.
type Values struct {
	Backend               string
	BackendSet            bool
	Model                 string
	ModelSet              bool
	BaseURL               string
	BaseURLSet            bool
	MaxCalls              int
	MaxCallsSet           bool
	BackendConcurrency    int
	BackendConcurrencySet bool
	WindowBytes           int64
	WindowBytesSet        bool
	NoCache               bool
	NoCacheSet            bool
}

// Resolve applies the single supported precedence chain:
// flags > env > file > defaults.
func Resolve(flags, env, file, defaults Values) Settings {
	merged := mergeValues(defaults, file, env, flags)
	return Settings{
		Backend:            merged.Backend,
		Model:              merged.Model,
		BaseURL:            merged.BaseURL,
		BaseURLExplicit:    flags.BaseURLSet || env.BaseURLSet || file.BaseURLSet,
		MaxCalls:           merged.MaxCalls,
		BackendConcurrency: merged.BackendConcurrency,
		WindowBytes:        resolvedWindowBytes(merged),
		NoCache:            merged.NoCache,
	}
}

func resolvedWindowBytes(values Values) int64 {
	if values.WindowBytesSet {
		return values.WindowBytes
	}
	return DefaultWindowBytes
}

func mergeValues(sources ...Values) Values {
	var out Values
	for _, source := range sources {
		if source.BackendSet {
			out.Backend = source.Backend
			out.BackendSet = true
		}
		if source.ModelSet {
			out.Model = source.Model
			out.ModelSet = true
		}
		if source.BaseURLSet {
			out.BaseURL = source.BaseURL
			out.BaseURLSet = true
		}
		if source.MaxCallsSet {
			out.MaxCalls = source.MaxCalls
			out.MaxCallsSet = true
		}
		if source.BackendConcurrencySet {
			out.BackendConcurrency = source.BackendConcurrency
			out.BackendConcurrencySet = true
		}
		if source.WindowBytesSet {
			out.WindowBytes = source.WindowBytes
			out.WindowBytesSet = true
		}
		if source.NoCacheSet {
			out.NoCache = source.NoCache
			out.NoCacheSet = true
		}
	}
	return out
}

// Env reads settings from AJQ_* environment variables. API key environment
// variables are intentionally not read here; provider credentials are consumed
// only by provider backends.
func Env(getenv func(string) string) (Values, error) {
	if getenv == nil {
		getenv = os.Getenv
	}
	var values Values
	if value, ok := lookupEnv(getenv, "AJQ_BACKEND"); ok {
		values.Backend = value
		values.BackendSet = true
	}
	if value, ok := lookupEnv(getenv, "AJQ_MODEL"); ok {
		values.Model = value
		values.ModelSet = true
	}
	if value, ok := lookupEnv(getenv, "AJQ_BASE_URL"); ok {
		values.BaseURL = value
		values.BaseURLSet = true
	}
	if value, ok := lookupEnv(getenv, "AJQ_MAX_CALLS"); ok {
		maxCalls, err := parseNonNegativeInt("AJQ_MAX_CALLS", value)
		if err != nil {
			return Values{}, err
		}
		values.MaxCalls = maxCalls
		values.MaxCallsSet = true
	}
	if value, ok := lookupEnv(getenv, "AJQ_BACKEND_CONCURRENCY"); ok {
		concurrency, err := parsePositiveInt("AJQ_BACKEND_CONCURRENCY", value)
		if err != nil {
			return Values{}, err
		}
		values.BackendConcurrency = concurrency
		values.BackendConcurrencySet = true
	}
	if value, ok := lookupEnv(getenv, "AJQ_WINDOW_BYTES"); ok {
		windowBytes, err := parsePositiveInt64("AJQ_WINDOW_BYTES", value)
		if err != nil {
			return Values{}, err
		}
		values.WindowBytes = windowBytes
		values.WindowBytesSet = true
	}
	return values, nil
}

func lookupEnv(getenv func(string) string, key string) (string, bool) {
	value := getenv(key)
	return value, value != ""
}

func parsePositiveInt64(name string, value string) (int64, error) {
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return parsed, nil
}

func parsePositiveInt(name string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	if parsed <= 0 {
		return 0, fmt.Errorf("%s must be positive", name)
	}
	return parsed, nil
}

func parseNonNegativeInt(name string, value string) (int, error) {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0, fmt.Errorf("%s must be a non-negative integer", name)
	}
	if parsed < 0 {
		return 0, fmt.Errorf("%s must be non-negative", name)
	}
	return parsed, nil
}

// Load reads ajq's optional TOML configuration file using process environment
// and filesystem defaults. Missing default config is not an error.
func Load() (Values, error) {
	return LoadWithOptions(LoadOptions{})
}

// LoadOptions makes config loading testable without mutating process globals.
type LoadOptions struct {
	Getenv   func(string) string
	ReadFile func(string) ([]byte, error)
	HomeDir  func() (string, error)
	Stderr   io.Writer
}

// LoadWithOptions reads AJQ_CONFIG, or the default
// ${XDG_CONFIG_HOME:-~/.config}/ajq/config.toml path, and returns only values
// explicitly present in the file.
func LoadWithOptions(opts LoadOptions) (Values, error) {
	getenv := opts.Getenv
	if getenv == nil {
		getenv = os.Getenv
	}
	readFile := opts.ReadFile
	if readFile == nil {
		readFile = os.ReadFile
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	path, explicit, err := configPath(getenv, opts.HomeDir)
	if err != nil {
		return Values{}, err
	}
	data, err := readFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) && !explicit {
			return Values{}, nil
		}
		return Values{}, fmt.Errorf("read config %s: %w", path, err)
	}
	return parse(data, stderr)
}

func configPath(getenv func(string) string, homeDir func() (string, error)) (path string, explicit bool, err error) {
	if value := strings.TrimSpace(getenv("AJQ_CONFIG")); value != "" {
		return value, true, nil
	}
	if value := strings.TrimSpace(getenv("XDG_CONFIG_HOME")); value != "" {
		return filepath.Join(value, "ajq", "config.toml"), false, nil
	}
	if homeDir == nil {
		homeDir = os.UserHomeDir
	}
	home, err := homeDir()
	if err != nil {
		return "", false, fmt.Errorf("resolve home directory for config: %w", err)
	}
	if strings.TrimSpace(home) == "" {
		return "", false, fmt.Errorf("resolve home directory for config: empty home directory")
	}
	return filepath.Join(home, ".config", "ajq", "config.toml"), false, nil
}

type fileSettings struct {
	Backend            *string `toml:"backend"`
	Model              *string `toml:"model"`
	BaseURL            *string `toml:"base_url"`
	MaxCalls           *int    `toml:"max_calls"`
	BackendConcurrency *int    `toml:"backend_concurrency"`
	WindowBytes        *int64  `toml:"window_bytes"`
	NoCache            *bool   `toml:"no_cache"`
}

func parse(data []byte, stderr io.Writer) (Values, error) {
	var raw map[string]any
	if _, err := toml.Decode(string(data), &raw); err != nil {
		return Values{}, fmt.Errorf("parse config: %w", err)
	}
	if err := rejectCredentialKeyPaths(nil, raw); err != nil {
		return Values{}, err
	}

	var file fileSettings
	metadata, err := toml.Decode(string(data), &file)
	if err != nil {
		return Values{}, fmt.Errorf("parse config: %w", err)
	}
	undecoded := metadata.Undecoded()
	if err := rejectCredentialKeys(undecoded); err != nil {
		return Values{}, err
	}
	warnUnknownKeys(stderr, undecoded)

	var values Values
	if file.Backend != nil {
		values.Backend = *file.Backend
		values.BackendSet = true
	}
	if file.Model != nil {
		values.Model = *file.Model
		values.ModelSet = true
	}
	if file.BaseURL != nil {
		values.BaseURL = *file.BaseURL
		values.BaseURLSet = true
	}
	if file.MaxCalls != nil {
		if *file.MaxCalls < 0 {
			return Values{}, fmt.Errorf("config max_calls must be non-negative")
		}
		values.MaxCalls = *file.MaxCalls
		values.MaxCallsSet = true
	}
	if file.BackendConcurrency != nil {
		if *file.BackendConcurrency <= 0 {
			return Values{}, fmt.Errorf("config backend_concurrency must be positive")
		}
		values.BackendConcurrency = *file.BackendConcurrency
		values.BackendConcurrencySet = true
	}
	if file.WindowBytes != nil {
		if *file.WindowBytes <= 0 {
			return Values{}, fmt.Errorf("config window_bytes must be positive")
		}
		values.WindowBytes = *file.WindowBytes
		values.WindowBytesSet = true
	}
	if file.NoCache != nil {
		values.NoCache = *file.NoCache
		values.NoCacheSet = true
	}
	return values, nil
}

func rejectCredentialKeys(keys []toml.Key) error {
	for _, key := range keys {
		if len(key) == 0 {
			continue
		}
		if isCredentialKey(key[len(key)-1]) {
			return credentialKeyError(key.String())
		}
	}
	return nil
}

func rejectCredentialKeyPaths(prefix []string, table map[string]any) error {
	for key, value := range table {
		path := append(append([]string(nil), prefix...), key)
		if isCredentialKey(key) {
			return credentialKeyError(strings.Join(path, "."))
		}
		if err := rejectCredentialValue(path, value); err != nil {
			return err
		}
	}
	return nil
}

func rejectCredentialValue(path []string, value any) error {
	switch v := value.(type) {
	case map[string]any:
		return rejectCredentialKeyPaths(path, v)
	case []map[string]any:
		for _, item := range v {
			if err := rejectCredentialKeyPaths(path, item); err != nil {
				return err
			}
		}
	case []any:
		for _, item := range v {
			if err := rejectCredentialValue(path, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func isCredentialKey(key string) bool {
	leaf := strings.ToLower(key)
	sanitized := strings.ReplaceAll(leaf, "-", "_")
	return sanitized == "api_key" || sanitized == "apikey" || sanitized == "token"
}

func credentialKeyError(key string) error {
	return fmt.Errorf("config key %q looks like a credential; API keys are env-only (use ANTHROPIC_API_KEY, OPENAI_API_KEY, or OPENROUTER_API_KEY)", key)
}

func warnUnknownKeys(stderr io.Writer, keys []toml.Key) {
	if stderr == nil {
		return
	}
	for _, key := range keys {
		_, _ = fmt.Fprintf(stderr, "ajq: warning: unknown config key %q ignored\n", key.String())
	}
}
