package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/ricardocabral/ajq/internal/config"
	"github.com/ricardocabral/ajq/internal/provision"
	"github.com/spf13/cobra"
)

type modelCatalogProvider interface {
	ModelCatalog() provision.Catalog
}

// newModelsCommand builds `ajq models`, the local GGUF model management group.
func newModelsCommand(opts Options) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "models",
		Short: "list, pull, and select local GGUF models",
		Long:  "Inspect and manage the catalog of local GGUF models used by --backend local.",
		Example: `  # List local model choices and whether they are installed.
  ajq models list`,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.AddCommand(newModelsListCommand(opts))
	cmd.AddCommand(newModelsPullCommand(opts))
	cmd.AddCommand(newModelsUseCommand(opts))
	return cmd
}

func newModelsListCommand(opts Options) *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:           "list",
		Short:         "list available local models",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			controller := resolveProvisionController(opts)
			var err error
			if jsonOutput {
				err = writeModelsListJSON(cmd.OutOrStdout(), controller, activeCatalogModelJSON(cmd))
			} else {
				active, activeNote := activeCatalogModel(cmd)
				err = writeModelsList(cmd.OutOrStdout(), controller, active, activeNote)
			}
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write models list: %w", err)}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the versioned machine-readable model status")
	return cmd
}

func newModelsPullCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:           "pull <name>",
		Short:         "download a pinned local model",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			controller := resolveProvisionController(opts)
			plan, err := controller.PlanModelOnly(name)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if plan.Model.Present {
				if _, err := fmt.Fprintf(cmd.OutOrStdout(), "model %s already installed: %s\n", plan.Model.Asset.Name, plan.Model.Path); err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("write models pull result: %w", err)}
				}
				return nil
			}
			progress := newProgressPrinter(cmd.ErrOrStderr())
			if err := controller.InstallModel(cmd.Context(), plan, progress.Print); err != nil {
				if progressErr := progress.Err(); progressErr != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("models pull failed: %w; write progress: %v", err, progressErr)}
				}
				return &ExitError{Code: 1, Err: fmt.Errorf("models pull failed: %w", err)}
			}
			if progressErr := progress.Err(); progressErr != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write models pull progress: %w", progressErr)}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "model %s installed: %s\n", plan.Model.Asset.Name, plan.Model.Path); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write models pull result: %w", err)}
			}
			return nil
		},
	}
}

func newModelsUseCommand(opts Options) *cobra.Command {
	return &cobra.Command{
		Use:           "use <name>",
		Short:         "persist the active local model",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := strings.TrimSpace(args[0])
			controller := resolveProvisionController(opts)
			plan, err := controller.PlanModelOnly(name)
			if err != nil {
				return &ExitError{Code: 1, Err: err}
			}
			if !plan.Model.Present {
				return &ExitError{Code: 1, Err: fmt.Errorf("model %s is not installed; run `ajq models pull %s` first", plan.Model.Asset.Name, plan.Model.Asset.Name)}
			}
			path, err := config.SetModel(plan.Model.Asset.Name, config.WriteOptions{})
			if err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write config model: %w", err)}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "active model set to %s in %s\n", plan.Model.Asset.Name, path); err != nil {
				return &ExitError{Code: 1, Err: fmt.Errorf("write models use result: %w", err)}
			}
			return nil
		},
	}
}

