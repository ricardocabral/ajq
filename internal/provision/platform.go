// Package provision implements first-run provisioning of local inference
// assets for ajq: it locates or downloads a platform-appropriate llama-server
// engine and a default GGUF model into the ajq cache directory, verifies their
// integrity, and reports progress.
//
// The package is deliberately decoupled from process lifecycle (owned by
// internal/daemon) and from cobra (progress is reported via a plain callback
// type). All external effects (filesystem checks, PATH lookups, HTTP) are
// injectable so automated tests remain offline and deterministic.
package provision

import (
	"runtime"
	"strings"
)

// Platform identifies an operating-system/architecture target used to select
// the correct prebuilt engine asset. Values mirror Go's runtime.GOOS and
// runtime.GOARCH so selection is stable across builds.
type Platform struct {
	// OS is the operating system, e.g. "darwin", "linux", "windows".
	OS string
	// Arch is the CPU architecture, e.g. "arm64", "amd64".
	Arch string
}

// CurrentPlatform returns the Platform of the running binary.
func CurrentPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Key returns a stable "os/arch" identifier used as a catalog map key.
func (p Platform) Key() string {
	return strings.ToLower(p.OS) + "/" + strings.ToLower(p.Arch)
}

// String returns a human-readable platform label.
func (p Platform) String() string {
	return p.Key()
}
