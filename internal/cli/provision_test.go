package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ricardocabral/ajq/internal/cli"
	"github.com/ricardocabral/ajq/internal/provision"
)

// fakeProvisioner is an injectable ProvisionController for CLI tests. It never
// touches the filesystem, PATH, or network.
type failingProvisionWriter struct{ err error }

func (w failingProvisionWriter) Write([]byte) (int, error) { return 0, w.err }

type fakeProvisioner struct {
	plan          provision.Plan
	planErr       error
	planCalled    bool
	planModelName string
	installCalled bool
	installErr    error
	events        []provision.Progress
}

func (f *fakeProvisioner) Plan() (provision.Plan, error) {
	f.planCalled = true
	return f.plan, f.planErr
}

func (f *fakeProvisioner) PlanModel(name string) (provision.Plan, error) {
	f.planCalled = true
	f.planModelName = name
	return f.plan, f.planErr
}

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

func TestProvisionCheckJSONContract(t *testing.T) {
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("AJQ_MODEL", "")
	readyPlan := planAllPresent()
	readyPlan.Engine.Asset.URL = "https://example.invalid/engine"
	readyPlan.Model.Asset.SHA256 = "provision-json-checksum-sentinel"
	ready := &fakeProvisioner{plan: readyPlan}
	stdout, stderr, err := runProvision(ready, "provision", "--check", "--json")
	if err != nil || stderr != "" || !ready.planCalled || ready.installCalled {
		t.Fatalf("ready provision JSON = (%v, %q), planned=%t installed=%t", err, stderr, ready.planCalled, ready.installCalled)
	}
	want := "{\"schema_version\":\"1\",\"platform\":{\"os\":\"darwin\",\"arch\":\"arm64\"},\"ready\":true,\"engine\":{\"kind\":\"engine\",\"name\":\"llama-server\",\"version\":\"\",\"filename\":\"\",\"present\":true,\"path\":\"/opt/homebrew/bin/llama-server\",\"source\":\"path\"},\"model\":{\"kind\":\"model\",\"name\":\"qwen2.5-1.5b\",\"version\":\"\",\"filename\":\"\",\"present\":true,\"path\":\"/cache/models/m.gguf\",\"source\":\"cache\"},\"actions\":[]}\n"
	if stdout != want {
		t.Fatalf("ready provision JSON = %q, want %q", stdout, want)
	}
	if strings.Contains(stdout, "http") || strings.Contains(stdout, "provision-json-checksum-sentinel") {
		t.Fatalf("provision JSON must omit download metadata: %q", stdout)
	}
	var document struct {
		Ready   bool `json:"ready"`
		Actions []struct {
			ID string `json:"id"`
		} `json:"actions"`
	}
	if err := json.Unmarshal([]byte(stdout), &document); err != nil || !document.Ready || len(document.Actions) != 0 {
		t.Fatalf("decode ready provision JSON = (%+v, %v)", document, err)
	}

	missing := planMissingModel()
	missing.Engine.Present = false
	missing.Model.Asset.Name = "qwen3-4b"
	missing.Model.Asset.Kind = provision.KindModel
	missing.Model.Asset.Filename = "qwen3.gguf"
	selected := &fakeProvisioner{plan: missing}
	stdout, stderr, err = runProvision(selected, "provision", "--check", "--json", "--model", "qwen3-4b")
	if err == nil || cli.ExitCode(err) != 1 || stderr != "" || selected.installCalled || selected.planModelName != "qwen3-4b" {
		t.Fatalf("missing selected provision JSON = (%v, %q), installed=%t model=%q", err, stderr, selected.installCalled, selected.planModelName)
	}
	wantMissingSelected := "{\"schema_version\":\"1\",\"platform\":{\"os\":\"darwin\",\"arch\":\"arm64\"},\"ready\":false,\"engine\":{\"kind\":\"engine\",\"name\":\"llama-server\",\"version\":\"\",\"filename\":\"\",\"present\":false,\"path\":\"/opt/homebrew/bin/llama-server\"},\"model\":{\"kind\":\"model\",\"name\":\"qwen3-4b\",\"version\":\"\",\"filename\":\"qwen3.gguf\",\"present\":false,\"path\":\"/cache/models/m.gguf\"},\"actions\":[{\"id\":\"provision\",\"command\":\"ajq provision\"},{\"id\":\"models_pull\",\"command\":\"ajq models pull qwen3-4b\"}]}\n"
	if stdout != wantMissingSelected {
		t.Fatalf("missing selected provision JSON/order = %q, want %q", stdout, wantMissingSelected)
	}

	missingDefault := &fakeProvisioner{plan: planMissingModel()}
	stdout, stderr, err = runProvision(missingDefault, "provision", "--check", "--json")
	wantMissingDefault := "{\"schema_version\":\"1\",\"platform\":{\"os\":\"darwin\",\"arch\":\"arm64\"},\"ready\":false,\"engine\":{\"kind\":\"engine\",\"name\":\"llama-server\",\"version\":\"\",\"filename\":\"\",\"present\":true,\"path\":\"/opt/homebrew/bin/llama-server\",\"source\":\"path\"},\"model\":{\"kind\":\"model\",\"name\":\"qwen2.5-1.5b\",\"version\":\"\",\"filename\":\"\",\"present\":false,\"path\":\"/cache/models/m.gguf\"},\"actions\":[{\"id\":\"provision\",\"command\":\"ajq provision\"}]}\n"
	if err == nil || cli.ExitCode(err) != 1 || stderr != "" || stdout != wantMissingDefault || missingDefault.installCalled {
		t.Fatalf("missing default provision JSON = (%v, %q, %q), want %q", err, stdout, stderr, wantMissingDefault)
	}

	legacy := planAllPresent()
	legacy.Engine.Source = "cache"
	legacy.Engine.Path = "/cache/bin/llama-server"
	stdout, stderr, err = runProvision(&fakeProvisioner{plan: legacy}, "provision", "--check", "--json")
	if err != nil || stderr != "" || !strings.Contains(stdout, `"source":"legacy_cache"`) {
		t.Fatalf("legacy cache provision JSON = (%v, %q, %q)", err, stdout, stderr)
	}
}

