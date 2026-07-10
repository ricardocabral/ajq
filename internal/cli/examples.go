package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

type exampleGroup struct {
	topic       string
	title       string
	description string
	commands    []string
}

var exampleGroups = []exampleGroup{
	{
		topic:       "pure-jq",
		title:       "Pure jq",
		description: "Deterministic: no semantic backend is constructed.",
		commands: []string{
			`printf '{"users":[{"name":"Ada"}]}' | ajq -r '.users[].name'`,
		},
	},
	{
		topic:       "semantic-filter",
		title:       "Semantic filter",
		description: "The mock backend is deterministic and needs no model, network, or API key.",
		commands: []string{
			`printf '[{"id":1,"msg":"please keep this"},{"id":2,"msg":"drop it"}]' | ajq --backend mock -c '.[] | select(.msg =~ "keep") | .id'`,
		},
	},
	{
		topic:       "explain",
		title:       "Explain and estimate",
		description: "Inspect the semantic plan and estimated calls without executing it.",
		commands: []string{
			`printf '[{"msg":"refund demanded"}]' | ajq --backend mock --explain '.[] | select(.msg =~ "angry/frustrated") | .msg'`,
		},
	},
	{
		topic:       "classification",
		title:       "Classification",
		description: "Classify values against an explicit, bounded label set.",
		commands: []string{
			`printf '[{"msg":"billing question"}]' | ajq --backend mock -c '.[] | sem_classify(.msg; "billing"; "other")'`,
		},
	},
	{
		topic:       "ndjson",
		title:       "NDJSON and raw lines",
		description: "Use one JSON value per line, or treat each input line as text.",
		commands: []string{
			`printf '{"id":1,"msg":"keep"}\n{"id":2,"msg":"drop"}\n' | ajq --backend mock -c 'select(.msg =~ "keep") | .id'`,
			`printf 'info\nerror: unavailable\n' | ajq --backend mock -R -r 'select(. =~ "error")'`,
		},
	},
}

func newExamplesCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "examples [topic]",
		Short:         "print safe copy-paste examples",
		Long:          "Print categorized ajq examples. Semantic examples use the deterministic mock backend, so they require no model, network access, or API key. Topics: " + exampleTopicNames() + ".",
		Args:          cobra.MaximumNArgs(1),
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			groups, err := selectExampleGroups(args)
			if err != nil {
				return &ExitError{Code: 2, Err: err}
			}
			if err := writeExamples(cmd, groups); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write examples: %w", err)}
			}
			return nil
		},
	}
}

func writeExamples(cmd *cobra.Command, groups []exampleGroup) error {
	if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Semantic examples use --backend mock and require no model, network access, or API key."); err != nil {
		return err
	}
	for i, group := range groups {
		if i > 0 {
			if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n%s\n", group.title, group.description); err != nil {
			return err
		}
		for _, command := range group.commands {
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "$ %s\n", command); err != nil {
				return err
			}
		}
	}
	return nil
}

func selectExampleGroups(args []string) ([]exampleGroup, error) {
	if len(args) == 0 {
		return exampleGroups, nil
	}
	for _, group := range exampleGroups {
		if args[0] == group.topic {
			return []exampleGroup{group}, nil
		}
	}
	return nil, fmt.Errorf("unknown examples topic %q: choose one of %s", args[0], exampleTopicNames())
}

func exampleTopicNames() string {
	topics := make([]string, 0, len(exampleGroups))
	for _, group := range exampleGroups {
		topics = append(topics, group.topic)
	}
	return strings.Join(topics, ", ")
}
