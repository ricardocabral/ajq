package cli_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
	"github.com/ricardocabral/ajq/internal/provision"
)

type failWriter struct {
	err    error
	writes int
}

func (w *failWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.err == nil {
		w.err = errors.New("writer failed")
	}
	return 0, w.err
}

func TestDaemonStatusWriterFailure(t *testing.T) {
	writeErr := errors.New("status writer failed")
	out := &failWriter{err: writeErr}
	var stderr bytes.Buffer
	ctrl := &fakeController{}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: &stderr,
		Daemon: ctrl,
	}, []string{"daemon", "status"})
	if err == nil {
		t.Fatal("expected daemon status writer failure")
	}
	if ctrl.statusCalls != 1 {
		t.Fatalf("expected status to be probed once, got %d", ctrl.statusCalls)
	}
	if out.writes == 0 {
		t.Fatal("expected status renderer to attempt stdout write")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(status writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write daemon status", "status writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestDaemonStopWriterFailure(t *testing.T) {
	writeErr := errors.New("stop writer failed")
	out := &failWriter{err: writeErr}
	var stderr bytes.Buffer
	ctrl := &fakeController{stopped: true}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: &stderr,
		Daemon: ctrl,
	}, []string{"daemon", "stop"})
	if err == nil {
		t.Fatal("expected daemon stop writer failure")
	}
	if ctrl.stopCalls != 1 {
		t.Fatalf("expected stop to be called once, got %d", ctrl.stopCalls)
	}
	if out.writes == 0 {
		t.Fatal("expected stop renderer to attempt stdout write")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(stop writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write daemon stop status", "stop writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionPlanWriterFailureStopsBeforeInstall(t *testing.T) {
	writeErr := errors.New("plan writer failed")
	out := &failWriter{err: writeErr}
	var stderr bytes.Buffer
	fake := &fakeProvisioner{plan: planMissingModel()}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    &stderr,
		Provision: fake,
	}, []string{"provision"})
	if err == nil {
		t.Fatal("expected provisioning plan writer failure")
	}
	if fake.installCalled {
		t.Fatal("Install must not be called when plan rendering fails")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(plan writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write provisioning plan", "plan writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionAllAssetsPresentResultWriterFailure(t *testing.T) {
	writeErr := errors.New("result writer failed")
	out := newFailWriterAfter(3, writeErr)
	var stderr bytes.Buffer
	fake := &fakeProvisioner{plan: planAllPresent()}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    &stderr,
		Provision: fake,
	}, []string{"provision"})
	if err == nil {
		t.Fatal("expected all-assets-present result writer failure")
	}
	if fake.installCalled {
		t.Fatal("Install must not be called when all assets are present")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(result writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write provisioning result", "result writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionCheckRequiredWriterFailure(t *testing.T) {
	writeErr := errors.New("check result writer failed")
	out := newFailWriterAfter(3, writeErr)
	var stderr bytes.Buffer
	fake := &fakeProvisioner{plan: planMissingModel()}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    &stderr,
		Provision: fake,
	}, []string{"provision", "--check"})
	if err == nil {
		t.Fatal("expected --check result writer failure")
	}
	if fake.installCalled {
		t.Fatal("--check must not install")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(check result writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write provisioning check result", "check result writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionCompletionWriterFailure(t *testing.T) {
	writeErr := errors.New("completion writer failed")
	out := newFailWriterAfter(3, writeErr)
	var stderr bytes.Buffer
	fake := &fakeProvisioner{plan: planMissingModel()}

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    out,
		Stderr:    &stderr,
		Provision: fake,
	}, []string{"provision"})
	if err == nil {
		t.Fatal("expected provisioning completion writer failure")
	}
	if !fake.installCalled {
		t.Fatal("Install should be called before completion rendering")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(completion writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write provisioning completion", "completion writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionProgressWriterFailureAfterSuccessfulInstall(t *testing.T) {
	writeErr := errors.New("progress writer failed")
	stderr := &failWriter{err: writeErr}
	var stdout bytes.Buffer
	fake := &fakeProvisioner{
		plan: planMissingModel(),
		events: []provision.Progress{
			{Asset: "qwen2.5-1.5b-instruct", Kind: provision.KindModel, BytesDone: 512, BytesTotal: 1024},
			{Asset: "qwen2.5-1.5b-instruct", Kind: provision.KindModel, BytesDone: 1024, BytesTotal: 1024, Done: true},
		},
	}

	err := executeRootCommand(cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    stderr,
		Provision: fake,
	}, "provision")
	if err == nil {
		t.Fatal("expected progress writer failure")
	}
	if !fake.installCalled {
		t.Fatal("Install should be called")
	}
	if stderr.writes != 1 {
		t.Fatalf("expected progress printer to stop after first write failure, got %d writes", stderr.writes)
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(progress writer failure) = %d, want 1", got)
	}
	for _, want := range []string{"write provisioning progress", "progress writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "provisioning failed") {
		t.Fatalf("writer-only progress failure should not invent install failure: %v", err)
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestProvisionProgressWriterFailureJoinsInstallFailure(t *testing.T) {
	writeErr := errors.New("progress writer failed")
	installErr := errors.New("install failed")
	stderr := &failWriter{err: writeErr}
	var stdout bytes.Buffer
	fake := &fakeProvisioner{
		plan:       planMissingModel(),
		installErr: installErr,
		events: []provision.Progress{
			{Asset: "qwen2.5-1.5b-instruct", Kind: provision.KindModel, BytesDone: 512, BytesTotal: 1024},
		},
	}

	err := executeRootCommand(cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    &stdout,
		Stderr:    stderr,
		Provision: fake,
	}, "provision")
	if err == nil {
		t.Fatal("expected joined install and progress writer failure")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(joined progress/install failure) = %d, want 1", got)
	}
	for _, want := range []string{"provisioning failed", "install failed", "write provisioning progress", "progress writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if !errors.Is(err, installErr) {
		t.Fatalf("errors.Is should find install failure in %v", err)
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestRootStderrWriterFailureJoinsOriginalError(t *testing.T) {
	writeErr := errors.New("stderr writer failed")
	stderr := &failWriter{err: writeErr}
	var stdout bytes.Buffer

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: stderr,
	}, []string{"--backend", "bogus", "."})
	if err == nil {
		t.Fatal("expected root stderr writer failure")
	}
	if stderr.writes != 1 {
		t.Fatalf("expected one stderr render attempt, got %d", stderr.writes)
	}
	if got := cli.ExitCode(err); got != 2 {
		t.Fatalf("ExitCode(joined root stderr failure) = %d, want 2", got)
	}
	for _, want := range []string{"unknown backend \"bogus\"", "write stderr error line", "stderr writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find stderr writer failure in %v", err)
	}
}

func TestRootSilentExitDoesNotRenderStderr(t *testing.T) {
	stderr := &failWriter{err: errors.New("stderr writer should not be used")}
	var stdout bytes.Buffer

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader("false"),
		Stdout: &stdout,
		Stderr: stderr,
	}, []string{"--exit-status", "."})
	if err == nil {
		t.Fatal("expected silent exit-status error")
	}
	if got := cli.ExitCode(err); got != 1 {
		t.Fatalf("ExitCode(silent false) = %d, want 1", got)
	}
	if stderr.writes != 0 {
		t.Fatalf("silent exit should not render stderr, got %d writes", stderr.writes)
	}
	if !strings.Contains(stdout.String(), "false") {
		t.Fatalf("expected query output before silent exit, got %q", stdout.String())
	}
}

type failWriterAfter struct {
	buf       bytes.Buffer
	remaining int
	err       error
}

func newFailWriterAfter(successfulWrites int, err error) *failWriterAfter {
	return &failWriterAfter{remaining: successfulWrites, err: err}
}

func (w *failWriterAfter) Write(p []byte) (int, error) {
	if w.remaining > 0 {
		w.remaining--
		return w.buf.Write(p)
	}
	return 0, w.err
}

func executeRootCommand(opts cli.Options, args ...string) error {
	cmd := cli.NewRootCommand(opts)
	cmd.SetArgs(args)
	return cmd.Execute()
}

var _ io.Writer = (*failWriter)(nil)
var _ io.Writer = (*failWriterAfter)(nil)

func TestCacheStatusWriterFailure(t *testing.T) {
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	writeErr := errors.New("cache status writer failed")
	out := &failWriter{err: writeErr}
	var stderr bytes.Buffer

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: &stderr,
	}, []string{"cache", "status"})
	if err == nil {
		t.Fatal("expected cache status writer failure")
	}
	if out.writes == 0 {
		t.Fatal("expected status renderer to attempt stdout write")
	}
	for _, want := range []string{"write cache status", "cache status writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}

func TestCacheClearWriterFailure(t *testing.T) {
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	writeErr := errors.New("cache clear writer failed")
	out := &failWriter{err: writeErr}
	var stderr bytes.Buffer

	err := cli.Execute(context.Background(), cli.Options{
		Stdin:  strings.NewReader(""),
		Stdout: out,
		Stderr: &stderr,
	}, []string{"cache", "clear"})
	if err == nil {
		t.Fatal("expected cache clear writer failure")
	}
	if out.writes == 0 {
		t.Fatal("expected clear renderer to attempt stdout write")
	}
	for _, want := range []string{"write cache clear", "cache clear writer failed"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q missing %q", err, want)
		}
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr %q missing %q", stderr.String(), want)
		}
	}
	if !errors.Is(err, writeErr) {
		t.Fatalf("errors.Is should find writer failure in %v", err)
	}
}
