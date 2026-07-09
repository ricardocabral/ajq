package plan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

type completenessFixture struct {
	Name    string          `json:"name"`
	Query   string          `json:"query"`
	Expect  []expectedNode  `json:"expect"`
	Summary expectedSummary `json:"summary"`
}

type expectedNode struct {
	Op        string   `json:"op"`
	Kind      string   `json:"kind"`
	Return    string   `json:"return"`
	ValueExpr string   `json:"valueExpr"`
	Specs     []string `json:"specs"`
}

type expectedSummary struct {
	SemanticCount      int `json:"semanticCount"`
	PredicateCount     int `json:"predicateCount"`
	ValueCount         int `json:"valueCount"`
	ModelCallsEstimate int `json:"modelCallsEstimate"`
}

func TestPlanCompletenessCorpus(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "testdata", "plan", "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no plan completeness fixtures found")
	}
	for _, path := range paths {
		fixture := readCompletenessFixture(t, path)
		t.Run(fixture.Name, func(t *testing.T) {
			plan, diags := Build(fixture.Query)
			if len(diags) != 0 {
				t.Fatalf("Build diagnostics = %#v", diags)
			}
			if plan.Deterministic || plan.Summary.Deterministic {
				t.Fatalf("plan should be semantic, deterministic flags plan=%v summary=%v", plan.Deterministic, plan.Summary.Deterministic)
			}
			if len(plan.Semantic) != len(fixture.Expect) {
				t.Fatalf("semantic count = %d, want %d; nodes %#v", len(plan.Semantic), len(fixture.Expect), plan.Semantic)
			}
			assertSummary(t, plan, fixture.Summary)
			assertExpectedNodes(t, plan, fixture.Expect)
			for i, node := range plan.Semantic {
				if node.Source.Expression == "" {
					t.Fatalf("node %d has empty source: %#v", i, node)
				}
				assertConcreteRange(t, fixture.Query, node.Source)
			}
		})
	}
}

func readCompletenessFixture(t *testing.T, path string) completenessFixture {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // path comes from checked-in fixture filenames under testdata.
	if err != nil {
		t.Fatal(err)
	}
	var fixture completenessFixture
	if err := json.Unmarshal(b, &fixture); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	if fixture.Name == "" || fixture.Query == "" || len(fixture.Expect) == 0 {
		t.Fatalf("fixture %s missing name/query/expect: %#v", path, fixture)
	}
	return fixture
}

func assertSummary(t *testing.T, plan Plan, want expectedSummary) {
	t.Helper()
	if plan.Summary.SemanticCount != want.SemanticCount || plan.Summary.PredicateCount != want.PredicateCount || plan.Summary.ValueCount != want.ValueCount || plan.Summary.ModelCallsEstimate != want.ModelCallsEstimate {
		t.Fatalf("summary = %#v, want %#v", plan.Summary, want)
	}
}

func assertExpectedNodes(t *testing.T, plan Plan, want []expectedNode) {
	t.Helper()
	gotCounts := map[string]int{}
	for _, node := range plan.Semantic {
		gotCounts[nodeKey(expectedNode{Op: node.Op, Kind: string(node.Kind), Return: string(node.Return), ValueExpr: node.ValueExpr, Specs: node.Specs})]++
	}
	wantCounts := map[string]int{}
	for _, node := range want {
		wantCounts[nodeKey(node)]++
	}
	if fmt.Sprint(gotCounts) != fmt.Sprint(wantCounts) {
		t.Fatalf("nodes = %v, want %v", gotCounts, wantCounts)
	}
}

func nodeKey(node expectedNode) string {
	b, _ := json.Marshal(node)
	return string(b)
}
