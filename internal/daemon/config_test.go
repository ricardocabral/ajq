package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/provision"
)

func TestDefaultConfigDefaults(t *testing.T) {
	t.Setenv(EnvCacheDir, "/tmp/ajq-cache-test")
	cfg := DefaultConfig()
	if cfg.Host != DefaultHost {
		t.Fatalf("host = %q, want %q", cfg.Host, DefaultHost)
	}
	if cfg.Port != DefaultPort {
		t.Fatalf("port = %d, want %d", cfg.Port, DefaultPort)
	}
	if cfg.IdleTimeout != DefaultIdleTimeout {
		t.Fatalf("idle timeout = %v, want %v", cfg.IdleTimeout, DefaultIdleTimeout)
	}
	if cfg.ParallelSlots != DefaultParallelSlots {
		t.Fatalf("parallel slots = %d, want %d", cfg.ParallelSlots, DefaultParallelSlots)
	}
	if cfg.CacheDir != "/tmp/ajq-cache-test" {
		t.Fatalf("cache dir = %q, want env override", cfg.CacheDir)
	}
}

func TestConfigValidateRejectsNonLoopback(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Host = "0.0.0.0"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected non-loopback host to be rejected")
	}
	if !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("error should mention loopback, got %v", err)
	}

	for _, host := range []string{"127.0.0.1", "localhost", "::1", ""} {
		cfg.Host = host
		if err := cfg.Validate(); err != nil {
			t.Fatalf("loopback host %q rejected: %v", host, err)
		}
	}
}

func TestConfigValidatePortRange(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Port = 70000
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected out-of-range port to be rejected")
	}
	cfg.Port = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative port to be rejected")
	}
}

func TestConfigValidateParallelSlots(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ParallelSlots = -1
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected negative parallel slots to be rejected")
	}
	cfg.ParallelSlots = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("zero parallel slots should mean default: %v", err)
	}
}

func TestConfigAddressAndBaseURL(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		address string
		baseURL string
	}{
		{
			name:    "defaults stay IPv4",
			cfg:     Config{},
			address: "127.0.0.1:8081",
			baseURL: "http://127.0.0.1:8081",
		},
		{
			name:    "hostname stays unbracketed",
			cfg:     Config{Host: "localhost", Port: 9000},
			address: "localhost:9000",
			baseURL: "http://localhost:9000",
		},
		{
			name:    "IPv6 loopback is bracketed",
			cfg:     Config{Host: "::1", Port: 9000},
			address: "[::1]:9000",
			baseURL: "http://[::1]:9000",
		},
		{
			name:    "pre-bracketed IPv6 loopback is normalized",
			cfg:     Config{Host: "[::1]", Port: 9000},
			address: "[::1]:9000",
			baseURL: "http://[::1]:9000",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.Address(); got != tt.address {
				t.Fatalf("Address() = %q, want %q", got, tt.address)
			}
			if got := tt.cfg.BaseURL(); got != tt.baseURL {
				t.Fatalf("BaseURL() = %q, want %q", got, tt.baseURL)
			}
		})
	}
}

func TestDiscoverPrecedenceConfigOverride(t *testing.T) {
	existing := map[string]bool{"/opt/custom/llama-server": true}
	d := Discoverer{
		LookPath:   func(string) (string, error) { return "/usr/bin/llama-server", nil },
		FileExists: func(p string) bool { return existing[p] },
	}
	cfg := DefaultConfig()
	cfg.ServerBinaryPath = "/opt/custom/llama-server"
	got, err := d.DiscoverServerBinary(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/opt/custom/llama-server" {
		t.Fatalf("got %q, want config override to win", got)
	}
}

func TestDiscoverPrecedenceEnvOverConfigMissing(t *testing.T) {
	t.Setenv(EnvServerBinary, "/env/llama-server")
	existing := map[string]bool{"/env/llama-server": true}
	d := Discoverer{
		LookPath:   func(string) (string, error) { return "/usr/bin/llama-server", nil },
		FileExists: func(p string) bool { return existing[p] },
	}
	cfg := DefaultConfig()
	// Config override set but does not exist -> falls through to env.
	cfg.ServerBinaryPath = "/does/not/exist"
	got, err := d.DiscoverServerBinary(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/env/llama-server" {
		t.Fatalf("got %q, want env override", got)
	}
}

