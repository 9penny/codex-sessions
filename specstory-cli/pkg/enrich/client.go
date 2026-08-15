package enrich

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const maxResponseBytes = 1 << 20

// Client is an authenticated client used only by explicit enrichment commands.
type Client struct {
	apiKey  string
	baseURL *url.URL
	http    *http.Client
}

func NewClient(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("AI API key is not configured")
	}
	baseURL, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return nil, err
	}
	originHost := baseURL.Host
	originScheme := baseURL.Scheme
	return &Client{
		apiKey:  cfg.APIKey,
		baseURL: baseURL,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(req *http.Request, _ []*http.Request) error {
				if req.URL.Host != originHost || req.URL.Scheme != originScheme {
					return errors.New("refusing AI API redirect outside the configured origin")
				}
				return nil
			},
		},
	}, nil
}

// ListModels returns only model identifiers and never prints endpoint or credential details.
func (c *Client) ListModels(ctx context.Context) ([]string, error) {
	endpoint := strings.TrimRight(c.baseURL.String(), "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, fmt.Errorf("building models request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	response, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("listing AI models: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("listing AI models failed with HTTP %d", response.StatusCode)
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseBytes))
	if err := decoder.Decode(&payload); err != nil {
		return nil, fmt.Errorf("decoding models response: %w", err)
	}
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		if id := strings.TrimSpace(model.ID); id != "" {
			models = append(models, id)
		}
	}
	slices.Sort(models)
	return slices.Compact(models), nil
}
