package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const codexSessionsDir = "csessions"

// CodexSessionsPaths contains every persistent location used by the local product.
// Native Codex JSONL files are not included because they remain owned by Codex CLI.
type CodexSessionsPaths struct {
	ConfigDir    string
	DataDir      string
	CacheDir     string
	ConfigFile   string
	DatabaseFile string
}

// ResolveCodexSessionsPaths resolves XDG locations without creating them. Relative XDG
// values are invalid under the base-directory specification and therefore fall back to HOME.
func ResolveCodexSessionsPaths() (CodexSessionsPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return CodexSessionsPaths{}, fmt.Errorf("resolving home directory: %w", err)
	}

	configBase := xdgBase("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	dataBase := xdgBase("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	cacheBase := xdgBase("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	paths := CodexSessionsPaths{
		ConfigDir: filepath.Join(configBase, codexSessionsDir),
		DataDir:   filepath.Join(dataBase, codexSessionsDir),
		CacheDir:  filepath.Join(cacheBase, codexSessionsDir),
	}
	paths.ConfigFile = filepath.Join(paths.ConfigDir, ConfigFileName)
	paths.DatabaseFile = filepath.Join(paths.DataDir, "sessions.db")
	return paths, nil
}

// LoadCodexSessions loads only the product's XDG configuration. In particular, it does not
// inspect inherited project or ~/.specstory configuration.
func LoadCodexSessions() (*Config, error) {
	paths, err := ResolveCodexSessionsPaths()
	if err != nil {
		return nil, err
	}
	cfg := &Config{}
	if err := loadTOMLFile(paths.ConfigFile, cfg); err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return cfg, fmt.Errorf("loading Codex Sessions config %s: %w", paths.ConfigFile, err)
	}
	return cfg, nil
}

// SaveCodexSessionsResumePrefs persists local TUI preferences under XDG_CONFIG_HOME.
func SaveCodexSessionsResumePrefs(viewMode, lastAgent string) error {
	paths, err := ResolveCodexSessionsPaths()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(paths.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("creating Codex Sessions config directory: %w", err)
	}
	data, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading Codex Sessions config: %w", err)
		}
		data = []byte("# Codex Sessions configuration\n")
	}
	updated := upsertResumeSection(string(data), viewMode, lastAgent)
	if err := os.WriteFile(paths.ConfigFile, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("writing Codex Sessions config: %w", err)
	}
	return nil
}

func xdgBase(envName, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(envName)); filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return fallback
}
