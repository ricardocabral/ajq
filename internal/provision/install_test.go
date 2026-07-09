package provision

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// installTestSetup builds a Provisioner + Plan backed by an httptest server
// serving the given engine and model payloads. Assets are treated as missing so
// Install performs a real (but local) download.
func installTestSetup(t *testing.T, engineBody, modelBody []byte) (*Provisioner, Plan, *int) {
	t.Helper()
	cacheDir := t.TempDir()
	var hits int
	mux := http.NewServeMux()
	mux.HandleFunc("/engine", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(engineBody)
	})
	mux.HandleFunc("/model", func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(modelBody)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	layout := NewLayout(cacheDir)
	engineAsset := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: EngineBinaryName, URL: srv.URL + "/engine", SHA256: sha256Hex(engineBody)}
	modelAsset := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf", URL: srv.URL + "/model", SHA256: sha256Hex(modelBody)}

	pr := &Provisioner{
		Catalog:    Catalog{Engines: map[string]Asset{"darwin/arm64": engineAsset}, Model: modelAsset},
		Layout:     layout,
		Platform:   Platform{OS: "darwin", Arch: "arm64"},
		LookPath:   noLookPath,
		HTTPClient: srv.Client(),
	}
	plan := Plan{
		Platform: pr.Platform,
		Layout:   layout,
		Engine:   AssetStatus{Asset: engineAsset, Path: layout.EngineBinaryPath(engineAsset.Filename)},
		Model:    AssetStatus{Asset: modelAsset, Path: layout.ModelPath(modelAsset.Filename)},
	}
	return pr, plan, &hits
}

