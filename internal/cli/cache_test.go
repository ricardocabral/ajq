package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/backend"
	semanticcache "github.com/ricardocabral/ajq/internal/cache"
)

func isolateCacheCLIEnv(t *testing.T) string {
	t.Helper()
	cacheDir := t.TempDir()
	t.Setenv("AJQ_CACHE_DIR", cacheDir)
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")
	return cacheDir
}

func executeCacheTest(t *testing.T, local backend.Backend, stdin string, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errBuf bytes.Buffer
	err = Execute(context.Background(), Options{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errBuf, LocalBackend: local}, args)
	return out.String(), errBuf.String(), err
}

func TestSemanticCLIRerunHitsPersistentCache(t *testing.T) {
	isolateCacheCLIEnv(t)
	be := &recordingSemanticBackend{}
	args := []string{"--backend", "local", "-c", `.msg =~ "keep"`}
	for i := 0; i < 2; i++ {
		stdout, stderr, err := executeCacheTest(t, be, `{"msg":"please keep this"}`, args...)
		if err != nil {
			t.Fatalf("run %d returned error: %v; stderr=%q", i+1, err, stderr)
		}
		if stdout != "true\n" || stderr != "" {
			t.Fatalf("run %d stdout=%q stderr=%q", i+1, stdout, stderr)
		}
	}
	calls, _ := be.snapshot()
	if calls != 1 {
		t.Fatalf("backend calls after rerun = %d, want 1", calls)
	}
}

func TestSemanticCLINoCacheFlagDisablesDiskReadsAndWrites(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	be := &recordingSemanticBackend{}
	args := []string{"--no-cache", "--backend", "local", "-c", `.msg =~ "keep"`}
	for i := 0; i < 2; i++ {
		stdout, stderr, err := executeCacheTest(t, be, `{"msg":"please keep this"}`, args...)
		if err != nil {
			t.Fatalf("run %d returned error: %v; stderr=%q", i+1, err, stderr)
		}
		if stdout != "true\n" {
			t.Fatalf("run %d stdout=%q, want true", i+1, stdout)
		}
	}
	calls, _ := be.snapshot()
	if calls != 2 {
		t.Fatalf("backend calls with --no-cache = %d, want 2", calls)
	}
	if _, err := os.Stat(semanticcache.JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want not exist", err)
	}
}

func TestSemanticCLINoCacheConfigDisablesDiskReadsAndWrites(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	t.Setenv("AJQ_CONFIG", writeTempConfig(t, "backend = \"local\"\nno_cache = true\n"))
	be := &recordingSemanticBackend{}
	for i := 0; i < 2; i++ {
		stdout, stderr, err := executeCacheTest(t, be, `{"msg":"please keep this"}`, "-c", `.msg =~ "keep"`)
		if err != nil {
			t.Fatalf("run %d returned error: %v; stderr=%q", i+1, err, stderr)
		}
		if stdout != "true\n" {
			t.Fatalf("run %d stdout=%q, want true", i+1, stdout)
		}
	}
	calls, _ := be.snapshot()
	if calls != 2 {
		t.Fatalf("backend calls with no_cache config = %d, want 2", calls)
	}
	if _, err := os.Stat(semanticcache.JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want not exist", err)
	}
}

func TestSemanticCLINoCacheFlagOverridesFalseConfig(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	t.Setenv("AJQ_CONFIG", writeTempConfig(t, "backend = \"local\"\nno_cache = false\n"))
	be := &recordingSemanticBackend{}
	for i := 0; i < 2; i++ {
		stdout, stderr, err := executeCacheTest(t, be, `{"msg":"please keep this"}`, "--no-cache", "-c", `.msg =~ "keep"`)
		if err != nil {
			t.Fatalf("run %d returned error: %v; stderr=%q", i+1, err, stderr)
		}
		if stdout != "true\n" {
			t.Fatalf("run %d stdout=%q, want true", i+1, stdout)
		}
	}
	calls, _ := be.snapshot()
	if calls != 2 {
		t.Fatalf("backend calls with --no-cache over false config = %d, want 2", calls)
	}
	if _, err := os.Stat(semanticcache.JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want not exist", err)
	}
}

