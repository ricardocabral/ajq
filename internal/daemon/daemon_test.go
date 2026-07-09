package daemon

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// fakeProcess is a controllable processHandle for tests.
type fakeProcess struct {
	pid        int
	mu         sync.Mutex
	signals    []os.Signal
	killed     bool
	waitCh     chan struct{}
	waitOnce   sync.Once
	signalFail bool
}

func newFakeProcess(pid int) *fakeProcess {
	return &fakeProcess{pid: pid, waitCh: make(chan struct{})}
}

func (f *fakeProcess) Pid() int { return f.pid }

func (f *fakeProcess) Signal(sig os.Signal) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.signalFail {
		return errors.New("signal failed")
	}
	f.signals = append(f.signals, sig)
	// Graceful signal causes the process to exit.
	if sig == syscall.SIGTERM {
		f.exit()
	}
	return nil
}

func (f *fakeProcess) exit() {
	f.waitOnce.Do(func() { close(f.waitCh) })
}

func (f *fakeProcess) Wait() error {
	<-f.waitCh
	return nil
}

func (f *fakeProcess) Kill() error {
	f.mu.Lock()
	f.killed = true
	f.mu.Unlock()
	f.exit()
	return nil
}

func (f *fakeProcess) sawSignal(sig os.Signal) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.signals {
		if s == sig {
			return true
		}
	}
	return false
}

func (f *fakeProcess) wasKilled() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.killed
}

// newTestManager builds a Manager with a discoverer that resolves to a dummy
// path and injectable fakes, avoiding any real process or network. CacheDir is
// a per-test temp dir so PID files never touch the real filesystem.
func newTestManager(t *testing.T) *Manager {
	t.Helper()
	port := freeTCPPort(t)
	m := &Manager{
		Config: Config{Host: "127.0.0.1", Port: port, IdleTimeout: time.Minute, CacheDir: t.TempDir()},
		Discoverer: Discoverer{
			LookPath:   func(name string) (string, error) { return "/fake/" + name, nil },
			FileExists: func(string) bool { return true },
		},
		PollInterval:   time.Millisecond,
		StartupTimeout: 100 * time.Millisecond,
		StopTimeout:    50 * time.Millisecond,
	}
	// No real sleeping.
	m.sleep = func(context.Context, time.Duration) {}
	// Never spawn a real detached reaper subprocess in unit tests.
	m.spawnReaper = func(context.Context) error { return nil }
	return m
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free TCP port: %v", err)
	}
	defer func() { _ = ln.Close() }()
	addr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address = %T, want *net.TCPAddr", ln.Addr())
	}
	return addr.Port
}

func TestEnsureRunningSpawnsWhenNotHealthy(t *testing.T) {
	m := newTestManager(t)
	var spawnCount int32
	fp := newFakeProcess(4321)
	m.starter = func(ctx context.Context, path string, args []string) (processHandle, error) {
		atomic.AddInt32(&spawnCount, 1)
		// Verify localhost binding args and no shell.
		if !containsPair(args, "--host", "127.0.0.1") {
			t.Errorf("expected --host 127.0.0.1 in args %v", args)
		}
		if !containsPair(args, "--port", strconv.Itoa(m.Config.Port)) {
			t.Errorf("expected --port %d in args %v", m.Config.Port, args)
		}
		if !containsPair(args, "--parallel", "4") {
			t.Errorf("expected --parallel 4 in args %v", args)
		}
		if !containsPair(args, "--api-key", m.APIKey()) {
			t.Errorf("expected --api-key with manager key in args %v", args)
		}
		return fp, nil
	}
	// Health fails until the process spawns, then succeeds.
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawnCount) == 0 {
			return errors.New("not up")
		}
		return nil
	}

	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning error: %v", err)
	}
	if atomic.LoadInt32(&spawnCount) != 1 {
		t.Fatalf("expected exactly one spawn, got %d", spawnCount)
	}
	st := m.Status()
	if st.State != StateRunning {
		t.Fatalf("state = %v, want running", st.State)
	}
	if st.PID != 4321 {
		t.Fatalf("pid = %d, want 4321", st.PID)
	}
	if st.External {
		t.Fatal("expected managed (non-external) process")
	}
}

