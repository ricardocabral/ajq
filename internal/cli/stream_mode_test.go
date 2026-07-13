package cli_test

import (
	"strings"
	"testing"
)

const streamQuery = `select(sem_match(.msg; "urgent")) | .id`

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
	if streamOut != "1\n" || !strings.Contains(streamErr, "  execution_mode: user-stream\n") {
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
