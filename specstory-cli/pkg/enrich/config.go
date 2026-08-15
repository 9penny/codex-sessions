package enrich

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
)

const DefaultBaseURL = "https://api.openai.com/v1"
const DefaultModel = "gpt-5.4-mini"

// Config contains only the settings needed by the explicit AI enrichment path.
type Config struct {
	APIKey  string `toml:"api_key"`
	BaseURL string `toml:"base_url"`
	Model   string `toml:"model"`
}

// Load resolves the private XDG AI configuration file. It never reads a project .env file.
func Load() (Config, error) {
	paths, err := config.ResolveCodexSessionsPaths()
	if err != nil {
		return Config{}, err
	}
	return LoadConfig(filepath.Join(paths.ConfigDir, "ai.toml"))
}

// LoadConfig loads a specific TOML file and then applies CSESSIONS_OPENAI_* environment
// overrides. A missing file is valid so environment-only configuration works.
func LoadConfig(path string) (Config, error) {
	cfg := Config{BaseURL: DefaultBaseURL, Model: DefaultModel}
	info, statErr := os.Stat(path)
	if statErr == nil {
		metadata, err := toml.DecodeFile(path, &cfg)
		if err != nil {
			return Config{}, fmt.Errorf("loading AI configuration: %w", err)
		}
		if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
			return Config{}, fmt.Errorf("AI configuration contains unknown field %q", undecoded[0])
		}
		if strings.TrimSpace(cfg.APIKey) != "" && info.Mode().Perm()&0o077 != 0 {
			return Config{}, fmt.Errorf("AI configuration containing an API key must have permissions 0600")
		}
	} else if !os.IsNotExist(statErr) {
		return Config{}, fmt.Errorf("reading AI configuration metadata: %w", statErr)
	}

	if value := strings.TrimSpace(os.Getenv("CSESSIONS_OPENAI_API_KEY")); value != "" {
		cfg.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("CSESSIONS_OPENAI_BASE_URL")); value != "" {
		cfg.BaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("CSESSIONS_OPENAI_MODEL")); value != "" {
		cfg.Model = value
	}
	cfg.APIKey = strings.TrimSpace(cfg.APIKey)
	cfg.Model = strings.TrimSpace(cfg.Model)
	parsed, err := validateBaseURL(cfg.BaseURL)
	if err != nil {
		return Config{}, err
	}
	cfg.BaseURL = strings.TrimRight(parsed.String(), "/")
	return cfg, nil
}

func validateBaseURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("AI base URL is invalid")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("AI base URL must not contain credentials, a query, or a fragment")
	}
	if parsed.Scheme != "https" {
		host := strings.ToLower(parsed.Hostname())
		ip := net.ParseIP(host)
		if parsed.Scheme != "http" || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return nil, fmt.Errorf("AI base URL must use HTTPS unless it is loopback")
		}
	}
	return parsed, nil
}
