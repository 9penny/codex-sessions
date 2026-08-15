// Package sessionindex implements sessions.db — the machine-level index of every coding-agent
// session SpecStory knows about, across all projects and providers. It backs the
// `specstory resume` selection UX and is (re)built by `specstory reindex`.
//
// sessions.db is a DERIVED CACHE over the native session stores: it can be deleted and
// fully rebuilt at any time. See docs/SESSIONS-DB.md for the design and schema.
package sessionindex

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/specstoryai/getspecstory/specstory-cli/pkg/config"
	"github.com/specstoryai/getspecstory/specstory-cli/pkg/spi"

	sqlite "modernc.org/sqlite" // SQLite driver (pure Go), same as pkg/provenance; named for *sqlite.Error
)

// Connection-pool sizing. Writes (reindex) must serialize on a single connection to
// avoid SQLITE_BUSY/deadlocks. The interactive browse path (resume/search) only reads,
// so it opens several connections — WAL permits many concurrent readers, which keeps a
// single slow full-text query (a broad prefix can take seconds) from starving the UI.
const (
	writerConns = 1
	readerConns = 4

	// CurrentAIPromptVersion identifies the metadata contract understood by the
	// list/search UI. Older prompt output stays disposable but is hidden as stale.
	CurrentAIPromptVersion = 1
)

// connectionPragmas are applied to EVERY pooled connection via the DSN rather than run once
// with db.Exec. synchronous/cache_size/temp_store/mmap_size are per-connection settings, and
// OpenReader uses several connections — a one-shot Exec would configure only whichever single
// connection happened to serve it and leave the rest on SQLite defaults, defeating the
// multi-reader design. page_size only takes effect before the database file is created, so it
// must be set at open time (on the writer that first creates sessions.db), not after the schema
// exists. Values avoid spaces so they need no URL escaping in the DSN query string.
var connectionPragmas = []string{
	"busy_timeout=15000",
	"journal_mode(WAL)",
	"synchronous=NORMAL",
	"cache_size=-64000",
	"temp_store=MEMORY",
	"mmap_size=268435456",
	"page_size=8192",
}

// Session is one row of the restore index (the `sessions` table), plus Body, which is
// indexed into the FTS table rather than stored on the row. See docs/SESSIONS-DB.md.
type Session struct {
	ProjectID    string // resolved git_id (else workspace_id)
	ProjectName  string
	Agent        string // provider id: claude, codex, gemini, droid, deepseek, cursor
	SessionID    string // native session id (uuid)
	CreatedAt    string // ISO 8601, first turn
	UpdatedAt    string // ISO 8601, last activity (last turn, else file mtime)
	UserTurns    int    // count of user prompts
	TotalTurns   int    // count of all messages
	Slug         string
	Name         string
	NativePath   string // absolute path the provider opens to read this session
	OriginCwd    string // working directory the session was launched from
	Kind         spi.SessionKind
	Size         int64  // native file size, bytes — part of the freshness fingerprint
	Mtime        int64  // native file mtime, epoch ms — part of the freshness fingerprint
	IndexVersion int    // reindex logic version that wrote this row — part of the fingerprint
	IndexedAt    string // ISO 8601, when reindex last wrote this row

	// Body is the full conversation text, indexed into sessions_fts (not persisted on
	// the sessions row). Left empty on rows read back out of the index.
	Body string

	// IsNew is a write-time hint, not a persisted column: when true, the caller has
	// determined this (agent, session_id) has no existing index row, so upsertOne can skip
	// looking up and deleting a prior FTS row before insert. Leave false when unsure — the
	// lookup-and-delete is then kept, which is always correct (a missing prior row is a no-op).
	IsNew bool

	// The following fields are TUI/cloud-only — NOT persisted to sessions.db (the DB
	// scan/upsert never references them). They are populated only on cloud-sourced rows
	// blended into the resume browser. Local rows leave them zero-valued.
	IsCloud     bool   // true = this row came from SpecStory Cloud, not the local index
	DeviceID    string // cloud metadata.deviceId — stable machine id for the machine filter
	MachineName string // cloud metadata.machineName — human machine label for the machine filter

	// Current derived AI metadata. These values are hydrated from ai_metadata only
	// when its source fingerprint and prompt version match this session.
	AITitle   string
	AISummary string
	AITags    []string
}

// Fingerprint identifies an indexed session's freshness: the native file's size and
// mtime, plus the reindex logic version that produced the row. reindex skips a session
// whose fingerprint is unchanged. See docs/SESSIONS-DB.md.
type Fingerprint struct {
	Size    int64
	Mtime   int64
	Version int
	// Deleted marks a soft-deleted (tombstoned) session. reindex skips it regardless of
	// whether the native file changed, so a user's delete stays deleted until sessions.db
	// is wiped and rebuilt. See the deleted-column migration in ensureSchema.
	Deleted bool
}

// AIMetadata is derived, replaceable metadata generated from a redacted session
// conversation. The source fingerprint and prompt version make enrichment
// incremental without ever modifying the provider's native session file.
type AIMetadata struct {
	Agent              string
	SessionID          string
	SourceSize         int64
	SourceMtime        int64
	SourceIndexVersion int
	PromptVersion      int
	Model              string
	Title              string
	Summary            string
	Tags               []string
	InputTokens        int
	OutputTokens       int
	EnrichedAt         string
}

// Store is a handle to sessions.db.
type Store struct {
	db *sql.DB
}

// DefaultPath returns the Codex Sessions database path under XDG_DATA_HOME.
func DefaultPath() (string, error) {
	paths, err := config.ResolveCodexSessionsPaths()
	if err != nil {
		return "", err
	}
	return paths.DatabaseFile, nil
}

// Open opens (or creates) sessions.db for writing — a single serialized connection,
// applying WAL + performance pragmas (matching pkg/provenance) and ensuring the schema
// exists. Use OpenReader for the read-only interactive browse path.
func Open(path string) (*Store, error) {
	return openWith(path, writerConns)
}

// OpenReader opens sessions.db for the interactive browse path (resume/search), which
// only reads. It allows several concurrent connections so one slow full-text query can't
// starve the UI (WAL permits many simultaneous readers). Never write through this handle.
func OpenReader(path string) (*Store, error) {
	return openWith(path, readerConns)
}

func openWith(path string, maxConns int) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating database directory: %w", err)
	}

	s, err := openStore(path, maxConns)
	if err == nil {
		return s, nil
	}

	// A disk-I/O error opening the index almost always means STALE WAL sidecars: the main
	// sessions.db was deleted or replaced while sessions.db-wal / sessions.db-shm were left behind,
	// so SQLite fails trying to recover a WAL that no longer matches the file (e.g. a short read,
	// SQLITE_IOERR_SHORT_READ / 522). The sidecars are ephemeral and sessions.db is a derived cache
	// (rebuilt by reindex), so clearing the sidecars and retrying once is safe and never loses real
	// data — it just avoids a hard failure that forces the user to hunt down the sidecar files.
	if isDiskIOError(err) {
		slog.Warn("sessions.db open failed with disk I/O error; clearing stale WAL sidecars and retrying", "error", err)
		removeWALSidecars(path)
		return openStore(path, maxConns)
	}
	return nil, err
}