func TestCacheStatusAndClearCommands(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	store := semanticcache.NewPersistentStore(cacheDir)
	keyA, err := semanticcache.KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"keep"}, ModelID: "model-a", Value: "a"})
	if err != nil {
		t.Fatalf("key A: %v", err)
	}
	keyB, err := semanticcache.KeyForJudgement(backend.Judgement{Op: "sem_match", Specs: []string{"keep"}, ModelID: "model-a", Value: "b"})
	if err != nil {
		t.Fatalf("key B: %v", err)
	}
	store.Set(keyA, backend.Result{Value: true})
	store.Set(keyB, backend.Result{Value: false})
	assetPath := filepath.Join(cacheDir, "models", "keep.gguf")
	if err := os.MkdirAll(filepath.Dir(assetPath), 0o700); err != nil {
		t.Fatalf("create asset dir: %v", err)
	}
	if err := os.WriteFile(assetPath, []byte("model"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}

	stdout, stderr, err := executeCacheTest(t, nil, "", "cache", "status")
	if err != nil {
		t.Fatalf("cache status returned error: %v; stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "location: "+semanticcache.JudgementsDir(cacheDir)) || !strings.Contains(stdout, "entries: 2\n") || !strings.Contains(stdout, "bytes: ") {
		t.Fatalf("unexpected status output: %q", stdout)
	}

	stdout, stderr, err = executeCacheTest(t, nil, "", "cache", "clear")
	if err != nil {
		t.Fatalf("cache clear returned error: %v; stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "location: "+semanticcache.JudgementsDir(cacheDir)) || !strings.Contains(stdout, "cleared_entries: 2\n") || !strings.Contains(stdout, "freed_bytes: ") {
		t.Fatalf("unexpected clear output: %q", stdout)
	}
	if _, err := os.Stat(semanticcache.JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want removed", err)
	}
	if _, err := os.Stat(assetPath); err != nil {
		t.Fatalf("non-judgement cache asset removed or inaccessible: %v", err)
	}

	stdout, stderr, err = executeCacheTest(t, nil, "", "cache", "status")
	if err != nil {
		t.Fatalf("cache status after clear returned error: %v; stderr=%q", err, stderr)
	}
	if !strings.Contains(stdout, "entries: 0\n") || !strings.Contains(stdout, "bytes: 0\n") {
		t.Fatalf("status after clear = %q, want zero", stdout)
	}
}

func TestCacheStatusJSONContract(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	stdout, stderr, err := executeCacheTest(t, nil, "", "cache", "status", "--json")
	if err != nil || stderr != "" {
		t.Fatalf("empty cache status JSON = (%v, %q)", err, stderr)
	}
	want := `{"schema_version":"1","availability":"available","path":"` + semanticcache.JudgementsDir(cacheDir) + `","entries":0,"bytes":0}` + "\n"
	if stdout != want {
		t.Fatalf("empty cache status JSON = %q, want %q", stdout, want)
	}
	var available struct {
		Availability string `json:"availability"`
		Path         string `json:"path"`
		Entries      int    `json:"entries"`
		Bytes        int64  `json:"bytes"`
	}
	if err := json.Unmarshal([]byte(stdout), &available); err != nil || available.Availability != "available" || available.Entries != 0 || available.Bytes != 0 {
		t.Fatalf("decode available cache JSON = (%+v, %v)", available, err)
	}

	location := semanticcache.JudgementsDir(cacheDir)
	if err := os.WriteFile(location, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	stdout, stderr, err = executeCacheTest(t, nil, "", "cache", "status", "--json")
	if err == nil || ExitCode(err) != 1 || stderr != "" {
		t.Fatalf("unavailable cache status JSON = (%v, %q)", err, stderr)
	}
	want = `{"schema_version":"1","availability":"unavailable","path":"` + location + `","entries":0,"bytes":0,"error":"status_unavailable"}` + "\n"
	if stdout != want {
		t.Fatalf("unavailable cache JSON = %q, want %q", stdout, want)
	}
	var unavailable map[string]any
	if err := json.Unmarshal([]byte(stdout), &unavailable); err != nil || unavailable["error"] != "status_unavailable" {
		t.Fatalf("decode unavailable cache JSON = (%v, %v)", unavailable, err)
	}
}

func TestPureJQDoesNotTouchCachePath(t *testing.T) {
	cacheDir := isolateCacheCLIEnv(t)
	be := &recordingSemanticBackend{}
	stdout, stderr, err := executeCacheTest(t, be, `{"a":42}`, "--backend", "local", "-c", ".a")
	if err != nil {
		t.Fatalf("pure jq returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "42\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
	calls, _ := be.snapshot()
	if calls != 0 {
		t.Fatalf("backend calls = %d, want 0", calls)
	}
	if _, err := os.Stat(semanticcache.JudgementsDir(cacheDir)); !os.IsNotExist(err) {
		t.Fatalf("judgements dir stat err = %v, want not exist", err)
	}
}