func TestInstallDownloadSuccess(t *testing.T) {
	engineBody := []byte("fake-llama-server-binary")
	modelBody := []byte("fake-gguf-model-weights")
	pr, plan, _ := installTestSetup(t, engineBody, modelBody)

	var events []Progress
	if err := pr.Install(context.Background(), plan, func(p Progress) { events = append(events, p) }); err != nil {
		t.Fatalf("Install: %v", err)
	}

	got, err := os.ReadFile(plan.Engine.Path)
	if err != nil || string(got) != string(engineBody) {
		t.Fatalf("engine not installed correctly: %v %q", err, string(got))
	}
	info, err := os.Stat(plan.Engine.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !executableFile(plan.Engine.Path) {
		t.Fatalf("engine binary not executable: %v", info.Mode())
	}
	gotModel, err := os.ReadFile(plan.Model.Path)
	if err != nil || string(gotModel) != string(modelBody) {
		t.Fatalf("model not installed correctly: %v", err)
	}

	// Metadata was written with checksums.
	md, err := LoadMetadata(plan.Layout.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if md.Engine == nil || md.Engine.SHA256 != sha256Hex(engineBody) {
		t.Fatalf("engine metadata missing/mismatched: %+v", md.Engine)
	}
	if m, ok := md.Models["model"]; !ok || m.SHA256 != sha256Hex(modelBody) {
		t.Fatalf("model metadata missing/mismatched: %+v", md.Models)
	}

	// A final Done event should have been emitted for each asset.
	var doneCount int
	for _, e := range events {
		if e.Done {
			doneCount++
		}
	}
	if doneCount < 2 {
		t.Fatalf("expected >=2 Done progress events, got %d (%+v)", doneCount, events)
	}

	// Temp dir should contain no leftover .part files.
	assertNoPartFiles(t, plan.Layout.TempDir())
}

func TestInstallChecksumMismatch(t *testing.T) {
	pr, plan, _ := installTestSetup(t, []byte("real-engine"), []byte("real-model"))
	// Corrupt the expected engine checksum so verification fails.
	plan.Engine.Asset.SHA256 = sha256Hex([]byte("something-else"))

	err := pr.Install(context.Background(), plan, nil)
	if err == nil {
		t.Fatal("expected checksum mismatch error")
	}
	if !contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error should mention checksum mismatch: %v", err)
	}
	// No engine file should have been installed.
	if _, err := os.Stat(plan.Engine.Path); !os.IsNotExist(err) {
		t.Fatalf("engine should not exist after mismatch: %v", err)
	}
	assertNoPartFiles(t, plan.Layout.TempDir())
}

func TestInstallInterruptedCleansUp(t *testing.T) {
	cacheDir := t.TempDir()
	body := []byte("0123456789")
	// Handler declares more bytes than it writes, forcing an unexpected EOF on
	// the client and an interrupted download.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(len(body)+50))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	layout := NewLayout(cacheDir)
	engineAsset := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: EngineBinaryName, URL: srv.URL, SHA256: sha256Hex(body)}
	pr := &Provisioner{Layout: layout, HTTPClient: srv.Client()}
	plan := Plan{
		Layout: layout,
		Engine: AssetStatus{Asset: engineAsset, Path: layout.EngineBinaryPath(engineAsset.Filename)},
		Model:  AssetStatus{Asset: Asset{Kind: KindModel, Name: "model"}, Present: true, Path: filepath.Join(cacheDir, "present")},
	}
	// Make the "present" model actually exist so its no-op path stats OK.
	if err := os.WriteFile(plan.Model.Path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	err := pr.Install(context.Background(), plan, nil)
	if err == nil {
		t.Fatal("expected interrupted download to error")
	}
	if _, statErr := os.Stat(plan.Engine.Path); !os.IsNotExist(statErr) {
		t.Fatalf("engine should not exist after interruption: %v", statErr)
	}
	assertNoPartFiles(t, layout.TempDir())
}

func TestInstallAlreadyPresentNoOp(t *testing.T) {
	cacheDir := t.TempDir()
	layout := NewLayout(cacheDir)
	enginePath := layout.EngineBinaryPath(EngineBinaryName)
	modelPath := layout.ModelPath("model.gguf")
	if err := os.MkdirAll(filepath.Dir(enginePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(modelPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(enginePath, []byte("engine"), 0o755); err != nil { //nolint:gosec // test creates an executable fixture to simulate a provisioned engine binary.
		t.Fatal(err)
	}
	if err := os.WriteFile(modelPath, []byte("model"), 0o600); err != nil {
		t.Fatal(err)
	}

	// A URL that would fail if hit — proving no download happens.
	badURL := "http://127.0.0.1:0/should-not-be-called"
	pr := &Provisioner{
		Layout:     layout,
		HTTPClient: &http.Client{},
	}
	plan := Plan{
		Layout: layout,
		Engine: AssetStatus{Asset: Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: EngineBinaryName, URL: badURL, SHA256: "x"}, Present: true, Path: enginePath},
		Model:  AssetStatus{Asset: Asset{Kind: KindModel, Name: "model", Filename: "model.gguf", URL: badURL, SHA256: "x"}, Present: true, Path: modelPath},
	}

	var skipped int
	if err := pr.Install(context.Background(), plan, func(p Progress) {
		if p.Skipped {
			skipped++
		}
	}); err != nil {
		t.Fatalf("Install no-op failed: %v", err)
	}
	if skipped != 2 {
		t.Fatalf("expected 2 skipped events, got %d", skipped)
	}
	// Metadata should record both present assets.
	md, err := LoadMetadata(layout.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if md.Engine == nil || md.Engine.Path != enginePath {
		t.Fatalf("engine not recorded from present state: %+v", md.Engine)
	}
}

func TestInstallEngineBundleFromArchiveAndRerunCacheHit(t *testing.T) {
	archive := writeTarGzFixture(t,
		tarEntry{name: "bundle/", typeflag: tar.TypeDir, mode: 0o755},
		tarEntry{name: "bundle/llama-server", body: []byte("engine"), mode: 0o755},
		tarEntry{name: "bundle/libllama.dylib", body: []byte("lib"), mode: 0o644},
	)
	body, err := os.ReadFile(archive) //nolint:gosec // archive is a fixture file created inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	layout := NewLayout(t.TempDir())
	model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf"}
	modelPath := mustWrite(t, layout.ModelPath(model.Filename), "model")
	engine := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: "engine.tar.gz", Version: "testtag", URL: srv.URL, SHA256: sha256Hex(body), Size: int64(len(body)), ReleaseTag: "testtag", ArchiveFormat: "tar.gz", BinaryPath: "bundle/llama-server"}
	catalog := Catalog{Engines: map[string]Asset{"darwin/arm64": engine}, Model: model}
	pr := &Provisioner{Catalog: catalog, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: noLookPath, HTTPClient: srv.Client()}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if plan.Engine.Present || plan.Engine.Path != layout.EngineBundleBinaryPath("testtag", "bundle/llama-server") {
		t.Fatalf("unexpected initial engine plan: %+v", plan.Engine)
	}
	if !plan.Model.Present || plan.Model.Path != modelPath {
		t.Fatalf("model should be a cache hit: %+v", plan.Model)
	}
	if err := pr.Install(context.Background(), plan, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected one archive download, got %d", hits)
	}
	if !executableFile(plan.Engine.Path) {
		t.Fatalf("engine bundle binary not executable at %q", plan.Engine.Path)
	}
	md, err := LoadMetadata(layout.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if md.Engine == nil || md.Engine.Bundle == nil || md.Engine.Bundle.Tag != "testtag" || md.Engine.Bundle.ArchiveSHA256 != sha256Hex(body) {
		t.Fatalf("bundle metadata missing/mismatched: %+v", md.Engine)
	}
	if len(md.Engine.Bundle.Files) != 2 {
		t.Fatalf("bundle file list = %+v", md.Engine.Bundle.Files)
	}

	pr2 := &Provisioner{Catalog: catalog, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: func(string) (string, error) { return "/path/llama-server", nil }}
	plan2, err := pr2.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Engine.Present || plan2.Engine.Source != "bundle" || plan2.Engine.Path != plan.Engine.Path {
		t.Fatalf("rerun should be bundle cache hit before PATH: %+v", plan2.Engine)
	}
}

func TestInstallEngineBundleModelFailureKeepsUsableBundleMetadata(t *testing.T) {
	archive := writeTarGzFixture(t, tarEntry{name: "bundle/llama-server", body: []byte("engine"), mode: 0o755})
	body, err := os.ReadFile(archive) //nolint:gosec // archive is a fixture file created inside t.TempDir.
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/engine":
			_, _ = w.Write(body)
		case "/model":
			_, _ = w.Write([]byte("bad-model"))
		}
	}))
	t.Cleanup(srv.Close)
	layout := NewLayout(t.TempDir())
	engine := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: "engine.tar.gz", URL: srv.URL + "/engine", SHA256: sha256Hex(body), Size: int64(len(body)), ReleaseTag: "testtag", ArchiveFormat: "tar.gz", BinaryPath: "bundle/llama-server"}
	model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf", URL: srv.URL + "/model", SHA256: sha256Hex([]byte("good-model"))}
	catalog := Catalog{Engines: map[string]Asset{"darwin/arm64": engine}, Model: model}
	pr := &Provisioner{Catalog: catalog, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: noLookPath, HTTPClient: srv.Client()}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Install(context.Background(), plan, nil); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("expected model checksum failure, got %v", err)
	}
	md, err := LoadMetadata(layout.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if md.Engine == nil || md.Engine.Bundle == nil {
		t.Fatalf("engine bundle metadata should survive later model failure: %+v", md.Engine)
	}
	plan2, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Engine.Present || plan2.Engine.Source != "bundle" {
		t.Fatalf("bundle should remain a cache hit after model failure: %+v", plan2.Engine)
	}
}

