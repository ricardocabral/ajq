package main

import (
	"context"
	"os"

	"github.com/ricardocabral/ajq/internal/cli"
)

func main() {
	if err := cli.Execute(context.Background(), cli.Options{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}, os.Args[1:]); err != nil {
		os.Exit(cli.ExitCode(err))
	}
}
