package enricheval

import (
	"context"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/enrich"
)

type fakeGenerator struct{}

func (fakeGenerator) GenerateMetadata(_ context.Context, request enrich.MetadataRequest) (enrich.MetadataResult, error) {
	return enrich.MetadataResult{
		Metadata: enrich.Metadata{Title: request.RedactedConversation, Summary: "中文 search callback index", Tags: []string{"oauth", "sqlite"}},
		Usage:    enrich.Usage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15},
	}, nil
}

func TestEvaluateReportsAggregateScoresWithoutResponseText(t *testing.T) {
	fixtures := []Fixture{
		{Name: "one", Text: "callback", Concepts: []string{"callback"}},
		{Name: "two", Text: "中文", Concepts: []string{"中文"}, WantHan: true},
	}
	summary := Evaluate(context.Background(), fakeGenerator{}, "model", fixtures)
	if summary.Cases != 2 || summary.Passed != 2 || summary.TotalTokens != 30 || summary.Errors != 0 {
		t.Fatalf("summary = %#v", summary)
	}
}
