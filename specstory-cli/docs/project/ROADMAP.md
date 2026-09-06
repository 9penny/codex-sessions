# Codex Sessions Status and Roadmap

Updated: 2026-08-16

Status: `v0.3.1` is the current supported Linux/WSL release. No implementation milestone is
currently open; the next phase is normal use and feedback collection.

## Product boundary

`Codex Sessions` is a personal-first, limited open source terminal application for discovering and
resuming local Codex CLI sessions. The command is `csessions`; the implementation is maintained at
[9penny/codex-sessions](https://github.com/9penny/codex-sessions).

Native Codex JSONL under `~/.codex/sessions` is the source of truth. csessions builds a disposable,
redacted SQLite index under the XDG data directory. Linux and WSL, including SSH terminals, are the
supported environments. Accounts, collaboration, mandatory cloud storage, macOS, native Windows,
and upstream contribution compatibility are not current goals.

## Shipped behavior

- Browse interactive Codex sessions by project and activity.
- Hide subagent, `exec`, empty, and unknown-source sessions by default, with an explicit visibility
  toggle.
- Search redacted user/assistant conversation text in English, technical identifiers, and Chinese.
- Preview native JSONL on demand with secrets masked by default; uppercase `R` reveals only the
  active in-memory preview.
- Resume a selected session or start a new Codex process in the recorded project directory.
- Optionally generate cached AI titles, summaries, and tags through an explicit
  `csessions enrich` command.
- Default enrichment to the current project; require `--all` for the complete indexed library or
  `--project PATH` for an explicit project.
- Soft-delete an entry from the disposable index with `d`, without changing native JSONL.
- Reconcile permanent Codex `/delete` operations during a complete successful
  `csessions reindex`; incomplete scans never infer deletion.

## Safety invariants

- Native Codex JSONL is never modified or deleted by csessions.
- The searchable index stores only fail-closed redacted user/assistant text. Reasoning, tool
  arguments, and tool output are excluded.
- Raw preview text is read only when requested, masked by default, and never cached or logged.
- Every command except explicit live enrichment remains network-free.
- Enrichment sends only redacted user/assistant text after `--yes` confirmation, uses `store: false`
  and no tools, and stores results only as disposable derived metadata.
- API credentials come from `CSESSIONS_OPENAI_API_KEY` or a private mode-0600 config file under
  `~/.config/csessions/ai.toml`; they are never accepted as command-line flags or stored in SQLite.
- A failed, partial, panicking, or interrupted native-session scan cannot remove indexed rows.
- Deleting the derived database remains the complete recovery path; the next reindex rebuilds it
  from native sessions.

## Release record

| Release | Date | Outcome |
|---|---|---|
| [`v0.1.0`](https://github.com/9penny/codex-sessions/releases/tag/v0.1.0) | 2026-08-15 | Safe local-only Codex browser: redacted index, masked native preview, bilingual search, correct-cwd resume, new-session launch, and hidden background sessions. |
| [`v0.2.1`](https://github.com/9penny/codex-sessions/releases/tag/v0.2.1) | 2026-08-15 | Optional explicit AI enrichment with strict redaction, bounded batches, derived metadata, search/TUI integration, and no background network activity. |
| [`v0.2.2`](https://github.com/9penny/codex-sessions/releases/tag/v0.2.2) | 2026-08-16 | Tightened the derived index directory and SQLite files to private permissions. |
| [`v0.2.3`](https://github.com/9penny/codex-sessions/releases/tag/v0.2.3) | 2026-08-16 | Made enrichment project-aware by default and added explicit `--project` and `--all` scopes. |
| [`v0.3.1`](https://github.com/9penny/codex-sessions/releases/tag/v0.3.1) | 2026-08-16 | Safely reconciled native Codex deletion, clarified deletion UX, and exposed missing TUI key hints. |

The inherited fork history already contained unrelated `v0.2.0` and `v0.3.0` tags. They were
preserved rather than overwritten, which is why the Codex Sessions releases start at `v0.2.1` and
`v0.3.1` for those minor lines.

Detailed implementation plans, release gates, privacy guidance, and synthetic test evidence live
in the implementation repository's
[`specstory-cli/docs`](https://github.com/9penny/codex-sessions/tree/dev/specstory-cli/docs)
directory.

## Current operating phase

Use `v0.3.1` normally before defining another milestone. Record concrete failures or repeated
friction rather than selecting features from the backlog in advance.

The current objective assessment is recorded in
[V0.3.1-DESIGN-REVIEW.md](V0.3.1-DESIGN-REVIEW.md). Its conclusion is that the core architecture is
sound; the highest-value candidates are responsive 80-column help, targeted soft-delete recovery,
and clearer lifecycle/budget feedback.

Dogfood should pay particular attention to:

- whether Codex `/delete` followed by `csessions reindex` removes the intended entry;
- whether index soft-delete needs a targeted undo/restore action;
- whether manually running reindex after native deletion is meaningfully inconvenient;
- whether the flat project browser becomes difficult to navigate from a broad directory such as
  the home directory;
- whether AI titles and summaries materially improve discovery and remain accurate;
- whether current project-scoped enrichment matches real command expectations.

## Candidate backlog

These are options, not a committed v0.4 scope. Promote an item only after observed use justifies it.

### Likely reliability and UX candidates

- Reconcile native deletions during the background index warm used by `resume`, with the same
  complete-scan safety contract as explicit reindex.
- Add targeted undo or restoration for csessions soft-delete tombstones without requiring a full
  database rebuild.
- Add a directory-tree or subtree project view if flat project filtering proves insufficient.
- Improve diagnostics for missing project directories and stale native paths when real failures
  recur.

### Deferred AI and discovery work

- Investigate provider token-accounting differences, pricing, and additional cost controls only if
  subscription limits or unexpected usage become a real constraint.
- Evaluate embeddings and semantic/vector search only if bilingual FTS plus generated metadata
  repeatedly fails to find known sessions.
- Consider automatic/background enrichment only with an explicit network, privacy, cancellation,
  and budget design. It must never become an implicit side effect of normal commands.
- Consider favorites, folders, manual metadata editing, or archive UI only if project grouping and
  search do not cover daily use.

### Explicit non-goals for now

- accounts, sync, teams, and collaboration;
- mandatory cloud services or a second canonical transcript store;
- macOS, native Windows, and package-manager distribution;
- compatibility work whose primary purpose is upstream contribution.

## Development and rollback protocol

- Make observable behavior changes test-first at the narrowest stable boundary.
- Use synthetic, sanitized fixtures; real-corpus validation records aggregate counts only.
- Develop each coherent slice on an `agent/*` branch and merge through a pull request into `dev`.
- Require focused tests, the full Go suite, lint, build, release snapshot, and installer validation
  in proportion to risk.
- Never overwrite inherited or published tags. Publish only from a verified `dev` commit.
- Preserve native JSONL as authoritative so the disposable database can always be rebuilt.

This directory records cross-version decisions and historical research alongside the implementation.
The source code and release records in this repository are authoritative for current behavior and
release evidence.
