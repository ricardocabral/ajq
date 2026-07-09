package provision

import (
	"fmt"
	"sort"
	"strings"
)

// Kind classifies a provisionable asset.
type Kind string

const (
	// KindEngine is the llama-server inference engine binary.
	KindEngine Kind = "engine"
	// KindModel is a GGUF model file.
	KindModel Kind = "model"
)

// EngineBinaryName is the executable name of the inference engine. It matches
// the name discovered on PATH and expected under the cache bin directory.
const EngineBinaryName = "llama-server"

// DefaultModelName is the short catalog name for the default local model.
const DefaultModelName = "qwen2.5-1.5b"

// DefaultModelFilename is the on-disk filename of the default GGUF model. It is
// chosen to match the model already present in the ajq cache so a first run on
// a machine that already has it is a cache hit rather than a redundant
// download. See docs/IMPLEMENTATION_PLAN.md §6.3 (Qwen2.5-1.5B default).
const DefaultModelFilename = "qwen2.5-1.5b-instruct-q5_k_m.gguf"

// Default model distribution metadata. The URL is pinned to an immutable
// Hugging Face commit (not a mutable branch like `main`) so the bytes behind it
// cannot change under the pinned SHA-256 — a supply-chain safety requirement.
// Source repo: https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF
const (
	// defaultModelURL is the commit-pinned download source for the default GGUF
	// model.
	defaultModelURL = "https://huggingface.co/Qwen/Qwen2.5-1.5B-Instruct-GGUF/resolve/91cad51170dc346986eccefdc2dd33a9da36ead9/qwen2.5-1.5b-instruct-q5_k_m.gguf"
	// defaultModelSHA256 is the lowercase hex SHA-256 of the pinned model bytes.
	defaultModelSHA256 = "b46661073c18e5b56a41fa320975f866a00def1ff08feef4718e013258896f8c"
	// defaultModelSize is the expected byte size of the pinned model file, used
	// for progress totals when the server omits Content-Length.
	defaultModelSize = 1285494304

	qwen25ThreeBURL    = "https://huggingface.co/Qwen/Qwen2.5-3B-Instruct-GGUF/resolve/7dabda4d13d513e3e842b20f0d435c732f172cbe/qwen2.5-3b-instruct-q4_k_m.gguf"
	qwen25ThreeBSHA256 = "626b4a6678b86442240e33df819e00132d3ba7dddfe1cdc4fbb18e0a9615c62d"
	qwen25ThreeBSize   = 2104932768

	qwen3FourBURL    = "https://huggingface.co/Qwen/Qwen3-4B-GGUF/resolve/bc640142c66e1fdd12af0bd68f40445458f3869b/Qwen3-4B-Q4_K_M.gguf"
	qwen3FourBSHA256 = "7485fe6f11af29433bc51cab58009521f205840f5b4ae3a32fa7f92e8534fdf5"
	qwen3FourBSize   = 2497280256
)

// Asset describes a single downloadable/installable artifact together with the
// integrity metadata needed to verify it. URL and SHA256 are empty for entries
// that are only ever satisfied by locally installed assets (e.g. in tests, or
// until real distribution endpoints exist); Install refuses to download an
// asset that lacks a URL or checksum.
type Asset struct {
	// Kind is engine or model.
	Kind Kind `json:"kind"`
	// Name is the logical asset name (without path), e.g. "llama-server" or the
	// model identity. Used in progress reporting and metadata keys.
	Name string `json:"name"`
	// Version is the upstream version string of the asset, recorded in metadata.
	Version string `json:"version"`
	// Filename is the destination filename within its cache subdirectory.
	Filename string `json:"filename"`
	// URL is the download source. Empty means the asset can only be satisfied
	// by an already-installed local copy.
	URL string `json:"url,omitempty"`
	// SHA256 is the lowercase hex-encoded expected checksum of the downloaded
	// bytes. Empty means no download is possible (verification cannot pass).
	SHA256 string `json:"sha256,omitempty"`
	// Size is the expected byte size, used for progress totals when the server
	// does not report Content-Length. For bundled engines this is the archive
	// byte size, not the extracted binary size. Zero means unknown.
	Size int64 `json:"size,omitempty"`
	// ReleaseTag names the upstream release tag for bundled engines.
	ReleaseTag string `json:"release_tag,omitempty"`
	// ArchiveFormat is "tar.gz" or "zip" for bundled engines.
	ArchiveFormat string `json:"archive_format,omitempty"`
	// BinaryPath is the slash-separated path to llama-server inside an extracted
	// engine bundle.
	BinaryPath string `json:"binary_path,omitempty"`
}

