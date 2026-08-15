# Derived Sessions Index

`sessions.db` is a disposable SQLite/FTS5 index over native Codex CLI JSONL. It is not a transcript
store and is never authoritative.

## Location and lifecycle

The default path is `~/.local/share/csessions/sessions.db`, or
`$XDG_DATA_HOME/csessions/sessions.db` when `XDG_DATA_HOME` is set. `csessions reindex` creates or
incrementally refreshes it; `csessions reindex --force` reparses all native sessions.

Deleting the database is safe. The next reindex rebuilds it from `~/.codex/sessions`.

## Persisted content

Each session row includes identifiers, project metadata, timestamps, native path, native kind,
freshness fingerprint, and redacted searchable text. The FTS body contains only redacted user and
assistant conversation text. It excludes reasoning, tool arguments, tool output, and raw JSONL.

Chinese discovery uses deterministic search-only Han unigram/bigram tokens stored separately from
readable text. Snippets are generated from the readable redacted column and cannot expose token
noise or unredacted content.

The index version is part of the freshness fingerprint. A parser, redaction, classification, or
search representation change bumps that version so legacy rows are safely replaced.

Optional AI titles, summaries, and tags live in a separate `ai_metadata` table. That table stores
only validated model output, model and prompt versions, aggregate token usage, enrichment time,
and the source size/mtime/index-version fingerprint. It does not store the outbound conversation.
An unchanged fingerprint is skipped; a changed source or prompt version becomes eligible again.
Deleting `sessions.db` removes this metadata without touching native Codex JSONL.

## Visibility and deletion

Default list, project-count, and search queries include only interactive sessions. Background
`subagent`, `exec`, and unknown kinds require an explicit process-local TUI opt-in and remain visibly
labelled.

TUI deletion is a database tombstone. It removes searchable text and keeps the row hidden across
incremental or forced reindex, while leaving native JSONL untouched. Deleting the derived database
clears tombstones and restores all discoverable native sessions on the next reindex.

## Preview boundary

Preview never reads the FTS body as a transcript substitute. It opens the selected native JSONL on
demand, parses supported conversation events, redacts before display, and fails closed on an invalid
path or redaction error. Temporary raw reveal exists only in the running TUI model.

## Concurrency and recovery

Reindex enumerates the sole active Codex provider, skips unchanged fingerprints, parses with a
bounded worker pool, and serializes SQLite writes. WAL and busy-timeout settings support concurrent
read-only TUI access. Stale or incompatible derived state may be discarded and rebuilt; native
session files are never repaired, rewritten, or deleted.
