package cli_test

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
	"github.com/ricardocabral/ajq/internal/testharness"
	"github.com/ricardocabral/ajq/internal/testutil"
)

func TestPureJQGoldenCorpus(t *testing.T) {
	corpus := testharness.VerifyCorpus(t, pureJQCorpusPath(t))
	if got := len(corpus.Fixtures); got < 50 {
		t.Fatalf("pure-jq corpus has %d fixtures, want at least 50", got)
	}
}

func TestPureJQGoldenCorpusCoversPhase02Categories(t *testing.T) {
	corpus := loadPureJQCorpus(t)
	byName := map[string]bool{}
	for _, fixture := range corpus.Fixtures {
		byName[fixture.Name] = true
	}

	required := map[string]string{
		"path":                         "path_raw_string",
		"slice":                        "array_slice",
		"iterator":                     "array_iterator",
		"select/map":                   "select_map_field",
		"object construction":          "object_construction",
		"array construction":           "array_construction",
		"updates":                      "update_multiply",
		"grouping":                     "group_by_kind",
		"sorting":                      "sort_by_number",
		"unique_by":                    "unique_by_kind",
		"reduce":                       "reduce_sum",
		"foreach":                      "foreach_running_sum",
		"interpolation":                "interpolation_raw",
		"defaults":                     "default_operator",
		"optional access":              "optional_access",
		"arithmetic":                   "arithmetic_precedence",
		"test":                         "regex_test_true",
		"match":                        "regex_match_object",
		"JSON framing":                 "identity_compact_object",
		"NDJSON independent frames":    "ndjson_multi_result_per_frame",
		"raw input":                    "raw_input_identity_json_strings",
		"null input":                   "null_input_constant",
		"compact output":               "identity_compact_object",
		"raw output":                   "raw_output_string",
		"exit-status true":             "exit_status_true",
		"exit-status false":            "exit_status_false",
		"exit-status null":             "exit_status_null",
		"exit-status empty":            "exit_status_empty",
		"compile stderr/exit":          "compile_error",
		"runtime stderr/exit":          "runtime_error",
		"partial output runtime error": "partial_output_then_runtime_error",
	}
	for category, fixtureName := range required {
		if !byName[fixtureName] {
			t.Fatalf("missing fixture %q for category %s", fixtureName, category)
		}
	}
}

func TestPureJQNoNetworkOrDaemonDependencies(t *testing.T) {
	cmd := exec.CommandContext(context.Background(), "go", "list", "-deps", "github.com/ricardocabral/ajq/internal/engine", "github.com/ricardocabral/ajq/internal/jq")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list deps failed: %v\n%s", err, out)
	}
	deps := strings.Split(strings.TrimSpace(string(out)), "\n")
	for _, dep := range deps {
		switch dep {
		case "net/http", "os/exec":
			t.Fatalf("pure-jq path unexpectedly depends on %s (network/daemon dependency)", dep)
		}
	}
}

