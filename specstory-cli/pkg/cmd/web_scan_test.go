package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

func webFixture(t *testing.T, path, id, cwd, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	b := fmt.Sprintf("{\"type\":\"session_meta\",\"timestamp\":\"2026-08-15T01:00:00Z\",\"payload\":{\"id\":%q,\"cwd\":%q,\"source\":\"cli\"}}\n{\"type\":\"event_msg\",\"timestamp\":\"2026-08-15T01:00:02Z\",\"payload\":{\"type\":\"user_message\",\"message\":%q}}\n", id, cwd, text)
	if err := os.WriteFile(path, []byte(b), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestWebScanHomeDiscoveryAndIncremental(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	normal := filepath.Join(home, ".codex", "sessions", "2026", "a.jsonl")
	extra := filepath.Join(home, "work", "custom", "b.jsonl")
	webFixture(t, normal, "one", filepath.Join(home, "Projects", "app"), "修复中文搜索")
	webFixture(t, extra, "two", filepath.Join(home, "Projects", "app", "child"), "extra session")
	webFixture(t, filepath.Join(home, "node_modules", "ignored.jsonl"), "ignored", home, "dependency fixture")
	if err := os.Symlink(filepath.Dir(extra), filepath.Join(home, "linked")); err != nil {
		t.Fatal(err)
	}
	scan := newWebScanner(store)
	if err := scan.run(context.Background(), []string{home}, true); err != nil {
		t.Fatal(err)
	}
	sessions, _, err := store.BrowserSessions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 2 {
		t.Fatalf("discovered %d sessions; want 2", len(sessions))
	}
	if scan.snapshot().Indexed != 2 {
		t.Fatalf("status: %+v", scan.snapshot())
	}
	if err := scan.run(context.Background(), []string{home}, false); err != nil {
		t.Fatal(err)
	}
	if scan.snapshot().Indexed != 0 {
		t.Fatalf("unchanged files reparsed: %+v", scan.snapshot())
	}
	webFixture(t, extra, "two", filepath.Join(home, "Projects", "app", "child"), "updated extra session")
	if err := scan.run(context.Background(), []string{home}, false); err != nil {
		t.Fatal(err)
	}
	if scan.snapshot().Indexed != 1 {
		t.Fatalf("changed file missed: %+v", scan.snapshot())
	}
	matches, err := store.BrowserMatches(context.Background(), ftsQuery("中文"))
	if err != nil || !matches["one"] {
		t.Fatalf("CJK index missing: %v %v", matches, err)
	}
}

func TestWebScanCancellationKeepsIndexedSessions(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	webFixture(t, filepath.Join(home, ".codex", "sessions", "one.jsonl"), "one", home, "retained history")
	scan := newWebScanner(store)
	if err := scan.run(context.Background(), []string{home}, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := scan.run(ctx, []string{home}, true); err == nil {
		t.Fatal("cancelled scan succeeded")
	}
	sessions, _, err := store.BrowserSessions(context.Background())
	if err != nil || len(sessions) != 1 {
		t.Fatalf("cancel lost sessions: %v %v", sessions, err)
	}
	if scan.snapshot().Running || scan.snapshot().Error == "" {
		t.Fatalf("cancellation status: %+v", scan.snapshot())
	}
}

func TestWebScanFollowsRenamedNativeFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, ".codex"))
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	original := filepath.Join(home, ".codex", "sessions", "original.jsonl")
	moved := filepath.Join(home, ".codex", "sessions", "moved.jsonl")
	webFixture(t, original, "one", home, "history retained after moving")
	scan := newWebScanner(store)
	if err := scan.run(context.Background(), []string{home}, true); err != nil {
		t.Fatal(err)
	}
	// Rename preserves content, size, and mtime; the source path still needs updating.
	if err := os.Rename(original, moved); err != nil {
		t.Fatal(err)
	}
	if err := scan.run(context.Background(), []string{home}, false); err != nil {
		t.Fatal(err)
	}
	session, ok, err := store.GetSession("codex", "one")
	if err != nil || !ok {
		t.Fatalf("session missing: %v", err)
	}
	if session.NativePath != moved {
		t.Fatalf("index still points at old file: %q", session.NativePath)
	}
	if _, err := os.Stat(session.NativePath); err != nil {
		t.Fatalf("preview source unavailable: %v", err)
	}
}
