package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

const enrichmentPromptVersion = 1

type metadataGenerator interface {
	GenerateMetadata(context.Context, enrich.MetadataRequest) (enrich.MetadataResult, error)
}

type enrichmentOptions struct {
	DryRun              bool
	Force               bool
	Limit               int
	PromptVersion       int
	Model               string
	MaxInputTokens      int
	MaxTotalInputTokens int
	MaxOutputTokens     int
}

type enrichmentStats struct {
	Candidates           int
	Prepared             int
	Enriched             int
	EstimatedInputTokens int
	InputTokens          int
	OutputTokens         int
}

func (o enrichmentOptions) validate() error {
	if o.Limit < 1 || o.Limit > 1000 {
		return errors.New("limit must be between 1 and 1000")
	}
	if o.PromptVersion < 1 {
		return errors.New("prompt version must be positive")
	}
	if strings.TrimSpace(o.Model) == "" {
		return errors.New("AI model is required")
	}
	if o.MaxInputTokens < 128 || o.MaxInputTokens > 200000 {
		return errors.New("max input tokens must be between 128 and 200000")
	}
	if o.MaxTotalInputTokens < o.MaxInputTokens || o.MaxTotalInputTokens > 1000000 {
		return errors.New("total input token budget must be at least the per-session limit and no more than 1000000")
	}
	if o.MaxOutputTokens < 64 || o.MaxOutputTokens > 2048 {
		return errors.New("max output tokens must be between 64 and 2048")
	}
	return nil
}

// enrichSessions processes only indexed, local, interactive Codex sessions. It
// never falls back to the FTS body and commits each successful result separately,
// so an interrupted run safely resumes from the next stale fingerprint.
func enrichSessions(ctx context.Context, store *sessionindex.Store, registry *factory.Registry, generator metadataGenerator, opts enrichmentOptions) (enrichmentStats, error) {
	var stats enrichmentStats
	if store == nil || registry == nil {
		return stats, errors.New("AI enrichment is unavailable")
	}
	if err := opts.validate(); err != nil {
		return stats, err
	}
	if !opts.DryRun && generator == nil {
		return stats, errors.New("AI enrichment client is unavailable")
	}

	candidates, err := store.ListEnrichmentCandidates(opts.Limit, opts.PromptVersion, opts.Force)
	if err != nil {
		return stats, fmt.Errorf("selecting enrichment candidates: %w", err)
	}
	stats.Candidates = len(candidates)
	for _, session := range candidates {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		conversation, err := loadRedactedConversation(registry, session)
		if err != nil {
			return stats, err
		}
		conversation, estimatedTokens := truncateConversation(conversation, opts.MaxInputTokens)
		if estimatedTokens == 0 {
			return stats, errors.New("AI enrichment source contained no safe conversation text")
		}
		if stats.EstimatedInputTokens+estimatedTokens > opts.MaxTotalInputTokens {
			break
		}
		stats.Prepared++
		stats.EstimatedInputTokens += estimatedTokens
		if opts.DryRun {
			continue
		}

		result, err := generator.GenerateMetadata(ctx, enrich.MetadataRequest{
			Model: opts.Model, RedactedConversation: conversation, MaxOutputTokens: opts.MaxOutputTokens,
		})
		if err != nil {
			return stats, err
		}
		if err := result.Metadata.Validate(); err != nil {
			return stats, err
		}
		if result.Usage.InputTokens < 0 || result.Usage.OutputTokens < 0 {
			return stats, errors.New("AI metadata response contained invalid token usage")
		}
		metadata := sessionindex.AIMetadata{
			Agent: session.Agent, SessionID: session.SessionID, SourceSize: session.Size,
			SourceMtime: session.Mtime, SourceIndexVersion: session.IndexVersion,
			PromptVersion: opts.PromptVersion, Model: opts.Model, Title: result.Metadata.Title,
			Summary: result.Metadata.Summary, Tags: result.Metadata.Tags,
			InputTokens: result.Usage.InputTokens, OutputTokens: result.Usage.OutputTokens,
			EnrichedAt: time.Now().UTC().Format(time.RFC3339),
		}
		if err := store.UpsertAIMetadata(metadata); err != nil {
			return stats, err
		}
		stats.Enriched++
		stats.InputTokens += result.Usage.InputTokens
		stats.OutputTokens += result.Usage.OutputTokens
	}
	return stats, nil
}

