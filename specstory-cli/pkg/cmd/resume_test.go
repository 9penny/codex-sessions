package cmd

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/sessionindex"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/factory"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi/schema"
)

func TestFTSQuery(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"empty", "", ""},
		{"single word is a prefix", "thank", "thank*"},
		{"bare words are independent prefixes", "thank you", "thank* you*"},
		{"punctuation in a bare word splits into an adjacency phrase", "max-cpu!", "max + cpu*"},
		{"a bare filename is an adjacency phrase with prefix last", "poem.txt", "poem + txt*"},
		{"a quoted filename is a committed phrase, no prefix", `"poem.txt"`, `"poem txt"`},
		{"an open-quoted filename keeps the last token a prefix", `"poem.txt`, "poem + txt*"},
		{"closed phrase is exact adjacency", `"thank you"`, `"thank you"`},
		{"closed single-word phrase has no prefix", `"thank"`, `"thank"`},
		{"open phrase keeps last word a prefix", `"thank yo`, "thank + yo*"},
		{"open single-word phrase is a prefix", `"thank`, "thank*"},
		{"phrase plus trailing bare word", `"thank you" now`, `"thank you" now*`},
		{"bare word before a closed phrase", `please "thank you"`, `please* "thank you"`},
		{"whitespace inside a phrase is collapsed", `"thank    you"`, `"thank you"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ftsQuery(c.in); got != c.want {
				t.Errorf("ftsQuery(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestQueryReady(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"a", false},   // one alnum char is below minQueryLen
		{`"a"`, false}, // quotes don't count toward the threshold
		{"ab", true},   // two alnum chars
		{"a!", false},  // punctuation doesn't count
		{"a b", true},  // two alnum chars across words
		{`"hi"`, true}, // alnum inside quotes counts
		{"日本", true},   // letters in other scripts count
	}
	for _, c := range cases {
		if got := queryReady(c.in); got != c.want {
			t.Errorf("queryReady(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestSearchFindsCJKSubstringsWithoutChangingEnglishBehavior(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })

	sessions := []sessionindex.Session{
		{
			ProjectID: "project", Agent: "codex", SessionID: "cjk",
			CreatedAt: "2026-08-15T01:00:00Z", UpdatedAt: "2026-08-15T01:00:00Z",
			Kind: spi.SessionKindInteractive,
			Name: "双语搜索", Body: "开始甲乙中文连续搜索功能结束 oauth callback",
		},
		{
			ProjectID: "project", Agent: "codex", SessionID: "technical",
			CreatedAt: "2026-08-15T00:00:00Z", UpdatedAt: "2026-08-15T00:00:00Z",
			Kind: spi.SessionKindInteractive,
			Name: "Technical", Body: "updated max-cpu parser",
		},
	}
	for _, sess := range sessions {
		if err := store.Upsert(sess); err != nil {
			t.Fatal(err)
		}
	}

	for _, query := range []string{"开始", "中文", "结束", "中文 oauth", "中 oauth"} {
		hits, err := store.Search(ftsQuery(query), "")
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		if len(hits) != 1 || hits[0].SessionID != "cjk" {
			t.Errorf("Search(%q) = %+v; want the CJK session", query, hits)
		}
	}
	if hits, err := store.Search(ftsQuery("max-cpu"), ""); err != nil || len(hits) != 1 || hits[0].SessionID != "technical" {
		t.Fatalf("technical-token search regressed: hits=%+v err=%v", hits, err)
	}

	hits, err := store.Search(ftsQuery("中文"), "")
	if err != nil || len(hits) != 1 {
		t.Fatalf("CJK snippet setup: hits=%+v err=%v", hits, err)
	}
	snippets, err := store.Snippets(ftsQuery("中文"), hits)
	if err != nil {
		t.Fatal(err)
	}
	snippet := snippets[sessionindex.FingerprintKey("codex", "cjk")]
	if !strings.Contains(snippet, "\x02中文\x03") {
		t.Errorf("CJK snippet does not highlight readable source text: %q", snippet)
	}
	if strings.Contains(snippet, "zh2") || strings.Contains(snippet, "search_terms") {
		t.Errorf("CJK snippet exposed search-only normalization: %q", snippet)
	}
}

func TestShortID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"1234567890abc", "1234567890abc"}, // <= 13 chars: unchanged
		{"1234567890abcdef", "12345...bcdef"},
	}
	for _, c := range cases {
		if got := shortID(c.in); got != c.want {
			t.Errorf("shortID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestWriteReconstructedSession(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "session.jsonl")
	content := []byte(`{"type":"user"}` + "\n")

	if err := writeReconstructedSession(path, content); err != nil {
		t.Fatalf("writeReconstructedSession: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading written file: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("content = %q, want %q", got, content)
	}

	// The atomic-write temp file must not be left behind in the target directory.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "session.jsonl" {
			t.Errorf("unexpected leftover file in target dir: %q", e.Name())
		}
	}
}

func TestSessionFileReadable(t *testing.T) {
	dir := t.TempDir()

	missing := filepath.Join(dir, "nope.jsonl")
	if ok, _ := sessionFileReadable(missing); ok {
		t.Error("missing file reported readable")
	}

	empty := filepath.Join(dir, "empty.jsonl")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, _ := sessionFileReadable(empty); ok {
		t.Error("empty file reported readable")
	}

	full := filepath.Join(dir, "full.jsonl")
	if err := os.WriteFile(full, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := sessionFileReadable(full); !ok {
		t.Errorf("file with content reported not readable: %v", err)
	}
}

func TestWaitForSessionFileVisible(t *testing.T) {
	dir := t.TempDir()

	present := filepath.Join(dir, "present.jsonl")
	if err := os.WriteFile(present, []byte(`{"type":"user"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := waitForSessionFileVisible(present, time.Second); err != nil {
		t.Errorf("expected an already-present file to be visible: %v", err)
	}

	// A file that never appears must time out with a diagnostic error rather than block forever.
	if err := waitForSessionFileVisible(filepath.Join(dir, "never.jsonl"), 100*time.Millisecond); err == nil {
		t.Error("expected timeout error for a file that never becomes visible")
	}
}

// TestBeginResumeWithPresetSkipsTargetStep verifies the `resume <agent>` contract: when a
// target agent was pre-selected, choosing a session resumes immediately into that agent
// rather than prompting for a target.
func TestBeginResumeWithPresetSkipsTargetStep(t *testing.T) {
	sess := &sessionindex.Session{SessionID: "s1", Agent: "codex"}
	m := sessionTUI{presetTo: "claude"}

	next, cmd := m.beginResume(sess)
	rm := next.(sessionTUI)

	if cmd == nil {
		t.Error("expected an immediate quit command when a target is preset")
	}
	if rm.mode == modeTarget {
		t.Error("a preset target must skip the target-selection step")
	}
	if rm.result.session != sess {
		t.Errorf("result session = %v, want the chosen session", rm.result.session)
	}
	if rm.result.targetID != "claude" {
		t.Errorf("result target = %q, want %q", rm.result.targetID, "claude")
	}
}

// TestBeginResumeWithoutPresetEntersTargetStep verifies the default flow: with no preset,
// choosing a session advances to the target-selection step (no immediate resume).
func TestBeginResumeWithoutPresetEntersTargetStep(t *testing.T) {
	sess := &sessionindex.Session{SessionID: "s1", Agent: "codex"}
	m := sessionTUI{}

	next, cmd := m.beginResume(sess)
	rm := next.(sessionTUI)

	if cmd != nil {
		t.Error("expected no immediate quit without a preset target")
	}
	if rm.mode != modeTarget {
		t.Errorf("mode = %v, want modeTarget", rm.mode)
	}
	if rm.chosen != sess {
		t.Error("chosen session must be recorded before target selection")
	}
}

func TestLocalTUIEnterResumesAndNewSessionUsesRecordedCwd(t *testing.T) {
	cwd := t.TempDir()
	sess := sessionindex.Session{
		Agent: "codex", SessionID: "s1", ProjectID: "project", OriginCwd: cwd,
		Kind: spi.SessionKindInteractive,
	}
	model := newSessionTUI(nil, factory.GetRegistry(), "project", "project", []sessionindex.Session{sess},
		map[string]agentMeta{"codex": {name: "Codex CLI"}}, nil,
		sessionTUIOpts{title: "Codex Sessions", localOnly: true})

	resumedModel, resumeCmd := model.updateList(previewKey("enter", tea.KeyEnter))
	resumed := resumedModel.(sessionTUI)
	if resumeCmd == nil || resumed.result.session == nil || resumed.result.targetID != "codex" {
		t.Fatalf("enter did not choose native Codex resume: result=%+v cmd=%v", resumed.result, resumeCmd != nil)
	}

	newModel, newCmd := model.updateList(previewKey("n", 'n'))
	started := newModel.(sessionTUI)
	if newCmd == nil || !started.result.newSession || started.result.newCwd != cwd {
		t.Fatalf("n did not choose a new Codex session in recorded cwd: result=%+v cmd=%v", started.result, newCmd != nil)
	}

	empty := sessionTUI{mode: modeProjects, localOnly: true, homeCwd: cwd}
	emptyModel, emptyCmd := empty.updateProjects(previewKey("n", 'n'))
	emptyResult := emptyModel.(sessionTUI).result
	if emptyCmd == nil || !emptyResult.newSession || emptyResult.newCwd != cwd {
		t.Fatalf("n could not start the first Codex session: result=%+v cmd=%v", emptyResult, emptyCmd != nil)
	}
}

func TestLocalTUIBlocksMissingRecordedCwd(t *testing.T) {
	sess := sessionindex.Session{
		Agent: "codex", SessionID: "s1", ProjectID: "project",
		OriginCwd: filepath.Join(t.TempDir(), "missing"), Kind: spi.SessionKindInteractive,
	}
	model := newSessionTUI(nil, factory.GetRegistry(), "project", "project", []sessionindex.Session{sess},
		map[string]agentMeta{"codex": {name: "Codex CLI"}}, nil,
		sessionTUIOpts{title: "Codex Sessions", localOnly: true})

	nextModel, cmd := model.updateList(previewKey("enter", tea.KeyEnter))
	next := nextModel.(sessionTUI)
	if cmd != nil || next.result.session != nil || !strings.Contains(next.statusMsg, "project directory") {
		t.Fatalf("missing cwd was not explained and blocked: result=%+v status=%q", next.result, next.statusMsg)
	}
}

func TestLocalTUIShowHiddenToggleAffectsBrowseAndSearch(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	var all []sessionindex.Session
	for _, kind := range []spi.SessionKind{spi.SessionKindInteractive, spi.SessionKindSubagent, spi.SessionKindExec, spi.SessionKindUnknown} {
		sess := sessionindex.Session{
			ProjectID: "project", Agent: "codex", SessionID: string(kind), OriginCwd: t.TempDir(),
			CreatedAt: "2026-08-15T01:00:00Z", UpdatedAt: "2026-08-15T01:00:00Z",
			Kind: kind, Name: string(kind), Body: "visibility marker " + string(kind),
		}
		if err := store.Upsert(sess); err != nil {
			t.Fatal(err)
		}
		all = append(all, sess)
	}
	visible, err := store.ListByProject("project")
	if err != nil {
		t.Fatal(err)
	}
	model := newSessionTUI(store, factory.GetRegistry(), "project", "project", visible,
		map[string]agentMeta{"codex": {name: "Codex CLI"}}, nil,
		sessionTUIOpts{title: "Codex Sessions", localOnly: true})
	model.width, model.height = 80, 24
	_ = model.View() // terminal-size smoke: browse chrome and footer render without overflow panics
	if len(model.filtered) != 1 {
		t.Fatalf("default browse has %d sessions; want 1", len(model.filtered))
	}

	shownModel, _ := model.updateList(previewKey("h", 'h'))
	shown := shownModel.(sessionTUI)
	_ = shown.View() // hidden-mode badge and kind labels render at 80x24
	if !shown.showHidden || len(shown.filtered) != len(all) || !strings.Contains(shown.renderHeader(), "HIDDEN SHOWN") {
		t.Fatalf("show-hidden state not applied: shown=%v rows=%d header=%q", shown.showHidden, len(shown.filtered), shown.renderHeader())
	}
	shown.searchQuery = "marker"
	shown.applyFilter()
	if len(shown.filtered) != len(all) {
		t.Fatalf("show-hidden search has %d rows; want %d", len(shown.filtered), len(all))
	}

	hiddenModel, _ := shown.updateList(previewKey("h", 'h'))
	hidden := hiddenModel.(sessionTUI)
	if hidden.showHidden || len(hidden.filtered) != 1 {
		t.Fatalf("hiding background sessions left shown=%v rows=%d", hidden.showHidden, len(hidden.filtered))
	}
}

func TestLaunchNewCodexSessionUsesCwdAndNoResumeID(t *testing.T) {
	cwd := t.TempDir()
	provider := &fakeProvider{name: "Codex CLI"}
	plan := &resumePlan{to: provider, toID: "codex", fromCwd: cwd, newSession: true}
	if err := launchNewCodexSession(plan, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if provider.gotExecPath != cwd || provider.gotResumeID != "" {
		t.Fatalf("new Codex launch path=%q resumeID=%q; want path=%q and empty id", provider.gotExecPath, provider.gotResumeID, cwd)
	}
}

func TestLocalOnlyFootersAdvertiseAvailableDeleteAndProjectFilterKeys(t *testing.T) {
	list := sessionTUI{localOnly: true}
	if footer := list.renderFooter(); !strings.Contains(footer, "d delete") {
		t.Errorf("local list footer omits delete key: %q", footer)
	}

	projects := sessionTUI{localOnly: true, mode: modeProjects, width: 120, height: 24}
	view := projects.renderProjects()
	for _, want := range []string{"p filter projects", "d delete"} {
		if !strings.Contains(view, want) {
			t.Errorf("local projects footer omits %q: %q", want, view)
		}
	}
}

// fakeProvider is a minimal spi.Provider for exercising prepareResumeTarget. It records
// the projectPath it is asked to load the source session from, and reconstructs into a
// caller-provided directory so the write/visibility tail succeeds.
type fakeProvider struct {
	name        string
	gotLoadPath string // projectPath captured from GetAgentChatSession
	gotExecPath string
	gotResumeID string
	nativeDir   string // where NativeSessionPath places the reconstructed file
	enumRefs    []spi.GlobalSessionRef
	enumErr     error
	enumPanic   bool
	// reconstructUnsupported makes the fake report no native serializer, the way
	// Antigravity does: both ReconstructSession and NativeSessionPath answer
	// spi.ErrReconstructionUnsupported.
	reconstructUnsupported bool
}

func (f *fakeProvider) Name() string                  { return f.name }
func (f *fakeProvider) Check(string) spi.CheckResult  { return spi.CheckResult{Success: true} }
func (f *fakeProvider) DetectAgent(string, bool) bool { return false }

func (f *fakeProvider) GetAgentChatSession(projectPath, sessionID string, _ bool) (*spi.AgentChatSession, error) {
	f.gotLoadPath = projectPath
	return &spi.AgentChatSession{SessionID: sessionID, SessionData: &schema.SessionData{}}, nil
}

func (f *fakeProvider) ReconstructSession(*schema.SessionData, spi.ReconstructOptions) (*spi.ReconstructedSession, error) {
	if f.reconstructUnsupported {
		return nil, spi.ErrReconstructionUnsupported
	}
	return &spi.ReconstructedSession{
		SessionID: "new-session-id",
		Filename:  "reconstructed.jsonl",
		Content:   []byte(`{"type":"user"}` + "\n"),
	}, nil
}

func (f *fakeProvider) NativeSessionPath(_ string, filename string) (string, error) {
	if f.reconstructUnsupported {
		return "", spi.ErrReconstructionUnsupported
	}
	return filepath.Join(f.nativeDir, filename), nil
}

func (f *fakeProvider) SupportsReconstruction() bool { return !f.reconstructUnsupported }

// Unused-by-these-tests interface methods.
func (f *fakeProvider) GetAgentChatSessions(string, bool, spi.ProgressCallback) ([]spi.AgentChatSession, error) {
	return nil, nil
}
func (f *fakeProvider) ListAgentChatSessions(string) ([]spi.SessionMetadata, error) { return nil, nil }

func (f *fakeProvider) ExecAgentAndWatch(projectPath, _ string, resumeID string, _ bool, _ func(*spi.AgentChatSession)) error {
	f.gotExecPath = projectPath
	f.gotResumeID = resumeID
	return nil
}
func (f *fakeProvider) WatchAgent(context.Context, string, bool, func(*spi.AgentChatSession)) error {
	return nil
}
func (f *fakeProvider) ListAllAgentChatSessions() ([]spi.GlobalSessionRef, error) {
	if f.enumPanic {
		panic("enumeration panic")
	}
	return f.enumRefs, f.enumErr
}

// TestActiveRegistrySupportsCodexReconstruction pins the capability used by the
// local resume path without making archived providers reachable again.
func TestActiveRegistrySupportsCodexReconstruction(t *testing.T) {
	registry := factory.GetRegistry()
	prov, err := registry.Get("codex")
	if err != nil {
		t.Fatalf("registry.Get(codex): %v", err)
	}
	if !prov.SupportsReconstruction() {
		t.Error("codex.SupportsReconstruction() = false, want true")
	}
}

// TestEligibleTargets verifies the target step narrows the installed agents per
// chosen session: an agent without a native serializer is offered only for its
// own LOCAL sessions (native in-place resume) — never for another agent's
// session, and never for a cloud session (which must reconstruct even into the
// same agent).
func TestEligibleTargets(t *testing.T) {
	m := sessionTUI{installed: []agentChoice{
		{id: "claude", provider: &fakeProvider{name: "Claude Code"}},
		{id: "antigravity", provider: &fakeProvider{name: "Antigravity CLI", reconstructUnsupported: true}},
	}}

	tests := []struct {
		name string
		sess *sessionindex.Session
		want []string
	}{
		{"other agent's session excludes the non-serializing agent", &sessionindex.Session{Agent: "claude"}, []string{"claude"}},
		{"own local session adds it back for native resume", &sessionindex.Session{Agent: "antigravity"}, []string{"claude", "antigravity"}},
		{"own cloud session still excludes it", &sessionindex.Session{Agent: "antigravity", IsCloud: true}, []string{"claude"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got []string
			for _, a := range m.eligibleTargets(tt.sess) {
				got = append(got, a.id)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("eligibleTargets() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestPrepareResumeTargetLoadsSourceFromOriginCwd guards the cross-project resume fix:
// the source session must be loaded from the directory it was launched in (fromCwd),
// not the user's current cwd, while the reconstructed file is still written under the
// current cwd. Regression test for "source session ... has no data to reconstruct" when
// resuming a session picked from another project via the all-projects browser.
func TestPrepareResumeTargetLoadsSourceFromOriginCwd(t *testing.T) {
	nativeDir := t.TempDir()
	from := &fakeProvider{name: "Codex CLI"}
	to := &fakeProvider{name: "Claude Code", nativeDir: nativeDir}

	const originCwd = "/Users/jake/dev/tmp/blog-site"
	const currentCwd = "/Users/jake/dev/specstory-website"

	plan := &resumePlan{
		from:      from,
		fromID:    "codex",
		sessionID: "019c4cdd-917a-74e3-9b2e-fdb45e9eddc5",
		fromCwd:   originCwd,
		to:        to,
		toID:      "claude",
	}

	newID, err := prepareResumeTarget(plan, currentCwd, io.Discard)
	if err != nil {
		t.Fatalf("prepareResumeTarget returned error: %v", err)
	}
	if newID != "new-session-id" {
		t.Errorf("resume target id = %q, want %q", newID, "new-session-id")
	}
	if from.gotLoadPath != originCwd {
		t.Errorf("source loaded from %q, want origin cwd %q (current cwd %q must not be used)",
			from.gotLoadPath, originCwd, currentCwd)
	}
}

func TestResumeLaunchCwdUsesLocalSessionOrigin(t *testing.T) {
	const currentCwd = "/work/launcher"
	const originCwd = "/work/original-project"

	tests := []struct {
		name string
		plan *resumePlan
		want string
	}{
		{
			name: "local indexed session",
			plan: &resumePlan{fromCwd: originCwd},
			want: originCwd,
		},
		{
			name: "legacy local row without origin",
			plan: &resumePlan{},
			want: currentCwd,
		},
		{
			name: "cloud path is not valid locally",
			plan: &resumePlan{fromCwd: originCwd, fromCloud: true},
			want: currentCwd,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resumeLaunchCwd(tt.plan, currentCwd); got != tt.want {
				t.Errorf("resumeLaunchCwd() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLocalOnlyResumeLaunchesWithoutAutosaveTail(t *testing.T) {
	provider := &fakeProvider{name: "Codex CLI"}
	plan := &resumePlan{
		from:      provider,
		fromID:    "codex",
		fromCwd:   "/work/original-project",
		to:        provider,
		toID:      "codex",
		sessionID: "synthetic-session",
	}

	if err := launchResume(plan, "/work/launcher", resumeLaunchOpts{localOnly: true}); err != nil {
		t.Fatalf("launchResume() error = %v", err)
	}
	if provider.gotExecPath != plan.fromCwd {
		t.Errorf("resume cwd = %q, want %q", provider.gotExecPath, plan.fromCwd)
	}
	if provider.gotResumeID != plan.sessionID {
		t.Errorf("resume ID = %q, want %q", provider.gotResumeID, plan.sessionID)
	}
}

func TestLocalOnlyTUIDoesNotStartCloudEligibility(t *testing.T) {
	m := sessionTUI{localOnly: true}
	if cmd := m.Init(); cmd != nil {
		t.Fatal("local-only TUI Init returned a command; want no cloud eligibility work")
	}
}

// TestPrepareResumeTargetFallsBackToCurrentCwd verifies that when the index row carries
// no origin cwd (older rows), the source load falls back to the current cwd rather than
// loading from an empty path.
func TestPrepareResumeTargetFallsBackToCurrentCwd(t *testing.T) {
	nativeDir := t.TempDir()
	from := &fakeProvider{name: "Codex CLI"}
	to := &fakeProvider{name: "Claude Code", nativeDir: nativeDir}

	const currentCwd = "/Users/jake/dev/blog-site"

	plan := &resumePlan{
		from:      from,
		fromID:    "codex",
		sessionID: "sid",
		fromCwd:   "", // older index row: no origin cwd recorded
		to:        to,
		toID:      "claude",
	}

	if _, err := prepareResumeTarget(plan, currentCwd, io.Discard); err != nil {
		t.Fatalf("prepareResumeTarget returned error: %v", err)
	}
	if from.gotLoadPath != currentCwd {
		t.Errorf("source loaded from %q, want fallback to current cwd %q", from.gotLoadPath, currentCwd)
	}
}

// TestLiveIndexerRecord exercises real-time indexing (watch/run/resume): a live session is
// upserted into the index, its FTS body is searchable, repeat records with no new activity are
// skipped, and new activity updates the same row in place.
func TestLiveIndexerRecord(t *testing.T) {
	store, err := sessionindex.Open(filepath.Join(t.TempDir(), "sessions.db"))
	if err != nil {
		t.Fatalf("open index: %v", err)
	}
	defer func() { _ = store.Close() }()

	li := &LiveIndexer{
		store:       store,
		cwd:         "/work/proj",
		projectID:   "proj-1",
		projectName: "proj",
		indexedAt:   "2026-06-25T00:00:00Z",
		lastSeen:    map[string]string{},
	}

	msg := func(role, ts, text string) schema.Message {
		return schema.Message{
			Role:      role,
			Timestamp: ts,
			Content:   []schema.ContentPart{{Type: schema.ContentTypeText, Text: text}},
		}
	}
	sess := func(msgs ...schema.Message) *spi.AgentChatSession {
		return &spi.AgentChatSession{
			SessionID: "sess-1",
			CreatedAt: "2026-06-25T00:00:00Z",
			Slug:      "hello-world",
			SessionData: &schema.SessionData{
				Provider:  schema.ProviderInfo{Name: "Claude Code"},
				Exchanges: []schema.Exchange{{Messages: msgs}},
			},
		}
	}
	user1 := msg(schema.RoleUser, "2026-06-25T00:01:00Z", "index this please")
	agent1 := msg(schema.RoleAgent, "2026-06-25T00:02:00Z", "done indexing")

	// First record: one user + one agent message → one fully-derived row.
	li.Record("claude", sess(user1, agent1))
	rows, err := store.ListByProject("proj-1")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("want 1 row, got %d", len(rows))
	}
	r := rows[0]
	if r.Agent != "claude" || r.SessionID != "sess-1" || r.OriginCwd != "/work/proj" {
		t.Errorf("identity wrong: agent=%q session=%q cwd=%q", r.Agent, r.SessionID, r.OriginCwd)
	}
	if r.UserTurns != 1 || r.TotalTurns != 2 {
		t.Errorf("turns = %d/%d, want 1/2", r.UserTurns, r.TotalTurns)
	}
	if r.UpdatedAt != "2026-06-25T00:02:00Z" {
		t.Errorf("updatedAt = %q, want last message timestamp", r.UpdatedAt)
	}

	// The conversation body is full-text searchable immediately.
	if hits, _ := store.Search(ftsQuery("indexing"), "proj-1"); len(hits) != 1 {
		t.Errorf("search for body word returned %d hits, want 1", len(hits))
	}

	// Dedup guard: re-recording with no new activity is a no-op.
	li.Record("claude", sess(user1, agent1))
	if rows, _ := store.ListByProject("proj-1"); len(rows) != 1 {
		t.Errorf("dedup: want 1 row, got %d", len(rows))
	}

	// New activity (a later message) updates the same row in place.
	li.Record("claude", sess(user1, agent1, msg(schema.RoleUser, "2026-06-25T00:03:00Z", "more")))
	rows, _ = store.ListByProject("proj-1")
	if len(rows) != 1 {
		t.Fatalf("want 1 row after update, got %d", len(rows))
	}
	if rows[0].UserTurns != 2 || rows[0].TotalTurns != 3 || rows[0].UpdatedAt != "2026-06-25T00:03:00Z" {
		t.Errorf("after update: turns=%d/%d updatedAt=%q, want 2/3 and advanced timestamp",
			rows[0].UserTurns, rows[0].TotalTurns, rows[0].UpdatedAt)
	}

	// A nil indexer (index failed to open) is safe to use.
	var nilLI *LiveIndexer
	nilLI.Record("claude", sess(user1))
	nilLI.Close()
}
