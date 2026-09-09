package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
)

func testWebApp(t *testing.T) *webApp {
	t.Helper()
	home := t.TempDir()
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	lib, err := openWebLibrary(filepath.Join(t.TempDir(), "library.json"), home)
	if err != nil {
		t.Fatal(err)
	}
	app, err := newWebApp(store, lib, home)
	if err != nil {
		t.Fatal(err)
	}
	return app
}
func webRequest(app *webApp, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://localhost:5431"+path, strings.NewReader(body))
	r.Header.Set("Authorization", "Bearer "+app.token)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func TestWebAPIAccessBoundary(t *testing.T) {
	app := testWebApp(t)
	for _, tc := range []struct {
		name, host, origin, token string
		want                      int
	}{
		{"missing token", "localhost:5431", "", "", 401},
		{"wrong token", "localhost:5431", "", "no", 401},
		{"host rebinding", "evil.example:5431", "", app.token, 403},
		{"foreign origin", "localhost:5431", "https://evil.example", app.token, 403},
		{"authorized", "localhost:5431", "http://localhost:5431", app.token, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("GET", "http://"+tc.host+"/api/state", nil)
			r.Header.Set("Authorization", "Bearer "+tc.token)
			r.Header.Set("Origin", tc.origin)
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("code=%d, body=%s", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), app.token) {
				t.Fatal("token leaked in response")
			}
		})
	}
}
func TestWebProjectSubtreeAndPersonalEdits(t *testing.T) {
	app := testWebApp(t)
	base := filepath.Join(app.home, "Projects")
	sessions := []sessionindex.Session{
		{Agent: "codex", SessionID: "one", OriginCwd: filepath.Join(base, "app"), Name: "first", Kind: "interactive", Body: "修复缓存中文搜索", UpdatedAt: "2026-08-15"},
		{Agent: "codex", SessionID: "two", OriginCwd: filepath.Join(base, "app", "child"), Name: "second", Kind: "interactive", UpdatedAt: "2026-08-16"},
		{Agent: "codex", SessionID: "three", OriginCwd: base + "-old", Name: "other", Kind: "interactive"},
	}
	if err := app.store.UpsertBatch(sessions); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "GET", "/api/sessions?directory="+base, " ")
	var result webSessionPage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 2 {
		t.Fatalf("subtree: %s", w.Body.String())
	}
	w = webRequest(app, "PATCH", "/api/annotation?id=one", `{"title":"发布计划","favorite":true,"hidden":true}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = webRequest(app, "GET", "/api/sessions?view=hidden&q=发布计划", "")
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 || result.Items[0].Title != "发布计划" {
		t.Fatalf("manual title search: %s", w.Body.String())
	}
	// Native/index body is intact and remains searchable; hidden is a personal annotation.
	matches, err := app.store.BrowserMatches(context.Background(), ftsQuery("中文"))
	if err != nil || !matches["one"] {
		t.Fatalf("body changed: %v %v", matches, err)
	}
	w = webRequest(app, "PATCH", "/api/annotation?id=one", `{"title":"发布计划","favorite":true,"hidden":false}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = webRequest(app, "GET", "/api/sessions?view=favorites", "")
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Total != 1 {
		t.Fatalf("favorite restore: %s", w.Body.String())
	}
}
func TestWebPreviewRedactsAndEscapesAndQuotesResume(t *testing.T) {
	app := testWebApp(t)
	cwd := filepath.Join(app.home, "project ' with $(touch marker)")
	if err := os.MkdirAll(cwd, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "session.jsonl")
	webFixture(t, path, "one", cwd, "<script>alert('xss')</script> secret "+previewTestSecret)
	if err := app.store.UpsertBatch([]sessionindex.Session{{Agent: "codex", SessionID: "one", NativePath: path, OriginCwd: cwd, Kind: "interactive"}}); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "GET", "/api/session?id=one", "")
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var detail webSessionDetail
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(detail.HTML, "<script>") || strings.Contains(detail.HTML, previewTestSecret) {
		t.Fatal("unsafe preview")
	}
	if !strings.Contains(detail.Resume, "'\"'\"'") || !strings.Contains(detail.Resume, "codex resume 'one'") {
		t.Fatalf("unsafe command: %s", detail.Resume)
	}
	// Execute the copied command against a harmless fake Codex binary to verify shell
	// quoting preserves the literal cwd and argument boundaries (including command syntax).
	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "codex"), []byte("#!/bin/sh\nprintf '%s\\n' \"$PWD\" \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("bash", "-c", detail.Resume)
	command.Env = append(os.Environ(), "PATH="+fakeBin+":"+os.Getenv("PATH"))
	output, err := command.CombinedOutput()
	if err != nil || string(output) != cwd+"\nresume\none\n" {
		t.Fatalf("restore command changed cwd or args: %q %v", output, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	w = webRequest(app, "GET", "/api/session?id=one", "")
	if w.Code != 409 {
		t.Fatalf("missing source not reported: %d", w.Code)
	}
}

func TestWebLegacyHiddenRecovery(t *testing.T) {
	app := testWebApp(t)
	s := sessionindex.Session{Agent: "codex", SessionID: "one", OriginCwd: app.home, Kind: "interactive", Size: 20, Mtime: 30, IndexVersion: reindexVersion, Name: "original"}
	if err := app.store.UpsertBatch([]sessionindex.Session{s}); err != nil {
		t.Fatal(err)
	}
	if _, err := app.store.SoftDeleteSession("codex", "one"); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "GET", "/api/sessions?view=hidden", "")
	var page webSessionPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatalf("legacy tombstone: %s %v", w.Body.String(), err)
	}
	w = webRequest(app, "PATCH", "/api/annotation?id=one", `{"title":"restored","favorite":false,"hidden":false}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = webRequest(app, "GET", "/api/sessions", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatalf("restore: %s %v", w.Body.String(), err)
	}
}

func TestWebSearchSubtreeBeyondGlobalLimit(t *testing.T) {
	app := testWebApp(t)
	sessions := make([]sessionindex.Session, 0, 510)
	for i := 0; i < 510; i++ {
		dir := "/other"
		if i == 509 {
			dir = "/target/project"
		}
		sessions = append(sessions, sessionindex.Session{Agent: "codex", SessionID: fmt.Sprintf("s%03d", i), OriginCwd: dir, Kind: "interactive", Body: "needle 中文搜索", UpdatedAt: fmt.Sprintf("%04d", 1000-i)})
	}
	if err := app.store.UpsertBatch(sessions); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "GET", "/api/sessions?directory=/target&q=needle", "")
	var page webSessionPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatalf("search truncated before scope: %s %v", w.Body.String(), err)
	}
	w = webRequest(app, "GET", "/api/sessions?offset=480", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 510 || len(page.Items) != 30 {
		t.Fatalf("pagination: %s %v", w.Body.String(), err)
	}
}

func TestWebPartialEditsPreserveOtherFields(t *testing.T) {
	app := testWebApp(t)
	if err := app.store.UpsertBatch([]sessionindex.Session{{Agent: "codex", SessionID: "one", Kind: "interactive"}}); err != nil {
		t.Fatal(err)
	}
	if err := app.library.setAnnotation("one", webAnnotation{Title: "keep title", Hidden: true}); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "PATCH", "/api/annotation?id=one", `{"favorite":true}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	got := app.library.snapshot().Annotations["one"]
	if !got.Favorite || !got.Hidden || got.Title != "keep title" {
		t.Fatalf("partial edit overwrote independent fields: %+v", got)
	}
}

func TestWebAIMetadataPreservedUnderManualTitle(t *testing.T) {
	app := testWebApp(t)
	s := sessionindex.Session{Agent: "codex", SessionID: "one", OriginCwd: app.home, Kind: "interactive", Size: 20, Mtime: 30, IndexVersion: reindexVersion, Name: "original"}
	if err := app.store.UpsertBatch([]sessionindex.Session{s}); err != nil {
		t.Fatal(err)
	}
	ai := sessionindex.AIMetadata{Agent: "codex", SessionID: "one", SourceSize: s.Size, SourceMtime: s.Mtime, SourceIndexVersion: s.IndexVersion, PromptVersion: sessionindex.CurrentAIPromptVersion, Model: "synthetic", Title: "AI 标题", Summary: "自动生成的摘要", Tags: []string{"架构"}}
	if err := app.store.UpsertAIMetadata(ai); err != nil {
		t.Fatal(err)
	}
	if err := app.library.setAnnotation("one", webAnnotation{Title: "手动标题"}); err != nil {
		t.Fatal(err)
	}
	w := webRequest(app, "GET", "/api/sessions?q=架构", "")
	var page webSessionPage
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Total != 1 {
		t.Fatalf("AI tag search failed: %s %v", w.Body.String(), err)
	}
	if page.Items[0].Title != "手动标题" || page.Items[0].Summary != ai.Summary || page.Items[0].AITitle != ai.Title {
		t.Fatalf("AI data overwritten: %+v", page.Items[0])
	}
	w = webRequest(app, "PATCH", "/api/annotation?id=one", `{"title":""}`)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = webRequest(app, "GET", "/api/sessions", "")
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || page.Items[0].Title != ai.Title {
		t.Fatalf("clear manual title: %s %v", w.Body.String(), err)
	}
	stored, ok, err := app.store.GetAIMetadata("codex", "one")
	if err != nil || !ok || stored.Title != ai.Title {
		t.Fatalf("AI metadata changed: %+v %v", stored, err)
	}
}