// Model describes a named, checksum-pinned GGUF catalog entry.
type Model struct {
	// Name is the stable short name users pass to --model / ajq models.
	Name string
	// Asset is the downloadable GGUF artifact. Asset.Name matches Name so
	// metadata keys, progress, and list output are consistent.
	Asset Asset
	// RAMNote is the user-facing approximate memory guidance for the model.
	RAMNote string
}

// Downloadable reports whether the asset carries the URL and checksum required
// to perform a verified download.
func (a Asset) Downloadable() bool {
	return a.URL != "" && a.SHA256 != ""
}

// BundleDownloadable reports whether the engine asset has all metadata needed
// to download, verify, extract and locate a multi-file release bundle.
func (a Asset) BundleDownloadable() bool {
	return a.Kind == KindEngine && a.Downloadable() && a.ReleaseTag != "" && a.ArchiveFormat != "" && a.BinaryPath != ""
}

// Catalog maps platforms to their engine asset and names the model catalog. It
// is intentionally data-only so new platforms/models are added by extending the
// maps rather than changing selection logic.
type Catalog struct {
	// Engines is keyed by Platform.Key() ("os/arch").
	Engines map[string]Asset
	// Models is keyed by stable short catalog name.
	Models map[string]Model
	// Model is a legacy single-model field accepted for tests/custom catalogs
	// that have not yet been converted to Models.
	Model Asset
}

// DefaultCatalog returns the built-in asset catalog.
//
// Models carry real, checksum-pinned public download URLs, so a fresh machine
// can provision selected GGUF models over the network with verified integrity
// (and a machine that already has one is a cache hit — no download).
//
// Engine: supported platforms carry llama.cpp release archives whose SHA-256
// and size refer to the archive bytes. Platforms without pinned archive
// metadata remain local-only so a missing engine yields the manual-install
// escape hatch rather than an unverified download. Tests inject their own
// catalog with fake URLs/checksums.
func DefaultCatalog() Catalog {
	const releaseTag = "b9917"
	engine := func(assetName, url, sha string, size int64) Asset {
		return Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: assetName, Version: releaseTag, URL: url, SHA256: sha, Size: size, ReleaseTag: releaseTag, ArchiveFormat: "tar.gz", BinaryPath: "llama-b9917/" + EngineBinaryName}
	}
	localOnly := func() Asset {
		return Asset{Kind: KindEngine, Name: EngineBinaryName, Filename: EngineBinaryName}
	}
	models := map[string]Model{
		DefaultModelName: modelEntry(DefaultModelName, "q5_k_m", DefaultModelFilename, defaultModelURL, defaultModelSHA256, defaultModelSize, "~4 GiB RAM"),
		"qwen2.5-3b":     modelEntry("qwen2.5-3b", "q4_k_m", "qwen2.5-3b-instruct-q4_k_m.gguf", qwen25ThreeBURL, qwen25ThreeBSHA256, qwen25ThreeBSize, "~6 GiB RAM"),
		"qwen3-4b":       modelEntry("qwen3-4b", "q4_k_m", "Qwen3-4B-Q4_K_M.gguf", qwen3FourBURL, qwen3FourBSHA256, qwen3FourBSize, "~8 GiB RAM"),
	}
	return Catalog{
		Engines: map[string]Asset{
			"darwin/arm64":  engine("llama-b9917-bin-macos-arm64.tar.gz", "https://github.com/ggml-org/llama.cpp/releases/download/b9917/llama-b9917-bin-macos-arm64.tar.gz", "050882d6348d82e11b1bd3537e06fd048ae2a03d419a7537bffcdb19358b1b78", 11139217),
			"darwin/amd64":  localOnly(),
			"linux/amd64":   engine("llama-b9917-bin-ubuntu-x64.tar.gz", "https://github.com/ggml-org/llama.cpp/releases/download/b9917/llama-b9917-bin-ubuntu-x64.tar.gz", "d66094fcbc134a8472fc9a96d27355a12bae78bbe43f0022fcac8e8129495dcc", 15890319),
			"linux/arm64":   engine("llama-b9917-bin-ubuntu-arm64.tar.gz", "https://github.com/ggml-org/llama.cpp/releases/download/b9917/llama-b9917-bin-ubuntu-arm64.tar.gz", "341bb0cc784d5153fc71618c5e766b62e0723b1774b3804116944eeeb2a10840", 12883405),
			"windows/amd64": {Kind: KindEngine, Name: EngineBinaryName, Filename: EngineBinaryName + ".exe"},
		},
		Models: models,
		Model:  models[DefaultModelName].Asset,
	}
}

