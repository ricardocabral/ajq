package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/provision"
	"github.com/spf13/cobra"
)

// ProvisionController is the injectable seam the provisioning CLI command and
// local backend startup operate against. The production implementation is a
// *provision.Provisioner; tests provide a fake to exercise behavior without
// real filesystem/PATH/network access.
type ProvisionController interface {
	// Plan resolves which default assets are present or missing without installing.
	Plan() (provision.Plan, error)
	// PlanModel resolves a requested catalog model and engine without installing.
	PlanModel(name string) (provision.Plan, error)
	// PlanModelOnly resolves a requested catalog model without inspecting the engine.
	PlanModelOnly(name string) (provision.Plan, error)
	// Install downloads and verifies missing assets, reporting progress.
	Install(ctx context.Context, plan provision.Plan, progress provision.ProgressFunc) error
	// InstallModel downloads and verifies only the model portion of a plan.
	InstallModel(ctx context.Context, plan provision.Plan, progress provision.ProgressFunc) error
}

// resolveProvisionController returns the injected controller or the default
// production provisioner.
func resolveProvisionController(opts Options) ProvisionController {
	if opts.Provision != nil {
		return opts.Provision
	}
	return provision.New()
}

// newProvisionCommand builds the `ajq provision` command, which ensures the
// local inference engine and default model are present in the ajq cache.
func newProvisionCommand(opts Options) *cobra.Command {
	var checkOnly bool
	var jsonOutput bool
	var modelID string
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "download or locate the local llama-server engine and selected model",
		Long:  "Provision the local inference assets used by --backend local: a platform-appropriate llama-server engine and the selected GGUF model, cached under ~/.cache/ajq. Already-present assets (including a Homebrew llama-server on PATH or a previously cached model) are detected and left untouched.",
		Example: `  # Safely inspect whether local assets are installed; does not download.
  ajq provision --check`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if jsonOutput && !checkOnly {
				return &ExitError{Code: 2, Err: errors.New("--json requires --check; JSON provisioning is status-only")}
			}
			controller := resolveProvisionController(opts)
			plan, err := provisionPlanForCommand(cmd, controller, modelID)
			if err != nil {
				if jsonOutput {
					return &ExitError{Code: 1, Err: errors.New("provisioning check unavailable")}
				}
				return &ExitError{Code: 1, Err: fmt.Errorf("provisioning check failed: %w", err)}
			}

			out := cmd.OutOrStdout()
			if jsonOutput {
				if err := writeProvisionStatusJSON(out, plan); err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning JSON status: %w", err)}
				}
				if plan.NeedsProvisioning() {
					return &ExitError{Code: 1, Silent: true}
				}
				return nil
			}
			if err := writeProvisionPlan(out, plan); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning plan: %w", err)}
			}

			if !plan.NeedsProvisioning() {
				if _, err := fmt.Fprintln(out, "all assets present: nothing to provision"); err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning result: %w", err)}
				}
				return nil
			}
			if checkOnly {
				if _, err := fmt.Fprintln(out, "provisioning required: run `ajq provision` to install missing assets"); err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning check result: %w", err)}
				}
				return &ExitError{Code: 1, Silent: true}
			}

			progress := newProgressPrinter(cmd.ErrOrStderr())
			if err := controller.Install(cmd.Context(), plan, progress.Print); err != nil {
				if progressErr := progress.Err(); progressErr != nil {
					return &ExitError{Code: 1, Err: errors.Join(fmt.Errorf("provisioning failed: %w", err), fmt.Errorf("write provisioning progress: %w", progressErr))}
				}
				return &ExitError{Code: 1, Err: fmt.Errorf("provisioning failed: %w", err)}
			}
			if progressErr := progress.Err(); progressErr != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning progress: %w", progressErr)}
			}
			if _, err := fmt.Fprintln(out, "provisioning complete"); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write provisioning completion: %w", err)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&checkOnly, "check", false, "report provisioning status and exit non-zero if assets are missing, without downloading")
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the versioned machine-readable provisioning status (requires --check)")
	cmd.Flags().StringVar(&modelID, "model", "", "local catalog model or GGUF path to inspect/provision")
	return cmd
}

func provisionPlanForCommand(cmd *cobra.Command, controller ProvisionController, modelID string) (provision.Plan, error) {
	fileValues, err := config.LoadWithOptions(config.LoadOptions{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return provision.Plan{}, err
	}
	envValues, err := config.Env(os.Getenv)
	if err != nil {
		return provision.Plan{}, err
	}
	flags := config.Values{}
	if cmd.Flags().Changed("model") {
		flags.Model = modelID
		flags.ModelSet = true
	}
	settings := config.Resolve(flags, envValues, fileValues, backendRegistryDefaultValues("local"))
	resolved, err := resolveLocalModelRequest(settings.Model)
	if err != nil {
		return provision.Plan{}, err
	}
	modelName := resolved.Name
	if resolved.PathLike {
		modelName = ""
		if production, ok := controller.(*provision.Provisioner); ok {
			clone := *production
			clone.ModelOverride = resolved.Path
			controller = &clone
		}
	}
	return controller.PlanModel(modelName)
}

// provisionStatusDocument is the deterministic v1 wire contract for
// `ajq provision --check --json`.
type provisionStatusDocument struct {
	SchemaVersion string                  `json:"schema_version"`
	Platform      provisionPlatformStatus `json:"platform"`
	Ready         bool                    `json:"ready"`
	Engine        provisionAssetStatus    `json:"engine"`
	Model         provisionAssetStatus    `json:"model"`
	Actions       []provisionAction       `json:"actions"`
}

type provisionPlatformStatus struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
}

