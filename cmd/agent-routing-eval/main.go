// Command agent-routing-eval scores a locally captured blind-agent routing run.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/ricardocabral/ajq/internal/testharness"
)

func main() {
	corpusPath := flag.String("corpus", "", "path to an agent-routing corpus JSON file")
	responsesPath := flag.String("responses", "", "path to a structured agent response record JSON file")
	enforce := flag.Bool("enforce", true, "exit non-zero when the recorded run misses the corpus threshold")
	flag.Parse()
	if *corpusPath == "" || *responsesPath == "" {
		fmt.Fprintln(os.Stderr, "usage: agent-routing-eval -corpus PATH -responses PATH [-enforce=false]")
		os.Exit(2)
	}

	corpus, err := testharness.LoadAgentRoutingCorpus(*corpusPath)
	if err != nil {
		fatal(err)
	}
	run, err := testharness.LoadAgentRoutingRun(*responsesPath)
	if err != nil {
		fatal(err)
	}
	report, err := testharness.ScoreAgentRoutingRun(corpus, run)
	if err != nil {
		fatal(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		fatal(fmt.Errorf("write report: %w", err))
	}
	if *enforce && !report.Passed {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "agent-routing-eval:", err)
	os.Exit(2)
}
