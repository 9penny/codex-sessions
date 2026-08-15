package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

type recordingMetadataGenerator struct {
	requests []enrich.MetadataRequest
}

func openTempSessionStore(t *testing.T) *sessionindex.Store {
	t.Helper()
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func (g *recordingMetadataGenerator) GenerateMetadata(_ context.Context, request enrich.MetadataRequest) (enrich.MetadataResult, error) {
	g.requests = append(g.requests, request)
	return enrich.MetadataResult{
		Metadata: enrich.Metadata{Title: "Safe title", Summary: "Safe summary", Tags: []string{"testing", "privacy"}},
		Usage:    enrich.Usage{InputTokens: 120, OutputTokens: 24, TotalTokens: 144},
	}, nil
}

func indexedEnrichmentFixture(t *testing.T, store *sessionindex.Store) sessionindex.Session {
	t.Helper()
	path := writeNativePreviewFixture(t)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	session := sessionindex.Session{
		ProjectID: "synthetic", ProjectName: "synthetic", Agent: "codex",
		SessionID: "11111111-1111-1111-1111-111111111111", NativePath: path,
		OriginCwd: "/synthetic/project", Kind: "interactive", Size: info.Size(),
		Mtime: info.ModTime().UnixMilli(), IndexVersion: 9,
		CreatedAt: "2026-08-15T01:00:00Z", UpdatedAt: "2026-08-15T01:00:07Z",
	}
	if err := store.Upsert(session); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestEnrichSessionsSendsOnlyRedactedConversationAndPersistsMetadata(t *testing.T) {
	store := openTempSessionStore(t)
	session := indexedEnrichmentFixture(t, store)
	generator := &recordingMetadataGenerator{}

	stats, err := enrichSessions(context.Background(), store, factory.GetRegistry(), generator, enrichmentOptions{
		Limit: 10, PromptVersion: 1, Model: "test-model", MaxInputTokens: 2000,
		MaxTotalInputTokens: 10000, MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Enriched != 1 || len(generator.requests) != 1 {
		t.Fatalf("stats=%+v requests=%d; want one enrichment", stats, len(generator.requests))
	}
	sent := generator.requests[0].RedactedConversation
	for _, forbidden := range []string{previewTestSecret, "synthetic reasoning marker", "tool-input-marker", "tool-output-marker", session.NativePath, session.OriginCwd, session.SessionID} {
		if strings.Contains(sent, forbidden) {
			t.Fatalf("outbound conversation contains forbidden value %q: %s", forbidden, sent)
		}
	}
	for _, required := range []string{"user asks with key", "[REDACTED:gcp-api-key]", "synthetic assistant answer", "answer after malformed line"} {
		if !strings.Contains(sent, required) {
			t.Fatalf("outbound conversation is missing %q: %s", required, sent)
		}
	}
	stored, ok, err := store.GetAIMetadata("codex", session.SessionID)
	if err != nil || !ok || stored.Title != "Safe title" || stored.Model != "test-model" || stored.SourceMtime != session.Mtime {
		t.Fatalf("stored=%+v ok=%v err=%v", stored, ok, err)
	}
}

func TestEnrichSessionsDryRunMakesNoRequestOrWrite(t *testing.T) {
	store := openTempSessionStore(t)
	session := indexedEnrichmentFixture(t, store)
	generator := &recordingMetadataGenerator{}

	stats, err := enrichSessions(context.Background(), store, factory.GetRegistry(), generator, enrichmentOptions{
		DryRun: true, Limit: 10, PromptVersion: 1, Model: "test-model",
		MaxInputTokens: 2000, MaxTotalInputTokens: 10000, MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if stats.Prepared != 1 || stats.Enriched != 0 || len(generator.requests) != 0 {
		t.Fatalf("stats=%+v requests=%d; dry run performed work", stats, len(generator.requests))
	}
	if _, ok, err := store.GetAIMetadata("codex", session.SessionID); err != nil || ok {
		t.Fatalf("dry run persisted metadata: ok=%v err=%v", ok, err)
	}
}

func TestEnrichSessionsRefusesSourceChangedAfterIndex(t *testing.T) {
	store := openTempSessionStore(t)
	session := indexedEnrichmentFixture(t, store)
	file, err := os.OpenFile(session.NativePath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	generator := &recordingMetadataGenerator{}

	_, err = enrichSessions(context.Background(), store, factory.GetRegistry(), generator, enrichmentOptions{
		Limit: 10, PromptVersion: 1, Model: "test-model", MaxInputTokens: 2000,
		MaxTotalInputTokens: 10000, MaxOutputTokens: 256,
	})
	if err == nil || len(generator.requests) != 0 {
		t.Fatalf("changed source err=%v requests=%d; want fail closed", err, len(generator.requests))
	}
}

func TestTruncateConversationPreservesHeadAndTailWithinBudget(t *testing.T) {
	input := strings.Repeat("a", 80) + "MIDDLE" + strings.Repeat("z", 80)
	got, tokens := truncateConversation(input, 100)
	if tokens > 100 || !strings.HasPrefix(got, strings.Repeat("a", 20)) || !strings.HasSuffix(got, strings.Repeat("z", 10)) {
		t.Fatalf("truncateConversation tokens=%d value=%q", tokens, got)
	}
}

func TestTruncateConversationUsesUTF8BytesAsConservativeEstimate(t *testing.T) {
	got, tokens := truncateConversation(strings.Repeat("会", 100), 128)
	if tokens != len([]byte(got)) || tokens > 128 || !strings.HasPrefix(got, "会") || !strings.HasSuffix(got, "会") {
		t.Fatalf("truncateConversation tokens=%d bytes=%d value=%q", tokens, len([]byte(got)), got)
	}
}
