package cmd

import (
	"errors"
	"fmt"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/redact"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/session"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

var redactNativePreview = redact.RedactContentSafe

const previewBlockedMarkdown = "_(Preview blocked: the native session could not be read and masked safely.)_"

type nativePreviewMsg struct {
	seq      int
	markdown string
	revealed bool
	blocked  bool
}

// loadNativePreview parses the selected native file on demand. The derived FTS body is
// deliberately not accepted as a fallback: a missing or unreadable native file blocks preview.
func loadNativePreview(registry *factory.Registry, indexed *sessionindex.Session, reveal bool) (string, error) {
	if registry == nil || indexed == nil {
		return "", errors.New("native preview is unavailable")
	}
	if indexed.IsCloud || strings.TrimSpace(indexed.NativePath) == "" {
		return "", errors.New("native preview source is unavailable")
	}
	info, err := os.Stat(indexed.NativePath)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("native preview source is unavailable")
	}

	provider, err := registry.Get(indexed.Agent)
	if err != nil {
		return "", fmt.Errorf("resolve native preview provider: %w", err)
	}
	reader, ok := provider.(spi.PathSessionReader)
	if !ok {
		return "", errors.New("native preview is unsupported for this session")
	}
	parsed, err := reader.GetAgentChatSessionByPath(indexed.NativePath, indexed.OriginCwd, false)
	if err != nil || parsed == nil || parsed.SessionData == nil {
		return "", errors.New("native preview source could not be read")
	}
	markdown, err := session.GenerateMarkdownFromAgentSession(parsed.SessionData, false, true)
	if err != nil {
		return "", errors.New("native preview could not be rendered")
	}
	if reveal {
		return markdown, nil
	}
	masked, _, err := redactNativePreview(markdown)
	if err != nil {
		return "", errors.New("native preview redaction is unavailable")
	}
	return masked, nil
}

func nativePreviewCmd(registry *factory.Registry, indexed sessionindex.Session, seq int, reveal bool) tea.Cmd {
	return func() tea.Msg {
		markdown, err := loadNativePreview(registry, &indexed, reveal)
		if err != nil {
			return nativePreviewMsg{seq: seq, blocked: true}
		}
		return nativePreviewMsg{seq: seq, markdown: markdown, revealed: reveal}
	}
}

func (m sessionTUI) applyNativePreview(msg nativePreviewMsg) (tea.Model, tea.Cmd) {
	if !m.previewing || msg.seq != m.previewSeq {
		return m, nil
	}
	markdown := msg.markdown
	if msg.blocked {
		markdown = previewBlockedMarkdown
	}
	m.previewRevealed = msg.revealed && !msg.blocked
	m.reader.SetContent(renderGlamour(markdown, m.width))
	m.reader.GotoTop()
	return m, nil
}
