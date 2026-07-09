package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// State enumerates the lifecycle states of the managed daemon process.
type State int

const (
	// StateStopped means no managed process is running.
	StateStopped State = iota
	// StateStarting means a spawn is in progress and health is not yet confirmed.
	StateStarting
	// StateRunning means the daemon is up and passing health checks.
	StateRunning
	// StateFailed means the last spawn attempt failed.
	StateFailed
)

// String renders a stable, machine-friendly lowercase state name.
func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Default lifecycle timing constants. They are overridable via Manager fields
// so tests can avoid real long waits.
const (
	// DefaultStartupTimeout bounds how long EnsureRunning waits for health.
	DefaultStartupTimeout = 30 * time.Second
	// DefaultPollInterval is the health poll cadence during startup.
	DefaultPollInterval = 100 * time.Millisecond
	// DefaultStopTimeout bounds how long Stop waits for graceful exit before
	// escalating to a hard kill.
	DefaultStopTimeout = 5 * time.Second
)

// processHandle abstracts a spawned OS process so tests can substitute a fake.
type processHandle interface {
	// Pid returns the process ID.
	Pid() int
	// Signal delivers a signal to the process.
	Signal(sig os.Signal) error
	// Wait blocks until the process exits.
	Wait() error
	// Kill forcibly terminates the process.
	Kill() error
}

// execProcess is the production processHandle backed by os/exec.
type execProcess struct {
	cmd *exec.Cmd
}

func (p *execProcess) Pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

func (p *execProcess) Signal(sig os.Signal) error {
	if p.cmd.Process == nil {
		return fmt.Errorf("process not started")
	}
	return p.cmd.Process.Signal(sig)
}

func (p *execProcess) Wait() error { return p.cmd.Wait() }

func (p *execProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}

// Snapshot is an immutable view of the daemon's current state, safe to render
// for status output.
type Snapshot struct {
	State        State
	PID          int
	Address      string
	BaseURL      string
	External     bool
	LastActivity time.Time
}

// Manager owns the lifecycle of the local llama-server daemon. It is safe for
// concurrent use. All external effects (spawning, health checks, time) are
// injectable to keep tests fast and hermetic.
type Manager struct {
	Config     Config
	Discoverer Discoverer

	// StartupTimeout bounds EnsureRunning health waiting. Defaults to
	// DefaultStartupTimeout when zero.
	StartupTimeout time.Duration
	// PollInterval is the health poll cadence during startup. Defaults to
	// DefaultPollInterval when zero.
	PollInterval time.Duration
	// StopTimeout bounds graceful shutdown before a hard kill. Defaults to
	// DefaultStopTimeout when zero.
	StopTimeout time.Duration
	// ReaperPoll is the idle-reaper tick cadence. Defaults to
	// DefaultReaperPoll when zero and is clamped so it never exceeds
	// IdleTimeout (with a positive-interval guard).
	ReaperPoll time.Duration

	// starter spawns the server process. Defaults to a real os/exec spawn.
	starter func(ctx context.Context, path string, args []string) (processHandle, error)
	// health probes daemon readiness. Defaults to an HTTP GET of /health.
	health func(ctx context.Context) error
	// now returns the current time. Defaults to time.Now.
	now func() time.Time
	// sleep waits for d (respecting ctx). Defaults to a context-aware sleep.
	sleep func(ctx context.Context, d time.Duration)
	// signalProc delivers a signal to an arbitrary PID (cross-process stop).
	// Defaults to os.FindProcess + Signal.
	signalProc func(pid int, sig os.Signal) error
	// procAlive reports whether a PID is alive. Defaults to a signal-0 probe.
	procAlive func(pid int) bool
	// newTicker builds a ticker for the idle-reaper loop. Defaults to a
	// time.Ticker wrapper. Tests inject a fake to drive ticks deterministically.
	newTicker func(d time.Duration) ticker
	// spawnReaper launches the detached idle-reaper subprocess. Defaults to a
	// detached `ajq daemon __reap` spawn. Tests inject a fake to avoid spawning.
	spawnReaper func(ctx context.Context) error

	mu           sync.Mutex
	state        State
	proc         processHandle
	external     bool
	apiKey       string
	lastActivity time.Time
	lastErr      error
}

// NewManager builds a Manager with production defaults for all injectable
// seams. Callers may override exported timing fields after construction.
func NewManager(cfg Config) *Manager {
	m := &Manager{Config: cfg}
	m.applyDefaults()
	return m
}