// openStore opens sessions.db with the connection pragmas and ensures the schema exists.
func openStore(path string, maxConns int) (*Store, error) {
	dsn := "file:" + filepath.ToSlash(path) + "?_pragma=" + strings.Join(connectionPragmas, "&_pragma=")
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("opening sessions.db: %w", err)
	}

	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)

	s := &Store{db: db}
	if err := s.ensureSchema(); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ensuring schema: %w", err)
	}
	return s, nil
}

// isDiskIOError reports whether err is (or wraps) a SQLite disk-I/O error — the SQLITE_IOERR
// family (primary result code 10), which includes extended codes like SQLITE_IOERR_SHORT_READ
// (522) that a stale WAL sidecar produces.
func isDiskIOError(err error) bool {
	const sqliteIOErr = 10 // SQLITE_IOERR primary code; extended codes are 10 | (n<<8)
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) {
		return sqliteErr.Code()&0xFF == sqliteIOErr
	}
	return false
}

// removeWALSidecars deletes sessions.db's WAL/shm sidecar files. Best-effort: a missing file is
// fine, and any other removal error is logged but not fatal (the retry open will surface it).
func removeWALSidecars(path string) {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(path + suffix); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove WAL sidecar", "path", path+suffix, "error", err)
		}
	}
}

// Close closes the database connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// sessionColumns is the canonical sessions column list, shared by every SELECT so the
// scan order in scanSessions stays in lockstep with it.
const sessionColumns = `project_id, project_name, agent, session_id, created_at, updated_at,
	user_turns, total_turns, slug, name, native_path, origin_cwd, kind, size, mtime, index_version, indexed_at`

// sessionInsertColumns is sessionColumns plus fts_rowid, the internal link to the session's
// FTS row. fts_rowid is write-only — set on insert, read only by SessionBody's join — so it is
// deliberately kept out of sessionColumns and the Session struct rather than threaded through
// every read path.
const sessionInsertColumns = sessionColumns + `, fts_rowid`

