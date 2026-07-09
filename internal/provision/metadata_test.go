package provision

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestLoadMetadataMissingFileIsEmpty(t *testing.T) {
	m, err := LoadMetadata(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if m.Engine != nil {
		t.Fatal("expected no engine in empty metadata")
	}
	if m.Models == nil {
		t.Fatal("expected non-nil Models map")
	}
	if m.SchemaVersion != metadataSchemaVersion {
		t.Fatalf("schema version = %d, want %d", m.SchemaVersion, metadataSchemaVersion)
	}
}

func TestMetadataSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "provision.json")
	m := NewMetadata()
	installed := time.Date(2026, 7, 6, 12, 0, 0, 0, time.UTC)
	m.SetEngine(InstalledAsset{
		Name:        EngineBinaryName,
		Version:     "9870",
		Path:        "/cache/bin/llama-server",
		SHA256:      "abc123",
		Size:        1024,
		InstalledAt: installed,
	})
	m.SetModel(InstalledAsset{
		Name:        "qwen2.5-1.5b-instruct",
		Version:     "q5_k_m",
		Path:        "/cache/models/model.gguf",
		SHA256:      "def456",
		Size:        2048,
		InstalledAt: installed,
	})

	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("metadata file not written: %v", err)
	}

	got, err := LoadMetadata(path)
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if got.Engine == nil || got.Engine.SHA256 != "abc123" || got.Engine.Version != "9870" {
		t.Fatalf("engine round-trip mismatch: %+v", got.Engine)
	}
	if got.Engine.Size != 1024 {
		t.Fatalf("engine size = %d, want 1024", got.Engine.Size)
	}
	model, ok := got.Models["qwen2.5-1.5b-instruct"]
	if !ok {
		t.Fatalf("model missing from metadata: %+v", got.Models)
	}
	if model.SHA256 != "def456" || model.Path != "/cache/models/model.gguf" {
		t.Fatalf("model round-trip mismatch: %+v", model)
	}
	if !model.InstalledAt.Equal(installed) {
		t.Fatalf("installed-at mismatch: %v", model.InstalledAt)
	}
}

func TestMetadataSaveUsesPrivateDirAndFileModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not reliable on Windows")
	}
	path := filepath.Join(t.TempDir(), "state", "provision.json")
	if err := NewMetadata().Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for _, tc := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{name: "metadata dir", path: filepath.Dir(path), want: 0o700},
		{name: "metadata file", path: path, want: 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.name, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("%s mode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestLoadMetadataRejectsMalformed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadMetadata(path); err == nil {
		t.Fatal("expected parse error for malformed metadata")
	}
}

func TestLayoutPaths(t *testing.T) {
	l := NewLayout("/base")
	if l.BinDir() != filepath.Join("/base", "bin") {
		t.Fatalf("BinDir = %q", l.BinDir())
	}
	if l.EngineBinaryPath("") != filepath.Join("/base", "bin", "llama-server") {
		t.Fatalf("EngineBinaryPath = %q", l.EngineBinaryPath(""))
	}
	if l.EngineBundleDir("b9917") != filepath.Join("/base", "engine", "b9917") {
		t.Fatalf("EngineBundleDir = %q", l.EngineBundleDir("b9917"))
	}
	if l.EngineBundleBinaryPath("b9917", "llama-b9917/llama-server") != filepath.Join("/base", "engine", "b9917", "llama-b9917", "llama-server") {
		t.Fatalf("EngineBundleBinaryPath = %q", l.EngineBundleBinaryPath("b9917", "llama-b9917/llama-server"))
	}
	if l.ModelPath("m.gguf") != filepath.Join("/base", "models", "m.gguf") {
		t.Fatalf("ModelPath = %q", l.ModelPath("m.gguf"))
	}
	if l.TempDir() != filepath.Join("/base", "tmp") {
		t.Fatalf("TempDir = %q", l.TempDir())
	}
	if l.MetadataPath() != filepath.Join("/base", "provision.json") {
		t.Fatalf("MetadataPath = %q", l.MetadataPath())
	}
}

func TestDefaultCacheDirRespectsEnv(t *testing.T) {
	t.Setenv(EnvCacheDir, "/env/cache/ajq")
	if got := DefaultCacheDir(); got != "/env/cache/ajq" {
		t.Fatalf("DefaultCacheDir = %q, want env override", got)
	}
}
