package provision

import (
	"path/filepath"
	"strings"

	"github.com/ricardocabral/ajq/internal/cachepath"
)

const (
	// EnvCacheDir overrides the cache directory used for provisioned assets. It
	// mirrors daemon.EnvCacheDir so both packages resolve the same location.
	EnvCacheDir = cachepath.EnvCacheDir

	// binSubdir holds provisioned engine binaries.
	binSubdir = "bin"
	// engineSubdir holds extracted multi-file engine bundles by release tag.
	engineSubdir = "engine"
	// modelsSubdir holds provisioned GGUF models.
	modelsSubdir = "models"
	// tmpSubdir holds in-progress downloads before atomic rename.
	tmpSubdir = "tmp"
	// metadataFilename records installed engine/model versions and checksums.
	metadataFilename = "provision.json"
)

// Layout describes the on-disk cache layout for provisioned assets rooted at a
// single cache directory. All paths are derived deterministically so the
// planner, installer and daemon agree on where assets live.
type Layout struct {
	// CacheDir is the base directory, e.g. ~/.cache/ajq.
	CacheDir string
}

// DefaultCacheDir resolves the cache directory using the same precedence as
// internal/daemon: AJQ_CACHE_DIR, else the OS user cache dir joined with "ajq",
// else a ~/.cache/ajq fallback. Keeping the resolution identical guarantees the
// daemon discovers exactly what the provisioner installs.
func DefaultCacheDir() string { return cachepath.DefaultCacheDir() }

// DefaultLayout returns a Layout rooted at DefaultCacheDir.
func DefaultLayout() Layout {
	return Layout{CacheDir: DefaultCacheDir()}
}

// NewLayout returns a Layout for the given cache directory, falling back to the
// default when empty.
func NewLayout(cacheDir string) Layout {
	if strings.TrimSpace(cacheDir) == "" {
		return DefaultLayout()
	}
	return Layout{CacheDir: cacheDir}
}

// BinDir returns the directory holding provisioned engine binaries.
func (l Layout) BinDir() string { return filepath.Join(l.root(), binSubdir) }

// EngineBinaryPath returns the destination path of the provisioned engine
// binary for the given asset filename. It matches daemon.Config.CacheBinaryPath
// for the default engine name.
func (l Layout) EngineBinaryPath(filename string) string {
	if strings.TrimSpace(filename) == "" {
		filename = EngineBinaryName
	}
	return filepath.Join(l.BinDir(), filename)
}

// EngineBundlesDir returns the directory holding extracted engine bundles.
func (l Layout) EngineBundlesDir() string { return filepath.Join(l.root(), engineSubdir) }

// EngineBundleDir returns the destination directory for one engine release tag.
func (l Layout) EngineBundleDir(tag string) string { return filepath.Join(l.EngineBundlesDir(), tag) }

// EngineBundleBinaryPath returns the binary path inside a bundled engine.
func (l Layout) EngineBundleBinaryPath(tag, rel string) string {
	return filepath.Join(l.EngineBundleDir(tag), filepath.FromSlash(rel))
}

// ModelsDir returns the directory holding provisioned GGUF models.
func (l Layout) ModelsDir() string { return filepath.Join(l.root(), modelsSubdir) }

// ModelPath returns the destination path of a model file by filename.
func (l Layout) ModelPath(filename string) string {
	return filepath.Join(l.ModelsDir(), filename)
}

// TempDir returns the directory for in-progress downloads.
func (l Layout) TempDir() string { return filepath.Join(l.root(), tmpSubdir) }

// MetadataPath returns the path of the provisioning metadata file.
func (l Layout) MetadataPath() string { return filepath.Join(l.root(), metadataFilename) }

// root returns the cache directory, falling back to the default when empty.
func (l Layout) root() string {
	if strings.TrimSpace(l.CacheDir) == "" {
		return DefaultCacheDir()
	}
	return l.CacheDir
}
