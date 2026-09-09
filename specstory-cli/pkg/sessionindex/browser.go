package sessionindex

import (
	"context"
)

// BrowserSessions includes soft-deleted rows so the web library can offer targeted recovery.
// Body remains in FTS and is not loaded into the project navigation response.
func (s *Store) BrowserSessions(ctx context.Context) ([]Session, map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE agent = 'codex' ORDER BY updated_at DESC, session_id`)
	if err != nil {
		return nil, nil, err
	}
	sessions, err := scanSessions(rows)
	_ = rows.Close()
	if err != nil {
		return nil, nil, err
	}
	deleted := map[string]bool{}
	rows, err = s.db.QueryContext(ctx, `SELECT session_id FROM sessions WHERE agent = 'codex' AND deleted = 1`)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			break
		}
		deleted[id] = true
	}
	if err == nil {
		err = rows.Err()
	}
	_ = rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if err = s.AttachCurrentAIMetadata(sessions); err != nil {
		return nil, nil, err
	}
	return sessions, deleted, nil
}

// BrowserMatches leaves scoping/pagination to the directory browser: applying a global LIMIT
// before a subtree filter would silently miss matching sessions in less recent projects.
func (s *Store) BrowserMatches(ctx context.Context, query string) (map[string]bool, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT session_id FROM sessions_fts WHERE sessions_fts MATCH ? AND agent = 'codex'`, query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}