func TestDiscoverPrecedenceBundleBeforePATH(t *testing.T) {
	t.Setenv(EnvServerBinary, "")
	cfg := DefaultConfig()
	cfg.CacheDir = t.TempDir()
	asset, err := provision.DefaultCatalog().EngineFor(provision.CurrentPlatform())
	if err != nil || !asset.BundleDownloadable() {
		t.Skipf("current platform has no pinned bundle asset: %v %+v", err, asset)
	}
	layout := provision.NewLayout(cfg.CacheDir)
	binary := layout.EngineBundleBinaryPath(asset.ReleaseTag, asset.BinaryPath)
	if err := os.MkdirAll(filepath.Dir(binary), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("engine"), 0o755); err != nil { //nolint:gosec // test creates an executable fixture to simulate a provisioned engine binary.
		t.Fatal(err)
	}
	md := provision.NewMetadata()
	md.SetEngine(provision.InstalledAsset{Name: provision.EngineBinaryName, Version: asset.ReleaseTag, Path: binary, SHA256: asset.SHA256, Size: asset.Size, InstalledAt: time.Now(), Bundle: &provision.InstalledBundle{Tag: asset.ReleaseTag, Root: layout.EngineBundleDir(asset.ReleaseTag), BinaryPath: binary, BinaryRelPath: asset.BinaryPath, ArchiveSHA256: asset.SHA256, ArchiveSize: asset.Size}})
	if err := md.Save(layout.MetadataPath()); err != nil {
		t.Fatal(err)
	}
	d := Discoverer{
		LookPath:   func(name string) (string, error) { return "/usr/local/bin/" + name, nil },
		FileExists: func(string) bool { return false },
	}
	got, err := d.DiscoverServerBinary(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != binary {
		t.Fatalf("got %q, want bundle binary %q", got, binary)
	}
}

func TestDiscoverPrecedencePATH(t *testing.T) {
	t.Setenv(EnvServerBinary, "")
	d := Discoverer{
		LookPath:   func(name string) (string, error) { return "/usr/local/bin/" + name, nil },
		FileExists: func(string) bool { return false },
	}
	cfg := DefaultConfig()
	got, err := d.DiscoverServerBinary(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "/usr/local/bin/llama-server" {
		t.Fatalf("got %q, want PATH resolution", got)
	}
}

func TestDiscoverPrecedenceCacheLocation(t *testing.T) {
	t.Setenv(EnvServerBinary, "")
	cfg := DefaultConfig()
	cfg.CacheDir = "/cache/ajq"
	cachePath := filepath.Join("/cache/ajq", "bin", ServerBinaryName)
	existing := map[string]bool{cachePath: true}
	d := Discoverer{
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		FileExists: func(p string) bool { return existing[p] },
	}
	got, err := d.DiscoverServerBinary(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cachePath {
		t.Fatalf("got %q, want cache location %q", got, cachePath)
	}
}

func TestDiscoverMissingBinaryDiagnostics(t *testing.T) {
	t.Setenv(EnvServerBinary, "/env/missing")
	cfg := DefaultConfig()
	cfg.CacheDir = "/cache/ajq"
	cfg.ServerBinaryPath = "/cfg/missing"
	d := Discoverer{
		LookPath:   func(string) (string, error) { return "", errors.New("not found") },
		FileExists: func(string) bool { return false },
	}
	_, err := d.DiscoverServerBinary(cfg)
	if err == nil {
		t.Fatal("expected missing-binary error")
	}
	if !IsServerBinaryNotFound(err) {
		t.Fatalf("expected ErrServerBinaryNotFound, got %T: %v", err, err)
	}
	msg := err.Error()
	for _, want := range []string{"/cfg/missing", EnvServerBinary, "PATH", "cache location"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("diagnostics %q missing %q", msg, want)
		}
	}
}
