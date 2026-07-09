package daemon

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// DefaultReaperPoll is the default idle-reaper tick cadence. The reaper wakes on
// this cadence to compare the daemon's last-activity timestamp against
// IdleTimeout. It is clamped so it never exceeds IdleTimeout.
const DefaultReaperPoll = 15 * time.Second

// ticker abstracts a periodic timer so the idle-reaper loop can be driven by a
// fake in tests. The production implementation wraps time.Ticker.
type ticker interface {
	// C returns the channel on which ticks are delivered.
	C() <-chan time.Time
	// Stop halts the ticker and releases its resources.
	Stop()
}

// realTicker is the production ticker backed by time.Ticker.
type realTicker struct{ t *time.Ticker }

func (r *realTicker) C() <-chan time.Time { return r.t.C }
func (r *realTicker) Stop()               { r.t.Stop() }

// ActivityFilePath returns the location of the daemon activity timestamp file
// within the cache directory. Its mtime records the last semantic activity so a
// detached reaper (a separate process) can compute idleness across CLI
// invocations, replacing the in-process Touch clock for the cross-process case.
func (c Config) ActivityFilePath() string {
	cacheDir := c.CacheDir
	if strings.TrimSpace(cacheDir) == "" {
		cacheDir = defaultCacheDir()
	}
	return filepath.Join(cacheDir, "daemon.activity")
}

// TouchActivity records semantic activity by stamping the activity file's mtime
// with the current (seam) time, creating it and parent directories as needed. It
// is the cross-process analogue of Touch: any process can call it and the
// detached reaper observes the updated mtime.
func (m *Manager) TouchActivity() error {
	m.applyDefaults()
	path := m.Config.ActivityFilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // path is the daemon activity file under the configured cache directory.
	if err != nil {
		return err
	}
	_ = f.Close()
	now := m.now()
	return os.Chtimes(path, now, now)
}

// activityTime returns the last-activity time recorded in the activity file. The
// bool result reports whether an activity record exists.
func (m *Manager) activityTime() (time.Time, bool) {
	info, err := os.Stat(m.Config.ActivityFilePath())
	if err != nil {
		return time.Time{}, false
	}
	return info.ModTime(), true
}

// removeActivityFile deletes the activity file, ignoring a missing file.
func (m *Manager) removeActivityFile() {
	_ = os.Remove(m.Config.ActivityFilePath())
}

// reaperInterval computes a positive tick cadence for the reaper loop. It uses
// ReaperPoll, clamped so it never exceeds IdleTimeout, and guards against a
// non-positive value (which would panic time.NewTicker).
func (m *Manager) reaperInterval() time.Duration {
	poll := m.ReaperPoll
	if poll <= 0 {
		poll = DefaultReaperPoll
	}
	if m.Config.IdleTimeout > 0 && m.Config.IdleTimeout < poll {
		poll = m.Config.IdleTimeout
	}
	if poll <= 0 {
		poll = time.Second
	}
	return poll
}

// maybeStartReaper launches the detached idle-reaper subprocess for a managed
// daemon spawn. It is a no-op when idle shutdown is disabled or when a live
// reaper is already tracked, ensuring only one reaper exists per daemon.
func (m *Manager) maybeStartReaper(ctx context.Context) {
	m.applyDefaults()
	if m.Config.IdleTimeout <= 0 {
		return
	}
	if pid, ok := m.readReaperPIDFile(); ok && m.procAlive(pid) {
		return // a reaper is already watching this daemon
	}
	_ = m.spawnReaper(ctx)
}

// defaultSpawnReaper launches a detached `ajq daemon __reap` subprocess that
// outlives the current CLI and owns the idle loop for the spawned daemon. The
// child is detached into its own session so it is not torn down with the
// short-lived parent, and the cache directory is propagated so the reaper
// resolves the same PID/activity files.
func (m *Manager) defaultSpawnReaper(_ context.Context) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Deliberately not exec.CommandContext: the reaper must outlive this CLI.
	cmd := exec.Command(exe, "daemon", "__reap") //nolint:gosec,noctx // exe is the current ajq executable and this detached reaper intentionally outlives the parent context.
	cmd.Env = append(os.Environ(), EnvCacheDir+"="+m.Config.CacheDir)
	cmd.SysProcAttr = detachedSysProcAttr()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return err
	}
	// Release so we don't hold a zombie/handle for the detached child.
	return cmd.Process.Release()
}

// RunReaper runs the idle-reaper loop until the daemon is stopped, the daemon
// disappears, or ctx is cancelled. It is intended to be the body of the hidden
// `ajq daemon __reap` subcommand: a long-lived process that shares the daemon's
// lifetime. When IdleTimeout is non-positive, idle shutdown is disabled and the
// loop returns immediately.
//
// On each tick it stops the daemon (via the PID file left by the spawning
// process) once now-lastActivity meets or exceeds IdleTimeout, and exits once
// the daemon is gone.
func (m *Manager) RunReaper(ctx context.Context) error {
	m.applyDefaults()
	if m.Config.IdleTimeout <= 0 {
		return nil
	}

	// Track this reaper so a concurrent spawn does not launch a duplicate.
	_ = m.writeReaperPIDFile(os.Getpid())
	defer m.removeReaperPIDFile()

	tk := m.newTicker(m.reaperInterval())
	defer tk.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tk.C():
			done, err := m.reapTick(ctx)
			if done {
				return err
			}
		}
	}
}

// reapTick performs a single reaper evaluation. It returns done=true when the
// loop should exit: either the daemon is gone, or it was reaped for being idle.
func (m *Manager) reapTick(ctx context.Context) (bool, error) {
	pid, ok := m.readPIDFile()
	if !ok || !m.procAlive(pid) {
		// The daemon we were watching is gone; nothing left to reap.
		m.removeActivityFile()
		return true, nil
	}

	last, ok := m.activityTime()
	if !ok {
		// No activity recorded yet; seed it so we don't reap immediately.
		_ = m.TouchActivity()
		return false, nil
	}

	if m.now().Sub(last) < m.Config.IdleTimeout {
		return false, nil
	}

	_, err := m.stopByPIDFile(ctx)
	m.removeActivityFile()
	return true, err
}
