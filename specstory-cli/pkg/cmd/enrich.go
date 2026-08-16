package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/utils"
	"github.com/spf13/cobra"
)

type enrichmentScope struct {
	ProjectID string
	Label     string
}

func resolveEnrichmentScope(allProjects bool, projectPath string) (enrichmentScope, error) {
	if allProjects && strings.TrimSpace(projectPath) != "" {
		return enrichmentScope{}, fmt.Errorf("--all and --project cannot be used together")
	}
	if allProjects {
		return enrichmentScope{Label: "all projects"}, nil
	}

	path := strings.TrimSpace(projectPath)
	scopeKind := "project"
	if path == "" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return enrichmentScope{}, fmt.Errorf("resolving current project: %w", err)
		}
		scopeKind = "current project"
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return enrichmentScope{}, fmt.Errorf("resolving project path: %w", err)
	}
	info, err := os.Stat(absPath)
	if err != nil {
		return enrichmentScope{}, fmt.Errorf("reading project path: %w", err)
	}
	if !info.IsDir() {
		return enrichmentScope{}, fmt.Errorf("project path is not a directory: %s", absPath)
	}
	projectID, projectName, err := utils.ComputeProjectID(absPath)
	if err != nil {
		return enrichmentScope{}, err
	}
	return enrichmentScope{ProjectID: projectID, Label: fmt.Sprintf("%s (%s)", scopeKind, projectName)}, nil
}

// CreateEnrichCommand exposes the only command family allowed to construct an AI HTTP client.
func CreateEnrichCommand() *cobra.Command {
	var (
		dryRun              bool
		force               bool
		confirmed           bool
		limit               int
		model               string
		maxInputTokens      int
		maxTotalInputTokens int
		maxOutputTokens     int
		allProjects         bool
		projectPath         string
	)
	command := &cobra.Command{
		Use:   "enrich",
		Short: "Generate optional AI session metadata",
		Long: "AI enrichment is opt-in. Ordinary Codex Sessions commands remain local-only. " +
			"This command reads native Codex sessions, structurally keeps only user/assistant text, " +
			"redacts detected secrets, applies explicit token budgets, and stores only derived metadata locally. " +
			"Live requests use store=false and no tools. API providers may still retain abuse-monitoring logs.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !dryRun && !confirmed {
				return fmt.Errorf("live enrichment requires --yes to confirm sending redacted conversation text")
			}
			scope, err := resolveEnrichmentScope(allProjects, projectPath)
			if err != nil {
				return err
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "Scope: %s\n", scope.Label); err != nil {
				return err
			}
			selectedModel := model
			var generator metadataGenerator
			if dryRun {
				if selectedModel == "" {
					selectedModel = enrich.DefaultModel
				}
			} else {
				cfg, err := enrich.Load()
				if err != nil {
					return err
				}
				if selectedModel == "" {
					selectedModel = cfg.Model
				}
				client, err := enrich.NewClient(cfg)
				if err != nil {
					return err
				}
				generator = client
				if _, err := fmt.Fprintln(cmd.OutOrStdout(), "Confirmed: sending only redacted user/assistant conversation text; provider retention policies may apply."); err != nil {
					return err
				}
			}

			databasePath, err := sessionindex.DefaultPath()
			if err != nil {
				return err
			}
			store, err := sessionindex.Open(databasePath)
			if err != nil {
				return fmt.Errorf("opening session index: %w", err)
			}
			defer func() { _ = store.Close() }()
			stats, err := enrichSessions(cmd.Context(), store, factory.GetRegistry(), generator, enrichmentOptions{
				DryRun: dryRun, Force: force, ProjectID: scope.ProjectID, Limit: limit,
				PromptVersion: sessionindex.CurrentAIPromptVersion,
				Model:         selectedModel, MaxInputTokens: maxInputTokens,
				MaxTotalInputTokens: maxTotalInputTokens, MaxOutputTokens: maxOutputTokens,
			})
			if err != nil {
				return err
			}
			if dryRun {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Dry run: candidates=%d prepared=%d estimated_input_tokens=%d requests=0 writes=0\n",
					stats.Candidates, stats.Prepared, stats.EstimatedInputTokens)
			} else {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "Enriched: candidates=%d prepared=%d saved=%d input_tokens=%d output_tokens=%d\n",
					stats.Candidates, stats.Prepared, stats.Enriched, stats.InputTokens, stats.OutputTokens)
			}
			return err
		},
	}
	command.Flags().BoolVar(&dryRun, "dry-run", false, "parse, redact, and budget locally without API requests or writes")
	command.Flags().BoolVar(&force, "force", false, "regenerate metadata even when the source fingerprint is unchanged")
	command.Flags().BoolVar(&confirmed, "yes", false, "confirm live transmission of redacted conversation text")
	command.Flags().IntVar(&limit, "limit", 10, "maximum sessions to consider (1-1000)")
	command.Flags().StringVar(&model, "model", "", "model ID (defaults to AI configuration, or gpt-5.4-mini)")
	command.Flags().IntVar(&maxInputTokens, "max-input-tokens", 2000, "conservative per-session input budget")
	command.Flags().IntVar(&maxTotalInputTokens, "max-total-input-tokens", 10000, "conservative total input budget")
	command.Flags().IntVar(&maxOutputTokens, "max-output-tokens", 256, "maximum output tokens per request")
	command.Flags().BoolVar(&allProjects, "all", false, "process sessions from all indexed projects")
	command.Flags().StringVar(&projectPath, "project", "", "process the project containing this directory")
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