func TestInstallPreservesPresentBundleMetadataWhileInstallingModel(t *testing.T) {
	layout := NewLayout(t.TempDir())
	engine := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: "engine.tar.gz", URL: "http://example.invalid/engine", SHA256: sha256Hex([]byte("archive")), Size: 7, ReleaseTag: "testtag", ArchiveFormat: "tar.gz", BinaryPath: "bundle/llama-server"}
	binary := mustWrite(t, layout.EngineBundleBinaryPath("testtag", "bundle/llama-server"), "engine")
	md := NewMetadata()
	md.SetEngine(InstalledAsset{Name: EngineBinaryName, Version: "testtag", Path: binary, SHA256: engine.SHA256, Size: engine.Size, Bundle: &InstalledBundle{Tag: "testtag", Root: layout.EngineBundleDir("testtag"), BinaryPath: binary, BinaryRelPath: "bundle/llama-server", ArchiveSHA256: engine.SHA256, ArchiveSize: engine.Size}})
	if err := md.Save(layout.MetadataPath()); err != nil {
		t.Fatal(err)
	}
	modelBody := []byte("model")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(modelBody) }))
	t.Cleanup(srv.Close)
	model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf", URL: srv.URL, SHA256: sha256Hex(modelBody)}
	pr := &Provisioner{Catalog: Catalog{Engines: map[string]Asset{"darwin/arm64": engine}, Model: model}, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: noLookPath, HTTPClient: srv.Client()}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Engine.Present || plan.Engine.Source != "bundle" || plan.Model.Present {
		t.Fatalf("expected bundle engine and missing model: %+v", plan)
	}
	if err := pr.Install(context.Background(), plan, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	md, err = LoadMetadata(layout.MetadataPath())
	if err != nil {
		t.Fatal(err)
	}
	if md.Engine == nil || md.Engine.Bundle == nil || md.Engine.Bundle.ArchiveSHA256 != engine.SHA256 {
		t.Fatalf("bundle metadata was not preserved: %+v", md.Engine)
	}
	plan2, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Engine.Present || plan2.Engine.Source != "bundle" {
		t.Fatalf("bundle should remain a cache hit: %+v", plan2.Engine)
	}
}

