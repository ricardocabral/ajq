package provision

import (
	"regexp"
	"strings"
	"testing"
)

// sha256HexPattern matches a lowercase 64-char hex SHA-256 digest.
var sha256HexPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestDefaultCatalogModelIsDownloadable proves the default model carries real,
// verified public download metadata so a fresh machine can provision it.
func TestDefaultCatalogModelIsDownloadable(t *testing.T) {
	m := DefaultCatalog().Model

	if !m.Downloadable() {
		t.Fatalf("default model must be downloadable, got URL=%q SHA256=%q", m.URL, m.SHA256)
	}
	if m.Filename != DefaultModelFilename {
		t.Fatalf("model filename = %q, want %q", m.Filename, DefaultModelFilename)
	}
	if m.Kind != KindModel {
		t.Fatalf("model kind = %q, want %q", m.Kind, KindModel)
	}
	// URL must be a commit-pinned Hugging Face resolve URL (not a mutable
	// branch like /main/) for supply-chain immutability.
	if !strings.HasPrefix(m.URL, "https://huggingface.co/") {
		t.Fatalf("model URL should be a Hugging Face URL: %q", m.URL)
	}
	if strings.Contains(m.URL, "/resolve/main/") {
		t.Fatalf("model URL must be commit-pinned, not branch-pinned: %q", m.URL)
	}
	if !strings.Contains(m.URL, "/resolve/") || !strings.HasSuffix(m.URL, DefaultModelFilename) {
		t.Fatalf("model URL should resolve the default model file: %q", m.URL)
	}
	if !sha256HexPattern.MatchString(m.SHA256) {
		t.Fatalf("model SHA256 must be lowercase 64-char hex: %q", m.SHA256)
	}
	if m.Size <= 0 {
		t.Fatalf("model size must be a positive byte count, got %d", m.Size)
	}
}

func TestDefaultCatalogModelsAreNamedAndStable(t *testing.T) {
	c := DefaultCatalog()
	wantNames := []string{"qwen2.5-1.5b", "qwen2.5-3b", "qwen3-4b"}
	if got := c.ModelNames(); strings.Join(got, ",") != strings.Join(wantNames, ",") {
		t.Fatalf("ModelNames() = %v, want %v", got, wantNames)
	}
	for _, name := range wantNames {
		model, err := c.ModelFor(name)
		if err != nil {
			t.Fatalf("ModelFor(%q): %v", name, err)
		}
		if model.Name != name || model.Asset.Name != name || model.RAMNote == "" {
			t.Fatalf("model %q metadata not normalized: %+v", name, model)
		}
		if !model.Asset.Downloadable() || !sha256HexPattern.MatchString(model.Asset.SHA256) || model.Asset.Size <= 0 {
			t.Fatalf("model %q integrity metadata invalid: %+v", name, model.Asset)
		}
	}
	model, err := c.DefaultModel()
	if err != nil {
		t.Fatalf("DefaultModel: %v", err)
	}
	if model.Name != DefaultModelName {
		t.Fatalf("DefaultModel name = %q, want %q", model.Name, DefaultModelName)
	}
}

func TestCatalogUnknownModelErrorListsValidNames(t *testing.T) {
	_, err := DefaultCatalog().ModelFor("bogus")
	if err == nil {
		t.Fatal("expected unknown model error")
	}
	for _, want := range []string{"unknown model \"bogus\"", "\"qwen2.5-1.5b\"", "\"qwen2.5-3b\"", "\"qwen3-4b\""} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err.Error(), want)
		}
	}
}

func TestDefaultCatalogEngineArchivesPinnedForSupportedPlatforms(t *testing.T) {
	c := DefaultCatalog()
	for _, key := range []string{"darwin/arm64", "linux/amd64", "linux/arm64"} {
		e := c.Engines[key]
		if !e.BundleDownloadable() {
			t.Fatalf("engine %q should have a pinned archive bundle: %+v", key, e)
		}
		if e.ReleaseTag != "b9917" || e.ArchiveFormat != "tar.gz" || e.BinaryPath != "llama-b9917/llama-server" {
			t.Fatalf("engine %q bundle metadata mismatch: %+v", key, e)
		}
		if !strings.HasPrefix(e.URL, "https://github.com/ggml-org/llama.cpp/releases/download/b9917/") {
			t.Fatalf("engine %q URL should be release-tag pinned: %q", key, e.URL)
		}
		if !sha256HexPattern.MatchString(e.SHA256) || e.Size <= 0 {
			t.Fatalf("engine %q archive integrity metadata invalid: %+v", key, e)
		}
	}
	for _, key := range []string{"darwin/amd64", "windows/amd64"} {
		e := c.Engines[key]
		if e.Downloadable() || e.BundleDownloadable() {
			t.Fatalf("engine %q should remain manual-install only: %+v", key, e)
		}
	}
}

// TestDefaultCatalogEngineSelectionAndUnsupported verifies per-platform
// selection still works and that an unsupported platform errors clearly.
func TestDefaultCatalogEngineSelectionAndUnsupported(t *testing.T) {
	c := DefaultCatalog()

	for _, key := range []string{"darwin/arm64", "darwin/amd64", "linux/amd64", "linux/arm64", "windows/amd64"} {
		parts := strings.SplitN(key, "/", 2)
		if _, err := c.EngineFor(Platform{OS: parts[0], Arch: parts[1]}); err != nil {
			t.Fatalf("expected engine for %q, got error: %v", key, err)
		}
	}

	_, err := c.EngineFor(Platform{OS: "plan9", Arch: "mips"})
	if err == nil {
		t.Fatal("expected error for unsupported platform")
	}
	if !strings.Contains(err.Error(), "plan9/mips") {
		t.Fatalf("unsupported-platform error should name the platform: %v", err)
	}
}
