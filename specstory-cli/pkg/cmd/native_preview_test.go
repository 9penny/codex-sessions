package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
)

const previewTestSecret = "AIzaSyD8xKq2mL9nP4rT7wZ0aB3cE6fH1jG5kM7"

func writeNativePreviewFixture(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "rollout-test-session.jsonl")
	content := `{"timestamp":"2026-08-15T01:00:00Z","type":"session_meta","payload":{"id":"11111111-1111-1111-1111-111111111111","timestamp":"2026-08-15T01:00:00Z","cwd":"/synthetic/project","source":"cli"}}
{"timestamp":"2026-08-15T01:00:01Z","type":"turn_context","payload":{"model":"gpt-test"}}
{"timestamp":"2026-08-15T01:00:02Z","type":"event_msg","payload":{"type":"user_message","message":"user asks with key ` + previewTestSecret + `"}}
{"timestamp":"2026-08-15T01:00:03Z","type":"event_msg","payload":{"type":"agent_reasoning","text":"synthetic reasoning marker"}}
{"timestamp":"2026-08-15T01:00:04Z","type":"event_msg","payload":{"type":"agent_message","message":"synthetic assistant answer"}}
{"timestamp":"2026-08-15T01:00:05Z","type":"response_item","payload":{"type":"function_call","name":"exec_command","call_id":"call-1","arguments":"{\"cmd\":\"printf tool-input-marker\"}"}}
{"timestamp":"2026-08-15T01:00:06Z","type":"response_item","payload":{"type":"function_call_output","call_id":"call-1","output":"tool-output-marker"}}
{malformed synthetic line}
{"timestamp":"2026-08-15T01:00:07Z","type":"event_msg","payload":{"type":"agent_message","message":"answer after malformed line"}}
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func previewKey(text string, code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg(tea.Key{Text: text, Code: code})
}

func newNativePreviewModel(session *sessionindex.Session) sessionTUI {
	model := newSessionTUI(nil, factory.GetRegistry(), "project", "project", []sessionindex.Session{*session},
		map[string]agentMeta{"codex": {name: "Codex CLI"}}, nil,
		sessionTUIOpts{title: "Codex Sessions", localOnly: true})
	model.width = 100
	model.height = 30
	return model
}

func TestNativePreviewRevealIsExplicitAndClearedOnClose(t *testing.T) {
	session := &sessionindex.Session{
		Agent:      "codex",
		SessionID:  "11111111-1111-1111-1111-111111111111",
		NativePath: writeNativePreviewFixture(t),
		OriginCwd:  "/synthetic/project",
	}

	openedModel, loadCmd := newNativePreviewModel(session).openPreview(session)
	opened := openedModel.(sessionTUI)
	if !opened.previewing || loadCmd == nil {
		t.Fatal("openPreview() did not start an asynchronous native preview")
	}
	maskedMsg, ok := loadCmd().(nativePreviewMsg)
	if !ok {
		t.Fatal("native preview command returned an unexpected message")
	}
	if strings.Contains(maskedMsg.markdown, previewTestSecret) || !strings.Contains(maskedMsg.markdown, "[REDACTED:gcp-api-key]") {
		t.Fatalf("default preview message is not masked:\n%s", maskedMsg.markdown)
	}
	maskedModel, _ := opened.Update(maskedMsg)
	masked := maskedModel.(sessionTUI)
	if masked.previewRevealed {
		t.Fatal("default preview is marked revealed")
	}
	if view := masked.reader.View(); strings.Contains(view, previewTestSecret) {
		t.Fatalf("default reader contains the raw secret:\n%s", view)
	}
	if chrome := masked.renderPreview(); !strings.Contains(chrome, "MASKED") || !strings.Contains(chrome, "R reveal raw") {
		t.Fatalf("masked preview state is not visible:\n%s", chrome)
	}

	revealingModel, revealCmd := masked.updatePreview(previewKey("R", 'R'))
	if revealCmd == nil {
		t.Fatal("uppercase R did not start an explicit reveal")
	}
	revealMsg, ok := revealCmd().(nativePreviewMsg)
	if !ok || !strings.Contains(revealMsg.markdown, previewTestSecret) {
		t.Fatal("explicit reveal command did not return raw preview content")
	}
	revealedModel, _ := revealingModel.(sessionTUI).Update(revealMsg)
	revealed := revealedModel.(sessionTUI)
	if !revealed.previewRevealed || !strings.Contains(revealed.reader.View(), previewTestSecret) {
		t.Fatal("explicit reveal did not install raw preview content")
	}
	if chrome := revealed.renderPreview(); !strings.Contains(chrome, "RAW REVEALED") {
		t.Fatalf("revealed preview state is not visible:\n%s", chrome)
	}

	closedModel, _ := revealed.updatePreview(previewKey("esc", tea.KeyEsc))
	closed := closedModel.(sessionTUI)
	if closed.previewing || closed.previewRevealed || closed.readerSession != nil {
		t.Fatalf("closing preview retained state: previewing=%v revealed=%v session=%v",
			closed.previewing, closed.previewRevealed, closed.readerSession != nil)
	}
	if strings.Contains(closed.reader.View(), previewTestSecret) {
		t.Fatal("closing preview retained raw content in the reader")
	}
	staleModel, _ := closed.Update(nativePreviewMsg{
		seq:      revealMsg.seq,
		markdown: previewTestSecret,
		revealed: true,
	})
	stale := staleModel.(sessionTUI)
	if stale.previewing || stale.previewRevealed || strings.Contains(stale.reader.View(), previewTestSecret) {
		t.Fatal("a stale asynchronous reveal restored raw content after close")
	}
}

func TestNativePreviewNeverFallsBackToIndexedBody(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	session := &sessionindex.Session{
		ProjectID:   "project",
		ProjectName: "project",
		Agent:       "codex",
		SessionID:   "missing-native",
		NativePath:  filepath.Join(t.TempDir(), "missing.jsonl"),
		Body:        "fts-fallback-must-not-appear",
	}
	if err := store.Upsert(*session); err != nil {
		t.Fatal(err)
	}

	model := newNativePreviewModel(session)
	model.store = store
	openedModel, cmd := model.openPreview(session)
	if cmd == nil {
		t.Fatal("missing native source did not start the fail-closed preview command")
	}
	blockedModel, _ := openedModel.(sessionTUI).Update(cmd())
	blocked := blockedModel.(sessionTUI)
	view := blocked.reader.View()
	if !strings.Contains(view, "Preview blocked") {
		t.Fatalf("missing native source did not show a blocked preview:\n%s", view)
	}
	if strings.Contains(view, "fts-fallback-must-not-appear") || strings.Contains(view, session.NativePath) {
		t.Fatalf("blocked preview exposed an indexed fallback or native path:\n%s", view)
	}
}

func TestLoadNativePreviewMasksCompleteConversation(t *testing.T) {
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(root, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(root, "data"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })

	session := &sessionindex.Session{
		Agent:      "codex",
		SessionID:  "11111111-1111-1111-1111-111111111111",
		NativePath: writeNativePreviewFixture(t),
		OriginCwd:  "/synthetic/project",
	}
	markdown, err := loadNativePreview(factory.GetRegistry(), session, false)
	if err != nil {
		t.Fatalf("loadNativePreview(masked) error = %v", err)
	}
	for _, marker := range []string{
		"user asks with key",
		"synthetic reasoning marker",
		"synthetic assistant answer",
		"tool-input-marker",
		"tool-output-marker",
		"answer after malformed line",
		"[REDACTED:gcp-api-key]",
	} {
		if !strings.Contains(markdown, marker) {
			t.Errorf("masked preview missing %q", marker)
		}
	}
	if strings.Contains(markdown, previewTestSecret) {
		t.Fatal("masked preview contains the synthetic secret")
	}

	revealed, err := loadNativePreview(factory.GetRegistry(), session, true)
	if err != nil {
		t.Fatalf("loadNativePreview(revealed) error = %v", err)
	}
	if !strings.Contains(revealed, previewTestSecret) {
		t.Fatal("revealed preview does not contain the synthetic secret")
	}
	if strings.Contains(logs.String(), previewTestSecret) {
		t.Fatalf("synthetic secret leaked into preview logs:\n%s", logs.String())
	}

	if _, err := os.Stat(filepath.Join(root, "data", "csessions", "sessions.db")); !os.IsNotExist(err) {
		t.Fatal("preview created or touched the derived database")
	}
	for _, rootDir := range []string{filepath.Join(root, "config"), filepath.Join(root, "data"), filepath.Join(root, "cache")} {
		err := filepath.WalkDir(rootDir, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil || entry.IsDir() {
				return walkErr
			}
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(content), previewTestSecret) {
				t.Errorf("synthetic secret persisted in %q", path)
			}
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			t.Fatalf("inspect persistent directory %q: %v", rootDir, err)
		}
	}
}

func TestLoadNativePreviewFailsClosedWhenRedactionFails(t *testing.T) {
	oldRedactor := redactNativePreview
	redactNativePreview = func(string) (string, int, error) {
		return "", 0, errors.New("synthetic detector failure")
	}
	t.Cleanup(func() { redactNativePreview = oldRedactor })

	session := &sessionindex.Session{
		Agent:      "codex",
		SessionID:  "11111111-1111-1111-1111-111111111111",
		NativePath: writeNativePreviewFixture(t),
		OriginCwd:  "/synthetic/project",
	}
	markdown, err := loadNativePreview(factory.GetRegistry(), session, false)
	if err == nil {
		t.Fatal("loadNativePreview() succeeded with a failed redactor")
	}
	if markdown != "" || strings.Contains(markdown, previewTestSecret) {
		t.Fatalf("fail-closed preview returned content: %q", markdown)
	}
}

func TestLoadNativePreviewHandlesLargeSession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rollout-large-session.jsonl")
	largeMessage := strings.Repeat("large-session-content ", 60_000) +
		"tail-marker with key " + previewTestSecret
	content := `{"timestamp":"2026-08-15T01:00:00Z","type":"session_meta","payload":{"id":"22222222-2222-2222-2222-222222222222","timestamp":"2026-08-15T01:00:00Z","cwd":"/synthetic/project","source":"cli"}}` + "\n" +
		`{"timestamp":"2026-08-15T01:00:01Z","type":"event_msg","payload":{"type":"user_message","message":"large session test"}}` + "\n" +
		`{"timestamp":"2026-08-15T01:00:02Z","type":"event_msg","payload":{"type":"agent_message","message":` +
		mustJSON(t, largeMessage) + `}}` + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	markdown, err := loadNativePreview(factory.GetRegistry(), &sessionindex.Session{
		Agent:      "codex",
		SessionID:  "22222222-2222-2222-2222-222222222222",
		NativePath: path,
		OriginCwd:  "/synthetic/project",
	}, false)
	if err != nil {
		t.Fatalf("loadNativePreview(large) error = %v", err)
	}
	if !strings.Contains(markdown, "tail-marker") || !strings.Contains(markdown, "[REDACTED:gcp-api-key]") {
		t.Fatal("large preview was truncated or its tail was not redacted")
	}
	if strings.Contains(markdown, previewTestSecret) {
		t.Fatal("large preview contains the synthetic secret")
	}
}

func mustJSON(t *testing.T, value string) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
