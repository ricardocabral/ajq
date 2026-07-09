package provision

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// FileExistsFunc reports whether path names an existing, non-directory file.
type FileExistsFunc func(path string) bool

// LookPathFunc resolves an executable name via PATH, mirroring exec.LookPath.
type LookPathFunc func(name string) (string, error)

// defaultFileExists is the production FileExists implementation.
func defaultFileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// AssetStatus describes the resolved state of one asset for a provisioning run.
type AssetStatus struct {
	// Asset is the catalog entry this status refers to.
	Asset Asset
	// Present is true when a usable copy already exists (cache hit).
	Present bool
	// Path is where the asset is (when Present) or will be installed (when not).
	Path string
	// Source is a human-readable description of how a present asset was found
	// (e.g. "cache", "PATH", "override"), empty when not present.
	Source string
	// Reason explains why installation is needed when Present is false.
	Reason string
}

// Plan is the resolved provisioning state for the current platform: which
// assets are already available and which must be downloaded/installed.
type Plan struct {
	// Platform is the target the plan was resolved for.
	Platform Platform
	// Layout is the cache layout used to resolve destinations.
	Layout Layout
	// Engine is the engine asset status.
	Engine AssetStatus
	// Model is the requested model asset status.
	Model AssetStatus
}

// NeedsProvisioning reports whether any required asset is missing.
func (p Plan) NeedsProvisioning() bool {
	return !p.Engine.Present || !p.Model.Present
}

// Missing returns the asset statuses that still require installation.
func (p Plan) Missing() []AssetStatus {
	var out []AssetStatus
	if !p.Engine.Present {
		out = append(out, p.Engine)
	}
	if !p.Model.Present {
		out = append(out, p.Model)
	}
	return out
}

// Provisioner resolves and (in Install) fulfills provisioning requirements. All
// external effects are injectable so tests stay offline and deterministic.
type Provisioner struct {
	// Catalog selects assets per platform. Defaults to DefaultCatalog.
	Catalog Catalog
	// Layout is the on-disk cache layout. Defaults to DefaultLayout.
	Layout Layout
	// Platform is the target platform. Defaults to CurrentPlatform.
	Platform Platform
	// FileExists checks for installed files. Defaults to a filesystem stat.
	FileExists FileExistsFunc
	// LookPath resolves the engine on PATH (e.g. a Homebrew install). Defaults
	// to exec.LookPath. Set to a no-op to disable PATH discovery in tests.
	LookPath LookPathFunc
	// HTTPClient issues download requests in Install. Defaults to a bounded
	// client. Tests inject an httptest-backed client.
	HTTPClient *http.Client

	// EngineOverride, when set and present on disk, is treated as a cache hit
	// for the engine (e.g. an explicit AJQ_LLAMA_SERVER path).
	EngineOverride string
	// ModelOverride, when set and present on disk, is treated as a cache hit
	// for the model.
	ModelOverride string
}

// New returns a Provisioner with production defaults for the current platform.
func New() *Provisioner {
	return &Provisioner{
		Catalog:  DefaultCatalog(),
		Layout:   DefaultLayout(),
		Platform: CurrentPlatform(),
	}
}

// resolveSeams fills in default implementations for unset injectable fields.
func (pr *Provisioner) resolveSeams() (Catalog, Layout, Platform, FileExistsFunc, LookPathFunc) {
	catalog := pr.Catalog
	if catalog.Engines == nil && len(catalog.Models) == 0 && catalog.Model.Name == "" {
		catalog = DefaultCatalog()
	}
	layout := pr.Layout
	if strings.TrimSpace(layout.CacheDir) == "" {
		layout = DefaultLayout()
	}
	platform := pr.Platform
	if platform.OS == "" && platform.Arch == "" {
		platform = CurrentPlatform()
	}
	fileExists := pr.FileExists
	if fileExists == nil {
		fileExists = defaultFileExists
	}
	lookPath := pr.LookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	return catalog, layout, platform, fileExists, lookPath
}

// Plan resolves the current provisioning state for the default model without
// performing any installation.
func (pr *Provisioner) Plan() (Plan, error) {
	return pr.PlanModel("")
}

