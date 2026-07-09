package daemon

import (
	"context"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeTicker is a manually-driven ticker for hermetic reaper-loop tests.
type fakeTicker struct {
	ch chan time.Time
}

func newFakeTicker() *fakeTicker { return &fakeTicker{ch: make(chan time.Time, 1)} }

func (f *fakeTicker) C() <-chan time.Time { return f.ch }
func (f *fakeTicker) Stop()               {}

// tick delivers a single tick to the reaper loop.
func (f *fakeTicker) tick() { f.ch <- time.Unix(0, 0) }

// clock is a controllable time source shared by the now seam and Chtimes.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func newClock() *clock { return &clock{now: time.Unix(1_000_000, 0)} }

func (c *clock) get() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

// newReaperManager builds a Manager wired for reaper tests: a temp cache dir,
// a controllable clock, and injected process seams so nothing real is spawned
// or signalled.
func newReaperManager(t *testing.T) (*Manager, *clock) {
	t.Helper()
	clk := newClock()
	m := &Manager{
		Config:       Config{Host: "127.0.0.1", Port: 8099, IdleTimeout: 10 * time.Second, CacheDir: t.TempDir()},
		ReaperPoll:   time.Second,
		PollInterval: time.Millisecond,
		StopTimeout:  50 * time.Millisecond,
	}
	m.now = clk.get
	m.health = func(context.Context) error { return nil }
	m.sleep = func(context.Context, time.Duration) {}
	m.spawnReaper = func(context.Context) error { return nil }
	return m, clk
}

func TestReapTickStopsIdleDaemon(t *testing.T) {
	m, clk := newReaperManager(t)
	const pid = 4242
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	alive := int32(1)
	var termed int32
	m.procAlive = func(p int) bool { return p == pid && atomic.LoadInt32(&alive) == 1 }
	m.signalProc = func(p int, sig os.Signal) error {
		if p == pid && sig == syscall.SIGTERM {
			atomic.StoreInt32(&termed, 1)
			atomic.StoreInt32(&alive, 0)
		}
		return nil
	}
	// Record activity "now", then let the clock cross the idle timeout.
	if err := m.TouchActivity(); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	clk.advance(11 * time.Second)

	done, err := m.reapTick(context.Background())
	if err != nil {
		t.Fatalf("reapTick error: %v", err)
	}
	if !done {
		t.Fatal("expected reapTick to report done after idle timeout exceeded")
	}
	if atomic.LoadInt32(&termed) != 1 {
		t.Fatal("expected the idle daemon to be signalled with SIGTERM")
	}
	if _, ok := m.activityTime(); ok {
		t.Fatal("expected activity file removed after reap")
	}
}

func TestReapTickKeepsActiveDaemon(t *testing.T) {
	m, clk := newReaperManager(t)
	const pid = 555
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	m.procAlive = func(p int) bool { return p == pid }
	m.signalProc = func(int, os.Signal) error {
		t.Fatal("must not signal a recently-active daemon")
		return nil
	}
	if err := m.TouchActivity(); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	clk.advance(3 * time.Second) // still within the 10s idle timeout

	done, err := m.reapTick(context.Background())
	if err != nil {
		t.Fatalf("reapTick error: %v", err)
	}
	if done {
		t.Fatal("expected reapTick to keep an active daemon alive")
	}
}

func TestReapTickExitsWhenDaemonGone(t *testing.T) {
	m, _ := newReaperManager(t)
	// No PID file at all: the daemon we watched is gone.
	m.procAlive = func(int) bool { return false }
	m.signalProc = func(int, os.Signal) error {
		t.Fatal("must not signal when no daemon is tracked")
		return nil
	}
	done, err := m.reapTick(context.Background())
	if err != nil {
		t.Fatalf("reapTick error: %v", err)
	}
	if !done {
		t.Fatal("expected reapTick to exit when the daemon is gone")
	}
}

func TestReapTickSeedsActivityWhenMissing(t *testing.T) {
	m, _ := newReaperManager(t)
	const pid = 777
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	m.procAlive = func(p int) bool { return p == pid }
	m.signalProc = func(int, os.Signal) error {
		t.Fatal("must not reap on the first tick with no prior activity")
		return nil
	}
	done, err := m.reapTick(context.Background())
	if err != nil {
		t.Fatalf("reapTick error: %v", err)
	}
	if done {
		t.Fatal("expected reapTick to keep running after seeding activity")
	}
	if _, ok := m.activityTime(); !ok {
		t.Fatal("expected activity file to be seeded on first tick")
	}
}

func TestRunReaperStopsAfterIdle(t *testing.T) {
	m, clk := newReaperManager(t)
	ft := newFakeTicker()
	m.newTicker = func(time.Duration) ticker { return ft }

	const pid = 9090
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	alive := int32(1)
	var termed int32
	m.procAlive = func(p int) bool { return p == pid && atomic.LoadInt32(&alive) == 1 }
	m.signalProc = func(p int, sig os.Signal) error {
		if p == pid && sig == syscall.SIGTERM {
			atomic.StoreInt32(&termed, 1)
			atomic.StoreInt32(&alive, 0)
		}
		return nil
	}
	if err := m.TouchActivity(); err != nil {
		t.Fatalf("TouchActivity: %v", err)
	}
	clk.advance(11 * time.Second)

	errCh := make(chan error, 1)
	go func() { errCh <- m.RunReaper(context.Background()) }()
	ft.tick()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("RunReaper error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("RunReaper did not exit after reaping the idle daemon")
	}
	if atomic.LoadInt32(&termed) != 1 {
		t.Fatal("expected the idle daemon to be reaped")
	}
	if _, ok := m.readReaperPIDFile(); ok {
		t.Fatal("expected reaper PID file removed on exit")
	}
}

func TestRunReaperDisabledWhenIdleTimeoutZero(t *testing.T) {
	m, _ := newReaperManager(t)
	m.Config.IdleTimeout = 0
	m.newTicker = func(time.Duration) ticker {
		t.Fatal("ticker must not be created when idle shutdown is disabled")
		return nil
	}
	if err := m.RunReaper(context.Background()); err != nil {
		t.Fatalf("RunReaper error: %v", err)
	}
}

func TestReaperIntervalPositiveGuard(t *testing.T) {
	// ReaperPoll unset + tiny IdleTimeout must still yield a positive interval.
	m := &Manager{Config: Config{IdleTimeout: 2 * time.Second}}
	m.applyDefaults()
	if got := m.reaperInterval(); got <= 0 {
		t.Fatalf("reaperInterval = %v, want > 0", got)
	}
	if got := m.reaperInterval(); got != 2*time.Second {
		t.Fatalf("reaperInterval = %v, want clamped to IdleTimeout 2s", got)
	}
	// Zero IdleTimeout: still positive (never panics NewTicker).
	m2 := &Manager{Config: Config{IdleTimeout: 0}}
	m2.applyDefaults()
	if got := m2.reaperInterval(); got <= 0 {
		t.Fatalf("reaperInterval (zero idle) = %v, want > 0", got)
	}
}

func TestEnsureRunningStartsReaperOnManagedSpawn(t *testing.T) {
	m := newTestManager(t)
	var reaperStarts int32
	m.spawnReaper = func(context.Context) error {
		atomic.AddInt32(&reaperStarts, 1)
		return nil
	}
	spawned := int32(0)
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return newFakeProcess(321), nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errObj("down")
		}
		return nil
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if atomic.LoadInt32(&reaperStarts) != 1 {
		t.Fatalf("expected exactly one reaper launch on managed spawn, got %d", reaperStarts)
	}
}