func TestEnsureRunningSpawnsWithConfiguredParallelSlots(t *testing.T) {
	m := newTestManager(t)
	m.Config.ParallelSlots = 7
	var spawnCount int32
	fp := newFakeProcess(4322)
	m.starter = func(ctx context.Context, path string, args []string) (processHandle, error) {
		atomic.AddInt32(&spawnCount, 1)
		if !containsPair(args, "--parallel", "7") {
			t.Errorf("expected --parallel 7 in args %v", args)
		}
		return fp, nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawnCount) == 0 {
			return errors.New("not up")
		}
		return nil
	}

	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning error: %v", err)
	}
	if atomic.LoadInt32(&spawnCount) != 1 {
		t.Fatalf("expected exactly one spawn, got %d", spawnCount)
	}
}

func TestEnsureRunningRejectsHealthOnlyResponder(t *testing.T) {
	m := newTestManager(t)
	var spawnCount int32
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.AddInt32(&spawnCount, 1)
		return newFakeProcess(1), nil
	}
	m.health = func(context.Context) error { return nil } // already healthy, but unmanaged

	err := m.EnsureRunning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unmanaged healthy server") {
		t.Fatalf("EnsureRunning error = %v, want unmanaged conflict", err)
	}
	if spawnCount != 0 {
		t.Fatalf("expected no spawn for port conflict, got %d", spawnCount)
	}
	if got := m.APIKey(); got != "" {
		t.Fatalf("APIKey after unmanaged conflict = %q, want empty", got)
	}
	st := m.Status()
	if st.State != StateFailed || st.External {
		t.Fatalf("expected failed non-external state, got state=%v external=%v", st.State, st.External)
	}
}

func TestEnsureRunningStartupTimeout(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(10)
	m.starter = func(context.Context, string, []string) (processHandle, error) { return fp, nil }
	m.health = func(context.Context) error { return errors.New("never healthy") }

	err := m.EnsureRunning(context.Background())
	if err == nil {
		t.Fatal("expected startup timeout error")
	}
	if !fp.wasKilled() {
		t.Fatal("expected process to be killed after failed startup")
	}
	if m.Status().State != StateFailed {
		t.Fatalf("state = %v, want failed", m.Status().State)
	}
}

func TestEnsureRunningDiscoveryError(t *testing.T) {
	m := newTestManager(t)
	m.Discoverer = Discoverer{
		LookPath:   func(string) (string, error) { return "", errors.New("nope") },
		FileExists: func(string) bool { return false },
	}
	m.health = func(context.Context) error { return errors.New("down") }
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		t.Fatal("starter should not be called when discovery fails")
		return nil, nil
	}
	err := m.EnsureRunning(context.Background())
	if !IsServerBinaryNotFound(err) {
		t.Fatalf("expected server-binary-not-found, got %v", err)
	}
}

func TestEnsureRunningRejectsNonLoopback(t *testing.T) {
	m := newTestManager(t)
	m.Config.Host = "0.0.0.0"
	err := m.EnsureRunning(context.Background())
	if err == nil {
		t.Fatal("expected non-loopback host to be rejected")
	}
}

func TestStopGraceful(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(55)
	m.starter = func(context.Context, string, []string) (processHandle, error) { return fp, nil }
	healthy := int32(0)
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&healthy) == 0 {
			return errors.New("down")
		}
		return nil
	}
	// Make spawn flip healthy.
	origStarter := m.starter
	m.starter = func(ctx context.Context, p string, a []string) (processHandle, error) {
		atomic.StoreInt32(&healthy, 1)
		return origStarter(ctx, p, a)
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if !stopped {
		t.Fatal("expected stopped=true")
	}
	if !fp.sawSignal(syscall.SIGTERM) {
		t.Fatal("expected SIGTERM to be sent")
	}
	if fp.wasKilled() {
		t.Fatal("did not expect hard kill on graceful stop")
	}
	if m.Status().State != StateStopped {
		t.Fatalf("state = %v, want stopped", m.Status().State)
	}
}

func TestStopIsIdempotent(t *testing.T) {
	m := newTestManager(t)
	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if stopped {
		t.Fatal("expected stopped=false when nothing running")
	}
	// Second call also fine.
	stopped, err = m.Stop(context.Background())
	if err != nil || stopped {
		t.Fatalf("second Stop = (%v, %v), want (false, nil)", stopped, err)
	}
}