func modelEntry(name, version, filename, url, sha string, size int64, ram string) Model {
	return Model{
		Name: name,
		Asset: Asset{
			Kind:     KindModel,
			Name:     name,
			Version:  version,
			Filename: filename,
			URL:      url,
			SHA256:   sha,
			Size:     size,
		},
		RAMNote: ram,
	}
}

// EngineFor returns the engine asset for the given platform, or an error naming
// the unsupported platform.
func (c Catalog) EngineFor(p Platform) (Asset, error) {
	asset, ok := c.Engines[p.Key()]
	if !ok {
		return Asset{}, fmt.Errorf("no prebuilt %s engine available for platform %s", EngineBinaryName, p.Key())
	}
	return asset, nil
}

// DefaultModel returns the default catalog model.
func (c Catalog) DefaultModel() (Model, error) {
	return c.ModelFor("")
}

// ModelFor resolves a user-requested model name. An empty name selects the
// default. Unknown names return a deterministic error listing valid catalog
// names.
func (c Catalog) ModelFor(name string) (Model, error) {
	requested := strings.TrimSpace(name)
	if requested == "" {
		requested = DefaultModelName
	}
	if len(c.Models) > 0 {
		model, ok := c.Models[requested]
		if !ok {
			return Model{}, fmt.Errorf("unknown model %q: valid models are %s", requested, strings.Join(quoteAll(c.ModelNames()), ", "))
		}
		if model.Name == "" {
			model.Name = requested
		}
		if model.Asset.Name == "" {
			model.Asset.Name = model.Name
		}
		return model, nil
	}
	if c.Model.Name != "" && (name == "" || requested == c.Model.Name || requested == DefaultModelName) {
		legacyName := c.Model.Name
		if legacyName == "" {
			legacyName = DefaultModelName
		}
		return Model{Name: legacyName, Asset: c.Model}, nil
	}
	return Model{}, fmt.Errorf("unknown model %q: valid models are %s", requested, strings.Join(quoteAll(c.ModelNames()), ", "))
}

// ModelNames returns the stable sorted list of catalog model names.
func (c Catalog) ModelNames() []string {
	if len(c.Models) == 0 {
		if c.Model.Name == "" {
			return nil
		}
		return []string{c.Model.Name}
	}
	names := make([]string, 0, len(c.Models))
	for name := range c.Models {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ModelsList returns catalog models in deterministic name order.
func (c Catalog) ModelsList() []Model {
	names := c.ModelNames()
	out := make([]Model, 0, len(names))
	for _, name := range names {
		if model, err := c.ModelFor(name); err == nil {
			out = append(out, model)
		}
	}
	return out
}

func quoteAll(items []string) []string {
	quoted := make([]string, 0, len(items))
	for _, item := range items {
		quoted = append(quoted, fmt.Sprintf("%q", item))
	}
	return quoted
}
