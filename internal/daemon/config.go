// Package daemon manages the lifecycle of a local llama-server process used as
// a warm localhost daemon by ajq. It is intentionally decoupled from the actual
// semantic HTTP backend: this package only discovers, spawns, health-checks and
// stops the process. It never downloads or provisions models (owned by TP-020)
// and never implements semantic judgement logic (owned by TP-018).
package daemon

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ricardocabral/ajq/internal/provision"
)

const (
	// DefaultHost is the loopback address the daemon binds to. The daemon must
	// never bind to a non-localhost address.
	DefaultHost = "127.0.0.1"
	// DefaultPort is the default TCP port for the local llama-server daemon.
	DefaultPort = 8081
	// DefaultIdleTimeout is how long the daemon may sit idle before it is
	// eligible to be shut down.
	DefaultIdleTimeout = 5 * time.Minute
	// DefaultParallelSlots is the managed llama-server slot count. llama.cpp's
	// --parallel flag controls how many concurrent completion requests the
	// server may decode.
	DefaultParallelSlots = 4
	// ServerBinaryName is the executable name looked up on PATH and in the
	// cache location.
	ServerBinaryName = "llama-server"
	// EnvServerBinary overrides binary discovery with an explicit path.
	EnvServerBinary = "AJQ_LLAMA_SERVER"
	// EnvCacheDir overrides the cache directory used for provisioned assets.
	EnvCacheDir = "AJQ_CACHE_DIR"
)

// Config describes how the local llama-server daemon should be located, bound
// and supervised. All fields are optional; use DefaultConfig for sane defaults
// and override individual fields as needed.
type Config struct {
	// Host is the loopback address to bind to. Defaults to DefaultHost. A
	// non-loopback host is rejected by Validate.
	Host string
	// Port is the TCP port to bind to. Defaults to DefaultPort.
	Port int
	// IdleTimeout is how long the daemon may sit idle before shutdown. A
	// non-positive value disables idle shutdown.
	IdleTimeout time.Duration
	// ServerBinaryPath is an explicit override for the llama-server binary.
	// When set and present, it takes precedence over env/PATH/cache discovery.
	ServerBinaryPath string
	// ModelPath is a placeholder for the GGUF model path. This package does not
	// provision or download models; provisioning is owned by TP-020.
	ModelPath string
	// ParallelSlots is the number of llama-server slots to spawn for managed
	// daemons. Defaults to DefaultParallelSlots when zero.
	ParallelSlots int
	// CacheDir is the base directory for ajq cached assets (binaries, models).
	// Defaults to the user cache dir joined with "ajq".
	CacheDir string
}

// DefaultConfig returns a Config with localhost defaults. CacheDir is resolved
// from AJQ_CACHE_DIR, else the OS user cache dir joined with "ajq", else a
// "~/.cache/ajq" fallback.
func DefaultConfig() Config {
	return Config{
		Host:          DefaultHost,
		Port:          DefaultPort,
		IdleTimeout:   DefaultIdleTimeout,
		ParallelSlots: DefaultParallelSlots,
		CacheDir:      defaultCacheDir(),
	}
}

// defaultCacheDir resolves the cache directory used for provisioned assets.
func defaultCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvCacheDir)); dir != "" {
		return dir
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "ajq")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "ajq")
	}
	return filepath.Join(".cache", "ajq")
}

// isLoopbackHost reports whether host is a recognized loopback address. Binding
// to a non-loopback address is forbidden for the local daemon.
func isLoopbackHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "", "127.0.0.1", "localhost", "::1", "[::1]":
		return true
	default:
		return false
	}
}

// Validate checks that the configuration is internally consistent and safe. In
// particular it enforces localhost-only binding.
func (c Config) Validate() error {
	if !isLoopbackHost(c.Host) {
		return fmt.Errorf("daemon host %q is not a loopback address: the local daemon must bind to localhost only", c.Host)
	}
	if c.Port < 0 || c.Port > 65535 {
		return fmt.Errorf("daemon port %d is out of range 0-65535", c.Port)
	}
	if c.ParallelSlots < 0 {
		return fmt.Errorf("daemon parallel slots %d is invalid: must be non-negative", c.ParallelSlots)
	}
	return nil
}

