package enrich

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"
)

const metadataInstructions = `Generate discovery metadata for a redacted Codex CLI conversation.
Use the conversation's language. Describe only the supplied text. Never infer secrets, paths,
identities, or hidden context. Return the required JSON schema.`

type Metadata struct {
	Title   string   `json:"title"`
	Summary string   `json:"summary"`
	Tags    []string `json:"tags"`
}

func (m Metadata) Validate() error {
	if strings.TrimSpace(m.Title) == "" || len([]rune(m.Title)) > 120 || containsControl(m.Title) {
		return errors.New("generated title is missing or invalid")
	}
	if strings.TrimSpace(m.Summary) == "" || len([]rune(m.Summary)) > 600 || containsControl(m.Summary) {
		return errors.New("generated summary is missing or invalid")
	}
	if len(m.Tags) == 0 || len(m.Tags) > 8 {
		return errors.New("generated tags count is invalid")
	}
	for _, tag := range m.Tags {
		if strings.TrimSpace(tag) == "" || len([]rune(tag)) > 40 || containsControl(tag) {
			return errors.New("generated tag is invalid")
		}
	}
	return nil
}

func containsControl(value string) bool {
	return strings.ContainsFunc(value, unicode.IsControl)
}

type MetadataRequest struct {
	Model                string
	RedactedConversation string
	MaxOutputTokens      int
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
}

type MetadataResult struct {
	Metadata Metadata
	Usage    Usage
}

// GenerateMetadata sends only caller-supplied redacted text with storage disabled and no tools.
func (c *Client) GenerateMetadata(ctx context.Context, request MetadataRequest) (MetadataResult, error) {
	request.Model = strings.TrimSpace(request.Model)
	request.RedactedConversation = strings.TrimSpace(request.RedactedConversation)
	if request.Model == "" {
		return MetadataResult{}, errors.New("AI model is required")
	}
	if request.RedactedConversation == "" {
		return MetadataResult{}, errors.New("redacted conversation is empty")
	}
	if request.MaxOutputTokens < 64 || request.MaxOutputTokens > 2048 {
		return MetadataResult{}, errors.New("max output tokens must be between 64 and 2048")
	}

	payload := map[string]any{
		"model":             request.Model,
		"store":             false,
		"instructions":      metadataInstructions,
		"input":             request.RedactedConversation,
		"max_output_tokens": request.MaxOutputTokens,
		"text": map[string]any{
			"format": map[string]any{
				"type":   "json_schema",
				"name":   "codex_session_metadata",
				"strict": true,
				"schema": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"required":             []string{"title", "summary", "tags"},
					"properties": map[string]any{
						"title":   map[string]any{"type": "string", "maxLength": 120},
						"summary": map[string]any{"type": "string", "maxLength": 600},
						"tags": map[string]any{
							"type": "array", "minItems": 1, "maxItems": 8,
							"items": map[string]any{"type": "string", "maxLength": 40},
						},
					},
				},
			},
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return MetadataResult{}, fmt.Errorf("encoding metadata request: %w", err)
	}
	endpoint := strings.TrimRight(c.baseURL.String(), "/") + "/responses"
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return MetadataResult{}, fmt.Errorf("building metadata request: %w", err)
	}
	httpRequest.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpRequest.Header.Set("Content-Type", "application/json")
	httpRequest.Header.Set("Accept", "application/json")
	response, err := c.http.Do(httpRequest)
	if err != nil {
		return MetadataResult{}, errors.New("generating AI metadata failed: transport error")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return MetadataResult{}, fmt.Errorf("generating AI metadata failed with HTTP %d", response.StatusCode)
	}
	var resultPayload struct {
		Output []struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
		Usage Usage `json:"usage"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(&resultPayload); err != nil {
		return MetadataResult{}, errors.New("decoding AI metadata response failed")
	}
	var outputText string
	for _, output := range resultPayload.Output {
		for _, content := range output.Content {
			if output.Type == "message" && content.Type == "output_text" {
				outputText = content.Text
				break
			}
		}
	}
	if outputText == "" {
		return MetadataResult{}, errors.New("AI metadata response contained no output text")
	}
	var metadata Metadata
	if err := json.Unmarshal([]byte(outputText), &metadata); err != nil {
		return MetadataResult{}, errors.New("AI metadata response was not valid structured JSON")
	}
	if err := metadata.Validate(); err != nil {
		return MetadataResult{}, err
	}
	return MetadataResult{Metadata: metadata, Usage: resultPayload.Usage}, nil
}