// applyDefaults wires production implementations for any unset seams. It is
// idempotent and invoked lazily so zero-value Managers (as used in tests with
// partial injection) still work.
func (m *Manager) applyDefaults() {
	if m.StartupTimeout == 0 {
		m.StartupTimeout = DefaultStartupTimeout
	}
	if m.PollInterval == 0 {
		m.PollInterval = DefaultPollInterval
	}
	if m.StopTimeout == 0 {
		m.StopTimeout = DefaultStopTimeout
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.sleep == nil {
		m.sleep = func(ctx context.Context, d time.Duration) {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
			case <-t.C:
			}
		}
	}
	if m.health == nil {
		m.health = m.httpHealth
	}
	if m.starter == nil {
		m.starter = defaultStarter
	}
	if m.signalProc == nil {
		m.signalProc = defaultSignalProc
	}
	if m.procAlive == nil {
		m.procAlive = defaultProcAlive
	}
	if m.ReaperPoll == 0 {
		m.ReaperPoll = DefaultReaperPoll
	}
	if m.newTicker == nil {
		m.newTicker = func(d time.Duration) ticker { return &realTicker{t: time.NewTicker(d)} }
	}
	if m.spawnReaper == nil {
		m.spawnReaper = m.defaultSpawnReaper
	}
}

// defaultStarter spawns the server with os/exec, binding localhost only and
// never invoking a shell.
func defaultStarter(ctx context.Context, path string, args []string) (processHandle, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	// Explicitly avoid shell interpretation: exec.Command does not use a shell.
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &execProcess{cmd: cmd}, nil
}

// httpHealth performs an HTTP GET against the daemon's /health endpoint.
func (m *Manager) httpHealth(ctx context.Context) error {
	url := m.Config.BaseURL() + "/health"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("health check returned status %d", resp.StatusCode)
	}
	return nil
}

// spawnArgs builds the argv for the server process, binding localhost only.
func (m *Manager) spawnArgs() []string {
	cfg := m.Config
	host := cfg.Host
	if host == "" {
		host = DefaultHost
	}
	port := cfg.Port
	if port == 0 {
		port = DefaultPort
	}
	parallelSlots := cfg.ParallelSlots
	if parallelSlots == 0 {
		parallelSlots = DefaultParallelSlots
	}
	args := []string{"--host", host, "--port", fmt.Sprintf("%d", port), "--parallel", fmt.Sprintf("%d", parallelSlots)}
	if key := m.APIKey(); key != "" {
		args = append(args, "--api-key", key)
	}
	if cfg.ModelPath != "" {
		args = append(args, "--model", cfg.ModelPath)
	}
	return args
}

// EnsureRunning lazily brings the daemon up. If a managed process is already
// running it returns immediately. If a previously managed daemon is answering
// health checks and still has PID/key evidence on disk, it is reused without
// spawning. Otherwise the binary is discovered, spawned, and health-polled
// until ready or StartupTimeout.
func (m *Manager) EnsureRunning(ctx context.Context) error {
	m.applyDefaults()

	if err := m.Config.Validate(); err != nil {
		return err
	}

	m.mu.Lock()
	if m.state == StateRunning && m.proc != nil {
		m.lastActivity = m.now()
		m.mu.Unlock()
		return nil
	}
	m.mu.Unlock()

	// Already-running detection: a healthy endpoint is reusable as an ajq-managed
	// warm daemon only when PID/key evidence is present. A health-only responder
	// on the managed daemon address is a port conflict, not trusted adoption.
	if err := m.health(ctx); err == nil {
		key, managed := m.warmManagedAPIKey()
		if !managed {
			err := fmt.Errorf("local daemon address %s is already in use by an unmanaged healthy server; pass --base-url explicitly to trust an external llama-server", m.Config.BaseURL())
			m.setFailed(err)
			return err
		}
		m.mu.Lock()
		m.apiKey = key
		m.state = StateRunning
		m.external = false
		m.lastActivity = m.now()
		m.lastErr = nil
		m.mu.Unlock()
		return nil
	}

	binPath, err := m.Discoverer.DiscoverServerBinary(m.Config)
	if err != nil {
		m.setFailed(err)
		return err
	}

	key, err := m.createAPIKeyFile()
	if err != nil {
		wrapped := fmt.Errorf("failed to prepare daemon API key: %w", err)
		m.setFailed(wrapped)
		return wrapped
	}
	m.mu.Lock()
	m.apiKey = key
	m.state = StateStarting
	m.mu.Unlock()

	proc, err := m.starter(ctx, binPath, m.spawnArgs())
	if err != nil {
		m.removeAPIKeyFile()
		wrapped := fmt.Errorf("failed to spawn %s: %w", binPath, err)
		m.setFailed(wrapped)
		return wrapped
	}

	if err := m.waitForHealth(ctx); err != nil {
		_ = proc.Kill()
		_ = proc.Wait()
		m.removeAPIKeyFile()
		m.setFailed(err)
		return err
	}

	m.mu.Lock()
	m.proc = proc
	m.external = false
	m.apiKey = key
	m.state = StateRunning
	m.lastActivity = m.now()
	m.lastErr = nil
	pid := proc.Pid()
	m.mu.Unlock()

	// Track the PID so a later, separate `ajq daemon stop` invocation can
	// terminate this warm daemon even though it holds no in-process handle.
	if err := m.writePIDFile(pid); err != nil {
		m.mu.Lock()
		m.lastErr = fmt.Errorf("daemon started but PID file write failed: %w", err)
		m.mu.Unlock()
	}

	// Launch the detached idle-reaper that outlives this short-lived CLI and
	// self-terminates the daemon after IdleTimeout. Only the managed spawn path
	// owns a reaper; adopted external daemons are left to their owner.
	m.maybeStartReaper(ctx)
	return nil
}

