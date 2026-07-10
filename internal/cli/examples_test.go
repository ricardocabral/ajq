package cli_test

import (
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
)

type parsedExampleCommand struct {
	input string
	args  []string
	query string
}

func TestExamplesOutputCommandsExecute(t *testing.T) {
	isolateExamplesEnvironment(t)

	stdout, stderr, err := run("examples")
	if err != nil {
		t.Fatalf("examples returned error: %v; stderr=%q", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("examples stderr = %q, want empty", stderr)
	}

	commands := parseExampleCommands(t, stdout)
	if len(commands) != 6 {
		t.Fatalf("examples command count = %d, want 6; output:\n%s", len(commands), stdout)
	}

	wantOutput := map[string]string{
		`.users[].name`:                                "Ada\n",
		`.[] | select(.msg =~ "keep") | .id`:           "1\n",
		`.[] | sem_classify(.msg; "billing"; "other")`: `"billing"` + "\n",
		`select(.msg =~ "keep") | .id`:                 "1\n",
		`select(. =~ "error")`:                         "error: unavailable\n",
	}
	seen := make(map[string]bool)
	for _, command := range commands {
		if strings.Contains(command.query, "=~") || strings.Contains(command.query, "sem_classify") {
			if !containsArgs(command.args, "--backend", "mock") {
				t.Fatalf("semantic example %q does not explicitly use --backend mock", command.query)
			}
		}

		got, commandStderr, commandErr := runWithStdin(command.input, command.args...)
		if commandErr != nil {
			t.Fatalf("example %q returned error: %v; stderr=%q", command.query, commandErr, commandStderr)
		}
		if commandStderr != "" {
			t.Fatalf("example %q stderr = %q, want empty", command.query, commandStderr)
		}

		if want, ok := wantOutput[command.query]; ok {
			if got != want {
				t.Fatalf("example %q output = %q, want %q", command.query, got, want)
			}
			seen[command.query] = true
			continue
		}
		if command.query == `.[] | select(.msg =~ "angry/frustrated") | .msg` {
			if !strings.Contains(got, "estimate_status: available") {
				t.Fatalf("explain example output missing estimate marker:\n%s", got)
			}
			seen[command.query] = true
			continue
		}
		t.Fatalf("unexpected example query %q", command.query)
	}
	if len(seen) != len(commands) {
		t.Fatalf("executed %d distinct examples, want %d", len(seen), len(commands))
	}
}

func TestExamplesUnknownTopic(t *testing.T) {
	isolateExamplesEnvironment(t)

	stdout, stderr, err := run("examples", "bogus")
	if err == nil {
		t.Fatal("examples bogus returned nil error")
	}
	if got := cli.ExitCode(err); got != 2 {
		t.Fatalf("ExitCode(examples bogus) = %d, want 2", got)
	}
	if stdout != "" {
		t.Fatalf("examples bogus stdout = %q, want empty", stdout)
	}
	for _, want := range []string{"unknown examples topic \"bogus\"", "choose one of", "semantic-filter"} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("examples bogus stderr missing %q: %q", want, stderr)
		}
	}
}

func isolateExamplesEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, name := range []string{"AJQ_BACKEND", "AJQ_MODEL", "AJQ_BASE_URL", "AJQ_MAX_CALLS"} {
		t.Setenv(name, "")
	}
}

func parseExampleCommands(t *testing.T, output string) []parsedExampleCommand {
	t.Helper()
	var commands []parsedExampleCommand
	for _, line := range strings.Split(output, "\n") {
		if !strings.HasPrefix(line, "$ ") {
			continue
		}
		commands = append(commands, parseExampleCommand(t, strings.TrimPrefix(line, "$ ")))
	}
	return commands
}

// parseExampleCommand accepts only the deliberately limited printf-single-quote
// pipe grammar emitted by ajq examples; it never invokes a shell.
func parseExampleCommand(t *testing.T, line string) parsedExampleCommand {
	t.Helper()
	const prefix = "printf '"
	const separator = "' | ajq "
	if !strings.HasPrefix(line, prefix) {
		t.Fatalf("example command %q does not start with %q", line, prefix)
	}
	rest := strings.TrimPrefix(line, prefix)
	index := strings.Index(rest, separator)
	if index < 0 {
		t.Fatalf("example command %q does not contain %q", line, separator)
	}
	input := strings.NewReplacer(`\n`, "\n", `\t`, "\t", `\\`, `\`).Replace(rest[:index])
	arguments := rest[index+len(separator):]
	queryStart := strings.LastIndex(arguments, " '")
	if queryStart < 0 || !strings.HasSuffix(arguments, "'") {
		t.Fatalf("example command %q does not have a single-quoted query", line)
	}
	query := arguments[queryStart+2 : len(arguments)-1]
	args := append(strings.Fields(arguments[:queryStart]), query)
	return parsedExampleCommand{input: input, args: args, query: query}
}

func containsArgs(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