// PlanModel resolves the current provisioning state for a requested catalog
// model without performing any installation. Engine discovery precedence:
// explicit override → cached bundle → legacy cache bin location → PATH (e.g.
// Homebrew). Model discovery precedence: explicit override → cache models
// location.
func (pr *Provisioner) PlanModel(name string) (Plan, error) {
	catalog, layout, platform, fileExists, lookPath := pr.resolveSeams()

	engineAsset, err := catalog.EngineFor(platform)
	if err != nil {
		return Plan{}, err
	}
	model, err := catalog.ModelFor(name)
	if err != nil {
		return Plan{}, err
	}

	engineDest := destinationFor(layout, engineAsset)
	engineStatus := AssetStatus{Asset: engineAsset, Path: engineDest}
	metadata, metadataErr := LoadMetadata(layout.MetadataPath())
	switch {
	case pr.EngineOverride != "" && fileExists(pr.EngineOverride):
		engineStatus.Present = true
		engineStatus.Path = pr.EngineOverride
		engineStatus.Source = "override"
	case metadataErr == nil && cachedBundleBinary(layout, engineAsset, metadata.Engine) != "":
		engineStatus.Present = true
		engineStatus.Path = cachedBundleBinary(layout, engineAsset, metadata.Engine)
		engineStatus.Source = "bundle"
	case fileExists(legacyEngineBinaryPath(layout, engineAsset)):
		engineStatus.Present = true
		engineStatus.Path = legacyEngineBinaryPath(layout, engineAsset)
		engineStatus.Source = "cache"
	default:
		if p, err := lookPath(EngineBinaryName); err == nil && strings.TrimSpace(p) != "" {
			engineStatus.Present = true
			engineStatus.Path = p
			engineStatus.Source = "PATH"
		} else {
			engineStatus.Reason = fmt.Sprintf("no %s found in override, bundle %q, legacy cache %q, or PATH", EngineBinaryName, engineDest, legacyEngineBinaryPath(layout, engineAsset))
		}
	}

	modelAsset := model.Asset
	modelDest := layout.ModelPath(modelAsset.Filename)
	modelStatus := AssetStatus{Asset: modelAsset, Path: modelDest}
	switch {
	case pr.ModelOverride != "" && fileExists(pr.ModelOverride):
		modelStatus.Present = true
		modelStatus.Path = pr.ModelOverride
		modelStatus.Source = "override"
	case fileExists(modelDest):
		modelStatus.Present = true
		modelStatus.Source = "cache"
	default:
		modelStatus.Reason = fmt.Sprintf("model %q not found at %q", modelAsset.Filename, modelDest)
	}

	return Plan{
		Platform: platform,
		Layout:   layout,
		Engine:   engineStatus,
		Model:    modelStatus,
	}, nil
}

// PlanModelOnly resolves only a requested catalog model without inspecting the
// engine. It lets model-management commands list, pull, and use models on
// systems where the engine is not yet installed or the platform is unsupported.
func (pr *Provisioner) PlanModelOnly(name string) (Plan, error) {
	catalog, layout, platform, fileExists, _ := pr.resolveSeams()
	model, err := catalog.ModelFor(name)
	if err != nil {
		return Plan{}, err
	}
	modelAsset := model.Asset
	modelDest := layout.ModelPath(modelAsset.Filename)
	modelStatus := AssetStatus{Asset: modelAsset, Path: modelDest}
	switch {
	case pr.ModelOverride != "" && fileExists(pr.ModelOverride):
		modelStatus.Present = true
		modelStatus.Path = pr.ModelOverride
		modelStatus.Source = "override"
	case fileExists(modelDest):
		modelStatus.Present = true
		modelStatus.Source = "cache"
	default:
		modelStatus.Reason = fmt.Sprintf("model %q not found at %q", modelAsset.Filename, modelDest)
	}
	return Plan{Platform: platform, Layout: layout, Model: modelStatus}, nil
}

// ModelCatalog returns the catalog resolved for this provisioner. CLI model
// commands use it for deterministic list output while tests can inject custom
// catalogs.
func (pr *Provisioner) ModelCatalog() Catalog {
	catalog, _, _, _, _ := pr.resolveSeams()
	return catalog
}

// CachedEngineBundleBinaryPath returns the currently valid default-catalog
// bundle binary path for cacheDir/current platform, if one is recorded and still
// matches the catalog. It is used by daemon discovery to avoid duplicating
// bundle tag/path constants.
func CachedEngineBundleBinaryPath(cacheDir string) (string, bool) {
	layout := NewLayout(cacheDir)
	catalog := DefaultCatalog()
	asset, err := catalog.EngineFor(CurrentPlatform())
	if err != nil {
		return "", false
	}
	metadata, err := LoadMetadata(layout.MetadataPath())
	if err != nil {
		return "", false
	}
	p := cachedBundleBinary(layout, asset, metadata.Engine)
	return p, p != ""
}

func legacyEngineBinaryPath(layout Layout, asset Asset) string {
	if asset.Filename != "" && !asset.BundleDownloadable() {
		return layout.EngineBinaryPath(asset.Filename)
	}
	return layout.EngineBinaryPath(EngineBinaryName)
}

func cachedBundleBinary(layout Layout, asset Asset, installed *InstalledAsset) string {
	if !asset.BundleDownloadable() || installed == nil || installed.Bundle == nil {
		return ""
	}
	bundle := installed.Bundle
	if bundle.Tag != asset.ReleaseTag || bundle.ArchiveSHA256 != asset.SHA256 || bundle.ArchiveSize != asset.Size {
		return ""
	}
	expectedRoot := layout.EngineBundleDir(asset.ReleaseTag)
	if filepath.Clean(bundle.Root) != filepath.Clean(expectedRoot) {
		return ""
	}
	if bundle.BinaryRelPath != asset.BinaryPath {
		return ""
	}
	binary := layout.EngineBundleBinaryPath(asset.ReleaseTag, asset.BinaryPath)
	if filepath.Clean(bundle.BinaryPath) != filepath.Clean(binary) {
		return ""
	}
	if !pathWithin(expectedRoot, binary) || !executableFile(binary) {
		return ""
	}
	return binary
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

func executableFile(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	if runtime.GOOS == "windows" {
		return true
	}
	return info.Mode().Perm()&0o111 != 0
}