func TestStopEscalatesToKillOnTimeout(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(77)
	fp.signalFail = true // SIGTERM fails -> escalate to kill
	m.starter = func(context.Context, string, []string) (processHandle, error) { return fp, nil }
	spawned := int32(0)
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	orig := m.starter
	m.starter = func(ctx context.Context, p string, a []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return orig(ctx, p, a)
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if !stopped {
		t.Fatal("expected stopped=true")
	}
	if !fp.wasKilled() {
		t.Fatal("expected hard kill when SIGTERM fails")
	}
}

func TestIdleTimeoutPlumbing(t *testing.T) {
	m := newTestManager(t)
	m.Config.IdleTimeout = 10 * time.Second

	// Controllable clock.
	var mu sync.Mutex
	fakeNow := time.Unix(1000, 0)
	m.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return fakeNow
	}
	advance := func(d time.Duration) {
		mu.Lock()
		fakeNow = fakeNow.Add(d)
		mu.Unlock()
	}

	fp := newFakeProcess(88)
	m.starter = func(context.Context, string, []string) (processHandle, error) { return fp, nil }
	spawned := int32(0)
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	orig := m.starter
	m.starter = func(ctx context.Context, p string, a []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return orig(ctx, p, a)
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}

	// Not idle yet.
	did, err := m.ShutdownIfIdle(context.Background())
	if err != nil || did {
		t.Fatalf("expected no shutdown before idle timeout, got (%v,%v)", did, err)
	}

	advance(5 * time.Second)
	if got := m.IdleDuration(); got != 5*time.Second {
		t.Fatalf("IdleDuration = %v, want 5s", got)
	}

	// Touch resets the clock.
	m.Touch()
	if got := m.IdleDuration(); got != 0 {
		t.Fatalf("IdleDuration after Touch = %v, want 0", got)
	}

	advance(11 * time.Second)
	did, err = m.ShutdownIfIdle(context.Background())
	if err != nil {
		t.Fatalf("ShutdownIfIdle error: %v", err)
	}
	if !did {
		t.Fatal("expected shutdown after idle timeout exceeded")
	}
	if m.Status().State != StateStopped {
		t.Fatalf("state = %v, want stopped after idle shutdown", m.Status().State)
	}
}

func TestProbeReachableUnmanagedIsExternal(t *testing.T) {
	m := newTestManager(t)
	m.health = func(context.Context) error { return nil } // reachable
	snap := m.Probe(context.Background())
	if snap.State != StateRunning {
		t.Fatalf("state = %v, want running", snap.State)
	}
	if !snap.External {
		t.Fatal("expected External=true when reachable with no managed proc")
	}
}

func TestProbeReachableManagedNotExternal(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(321)
	spawned := int32(0)
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return fp, nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	snap := m.Probe(context.Background())
	if snap.State != StateRunning {
		t.Fatalf("state = %v, want running", snap.State)
	}
	if snap.External {
		t.Fatal("expected External=false for managed proc")
	}
	if snap.PID != 321 {
		t.Fatalf("pid = %d, want 321", snap.PID)
	}
}

func TestProbeUnreachableReturnsTrackedState(t *testing.T) {
	m := newTestManager(t)
	m.health = func(context.Context) error { return errors.New("down") }
	snap := m.Probe(context.Background())
	if snap.State != StateStopped {
		t.Fatalf("state = %v, want stopped (tracked)", snap.State)
	}
	if snap.External {
		t.Fatal("expected External=false when unreachable")
	}
	if m.Status().State != StateStopped {
		t.Fatalf("persisted state mutated to %v", m.Status().State)
	}
}

func TestStopByPIDFileCrossProcess(t *testing.T) {
	m := newTestManager(t)
	const pid = 4242
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	alive := int32(1)
	var sentTerm int32
	m.procAlive = func(p int) bool { return p == pid && atomic.LoadInt32(&alive) == 1 }
	m.health = func(context.Context) error { return nil }
	m.signalProc = func(p int, sig os.Signal) error {
		if p != pid {
			t.Errorf("signalled wrong pid %d", p)
		}
		if sig == syscall.SIGTERM {
			atomic.StoreInt32(&sentTerm, 1)
			atomic.StoreInt32(&alive, 0)
		}
		return nil
	}

	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if !stopped {
		t.Fatal("expected stopped=true for live cross-process daemon")
	}
	if atomic.LoadInt32(&sentTerm) != 1 {
		t.Fatal("expected SIGTERM to the tracked PID")
	}
	if _, ok := m.readPIDFile(); ok {
		t.Fatal("expected PID file to be removed after stop")
	}
}