func TestEnsureRunningNoReaperOnUnmanagedPortConflict(t *testing.T) {
	m := newTestManager(t)
	var reaperStarts int32
	m.spawnReaper = func(context.Context) error {
		atomic.AddInt32(&reaperStarts, 1)
		return nil
	}
	m.health = func(context.Context) error { return nil } // already healthy, but unmanaged
	if err := m.EnsureRunning(context.Background()); err == nil {
		t.Fatal("expected unmanaged port conflict")
	}
	if atomic.LoadInt32(&reaperStarts) != 0 {
		t.Fatalf("expected no reaper launch for unmanaged conflict, got %d", reaperStarts)
	}
}

func TestMaybeStartReaperSkipsWhenReaperAlive(t *testing.T) {
	m, _ := newReaperManager(t)
	if err := m.writeReaperPIDFile(31337); err != nil {
		t.Fatalf("writeReaperPIDFile: %v", err)
	}
	m.procAlive = func(int) bool { return true } // an existing reaper is alive
	var starts int32
	m.spawnReaper = func(context.Context) error {
		atomic.AddInt32(&starts, 1)
		return nil
	}
	m.maybeStartReaper(context.Background())
	if atomic.LoadInt32(&starts) != 0 {
		t.Fatal("expected maybeStartReaper to skip when a live reaper already exists")
	}
}

// errObj is a tiny error helper local to this test file.
type errObj string

func (e errObj) Error() string { return string(e) }