type provisionAssetStatus struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Filename string `json:"filename"`
	Present  bool   `json:"present"`
	Path     string `json:"path"`
	Source   string `json:"source,omitempty"`
}

type provisionAction struct {
	ID      string `json:"id"`
	Command string `json:"command"`
}

func writeProvisionStatusJSON(w io.Writer, plan provision.Plan) error {
	document := provisionStatusDocument{
		SchemaVersion: "1",
		Platform:      provisionPlatformStatus{OS: plan.Platform.OS, Arch: plan.Platform.Arch},
		Ready:         !plan.NeedsProvisioning(),
		Engine:        provisionAssetStatusFor(plan.Engine),
		Model:         provisionAssetStatusFor(plan.Model),
		Actions:       provisionActions(plan),
	}
	return json.NewEncoder(w).Encode(document)
}

func provisionAssetStatusFor(status provision.AssetStatus) provisionAssetStatus {
	asset := provisionAssetStatus{
		Kind:     string(status.Asset.Kind),
		Name:     status.Asset.Name,
		Version:  status.Asset.Version,
		Filename: status.Asset.Filename,
		Present:  status.Present,
		Path:     status.Path,
	}
	if status.Present {
		switch strings.ToLower(status.Source) {
		case "override", "bundle":
			asset.Source = strings.ToLower(status.Source)
		case "cache":
			if status.Asset.Kind == provision.KindEngine {
				asset.Source = "legacy_cache"
			} else {
				asset.Source = "cache"
			}
		case "path":
			asset.Source = "path"
		default:
			asset.Source = "unknown"
		}
	}
	return asset
}

func provisionActions(plan provision.Plan) []provisionAction {
	actions := make([]provisionAction, 0, 2)
	modelMissing := !plan.Model.Present
	defaultModel := plan.Model.Asset.Name == "" || plan.Model.Asset.Name == provision.DefaultModelName
	if !plan.Engine.Present || (modelMissing && defaultModel) {
		actions = append(actions, provisionAction{ID: "provision", Command: "ajq provision"})
	}
	if modelMissing && !defaultModel {
		actions = append(actions, provisionAction{ID: "models_pull", Command: "ajq models pull " + plan.Model.Asset.Name})
	}
	return actions
}

// writeProvisionPlan renders a stable status block for each asset.
func writeProvisionPlan(w io.Writer, plan provision.Plan) error {
	if _, err := fmt.Fprintf(w, "platform: %s\n", plan.Platform); err != nil {
		return err
	}
	if err := writeAssetStatus(w, "engine", plan.Engine); err != nil {
		return err
	}
	return writeAssetStatus(w, "model", plan.Model)
}

func writeAssetStatus(w io.Writer, label string, s provision.AssetStatus) error {
	if s.Present {
		source := s.Source
		if source == "" {
			source = "present"
		}
		_, err := fmt.Fprintf(w, "%s: present (%s) %s\n", label, source, s.Path)
		return err
	}
	_, err := fmt.Fprintf(w, "%s: missing -> %s\n", label, s.Path)
	return err
}

type progressPrinter struct {
	w       io.Writer
	lastPct map[string]int
	err     error
}

// newProgressPrinter returns a stateful progress renderer that records the
// first write failure. provision.ProgressFunc cannot return errors, so callers
// must pass Print to Install and check Err after Install returns.
func newProgressPrinter(w io.Writer) *progressPrinter {
	return &progressPrinter{w: w, lastPct: map[string]int{}}
}

func (p *progressPrinter) Print(progress provision.Progress) {
	switch {
	case progress.Skipped:
		p.writef("%s: already present, skipping\n", progress.Asset)
	case progress.Done:
		p.writef("%s: installed (%s)\n", progress.Asset, humanBytes(progress.BytesDone))
	default:
		pct := -1
		if progress.BytesTotal > 0 {
			pct = int(progress.BytesDone * 100 / progress.BytesTotal)
		}
		if pct != p.lastPct[progress.Asset] {
			p.lastPct[progress.Asset] = pct
			if pct >= 0 {
				p.writef("%s: %d%% (%s/%s)\n", progress.Asset, pct, humanBytes(progress.BytesDone), humanBytes(progress.BytesTotal))
			} else {
				p.writef("%s: %s\n", progress.Asset, humanBytes(progress.BytesDone))
			}
		}
	}
}

func (p *progressPrinter) Err() error { return p.err }

func (p *progressPrinter) writef(format string, args ...any) {
	if p.err != nil {
		return
	}
	_, p.err = fmt.Fprintf(p.w, format, args...)
}

// humanBytes renders a byte count in a compact human-readable form.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// provisioningRequiredError builds an actionable error describing which assets
// are missing and how to install them, for surfacing at local backend startup.
func provisioningRequiredError(plan provision.Plan) error {
	var missing []string
	engineMissing := !plan.Engine.Present
	modelMissing := !plan.Model.Present
	for _, s := range plan.Missing() {
		missing = append(missing, fmt.Sprintf("%s (%s)", s.Asset.Name, s.Asset.Kind))
	}
	advice := "run `ajq provision` to install the llama-server engine and default model"
	if modelMissing && plan.Model.Asset.Name != "" && plan.Model.Asset.Name != provision.DefaultModelName {
		if engineMissing {
			advice = fmt.Sprintf("run `ajq provision` to install the llama-server engine, then `ajq models pull %s` to install the selected model", plan.Model.Asset.Name)
		} else {
			advice = fmt.Sprintf("run `ajq models pull %s` to install the selected model", plan.Model.Asset.Name)
		}
	} else if engineMissing && !modelMissing {
		advice = "run `ajq provision` to install the llama-server engine"
	}
	return fmt.Errorf("local backend unavailable: missing %s; %s", strings.Join(missing, ", "), advice)
}
