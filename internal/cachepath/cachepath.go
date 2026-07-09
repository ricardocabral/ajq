// Package cachepath resolves the shared ajq cache root used by provisioning,
// daemon-managed assets, and persistent judgement cache files.
package cachepath

import (
	"os"
	"path/filepath"
	"strings"
)

// EnvCacheDir overrides the cache directory used by ajq cache-backed features.
const EnvCacheDir = "AJQ_CACHE_DIR"

// DefaultCacheDir resolves AJQ_CACHE_DIR, else the OS user cache dir joined
// with "ajq", else a ~/.cache/ajq fallback.
func DefaultCacheDir() string {
	if dir := strings.TrimSpace(os.Getenv(EnvCacheDir)); dir != "" {
		return dir
	}
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "ajq")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".cache", "ajq")
	}
	return filepath.Join(".cache", "ajq")
}
