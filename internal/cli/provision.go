package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

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
	cmd := &cobra.Command{
		Use:   "provision",
		Short: "download or locate the local llama-server engine and default model",
		Long:  "Provision the local inference assets used by --backend local: a platform-appropriate llama-server engine and the default GGUF model, cached under ~/.cache/ajq. Already-present assets (including a Homebrew llama-server on PATH or a previously cached model) are detected and left untouched.",
		Example: `  # Safely inspect whether local assets are installed; does not download.
  ajq provision --check`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			controller := resolveProvisionController(opts)
			plan, err := controller.Plan()
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("provisioning check failed: %w", err)}
			}

			out := cmd.OutOrStdout()
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
	return cmd
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