func (s *Store) ensureSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS sessions (
		project_id   TEXT NOT NULL,
		project_name TEXT,
		agent        TEXT NOT NULL,
		session_id   TEXT NOT NULL,
		created_at   TEXT,
		updated_at   TEXT,
		user_turns   INTEGER,
		total_turns  INTEGER,
		slug         TEXT,
		name         TEXT,
		native_path  TEXT,
		origin_cwd   TEXT,
		kind         TEXT NOT NULL DEFAULT '',
		size          INTEGER,
		mtime         INTEGER,
		index_version INTEGER,
		indexed_at    TEXT,
		fts_rowid     INTEGER,
		PRIMARY KEY (agent, session_id)
	);
	-- Composite over the project filter PLUS the picker's sort order. ListByProject
	-- (the resume picker's hot path) filters by project_id and orders by
	-- updated_at DESC, created_at DESC; this index serves both, so SQLite walks it
	-- backward instead of filtering then sorting in a temp b-tree. project_id is the
	-- left prefix, so ProjectCount/UnattributedCount still use it. Replaces the old
	-- single-column idx_sessions_project (dropped in the migration below).
	CREATE INDEX IF NOT EXISTS idx_sessions_project_recent ON sessions(project_id, updated_at, created_at);

	-- Standalone FTS5 index over the conversation body + name. session_id/agent ride
	-- along UNINDEXED as join keys back to the sessions row. See docs/SESSIONS-DB.md.
	CREATE VIRTUAL TABLE IF NOT EXISTS sessions_fts USING fts5(
		session_id UNINDEXED,
		agent UNINDEXED,
		name,
		body,
		search_terms,
		ai_metadata,
		ai_search_terms
	);

	-- AI metadata is a derived sidecar over sessions. It deliberately contains no
	-- conversation text and has no foreign key, so sessions.db remains a disposable
	-- cache that can be rebuilt in either order.
	CREATE TABLE IF NOT EXISTS ai_metadata (
		agent                TEXT NOT NULL,
		session_id           TEXT NOT NULL,
		source_size          INTEGER NOT NULL,
		source_mtime         INTEGER NOT NULL,
		source_index_version INTEGER NOT NULL,
		prompt_version       INTEGER NOT NULL,
		model                TEXT NOT NULL,
		title                TEXT NOT NULL,
		summary              TEXT NOT NULL,
		tags_json            TEXT NOT NULL,
		input_tokens         INTEGER NOT NULL,
		output_tokens        INTEGER NOT NULL,
		enriched_at          TEXT NOT NULL,
		PRIMARY KEY (agent, session_id)
	);
	`
	if _, err := s.db.Exec(schema); err != nil {
		return fmt.Errorf("executing schema: %w", err)
	}
	// Migration for indexes created before index_version existed (the column is part of
	// the freshness fingerprint). The error is benign when the column already exists.
	s.runMigration(`ALTER TABLE sessions ADD COLUMN index_version INTEGER DEFAULT 0`)
	// fts_rowid links a session row to its sessions_fts row, so the body read and the
	// delete-before-insert are O(1) rowid lookups instead of whole-FTS scans (session_id/agent
	// are UNINDEXED). NULL on rows written before this column existed; a reindex (reindexVersion
	// bump) repopulates it. Benign error when the column already exists.
	s.runMigration(`ALTER TABLE sessions ADD COLUMN fts_rowid INTEGER`)
	// Soft-delete tombstone: a user can remove a session (or a whole project) from the
	// picker via the resume/search TUI. Rather than dropping the row — which reindex would
	// simply re-add on the next pass — we keep the row, flag it deleted, and strip its FTS
	// body. Every read path filters deleted=0, the write path (upsertOne) refuses to
	// resurrect a tombstoned row even on new content, and reindex skips it. So a delete
	// stays deleted until the user wipes sessions.db and rebuilds. Benign error when the
	// column already exists. Constant DEFAULT keeps existing rows visible (0). See
	// docs/SESSIONS-DB.md.
	s.runMigration(`ALTER TABLE sessions ADD COLUMN deleted INTEGER NOT NULL DEFAULT 0`)
	// Session source classification supports hiding background Codex work by default. An empty
	// value keeps legacy/non-Codex rows visible; a reindex version bump classifies Codex rows.
	s.runMigration(`ALTER TABLE sessions ADD COLUMN kind TEXT NOT NULL DEFAULT ''`)
	// Drop the old single-column project index now superseded by the composite
	// idx_sessions_project_recent (project_id is its left prefix). Idempotent; a no-op
	// on fresh databases that never had it.
	s.runMigration(`DROP INDEX IF EXISTS idx_sessions_project`)
	return s.ensureFTSSearchColumns()
}

// ensureFTSSearchColumns upgrades standalone FTS tables created by older versions. FTS5
// virtual tables cannot add columns, so rows are copied into a replacement table while
// preserving rowids (and therefore every sessions.fts_rowid link).
func (s *Store) ensureFTSSearchColumns() error {
	rows, err := s.db.Query(`PRAGMA table_info(sessions_fts)`)
	if err != nil {
		return err
	}
	found := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, columnType string
		var notNull, primaryKey int
		var defaultValue any
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			_ = rows.Close()
			return err
		}
		found[name] = true
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if found["search_terms"] && found["ai_metadata"] && found["ai_search_terms"] {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if _, err := tx.Exec(`DROP TABLE IF EXISTS sessions_fts_search_upgrade`); err != nil {
		return err
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE sessions_fts_search_upgrade USING fts5(
		session_id UNINDEXED, agent UNINDEXED, name, body, search_terms, ai_metadata, ai_search_terms)`); err != nil {
		return err
	}
	oldRows, err := tx.Query(`SELECT f.rowid, f.session_id, f.agent, f.name, f.body,
		COALESCE(a.title, ''), COALESCE(a.summary, ''), COALESCE(a.tags_json, '')
		FROM sessions_fts f
		LEFT JOIN sessions s ON s.agent = f.agent AND s.session_id = f.session_id
		LEFT JOIN ai_metadata a ON a.agent = s.agent AND a.session_id = s.session_id
			AND a.source_size = s.size AND a.source_mtime = s.mtime
			AND a.source_index_version = s.index_version AND a.prompt_version = ?`, CurrentAIPromptVersion)
	if err != nil {
		return err
	}
	insert, err := tx.Prepare(`INSERT INTO sessions_fts_search_upgrade
		(rowid, session_id, agent, name, body, search_terms, ai_metadata, ai_search_terms)
		VALUES (?,?,?,?,?,?,?,?)`)
	if err != nil {
		_ = oldRows.Close()
		return err
	}
	defer func() { _ = insert.Close() }()
	for oldRows.Next() {
		var rowid int64
		var sessionID, agent, name, body, aiTitle, aiSummary, tagsJSON string
		if err := oldRows.Scan(&rowid, &sessionID, &agent, &name, &body, &aiTitle, &aiSummary, &tagsJSON); err != nil {
			_ = oldRows.Close()
			return err
		}
		aiText := aiMetadataSearchText(aiTitle, aiSummary, tagsJSON)
		if _, err := insert.Exec(rowid, sessionID, agent, name, body,
			cjkSearchTerms(name+"\n"+body), aiText, cjkSearchTerms(aiText)); err != nil {
			_ = oldRows.Close()
			return err
		}
	}
	if err := oldRows.Err(); err != nil {
		_ = oldRows.Close()
		return err
	}
	if err := oldRows.Close(); err != nil {
		return err
	}
	if err := insert.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`DROP TABLE sessions_fts`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE sessions_fts_search_upgrade RENAME TO sessions_fts`); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	committed = true
	return nil
}

func aiMetadataSearchText(title, summary, tagsJSON string) string {
	var tags []string
	if tagsJSON != "" {
		_ = json.Unmarshal([]byte(tagsJSON), &tags)
	}
	parts := []string{strings.TrimSpace(title), strings.TrimSpace(summary), strings.Join(tags, " ")}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// runMigration applies an idempotent schema migration whose error is usually the benign
// "column/index already exists". We can't cleanly tell that apart from a real failure (locked
// DB, I/O) without matching driver-specific strings, so we log at Debug rather than fail: a
// genuine problem still resurfaces as a later query error, but it is no longer fully invisible.
func (s *Store) runMigration(stmt string) {
	if _, err := s.db.Exec(stmt); err != nil {
		slog.Debug("sessionindex: migration step skipped", "stmt", stmt, "error", err)
	}
}

// Fingerprints returns the freshness fingerprint of every indexed session, keyed by
// fingerprintKey(agent, session_id). reindex loads this once and skips any session
// whose native file is unchanged (same size + mtime) and was indexed by the current
// logic version.
func (s *Store) Fingerprints() (map[string]Fingerprint, error) {
	rows, err := s.db.Query(`SELECT agent, session_id, size, mtime, index_version, deleted FROM sessions`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]Fingerprint)
	for rows.Next() {
		var agent, sessionID string
		var deleted int
		var fp Fingerprint
		if err := rows.Scan(&agent, &sessionID, &fp.Size, &fp.Mtime, &fp.Version, &deleted); err != nil {
			return nil, err
		}
		fp.Deleted = deleted != 0
		out[FingerprintKey(agent, sessionID)] = fp
	}
	return out, rows.Err()
}

// FingerprintKey is the map key for a session's fingerprint: agent + NUL + session_id.
func FingerprintKey(agent, sessionID string) string {
	return agent + "\x00" + sessionID
}

// ListEnrichmentCandidates returns newest-first Codex sessions whose derived AI
// metadata is absent or stale. force includes every live interactive Codex session.
func (s *Store) ListEnrichmentCandidates(limit, promptVersion int, force bool) ([]Session, error) {
	if limit < 1 || limit > 1000 {
		return nil, fmt.Errorf("enrichment limit must be between 1 and 1000")
	}
	if promptVersion < 1 {
		return nil, fmt.Errorf("prompt version must be positive")
	}

	q := `SELECT ` + sessionColumns + ` FROM sessions
		WHERE deleted = 0 AND agent = 'codex' AND native_path != ''
		AND (kind = '' OR kind = 'interactive')`
	args := []any{}
	if !force {
		q += ` AND NOT EXISTS (
			SELECT 1 FROM ai_metadata a
			WHERE a.agent = sessions.agent AND a.session_id = sessions.session_id
			AND a.source_size = sessions.size AND a.source_mtime = sessions.mtime
			AND a.source_index_version = sessions.index_version AND a.prompt_version = ?
		)`
		args = append(args, promptVersion)
	}
	q += ` ORDER BY updated_at DESC, created_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanSessions(rows)
}

