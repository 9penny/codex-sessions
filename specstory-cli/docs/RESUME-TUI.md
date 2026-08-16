# Resume TUI

`csessions resume` opens the shared local session browser. It starts on the current project's
interactive Codex sessions when possible and otherwise opens the all-projects browser. A missing
or empty derived index is rebuilt from native Codex JSONL first.

## Behavior

- `enter` or `r` resumes the selected native Codex session.
- `n` starts a new Codex process in the selected project's directory.
- Launch always uses the recorded directory. Missing, empty, or non-directory paths block launch
  with an explanation; there is no silent cwd fallback.
- `space` parses native JSONL on demand and opens a masked preview.
- A current optional AI title is preferred in lists and visibly prefixed with `✦`. Masked preview
  shows the generated summary and tags in a separate “may be wrong” block above the native
  transcript. Raw reveal contains only the native transcript.
- Uppercase `R` reveals only the active preview in memory. Closing preview, navigating, or exiting
  clears the revealed content.
- `/` searches sessions in the current scope. `tab` switches between the current project and all
  projects where available.
- `h` toggles process-local visibility of background `subagent`, `exec`, and unknown-source
  sessions. The header shows `HIDDEN SHOWN`, and rows carry a kind label while enabled.
- `d` soft-deletes an indexed row after confirmation. Native JSONL is never deleted, and the
  tombstone prevents ordinary or forced reindex from restoring the row.
- `q` quits; `esc` closes the current mode or goes back.

The TUI targets an 80-column SSH terminal. In v0.3.1 the complete local-mode key hint may wrap on
an exactly 80-column terminal; the actions remain available, but responsive footer help is a known
follow-up.

## Data boundary

The session list and search use the disposable SQLite index. Preview does not use the indexed FTS
body: it reads the selected native JSONL file, applies redaction, and sends only the resulting text
to the TUI model. Raw reveal is neither persisted nor logged.

AI metadata never replaces the native transcript or native session name. It is hydrated only when
its source size, mtime, index version, and prompt version are current; otherwise the TUI silently
uses the native fallback.

The index and native files remain separate:

- deleting `sessions.db` and running `csessions reindex` restores soft-deleted rows;
- Codex `/delete` permanently removes a saved native session; after a complete successful scan,
  `csessions reindex` removes the corresponding live derived row, FTS data, and AI metadata;
- failed, panicking, partial, or interrupted scans never use absence as deletion evidence;
- no TUI action mutates files under `~/.codex/sessions`;
- a preview parse or redaction failure blocks preview rather than falling back to unsafe content.

## Model and tests

`resume` and [`search`](SESSION-SEARCH.md) use the same Bubble Tea model in
`pkg/cmd/session_tui*.go`. Model tests cover visibility, counts, search scope, masked preview,
resume, new session, missing cwd, and cancellation. Provider fakes verify launch cwd and resume ID;
PTY smoke tests cover the non-launch TUI path at 80x24.
