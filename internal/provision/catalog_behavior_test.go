package provision

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rejectingTransport fails any HTTP request. Injected into a Provisioner it
// guarantees a code path that would otherwise reach the network fails loudly
// (with this sentinel) instead of performing a real download.
type rejectingTransport struct{ t *testing.T }

func (rt rejectingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.t.Fatalf("unexpected outbound HTTP request to %q — automated tests must not hit the network", req.URL)
	return nil, nil
}

// defaultCatalogProvisioner builds a Provisioner using the real DefaultCatalog
// for darwin/arm64 with nothing on disk and nothing on PATH. Its HTTP client
// rejects every request so any accidental real download is caught.
func defaultCatalogProvisioner(t *testing.T) *Provisioner {
	t.Helper()
	return &Provisioner{
		Catalog:    DefaultCatalog(),
		Layout:     NewLayout(t.TempDir()),
		Platform:   Platform{OS: "darwin", Arch: "arm64"},
		LookPath:   noLookPath, // do not discover a real llama-server on the test host
		FileExists: fakeFS(),   // nothing present
		HTTPClient: &http.Client{Transport: rejectingTransport{t}},
	}
}

// TestDefaultCatalogMakesMissingModelDownloadable proves the new catalog
// metadata makes a missing model downloadable while the engine remains a
// clear/actionable manual-install case — the core behavioral change of TP-025.
func TestDefaultCatalogMakesMissingModelDownloadable(t *testing.T) {
	pr := defaultCatalogProvisioner(t)

	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.NeedsProvisioning() {
		t.Fatalf("expected provisioning to be needed with empty cache: %+v", plan)
	}
	if !plan.Model.Asset.Downloadable() {
		t.Fatalf("missing model should be downloadable from catalog metadata: %+v", plan.Model.Asset)
	}
	if !plan.Engine.Asset.BundleDownloadable() {
		t.Fatalf("darwin/arm64 engine should be downloadable as a pinned archive bundle: %+v", plan.Engine.Asset)
	}
}

// TestUnpinnedCatalogEngineInstallIsActionableAndOffline proves that installing
// a plan whose engine platform has no pinned archive returns the actionable
// manual-install error WITHOUT issuing any HTTP request (the rejecting transport
// would fail the test otherwise).
func TestUnpinnedCatalogEngineInstallIsActionableAndOffline(t *testing.T) {
	pr := defaultCatalogProvisioner(t)
	pr.Platform = Platform{OS: "windows", Arch: "amd64"}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	err = pr.Install(context.Background(), plan, nil)
	if err == nil {
		t.Fatal("expected engine install to fail without a download source")
	}
	if !strings.Contains(err.Error(), "no download source") {
		t.Fatalf("engine error should be the actionable manual-install message, got: %v", err)
	}
	// Engine must not have been written.
	if _, statErr := os.Stat(plan.Engine.Path); !os.IsNotExist(statErr) {
		t.Fatalf("engine should not exist after actionable failure: %v", statErr)
	}
}

// TestCatalogModelDownloadsWhenMetadataPresent proves that a model asset
// carrying URL+checksum metadata (as the default catalog model now does) is
// fetched, verified, and installed — using an httptest server so no real
// network access occurs. It uses the default model's identity (Name/Filename)
// to tie the behavior to the real catalog entry.
func TestCatalogModelDownloadsWhenMetadataPresent(t *testing.T) {
	body := []byte("fake-gguf-weights-for-offline-test")
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)

	layout := NewLayout(t.TempDir())
	// Start from the real catalog model, then redirect the URL to the local
	// server and set the checksum to match the served bytes.
	modelAsset := DefaultCatalog().Model
	modelAsset.URL = srv.URL + "/model"
	modelAsset.SHA256 = sha256Hex(body)
	modelAsset.Size = int64(len(body))

	pr := &Provisioner{Layout: layout, HTTPClient: srv.Client()}
	plan := Plan{
		Layout: layout,
		// Engine already present so Install focuses on the model path.
		Engine: AssetStatus{Asset: Asset{Kind: KindEngine, Name: EngineBinaryName}, Present: true, Path: mustWrite(t, layout.EngineBinaryPath(EngineBinaryName), "engine")},
		Model:  AssetStatus{Asset: modelAsset, Path: layout.ModelPath(modelAsset.Filename)},
	}

	if err := pr.Install(context.Background(), plan, nil); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if hits != 1 {
		t.Fatalf("expected exactly one model download, got %d", hits)
	}
	got, err := os.ReadFile(plan.Model.Path)
	if err != nil || string(got) != string(body) {
		t.Fatalf("model not installed correctly: %v %q", err, string(got))
	}
	if md, err := LoadMetadata(layout.MetadataPath()); err != nil {
		t.Fatal(err)
	} else if m, ok := md.Models[modelAsset.Name]; !ok || m.SHA256 != sha256Hex(body) {
		t.Fatalf("model metadata missing/mismatched: %+v", md.Models)
	}
}

// mustWrite creates parent dirs and writes content, returning the path.
func mustWrite(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil { //nolint:gosec // tests create executable fixture files to simulate provisioned engines.
		t.Fatal(err)
	}
	return path
}