func TestInstallEngineBundleChecksumMismatchAndExtractionFailureCleanUp(t *testing.T) {
	for _, tc := range []struct {
		name     string
		archive  string
		checksum string
		want     string
	}{
		{name: "checksum", archive: writeTarGzFixture(t, tarEntry{name: "bundle/llama-server", body: []byte("engine"), mode: 0o755}), checksum: sha256Hex([]byte("wrong")), want: "checksum mismatch"},
		{name: "extract", archive: writeTarGzFixture(t, tarEntry{name: "../pwned", body: []byte("x")}), want: "parent directory traversal"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, err := os.ReadFile(tc.archive)
			if err != nil {
				t.Fatal(err)
			}
			checksum := tc.checksum
			if checksum == "" {
				checksum = sha256Hex(body)
			}
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
			t.Cleanup(srv.Close)
			layout := NewLayout(t.TempDir())
			model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf"}
			mustWrite(t, layout.ModelPath(model.Filename), "model")
			engine := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: "engine.tar.gz", URL: srv.URL, SHA256: checksum, Size: int64(len(body)), ReleaseTag: "testtag", ArchiveFormat: "tar.gz", BinaryPath: "bundle/llama-server"}
			pr := &Provisioner{Catalog: Catalog{Engines: map[string]Asset{"darwin/arm64": engine}, Model: model}, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: noLookPath, HTTPClient: srv.Client()}
			plan, err := pr.Plan()
			if err != nil {
				t.Fatal(err)
			}
			err = pr.Install(context.Background(), plan, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
			if _, err := os.Stat(layout.EngineBundleDir("testtag")); !os.IsNotExist(err) {
				t.Fatalf("bundle destination should not exist: %v", err)
			}
			if md, err := LoadMetadata(layout.MetadataPath()); err != nil {
				t.Fatal(err)
			} else if md.Engine != nil {
				t.Fatalf("metadata should not claim failed engine install: %+v", md.Engine)
			}
		})
	}
}