// waitForHealth polls health until success, StartupTimeout, or ctx cancel.
func (m *Manager) waitForHealth(ctx context.Context) error {
	deadline := m.now().Add(m.StartupTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := m.health(ctx); err == nil {
			return nil
		}
		if !m.now().Before(deadline) {
			return fmt.Errorf("daemon did not become healthy within %s", m.StartupTimeout)
		}
		m.sleep(ctx, m.PollInterval)
	}
}

// setFailed records a failure state and error under lock.
func (m *Manager) setFailed(err error) {
	m.mu.Lock()
	m.state = StateFailed
	m.apiKey = ""
	m.lastErr = err
	m.mu.Unlock()
}

// Stop terminates the managed daemon. It is idempotent: it returns
// (stopped=false) with no error when nothing was running. It attempts a
// graceful SIGTERM, waits up to StopTimeout, then escalates to a hard Kill.
// Adopted external processes are not killed; Stop only clears local state and
// reports stopped=false.
func (m *Manager) Stop(ctx context.Context) (bool, error) {
	m.applyDefaults()

	m.mu.Lock()
	proc := m.proc
	external := m.external
	running := m.state == StateRunning || m.state == StateStarting
	m.proc = nil
	m.external = false
	m.apiKey = ""
	m.state = StateStopped
	m.mu.Unlock()

	if proc == nil {
		_ = external
		_ = running
		// No in-process handle: attempt a cross-process stop using the PID file
		// left behind by a previous `ajq` invocation that spawned the daemon.
		return m.stopByPIDFile(ctx)
	}

	// Graceful termination of the in-process managed daemon.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// If signalling failed, escalate straight to kill. A failed graceful
		// signal is tolerated only when the hard kill succeeds and the process
		// can still be reaped.
		if killErr := proc.Kill(); killErr != nil {
			return true, errors.Join(
				shutdownErr("signal daemon with SIGTERM", err),
				shutdownErr("kill daemon after SIGTERM failure", killErr),
			)
		}
		waitErr := proc.Wait()
		m.removePIDFile()
		if waitErr != nil {
			return true, errors.Join(
				shutdownErr("signal daemon with SIGTERM", err),
				shutdownErr("wait for daemon after hard kill", waitErr),
			)
		}
		return true, nil
	}

	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()

	timer := time.NewTimer(m.StopTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		m.removePIDFile()
		return true, shutdownErr("wait for daemon after SIGTERM", err)
	case <-timer.C:
		if killErr := proc.Kill(); killErr != nil {
			return true, shutdownErr("kill daemon after stop timeout", killErr)
		}
		waitErr := <-done
		m.removePIDFile()
		return true, shutdownErr("wait for daemon after hard kill", waitErr)
	case <-ctx.Done():
		if killErr := proc.Kill(); killErr != nil {
			return true, errors.Join(ctx.Err(), shutdownErr("kill daemon after stop cancellation", killErr))
		}
		waitErr := <-done
		m.removePIDFile()
		return true, errors.Join(ctx.Err(), shutdownErr("wait for daemon after stop cancellation", waitErr))
	}
}

// stopByPIDFile terminates a daemon tracked only by the PID file (spawned by a
// separate process). Before signalling, it requires both a live PID and a
// healthy response from the configured daemon endpoint; failures are treated as
// stale PID evidence to reduce PID-reuse hazards. It then sends SIGTERM, waits
// up to StopTimeout for the process to exit, and escalates to SIGKILL. Stale PID
// files are cleaned up and reported as stopped=false. It returns stopped=true
// only when a live, healthy process was signalled.
func shutdownErr(action string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", action, err)
}

