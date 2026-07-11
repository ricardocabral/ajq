package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
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

func TestModelsListJSONContract(t *testing.T) {
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

	stdout, stderr, err := runCLIForModelsTest("", nil, "models", "list", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("models list --json = (%v, %q)", err, stderr)
	}
	if !strings.HasSuffix(stdout, "\n") || !strings.HasPrefix(stdout, `{"schema_version":"1","active":{"state":"catalog","name":"qwen2.5-3b"},"models":[`) {
		t.Fatalf("models JSON wire prefix/order = %q", stdout)
	}
	var document struct {
		SchemaVersion string `json:"schema_version"`
		Active        struct {
			State string `json:"state"`
			Name  string `json:"name"`
		} `json:"active"`
		Models []struct {
			Name      string `json:"name"`
			Active    bool   `json:"active"`
			Installed bool   `json:"installed"`
			Filename  string `json:"filename"`
			Path      string `json:"path"`
			SizeBytes int64  `json:"size_bytes"`
			RAM       string `json:"ram"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil {
		t.Fatalf("models JSON invalid: %v", err)
	}
	if document.SchemaVersion != "1" || document.Active.State != "catalog" || document.Active.Name != "qwen2.5-3b" || len(document.Models) < 2 {
		t.Fatalf("models document = %+v", document)
	}
	for i, model := range document.Models {
		if i > 0 && document.Models[i-1].Name > model.Name {
			t.Fatalf("models not ordered: %#v", document.Models)
		}
		if model.Name == "qwen2.5-3b" && (!model.Active || !model.Installed || model.Filename == "" || model.Path == "" || model.SizeBytes <= 0 || model.RAM == "") {
			t.Fatalf("active model row = %#v", model)
		}
	}

	t.Run("path-like active", func(t *testing.T) {
		t.Setenv("AJQ_MODEL", filepath.Join(t.TempDir(), "selected.gguf"))
		stdout, stderr, err := runCLIForModelsTest("", nil, "models", "list", "--json")
		if err != nil || stderr != "" || !strings.Contains(stdout, `"active":{"state":"path_like","path":`) || strings.Contains(stdout, `"active":true`) {
			t.Fatalf("path-like models JSON = (%v, %q, %q)", err, stdout, stderr)
		}
	})
	t.Run("unknown active hides config error", func(t *testing.T) {
		const sentinel = "models-json-secret-sentinel"
		t.Setenv("AJQ_CONFIG", filepath.Join(t.TempDir(), "bad.toml"))
		if err := os.WriteFile(os.Getenv("AJQ_CONFIG"), []byte("api_key = \""+sentinel+"\"\nmodel = ["), 0o600); err != nil {
			t.Fatal(err)
		}
		stdout, stderr, err := runCLIForModelsTest("", nil, "models", "list", "--json")
		if err != nil || stderr != "" || !strings.Contains(stdout, `"active":{"state":"unknown"}`) || strings.Contains(stdout, sentinel) {
			t.Fatalf("unknown models JSON leaked or failed = (%v, %q, %q)", err, stdout, stderr)
		}
	})
}

func TestModelsListJSONExactSingleCatalogRow(t *testing.T) {
	cacheDir := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("AJQ_CACHE_DIR", cacheDir)
	t.Setenv("AJQ_CONFIG", configPath)
	t.Setenv("AJQ_MODEL", "")
	if err := os.WriteFile(configPath, []byte("# use local default\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pr := &provision.Provisioner{Catalog: provision.Catalog{Models: map[string]provision.Model{
		"qwen2.5-1.5b": {Name: "qwen2.5-1.5b", Asset: provision.Asset{Kind: provision.KindModel, Name: "qwen2.5-1.5b", Version: "test", Filename: "tiny.gguf", Size: 10}, RAMNote: "tiny RAM"},
	}}, Layout: provision.NewLayout(cacheDir)}
	stdout, stderr, err := runCLIForModelsTest("", pr, "models", "list", "--json")
	modelPath, marshalErr := json.Marshal(filepath.Join(cacheDir, "models", "tiny.gguf"))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	want := `{"schema_version":"1","active":{"state":"catalog","name":"qwen2.5-1.5b"},"models":[{"name":"qwen2.5-1.5b","active":true,"installed":false,"filename":"tiny.gguf","path":` + string(modelPath) + `,"size_bytes":10,"ram":"tiny RAM"}]}` + "\n"
	if err != nil || stderr != "" || stdout != want {
		t.Fatalf("exact models JSON = (%v, %q, %q), want %q", err, stdout, stderr, want)
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