// UpsertAIMetadata atomically replaces one session's derived metadata.
func (s *Store) UpsertAIMetadata(metadata AIMetadata) error {
	tagsJSON, err := json.Marshal(metadata.Tags)
	if err != nil {
		return fmt.Errorf("encode AI metadata tags: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin AI metadata upsert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	_, err = tx.Exec(`INSERT INTO ai_metadata (
		agent, session_id, source_size, source_mtime, source_index_version,
		prompt_version, model, title, summary, tags_json, input_tokens,
		output_tokens, enriched_at
	) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(agent, session_id) DO UPDATE SET
		source_size=excluded.source_size, source_mtime=excluded.source_mtime,
		source_index_version=excluded.source_index_version, prompt_version=excluded.prompt_version,
		model=excluded.model, title=excluded.title, summary=excluded.summary,
		tags_json=excluded.tags_json, input_tokens=excluded.input_tokens,
		output_tokens=excluded.output_tokens, enriched_at=excluded.enriched_at`,
		metadata.Agent, metadata.SessionID, metadata.SourceSize, metadata.SourceMtime,
		metadata.SourceIndexVersion, metadata.PromptVersion, metadata.Model, metadata.Title,
		metadata.Summary, string(tagsJSON), metadata.InputTokens, metadata.OutputTokens,
		metadata.EnrichedAt)
	if err != nil {
		return fmt.Errorf("upsert AI metadata: %w", err)
	}

	var size, mtime int64
	var indexVersion int
	var ftsRowid sql.NullInt64
	err = tx.QueryRow(`SELECT size, mtime, index_version, fts_rowid FROM sessions
		WHERE agent = ? AND session_id = ?`, metadata.Agent, metadata.SessionID).Scan(
		&size, &mtime, &indexVersion, &ftsRowid)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("look up AI metadata session: %w", err)
	}
	aiText := ""
	if err == nil && size == metadata.SourceSize && mtime == metadata.SourceMtime &&
		indexVersion == metadata.SourceIndexVersion && metadata.PromptVersion == CurrentAIPromptVersion {
		aiText = aiMetadataSearchText(metadata.Title, metadata.Summary, string(tagsJSON))
	}
	if ftsRowid.Valid {
		if _, err := tx.Exec(`UPDATE sessions_fts SET ai_metadata = ?, ai_search_terms = ? WHERE rowid = ?`,
			aiText, cjkSearchTerms(aiText), ftsRowid.Int64); err != nil {
			return fmt.Errorf("update AI metadata search index: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit AI metadata upsert: %w", err)
	}
	committed = true
	return nil
}

// GetAIMetadata returns a session's derived metadata, if present.
func (s *Store) GetAIMetadata(agent, sessionID string) (AIMetadata, bool, error) {
	var metadata AIMetadata
	var tagsJSON string
	err := s.db.QueryRow(`SELECT agent, session_id, source_size, source_mtime,
		source_index_version, prompt_version, model, title, summary, tags_json,
		input_tokens, output_tokens, enriched_at
		FROM ai_metadata WHERE agent = ? AND session_id = ?`, agent, sessionID).Scan(
		&metadata.Agent, &metadata.SessionID, &metadata.SourceSize, &metadata.SourceMtime,
		&metadata.SourceIndexVersion, &metadata.PromptVersion, &metadata.Model,
		&metadata.Title, &metadata.Summary, &tagsJSON, &metadata.InputTokens,
		&metadata.OutputTokens, &metadata.EnrichedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return AIMetadata{}, false, nil
	}
	if err != nil {
		return AIMetadata{}, false, err
	}
	if err := json.Unmarshal([]byte(tagsJSON), &metadata.Tags); err != nil {
		return AIMetadata{}, false, fmt.Errorf("decode AI metadata tags: %w", err)
	}
	return metadata, true, nil
}

// AttachCurrentAIMetadata hydrates sessions in place, but only when the stored
// metadata matches their exact source fingerprint and the active prompt contract.
func (s *Store) AttachCurrentAIMetadata(sessions []Session) error {
	for i := range sessions {
		sessions[i].AITitle = ""
		sessions[i].AISummary = ""
		sessions[i].AITags = nil
	}
	const chunkSize = 300 // two bind variables each; stays below SQLite's common 999 limit
	for start := 0; start < len(sessions); start += chunkSize {
		end := min(start+chunkSize, len(sessions))
		clauses := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*2)
		positions := make(map[string][]int, end-start)
		for i := start; i < end; i++ {
			clauses = append(clauses, `(agent = ? AND session_id = ?)`)
			args = append(args, sessions[i].Agent, sessions[i].SessionID)
			key := FingerprintKey(sessions[i].Agent, sessions[i].SessionID)
			positions[key] = append(positions[key], i)
		}
		rows, err := s.db.Query(`SELECT agent, session_id, source_size, source_mtime,
			source_index_version, prompt_version, title, summary, tags_json
			FROM ai_metadata WHERE `+strings.Join(clauses, ` OR `), args...)
		if err != nil {
			return err
		}
		for rows.Next() {
			var agent, sessionID, title, summary, tagsJSON string
			var sourceSize, sourceMtime int64
			var sourceVersion, promptVersion int
			if err := rows.Scan(&agent, &sessionID, &sourceSize, &sourceMtime, &sourceVersion,
				&promptVersion, &title, &summary, &tagsJSON); err != nil {
				_ = rows.Close()
				return err
			}
			for _, i := range positions[FingerprintKey(agent, sessionID)] {
				if sessions[i].Size != sourceSize || sessions[i].Mtime != sourceMtime ||
					sessions[i].IndexVersion != sourceVersion || promptVersion != CurrentAIPromptVersion {
					continue
				}
				var tags []string
				if err := json.Unmarshal([]byte(tagsJSON), &tags); err != nil {
					_ = rows.Close()
					return fmt.Errorf("decode AI metadata tags: %w", err)
				}
				sessions[i].AITitle, sessions[i].AISummary, sessions[i].AITags = title, summary, tags
			}
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return err
		}
		if err := rows.Close(); err != nil {
			return err
		}
	}
	return nil
}

// sessionUpsertStmts are the per-row statements UpsertBatch prepares once and reuses across
// every row in a transaction, so the SQL is parsed once per batch instead of once per row.
type sessionUpsertStmts struct {
	insSession    *sql.Stmt
	selOldRowid   *sql.Stmt
	selAIMetadata *sql.Stmt
	delFTSByRowid *sql.Stmt
	delFTSByKey   *sql.Stmt
	insFTS        *sql.Stmt
}

// upsertOne writes one session's row and full-text row using the batch's prepared statements.
// FTS5 standalone tables are not auto-synced, so the FTS row is maintained by hand. The new FTS
// row is inserted first so its rowid can be stored on the sessions row (fts_rowid) — that link
// is what makes later body reads and replace-deletes O(1) rowid lookups instead of whole-FTS
// scans (session_id/agent are UNINDEXED). For an existing session the prior FTS row is removed
// first; brand-new sessions (sess.IsNew) skip that lookup-and-delete entirely.
func upsertOne(st sessionUpsertStmts, sess Session) error {
	if !sess.IsNew {
		ex, err := lookupExisting(st, sess)
		if err != nil {
			return err
		}
		// A soft-deleted session stays deleted even when new turns arrive: skip the write
		// entirely so the tombstone (and its stripped FTS body) survives. This covers every
		// writer — reindex, the live `run`/`watch` indexer, and `sync` — so a delete only
		// comes back after a full sessions.db wipe + reindex. See the deleted-column migration.
		if ex.deleted {
			return nil
		}
		if ex.found {
			if err := clearOldFTSRow(st, sess, ex.ftsRowid); err != nil {
				return err
			}
		}
	}
	aiText, err := currentAIMetadataSearchText(st.selAIMetadata, sess)
	if err != nil {
		return err
	}
	res, err := st.insFTS.Exec(sess.SessionID, sess.Agent, sess.Name, sess.Body,
		cjkSearchTerms(sess.Name+"\n"+sess.Body), aiText, cjkSearchTerms(aiText))
	if err != nil {
		return fmt.Errorf("insert fts row: %w", err)
	}
	ftsRowid, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("read fts rowid: %w", err)
	}
	if _, err := st.insSession.Exec(
		sess.ProjectID, sess.ProjectName, sess.Agent, sess.SessionID, sess.CreatedAt, sess.UpdatedAt,
		sess.UserTurns, sess.TotalTurns, sess.Slug, sess.Name, sess.NativePath, sess.OriginCwd,
		sess.Kind, sess.Size, sess.Mtime, sess.IndexVersion, sess.IndexedAt, ftsRowid); err != nil {
		return fmt.Errorf("upsert session row: %w", err)
	}
	return nil
}

func currentAIMetadataSearchText(stmt *sql.Stmt, sess Session) (string, error) {
	var title, summary, tagsJSON string
	err := stmt.QueryRow(sess.Agent, sess.SessionID, sess.Size, sess.Mtime,
		sess.IndexVersion, CurrentAIPromptVersion).Scan(&title, &summary, &tagsJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("look up current AI metadata: %w", err)
	}
	return aiMetadataSearchText(title, summary, tagsJSON), nil
}

// existingRow is the prior index state for a session: its FTS rowid link (if any) and whether
// it is a soft-delete tombstone. found is false when there is no prior sessions row at all.
type existingRow struct {
	ftsRowid sql.NullInt64
	deleted  bool
	found    bool
}

// lookupExisting resolves the prior sessions row for sess by primary key, reporting its FTS
// rowid link and tombstone flag. It is the single read upsertOne needs before writing: a
// tombstoned row must not be resurrected, and a live row's stale FTS row must be cleared first.
func lookupExisting(st sessionUpsertStmts, sess Session) (existingRow, error) {
	var ex existingRow
	var deleted int
	switch err := st.selOldRowid.QueryRow(sess.Agent, sess.SessionID).Scan(&ex.ftsRowid, &deleted); {
	case errors.Is(err, sql.ErrNoRows):
		return ex, nil // no prior row
	case err != nil:
		return ex, fmt.Errorf("look up existing session: %w", err)
	}
	ex.found = true
	ex.deleted = deleted != 0
	return ex, nil
}

// clearOldFTSRow removes an existing session's current FTS row before the sessions row is
// replaced (INSERT OR REPLACE would otherwise drop the fts_rowid link and orphan the FTS row).
// It uses the already-resolved fts_rowid (O(1)); for rows written before fts_rowid existed
// (NULL) it falls back to the by-key delete — a whole-FTS scan that a reindex retires by
// repopulating fts_rowid.
func clearOldFTSRow(st sessionUpsertStmts, sess Session, ftsRowid sql.NullInt64) error {
	if ftsRowid.Valid {
		if _, err := st.delFTSByRowid.Exec(ftsRowid.Int64); err != nil {
			return fmt.Errorf("clear fts row by rowid: %w", err)
		}
		return nil
	}
	if _, err := st.delFTSByKey.Exec(sess.Agent, sess.SessionID); err != nil {
		return fmt.Errorf("clear legacy fts row: %w", err)
	}
	return nil
}

// prepareUpsert prepares the per-row statements on tx and returns them with a closer.
func prepareUpsert(tx *sql.Tx) (sessionUpsertStmts, func(), error) {
	var prepared []*sql.Stmt
	closer := func() {
		for _, s := range prepared {
			_ = s.Close()
		}
	}
	prep := func(what, query string) (*sql.Stmt, error) {
		s, err := tx.Prepare(query)
		if err != nil {
			closer()
			return nil, fmt.Errorf("prepare %s: %w", what, err)
		}
		prepared = append(prepared, s)
		return s, nil
	}

	insSession, err := prep("session upsert", `INSERT OR REPLACE INTO sessions (`+sessionInsertColumns+`)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	selOldRowid, err := prep("existing row lookup", `SELECT fts_rowid, deleted FROM sessions WHERE agent = ? AND session_id = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	selAIMetadata, err := prep("current AI metadata lookup", `SELECT title, summary, tags_json
		FROM ai_metadata WHERE agent = ? AND session_id = ? AND source_size = ?
		AND source_mtime = ? AND source_index_version = ? AND prompt_version = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	delFTSByRowid, err := prep("fts delete by rowid", `DELETE FROM sessions_fts WHERE rowid = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	delFTSByKey, err := prep("fts delete by key", `DELETE FROM sessions_fts WHERE agent = ? AND session_id = ?`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	insFTS, err := prep("fts insert", `INSERT INTO sessions_fts
		(session_id, agent, name, body, search_terms, ai_metadata, ai_search_terms)
		VALUES (?,?,?,?,?,?,?)`)
	if err != nil {
		return sessionUpsertStmts{}, func() {}, err
	}
	return sessionUpsertStmts{
		insSession:    insSession,
		selOldRowid:   selOldRowid,
		selAIMetadata: selAIMetadata,
		delFTSByRowid: delFTSByRowid,
		delFTSByKey:   delFTSByKey,
		insFTS:        insFTS,
	}, closer, nil
}

// Upsert inserts or replaces a single session row and its full-text row, atomically. A
// re-index of the same session (same agent + session_id) replaces both in lockstep.
func (s *Store) Upsert(sess Session) error {
	return s.UpsertBatch([]Session{sess})
}

// UpsertBatch writes many sessions in one transaction — the write path for `reindex`,
// which batches to avoid thousands of tiny WAL commits.
func (s *Store) UpsertBatch(sessions []Session) error {
	if len(sessions) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin upsert: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	st, closeStmts, err := prepareUpsert(tx)
	if err != nil {
		return err
	}
	defer closeStmts()

	for _, sess := range sessions {
		if err := upsertOne(st, sess); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert: %w", err)
	}
	committed = true
	return nil
}

// SoftDeleteSession tombstones a single session: flags the sessions row deleted and strips its
// FTS body, so it vanishes from the picker and search but the row survives as a fingerprint that
// reindex/warm skip forever (see the deleted-column migration and upsertOne's tombstone guard).
// The native session file on disk is untouched. Must be called on a writer handle (Open, not
// OpenReader). Returns the number of rows affected (0 when the session isn't indexed).
func (s *Store) SoftDeleteSession(agent, sessionID string) (int, error) {
	return s.softDelete(`agent = ? AND session_id = ?`, agent, sessionID)
}

// SoftDeleteProject tombstones every session in a project (see SoftDeleteSession). Future
// sessions in the same project are NOT blocked — a fresh session there indexes normally; only
// the rows that exist at delete time are tombstoned. Returns the number of rows affected.
func (s *Store) SoftDeleteProject(projectID string) (int, error) {
	return s.softDelete(`project_id = ?`, projectID)
}

// softDelete flags the rows matched by where (deleted = 1) and clears their FTS rows in one
// transaction, keeping each sessions row (and its fingerprint) so reindex treats it as
// unchanged. FTS rows are removed by (agent, session_id) — the same by-key delete upsert uses,
// which works on the UNINDEXED FTS columns without relying on row-value subquery support in
// FTS5. Only already-live rows are touched (deleted = 0), so a re-delete is a no-op returning 0.
func (s *Store) softDelete(where string, args ...any) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin soft delete: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	// Resolve exactly which sessions this delete targets, so the FTS rows can be cleared by key.
	rows, err := tx.Query(`SELECT agent, session_id FROM sessions WHERE (`+where+`) AND deleted = 0`, args...)
	if err != nil {
		return 0, fmt.Errorf("select rows to delete: %w", err)
	}
	type key struct{ agent, sessionID string }
	var targets []key
	for rows.Next() {
		var k key
		if err := rows.Scan(&k.agent, &k.sessionID); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("scan row to delete: %w", err)
		}
		targets = append(targets, k)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return 0, fmt.Errorf("iterate rows to delete: %w", err)
	}
	_ = rows.Close()

	for _, k := range targets {
		if _, err := tx.Exec(`DELETE FROM sessions_fts WHERE agent = ? AND session_id = ?`, k.agent, k.sessionID); err != nil {
			return 0, fmt.Errorf("clear fts row: %w", err)
		}
	}

	if _, err := tx.Exec(`UPDATE sessions SET deleted = 1, fts_rowid = NULL WHERE (`+where+`) AND deleted = 0`, args...); err != nil {
		return 0, fmt.Errorf("tombstone sessions: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit soft delete: %w", err)
	}
	committed = true
	return len(targets), nil
}

// Exists reports whether a session row is already indexed, looked up by the primary key
// (agent, session_id) so it stays O(log n) instead of scanning. Because a session's sessions
// row and its sessions_fts row are always written together (upsertOne, one transaction), a
// missing sessions row guarantees a missing FTS row — which lets the live writer set
// Session.IsNew and skip the whole-table FTS delete for genuinely new sessions.
func (s *Store) Exists(agent, sessionID string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM sessions WHERE agent = ? AND session_id = ?`, agent, sessionID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// Count returns the number of indexed sessions.
func (s *Store) Count() (int, error) {
	return s.CountForAgent("")
}

// CountForAgent counts only one provider when agent is non-empty.
func (s *Store) CountForAgent(agent string) (int, error) {
	var n int
	q := `SELECT COUNT(*) FROM sessions WHERE deleted = 0 AND (kind = '' OR kind = 'interactive')`
	args := []any{}
	if agent != "" {
		q += ` AND agent = ?`
		args = append(args, agent)
	}
	if err := s.db.QueryRow(q, args...).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// ProjectCount returns the number of distinct attributed projects in the index
// (excluding the unknownID bucket).
func (s *Store) ProjectCount(unknownID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT project_id) FROM sessions WHERE project_id != ? AND deleted = 0 AND (kind = '' OR kind = 'interactive')`, unknownID).Scan(&n)
	return n, err
}

// UnattributedCount returns the number of sessions in the unknownID bucket.
func (s *Store) UnattributedCount(unknownID string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE project_id = ? AND deleted = 0 AND (kind = '' OR kind = 'interactive')`, unknownID).Scan(&n)
	return n, err
}

// KnownCwds returns the distinct non-empty origin_cwd values across all live indexed sessions —
// every working directory the index has ever learned, including those captured in real time by
// run/watch/sync. reindex's Cursor cwd-recovery seeds its md5(cwd)→cwd map with these (read before
// it upserts) so a Cursor session keeps the project attribution the live path recorded, instead of
// having reindex re-derive it from a cwd-less global enumeration and clobber it to "unknown".
func (s *Store) KnownCwds() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT origin_cwd FROM sessions WHERE origin_cwd != '' AND deleted = 0`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var cwds []string
	for rows.Next() {
		var cwd string
		if err := rows.Scan(&cwd); err != nil {
			return nil, err
		}
		cwds = append(cwds, cwd)
	}
	return cwds, rows.Err()
}

// GetSession returns the LIVE indexed session for (agent, sessionID), or ok=false when there is
// no such row — never indexed, OR soft-deleted (deleted = 0 is required, unlike Exists). Body is
// not populated. Used by resume's selection-time guard to prefer an in-place local resume over a
// cloud fetch when a cloud-badged session is actually present on this machine.
func (s *Store) GetSession(agent, sessionID string) (Session, bool, error) {
	rows, err := s.db.Query(`SELECT `+sessionColumns+`
		FROM sessions WHERE agent = ? AND session_id = ? AND deleted = 0 AND (kind = '' OR kind = 'interactive')`, agent, sessionID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()

	sessions, err := scanSessions(rows)
	if err != nil {
		return Session{}, false, err
	}
	if len(sessions) == 0 {
		return Session{}, false, nil
	}
	return sessions[0], true, nil
}

// GetSessionByID looks up a LIVE indexed session by its native session id across ALL agents
// (deleted = 0), most-recent-first. Used by `specstory resume --session <uuid>` to resolve a
// bare session id locally (before any cloud lookup) without knowing the agent up front. A
// native session id is a provider-generated UUID and reconstruction mints a fresh one, so two
// agents sharing an id is near-impossible — but if it happens, the most recent row wins.
func (s *Store) GetSessionByID(sessionID string) (Session, bool, error) {
	rows, err := s.db.Query(`SELECT `+sessionColumns+`
		FROM sessions WHERE session_id = ? AND deleted = 0 AND (kind = '' OR kind = 'interactive')
		ORDER BY updated_at DESC, created_at DESC LIMIT 1`, sessionID)
	if err != nil {
		return Session{}, false, err
	}
	defer func() { _ = rows.Close() }()

	sessions, err := scanSessions(rows)
	if err != nil {
		return Session{}, false, err
	}
	if len(sessions) == 0 {
		return Session{}, false, nil
	}
	return sessions[0], true, nil
}

// ListByProject returns a project's sessions, newest activity first. Body is not
// populated (it lives only in the FTS index). Used by the `specstory resume` picker.
func (s *Store) ListByProject(projectID string) ([]Session, error) {
	return s.ListByProjectVisibility(projectID, false)
}

// ListByProjectVisibility optionally includes live background sessions. Normal product paths
// pass false; the TUI's explicit show-hidden mode is the only supported true caller.
func (s *Store) ListByProjectVisibility(projectID string, includeHidden bool) ([]Session, error) {
	return s.ListByProjectForAgentVisibility(projectID, "", includeHidden)
}

// ListByProjectForAgentVisibility adds an explicit provider boundary to the visibility query.
// Codex Sessions uses agent="codex" so rows left by an older multi-provider build cannot reappear.
func (s *Store) ListByProjectForAgentVisibility(projectID, agent string, includeHidden bool) ([]Session, error) {
	q := `SELECT ` + sessionColumns + ` FROM sessions WHERE project_id = ? AND deleted = 0`
	args := []any{projectID}
	if agent != "" {
		q += ` AND agent = ?`
		args = append(args, agent)
	}
	q += kindVisibilitySQL(includeHidden) + ` ORDER BY updated_at DESC, created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	sessions, err := scanSessions(rows)
	if err != nil {
		return nil, err
	}
	if err := s.AttachCurrentAIMetadata(sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

// ProjectSummary is a rolled-up view of one project for the all-projects picker.
type ProjectSummary struct {
	ProjectID    string
	ProjectName  string
	Sessions     int            // total sessions in the project
	LastActivity string         // most recent updated_at across the project
	AgentCounts  map[string]int // sessions per agent (claude, codex, …)

	// IsCloud marks a project that exists only in SpecStory Cloud (from another machine, never
	// worked on locally). Set only on cloud-blended rows in the all-projects browser; local
	// rollups leave it false. AgentCounts is nil for these (the cloud list has no per-agent
	// breakdown).
	IsCloud bool
}

// ListProjects returns one rolled-up summary per project, most recently active first.
// Used by the all-projects view (date-bucketed). The unknown-project bucket is included;
// the caller decides how to present it.
func (s *Store) ListProjects() ([]ProjectSummary, error) {
	return s.ListProjectsVisibility(false)
}

// ListProjectsVisibility optionally includes background sessions in project counts.
func (s *Store) ListProjectsVisibility(includeHidden bool) ([]ProjectSummary, error) {
	return s.ListProjectsForAgentVisibility("", includeHidden)
}

// ListProjectsForAgentVisibility rolls up only one provider when agent is non-empty.
func (s *Store) ListProjectsForAgentVisibility(agent string, includeHidden bool) ([]ProjectSummary, error) {
	q := `SELECT project_id, project_name, agent, COUNT(*), MAX(updated_at) FROM sessions WHERE deleted = 0`
	args := []any{}
	if agent != "" {
		q += ` AND agent = ?`
		args = append(args, agent)
	}
	q += kindVisibilitySQL(includeHidden) + ` GROUP BY project_id, agent`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	byID := map[string]*ProjectSummary{}
	for rows.Next() {
		var pid, pname, agent, last string
		var n int
		if err := rows.Scan(&pid, &pname, &agent, &n, &last); err != nil {
			return nil, err
		}
		ps, ok := byID[pid]
		if !ok {
			ps = &ProjectSummary{ProjectID: pid, ProjectName: pname, AgentCounts: map[string]int{}}
			byID[pid] = ps
		}
		ps.Sessions += n
		ps.AgentCounts[agent] += n
		if ps.ProjectName == "" {
			ps.ProjectName = pname
		}
		if last > ps.LastActivity {
			ps.LastActivity = last
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]ProjectSummary, 0, len(byID))
	for _, ps := range byID {
		out = append(out, *ps)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastActivity > out[j].LastActivity })
	return out, nil
}

// ProjectSessionKey identifies one local session within a project for the all-projects rollup
// merge: the (agent, session_id) fingerprint the cloud rows dedup against. No activity time
// rides along — the local rollup's LastActivity (from ListProjects) already carries it.
type ProjectSessionKey struct {
	Agent     string // provider id: claude, codex, …
	SessionID string // native session id
}

// ListAllSessionKeysByProject returns every non-deleted local session keyed by project_id, for
// the all-projects browser's session-level merge with cloud rows. Unlike ListProjects (a
// rollup), this returns the per-session fingerprint set so the caller can dedup cloud rows against
// local by (agent, session_id) — local preferred — and recompute accurate per-agent chips / totals /
// last activity from the union. Used only by the cloud-projects blend; the local-only path keeps
// using ListProjects (a single grouped query is cheaper than enumerating then rolling up client-side).
func (s *Store) ListAllSessionKeysByProject() (map[string][]ProjectSessionKey, error) {
	rows, err := s.db.Query(`SELECT project_id, agent, session_id
		FROM sessions WHERE deleted = 0 AND (kind = '' OR kind = 'interactive')`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string][]ProjectSessionKey)
	for rows.Next() {
		var pid, agent, sid string
		if err := rows.Scan(&pid, &agent, &sid); err != nil {
			return nil, err
		}
		out[pid] = append(out[pid], ProjectSessionKey{Agent: agent, SessionID: sid})
	}
	return out, rows.Err()
}

// SessionBody returns the full-text conversation body for a session (for the preview
// pane), or "" if the session has no indexed body (e.g. Cursor, metadata-only).
func (s *Store) SessionBody(agent, sessionID string) (string, error) {
	var body string
	// Fast path: resolve the FTS row by the rowid stored on the sessions row (O(1)), instead of
	// scanning the whole FTS (session_id/agent are UNINDEXED).
	err := s.db.QueryRow(`SELECT f.body FROM sessions s
		JOIN sessions_fts f ON f.rowid = s.fts_rowid
		WHERE s.agent = ? AND s.session_id = ?`, agent, sessionID).Scan(&body)
	if err == nil {
		return body, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	// No fts_rowid link (a row written before the column existed). Fall back to the by-key scan;
	// a reindex repopulates fts_rowid and retires this path.
	err = s.db.QueryRow(`SELECT body FROM sessions_fts WHERE agent = ? AND session_id = ?`,
		agent, sessionID).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return body, err
}

// Search runs a full-text query and returns matching sessions, most recent first.
// projectID scopes the search to one project; "" searches across all projects.
func (s *Store) Search(query, projectID string) ([]Session, error) {
	return s.SearchContext(context.Background(), query, projectID)
}

// SearchContext is Search bound to a context. The interactive search cancels the context
// when a newer keystroke arrives, which aborts a slow in-flight query and frees its
// connection. Snippets are deliberately fetched separately for only the visible rows:
// FTS5 snippet generation over hundreds of full transcripts dominates broad searches.
func (s *Store) SearchContext(ctx context.Context, query, projectID string) ([]Session, error) {
	return s.SearchContextVisibility(ctx, query, projectID, false)
}

// SearchContextVisibility optionally includes live background sessions. Default search remains
// interactive-only; callers must explicitly opt in for a visibly marked show-hidden mode.
func (s *Store) SearchContextVisibility(ctx context.Context, query, projectID string, includeHidden bool) ([]Session, error) {
	return s.SearchContextForAgentVisibility(ctx, query, projectID, "", includeHidden)
}

// SearchContextForAgentVisibility restricts search to one provider when agent is non-empty.
func (s *Store) SearchContextForAgentVisibility(ctx context.Context, query, projectID, agent string, includeHidden bool) ([]Session, error) {
	q := `SELECT ` + prefixed("s", sessionColumns) + `
		FROM sessions_fts
		JOIN sessions s ON s.agent = sessions_fts.agent AND s.session_id = sessions_fts.session_id
		WHERE sessions_fts MATCH ? AND s.deleted = 0` + prefixedKindVisibilitySQL("s", includeHidden)
	args := []any{query}
	if agent != "" {
		q += ` AND s.agent = ?`
		args = append(args, agent)
	}
	if projectID != "" {
		q += ` AND s.project_id = ?`
		args = append(args, projectID)
	}
	// Order by recency, not FTS5's BM25 rank. For full-conversation transcripts BM25 is
	// noisy (length-normalization dominates) and a single-term query gets no IDF signal,
	// so relevance order looks arbitrary; newest-first is what's useful when browsing your
	// own history. It also makes LIMIT keep the 500 most RECENT matches (vs the 500 densest)
	// and skips BM25 scoring entirely. updated_at is ISO 8601, so TEXT sort = chronological.
	q += ` ORDER BY s.updated_at DESC LIMIT 500`

	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	sessions, err := scanSessions(rows)
	if err != nil {
		return nil, err
	}
	if err := s.AttachCurrentAIMetadata(sessions); err != nil {
		return nil, err
	}
	return sessions, nil
}

func kindVisibilitySQL(includeHidden bool) string {
	return prefixedKindVisibilitySQL("", includeHidden)
}

func prefixedKindVisibilitySQL(alias string, includeHidden bool) string {
	if includeHidden {
		return ""
	}
	if alias != "" {
		alias += "."
	}
	return ` AND (` + alias + `kind = '' OR ` + alias + `kind = 'interactive')`
}

// Snippets returns highlighted match snippets for the provided sessions. The result is
// keyed by FingerprintKey(agent, session_id). Matched terms are wrapped in \x02 and \x03
// (STX/ETX) so callers can highlight without colliding with conversation text.
func (s *Store) Snippets(query string, sessions []Session) (map[string]string, error) {
	return s.SnippetsContext(context.Background(), query, sessions)
}

// SnippetsContext is Snippets bound to a context. Callers should pass only the currently
// visible rows; snippet() is intentionally lazy because it is the expensive part of FTS
// search on long transcripts.
func (s *Store) SnippetsContext(ctx context.Context, query string, sessions []Session) (map[string]string, error) {
	out := map[string]string{}
	if query == "" || len(sessions) == 0 {
		return out, nil
	}

	clauses := make([]string, 0, len(sessions))
	args := []any{query}
	seen := map[string]bool{}
	for _, sess := range sessions {
		key := FingerprintKey(sess.Agent, sess.SessionID)
		if seen[key] {
			continue
		}
		seen[key] = true
		clauses = append(clauses, `(agent = ? AND session_id = ?)`)
		args = append(args, sess.Agent, sess.SessionID)
	}
	if len(clauses) == 0 {
		return out, nil
	}

	q := `SELECT agent, session_id, body, ai_metadata,
		snippet(sessions_fts, 3, char(2), char(3), '…', 12),
		snippet(sessions_fts, 5, char(2), char(3), '…', 12)
		FROM sessions_fts
		WHERE sessions_fts MATCH ? AND (` + strings.Join(clauses, ` OR `) + `)`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cjkNeedles := CJKNeedlesFromQuery(query)
	for rows.Next() {
		var agent, sessionID, body, aiText, bodySnippet, aiSnippet string
		if err := rows.Scan(&agent, &sessionID, &body, &aiText, &bodySnippet, &aiSnippet); err != nil {
			return nil, err
		}
		snippet := bodySnippet
		if readable := readableCJKSnippet(body, cjkNeedles); readable != "" {
			snippet = readable
		} else if readable := readableCJKSnippet(aiText, cjkNeedles); readable != "" {
			snippet = "✦ AI-generated: " + readable
		} else if aiSnippet != "" {
			snippet = "✦ AI-generated: " + aiSnippet
		}
		out[FingerprintKey(agent, sessionID)] = snippet
	}
	return out, rows.Err()
}

func readableCJKSnippet(body string, needles []string) string {
	matchByte := -1
	match := ""
	for _, needle := range needles {
		if at := strings.Index(body, needle); at >= 0 && (matchByte < 0 || at < matchByte) {
			matchByte = at
			match = needle
		}
	}
	if matchByte < 0 {
		return ""
	}
	bodyRunes := []rune(body)
	matchStart := len([]rune(body[:matchByte]))
	matchEnd := matchStart + len([]rune(match))
	const contextRunes = 30
	start := max(0, matchStart-contextRunes)
	end := min(len(bodyRunes), matchEnd+contextRunes)
	var snippet strings.Builder
	if start > 0 {
		snippet.WriteRune('…')
	}
	snippet.WriteString(string(bodyRunes[start:matchStart]))
	snippet.WriteByte('\x02')
	snippet.WriteString(string(bodyRunes[matchStart:matchEnd]))
	snippet.WriteByte('\x03')
	snippet.WriteString(string(bodyRunes[matchEnd:end]))
	if end < len(bodyRunes) {
		snippet.WriteRune('…')
	}
	return snippet.String()
}

// prefixed qualifies each comma-separated column in cols with the given table alias,
// e.g. prefixed("s", "a, b") -> "s.a, s.b". Used to disambiguate the sessions columns
// in the FTS join (where session_id/agent also exist on the FTS table).
func prefixed(alias, cols string) string {
	parts := strings.Split(cols, ",")
	for i, p := range parts {
		parts[i] = alias + "." + strings.TrimSpace(p)
	}
	return strings.Join(parts, ", ")
}

// scanSessions scans rows selected with sessionColumns (in order) into Sessions.
func scanSessions(rows *sql.Rows) ([]Session, error) {
	var out []Session
	for rows.Next() {
		var s Session
		if err := rows.Scan(
			&s.ProjectID, &s.ProjectName, &s.Agent, &s.SessionID, &s.CreatedAt, &s.UpdatedAt,
			&s.UserTurns, &s.TotalTurns, &s.Slug, &s.Name, &s.NativePath, &s.OriginCwd,
			&s.Kind, &s.Size, &s.Mtime, &s.IndexVersion, &s.IndexedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
