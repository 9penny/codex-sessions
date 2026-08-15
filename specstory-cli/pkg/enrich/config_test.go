package enrich

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigUsesPrivateFileAndEnvironmentOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ai.toml")
	if err := os.WriteFile(path, []byte("api_key = \"file-key\"\nbase_url = \"https://file.example/v1\"\nmodel = \"file-model\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CSESSIONS_OPENAI_API_KEY", "env-key")
	t.Setenv("CSESSIONS_OPENAI_BASE_URL", "https://env.example/v1/")
	t.Setenv("CSESSIONS_OPENAI_MODEL", "env-model")

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if cfg.APIKey != "env-key" || cfg.BaseURL != "https://env.example/v1" || cfg.Model != "env-model" {
		t.Fatalf("LoadConfig() = %#v", cfg)
	}
}

func TestLoadConfigDefaultsToEvaluatedModel(t *testing.T) {
	t.Setenv("CSESSIONS_OPENAI_API_KEY", "")
	t.Setenv("CSESSIONS_OPENAI_BASE_URL", "")
	t.Setenv("CSESSIONS_OPENAI_MODEL", "")
	cfg, err := LoadConfig(filepath.Join(t.TempDir(), "missing.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Model != "gpt-5.4-mini" || cfg.BaseURL != DefaultBaseURL {
		t.Fatalf("defaults = %#v", cfg)
	}
}

func TestLoadConfigRejectsReadableKeyFileAndUnknownFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
		mode    os.FileMode
		want    string
	}{
		{name: "readable key", content: "api_key = \"secret\"\n", mode: 0o644, want: "permissions"},
		{name: "unknown field", content: "surprise = true\n", mode: 0o600, want: "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "ai.toml")
			if err := os.WriteFile(path, []byte(tt.content), tt.mode); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(path, tt.mode); err != nil {
				t.Fatal(err)
			}
			_, err := LoadConfig(path)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tt.want) {
				t.Fatalf("LoadConfig() error = %v, want %q", err, tt.want)
			}
			if strings.Contains(err.Error(), "secret") {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

func TestValidateEndpointAllowsHTTPSAndLoopbackOnly(t *testing.T) {
	for _, raw := range []string{"https://api.openai.com/v1", "http://127.0.0.1:8080/v1", "http://localhost:8080/v1"} {
		if _, err := validateBaseURL(raw); err != nil {
			t.Errorf("validateBaseURL(%q) error = %v", raw, err)
		}
	}
	for _, raw := range []string{"http://api.example/v1", "https://user:pass@example.test/v1", "file:///tmp/api", "https://example.test/v1?key=x"} {
		if _, err := validateBaseURL(raw); err == nil {
			t.Errorf("validateBaseURL(%q) unexpectedly passed", raw)
		}
	}
}
