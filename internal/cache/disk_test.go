package cache

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
)

func diskTestKey(t *testing.T, modelID string) Key {
	t.Helper()
	key, err := KeyForJudgement(backend.Judgement{
		Op:      "sem_classify",
		Specs:   []string{"keep", "drop"},
		ModelID: modelID,
		Value:   map[string]any{"id": 12, "text": "hello"},
	})
	if err != nil {
		t.Fatalf("key: %v", err)
	}
	return key
}

func TestPersistentStoreHitsAcrossInstancesAndUsesShardedVersionedSchema(t *testing.T) {
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")

	first := NewPersistentStore(cacheDir)
	first.Set(key, backend.Result{Value: "keep"})

	second := NewPersistentStore(cacheDir)
	got, ok := second.Get(key)
	if !ok || got.Value != "keep" {
		t.Fatalf("disk hit = %#v, %v; want keep, true", got, ok)
	}

	path := second.disk.pathFor(key)
	wantDir := filepath.Join(JudgementsDir(cacheDir), hashKey(key)[:2])
	if filepath.Dir(path) != wantDir || filepath.Base(path) != hashKey(key)+".json" {
		t.Fatalf("path = %s, want shard dir %s and hash filename", path, wantDir)
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is derived from the test cache key inside t.TempDir.
	if err != nil {
		t.Fatalf("read entry: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("decode entry: %v", err)
	}
	if raw["ajq_cache_version"] != float64(1) {
		t.Fatalf("version field = %#v, want 1", raw["ajq_cache_version"])
	}
	if _, ok := raw["key_value"]; !ok {
		t.Fatalf("entry missing key_value: %s", data)
	}
	if _, ok := raw["result_value"]; !ok {
		t.Fatalf("entry missing result_value: %s", data)
	}
}

func TestPersistentStoreWritesPrivateDirectoriesAndEntryFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not reliable on Windows")
	}
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")
	store := NewPersistentStore(cacheDir)
	store.Set(key, backend.Result{Value: "keep"})

	for _, tc := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{name: "judgements dir", path: JudgementsDir(cacheDir), want: 0o700},
		{name: "shard dir", path: filepath.Dir(store.disk.pathFor(key)), want: 0o700},
		{name: "entry file", path: store.disk.pathFor(key), want: 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.name, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("%s mode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestPersistentStoreModelIDChangeIsMiss(t *testing.T) {
	cacheDir := t.TempDir()
	NewPersistentStore(cacheDir).Set(diskTestKey(t, "model-a"), backend.Result{Value: true})

	if got, ok := NewPersistentStore(cacheDir).Get(diskTestKey(t, "model-b")); ok {
		t.Fatalf("different model hit = %#v, want miss", got)
	}
}

func TestPersistentStoreInvalidEntriesAreMisses(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(t *testing.T, path string)
	}{
		{
			name: "corrupt",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
					t.Fatalf("write corrupt entry: %v", err)
				}
			},
		},
		{
			name: "wrong-version",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				var entry diskEntry
				data, err := os.ReadFile(path) //nolint:gosec // path is derived from the test cache key inside t.TempDir.
				if err != nil {
					t.Fatalf("read entry: %v", err)
				}
				if err := json.Unmarshal(data, &entry); err != nil {
					t.Fatalf("decode entry: %v", err)
				}
				entry.Version = 99
				data, err = json.Marshal(entry)
				if err != nil {
					t.Fatalf("encode entry: %v", err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatalf("write entry: %v", err)
				}
			},
		},
		{
			name: "wrong-key",
			mutate: func(t *testing.T, path string) {
				t.Helper()
				var entry diskEntry
				data, err := os.ReadFile(path) //nolint:gosec // path is derived from the test cache key inside t.TempDir.
				if err != nil {
					t.Fatalf("read entry: %v", err)
				}
				if err := json.Unmarshal(data, &entry); err != nil {
					t.Fatalf("decode entry: %v", err)
				}
				entry.ModelID = "other-model"
				data, err = json.Marshal(entry)
				if err != nil {
					t.Fatalf("encode entry: %v", err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatalf("write entry: %v", err)
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cacheDir := t.TempDir()
			key := diskTestKey(t, "model-a")
			store := NewPersistentStore(cacheDir)
			store.Set(key, backend.Result{Value: "keep"})
			tc.mutate(t, store.disk.pathFor(key))

			if got, ok := NewPersistentStore(cacheDir).Get(key); ok {
				t.Fatalf("invalid entry hit = %#v, want miss", got)
			}
		})
	}
}

func TestPersistentStoreDiskHitPopulatesMemoryFront(t *testing.T) {
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")
	NewPersistentStore(cacheDir).Set(key, backend.Result{Value: "keep"})

	store := NewPersistentStore(cacheDir)
	if got, ok := store.Get(key); !ok || got.Value != "keep" {
		t.Fatalf("first disk get = %#v, %v", got, ok)
	}
	if err := os.Remove(store.disk.pathFor(key)); err != nil {
		t.Fatalf("remove entry: %v", err)
	}
	if got, ok := store.Get(key); !ok || got.Value != "keep" {
		t.Fatalf("second memory get after disk removal = %#v, %v", got, ok)
	}
}

func TestPersistentStoreConcurrentSameKeyWritesConverge(t *testing.T) {
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")
	store := NewPersistentStore(cacheDir)

	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			store.Set(key, backend.Result{Value: "keep"})
		}()
	}
	wg.Wait()

	got, ok := NewPersistentStore(cacheDir).Get(key)
	if !ok || got.Value != "keep" {
		t.Fatalf("converged get = %#v, %v", got, ok)
	}
	matches, err := filepath.Glob(filepath.Join(JudgementsDir(cacheDir), hashKey(key)[:2], "*.tmp"))
	if err != nil {
		t.Fatalf("glob temp files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover temp files: %v", matches)
	}
}

func TestMemoryOnlyStoreDoesNotAccessDiskAndStillDedupes(t *testing.T) {
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")
	store := NewStore()
	store.Set(key, backend.Result{Value: "keep"})
	if got, ok := store.Get(key); !ok || got.Value != "keep" {
		t.Fatalf("memory get = %#v, %v", got, ok)
	}
	if _, err := os.Stat(JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want not exist", err)
	}
}

func TestPersistentStoreWriteFailureStillPopulatesMemory(t *testing.T) {
	cacheDirFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(cacheDirFile, []byte("file"), 0o600); err != nil {
		t.Fatalf("write cache dir file: %v", err)
	}
	key := diskTestKey(t, "model-a")
	store := NewPersistentStore(cacheDirFile)
	store.Set(key, backend.Result{Value: "keep"})
	if got, ok := store.Get(key); !ok || got.Value != "keep" {
		t.Fatalf("memory get after write failure = %#v, %v", got, ok)
	}
	if got, ok := NewPersistentStore(cacheDirFile).Get(key); ok {
		t.Fatalf("new store unexpectedly hit after write failure: %#v", got)
	}
}

func TestPersistentStoreDoesNotPersistErrorResults(t *testing.T) {
	cacheDir := t.TempDir()
	key := diskTestKey(t, "model-a")
	store := NewPersistentStore(cacheDir)
	store.Set(key, backend.Result{Error: "backend failed"})
	if got, ok := store.Get(key); !ok || got.Error != "backend failed" {
		t.Fatalf("memory error get = %#v, %v", got, ok)
	}
	if got, ok := NewPersistentStore(cacheDir).Get(key); ok {
		t.Fatalf("error result persisted = %#v, want disk miss", got)
	}
}
