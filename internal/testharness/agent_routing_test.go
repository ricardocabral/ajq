package testharness

import (
	"path/filepath"
	"testing"

	"github.com/ricardocabral/ajq/internal/testutil"
)

func TestAgentRoutingV1CorpusAndScorerFixture(t *testing.T) {
	root := testutil.RepoRoot(t)
	corpus, err := LoadAgentRoutingCorpus(filepath.Join(root, "testdata", "agent-routing", "v1", "corpus.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingCorpus: %v", err)
	}
	if len(corpus.Scenarios) != 6 {
		t.Fatalf("scenario count = %d, want 6 required routing scenarios", len(corpus.Scenarios))
	}
	wantScenarios := map[string]bool{
		"exact-structural-transform":               false,
		"fuzzy-ndjson-filter":                      false,
		"bounded-intent-routing":                   false,
		"sensitive-one-off-data":                   false,
		"unsupported-general-extraction-redaction": false,
		"ajq-unavailable":                          false,
	}
	for _, scenario := range corpus.Scenarios {
		if _, ok := wantScenarios[scenario.ID]; !ok {
			t.Fatalf("unexpected routing scenario %q", scenario.ID)
		}
		wantScenarios[scenario.ID] = true
	}
	for scenarioID, found := range wantScenarios {
		if !found {
			t.Fatalf("required routing scenario %q is missing", scenarioID)
		}
	}
	if len(corpus.Artifacts) != 4 {
		t.Fatalf("artifact count = %d, want none, local guidance, installed skill, and public docs", len(corpus.Artifacts))
	}
	wantArtifacts := map[string]bool{"none": false, "local-guidance": false, "installed-skill": false, "public-docs": false}
	for _, artifact := range corpus.Artifacts {
		if _, ok := wantArtifacts[artifact.ID]; !ok {
			t.Fatalf("unexpected routing artifact %q", artifact.ID)
		}
		wantArtifacts[artifact.ID] = true
	}
	for artifactID, found := range wantArtifacts {
		if !found {
			t.Fatalf("required routing artifact %q is missing", artifactID)
		}
	}
	run, err := LoadAgentRoutingRun(filepath.Join(root, "testdata", "agent-routing", "v1", "responses", "scorer-fixture-local-guidance.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingRun: %v", err)
	}
	report, err := ScoreAgentRoutingRun(corpus, run)
	if err != nil {
		t.Fatalf("ScoreAgentRoutingRun: %v", err)
	}
	if !report.Passed {
		t.Fatalf("scorer fixture did not pass: %+v", report.Failures)
	}
	if report.CorrectToolSelections != 6 || report.RequiredSafePreflights != 2 || report.SuccessfulSafePreflights != 2 {
		t.Fatalf("unexpected scorer report: %+v", report)
	}
}

func TestAgentRoutingScoringReportsUnsafeAndIncorrectRouting(t *testing.T) {
	root := testutil.RepoRoot(t)
	corpus, err := LoadAgentRoutingCorpus(filepath.Join(root, "testdata", "agent-routing", "v1", "corpus.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingCorpus: %v", err)
	}
	run, err := LoadAgentRoutingRun(filepath.Join(root, "testdata", "agent-routing", "v1", "responses", "scorer-fixture-local-guidance.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingRun: %v", err)
	}
	for i := range run.Responses {
		if run.Responses[i].ScenarioID == "exact-structural-transform" {
			run.Responses[i].Selection = "ajq"
			run.Responses[i].UsesRealBackend = true
		}
		if run.Responses[i].ScenarioID == "unsupported-general-extraction-redaction" {
			run.Responses[i].Claims = []string{"general_extraction", "standalone_redaction"}
		}
	}
	report, err := ScoreAgentRoutingRun(corpus, run)
	if err != nil {
		t.Fatalf("ScoreAgentRoutingRun: %v", err)
	}
	if report.Passed || report.FalsePositiveAJQUses != 1 || report.UnsafeRealBackendInvocations != 1 || report.UnsupportedCapabilityClaims != 2 {
		t.Fatalf("unsafe routing report = %+v", report)
	}
}

func TestAgentRoutingObservedPairedBaseline(t *testing.T) {
	root := testutil.RepoRoot(t)
	corpus, err := LoadAgentRoutingCorpus(filepath.Join(root, "testdata", "agent-routing", "v1", "corpus.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingCorpus: %v", err)
	}
	observed := filepath.Join(root, "testdata", "agent-routing", "v1", "responses", "observed", "2026-07-12-codex-gpt-5")
	control, err := LoadAgentRoutingRun(filepath.Join(observed, "none.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingRun control: %v", err)
	}
	guidance, err := LoadAgentRoutingRun(filepath.Join(observed, "local-guidance.json"))
	if err != nil {
		t.Fatalf("LoadAgentRoutingRun local guidance: %v", err)
	}
	if control.Agent != guidance.Agent || control.RecordedAt != guidance.RecordedAt {
		t.Fatalf("paired baseline must retain the same agent and timestamp: control=%+v guidance=%+v", control, guidance)
	}

	controlReport, err := ScoreAgentRoutingRun(corpus, control)
	if err != nil {
		t.Fatalf("ScoreAgentRoutingRun control: %v", err)
	}
	if controlReport.Passed || controlReport.CorrectToolSelections != 4 || controlReport.SuccessfulSafePreflights != 0 {
		t.Fatalf("control report = %+v, want 4/6 selection, 0/2 preflights, and failure", controlReport)
	}
	guidanceReport, err := ScoreAgentRoutingRun(corpus, guidance)
	if err != nil {
		t.Fatalf("ScoreAgentRoutingRun local guidance: %v", err)
	}
	if !guidanceReport.Passed || guidanceReport.CorrectToolSelections != 6 || guidanceReport.SuccessfulSafePreflights != 2 {
		t.Fatalf("local-guidance report = %+v, want 6/6 selection, 2/2 preflights, and pass", guidanceReport)
	}
}
