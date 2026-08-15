package cmd

import (
	"fmt"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
	"github.com/spf13/cobra"
)

// CreateEnrichCommand exposes the only command family allowed to construct an AI HTTP client.
func CreateEnrichCommand() *cobra.Command {
	command := &cobra.Command{
		Use:   "enrich",
		Short: "Explicitly manage optional AI session metadata",
		Long: "AI enrichment is opt-in. Ordinary Codex Sessions commands remain local-only. " +
			"Only redacted synthetic evaluation data is supported at this stage.",
	}
	command.AddCommand(&cobra.Command{
		Use:   "models",
		Short: "List model IDs exposed by the configured API endpoint",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := enrich.Load()
			if err != nil {
				return err
			}
			client, err := enrich.NewClient(cfg)
			if err != nil {
				return err
			}
			models, err := client.ListModels(cmd.Context())
			if err != nil {
				return err
			}
			for _, model := range models {
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), model); err != nil {
					return fmt.Errorf("writing model list: %w", err)
				}
			}
			return nil
		},
	})
	return command
}
