package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ricardocabral/ajq/internal/daemon"
)

type fakeRealDaemonManager struct {
	ensureErr   error
	ensureCalls int
	stopErrs    []error
	stopCalls   int
}

func (m *fakeRealDaemonManager) EnsureRunning(context.Context) error {
	m.ensureCalls++
	return m.ensureErr
}

func (m *fakeRealDaemonManager) Stop(context.Context) (bool, error) {
	call := m.stopCalls
	m.stopCalls++
	if call < len(m.stopErrs) {
		return false, m.stopErrs[call]
	}
	return false, nil
}

func installFakeRealDaemonManager(t *testing.T, mgr realDaemonManager) {
	t.Helper()
	old := newRealDaemonManager
	newRealDaemonManager = func(daemon.Config) realDaemonManager { return mgr }
	t.Cleanup(func() { newRealDaemonManager = old })
}

func startRealBenchCompletionServer(t *testing.T, handler http.Handler) RealConfig {
	t.Helper()
	ln, err := new(net.ListenConfig).Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().(*net.TCPAddr)
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("completion server: %v", err)
		}
	}()
	t.Cleanup(func() { _ = srv.Shutdown(context.Background()) })
	return RealConfig{Host: addr.IP.String(), Port: addr.Port}
}

func successfulCompletionHandler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/completion" {
			t.Errorf("request path = %q, want /completion", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(struct {
			Content string `json:"content"`
		}{Content: "true"})
	})
}

func TestRunRealInitialStopFailure(t *testing.T) {
	initialErr := errors.New("initial stop failed")
	mgr := &fakeRealDaemonManager{stopErrs: []error{initialErr}}
	installFakeRealDaemonManager(t, mgr)

	w, err := GenerateArray("test", QuerySemMatch, 1)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	_, err = RunReal(context.Background(), RealConfig{Host: "127.0.0.1", Port: 1}, w)
	if err == nil {
		t.Fatal("expected initial stop error, got nil")
	}
	if !strings.Contains(err.Error(), "initial stop") || !errors.Is(err, initialErr) {
		t.Fatalf("error = %v, want wrapped initial stop failure", err)
	}
	if mgr.ensureCalls != 0 {
		t.Fatalf("EnsureRunning called %d times after initial stop failure, want 0", mgr.ensureCalls)
	}
	if mgr.stopCalls != 1 {
		t.Fatalf("Stop called %d times, want initial stop only", mgr.stopCalls)
	}
}

func TestRunRealCleanupStopFailureAfterSuccess(t *testing.T) {
	cleanupErr := errors.New("cleanup stop failed")
	mgr := &fakeRealDaemonManager{stopErrs: []error{nil, cleanupErr}}
	installFakeRealDaemonManager(t, mgr)
	cfg := startRealBenchCompletionServer(t, successfulCompletionHandler(t))

	w, err := GenerateArray("test", QuerySemMatch, 2)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	_, err = RunReal(context.Background(), cfg, w)
	if err == nil {
		t.Fatal("expected cleanup stop error, got nil")
	}
	if !strings.Contains(err.Error(), "cleanup stop") || !errors.Is(err, cleanupErr) {
		t.Fatalf("error = %v, want wrapped cleanup stop failure", err)
	}
	if mgr.ensureCalls != 1 {
		t.Fatalf("EnsureRunning called %d times, want 1", mgr.ensureCalls)
	}
	if mgr.stopCalls != 2 {
		t.Fatalf("Stop called %d times, want initial and cleanup stops", mgr.stopCalls)
	}
}

func TestRunRealCleanupStopFailurePreservesPrimaryError(t *testing.T) {
	cleanupErr := errors.New("cleanup stop failed")
	mgr := &fakeRealDaemonManager{stopErrs: []error{nil, cleanupErr}}
	installFakeRealDaemonManager(t, mgr)
	cfg := startRealBenchCompletionServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "daemon unavailable", http.StatusInternalServerError)
	}))

	w, err := GenerateArray("test", QuerySemMatch, 1)
	if err != nil {
		t.Fatalf("GenerateArray: %v", err)
	}
	_, err = RunReal(context.Background(), cfg, w)
	if err == nil {
		t.Fatal("expected primary benchmark and cleanup errors, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"warm judgement", "status 500", "cleanup stop"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error = %v, want it to contain %q", err, want)
		}
	}
	if !errors.Is(err, cleanupErr) {
		t.Fatalf("error = %v, want cleanup error in chain", err)
	}
	if got, want := fmt.Sprint(err), "warm judgement"; !strings.Contains(got, want) {
		t.Fatalf("primary error was not preserved first in %q", got)
	}
	if mgr.stopCalls != 2 {
		t.Fatalf("Stop called %d times, want initial and cleanup stops", mgr.stopCalls)
	}
}
