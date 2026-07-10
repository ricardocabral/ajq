package provision

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestCurrentPlatformKey(t *testing.T) {
	p := Platform{OS: "Darwin", Arch: "ARM64"}
	if got := p.Key(); got != "darwin/arm64" {
		t.Fatalf("Key() = %q, want darwin/arm64", got)
	}
	cur := CurrentPlatform()
	if cur.OS == "" || cur.Arch == "" {
		t.Fatalf("CurrentPlatform returned empty fields: %+v", cur)
	}
}

func TestCatalogEngineForSelection(t *testing.T) {
	c := DefaultCatalog()

	mac, err := c.EngineFor(Platform{OS: "darwin", Arch: "arm64"})
	if err != nil {
		t.Fatalf("darwin/arm64 engine: %v", err)
	}
	if mac.Kind != KindEngine || !mac.BundleDownloadable() || mac.BinaryPath != "llama-b9917/llama-server" {
		t.Fatalf("unexpected mac engine asset: %+v", mac)
	}

	win, err := c.EngineFor(Platform{OS: "windows", Arch: "amd64"})
	if err != nil {
		t.Fatalf("windows engine: %v", err)
	}
	if win.Filename != EngineBinaryName+".exe" {
		t.Fatalf("windows engine filename = %q, want .exe suffix", win.Filename)
	}

	if _, err := c.EngineFor(Platform{OS: "plan9", Arch: "mips"}); err == nil {
		t.Fatal("expected error for unsupported platform")
	}
}

// fakeFS builds a FileExistsFunc from a set of present paths.
func fakeFS(present ...string) FileExistsFunc {
	set := map[string]bool{}
	for _, p := range present {
		set[p] = true
	}
	return func(path string) bool { return set[path] }
}

func noLookPath(string) (string, error) { return "", errors.New("not found") }

func macProvisioner(cacheDir string) *Provisioner {
	return &Provisioner{
		Catalog:  DefaultCatalog(),
		Layout:   NewLayout(cacheDir),
		Platform: Platform{OS: "darwin", Arch: "arm64"},
		LookPath: noLookPath,
	}
}

func TestPlanCacheMissWhenNothingPresent(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	pr := macProvisioner(cacheDir)
	pr.FileExists = fakeFS() // nothing present

	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.NeedsProvisioning() {
		t.Fatal("expected provisioning to be needed")
	}
	if plan.Engine.Present || plan.Model.Present {
		t.Fatalf("expected both assets missing: %+v", plan)
	}
	if len(plan.Missing()) != 2 {
		t.Fatalf("expected 2 missing assets, got %d", len(plan.Missing()))
	}
	if plan.Engine.Path != filepath.Join(cacheDir, "engine", "b9917", "llama-b9917", EngineBinaryName) {
		t.Fatalf("engine dest = %q", plan.Engine.Path)
	}
	if plan.Model.Path != filepath.Join(cacheDir, "models", DefaultModelFilename) {
		t.Fatalf("model dest = %q", plan.Model.Path)
	}
	if plan.Model.Asset.Name != DefaultModelName {
		t.Fatalf("default model asset name = %q, want %q", plan.Model.Asset.Name, DefaultModelName)
	}
}

func TestPlanModelResolvesRequestedCatalogModel(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	pr := macProvisioner(cacheDir)
	pr.FileExists = fakeFS(filepath.Join(cacheDir, "models", "Qwen3-4B-Q4_K_M.gguf"))

	plan, err := pr.PlanModel("qwen3-4b")
	if err != nil {
		t.Fatalf("PlanModel: %v", err)
	}
	if !plan.Model.Present || plan.Model.Asset.Name != "qwen3-4b" || plan.Model.Source != "cache" {
		t.Fatalf("requested model should be a qwen3-4b cache hit: %+v", plan.Model)
	}
	if plan.Model.Path != filepath.Join(cacheDir, "models", "Qwen3-4B-Q4_K_M.gguf") {
		t.Fatalf("requested model path = %q", plan.Model.Path)
	}
}

func TestPlanModelOnlyResolvesRequestedCatalogModel(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	modelPath := filepath.Join(cacheDir, "models", "Qwen3-4B-Q4_K_M.gguf")
	pr := macProvisioner(cacheDir)
	pr.FileExists = fakeFS(modelPath)

	plan, err := pr.PlanModelOnly("qwen3-4b")
	if err != nil {
		t.Fatalf("PlanModelOnly: %v", err)
	}
	if !plan.Model.Present || plan.Model.Asset.Name != "qwen3-4b" || plan.Model.Source != "cache" {
		t.Fatalf("requested model should be a qwen3-4b cache hit: %+v", plan.Model)
	}
	if plan.Model.Path != modelPath {
		t.Fatalf("requested model path = %q, want %q", plan.Model.Path, modelPath)
	}
	if plan.Engine != (AssetStatus{}) {
		t.Fatalf("PlanModelOnly engine = %+v, want zero value", plan.Engine)
	}
}