func TestStopByPIDFileStaleIsCleanedUp(t *testing.T) {
	m := newTestManager(t)
	if err := m.writePIDFile(9999); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	m.procAlive = func(int) bool { return false }
	m.health = func(context.Context) error {
		t.Fatal("should not health-check a dead process")
		return nil
	}
	m.signalProc = func(int, os.Signal) error {
		t.Fatal("should not signal a dead process")
		return nil
	}
	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if stopped {
		t.Fatal("expected stopped=false for stale PID file")
	}
	if _, ok := m.readPIDFile(); ok {
		t.Fatal("expected stale PID file to be removed")
	}
}

func TestStopByPIDFileReusedPIDIsNotSignalledWhenHealthFails(t *testing.T) {
	m := newTestManager(t)
	const pid = 5252
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	m.procAlive = func(p int) bool { return p == pid }
	m.health = func(context.Context) error { return errors.New("daemon endpoint is down") }
	m.signalProc = func(int, os.Signal) error {
		t.Fatal("should not signal a live PID that fails daemon health sanity")
		return nil
	}

	stopped, err := m.Stop(context.Background())
	if err != nil {
		t.Fatalf("Stop error: %v", err)
	}
	if stopped {
		t.Fatal("expected stopped=false for live PID failing health sanity")
	}
	if _, ok := m.readPIDFile(); ok {
		t.Fatal("expected mismatched PID file to be removed")
	}
}

func TestEnsureRunningHealthOnlyDoesNotLoadOrphanedAPIKey(t *testing.T) {
	m := newTestManager(t)
	oldKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.MkdirAll(m.Config.CacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(m.Config.APIKeyFilePath(), []byte(oldKey+"\n"), 0o600); err != nil {
		t.Fatalf("write orphan API key: %v", err)
	}
	m.health = func(context.Context) error { return nil }
	err := m.EnsureRunning(context.Background())
	if err == nil || !strings.Contains(err.Error(), "unmanaged healthy server") {
		t.Fatalf("EnsureRunning error = %v, want unmanaged conflict", err)
	}
	if got := m.APIKey(); got != "" {
		t.Fatalf("health-only conflict loaded API key %q, want empty", got)
	}
	if st := m.Status(); st.State != StateFailed || st.External {
		t.Fatalf("expected failed non-external state, got state=%v external=%v", st.State, st.External)
	}
}

func TestPIDAndAPIKeyStateFilesUsePrivateModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission bits are not reliable on Windows")
	}
	m := newTestManager(t)
	m.Config.CacheDir = filepath.Join(t.TempDir(), "state")

	if err := m.writePIDFile(123); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	if err := m.writeReaperPIDFile(456); err != nil {
		t.Fatalf("writeReaperPIDFile: %v", err)
	}
	if err := m.writeAPIKeyFile(strings.Repeat("a", 64)); err != nil {
		t.Fatalf("writeAPIKeyFile: %v", err)
	}

	for _, tc := range []struct {
		name string
		path string
		want os.FileMode
	}{
		{name: "state dir", path: m.Config.CacheDir, want: 0o700},
		{name: "daemon PID", path: m.Config.PIDFilePath(), want: 0o600},
		{name: "reaper PID", path: m.Config.ReaperPIDFilePath(), want: 0o600},
		{name: "API key", path: m.Config.APIKeyFilePath(), want: 0o600},
	} {
		info, err := os.Stat(tc.path)
		if err != nil {
			t.Fatalf("stat %s: %v", tc.name, err)
		}
		if got := info.Mode().Perm(); got != tc.want {
			t.Fatalf("%s mode = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestEnsureRunningCreatesFreshAPIKeyFile0600AndRemovesWithPID(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(556)
	spawned := int32(0)
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return fp, nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	key := m.APIKey()
	if len(key) != 64 {
		t.Fatalf("APIKey length = %d, want 64", len(key))
	}
	if _, err := hex.DecodeString(key); err != nil {
		t.Fatalf("APIKey is not hex: %v", err)
	}
	info, err := os.Stat(m.Config.APIKeyFilePath())
	if err != nil {
		t.Fatalf("stat API key file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("API key file mode = %v, want 0600", got)
		}
	}
	stopped, err := m.Stop(context.Background())
	if err != nil || !stopped {
		t.Fatalf("Stop = (%v, %v), want stopped without error", stopped, err)
	}
	if _, err := os.Stat(m.Config.APIKeyFilePath()); !os.IsNotExist(err) {
		t.Fatalf("expected API key file removed, stat err = %v", err)
	}
}

func TestEnsureRunningWarmReuseReadsManagedAPIKey(t *testing.T) {
	m1 := newTestManager(t)
	fp := newFakeProcess(559)
	spawned := int32(0)
	m1.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return fp, nil
	}
	m1.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	if err := m1.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("first EnsureRunning: %v", err)
	}
	key := m1.APIKey()
	m2 := newTestManager(t)
	m2.Config = m1.Config
	m2.health = func(context.Context) error { return nil }
	m2.procAlive = func(pid int) bool { return pid == 559 }
	m2.starter = func(context.Context, string, []string) (processHandle, error) {
		t.Fatal("warm reuse should not spawn")
		return nil, nil
	}
	if err := m2.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("second EnsureRunning: %v", err)
	}
	if got := m2.APIKey(); got != key || got == "" {
		t.Fatalf("warm APIKey = %q, want first key %q", got, key)
	}
	if st := m2.Status(); st.State != StateRunning || st.External {
		t.Fatalf("warm status state=%v external=%v, want running managed", st.State, st.External)
	}
}

