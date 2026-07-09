package plan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/ricardocabral/ajq/internal/testutil"
)

type gatedCorpus struct {
	Fixtures []gatedFixture `json:"fixtures"`
}

type gatedFixture struct {
	Name      string `json:"name"`
	Query     string `json:"query"`
	WantGated bool   `json:"want_gated"`
	WantMode  string `json:"want_mode"`
}

func TestGatedValueOpCorpus(t *testing.T) {
	for _, fixture := range loadGatedCorpus(t).Fixtures {
		fixture := fixture
		t.Run(fixture.Name, func(t *testing.T) {
			plan, diagnostics := Build(fixture.Query)
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == SeverityError {
					t.Fatalf("Build(%q) diagnostic: %s", fixture.Query, diagnostic.Message)
				}
			}
			if len(plan.Semantic) != 1 {
				t.Fatalf("Build(%q) semantic nodes = %#v, want exactly one", fixture.Query, plan.Semantic)
			}
			node := plan.Semantic[0]
			if node.Gated != fixture.WantGated {
				t.Fatalf("Build(%q) gated = %v, want %v; node=%#v", fixture.Query, node.Gated, fixture.WantGated, node)
			}
			if string(node.ExecutionMode) != fixture.WantMode {
				t.Fatalf("Build(%q) mode = %q, want %q; node=%#v", fixture.Query, node.ExecutionMode, fixture.WantMode, node)
			}
			if plan.RequiresInterleaved != (fixture.WantMode == string(ExecutionModeInterleavedFallback)) {
				t.Fatalf("Build(%q) RequiresInterleaved = %v, node mode = %q", fixture.Query, plan.RequiresInterleaved, node.ExecutionMode)
			}
		})
	}
}

func loadGatedCorpus(t *testing.T) gatedCorpus {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRootForGatedCorpus(t), "testdata", "gated", "planner_corpus.json"))
	if err != nil {
		t.Fatalf("read gated corpus: %v", err)
	}
	var corpus gatedCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("decode gated corpus: %v", err)
	}
	return corpus
}

func repoRootForGatedCorpus(t *testing.T) string {
	t.Helper()
	return testutil.RepoRoot(t)
}
