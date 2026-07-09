// Package testharness verifies ajq golden-output fixtures.
package testharness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
)

// UpdateEnv enables golden fixture rewrites when set to 1.
const UpdateEnv = "AJQ_UPDATE_GOLDEN"

// Corpus is the on-disk golden fixture format.
type Corpus struct {
	Fixtures []Fixture `json:"fixtures"`
}

// Fixture describes one CLI invocation and its expected process outputs.
type Fixture struct {
	Name       string   `json:"name"`
	Query      string   `json:"query"`
	Args       []string `json:"args"`
	Stdin      string   `json:"stdin"`
	WantStdout string   `json:"want_stdout"`
	WantStderr string   `json:"want_stderr"`
	WantExit   int      `json:"want_exit"`
}

type result struct {
	stdout string
	stderr string
	exit   int
}

// LoadCorpus reads a JSON golden corpus from path.
func LoadCorpus(path string) (Corpus, error) {
	data, err := os.ReadFile(path) //nolint:gosec // golden corpus path is supplied by tests from checked-in testdata.
	if err != nil {
		return Corpus{}, err
	}
	var corpus Corpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		return Corpus{}, err
	}
	return corpus, nil
}

// VerifyCorpus runs every fixture in path. By default it verifies expected
// stdout, stderr, and exit code. If AJQ_UPDATE_GOLDEN=1 is set, it rewrites the
// expected values with the observed results instead of failing on mismatches.
func VerifyCorpus(t *testing.T, path string) Corpus {
	t.Helper()
	corpus, err := LoadCorpus(path)
	if err != nil {
		t.Fatalf("load golden corpus %s: %v", path, err)
	}
	update := os.Getenv(UpdateEnv) == "1"

	for i := range corpus.Fixtures {
		idx := i
		fixture := corpus.Fixtures[idx]
		t.Run(fixture.Name, func(t *testing.T) {
			got := runFixture(t, fixture)
			if update {
				corpus.Fixtures[idx].WantStdout = got.stdout
				corpus.Fixtures[idx].WantStderr = got.stderr
				corpus.Fixtures[idx].WantExit = got.exit
				return
			}
			if diff := compareFixture(fixture, got); diff != "" {
				t.Fatalf("golden mismatch for %s\n%s", fixture.Name, diff)
			}
		})
	}

	if update {
		SaveCorpus(t, path, corpus)
	}
	return corpus
}

// SaveCorpus writes a corpus using stable indentation.
func SaveCorpus(t testing.TB, path string, corpus Corpus) {
	t.Helper()
	data, err := json.MarshalIndent(corpus, "", "  ")
	if err != nil {
		t.Fatalf("encode golden corpus %s: %v", path, err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write golden corpus %s: %v", path, err)
	}
}

func runFixture(t testing.TB, fixture Fixture) result {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	args := append([]string{}, fixture.Args...)
	args = append(args, fixture.Query)
	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(fixture.Stdin),
		Stdout: &stdout,
		Stderr: &stderr,
	}, args)
	return result{stdout: stdout.String(), stderr: stderr.String(), exit: cli.ExitCode(err)}
}

func compareFixture(fixture Fixture, got result) string {
	var b strings.Builder
	if got.exit != fixture.WantExit {
		fmt.Fprintf(&b, "exit code mismatch\n--- want\n%d\n+++ got\n%d\n", fixture.WantExit, got.exit)
	}
	if got.stdout != fixture.WantStdout {
		fmt.Fprintf(&b, "stdout mismatch\n--- want\n%s\n+++ got\n%s\n", quoteMultiline(fixture.WantStdout), quoteMultiline(got.stdout))
	}
	if got.stderr != fixture.WantStderr {
		fmt.Fprintf(&b, "stderr mismatch\n--- want\n%s\n+++ got\n%s\n", quoteMultiline(fixture.WantStderr), quoteMultiline(got.stderr))
	}
	return b.String()
}

func quoteMultiline(s string) string {
	if s == "" {
		return "<empty>"
	}
	return fmt.Sprintf("%q", s)
}
