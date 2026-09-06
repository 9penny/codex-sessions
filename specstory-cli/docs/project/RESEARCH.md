# Codex Session Management Notes

Updated: 2026-08-16

## Document status

This is the historical research record that led to the Codex Sessions fork. Measurements,
candidate comparisons, and the SpecStory trial below describe the state observed on 2026-08-15;
they are retained as evidence, not as the current roadmap.

The implementation work is complete through
[`v0.3.1`](https://github.com/9penny/codex-sessions/releases/tag/v0.3.1). Current behavior and the
feedback-driven backlog are summarized in [ROADMAP.md](ROADMAP.md); detailed release evidence lives in
the implementation repository.

## Current outcome

- SpecStory was selected as the implementation base and forked as
  [9penny/codex-sessions](https://github.com/9penny/codex-sessions).
- The supported command is `csessions`; the product is Codex-only, local-first, and Linux/WSL-first.
- `v0.1.0` shipped the safe redacted index, masked native preview, bilingual search, background
  session filtering, correct-directory resume, and new-session launch.
- `v0.2.1` shipped explicit optional AI enrichment. `v0.2.2` hardened database permissions, and
  `v0.2.3` made enrichment project-aware by default.
- `v0.3.1` shipped fail-closed reconciliation for sessions permanently deleted by Codex and
  clarified the difference between Codex `/delete` and csessions soft-delete.
- Normal commands remain network-free. Enrichment is explicit, confirmed with `--yes`, bounded,
  and receives only redacted user/assistant text.
- No new product milestone is committed. The current next action is normal use and collection of
  concrete friction before defining v0.4.

## Original goal

Find and adopt a relatively mature solution for managing local Codex CLI sessions. Avoid building a new product unless existing tools cannot satisfy the core workflow.

The primary problem is not long-term knowledge management. It is replacing the poor `/resume` experience with a clear, SSH-friendly session browser and launcher.

## Actual usage pattern

- Codex CLI sessions are mainly long-running work for projects and VPS operations.
- Disposable questions are usually handled in the web app instead of Codex CLI.
- Sessions live primarily in the home WSL environment.
- Other computers connect back to this WSL over SSH.
- Cross-device synchronization, user accounts, and team features are not needed.
- Exact transcript continuity is less important than quickly recognizing the correct project and session.

## Historical session inventory findings

- Before cleanup, `~/.codex/sessions` contained 232 sessions and used about 248 MB.
- A large amount of noise came from abandoned `cogito` projects, subagents, empty sessions, and short sessions.
- 129 Codex session files related to `/home/jy/cogito` and `/home/jy/cogito2` were moved to the system trash:
  - `cogito`: 126 sessions
  - `cogito2`: 3 sessions
- Both project directories were already absent when cleanup was performed.
- The deleted session files remain recoverable from the system trash.
- Re-counted on 2026-08-15: 104 JSONL files, about 209 MB.
- Classification from the first `session_meta` record:
  - 69 normal interactive CLI sessions
  - 27 subagent sessions
  - 8 `exec` sessions
- 101 files contain at least one `user_message`; 3 contain none.

## Original first-priority workflow

The preferred initial product is a local terminal UI with this flow:

1. Scan local Codex session files.
2. Hide subagent, empty, and archived sessions by default.
3. Group sessions by project.
4. Generate a useful title, summary, and task tags for every session.
5. Preview the complete conversation.
6. Search across conversations, generated summaries, and tags.
7. Press Enter to run `codex resume <session-id>`.
8. Start a new Codex session in the selected project directory.

## Original desired information model

- The first-level grouping is the session working directory (`cwd`).
- One working directory is treated as one project.
- Related content in different directories may initially appear as separate projects.
- One original Codex session remains one UI entry.
- A session containing multiple tasks is not split into virtual sessions.
- Multiple tasks are represented by AI-generated tags.
- Each session should have:
  - One overall title
  - One short summary describing what actually happened
  - Multiple task or purpose tags
  - Project path
  - Last activity time
  - Original session ID

## Original AI processing requirements

This section records the initial preference before implementation and model evaluation. It is
superseded by the shipped v0.2 contract: enrichment is explicit rather than automatic, the default
model came from synthetic endpoint evaluation, model selection remains configurable, and current
configuration uses `CSESSIONS_OPENAI_*` variables or the private `ai.toml` file documented by the
implementation repository.

- Initial setup should process all existing sessions in one batch.
- Results must be cached locally.
- New or changed sessions should be processed incrementally.
- The default summarization model must be pinned to the exact model `gpt-5.4-mini`.
- `gpt-5.3-codex-spark` may be offered as a manual experimental option.
- Do not inherit a moving default or `latest` alias.
- Do not silently fall back to another model if the selected model is unavailable.
- Low reasoning effort is preferred for title, summary, and tag generation.
- Session content must pass through secret detection and redaction before being sent for AI processing.
- The user's private VPS-hosted API successfully completed structured Responses API trials with `gpt-5.4-mini` and `gpt-5.3-codex-spark`.
- `gpt-5.4-mini` produced the cleaner result at similar latency and is the default. The private API's actual billing rate is unknown.
- Do not store the API endpoint or credentials in this requirements document.

## Search exploration

Desired end state includes semantic search.

The private API's support for `/v1/embeddings` is currently unknown. This is intentionally deferred.

Possible paths:

- If embeddings are supported, build a local vector index from redacted conversation chunks.
- If embeddings are not supported, start with SQLite full-text search over raw text, AI summaries, and task tags.
- Do not make embeddings support a blocker for the initial session browser.

## Original priority tradeoff

The user chose "find sessions quickly" over "manage sessions in detail" for the initial version.

First priority:

- Project grouping
- AI-generated titles and summaries
- Complete transcript preview
- Search
- Resume and new-session launch

Lower priority for later:

- Manual title and summary editing
- Manual tags
- Custom folders
- Favorites and pinning
- Archive management
- Deletion from inside the UI
- File-change and tool-operation views

## Complexity constraints

Do not introduce these in the initial solution:

- User accounts
- Cross-device synchronization
- Team collaboration
- Mandatory cloud storage
- A second canonical copy of Codex session data
- Complex manual organization before the tool becomes useful

The source of truth remains the native Codex session files under `~/.codex/sessions`.

## Historical existing-tool direction

Do not assume a custom implementation is required.

Previously considered tools include `showagent`, `cxresume`, `codex-sessions`, and SpecStory. Lightweight session browsers cover grouping, preview, search, and resume, but AI-generated summaries, task tags, redaction, and pinned-model processing still need to be checked carefully.

That evaluation is recorded below. It resolves the base-tool question in favor of a local-only SpecStory trial.

## Existing-tool evaluation (2026-08-15)

### Decision

Use [SpecStory CLI](https://github.com/specstoryai/getspecstory) as the leading base for a short local trial and, if the trial is acceptable, extend it narrowly. Do not start a standalone session manager.

SpecStory is the only evaluated tool that already combines all of the difficult non-AI parts in one maintained codebase:

- Project browser with sessions grouped by project
- Full-transcript terminal preview
- Full-text search across all projects and complete conversation bodies
- Resume into Codex from a selected result
- A rebuildable SQLite cache while native Codex JSONL remains authoritative
- Full initial indexing and incremental refresh based on file size, mtime, and parser version
- An existing `betterleaks` redaction pipeline for saved or synced material that can be reused before future AI calls

The required extension is still material but bounded: hide subagent/exec sessions by default, add cached AI title/summary/tags, expose those fields in browse/search, and add “new Codex session in selected project.” This is much smaller than building parsing, indexing, preview, TUI navigation, and resume plumbing from scratch.

### Real-corpus validation

SpecStory v2.9.0 was downloaded from its official GitHub release and its Linux x86-64 archive matched the published SHA-256 (`e94f7259…81badd4`). It was run with an isolated temporary HOME and hard-linked read-only copies of the current Codex session files; it was not installed and did not modify `~/.codex`.

Results:

- Parsed and indexed 101 of 104 JSONL files successfully in about 1.7 seconds.
- Produced a 14.0 MB `sessions.db` covering 35 projects.
- A second run recognized all 101 entries as unchanged and completed in about 1.0 second.
- The TUI launched correctly in a PTY, showed the all-projects browser, grouped projects by relative date, and exited cleanly.
- The 3 files it omitted are exactly the files with no `user_message`.
- It did **not** hide subagents: the 101 indexed entries include non-empty subagent sessions. The current database schema does not retain Codex `source`/`thread_source`, so this needs a parser/schema/filter change rather than configuration.
- Its index and preview copied raw tool output, including likely credentials and connection information; the existing redaction path does not protect `sessions.db`.
- English and technical unique-token search passed all sampled cases, while Chinese substring search failed all sampled cases because continuous Chinese text is indexed as unsuitable FTS5 tokens.
- A launcher test invoked the correct `codex resume <id>` command but used SpecStory's launch directory instead of the session's original `cwd`.
- Full previews were complete in the sampled sessions. An 80x24 PTY was usable; 60x20 was marginal.

### Requirement comparison

| Candidate | Strong fit | Important gap | Assessment |
|---|---|---|---|
| [SpecStory CLI](https://github.com/specstoryai/getspecstory) | Project rollup, complete preview, FTS5 over full bodies, resume, native-source cache, incremental indexing, redaction | No AI title/summary/tags; includes subagents; no new-session action in selected project | Best base |
| [coding-agent-search (`cass`)](https://github.com/Dicklesworthstone/coding_agent_session_search) | Most mature search/indexing; rich full-conversation view; BM25 plus opt-in local MiniLM semantic search | Search tool rather than Codex session launcher; no Codex resume/new-session workflow; no required AI metadata | Strong search reference or fallback, not the primary UI |
| [showagent](https://github.com/aytzey/showagent) | Excellent lightweight workspace-grouped picker; static local binary; native resume; active project | TUI search/preview is based on first/latest messages rather than full transcript; no AI metadata or new-session action | Best minimal picker, but too far from discovery requirements |
| [cxresume](https://github.com/lingtaolf/cxresume) | Codex-specific split preview, content search, resume, and new session | No true all-project grouping or AI metadata; project mode writes `.cxresume_sessions`; little activity since 2025 | Useful small baseline, weaker base |
| [recall](https://github.com/zippoxer/recall) | Fast full-text search and resume across several agents | Search-first rather than project browser; no summaries/tags or selected-project new session | Good narrow utility |
| [cdxresume](https://github.com/sasazame/cdxresume) | Project browsing and new-session launch | Compatibility logic is tied to old Codex format eras; lacks the required search/enrichment pipeline | Not preferred |
| [SpecStory Cloud](https://docs.specstory.com/integrations/codex-cli) | Searchable synced history and Markdown export | Cloud/second-copy direction is unnecessary; AI metadata and pinned private model are not supplied by the local picker | Use local CLI only, not cloud |

### Maturity snapshot

Snapshot from GitHub on 2026-08-15:

- SpecStory: 1,301 stars, Apache-2.0, v2.9.0 released 2026-08-12, pushed 2026-08-14.
- `cass`: 1,062 stars, v0.6.24 released 2026-08-11, pushed 2026-08-14.
- `showagent`: 40 stars, MIT, v0.11.0 released 2026-07-10, pushed 2026-08-10.
- `cxresume`: 13 stars, MIT, v1.0.1 released 2025-10-10, last pushed 2025-10-14.
- `recall`: 194 stars, MIT, v0.5.0 released 2026-01-13, last pushed 2026-01-14.
- `cdxresume`: 5 stars, MIT, v1.0.0 released 2026-05-08, pushed 2026-08-01.

Star counts are only a weak signal; the recommendation primarily follows verified behavior and architectural fit.

### Privacy and configuration caveats for a trial

SpecStory is local-first, but its generated default configuration enables anonymous usage analytics and version checks, and cloud sync becomes enabled when logged in. A local-only trial should explicitly set:

```toml
[cloud_sync]
enabled = false

[analytics]
enabled = false

[version_check]
enabled = false
```

Do not run `specstory sync`, `specstory run`, or `specstory watch` during the first trial. Use only `reindex`, `resume`, and `search`; these operate from native session files and the derived local `~/.specstory/sessions.db` cache without requiring project-local Markdown history.

### Historical extension proposal

The following list was the proposed fork boundary before implementation. It is preserved to show
how the shipped product was derived; [ROADMAP.md](ROADMAP.md) is authoritative for current behavior and
future candidates.

If the local trial succeeds, keep the extension inside or immediately alongside SpecStory's existing index rather than introducing another canonical session store:

1. Preserve Codex `source`, `thread_source`, and `originator` during enumeration/indexing.
2. Default-filter subagent, `exec`, empty, and archived sessions; provide an explicit “show hidden” toggle.
3. Add derived enrichment keyed by `(agent, session_id, source fingerprint, enrichment version)`:
   - AI title
   - Short factual summary
   - Multiple task/purpose tags
   - Model name and processing status/error
4. Redact before enrichment calls; store only the redacted request hash and derived result, not another raw transcript copy.
5. Pin enrichment to `gpt-5.4-mini` with low reasoning and fail closed if the selected model is unavailable; expose `gpt-5.3-codex-spark` only as a manual experiment.
6. Add `n` in a project/session view to run a new `codex` process with that project's cwd.
7. Include title, summary, and tags in FTS; defer embeddings until the private API is tested.

### Outcome of the proposed work

The staged fork plan was executed. Index privacy, Chinese search, source classification,
correct-directory resume, new-session launch, optional AI metadata, project-aware enrichment,
private database permissions, and native-deletion reconciliation all shipped through `v0.3.1`.

## Remaining research questions

These questions are intentionally dormant until real use makes them relevant:

- Does the configured private endpoint expose a suitable embeddings model, and would semantic
  search materially outperform current bilingual FTS plus AI metadata?
- Do provider-reported token totals require additional accounting or cost controls under the
  user's subscription?
- Should background index warming reconcile native deletion so `resume` needs no explicit reindex?
- What targeted recovery UX should restore a csessions soft-delete tombstone without rebuilding the
  entire derived database?
- Does the flat project browser need directory-subtree navigation for home-directory workflows?
