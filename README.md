# Codex Sessions

Codex Sessions is a local-only terminal browser for Codex CLI history. It indexes the native
`~/.codex/sessions` JSONL files into a disposable, redacted SQLite database so you can browse,
search, preview, resume, or start sessions without an account or cloud service.

> This is a personal-first open-source fork under active development. The `dev` branch is the
> integration branch; `v0.1.0` is the first supported Linux/WSL release.

## Safety model

- Native Codex JSONL files are authoritative and are never modified.
- The SQLite index contains redacted user/assistant text only. Reasoning, tool arguments, and
  tool output are excluded.
- Preview reads native JSONL on demand, masks secrets by default, and never caches raw text.
- Uppercase `R` reveals only the current preview in process memory; navigating away clears it.
- v0.1 has no login, sync, analytics, telemetry, version check, or other outbound path.
- Background `subagent`, `exec`, and unknown-source sessions are hidden by default.

## Install

- Linux or WSL, including SSH terminals
- Codex CLI available as `codex`

```bash
# Review the installer before running it, then install the latest release.
curl -fsSLO https://raw.githubusercontent.com/9penny/codex-sessions/v0.1.0/install.sh
less install.sh
bash install.sh
```

The installer downloads the matching Linux `amd64` or `arm64` archive, verifies it against the
published SHA-256 checksums, and installs `csessions` in `/usr/local/bin`. To use another writable
directory, run `INSTALL_DIR="$HOME/.local/bin" bash install.sh`.

To build from source instead, install Go 1.26.5 and run:

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

# Inspect enrichment size locally: no API request and no metadata write
csessions enrich --dry-run --limit 10
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

Optional AI enrichment is never automatic. It requires the explicit `csessions enrich --yes`
command, sends only fail-closed redacted user/assistant text, and stores generated titles,
summaries, and tags only in the disposable database. Read the
[enrichment privacy and usage guide](specstory-cli/docs/V0.2-ENRICHMENT.md) before enabling it.

## Development

The ordered work and release gates are in the
[v0.2 implementation plan](specstory-cli/docs/V0.2-IMPLEMENTATION-PLAN.md). The completed v0.1
scope remains in the [v0.1 plan](specstory-cli/docs/V0.1-IMPLEMENTATION-PLAN.md). Run the standard
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