func loadRedactedConversation(registry *factory.Registry, indexed sessionindex.Session) (string, error) {
	if indexed.Agent != "codex" || indexed.IsCloud || strings.TrimSpace(indexed.NativePath) == "" {
		return "", errors.New("AI enrichment source is unavailable")
	}
	if !sourceMatchesFingerprint(indexed) {
		return "", errors.New("AI enrichment source changed after indexing; run reindex first")
	}
	provider, err := registry.Get(indexed.Agent)
	if err != nil {
		return "", errors.New("AI enrichment provider is unavailable")
	}
	reader, ok := provider.(spi.PathSessionReader)
	if !ok {
		return "", errors.New("AI enrichment is unsupported for this session")
	}
	parsed, err := reader.GetAgentChatSessionByPath(indexed.NativePath, indexed.OriginCwd, false)
	if err != nil || parsed == nil || parsed.SessionData == nil {
		return "", errors.New("AI enrichment source could not be read")
	}
	if !sourceMatchesFingerprint(indexed) {
		return "", errors.New("AI enrichment source changed while it was read; run reindex first")
	}
	conversation, err := flattenBodySafe(parsed.SessionData)
	if err != nil {
		return "", errors.New("AI enrichment redaction is unavailable")
	}
	// Known identifiers can legitimately be repeated inside user-visible messages.
	// Remove the exact indexed values after secret detection so structural metadata
	// cannot leak merely because Codex echoed it into conversation text.
	for _, identifier := range []struct {
		value       string
		placeholder string
	}{
		{indexed.NativePath, "[REDACTED:native-path]"},
		{indexed.OriginCwd, "[REDACTED:project-path]"},
		{indexed.SessionID, "[REDACTED:session-id]"},
	} {
		if identifier.value != "" {
			conversation = strings.ReplaceAll(conversation, identifier.value, identifier.placeholder)
		}
	}
	return strings.TrimSpace(conversation), nil
}

func sourceMatchesFingerprint(indexed sessionindex.Session) bool {
	info, err := os.Stat(indexed.NativePath)
	return err == nil && info.Mode().IsRegular() && info.Size() == indexed.Size && info.ModTime().UnixMilli() == indexed.Mtime
}

// truncateConversation uses UTF-8 byte length as a conservative tokenizer-independent
// token upper bound. Long sessions retain both their opening intent and latest resolution.
func truncateConversation(conversation string, maxTokens int) (string, int) {
	conversation = strings.TrimSpace(conversation)
	if len([]byte(conversation)) <= maxTokens {
		return conversation, len([]byte(conversation))
	}
	runes := []rune(conversation)
	marker := []rune("\n\n[... redacted conversation truncated ...]\n\n")
	available := maxTokens - len([]byte(string(marker)))
	if available < 2 {
		prefix := runePrefixWithinBytes(runes, maxTokens)
		return string(prefix), len([]byte(string(prefix)))
	}
	headBudget := available * 7 / 10
	tailBudget := available - headBudget
	head := runePrefixWithinBytes(runes, headBudget)
	tail := runeSuffixWithinBytes(runes[len(head):], tailBudget)
	truncated := make([]rune, 0, len(head)+len(marker)+len(tail))
	truncated = append(truncated, head...)
	truncated = append(truncated, marker...)
	truncated = append(truncated, tail...)
	value := string(truncated)
	return value, len([]byte(value))
}

func runePrefixWithinBytes(runes []rune, budget int) []rune {
	used := 0
	for i, value := range runes {
		size := len([]byte(string(value)))
		if used+size > budget {
			return runes[:i]
		}
		used += size
	}
	return runes
}

func runeSuffixWithinBytes(runes []rune, budget int) []rune {
	used := 0
	for i := len(runes) - 1; i >= 0; i-- {
		size := len([]byte(string(runes[i])))
		if used+size > budget {
			return runes[i+1:]
		}
		used += size
	}
	return runes
}
