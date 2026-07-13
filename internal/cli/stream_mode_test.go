package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
)

const streamQuery = `select(sem_match(.msg; "urgent")) | .id`

type poisonedStreamExplainInput struct{}

func (poisonedStreamExplainInput) Read([]byte) (int, error) {
	return 0, errors.New("stream explain must not read stdin")
}

func TestStreamExplainDoesNotReadStdinAndPreservesPrecedence(t *testing.T) {
	runExplain := func(stdin io.Reader, args ...string) (string, string, error) {
		t.Helper()
		var stdout, stderr bytes.Buffer
		err := cli.Execute(context.Background(), cli.Options{Stdin: stdin, Stdout: &stdout, Stderr: &stderr}, args)
		return stdout.String(), stderr.String(), err
	}

	streamOut, streamErr, err := runExplain(poisonedStreamExplainInput{}, "--backend", "mock", "--stream", "--explain", streamQuery)
	if err != nil || streamErr != "" {
		t.Fatalf("stream explain stdout=%q stderr=%q err=%v", streamOut, streamErr, err)
	}
	for _, line := range []string{
		"execution: semantic user-stream inline\n",
		"stdin: not harvested\n",
		"  estimate_status: unavailable: user stream\n",
		"  execution_selection: user-selected --stream interleaving\n",
		"  semantic_batching: inline per uncached judgement\n",
		"  cross_frame_pre_resolve_dedup: disabled\n",
	} {
		if !strings.Contains(streamOut, line) {
			t.Fatalf("stream explain missing %q:\n%s", line, streamOut)
		}
	}

	pureOut, pureErr, err := runExplain(poisonedStreamExplainInput{}, "--stream", "--explain", ".")
	if err != nil || pureErr != "" || !strings.Contains(pureOut, "execution: pure-jq deterministic\n") || strings.Contains(pureOut, "user-stream") {
		t.Fatalf("pure stream explain stdout=%q stderr=%q err=%v", pureOut, pureErr, err)
	}

	plannerOut, plannerErr, err := runExplain(strings.NewReader(`[{"id":1,"msg":"urgent"}]`), "--backend", "mock", "--stream", "--explain", `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`)
	if err != nil || plannerErr != "" || !strings.Contains(plannerOut, "execution: semantic interleaved fallback\n") || !strings.Contains(plannerOut, "  estimate_status: unavailable: interleaved fallback\n") || strings.Contains(plannerOut, "user-selected --stream") {
		t.Fatalf("planner stream explain stdout=%q stderr=%q err=%v", plannerOut, plannerErr, err)
	}
}

func TestStreamStatsRenderingKeepsSemanticOutputOnStdout(t *testing.T) {
	plannerQuery := `.[] | select(sem_score(.msg; "urgent") > 0.2) | .id`
	for _, tc := range []struct {
		name, stdin, mode, tradeoff, query string
		args                               []string
	}{
		{name: "pure-jq", stdin: "1\n", mode: "pure-jq", tradeoff: "not applicable: pure jq", query: ".", args: []string{"--stream"}},
		{name: "windowed", stdin: `{"id":1,"msg":"urgent"}` + "\n", mode: "three-phase-windowed", tradeoff: "windowed harvest with cross-frame pre-resolve dedup", query: streamQuery, args: []string{"--backend", "mock"}},
		{name: "user-stream", stdin: `{"id":1,"msg":"urgent"}` + "\n", mode: "user-stream", tradeoff: "inline per frame; cross-frame pre-resolve dedup disabled", query: streamQuery, args: []string{"--backend", "mock", "--stream"}},
		{name: "planner-interleaved", stdin: `[{"id":1,"msg":"urgent"}]` + "\n", mode: "planner-interleaved", tradeoff: "inline planner-required; cross-frame pre-resolve dedup unavailable", query: plannerQuery, args: []string{"--backend", "mock", "--stream"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withStats := append(append([]string(nil), tc.args...), "--stats", tc.query)
			stdout, stderr, err := runWindowCommand(t, tc.stdin, withStats...)
			if err != nil {
				t.Fatalf("stats command: %v; stderr=%q", err, stderr)
			}
			withoutStats := append(append([]string(nil), tc.args...), tc.query)
			plain, plainErr, err := runWindowCommand(t, tc.stdin, withoutStats...)
			if err != nil || plainErr != "" || stdout != plain {
				t.Fatalf("stdout separation stats=(%q,%q,%v) plain=(%q,%q,%v)", stdout, stderr, err, plain, plainErr, err)
			}
			for _, line := range []string{"ajq stats:\n", "  execution_mode: " + tc.mode + "\n", "  batching_dedup: " + tc.tradeoff + "\n"} {
				if !strings.Contains(stderr, line) || strings.Contains(stdout, line) {
					t.Fatalf("stats line %q not stderr-only; stdout=%q stderr=%q", line, stdout, stderr)
				}
			}
		})
	}
}

func TestStreamHelpWiringAndPureJQParity(t *testing.T) {
	help, stderr, err := run("--help")
	if err != nil || stderr != "" {
		t.Fatalf("--help stdout=%q stderr=%q err=%v", help, stderr, err)
	}
	if !strings.Contains(help, "--stream") || !strings.Contains(help, "low-latency frame output") {
		t.Fatalf("help missing stream flag: %q", help)
	}

	streamOut, streamErr, err := runWindowCommand(t, `{"id":1,"msg":"urgent"}`+"\n", "--backend", "mock", "--stream", "--stats", streamQuery)
	if err != nil {
		t.Fatalf("stream semantic command: %v; stderr=%q", err, streamErr)
	}
	if streamOut != "1\n" || !strings.Contains(streamErr, "  execution_mode: user-stream\n") || !strings.Contains(streamErr, "  batching_dedup: inline per frame; cross-frame pre-resolve dedup disabled\n") {
		t.Fatalf("stream semantic stdout/stderr = %q/%q", streamOut, streamErr)
	}
	for _, forbidden := range []string{"window_bytes: 262144", "window_count: 1"} {
		if strings.Contains(streamErr, forbidden) {
			t.Fatalf("stream changed only executor, stats unexpectedly contain %q: %q", forbidden, streamErr)
		}
	}

	plainOut, plainErr, plainErrRun := runWithStdin("1\n", "-c", ".")
	pureOut, pureErr, pureErrRun := runWithStdin("1\n", "--stream", "-c", ".")
	if plainErrRun != nil || pureErrRun != nil || plainErr != pureErr || plainOut != pureOut || pureOut != "1\n" {
		t.Fatalf("pure jq parity plain=(%q,%q,%v) stream=(%q,%q,%v)", plainOut, plainErr, plainErrRun, pureOut, pureErr, pureErrRun)
	}
}
