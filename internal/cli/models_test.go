package cli

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/provision"
)

func TestModelsListShowsInstalledAndActive(t *testing.T) {
	cacheDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("AJQ_CACHE_DIR", cacheDir)
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_MODEL", "")
	if err := os.WriteFile(configPath, []byte("model = \"qwen2.5-3b\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	layout := provision.NewLayout(cacheDir)
	if err := os.MkdirAll(layout.ModelsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(layout.ModelPath("qwen2.5-3b-instruct-q4_k_m.gguf"), []byte("installed"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLIForModelsTest("", nil, "models", "list")
	if err != nil {
		t.Fatalf("models list: %v stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "NAME") || !strings.Contains(stdout, "qwen2.5-1.5b") || !strings.Contains(stdout, "qwen3-4b") {
		t.Fatalf("list missing catalog rows: %q", stdout)
	}
	if !strings.Contains(stdout, "qwen2.5-3b") || !strings.Contains(stdout, "*       yes") {
		t.Fatalf("list should mark qwen2.5-3b active and installed: %q", stdout)
	}
}

func TestModelsPullDownloadsChecksumVerifiedModel(t *testing.T) {
	body := []byte("tiny model")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	pr := provisionerForModelTest(t, srv.URL, sha256HexForModelsTest(body))

	stdout, stderr, err := runCLIForModelsTest("", pr, "models", "pull", "tiny")
	if err != nil {
		t.Fatalf("models pull: %v stderr=%q", err, stderr)
	}
	if hits != 1 {
		t.Fatalf("download hits = %d, want 1", hits)
	}
	if !strings.Contains(stdout, "model tiny installed:") || !strings.Contains(stderr, "tiny:") {
		t.Fatalf("stdout/stderr missing install/progress: stdout=%q stderr=%q", stdout, stderr)
	}
	if got, err := os.ReadFile(pr.Layout.ModelPath("tiny.gguf")); err != nil || string(got) != string(body) {
		t.Fatalf("installed body = %q err=%v", got, err)
	}
}

func TestModelsPullAlreadyInstalledNoOp(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		http.Error(w, "should not be called", http.StatusTeapot)
	}))
	t.Cleanup(srv.Close)
	pr := provisionerForModelTest(t, srv.URL, strings.Repeat("0", 64))
	if err := os.MkdirAll(pr.Layout.ModelsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pr.Layout.ModelPath("tiny.gguf"), []byte("present"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLIForModelsTest("", pr, "models", "pull", "tiny")
	if err != nil {
		t.Fatalf("models pull no-op: %v stderr=%q", err, stderr)
	}
	if hits != 0 {
		t.Fatalf("installed model should not hit network, got %d", hits)
	}
	if !strings.Contains(stdout, "model tiny already installed:") {
		t.Fatalf("stdout missing no-op message: %q", stdout)
	}
}

func TestModelsPullChecksumMismatchCleansUp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bad bytes"))
	}))
	t.Cleanup(srv.Close)
	pr := provisionerForModelTest(t, srv.URL, sha256HexForModelsTest([]byte("good bytes")))

	_, stderr, err := runCLIForModelsTest("", pr, "models", "pull", "tiny")
	if err == nil {
		t.Fatal("expected checksum mismatch")
	}
	if !strings.Contains(stderr, "checksum mismatch") {
		t.Fatalf("stderr missing checksum mismatch: %q", stderr)
	}
	if _, statErr := os.Stat(pr.Layout.ModelPath("tiny.gguf")); !os.IsNotExist(statErr) {
		t.Fatalf("destination should be absent after mismatch, stat=%v", statErr)
	}
}

func TestModelsUsePersistsInstalledModel(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("AJQ_CONFIG", configPath)
	pr := provisionerForModelTest(t, "http://example.invalid/model", strings.Repeat("0", 64))
	if err := os.MkdirAll(pr.Layout.ModelsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pr.Layout.ModelPath("tiny.gguf"), []byte("present"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("backend = \"local\"\nmax_calls = 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stdout, stderr, err := runCLIForModelsTest("", pr, "models", "use", "tiny")
	if err != nil {
		t.Fatalf("models use: %v stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "active model set to tiny") {
		t.Fatalf("stdout missing use message: %q", stdout)
	}
	data, err := os.ReadFile(configPath) //nolint:gosec // configPath is a temp config file path created by this test.
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "model = \"tiny\"") || !strings.Contains(text, "backend = \"local\"") || !strings.Contains(text, "max_calls = 7") {
		t.Fatalf("config not updated/preserved: %q", text)
	}
}

func TestModelsUseRequiresInstalledModel(t *testing.T) {
	pr := provisionerForModelTest(t, "http://example.invalid/model", strings.Repeat("0", 64))
	_, stderr, err := runCLIForModelsTest("", pr, "models", "use", "tiny")
	if err == nil {
		t.Fatal("expected not installed error")
	}
	if !strings.Contains(stderr, "ajq models pull tiny") {
		t.Fatalf("stderr should suggest pull: %q", stderr)
	}
}

func TestModelsLivePullOptIn(t *testing.T) {
	if os.Getenv("AJQ_MODELS_LIVE") != "1" {
		t.Skip("set AJQ_MODELS_LIVE=1 to download a real catalog model")
	}
	name := strings.TrimSpace(os.Getenv("AJQ_MODELS_LIVE_MODEL"))
	if name == "" {
		name = provision.DefaultModelName
	}
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	t.Setenv("AJQ_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	stdout, stderr, err := runCLIForModelsTest("", nil, "models", "pull", name)
	if err != nil {
		t.Fatalf("live pull %s: %v stderr=%q", name, err, stderr)
	}
	if !strings.Contains(stdout, fmt.Sprintf("model %s installed:", name)) && !strings.Contains(stdout, fmt.Sprintf("model %s already installed:", name)) {
		t.Fatalf("live pull stdout missing completion for %s: %q", name, stdout)
	}
}

func provisionerForModelTest(t *testing.T, url, sha string) *provision.Provisioner {
	t.Helper()
	catalog := provision.Catalog{Models: map[string]provision.Model{
		"tiny": {
			Name: "tiny",
			Asset: provision.Asset{
				Kind:     provision.KindModel,
				Name:     "tiny",
				Version:  "test",
				Filename: "tiny.gguf",
				URL:      url,
				SHA256:   sha,
				Size:     10,
			},
			RAMNote: "tiny RAM",
		},
	}}
	return &provision.Provisioner{Catalog: catalog, Layout: provision.NewLayout(t.TempDir())}
}

func runCLIForModelsTest(stdin string, pr ProvisionController, args ...string) (stdout, stderr string, err error) {
	var out, errBuf strings.Builder
	opts := Options{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errBuf, Provision: pr}
	err = Execute(context.Background(), opts, args)
	return out.String(), errBuf.String(), err
}

func sha256HexForModelsTest(body []byte) string {
	sum := sha256.Sum256(body)
	return fmt.Sprintf("%x", sum[:])
}
