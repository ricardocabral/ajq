package cli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
	"github.com/ricardocabral/ajq/internal/daemon"
)

// fakeController is an injectable DaemonController for CLI tests.
type fakeController struct {
	snap        daemon.Snapshot
	stopped     bool
	stopErr     error
	stopCalls   int
	statusCalls int
}

func (f *fakeController) Status(ctx context.Context) daemon.Snapshot {
	f.statusCalls++
	return f.snap
}

func (f *fakeController) Stop(ctx context.Context) (bool, error) {
	f.stopCalls++
	return f.stopped, f.stopErr
}

func runDaemon(ctrl cli.DaemonController, args ...string) (stdout, stderr string, err error) {
	var out, errBuf bytes.Buffer
	err = cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: &out,
		Stderr: &errBuf,
		Daemon: ctrl,
	}, args)
	return out.String(), errBuf.String(), err
}

func TestDaemonStatusRunning(t *testing.T) {
	ctrl := &fakeController{snap: daemon.Snapshot{
		State:    daemon.StateRunning,
		PID:      1234,
		Address:  "127.0.0.1:8081",
		External: false,
	}}
	stdout, stderr, err := runDaemon(ctrl, "daemon", "status")
	if err != nil {
		t.Fatalf("daemon status error: %v (stderr=%q)", err, stderr)
	}
	if stderr != "" {
		t.Fatalf("expected empty stderr, got %q", stderr)
	}
	for _, want := range []string{"state: running", "pid: 1234", "address: 127.0.0.1:8081", "external: false"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("status output %q missing %q", stdout, want)
		}
	}
	if ctrl.statusCalls != 1 {
		t.Fatalf("expected 1 Status call, got %d", ctrl.statusCalls)
	}
}

func TestDaemonStatusStopped(t *testing.T) {
	ctrl := &fakeController{snap: daemon.Snapshot{
		State:   daemon.StateStopped,
		Address: "127.0.0.1:8081",
	}}
	stdout, _, err := runDaemon(ctrl, "daemon", "status")
	if err != nil {
		t.Fatalf("daemon status error: %v", err)
	}
	if !strings.Contains(stdout, "state: stopped") {
		t.Fatalf("expected stopped state, got %q", stdout)
	}
}

func TestDaemonStopWhenRunning(t *testing.T) {
	ctrl := &fakeController{stopped: true}
	stdout, stderr, err := runDaemon(ctrl, "daemon", "stop")
	if err != nil {
		t.Fatalf("daemon stop error: %v (stderr=%q)", err, stderr)
	}
	if !strings.Contains(stdout, "stopped: true") {
		t.Fatalf("expected stopped: true, got %q", stdout)
	}
	if ctrl.stopCalls != 1 {
		t.Fatalf("expected 1 Stop call, got %d", ctrl.stopCalls)
	}
}

func TestDaemonStopIdempotent(t *testing.T) {
	ctrl := &fakeController{stopped: false}
	stdout, _, err := runDaemon(ctrl, "daemon", "stop")
	if err != nil {
		t.Fatalf("daemon stop error: %v", err)
	}
	if !strings.Contains(stdout, "stopped: false") {
		t.Fatalf("expected stopped: false, got %q", stdout)
	}
}

func TestDaemonStopError(t *testing.T) {
	ctrl := &fakeController{stopped: true, stopErr: errors.New("boom")}
	stdout, stderr, err := runDaemon(ctrl, "daemon", "stop")
	if err == nil {
		t.Fatal("expected error from daemon stop")
	}
	if cli.ExitCode(err) != 1 {
		t.Fatalf("expected exit code 1, got %d", cli.ExitCode(err))
	}
	if stdout != "" {
		t.Fatalf("expected no success output on stop error, got %q", stdout)
	}
	if !strings.Contains(stderr, "ajq: error:") || !strings.Contains(stderr, "failed to stop daemon") {
		t.Fatalf("stderr missing conventional error: %q", stderr)
	}
}

func TestDaemonNoSubcommandErrors(t *testing.T) {
	ctrl := &fakeController{}
	_, stderr, err := runDaemon(ctrl, "daemon")
	if err == nil {
		t.Fatal("expected error when no subcommand given")
	}
	if cli.ExitCode(err) != 2 {
		t.Fatalf("expected exit code 2, got %d", cli.ExitCode(err))
	}
	if !strings.Contains(stderr, "subcommand") {
		t.Fatalf("stderr should mention subcommand requirement: %q", stderr)
	}
}

func TestDaemonHelpListsSubcommands(t *testing.T) {
	ctrl := &fakeController{}
	stdout, _, err := runDaemon(ctrl, "daemon", "--help")
	if err != nil {
		t.Fatalf("daemon --help error: %v", err)
	}
	for _, want := range []string{"status", "stop"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("daemon help %q missing subcommand %q", stdout, want)
		}
	}
}

func TestRootHelpListsDaemon(t *testing.T) {
	ctrl := &fakeController{}
	stdout, _, err := runDaemon(ctrl, "--help")
	if err != nil {
		t.Fatalf("--help error: %v", err)
	}
	if !strings.Contains(stdout, "daemon") {
		t.Fatalf("root help missing daemon command: %q", stdout)
	}
}
