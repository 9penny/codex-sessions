# Codex Sessions

Codex Sessions is a local-only terminal browser for Codex CLI history. It indexes the native
`~/.codex/sessions` JSONL files into a disposable, redacted SQLite database so you can browse,
search, preview, resume, or start sessions without an account or cloud service.

> This is a personal-first open-source fork under active development. The `dev` branch is the
> integration branch; `v0.1.0` has not been tagged yet.

## Safety model

- Native Codex JSONL files are authoritative and are never modified.
- The SQLite index contains redacted user/assistant text only. Reasoning, tool arguments, and
  tool output are excluded.
- Preview reads native JSONL on demand, masks secrets by default, and never caches raw text.
- Uppercase `R` reveals only the current preview in process memory; navigating away clears it.
- v0.1 has no login, sync, analytics, telemetry, version check, or other outbound path.
- Background `subagent`, `exec`, and unknown-source sessions are hidden by default.

## Requirements and build

- Linux or WSL, including SSH terminals
- Go 1.26.5
- Codex CLI available as `codex`

```bash
git clone https://github.com/9penny/codex-sessions.git
cd codex-sessions/specstory-cli
go build -o bin/csessions .
./bin/csessions version
```

## Usage

```bash
# Browse the current project, preview, resume, or start a new session
csessions resume

# Search all indexed projects; Chinese and English text are supported
csessions search "query"

# Incrementally rebuild the disposable index
csessions reindex

# Reparse every native Codex session
csessions reindex --force
```

Core TUI keys:

- `enter` or `r`: resume the selected session in its recorded directory
- `n`: start a new Codex session in the selected project directory
- `space`: open masked preview; `R` temporarily reveals it
- `/`: search; `h`: show or hide background sessions
- `q` or `esc`: quit or go back

If a recorded directory is missing, launch is blocked with an explanation instead of silently
using a different directory.

## Local storage

Codex Sessions respects the XDG base-directory variables:

| Purpose | Default path |
|---|---|
| Configuration | `~/.config/csessions/` |
| Derived index | `~/.local/share/csessions/sessions.db` |
| Disposable cache | `~/.cache/csessions/` |

Deleting the derived database is safe; `csessions reindex` rebuilds it from native JSONL.

## Development

The ordered work and release gates are in the
[v0.1 implementation plan](specstory-cli/docs/V0.1-IMPLEMENTATION-PLAN.md). Run the standard
gate from `specstory-cli/`:

```bash
gofmt -w .
go vet ./...
go test ./...
go build -o bin/csessions .
```

The fork intentionally keeps inherited non-Codex source at its original paths as unsupported
reference material. It is not registered or reachable from `csessions`; see the
[archive inventory](specstory-cli/docs/ARCHIVED-UPSTREAM.md).

## License and upstream

Codex Sessions is forked from [SpecStory](https://github.com/specstoryai/getspecstory) and
retains its Apache-2.0 license, attribution, and Git history. See [LICENSE.txt](LICENSE.txt).