func TestProvisionJSONRejectsInstallAndHidesSelectionFailure(t *testing.T) {
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	fake := &fakeProvisioner{plan: planMissingModel()}
	stdout, stderr, err := runProvision(fake, "provision", "--json")
	if err == nil || cli.ExitCode(err) != 2 || stdout != "" || !strings.Contains(stderr, "--json requires --check") || fake.planCalled || fake.installCalled {
		t.Fatalf("install JSON rejection = (%v, %q, %q), planned=%t installed=%t", err, stdout, stderr, fake.planCalled, fake.installCalled)
	}

	const sentinel = "provision-json-secret-sentinel"
	configPath := filepath.Join(t.TempDir(), "bad.toml")
	if err := os.WriteFile(configPath, []byte("model = [ # "+sentinel), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AJQ_CONFIG", configPath)
	stdout, stderr, err = runProvision(&fakeProvisioner{plan: planAllPresent()}, "provision", "--check", "--json")
	if err == nil || cli.ExitCode(err) != 1 || stdout != "" || strings.Contains(stderr, sentinel) || !strings.Contains(stderr, "provisioning check unavailable") {
		t.Fatalf("selection failure leaked or changed = (%v, %q, %q)", err, stdout, stderr)
	}
}

func TestProvisionJSONWriterFailure(t *testing.T) {
	t.Setenv("AJQ_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	writeErr := errors.New("provision-json-writer-sentinel")
	var stderr bytes.Buffer
	err := cli.Execute(context.Background(), cli.Options{Stdin: strings.NewReader(""), Stdout: failingProvisionWriter{err: writeErr}, Stderr: &stderr, Provision: &fakeProvisioner{plan: planAllPresent()}}, []string{"provision", "--check", "--json"})
	if err == nil || !errors.Is(err, writeErr) || !strings.Contains(stderr.String(), "write provisioning JSON status") {
		t.Fatalf("provision JSON writer failure = (%v, %q)", err, stderr.String())
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
