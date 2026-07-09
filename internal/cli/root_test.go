package cli_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
)

func run(args ...string) (stdout string, stderr string, err error) {
	return runWithStdin("", args...)
}

func runWithStdin(stdin string, args ...string) (stdout string, stderr string, err error) {
	cleanup, cleanupErr := isolateCacheEnvForRun()
	if cleanup != nil {
		defer cleanup()
	}
	if cleanupErr != nil {
		return "", "", cleanupErr
	}
	var out bytes.Buffer
	var errBuf bytes.Buffer
	err = cli.Execute(context.Background(), cli.Options{Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errBuf}, args)
	return out.String(), errBuf.String(), err
}

func isolateCacheEnvForRun() (func(), error) {
	old, hadOld := os.LookupEnv("AJQ_CACHE_DIR")
	dir, err := os.MkdirTemp("", "ajq-cli-cache-*")
	if err != nil {
		return nil, err
	}
	if err := os.Setenv("AJQ_CACHE_DIR", dir); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return func() {
		if hadOld {
			_ = os.Setenv("AJQ_CACHE_DIR", old)
		} else {
			_ = os.Unsetenv("AJQ_CACHE_DIR")
		}
		_ = os.RemoveAll(dir)
	}, nil
}

func TestHelp(t *testing.T) {
	stdout, stderr, err := run("--help")
	if err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	for _, want := range []string{"ajq [query]", "--version", "--max-calls", "paid backends default to 100", "0 = unlimited"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("help output missing %q: %q", want, stdout)
		}
	}
}

func TestVersion(t *testing.T) {
	stdout, stderr, err := run("--version")
	if err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if strings.TrimSpace(stdout) != "ajq dev" {
		t.Fatalf("unexpected version output: %q", stdout)
	}
}

func TestMissingQueryReportsError(t *testing.T) {
	_, stderr, err := run()
	if err == nil {
		t.Fatal("expected missing query to fail")
	}
	if !strings.Contains(stderr, "ajq: error:") || !strings.Contains(stderr, "missing query") {
		t.Fatalf("stderr missing clear error: %q", stderr)
	}
}

func TestUnknownFlagReportsConventionalError(t *testing.T) {
	_, stderr, err := run("--definitely-not-a-flag")
	if err == nil {
		t.Fatal("expected unknown flag to fail")
	}
	if !strings.Contains(stderr, "ajq: error:") || !strings.Contains(stderr, "unknown flag") {
		t.Fatalf("stderr missing unknown flag error: %q", stderr)
	}
}