func TestPlanIgnoresStaleBundleMetadataAndFallsBackToLegacy(t *testing.T) {
	layout := NewLayout(t.TempDir())
	engine := Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: "engine.tar.gz", URL: "http://example.invalid/engine", SHA256: sha256Hex([]byte("archive")), Size: 7, ReleaseTag: "testtag", ArchiveFormat: "tar.gz", BinaryPath: "bundle/llama-server"}
	binary := mustWrite(t, layout.EngineBundleBinaryPath("testtag", "bundle/llama-server"), "engine")
	legacy := mustWrite(t, layout.EngineBinaryPath(EngineBinaryName), "legacy")
	md := NewMetadata()
	md.SetEngine(InstalledAsset{Name: EngineBinaryName, Path: binary, SHA256: "stale", Bundle: &InstalledBundle{Tag: "testtag", Root: layout.EngineBundleDir("testtag"), BinaryPath: binary, BinaryRelPath: "bundle/llama-server", ArchiveSHA256: "stale", ArchiveSize: 1}})
	if err := md.Save(layout.MetadataPath()); err != nil {
		t.Fatal(err)
	}
	model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf"}
	mustWrite(t, layout.ModelPath(model.Filename), "model")
	pr := &Provisioner{Catalog: Catalog{Engines: map[string]Asset{"darwin/arm64": engine}, Model: model}, Layout: layout, Platform: Platform{OS: "darwin", Arch: "arm64"}, LookPath: noLookPath}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if !plan.Engine.Present || plan.Engine.Source != "cache" || plan.Engine.Path != legacy {
		t.Fatalf("stale bundle should be ignored in favor of legacy cache: %+v", plan.Engine)
	}
}

func TestInstallEngineBundleLiveDownloadOptIn(t *testing.T) {
	if os.Getenv("AJQ_PROVISION_LIVE") != "1" {
		t.Skip("set AJQ_PROVISION_LIVE=1 to download and execute the real llama-server archive")
	}
	catalog := DefaultCatalog()
	platform := CurrentPlatform()
	engine, err := catalog.EngineFor(platform)
	if err != nil || !engine.BundleDownloadable() {
		t.Skipf("no pinned engine bundle for %s: %v", platform, err)
	}
	layout := NewLayout(t.TempDir())
	model := Asset{Kind: KindModel, Name: "model", Filename: "model.gguf"}
	mustWrite(t, layout.ModelPath(model.Filename), "model")
	catalog.Model = model
	pr := &Provisioner{Catalog: catalog, Layout: layout, Platform: platform, LookPath: noLookPath}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatal(err)
	}
	if err := pr.Install(context.Background(), plan, nil); err != nil {
		t.Fatalf("Install live engine bundle: %v", err)
	}
	out, err := exec.CommandContext(context.Background(), plan.Engine.Path, "--version").CombinedOutput() //nolint:gosec // live opt-in test executes the just-provisioned pinned engine binary.
	if err != nil {
		t.Fatalf("llama-server --version failed: %v\n%s", err, out)
	}
}

func TestInstallRefusesNonDownloadableMissingAsset(t *testing.T) {
	layout := NewLayout(t.TempDir())
	pr := &Provisioner{Layout: layout}
	plan := Plan{
		Layout: layout,
		Engine: AssetStatus{Asset: Asset{Kind: KindEngine, Name: EngineBinaryName}, Present: false, Path: layout.EngineBinaryPath("")},
		Model:  AssetStatus{Asset: Asset{Kind: KindModel, Name: "model"}, Present: true, Path: filepath.Join(layout.CacheDir, "m")},
	}
	_ = os.MkdirAll(layout.CacheDir, 0o700)
	_ = os.WriteFile(plan.Model.Path, []byte("x"), 0o600)

	err := pr.Install(context.Background(), plan, nil)
	if err == nil || !contains(err.Error(), "no download source") {
		t.Fatalf("expected no-download-source error, got %v", err)
	}
}

func assertNoPartFiles(t *testing.T, tempDir string) {
	t.Helper()
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".part") {
			t.Fatalf("leftover temp download file: %s", e.Name())
		}
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
