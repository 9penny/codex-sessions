package cmd

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

type webSession struct {
	ID          string   `json:"id"`
	Directory   string   `json:"directory"`
	Title       string   `json:"title"`
	ManualTitle string   `json:"manualTitle"`
	AITitle     string   `json:"aiTitle"`
	Summary     string   `json:"summary"`
	Tags        []string `json:"tags"`
	Updated     string   `json:"updated"`
	Turns       int      `json:"turns"`
	Favorite    bool     `json:"favorite"`
	Hidden      bool     `json:"hidden"`
	Background  bool     `json:"background"`
	Snippet     string   `json:"snippet,omitempty"`
}
type webProject struct {
	Path    string `json:"path"`
	Count   int    `json:"count"`
	Updated string `json:"updated"`
	Missing bool   `json:"missing"`
}
type webSessionPage struct {
	Items  []webSession `json:"items"`
	Total  int          `json:"total"`
	Offset int          `json:"offset"`
}
type webSessionDetail struct {
	Session          webSession `json:"session"`
	HTML             string     `json:"html"`
	Resume           string     `json:"resume"`
	EnrichPreview    string     `json:"enrichPreview"`
	Enrich           string     `json:"enrich"`
	MissingDirectory bool       `json:"missingDirectory"`
}

func webSessionView(s sessionindex.Session, lib webLibraryData, deleted bool) webSession {
	a, annotated := lib.Annotations[s.SessionID]
	title := a.Title
	if title == "" {
		title = s.AITitle
	}
	if title == "" {
		title = s.Name
	}
	if title == "" {
		title = s.Slug
	}
	if title == "" {
		title = "未命名会话"
	}
	return webSession{ID: s.SessionID, Directory: s.OriginCwd, Title: title, ManualTitle: a.Title, AITitle: s.AITitle, Summary: s.AISummary, Tags: s.AITags, Updated: s.UpdatedAt, Turns: s.UserTurns, Favorite: a.Favorite, Hidden: a.Hidden || (deleted && !annotated), Background: s.Kind != "" && s.Kind != "interactive"}
}
func (a *webApp) state(w http.ResponseWriter, r *http.Request) {
	sessions, deleted, err := a.store.BrowserSessions(r.Context())
	if err != nil {
		webError(w, 500, "读取会话索引失败")
		return
	}
	lib := a.library.snapshot()
	projects := map[string]webProject{}
	favorites, hidden, total := 0, 0, 0
	for _, s := range sessions {
		v := webSessionView(s, lib, deleted[s.SessionID])
		if v.Hidden {
			hidden++
			continue
		}
		if v.Background {
			continue
		}
		total++
		if v.Favorite {
			favorites++
		}
		if !filepath.IsAbs(s.OriginCwd) {
			continue
		}
		// Every ancestor is selectable and aggregates exactly its descendant sessions.
		for path := filepath.Clean(s.OriginCwd); ; path = filepath.Dir(path) {
			p := projects[path]
			p.Path = path
			p.Count++
			if s.UpdatedAt > p.Updated {
				p.Updated = s.UpdatedAt
			}
			projects[path] = p
			if path == filepath.Dir(path) {
				break
			}
		}
	}
	list := make([]webProject, 0, len(projects))
	for _, p := range projects {
		info, err := os.Stat(p.Path)
		p.Missing = err != nil || !info.IsDir()
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Path < list[j].Path })
	hostname, _ := os.Hostname()
	webJSON(w, 200, map[string]any{"home": a.home, "machine": hostname, "roots": lib.Roots, "projects": list, "total": total, "favorites": favorites, "hidden": hidden, "scan": a.scanner.snapshot()})
}
func (a *webApp) sessions(w http.ResponseWriter, r *http.Request) {
	all, deleted, err := a.store.BrowserSessions(r.Context())
	if err != nil {
		webError(w, 500, "读取会话索引失败")
		return
	}
	lib := a.library.snapshot()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	directory := r.URL.Query().Get("directory")
	if directory != "" && !filepath.IsAbs(directory) {
		webError(w, 400, "目录必须是绝对路径")
		return
	}
	expression := ftsQuery(q)
	matches := map[string]bool{}
	if expression != "" {
		matches, err = a.store.BrowserMatches(r.Context(), expression)
		if err != nil {
			webError(w, 500, "搜索失败，请重试")
			return
		}
	}
	view := r.URL.Query().Get("view")
	background := r.URL.Query().Get("background") == "true"
	items := make([]webSession, 0)
	matchedSources := make([]sessionindex.Session, 0)
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}
	for _, s := range all {
		v := webSessionView(s, lib, deleted[s.SessionID])
		if (view == "hidden") != v.Hidden || (v.Background && !background && view != "hidden") || (view == "favorites" && !v.Favorite) {
			continue
		}
		if directory != "" && !webWithin(s.OriginCwd, directory) {
			continue
		}
		if q != "" && !matches[s.SessionID] && !strings.Contains(strings.ToLower(v.Title+" "+v.Summary+" "+strings.Join(v.Tags, " ")), strings.ToLower(q)) {
			continue
		}
		items = append(items, v)
		matchedSources = append(matchedSources, s)
	}
	total := len(items)
	offset = min(offset, total)
	end := min(offset+80, total)
	items = items[offset:end]
	matchedSources = matchedSources[offset:end]
	if expression != "" {
		snippets, err := a.store.SnippetsContext(r.Context(), expression, matchedSources)
		if err == nil {
			for i := range items {
				items[i].Snippet = snippets[sessionindex.FingerprintKey("codex", items[i].ID)]
			}
		}
	}
	webJSON(w, 200, webSessionPage{Items: items, Total: total, Offset: offset})
}
func (a *webApp) findSession(w http.ResponseWriter, r *http.Request) (sessionindex.Session, bool, bool) {
	all, deleted, err := a.store.BrowserSessions(r.Context())
	if err != nil {
		webError(w, 500, "读取会话失败")
		return sessionindex.Session{}, false, false
	}
	for _, s := range all {
		if s.SessionID == r.URL.Query().Get("id") {
			return s, deleted[s.SessionID], true
		}
	}
	webError(w, 404, "会话不存在，请刷新列表")
	return sessionindex.Session{}, false, false
}
func shellQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'" }
func (a *webApp) detail(w http.ResponseWriter, r *http.Request) {
	s, deleted, ok := a.findSession(w, r)
	if !ok {
		return
	}
	markdown, err := loadNativePreview(factory.GetRegistry(), &s, false)
	if err != nil {
		webError(w, 409, "无法读取原始会话或完成脱敏；文件可能已移动或删除")
		return
	}
	var html bytes.Buffer
	// Goldmark is already part of the TUI's dependency graph. Its default renderer omits
	// raw HTML and unsafe URLs; CSP additionally blocks transcript images and inline scripts.
	renderer := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err = renderer.Convert([]byte(markdown), &html); err != nil {
		webError(w, 500, "对话渲染失败")
		return
	}
	info, err := os.Stat(s.OriginCwd)
	missing := err != nil || !info.IsDir() || !filepath.IsAbs(s.OriginCwd)
	resume := ""
	enrichCommand := ""
	enrichPreview := ""
	if !missing {
		codex := "codex"
		sourceRoot := webSourceRoot(s.NativePath)
		if filepath.Base(sourceRoot) == "sessions" {
			currentHome := os.Getenv("CODEX_HOME")
			if currentHome == "" {
				currentHome = filepath.Join(a.home, ".codex")
			}
			nativeHome := filepath.Dir(sourceRoot)
			if filepath.Clean(nativeHome) != filepath.Clean(currentHome) {
				codex = "env CODEX_HOME=" + shellQuote(nativeHome) + " codex"
			}
		}
		resume = "cd -- " + shellQuote(s.OriginCwd) + " && " + codex + " resume " + shellQuote(s.SessionID)
		enrichCommand = "csessions enrich --project " + shellQuote(s.OriginCwd) + " --limit 10 --yes"
		enrichPreview = "csessions enrich --project " + shellQuote(s.OriginCwd) + " --limit 10 --dry-run"
	}
	webJSON(w, 200, webSessionDetail{Session: webSessionView(s, a.library.snapshot(), deleted), HTML: html.String(), Resume: resume, Enrich: enrichCommand, EnrichPreview: enrichPreview, MissingDirectory: missing})
}
func (a *webApp) annotate(w http.ResponseWriter, r *http.Request) {
	s, deleted, ok := a.findSession(w, r)
	if !ok {
		return
	}
	var patch webAnnotationPatch
	if !webDecode(w, r, &patch) {
		return
	}
	if patch.Title != nil {
		title := strings.TrimSpace(*patch.Title)
		patch.Title = &title
		if utf8.RuneCountInString(title) > 200 {
			webError(w, 400, "标题最多 200 个字符")
			return
		}
	}
	annotation, err := a.library.patchAnnotation(s.SessionID, patch, deleted)
	if err != nil {
		webError(w, 500, "保存失败，原有整理数据仍保留")
		return
	}
	webJSON(w, 200, annotation)
}
func (a *webApp) addRoot(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		Path string `json:"path"`
	}
	if !webDecode(w, r, &payload) {
		return
	}
	path := strings.TrimSpace(payload.Path)
	if path == "~" {
		path = a.home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(a.home, path[2:])
	}
	if !filepath.IsAbs(path) {
		webError(w, 400, "请输入绝对路径或 ~/ 开头的路径")
		return
	}
	path = filepath.Clean(path)
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() {
		webError(w, 400, "目录不存在、无法读取或为符号链接")
		return
	}
	if err = a.library.addRoot(path); err != nil {
		webError(w, 500, "扫描位置保存失败")
		return
	}
	select {
	case a.refresh <- true:
	default:
	}
	webJSON(w, 200, map[string]string{"path": path, "message": fmt.Sprintf("已添加扫描位置 %s", path)})
}