func TestPlanModelOnlyWorksWithoutSupportedEngine(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	modelPath := filepath.Join(cacheDir, "models", "Qwen3-4B-Q4_K_M.gguf")
	pr := &Provisioner{
		Catalog:    DefaultCatalog(),
		Layout:     NewLayout(cacheDir),
		Platform:   Platform{OS: "plan9", Arch: "mips"},
		FileExists: fakeFS(modelPath),
	}

	plan, err := pr.PlanModelOnly("qwen3-4b")
	if err != nil {
		t.Fatalf("PlanModelOnly on unsupported platform: %v", err)
	}
	if !plan.Model.Present || plan.Model.Asset.Name != "qwen3-4b" || plan.Model.Source != "cache" {
		t.Fatalf("requested model should be a qwen3-4b cache hit: %+v", plan.Model)
	}
	if plan.Engine != (AssetStatus{}) {
		t.Fatalf("PlanModelOnly engine = %+v, want zero value", plan.Engine)
	}
}

func TestPlanModelOnlyUnknownNameErrors(t *testing.T) {
	pr := macProvisioner("/tmp/ajq-cache")
	pr.FileExists = fakeFS()
	if _, err := pr.PlanModelOnly("bogus"); err == nil || !strings.Contains(err.Error(), "valid models") {
		t.Fatalf("PlanModelOnly unknown error = %v, want valid-model list", err)
	}
}

func TestPlanModelOnlyHonorsModelOverride(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	modelOverride := "/custom/model.gguf"
	cachedModel := filepath.Join(cacheDir, "models", DefaultModelFilename)
	pr := macProvisioner(cacheDir)
	pr.ModelOverride = modelOverride
	pr.FileExists = fakeFS(modelOverride, cachedModel)

	plan, err := pr.PlanModelOnly("")
	if err != nil {
		t.Fatalf("PlanModelOnly: %v", err)
	}
	if plan.Model.Path != modelOverride || plan.Model.Source != "override" {
		t.Fatalf("model override should take precedence over cache: %+v", plan.Model)
	}
}

func TestPlanModelUnknownNameErrors(t *testing.T) {
	pr := macProvisioner("/tmp/ajq-cache")
	pr.FileExists = fakeFS()
	if _, err := pr.PlanModel("bogus"); err == nil || !strings.Contains(err.Error(), "valid models") {
		t.Fatalf("PlanModel unknown error = %v, want valid-model list", err)
	}
}

func TestPlanCacheHitFromCacheDir(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	engine := filepath.Join(cacheDir, "bin", EngineBinaryName)
	model := filepath.Join(cacheDir, "models", DefaultModelFilename)
	pr := macProvisioner(cacheDir)
	pr.FileExists = fakeFS(engine, model)

	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.NeedsProvisioning() {
		t.Fatalf("expected no provisioning needed: %+v", plan)
	}
	if plan.Engine.Source != "cache" || plan.Model.Source != "cache" {
		t.Fatalf("expected cache source, got engine=%q model=%q", plan.Engine.Source, plan.Model.Source)
	}
}

func TestPlanLegacyWindowsCacheHitUsesExeFilename(t *testing.T) {
	cacheDir := "/tmp/ajq-cache"
	engine := filepath.Join(cacheDir, "bin", EngineBinaryName+".exe")
	model := filepath.Join(cacheDir, "models", DefaultModelFilename)
	pr := &Provisioner{Catalog: DefaultCatalog(), Layout: NewLayout(cacheDir), Platform: Platform{OS: "windows", Arch: "amd64"}, LookPath: noLookPath, FileExists: fakeFS(engine, model)}
	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.NeedsProvisioning() || plan.Engine.Source != "cache" || plan.Engine.Path != engine {
		t.Fatalf("expected windows legacy exe cache hit, got %+v", plan.Engine)
	}
}

func TestPlanEngineCacheHitFromPATH(t *testing.T) {
	// Simulate a Homebrew llama-server on PATH while nothing is in the cache.
	brew := "/opt/homebrew/bin/llama-server"
	pr := macProvisioner("/tmp/ajq-cache")
	pr.FileExists = fakeFS()
	pr.LookPath = func(name string) (string, error) {
		if name == EngineBinaryName {
			return brew, nil
		}
		return "", errors.New("not found")
	}

	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !plan.Engine.Present || plan.Engine.Source != "PATH" || plan.Engine.Path != brew {
		t.Fatalf("expected engine PATH hit at %q, got %+v", brew, plan.Engine)
	}
	if plan.Model.Present {
		t.Fatal("model should still be missing")
	}
}

func TestPlanHonorsOverrides(t *testing.T) {
	engineOverride := "/custom/llama-server"
	modelOverride := "/custom/model.gguf"
	pr := macProvisioner("/tmp/ajq-cache")
	pr.EngineOverride = engineOverride
	pr.ModelOverride = modelOverride
	pr.FileExists = fakeFS(engineOverride, modelOverride)

	plan, err := pr.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.NeedsProvisioning() {
		t.Fatalf("expected overrides to satisfy plan: %+v", plan)
	}
	if plan.Engine.Path != engineOverride || plan.Engine.Source != "override" {
		t.Fatalf("engine override not honored: %+v", plan.Engine)
	}
	if plan.Model.Path != modelOverride || plan.Model.Source != "override" {
		t.Fatalf("model override not honored: %+v", plan.Model)
	}
}

func TestPlanUnsupportedPlatformErrors(t *testing.T) {
	pr := &Provisioner{
		Catalog:    DefaultCatalog(),
		Layout:     NewLayout("/tmp/ajq-cache"),
		Platform:   Platform{OS: "plan9", Arch: "mips"},
		LookPath:   noLookPath,
		FileExists: fakeFS(),
	}
	if _, err := pr.Plan(); err == nil {
		t.Fatal("expected unsupported platform error")
	}
}
