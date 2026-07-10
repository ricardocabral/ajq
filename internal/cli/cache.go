package cli

import (
	"encoding/json"
	"fmt"
	"io"

	semanticcache "github.com/ricardocabral/ajq/internal/cache"
	"github.com/spf13/cobra"
)

func newCacheCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "cache",
		Short: "Manage the persistent judgement cache",
		Long:  "Inspect or clear cached semantic judgements stored on this machine.",
		Example: `  # Inspect the local semantic judgement cache.
  ajq cache status`,
	}
	cmd.AddCommand(newCacheStatusCommand())
	cmd.AddCommand(newCacheClearCommand())
	return cmd
}

func newCacheStatusCommand() *cobra.Command {
	var jsonOutput bool
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show persistent judgement cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stats, statusErr := semanticcache.Status("")
			if statusErr != nil && !jsonOutput {
				return statusErr
			}
			if jsonOutput {
				if stats.Location == "" {
					stats.Location = semanticcache.JudgementsDir("")
				}
				if err := writeCacheStatusJSON(cmd.OutOrStdout(), stats, statusErr == nil); err != nil {
					return &ExitError{Code: 1, Err: fmt.Errorf("write cache status: %w", err)}
				}
				if statusErr != nil {
					return &ExitError{Code: 1, Silent: true}
				}
				return nil
			}
			if err := writeCacheStats(cmd, stats, false); err != nil {
				return fmt.Errorf("write cache status: %w", err)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print the versioned machine-readable cache status")
	return cmd
}

func newCacheClearCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "clear",
		Short: "Clear persistent judgement cache entries",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stats, err := semanticcache.Clear("")
			if err != nil {
				return err
			}
			if err := writeCacheStats(cmd, stats, true); err != nil {
				return fmt.Errorf("write cache clear: %w", err)
			}
			return nil
		},
	}
}

// cacheStatusDocument is the deterministic v1 wire contract for `ajq cache status --json`.
type cacheStatusDocument struct {
	SchemaVersion string `json:"schema_version"`
	Availability  string `json:"availability"`
	Path          string `json:"path"`
	Entries       int    `json:"entries"`
	Bytes         int64  `json:"bytes"`
	Error         string `json:"error,omitempty"`
}

func writeCacheStatusJSON(w io.Writer, stats semanticcache.Stats, available bool) error {
	document := cacheStatusDocument{
		SchemaVersion: "1",
		Availability:  "available",
		Path:          stats.Location,
		Entries:       stats.Entries,
		Bytes:         stats.Bytes,
	}
	if !available {
		document.Availability = "unavailable"
		document.Entries = 0
		document.Bytes = 0
		document.Error = "status_unavailable"
	}
	return json.NewEncoder(w).Encode(document)
}

func writeCacheStats(cmd *cobra.Command, stats semanticcache.Stats, cleared bool) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "location: %s\n", stats.Location); err != nil {
		return err
	}
	if cleared {
		if _, err := fmt.Fprintf(out, "cleared_entries: %d\n", stats.Entries); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "freed_bytes: %d\n", stats.Bytes)
		return err
	}
	if _, err := fmt.Fprintf(out, "entries: %d\n", stats.Entries); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "bytes: %d\n", stats.Bytes)
	return err
}
