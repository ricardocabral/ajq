package cache

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/ricardocabral/ajq/internal/backend"
	"github.com/ricardocabral/ajq/internal/cachepath"
)

const (
	diskCacheVersion = 1
	judgementsDir    = "judgements"
)

type diskStore struct {
	root string
}

type canonicalKeyPayload struct {
	Op      string          `json:"op"`
	Specs   []string        `json:"spec"`
	ModelID string          `json:"model_id"`
	Value   json.RawMessage `json:"value"`
}

type diskEntry struct {
	Version     int             `json:"ajq_cache_version"`
	Op          string          `json:"op"`
	Specs       []string        `json:"spec"`
	ModelID     string          `json:"model_id"`
	KeyValue    json.RawMessage `json:"key_value"`
	ResultValue json.RawMessage `json:"result_value"`
	CreatedAt   time.Time       `json:"created_at"`
}

// Stats summarizes files currently under a persistent judgement cache.
type Stats struct {
	Location string
	Entries  int
	Bytes    int64
}

// DefaultCacheDir returns the cache root used for persistent judgements. It is
// the same source of truth used by provisioning and daemon code.
func DefaultCacheDir() string { return cachepath.DefaultCacheDir() }

// JudgementsDir returns the directory that contains persistent judgement cache
// entries for cacheDir, falling back to the default cache root when empty.
func JudgementsDir(cacheDir string) string {
	if cacheDir == "" {
		cacheDir = cachepath.DefaultCacheDir()
	}
	return filepath.Join(cacheDir, judgementsDir)
}

// NewPersistentStore creates a Store with memory in front of the persistent
// on-disk judgement cache rooted at cacheDir. An empty cacheDir uses the default
// ajq cache directory.
func NewPersistentStore(cacheDir string) *Store {
	return &Store{results: make(map[Key]backend.Result), disk: &diskStore{root: JudgementsDir(cacheDir)}}
}

// NewDefaultPersistentStore creates a persistent Store rooted at DefaultCacheDir.
func NewDefaultPersistentStore() *Store { return NewPersistentStore("") }

// Status returns the count and total size of regular files under judgements/.
// A missing directory is a valid empty cache; other stat/read errors are
// returned to callers so management commands can report them.
func Status(cacheDir string) (Stats, error) {
	location := JudgementsDir(cacheDir)
	stats := Stats{Location: location}
	if _, err := os.Stat(location); err != nil {
		if os.IsNotExist(err) {
			return stats, nil
		}
		return stats, fmt.Errorf("stat judgement cache: %w", err)
	}
	if err := filepath.WalkDir(location, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || entry.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			stats.Entries++
			stats.Bytes += info.Size()
		}
		return nil
	}); err != nil {
		return stats, fmt.Errorf("walk judgement cache: %w", err)
	}
	return stats, nil
}

// Clear removes only the persistent judgement cache directory and returns the
// entries/bytes that were present before removal. A missing directory is a
// successful no-op.
func Clear(cacheDir string) (Stats, error) {
	stats, err := Status(cacheDir)
	if err != nil {
		return stats, err
	}
	if err := os.RemoveAll(stats.Location); err != nil {
		return stats, fmt.Errorf("clear judgement cache: %w", err)
	}
	return stats, nil
}

func (d *diskStore) get(key Key) (backend.Result, bool) {
	path := d.pathFor(key)
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from a canonical cache key under the configured cache directory.
	if err != nil {
		return backend.Result{}, false
	}
	var entry diskEntry
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	if err := dec.Decode(&entry); err != nil {
		return backend.Result{}, false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return backend.Result{}, false
	}
	if entry.Version != diskCacheVersion || len(entry.ResultValue) == 0 {
		return backend.Result{}, false
	}
	if !entry.matches(key) {
		return backend.Result{}, false
	}
	var value any
	dec = json.NewDecoder(bytes.NewReader(entry.ResultValue))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return backend.Result{}, false
	}
	return backend.Result{Value: value}, true
}

func (d *diskStore) set(key Key, result backend.Result) {
	payload, ok := parseCanonicalKey(key)
	if !ok {
		return
	}
	resultValue, err := json.Marshal(result.Value)
	if err != nil {
		return
	}
	entry := diskEntry{
		Version:     diskCacheVersion,
		Op:          payload.Op,
		Specs:       append([]string(nil), payload.Specs...),
		ModelID:     payload.ModelID,
		KeyValue:    append(json.RawMessage(nil), payload.Value...),
		ResultValue: resultValue,
		CreatedAt:   time.Now().UTC(),
	}
	encoded, err := json.MarshalIndent(entry, "", "  ")
	if err != nil {
		return
	}
	encoded = append(encoded, '\n')

	path := d.pathFor(key)
	shardDir := filepath.Dir(path)
	if err := os.MkdirAll(shardDir, 0o700); err != nil {
		return
	}
	tmp, err := os.CreateTemp(shardDir, "."+filepath.Base(path)+"-*.tmp")
	if err != nil {
		return
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		_ = tmp.Close()
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(encoded); err != nil {
		return
	}
	if err := tmp.Sync(); err != nil {
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	if err := os.Rename(tmpName, path); err != nil {
		return
	}
	cleanup = false
}

func (entry diskEntry) matches(key Key) bool {
	payload := canonicalKeyPayload{
		Op:      entry.Op,
		Specs:   append([]string(nil), entry.Specs...),
		ModelID: entry.ModelID,
		Value:   entry.KeyValue,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false
	}
	return bytes.Equal(encoded, []byte(key))
}

func (d *diskStore) pathFor(key Key) string {
	hash := hashKey(key)
	return filepath.Join(d.root, hash[:2], hash+".json")
}

func hashKey(key Key) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func parseCanonicalKey(key Key) (canonicalKeyPayload, bool) {
	var payload canonicalKeyPayload
	dec := json.NewDecoder(bytes.NewReader([]byte(key)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		return canonicalKeyPayload{}, false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		return canonicalKeyPayload{}, false
	}
	if payload.Op == "" || payload.ModelID == "" || len(payload.Value) == 0 {
		return canonicalKeyPayload{}, false
	}
	encoded, err := json.Marshal(payload)
	if err != nil || string(encoded) != string(key) {
		return canonicalKeyPayload{}, false
	}
	return payload, true
}

func (d *diskStore) String() string {
	return fmt.Sprintf("diskStore(%s)", d.root)
}
