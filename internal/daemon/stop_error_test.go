package daemon

import (
	"context"
	"errors"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

type stopErrorProcess struct {
	pid        int
	exitOnTerm bool
	killErr    error
	waitErr    error
	waitCh     chan struct{}
}

func newStopErrorProcess(pid int) *stopErrorProcess {
	return &stopErrorProcess{pid: pid, waitCh: make(chan struct{})}
}

func (p *stopErrorProcess) Pid() int { return p.pid }

func (p *stopErrorProcess) Signal(sig os.Signal) error {
	if sig == syscall.SIGTERM && p.exitOnTerm {
		p.exit()
	}
	return nil
}

func (p *stopErrorProcess) Wait() error {
	<-p.waitCh
	return p.waitErr
}

func (p *stopErrorProcess) Kill() error {
	if p.killErr != nil {
		return p.killErr
	}
	p.exit()
	return nil
}

func (p *stopErrorProcess) exit() {
	select {
	case <-p.waitCh:
	default:
		close(p.waitCh)
	}
}

func TestStopReportsFailedKill(t *testing.T) {
	m := newTestManager(t)
	m.StopTimeout = time.Nanosecond
	proc := newStopErrorProcess(606)
	proc.killErr = errors.New("kill denied")
	m.proc = proc
	m.state = StateRunning
	if err := m.writePIDFile(proc.pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	stopped, err := m.Stop(context.Background())
	if !stopped {
		t.Fatal("expected stopped=true when a managed process was targeted")
	}
	if err == nil {
		t.Fatal("expected failed Kill to be reported")
	}
	if got := err.Error(); !strings.Contains(got, "kill daemon after stop timeout") || !strings.Contains(got, "kill denied") {
		t.Fatalf("error %q missing failed kill detail", got)
	}
	if _, ok := m.readPIDFile(); !ok {
		t.Fatal("expected PID file to remain when hard kill failed")
	}
}

func TestStopReportsFailedWait(t *testing.T) {
	m := newTestManager(t)
	proc := newStopErrorProcess(707)
	proc.exitOnTerm = true
	proc.waitErr = errors.New("wait failed")
	m.proc = proc
	m.state = StateRunning
	if err := m.writePIDFile(proc.pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}

	stopped, err := m.Stop(context.Background())
	if !stopped {
		t.Fatal("expected stopped=true when a managed process was targeted")
	}
	if err == nil {
		t.Fatal("expected failed Wait to be reported")
	}
	if got := err.Error(); !strings.Contains(got, "wait for daemon after SIGTERM") || !strings.Contains(got, "wait failed") {
		t.Fatalf("error %q missing failed wait detail", got)
	}
	if _, ok := m.readPIDFile(); ok {
		t.Fatal("expected PID file to be removed after process exit even when Wait reports an error")
	}
}

func TestStopByPIDFileReportsFailedSIGKILL(t *testing.T) {
	m := newTestManager(t)
	m.StopTimeout = 0
	const pid = 808
	if err := m.writePIDFile(pid); err != nil {
		t.Fatalf("writePIDFile: %v", err)
	}
	m.procAlive = func(p int) bool { return p == pid }
	m.health = func(context.Context) error { return nil }
	m.signalProc = func(p int, sig os.Signal) error {
		if p != pid {
			t.Fatalf("signalled wrong pid %d", p)
		}
		if sig == syscall.SIGKILL {
			return errors.New("sigkill denied")
		}
		return nil
	}

	stopped, err := m.Stop(context.Background())
	if !stopped {
		t.Fatal("expected stopped=true when a live PID-file daemon was targeted")
	}
	if err == nil {
		t.Fatal("expected failed PID-file SIGKILL to be reported")
	}
	if got := err.Error(); !strings.Contains(got, "SIGKILL") || !strings.Contains(got, "sigkill denied") {
		t.Fatalf("error %q missing failed SIGKILL detail", got)
	}
	if _, ok := m.readPIDFile(); !ok {
		t.Fatal("expected PID file to remain when PID-file SIGKILL failed")
	}
}
