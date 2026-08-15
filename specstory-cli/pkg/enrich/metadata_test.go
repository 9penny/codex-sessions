package enrich

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGenerateMetadataUsesNonStoredStrictResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "test-model" || body["store"] != false {
			t.Errorf("model/store = %v/%v", body["model"], body["store"])
		}
		if _, exists := body["tools"]; exists {
			t.Error("request unexpectedly enabled tools")
		}
		text, ok := body["text"].(map[string]any)
		if !ok {
			t.Fatalf("text format missing: %#v", body["text"])
		}
		format, ok := text["format"].(map[string]any)
		if !ok || format["type"] != "json_schema" || format["strict"] != true {
			t.Fatalf("strict JSON schema missing: %#v", text)
		}
		encoded, _ := json.Marshal(body["input"])
		if !strings.Contains(string(encoded), "REDACTED synthetic conversation") {
			t.Errorf("redacted input missing: %s", encoded)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"output": []any{map[string]any{
				"type": "message",
				"content": []any{map[string]any{
					"type": "output_text",
					"text": `{"title":"OAuth callback fix","summary":"Fixed callback state validation.","tags":["oauth","go"]}`,
				}},
			}},
			"usage": map[string]any{"input_tokens": 120, "output_tokens": 30, "total_tokens": 150},
		})
	}))
	defer server.Close()

	client, err := NewClient(Config{APIKey: "test-key", BaseURL: server.URL + "/v1"})
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.GenerateMetadata(context.Background(), MetadataRequest{
		Model: "test-model", RedactedConversation: "REDACTED synthetic conversation", MaxOutputTokens: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Metadata.Title != "OAuth callback fix" || len(result.Metadata.Tags) != 2 {
		t.Fatalf("metadata = %#v", result.Metadata)
	}
	if result.Usage.TotalTokens != 150 {
		t.Fatalf("usage = %#v", result.Usage)
	}
}

func TestMetadataValidationRejectsUnboundedOrMalformedOutput(t *testing.T) {
	tests := []Metadata{
		{},
		{Title: strings.Repeat("x", 121), Summary: "ok", Tags: []string{"tag"}},
		{Title: "ok", Summary: strings.Repeat("x", 601), Tags: []string{"tag"}},
		{Title: "ok", Summary: "ok", Tags: []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine"}},
		{Title: "ok", Summary: "ok", Tags: []string{"bad\ntag"}},
	}
	for i, metadata := range tests {
		if err := metadata.Validate(); err == nil {
			t.Errorf("case %d unexpectedly valid: %#v", i, metadata)
		}
	}
}

func TestGenerateMetadataRejectsInvalidBudgetsBeforeNetwork(t *testing.T) {
	client, err := NewClient(Config{APIKey: "test-key", BaseURL: "https://api.example.test/v1"})
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []MetadataRequest{
		{Model: "", RedactedConversation: "text", MaxOutputTokens: 256},
		{Model: "model", RedactedConversation: "", MaxOutputTokens: 256},
		{Model: "model", RedactedConversation: "text", MaxOutputTokens: 0},
		{Model: "model", RedactedConversation: "text", MaxOutputTokens: 2049},
	} {
		if _, err := client.GenerateMetadata(context.Background(), request); err == nil {
			t.Errorf("request unexpectedly passed: %#v", request)
		}
	}
}
