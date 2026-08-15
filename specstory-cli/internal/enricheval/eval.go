package enricheval

import (
	"context"
	"strings"
	"time"
	"unicode"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
)

type Generator interface {
	GenerateMetadata(context.Context, enrich.MetadataRequest) (enrich.MetadataResult, error)
}

type Fixture struct {
	Name     string
	Text     string
	Concepts []string
	WantHan  bool
}

var DefaultFixtures = []Fixture{
	{Name: "english", Text: "User: The OAuth callback rejects valid state after a restart.\nAssistant: Persist the state nonce and verify it once.", Concepts: []string{"oauth", "callback"}},
	{Name: "chinese", Text: "用户：中文搜索只能匹配完整句子。\n助手：给连续中文生成二元词并保持可读摘要。", Concepts: []string{"中文", "搜索"}, WantHan: true},
	{Name: "mixed", Text: "User: SQLite reindex is slow for 中文项目.\nAssistant: Reuse unchanged fingerprints and update only stale FTS rows.", Concepts: []string{"sqlite", "index"}},
}

type Summary struct {
	Model        string `json:"model"`
	Cases        int    `json:"cases"`
	Passed       int    `json:"passed"`
	ConceptScore int    `json:"concept_score"`
	InputTokens  int    `json:"input_tokens"`
	OutputTokens int    `json:"output_tokens"`
	TotalTokens  int    `json:"total_tokens"`
	DurationMS   int64  `json:"duration_ms"`
	Errors       int    `json:"errors"`
}

func Evaluate(ctx context.Context, generator Generator, model string, fixtures []Fixture) Summary {
	summary := Summary{Model: model, Cases: len(fixtures)}
	started := time.Now()
	for _, fixture := range fixtures {
		result, err := generator.GenerateMetadata(ctx, enrich.MetadataRequest{
			Model: model, RedactedConversation: fixture.Text, MaxOutputTokens: 512,
		})
		if err != nil {
			summary.Errors++
			continue
		}
		combined := strings.ToLower(result.Metadata.Title + " " + result.Metadata.Summary + " " + strings.Join(result.Metadata.Tags, " "))
		concepts := 0
		for _, concept := range fixture.Concepts {
			if strings.Contains(combined, strings.ToLower(concept)) {
				concepts++
			}
		}
		languageOK := !fixture.WantHan || strings.ContainsFunc(combined, func(r rune) bool {
			return unicode.Is(unicode.Han, r)
		})
		if concepts == len(fixture.Concepts) && languageOK {
			summary.Passed++
		}
		summary.ConceptScore += concepts
		summary.InputTokens += result.Usage.InputTokens
		summary.OutputTokens += result.Usage.OutputTokens
		summary.TotalTokens += result.Usage.TotalTokens
	}
	summary.DurationMS = time.Since(started).Milliseconds()
	return summary
}
