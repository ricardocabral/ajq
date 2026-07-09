package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoRoot returns the repository root for tests, even when Go trims runtime
// caller paths via GOFLAGS=-trimpath.
func RepoRoot(t testing.TB) string {
	t.Helper()

	start, err := os.Getwd()
	if err != nil {
		t.Fatalf("get working directory: %v", err)
	}

	dir := start
	for {
		path := filepath.Join(dir, "go.mod")
		if _, err := os.Stat(path); err == nil {
			return dir
		} else if !os.IsNotExist(err) {
			t.Fatalf("stat %s: %v", path, err)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find go.mod from %s", start)
		}
		dir = parent
	}
}
