package testharness

import (
	"path/filepath"
	"testing"
)

func TestVerifyCorpusCanUpdateGoldenExpectations(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixtures.json")
	SaveCorpus(t, path, Corpus{Fixtures: []Fixture{{
		Name:       "update_identity",
		Query:      ".",
		Args:       []string{"-c"},
		Stdin:      "{\"b\":2,\"a\":1}\n",
		WantStdout: "stale\n",
		WantStderr: "stale\n",
		WantExit:   99,
	}}})

	t.Setenv(UpdateEnv, "1")
	updated := VerifyCorpus(t, path)
	if len(updated.Fixtures) != 1 {
		t.Fatalf("fixtures = %d, want 1", len(updated.Fixtures))
	}
	fixture := updated.Fixtures[0]
	if fixture.WantStdout != "{\"a\":1,\"b\":2}\n" || fixture.WantStderr != "" || fixture.WantExit != 0 {
		t.Fatalf("fixture not updated correctly: %+v", fixture)
	}

	reloaded, err := LoadCorpus(path)
	if err != nil {
		t.Fatalf("LoadCorpus(updated): %v", err)
	}
	if reloaded.Fixtures[0].WantStdout != fixture.WantStdout {
		t.Fatalf("updated corpus was not written to disk: %+v", reloaded.Fixtures[0])
	}
}