func TestPureJQQueryExecutes(t *testing.T) {
	stdout, stderr, err := runWithStdin(`{"foo":{"bar":3}}`, "-c", ".foo.bar + 2")
	if err != nil {
		t.Fatalf("pure jq query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "5\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestEmptyQueryDoesNotPanic(t *testing.T) {
	_, stderr, err := run("")
	if err == nil {
		t.Fatal("expected empty query to fail")
	}
	if !strings.Contains(stderr, "query \"\" is empty") {
		t.Fatalf("stderr missing empty query detail: %q", stderr)
	}
}

func TestIdentityQueryFramesAutoInput(t *testing.T) {
	stdout, stderr, err := runWithStdin("{\"a\":1}\n{\"a\":2}\n", "-c", ".")
	if err != nil {
		t.Fatalf("identity query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "{\"a\":1}\n{\"a\":2}\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestNullInputFlagPlumbing(t *testing.T) {
	stdout, stderr, err := runWithStdin("{\"ignored\":true}", "-n", "-c", ".")
	if err != nil {
		t.Fatalf("null-input query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "null\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestRawInputAndRawOutputFlagPlumbing(t *testing.T) {
	stdout, stderr, err := runWithStdin("alpha\nbeta\n", "-R", "-r", ".")
	if err != nil {
		t.Fatalf("raw-input query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "alpha\nbeta\n" {
		t.Fatalf("stdout = %q", stdout)
	}
}

func TestMalformedInputErrorNamesQuery(t *testing.T) {
	_, stderr, err := runWithStdin("{bad json}\n", ".")
	if err == nil {
		t.Fatal("expected malformed stdin to fail")
	}
	if !strings.Contains(stderr, "query \".\" input error") || !strings.Contains(stderr, "near byte") {
		t.Fatalf("stderr missing query and position: %q", stderr)
	}
}

func TestCompileErrorNamesQuery(t *testing.T) {
	_, stderr, err := runWithStdin("{}", ".[")
	if err == nil {
		t.Fatal("expected compile error to fail")
	}
	if !strings.Contains(stderr, "query \".[\" compile error") {
		t.Fatalf("stderr missing query and compile error: %q", stderr)
	}
	if got := cli.ExitCode(err); got != 3 {
		t.Fatalf("ExitCode(compile) = %d, want 3", got)
	}
}

func TestRuntimeErrorNamesQuery(t *testing.T) {
	_, stderr, err := runWithStdin("1", ".[10]")
	if err == nil {
		t.Fatal("expected runtime error to fail")
	}
	if !strings.Contains(stderr, "query \".[10]\" runtime error") {
		t.Fatalf("stderr missing query and runtime error: %q", stderr)
	}
	if got := cli.ExitCode(err); got != 5 {
		t.Fatalf("ExitCode(runtime) = %d, want 5", got)
	}
}

func TestExplainPureJQSuccess(t *testing.T) {
	stdout, stderr, err := runWithStdin(`{"items":[{"id":1,"active":true},{"id":2,"active":false}]}`, "--explain", ".items[] | select(.active) | .id")
	if err != nil {
		t.Fatalf("--explain returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	want := "ajq explain v1\n" +
		"query: \".items[] | select(.active) | .id\"\n" +
		"execution: pure-jq deterministic\n" +
		"deterministic: yes\n" +
		"model_calls: 0\n" +
		"backend_calls: 0\n" +
		"byte_reproducible: yes\n" +
		"stdin: ignored\n"
	if stdout != want {
		t.Fatalf("stdout = %q, want %q", stdout, want)
	}
}

func TestExplainDesugarsSemanticInfix(t *testing.T) {
	stdout, stderr, err := runWithStdin("ignored", "--explain", `. =~ "urgent"`)
	if err != nil {
		t.Fatalf("--explain semantic infix returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, `query: "sem_match(\"urgent\")"`) {
		t.Fatalf("explain output did not contain rewritten query: %q", stdout)
	}
}

func TestStatsPrintsSummaryToStderrNotStdout(t *testing.T) {
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")
	t.Setenv("AJQ_MAX_CALLS", "")

	stdout, stderr, err := runWithStdin(`[{"msg":"keep"},{"msg":"drop"}]`, "--backend", "mock", "--stats", `.[] | .msg =~ "keep"`)
	if err != nil {
		t.Fatalf("--stats query returned error: %v; stderr=%q", err, stderr)
	}
	if stdout != "true\nfalse\n" {
		t.Fatalf("stdout = %q, want only data", stdout)
	}
	for _, want := range []string{"ajq stats:\n", "  input_frames: 1\n", "  semantic_call_sites: 1\n", "  harvested_judgements: 2\n", "  post_dedup_backend_calls: 2\n", "  cache_hits: 0\n", "  elapsed:"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("stderr missing %q in %q", want, stderr)
		}
	}
	if strings.Contains(stdout, "ajq stats") {
		t.Fatalf("stdout contains stats: %q", stdout)
	}
}

func TestExplainSemanticInputEstimatesUseMockHarvest(t *testing.T) {
	stdout, stderr, err := runWithStdin(`[{"msg":"keep"},{"msg":"keep"},{"msg":"drop"}]`, "--explain", `.[] | sem_match(.msg; "keep")`)
	if err != nil {
		t.Fatalf("--explain semantic estimates returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	for _, want := range []string{
		"stdin: harvested for estimates\n",
		"  estimate_status: available\n",
		"  harvested_judgements: 3\n",
		"  post_dedup_judgements: 2\n",
		"  mock_judge_batches: 1\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("explain output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestExplainPaidBackendIncludesEstimatedCost(t *testing.T) {
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AJQ_BACKEND", "")
	t.Setenv("AJQ_MODEL", "")
	t.Setenv("AJQ_BASE_URL", "")
	t.Setenv("AJQ_MAX_CALLS", "")

	stdout, stderr, err := runWithStdin(`[{"msg":"keep"},{"msg":"drop"}]`, "--cloud", "--explain", `.[] | .msg =~ "keep"`)
	if err != nil {
		t.Fatalf("paid --explain returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	for _, want := range []string{
		"  post_dedup_judgements: 2\n",
		"  estimated_cost_usd: ~$0.01 (2 calls × model anthropic/claude-haiku-4-5)\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("explain output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestExplainSemanticInvalidInputKeepsStaticPlan(t *testing.T) {
	stdout, stderr, err := runWithStdin(`ignored`, "--explain", `. =~ "urgent"`)
	if err != nil {
		t.Fatalf("--explain semantic invalid input returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	for _, want := range []string{
		`query: "sem_match(\"urgent\")"`,
		"  estimate_status: unavailable: invalid input\n",
		"  static_call_sites: 1\n",
		"semantic_plan:\n",
	} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("explain output missing %q in:\n%s", want, stdout)
		}
	}
}

func TestExplainUnknownFunctionReturnsCompileExit(t *testing.T) {
	_, stderr, err := runWithStdin("ignored", "--explain", `unknown_func(.)`)
	if err == nil {
		t.Fatal("expected unknown function explain query to fail")
	}
	if got := cli.ExitCode(err); got != 3 {
		t.Fatalf("ExitCode(unknown function explain) = %d, want 3", got)
	}
	if !strings.Contains(stderr, "compile error") {
		t.Fatalf("stderr missing compile error: %q", stderr)
	}
}

func TestExplainUndefinedVariableReturnsCompileExit(t *testing.T) {
	_, stderr, err := runWithStdin("ignored", "--explain", `$x`)
	if err == nil {
		t.Fatal("expected undefined variable explain query to fail")
	}
	if got := cli.ExitCode(err); got != 3 {
		t.Fatalf("ExitCode(undefined variable explain) = %d, want 3", got)
	}
	if !strings.Contains(stderr, "compile error") {
		t.Fatalf("stderr missing compile error: %q", stderr)
	}
}

func TestExplainInvalidQueryReturnsCompileExit(t *testing.T) {
	_, stderr, err := runWithStdin("{bad json is ignored}", "--explain", ".[")
	if err == nil {
		t.Fatal("expected invalid explain query to fail")
	}
	if got := cli.ExitCode(err); got != 3 {
		t.Fatalf("ExitCode(invalid explain) = %d, want 3", got)
	}
	if !strings.Contains(stderr, "query \".[\" compile error") {
		t.Fatalf("stderr missing compile error: %q", stderr)
	}
}

func TestExplainDoesNotReadStdin(t *testing.T) {
	stdout, stderr, err := runWithStdin("{not json and should not be framed", "--explain", ".")
	if err != nil {
		t.Fatalf("--explain should ignore invalid stdin, err=%v stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if !strings.Contains(stdout, "stdin: ignored\n") {
		t.Fatalf("explain output missing stdin ignored marker: %q", stdout)
	}
}

func TestExplainOutputIsDeterministic(t *testing.T) {
	stdout1, stderr1, err1 := runWithStdin("ignored", "--explain", "{id:.id,label:(.name | ascii_upcase)}")
	stdout2, stderr2, err2 := runWithStdin("different ignored input", "--explain", "{id:.id,label:(.name | ascii_upcase)}")
	if err1 != nil || err2 != nil {
		t.Fatalf("--explain returned errors: %v %v", err1, err2)
	}
	if stderr1 != "" || stderr2 != "" {
		t.Fatalf("expected empty stderrs, got %q and %q", stderr1, stderr2)
	}
	if stdout1 != stdout2 {
		t.Fatalf("explain output changed across runs\nfirst: %q\nsecond:%q", stdout1, stdout2)
	}
}

func TestBackendMockRunsSemMatchQuery(t *testing.T) {
	stdout, stderr, err := runWithStdin(
		`[{"id":1,"msg":"please keep this"},{"id":2,"msg":"drop it"}]`,
		"--backend", "mock", "-c", `.[] | select(.msg =~ "keep") | .id`,
	)
	if err != nil {
		t.Fatalf("--backend mock sem_match query returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "1\n")
	}
}

func TestBackendMockRunsSemMatchInfixDesugar(t *testing.T) {
	// Confirms the =~ infix operator desugars and executes end-to-end via the CLI.
	stdout, stderr, err := runWithStdin(
		`{"msg":"keep this record"}`,
		"--backend", "mock", "-c", `.msg =~ "keep"`,
	)
	if err != nil {
		t.Fatalf("--backend mock =~ query returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "true\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "true\n")
	}
}

func TestBackendMockRunsClassifyEnrichment(t *testing.T) {
	stdout, stderr, err := runWithStdin(
		`[{"msg":"billing issue"},{"msg":"other stuff"}]`,
		"--backend", "mock", "-c", `.[] | sem_classify(.msg; "billing"; "other")`,
	)
	if err != nil {
		t.Fatalf("--backend mock sem_classify query returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "\"billing\"\n\"other\"\n" {
		t.Fatalf("stdout = %q, want billing then other", stdout)
	}
}

func TestBackendMockRunsInterleavedGatedFallback(t *testing.T) {
	// sem_score inside a select gate requires interleaved fallback execution.
	stdout, stderr, err := runWithStdin(
		`[{"id":1,"msg":"urgent request"},{"id":2,"msg":"calm day"}]`,
		"--backend", "mock", "-c", `.[] | select(sem_score(.msg; "urgent") > 0.8) | .id`,
	)
	if err != nil {
		t.Fatalf("--backend mock interleaved query returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "1\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "1\n")
	}
}

func TestSemanticQueryWithoutBackendFailsClearly(t *testing.T) {
	_, stderr, err := runWithStdin(
		`[{"msg":"keep"}]`,
		"-c", `.[] | select(.msg =~ "keep")`,
	)
	if err == nil {
		t.Fatal("expected semantic query without --backend to fail")
	}
	if !strings.Contains(stderr, "ajq: error:") || !strings.Contains(stderr, "semantic operators require a backend") || !strings.Contains(stderr, "--backend mock") {
		t.Fatalf("stderr missing actionable no-backend error: %q", stderr)
	}
	if got := cli.ExitCode(err); got == 0 {
		t.Fatalf("ExitCode(no-backend semantic) = %d, want non-zero", got)
	}
}

func TestUnknownBackendFailsBeforeExecution(t *testing.T) {
	stdout, stderr, err := runWithStdin(
		`{"a":1}`,
		"--backend", "bogus", "-c", ".a",
	)
	if err == nil {
		t.Fatal("expected unknown backend to fail")
	}
	if stdout != "" {
		t.Fatalf("expected no stdout when backend is rejected before execution, got %q", stdout)
	}
	if !strings.Contains(stderr, "ajq: error:") || !strings.Contains(stderr, `unknown backend "bogus"`) {
		t.Fatalf("stderr missing unknown backend error: %q", stderr)
	}
	if got := cli.ExitCode(err); got != 2 {
		t.Fatalf("ExitCode(unknown backend) = %d, want 2", got)
	}
}

func TestBackendOmittedPreservesPureJQ(t *testing.T) {
	stdout, stderr, err := runWithStdin(`{"foo":{"bar":3}}`, "-c", ".foo.bar + 2")
	if err != nil {
		t.Fatalf("pure jq query returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	if stdout != "5\n" {
		t.Fatalf("stdout = %q, want %q", stdout, "5\n")
	}
}

func TestExitStatusFlag(t *testing.T) {
	stdout, stderr, err := runWithStdin("false", "-e", ".")
	if err == nil {
		t.Fatal("expected false result to return exit status error")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(false) = %d, want 1", got)
	}
	if stdout != "false\n" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}

	stdout, stderr, err = runWithStdin("[]", "-e", ".[]")
	if err == nil {
		t.Fatal("expected empty output to return exit status error")
	}
	if got := cli.ExitCode(err); got != 4 {
		t.Fatalf("ExitCode(empty) = %d, want 4", got)
	}
	if stdout != "" || stderr != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout, stderr)
	}
}