func writeModelsList(w io.Writer, controller ProvisionController, active, activeNote string) error {
	catalog := catalogForController(controller)
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(tw, "NAME\tACTIVE\tINSTALLED\tSIZE\tRAM"); err != nil {
		return err
	}
	for _, model := range catalog.ModelsList() {
		installed := "no"
		plan, err := controller.PlanModelOnly(model.Name)
		if err == nil && plan.Model.Present {
			installed = "yes"
		}
		activeMark := ""
		if active == model.Name {
			activeMark = "*"
		}
		if _, err := fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", model.Name, activeMark, installed, humanBytes(model.Asset.Size), model.RAMNote); err != nil {
			return err
		}
	}
	if activeNote != "" {
		if _, err := fmt.Fprintf(tw, "# %s\n", activeNote); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// modelsListDocument is the deterministic v1 wire contract for `ajq models list --json`.
type modelsListDocument struct {
	SchemaVersion string           `json:"schema_version"`
	Active        modelActiveState `json:"active"`
	Models        []modelListEntry `json:"models"`
}

type modelActiveState struct {
	State string `json:"state"`
	Name  string `json:"name,omitempty"`
	Path  string `json:"path,omitempty"`
}

type modelListEntry struct {
	Name      string `json:"name"`
	Active    bool   `json:"active"`
	Installed bool   `json:"installed"`
	Filename  string `json:"filename"`
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	RAM       string `json:"ram"`
}

func writeModelsListJSON(w io.Writer, controller ProvisionController, active modelActiveState) error {
	catalog := catalogForController(controller)
	document := modelsListDocument{SchemaVersion: "1", Active: active, Models: make([]modelListEntry, 0, len(catalog.Models))}
	for _, model := range catalog.ModelsList() {
		plan, err := controller.PlanModelOnly(model.Name)
		entry := modelListEntry{
			Name:      model.Name,
			Active:    active.State == "catalog" && active.Name == model.Name,
			Installed: err == nil && plan.Model.Present,
			Filename:  model.Asset.Filename,
			Path:      plan.Model.Path,
			SizeBytes: model.Asset.Size,
			RAM:       model.RAMNote,
		}
		if err != nil {
			entry.Path = provision.NewLayout("").ModelPath(model.Asset.Filename)
		}
		document.Models = append(document.Models, entry)
	}
	if active.State == "catalog" {
		found := false
		for _, model := range document.Models {
			if model.Active {
				found = true
				break
			}
		}
		if !found {
			document.Active = modelActiveState{State: "unknown"}
		}
	}
	return json.NewEncoder(w).Encode(document)
}

func activeCatalogModelJSON(cmd *cobra.Command) modelActiveState {
	fileValues, err := config.LoadWithOptions(config.LoadOptions{Stderr: io.Discard})
	if err != nil {
		return modelActiveState{State: "unknown"}
	}
	envValues, err := config.Env(os.Getenv)
	if err != nil {
		return modelActiveState{State: "unknown"}
	}
	settings := config.Resolve(config.Values{}, envValues, fileValues, backendRegistryDefaultValues("local"))
	resolved, err := resolveLocalModelRequest(settings.Model)
	if err != nil {
		return modelActiveState{State: "unknown"}
	}
	if resolved.PathLike {
		return modelActiveState{State: "path_like", Path: resolved.Path}
	}
	return modelActiveState{State: "catalog", Name: resolved.Name}
}

func catalogForController(controller ProvisionController) provision.Catalog {
	if provider, ok := controller.(modelCatalogProvider); ok {
		return provider.ModelCatalog()
	}
	return provision.DefaultCatalog()
}

func activeCatalogModel(cmd *cobra.Command) (name, note string) {
	fileValues, err := config.LoadWithOptions(config.LoadOptions{Stderr: cmd.ErrOrStderr()})
	if err != nil {
		return "", fmt.Sprintf("active model unknown: %v", err)
	}
	envValues, err := config.Env(os.Getenv)
	if err != nil {
		return "", fmt.Sprintf("active model unknown: %v", err)
	}
	settings := config.Resolve(config.Values{}, envValues, fileValues, backendRegistryDefaultValues("local"))
	resolved, err := resolveLocalModelRequest(settings.Model)
	if err != nil {
		return "", fmt.Sprintf("active model %q is not a catalog model", settings.Model)
	}
	if resolved.PathLike {
		return "", fmt.Sprintf("active model is path-like (%s), not a catalog entry", settings.Model)
	}
	return resolved.Name, ""
}

func backendRegistryDefaultValues(name string) config.Values {
	registration, ok := lookupBackend(name)
	if !ok {
		return config.Values{}
	}
	return registration.defaults()
}
