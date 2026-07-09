package cli_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
	"github.com/ricardocabral/ajq/internal/provision"
)

// fakeProvisioner is an injectable ProvisionController for CLI tests. It never
// touches the filesystem, PATH, or network.
type fakeProvisioner struct {
	plan          provision.Plan
	planErr       error
	installCalled bool
	installErr    error
	events        []provision.Progress
}

func (f *fakeProvisioner) Plan() (provision.Plan, error) { return f.plan, f.planErr }

func (f *fakeProvisioner) PlanModel(string) (provision.Plan, error) { return f.plan, f.planErr }

func (f *fakeProvisioner) PlanModelOnly(string) (provision.Plan, error) { return f.plan, f.planErr }

func (f *fakeProvisioner) Install(_ context.Context, _ provision.Plan, progress provision.ProgressFunc) error {
	f.installCalled = true
	if progress != nil {
		for _, e := range f.events {
			progress(e)
		}
	}
	return f.installErr
}

func (f *fakeProvisioner) InstallModel(ctx context.Context, plan provision.Plan, progress provision.ProgressFunc) error {
	return f.Install(ctx, plan, progress)
}

func planAllPresent() provision.Plan {
	return provision.Plan{
		Platform: provision.Platform{OS: "darwin", Arch: "arm64"},
		Engine:   provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindEngine, Name: "llama-server"}, Present: true, Source: "PATH", Path: "/opt/homebrew/bin/llama-server"},
		Model:    provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindModel, Name: provision.DefaultModelName}, Present: true, Source: "cache", Path: "/cache/models/m.gguf"},
	}
}

func planMissingModel() provision.Plan {
	return provision.Plan{
		Platform: provision.Platform{OS: "darwin", Arch: "arm64"},
		Engine:   provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindEngine, Name: "llama-server"}, Present: true, Source: "PATH", Path: "/opt/homebrew/bin/llama-server"},
		Model:    provision.AssetStatus{Asset: provision.Asset{Kind: provision.KindModel, Name: provision.DefaultModelName}, Present: false, Path: "/cache/models/m.gguf"},
	}
}

func runProvision(fake cli.ProvisionController, args ...string) (stdout, stderr string, err error) {
	var out, errBuf bytes.Buffer
	err = cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(""),
		Stdout:    &out,
		Stderr:    &errBuf,
		Provision: fake,
	}, args)
	return out.String(), errBuf.String(), err
}

func TestProvisionCheckReportsBundleSource(t *testing.T) {
	plan := planAllPresent()
	plan.Engine.Source = "bundle"
	plan.Engine.Path = "/cache/engine/b9917/llama-b9917/llama-server"
	fake := &fakeProvisioner{plan: plan}
	stdout, _, err := runProvision(fake, "provision", "--check")
	if err != nil {
		t.Fatalf("provision --check returned error: %v", err)
	}
	if !strings.Contains(stdout, "engine: present (bundle) /cache/engine/b9917/llama-b9917/llama-server") {
		t.Fatalf("stdout should report bundle engine source/path: %q", stdout)
	}
}

func TestProvisionAllPresentNoOp(t *testing.T) {
	fake := &fakeProvisioner{plan: planAllPresent()}
	stdout, _, err := runProvision(fake, "provision")
	if err != nil {
		t.Fatalf("provision returned error: %v", err)
	}
	if fake.installCalled {
		t.Fatal("Install must not be called when all assets present")
	}
	if !strings.Contains(stdout, "nothing to provision") {
		t.Fatalf("stdout should report nothing to provision: %q", stdout)
	}
	if !strings.Contains(stdout, "present") {
		t.Fatalf("stdout should show asset status: %q", stdout)
	}
}

func TestProvisionInstallsMissingWithProgress(t *testing.T) {
	fake := &fakeProvisioner{
		plan: planMissingModel(),
		events: []provision.Progress{
			{Asset: "qwen2.5-1.5b-instruct", Kind: provision.KindModel, BytesDone: 512, BytesTotal: 1024},
			{Asset: "qwen2.5-1.5b-instruct", Kind: provision.KindModel, BytesDone: 1024, BytesTotal: 1024, Done: true},
		},
	}
	stdout, stderr, err := runProvision(fake, "provision")
	if err != nil {
		t.Fatalf("provision returned error: %v", err)
	}
	if !fake.installCalled {
		t.Fatal("Install should be called when assets are missing")
	}
	if !strings.Contains(stdout, "missing") {
		t.Fatalf("stdout should list missing asset: %q", stdout)
	}
	if !strings.Contains(stdout, "provisioning complete") {
		t.Fatalf("stdout should confirm completion: %q", stdout)
	}
	// Progress goes to stderr and includes a percentage and a final install line.
	if !strings.Contains(stderr, "50%") {
		t.Fatalf("stderr should include progress percentage: %q", stderr)
	}
	if !strings.Contains(stderr, "installed") {
		t.Fatalf("stderr should include final install line: %q", stderr)
	}
}

func TestProvisionCheckReportsMissingWithoutInstalling(t *testing.T) {
	fake := &fakeProvisioner{plan: planMissingModel()}
	stdout, _, err := runProvision(fake, "provision", "--check")
	if err == nil {
		t.Fatal("provision --check should exit non-zero when assets missing")
	}
	if cli.ExitCode(err) == 0 {
		t.Fatalf("expected non-zero exit code, got %d", cli.ExitCode(err))
	}
	if fake.installCalled {
		t.Fatal("--check must not install anything")
	}
	if !strings.Contains(stdout, "provisioning required") {
		t.Fatalf("stdout should report provisioning required: %q", stdout)
	}
}

func TestLocalBackendMissingAssetsSurfacesProvisionError(t *testing.T) {
	t.Setenv("AJQ_CACHE_DIR", t.TempDir())
	// With --backend local and missing assets, a semantic query must fail at
	// warm time with an actionable message pointing to `ajq provision`, without
	// spawning a real daemon.
	fake := &fakeProvisioner{plan: planMissingModel()}
	var out, errBuf bytes.Buffer
	err := cli.Execute(context.Background(), cli.Options{
		Stdin:     strings.NewReader(`[{"msg":"please keep this"}]`),
		Stdout:    &out,
		Stderr:    &errBuf,
		Provision: fake,
	}, []string{"--backend", "local", "-c", `.[] | select(.msg =~ "keep")`})
	if err == nil {
		t.Fatal("expected missing-provisioning to fail the query")
	}
	if out.String() != "" {
		t.Fatalf("expected no stdout, got %q", out.String())
	}
	if !strings.Contains(errBuf.String(), "ajq provision") {
		t.Fatalf("error should direct user to `ajq provision`: %q", errBuf.String())
	}
}
