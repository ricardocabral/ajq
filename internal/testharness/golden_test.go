package testharness_test

import (
	"path/filepath"
	"testing"

	"github.com/ricardocabral/ajq/internal/testharness"
	"github.com/ricardocabral/ajq/internal/testutil"
)

func TestGoldenCorpus(t *testing.T) {
	testharness.VerifyCorpus(t, filepath.Join(repoRoot(t), "testdata", "golden", "purejq", "fixtures.json"))
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return testutil.RepoRoot(t)
}
