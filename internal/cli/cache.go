package cli

import (
	"fmt"

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
	return &cobra.Command{
		Use:   "status",
		Short: "Show persistent judgement cache status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			stats, err := semanticcache.Status("")
			if err != nil {
				return err
			}
			if err := writeCacheStats(cmd, stats, false); err != nil {
				return fmt.Errorf("write cache status: %w", err)
			}
			return nil
		},
	}
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