// Address returns the host:port the daemon binds to, applying defaults for
// unset fields.
func (c Config) Address() string {
	host := strings.TrimSpace(c.Host)
	if host == "" {
		host = DefaultHost
	}
	if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
		host = strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
	}
	port := c.Port
	if port == 0 {
		port = DefaultPort
	}
	return net.JoinHostPort(host, fmt.Sprintf("%d", port))
}

// BaseURL returns the http base URL for the daemon, applying host/port defaults.
func (c Config) BaseURL() string {
	return "http://" + c.Address()
}

// fileExists reports whether path names an existing, non-directory file.
type fileExistsFunc func(path string) bool

// lookPathFunc resolves an executable name via PATH, mirroring exec.LookPath.
type lookPathFunc func(name string) (string, error)

// defaultFileExists is the production fileExists implementation.
func defaultFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// Discoverer resolves the llama-server binary path. The lookup functions are
// injectable so tests never depend on a real binary being installed.
type Discoverer struct {
	// LookPath resolves an executable on PATH. Defaults to exec.LookPath.
	LookPath lookPathFunc
	// FileExists checks for an existing file. Defaults to a filesystem stat.
	FileExists fileExistsFunc
}

// ErrServerBinaryNotFound is returned when no llama-server binary can be located
// through any discovery mechanism. It carries the candidate locations that were
// tried for actionable diagnostics.
type ErrServerBinaryNotFound struct {
	// Tried lists the human-readable candidate sources that were attempted.
	Tried []string
}

func (e *ErrServerBinaryNotFound) Error() string {
	if len(e.Tried) == 0 {
		return "llama-server binary not found"
	}
	return fmt.Sprintf("llama-server binary not found; looked in: %s", strings.Join(e.Tried, ", "))
}

// CacheBinaryPath returns the expected legacy location of a provisioned
// llama-server binary within the cache directory.
func (c Config) CacheBinaryPath() string {
	cacheDir := c.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultCacheDir()
	}
	return filepath.Join(cacheDir, "bin", ServerBinaryName)
}

// DiscoverServerBinary resolves the llama-server binary path using the following
// precedence:
//
//  1. cfg.ServerBinaryPath, if set and the file exists.
//  2. The AJQ_LLAMA_SERVER environment variable, if set and the file exists.
//  3. A recorded provisioned bundle, if valid.
//  4. The legacy provisioned cache location <CacheDir>/bin/llama-server.
//  5. A PATH lookup for "llama-server".
//
// If none succeed, it returns an *ErrServerBinaryNotFound describing every
// candidate that was tried.
func (d Discoverer) DiscoverServerBinary(cfg Config) (string, error) {
	lookPath := d.LookPath
	fileExists := d.FileExists
	if fileExists == nil {
		fileExists = defaultFileExists
	}

	var tried []string

	// 1. Explicit config override.
	if p := strings.TrimSpace(cfg.ServerBinaryPath); p != "" {
		if fileExists(p) {
			return p, nil
		}
		tried = append(tried, fmt.Sprintf("config override %q", p))
	}

	// 2. Environment override.
	if p := strings.TrimSpace(os.Getenv(EnvServerBinary)); p != "" {
		if fileExists(p) {
			return p, nil
		}
		tried = append(tried, fmt.Sprintf("$%s=%q", EnvServerBinary, p))
	}

	// 3. Recorded provisioned bundle.
	if p, ok := provision.CachedEngineBundleBinaryPath(cfg.CacheDir); ok {
		return p, nil
	}
	tried = append(tried, "recorded engine bundle")

	// 4. Legacy provisioned cache location.
	cachePath := cfg.CacheBinaryPath()
	if fileExists(cachePath) {
		return cachePath, nil
	}
	tried = append(tried, fmt.Sprintf("cache location %q", cachePath))

	// 5. PATH lookup.
	if lookPath != nil {
		if p, err := lookPath(ServerBinaryName); err == nil && p != "" {
			return p, nil
		}
		tried = append(tried, fmt.Sprintf("PATH lookup of %q", ServerBinaryName))
	}

	return "", &ErrServerBinaryNotFound{Tried: tried}
}

// IsServerBinaryNotFound reports whether err is (or wraps) an
// *ErrServerBinaryNotFound.
func IsServerBinaryNotFound(err error) bool {
	var e *ErrServerBinaryNotFound
	return errors.As(err, &e)
}
