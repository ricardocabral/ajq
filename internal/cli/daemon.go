package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/ricardocabral/ajq/internal/daemon"
	"github.com/spf13/cobra"
)

// DaemonController is the injectable seam the daemon CLI commands operate
// against. The production implementation wraps a *daemon.Manager; tests provide
// a fake to exercise command behavior without spawning processes.
type DaemonController interface {
	// Status returns a snapshot of the current daemon state (may probe health).
	Status(ctx context.Context) daemon.Snapshot
	// Stop terminates the daemon, returning whether one was stopped.
	Stop(ctx context.Context) (bool, error)
}

// managerController is the default DaemonController backed by a daemon.Manager.
type managerController struct {
	m *daemon.Manager
}

func (c *managerController) Status(ctx context.Context) daemon.Snapshot {
	return c.m.Probe(ctx)
}

func (c *managerController) Stop(ctx context.Context) (bool, error) {
	return c.m.Stop(ctx)
}

// defaultDaemonController builds the production controller with localhost
// defaults.
func defaultDaemonController() DaemonController {
	return &managerController{m: daemon.NewManager(daemon.DefaultConfig())}
}

// resolveDaemonController returns the injected controller or the default.
func resolveDaemonController(opts Options) DaemonController {
	if opts.Daemon != nil {
		return opts.Daemon
	}
	return defaultDaemonController()
}

// newDaemonCommand builds the `ajq daemon` command tree.
func newDaemonCommand(opts Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:           "daemon",
		Short:         "manage the local llama-server daemon",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// No subcommand given: show help on stderr with a non-zero exit.
			return &ExitError{Code: 2, Err: fmt.Errorf("daemon requires a subcommand: status or stop")}
		},
	}

	cmd.AddCommand(newDaemonStatusCommand(opts))
	cmd.AddCommand(newDaemonStopCommand(opts))
	cmd.AddCommand(newDaemonReapCommand())
	return cmd
}

// newDaemonReapCommand implements the hidden `ajq daemon __reap` subcommand. It
// is launched detached by the daemon manager when a managed daemon is spawned
// and runs the long-lived idle-reaper loop, sharing the daemon's lifetime and
// self-terminating it after the configured idle timeout. It is not intended for
// direct end-user invocation, hence Hidden.
func newDaemonReapCommand() *cobra.Command {
	return &cobra.Command{
		Use:           "__reap",
		Short:         "internal: run the daemon idle-reaper loop",
		Hidden:        true,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			mgr := daemon.NewManager(daemon.DefaultConfig())
			if err := mgr.RunReaper(cmd.Context()); err != nil && !errors.Is(err, context.Canceled) {
				return &ExitError{Code: 1, Err: fmt.Errorf("daemon reaper: %w", err)}
			}
			return nil
		},
	}
}

// newDaemonStatusCommand implements `ajq daemon status`.
func newDaemonStatusCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:           "status",
		Short:         "print the local daemon status",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			controller := resolveDaemonController(opts)
			snap := controller.Status(cmd.Context())
			if err := writeDaemonStatus(cmd.OutOrStdout(), snap); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write daemon status: %w", err)}
			}
			return nil
		},
	}
}

// newDaemonStopCommand implements `ajq daemon stop`.
func newDaemonStopCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:           "stop",
		Short:         "stop the local daemon if running (idempotent)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			controller := resolveDaemonController(opts)
			stopped, err := controller.Stop(cmd.Context())
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("failed to stop daemon: %w", err)}
			}
			if err := writeDaemonStopStatus(cmd.OutOrStdout(), stopped); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write daemon stop status: %w", err)}
			}
			return nil
		},
	}
}

// writeDaemonStatus renders a stable, machine- and human-readable status block.
func writeDaemonStatus(w io.Writer, snap daemon.Snapshot) error {
	if _, err := fmt.Fprintf(w, "state: %s\n", snap.State); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "pid: %d\n", snap.PID); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "address: %s\n", snap.Address); err != nil {
		return err
	}
	_, err := fmt.Fprintf(w, "external: %t\n", snap.External)
	return err
}

func writeDaemonStopStatus(w io.Writer, stopped bool) error {
	_, err := fmt.Fprintf(w, "stopped: %t\n", stopped)
	return err
}