func (m *Manager) stopByPIDFile(ctx context.Context) (bool, error) {
	pid, ok := m.readPIDFile()
	if !ok {
		return false, nil
	}
	if !m.procAlive(pid) {
		// Stale PID file; clean it up.
		m.removePIDFile()
		return false, nil
	}
	if err := m.health(ctx); err != nil {
		// A live PID without a healthy daemon endpoint may be PID reuse; do not signal it.
		m.removePIDFile()
		return false, nil
	}

	if err := m.signalProc(pid, syscall.SIGTERM); err != nil {
		// Escalate to a hard kill. The failed graceful signal is tolerated only
		// when the hard-kill signal succeeds.
		if killErr := m.signalProc(pid, syscall.SIGKILL); killErr != nil {
			return true, errors.Join(
				shutdownErr("signal PID-file daemon with SIGTERM", err),
				shutdownErr("signal PID-file daemon with SIGKILL after SIGTERM failure", killErr),
			)
		}
		m.removePIDFile()
		return true, nil
	}

	deadline := m.now().Add(m.StopTimeout)
	for m.procAlive(pid) {
		if err := ctx.Err(); err != nil {
			if killErr := m.signalProc(pid, syscall.SIGKILL); killErr != nil {
				return true, errors.Join(err, shutdownErr("signal PID-file daemon with SIGKILL after stop cancellation", killErr))
			}
			m.removePIDFile()
			return true, err
		}
		if !m.now().Before(deadline) {
			if killErr := m.signalProc(pid, syscall.SIGKILL); killErr != nil {
				return true, shutdownErr("signal PID-file daemon with SIGKILL after stop timeout", killErr)
			}
			break
		}
		m.sleep(ctx, m.PollInterval)
	}
	m.removePIDFile()
	return true, nil
}

// Touch records daemon activity, resetting the idle clock.
func (m *Manager) Touch() {
	m.applyDefaults()
	m.mu.Lock()
	m.lastActivity = m.now()
	m.mu.Unlock()
}

// IdleDuration returns how long the daemon has been idle since the last
// activity. It returns 0 when the daemon is not running.
func (m *Manager) IdleDuration() time.Duration {
	m.applyDefaults()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != StateRunning || m.lastActivity.IsZero() {
		return 0
	}
	return m.now().Sub(m.lastActivity)
}

// ShutdownIfIdle stops the daemon when it has been idle beyond IdleTimeout. It
// returns (true, err) when a shutdown was attempted. A non-positive IdleTimeout
// disables idle shutdown.
func (m *Manager) ShutdownIfIdle(ctx context.Context) (bool, error) {
	m.applyDefaults()
	if m.Config.IdleTimeout <= 0 {
		return false, nil
	}
	m.mu.Lock()
	idle := time.Duration(0)
	if m.state == StateRunning && !m.lastActivity.IsZero() {
		idle = m.now().Sub(m.lastActivity)
	}
	running := m.state == StateRunning
	m.mu.Unlock()

	if !running || idle < m.Config.IdleTimeout {
		return false, nil
	}
	_, err := m.Stop(ctx)
	return true, err
}

// Status returns a snapshot of the daemon's current state.
func (m *Manager) Status() Snapshot {
	m.applyDefaults()
	m.mu.Lock()
	defer m.mu.Unlock()
	pid := 0
	if m.proc != nil {
		pid = m.proc.Pid()
	}
	return Snapshot{
		State:        m.state,
		PID:          pid,
		Address:      m.Config.Address(),
		BaseURL:      m.Config.BaseURL(),
		External:     m.external,
		LastActivity: m.lastActivity,
	}
}

// Probe performs a single health check without spawning anything and returns a
// Snapshot reflecting live reachability. If the daemon is reachable it is
// reported as running; when no managed process is tracked locally it is marked
// External (i.e. started outside this process). If unreachable, the current
// tracked state is returned. Probe does not mutate persisted lifecycle state.
func (m *Manager) Probe(ctx context.Context) Snapshot {
	m.applyDefaults()
	reachable := m.health(ctx) == nil

	m.mu.Lock()
	defer m.mu.Unlock()
	pid := 0
	if m.proc != nil {
		pid = m.proc.Pid()
	}
	snap := Snapshot{
		State:        m.state,
		PID:          pid,
		Address:      m.Config.Address(),
		BaseURL:      m.Config.BaseURL(),
		External:     m.external,
		LastActivity: m.lastActivity,
	}
	if reachable {
		snap.State = StateRunning
		if m.proc == nil {
			snap.External = true
		}
	}
	return snap
}

// LastError returns the most recent lifecycle error, if any.
// APIKey returns the non-rendered bearer token for the managed daemon, if one
// is known. It is intentionally kept out of Snapshot/status rendering.
func (m *Manager) APIKey() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.apiKey
}

func (m *Manager) LastError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastErr
}