func TestEnsureRunningReplacesStaleAPIKeyFileBeforeSpawn(t *testing.T) {
	m := newTestManager(t)
	oldKey := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := os.MkdirAll(m.Config.CacheDir, 0o700); err != nil {
		t.Fatalf("mkdir cache: %v", err)
	}
	if err := os.WriteFile(m.Config.APIKeyFilePath(), []byte(oldKey+"\n"), 0o600); err != nil {
		t.Fatalf("write stale API key: %v", err)
	}
	fp := newFakeProcess(558)
	spawned := int32(0)
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return fp, nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	if got := m.APIKey(); got == oldKey {
		t.Fatal("managed spawn reused stale API key")
	}
	info, err := os.Stat(m.Config.APIKeyFilePath())
	if err != nil {
		t.Fatalf("stat API key file: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("API key file mode = %v, want 0600", got)
		}
	}
}

func TestEnsureRunningRemovesAPIKeyFileOnStartupFailure(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(557)
	m.starter = func(context.Context, string, []string) (processHandle, error) { return fp, nil }
	m.health = func(context.Context) error { return errors.New("never healthy") }
	if err := m.EnsureRunning(context.Background()); err == nil {
		t.Fatal("expected startup failure")
	}
	if _, err := os.Stat(m.Config.APIKeyFilePath()); !os.IsNotExist(err) {
		t.Fatalf("expected API key file removed after startup failure, stat err = %v", err)
	}
	if got := m.APIKey(); got != "" {
		t.Fatalf("APIKey after failure = %q, want empty", got)
	}
}

func TestEnsureRunningWritesPIDFile(t *testing.T) {
	m := newTestManager(t)
	fp := newFakeProcess(555)
	spawned := int32(0)
	m.starter = func(context.Context, string, []string) (processHandle, error) {
		atomic.StoreInt32(&spawned, 1)
		return fp, nil
	}
	m.health = func(context.Context) error {
		if atomic.LoadInt32(&spawned) == 0 {
			return errors.New("down")
		}
		return nil
	}
	if err := m.EnsureRunning(context.Background()); err != nil {
		t.Fatalf("EnsureRunning: %v", err)
	}
	pid, ok := m.readPIDFile()
	if !ok || pid != 555 {
		t.Fatalf("expected PID file with 555, got (%d, %v)", pid, ok)
	}
}

func TestStateString(t *testing.T) {
	cases := map[State]string{
		StateStopped:  "stopped",
		StateStarting: "starting",
		StateRunning:  "running",
		StateFailed:   "failed",
		State(99):     "unknown",
	}
	for st, want := range cases {
		if st.String() != want {
			t.Fatalf("State(%d).String() = %q, want %q", st, st.String(), want)
		}
	}
}

func containsPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}
