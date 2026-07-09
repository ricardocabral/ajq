package provision

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// metadataSchemaVersion versions the on-disk metadata format so future changes
// can be detected and migrated.
const metadataSchemaVersion = 1

// InstalledBundle records the extracted bundle metadata for a provisioned
// multi-file engine archive.
type InstalledBundle struct {
	// Tag is the upstream release tag installed under <cache>/engine/<tag>.
	Tag string `json:"tag"`
	// Root is the absolute extracted bundle root directory.
	Root string `json:"root"`
	// Files are regular files extracted from the archive, relative to Root.
	Files []string `json:"files,omitempty"`
	// BinaryPath is the absolute llama-server executable path.
	BinaryPath string `json:"binary_path"`
	// BinaryRelPath is the slash-separated executable path inside Root.
	BinaryRelPath string `json:"binary_rel_path"`
	// ArchiveSHA256 is the checksum verified over the archive bytes.
	ArchiveSHA256 string `json:"archive_sha256"`
	// ArchiveSize is the verified archive byte size.
	ArchiveSize int64 `json:"archive_size"`
}

// InstalledAsset records a provisioned artifact and the integrity information
// needed to detect corruption or version drift on subsequent runs.
type InstalledAsset struct {
	// Name is the logical asset name (matches Asset.Name).
	Name string `json:"name"`
	// Version is the upstream version string of the installed asset.
	Version string `json:"version,omitempty"`
	// Path is the absolute on-disk location of the installed artifact.
	Path string `json:"path"`
	// SHA256 is the lowercase hex checksum verified at install time.
	SHA256 string `json:"sha256,omitempty"`
	// Size is the byte size of the installed artifact.
	Size int64 `json:"size,omitempty"`
	// InstalledAt is when the artifact was installed (UTC).
	InstalledAt time.Time `json:"installed_at"`
	// Bundle is set for extracted multi-file engine installs.
	Bundle *InstalledBundle `json:"bundle,omitempty"`
}

// Metadata is the persisted record of provisioned assets. It tracks a single
// engine and a set of models keyed by logical name so multiple models can
// coexist (Phase 5 model management extends this without a format change).
type Metadata struct {
	// SchemaVersion is the metadata format version.
	SchemaVersion int `json:"schema_version"`
	// Engine is the installed engine, if any.
	Engine *InstalledAsset `json:"engine,omitempty"`
	// Models maps logical model name to its installed record.
	Models map[string]InstalledAsset `json:"models,omitempty"`
}

// NewMetadata returns an empty, well-formed Metadata.
func NewMetadata() Metadata {
	return Metadata{SchemaVersion: metadataSchemaVersion, Models: map[string]InstalledAsset{}}
}

// LoadMetadata reads and parses the metadata file at path. A missing file
// yields an empty Metadata and no error so callers can treat "never
// provisioned" and "empty record" identically.
func LoadMetadata(path string) (Metadata, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is the provisioning metadata file under the configured cache directory.
	if err != nil {
		if os.IsNotExist(err) {
			return NewMetadata(), nil
		}
		return Metadata{}, fmt.Errorf("read provisioning metadata %q: %w", path, err)
	}
	var m Metadata
	if err := json.Unmarshal(data, &m); err != nil {
		return Metadata{}, fmt.Errorf("parse provisioning metadata %q: %w", path, err)
	}
	if m.Models == nil {
		m.Models = map[string]InstalledAsset{}
	}
	if m.SchemaVersion == 0 {
		m.SchemaVersion = metadataSchemaVersion
	}
	return m, nil
}

// Save writes the metadata to path atomically (temp file + rename), creating
// parent directories as needed.
func (m Metadata) Save(path string) error {
	if m.SchemaVersion == 0 {
		m.SchemaVersion = metadataSchemaVersion
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create metadata dir: %w", err)
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode provisioning metadata: %w", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(filepath.Dir(path), ".provision-*.json")
	if err != nil {
		return fmt.Errorf("create temp metadata: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp metadata: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("close temp metadata: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename metadata into place: %w", err)
	}
	return nil
}

// SetEngine records an installed engine.
func (m *Metadata) SetEngine(a InstalledAsset) {
	m.Engine = &a
}

// SetModel records an installed model keyed by its name.
func (m *Metadata) SetModel(a InstalledAsset) {
	if m.Models == nil {
		m.Models = map[string]InstalledAsset{}
	}
	m.Models[a.Name] = a
}
