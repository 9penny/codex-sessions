# `specstory resume` — the picker TUI

The interactive session picker that makes SpecStory the best way to resume a coding-agent
session. It reads the [`sessions.db` index](SESSIONS-DB.md) and launches the chosen session
via the existing reconstruct + `ExecAgentAndWatch` plumbing (see
[SESSION-PORTABILITY.md](SESSION-PORTABILITY.md)). This doc covers the UX and the build plan;
it replaces the old plain numbered-menu selection in `pkg/cmd/resume.go`.

> **Shared model.** `resume` and [`search`](SESSION-SEARCH.md) are the **same** Bubble Tea
> model (`sessionTUI` in `pkg/cmd/session_tui.go`), differing only in entry point: `resume`
> opens on the current project's session list; `search` opens straight into the all-projects
> FTS with the input focused. Keys, preview, agent filter, dense/sparse, and the target-agent
> step are identical by construction.

## Decisions (ratified)

- **TUI stack:** Bubble Tea v2 + Bubbles v2 + Lipgloss v2 (the `charm.land/*/v2` modules —
  latest, and aligned with the `lipgloss/v2` already pulled in by `fang`). Not the v1 stack
  stoa-cli pins; we use the latest.
- **No source-agent step.** The picker shows **all sessions across all agents** for the
  current project by default, each row tagged with its agent. (The old flow's "pick the source
  agent first" is gone.)
- **Empty current project → all-projects view.** If the current project has no sessions, skip
  the project list and open directly in the all-projects view.
- **Empty/missing `sessions.db` → reindex first.** If the index doesn't exist, run `reindex`
  (with its normal progress UI) and then continue straight into the picker (don't exit).
- **Dense / sparse view modes.** Dense = more sessions, less per-session detail; sparse = more
  detail, fewer sessions. Toggle is easy/obvious; the choice is **remembered** in
  `~/.specstory/cli/config.toml` `[resume] view_mode` (via `config.SaveResumePrefs`).
- **Preview (`space`)** parses the native Codex JSONL on demand and opens a scrollable,
  **glamour-rendered** reader. It is masked before reaching the TUI model; uppercase `R`
  explicitly reveals the current preview in memory until it is closed. An unreadable source or
  redaction failure blocks preview rather than falling back to the FTS body.
- **All-projects view rolls up by relative date** (Today · Yesterday · Previous 7 days ·
  Previous 30 days · Older) by each project's latest activity, showing per-agent session counts
  (`Store.ListProjects`); the user expands a project to see its sessions.
- **`enter` and `r` resume; `n` starts a new Codex session.** Codex Sessions launches in the
  selected session's recorded project directory. A missing directory is explained and blocks
  launch rather than silently falling back to the caller's cwd.
- **`h` explicitly shows background sessions.** Subagent, `exec`, and unknown-source sessions
  remain excluded from default lists, project counts, and search. When enabled, the header says
  `HIDDEN SHOWN` and each background row displays its kind; this state lasts only for the process.
- **`d` deletes (soft), behind a `y/N` confirmation.** In a session list (or a cross-project
  search hit) `d` removes the highlighted **session**; in the all-projects browser it removes
  the highlighted **project** (all its sessions at once). This is a *soft delete*: the native
  session files on disk are untouched, but the row is tombstoned (`sessions.deleted = 1`, FTS
  body stripped) so it vanishes from resume/search and — crucially — **is not re-added by any
  later `reindex`/warm or live write** (see the tombstone guard in
  [SESSIONS-DB.md](SESSIONS-DB.md)). Deleting a project does **not** blacklist it: new sessions
  started there later index normally. The only way to restore a tombstoned session is to delete
  `~/.specstory/sessions.db` and rebuild it with `specstory reindex`. The confirmation screen
  spells all of this out; every key other than `y` cancels, so the destructive path is never
  the default.
- **Target agent (last step).** `specstory resume` lets the user pick the target agent as the
  final step; `specstory resume <agent>` pre-selects it. The **last-resumed agent** is the
  default selection there, remembered in `[resume] last_agent`.
- **`/` is always session full-text search; only its scope changes.** In a session list `/`
  is FTS scoped to that project; in the all-projects browser `/` is FTS across *all* projects
  (a flat results list, project shown per row, `r` resumes a hit).
  Project-name filtering is a *separate* key, **`p`** (browser only) — never a single box that
  mode-switches between the two.
- **Search results show the match, not the title.** Each result row renders the FTS
  `snippet()` (matched terms highlighted) in place of the session title once the visible-row
  snippet fetch lands, so you see *why* it matched without making the main search pay for every
  match. The query runs **async + debounced** (`searchDebounceMsg`/`searchResultMsg`, ~50ms)
  off the UI thread, `LIMIT`-bounded, newest-first, and snippet-free; snippets are fetched
  lazily for the visible window.

## Data foundation (built)

- `config`: `[resume]` section (`view_mode`, `last_agent`), `GetResumeViewMode()` /
  `GetResumeLastAgent()`, and `SaveResumePrefs()` — a section-preserving writer that upserts
  only `[resume]` so the self-documenting template's comments survive.
- `sessionindex`: `ListByProject(projectID)` (sessions, newest first), `ListProjects()`
  (date-sortable rollup with per-agent counts), `SessionBody(agent, sessionID)` (preview),
  `Search(query)` (global FTS), `SoftDeleteSession(agent, id)` / `SoftDeleteProject(projectID)`
  (the `d` tombstone). All tested.

## Build plan

### Stage A — current-project picker **(built)**

`pkg/cmd/session_tui.go` — a Bubble Tea v2 model wired into `resume.go`:

- Mixed-agent session list for the current project (`ComputeProjectID(cwd)` →
  `ListByProject`), newest first, colored agent tags.
- Agent filter (`a` cycles all → each present agent); dense/sparse toggle (`v`, persisted
  via `SaveResumePrefs`); glamour preview (`space`); full-text search (`/` → FTS, scoped to
  the project).
- Missing/empty `sessions.db` → `reindex` (normal progress UI) then continue.
- Resume a session (`enter`/`r`) → target-agent step (pre-selected by `resume <agent>`; else the
  last-resumed agent; else the session's own agent) → hands off to the existing
  `prepareResumeTarget` + `ExecAgentAndWatch`.
- Keys: `↑↓`/`jk` move · `enter`/`r` resume · `n` new Codex session · `space` preview · `/`
  search · `a` agent · `h` show/hide background · `d` delete (soft, confirmed) · `v`
  dense/sparse · `tab` all-projects · `q`/`esc` quit.

**Deferred to Stage B / follow-up:** empty current project currently shows a message rather
than jumping to all-projects (that view *is* Stage B); the `tab` scope toggle; and persisting
the view-mode on cancel (today it saves only on a committed resume). The interactive UX itself
is validated by running it in a real terminal (it can't be exercised headless).

### Stage B — all-projects browser **(built)**

- A `modeProjects` screen: the `ListProjects` rollup grouped by relative date buckets
  (Today · Yesterday · Previous 7 days · Previous 30 days · Older) by each project's latest
  activity, each row showing the project name, colored per-agent count chips, and relative
  time. `↵` drills into a project's session list (the Stage A list, scoped); `esc`/`tab`
  returns to the browser.
- **Scope toggle:** `tab` from the home session list opens the browser; `tab` (or `esc`)
  from the browser returns to the current project.
- **Empty current project → browser:** the picker opens directly in `modeProjects`.
- **Search:** `/` in the browser runs **session FTS across all projects** → a flat results
  list (agent · time · project · highlighted snippet); `r` resumes a hit, `space` previews it,
  `d` soft-deletes it, `a`/`v` filter and toggle density, `esc` back to the rollup. `p` filters
  the **project list by name**; in the rollup, `d` soft-deletes the highlighted **project**. (`/` in a drilled-in session list stays project-scoped FTS — same key, scope
  follows the view. This same screen, entered directly, *is* `specstory search`.)
- Header reflects scope: `project: <name>` vs `all projects`. The whole-index empty case
  (nothing indexed at all) prints a hint instead of opening an empty browser.

## Out of scope (here)

- Reconstruction / launch plumbing — unchanged (`prepareResumeTarget`, `ExecAgentAndWatch`).
- Index population / freshness — see [SESSIONS-DB.md](SESSIONS-DB.md). `resume` is one of the
  staleness-trigger occasions, handled in the warm-keeping thread.