// TestPureJQLiveDifferentialAgainstJQ proves that ajq's pure-jq behavior matches
// the installed jq CLI for the comparable fixtures in the golden corpus. The
// golden corpus (TestPureJQGoldenCorpus) proves ajq is stable; this test adds an
// independent check that ajq's stdout and exit status still align with jq
// semantics.
//
// Only stdout and exit status are compared. Stderr is intentionally excluded
// because ajq's diagnostic wording ("ajq: error: ...") is deliberately distinct
// from jq's wording; comparing stderr would produce spurious differences that
// say nothing about jq-semantic equivalence.
//
// ajq emits object keys in deterministic sorted order (part of its
// byte-reproducibility guarantee), whereas jq preserves input/insertion order by
// default. To compare value-equivalence rather than incidental key ordering, the
// live jq invocation is normalized with --sort-keys, which matches ajq's
// deterministic ordering exactly. See runLiveJQFixture.
//
// Fixtures that exercise ajq-specific behavior (e.g. --explain) are skipped via
// skipLiveJQReason because jq has no equivalent surface.
func TestPureJQLiveDifferentialAgainstJQ(t *testing.T) {
	jqPath, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("jq CLI not found on PATH; skipping live differential test (install jq to enable)")
	}

	corpus := loadPureJQCorpus(t)
	compared := 0
	for i := range corpus.Fixtures {
		fixture := corpus.Fixtures[i]
		if reason := skipLiveJQReason(fixture); reason != "" {
			t.Run(fixture.Name, func(t *testing.T) { t.Skipf("not comparable to live jq: %s", reason) })
			continue
		}
		compared++
		t.Run(fixture.Name, func(t *testing.T) {
			ajq := runAJQFixture(t, fixture)
			live := runLiveJQFixture(t, jqPath, fixture)
			if ajq.stdout != live.stdout {
				t.Errorf("stdout differs from live jq for %s\nquery: %s\nargs: %v\n--- ajq\n%q\n+++ jq\n%q", fixture.Name, fixture.Query, fixture.Args, ajq.stdout, live.stdout)
			}
			if ajq.exit != live.exit {
				t.Errorf("exit status differs from live jq for %s\nquery: %s\nargs: %v\n--- ajq: %d\n+++ jq:  %d", fixture.Name, fixture.Query, fixture.Args, ajq.exit, live.exit)
			}
		})
	}

	if compared == 0 {
		t.Fatal("no fixtures were comparable to live jq; expected at least one")
	}
}

type cliResult struct {
	stdout string
	exit   int
}

// skipLiveJQReason returns a non-empty reason when a fixture cannot be compared
// against the live jq CLI. An empty string means the fixture is comparable.
func skipLiveJQReason(fixture testharness.Fixture) string {
	for _, arg := range fixture.Args {
		if arg == "--explain" {
			return "--explain is an ajq-specific diagnostic with no jq equivalent"
		}
	}
	return ""
}

// runAJQFixture executes the in-process ajq CLI for a fixture and returns its
// stdout and exit status.
func runAJQFixture(t *testing.T, fixture testharness.Fixture) cliResult {
	t.Helper()
	var stdout, stderr bytes.Buffer
	args := append([]string{}, fixture.Args...)
	args = append(args, fixture.Query)
	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(fixture.Stdin),
		Stdout: &stdout,
		Stderr: &stderr,
	}, args)
	return cliResult{stdout: stdout.String(), exit: cli.ExitCode(err)}
}

// runLiveJQFixture executes the installed jq CLI for a fixture using the same
// flags and query, and returns its stdout and exit status.
func runLiveJQFixture(t *testing.T, jqPath string, fixture testharness.Fixture) cliResult {
	t.Helper()
	// ajq emits object keys in deterministic sorted order; normalize jq with
	// --sort-keys so the comparison measures value-equivalence, not key ordering.
	args := append([]string{"--sort-keys"}, fixture.Args...)
	args = append(args, fixture.Query)
	cmd := exec.CommandContext(context.Background(), jqPath, args...) //nolint:gosec // jqPath comes from exec.LookPath in an opt-in live differential test.
	cmd.Stdin = strings.NewReader(fixture.Stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exit = exitErr.ExitCode()
		} else {
			t.Fatalf("run live jq for %s: %v\nstderr: %s", fixture.Name, err, stderr.String())
		}
	}
	return cliResult{stdout: stdout.String(), exit: exit}
}

func loadPureJQCorpus(t *testing.T) testharness.Corpus {
	t.Helper()
	corpus, err := testharness.LoadCorpus(pureJQCorpusPath(t))
	if err != nil {
		t.Fatalf("load pure-jq corpus: %v", err)
	}
	return corpus
}

func pureJQCorpusPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "testdata", "golden", "purejq", "fixtures.json")
}

func repoRoot(t *testing.T) string {
	t.Helper()
	return testutil.RepoRoot(t)
}
