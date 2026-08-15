# Codex Sessions CLI

This Go module builds `csessions`, a local-only TUI for browsing and resuming native Codex CLI
sessions. See the [repository README](../README.md) for installation, usage, privacy guarantees,
and storage paths.

## Commands

The complete supported command tree is:

```text
csessions
├── resume
├── search
├── reindex
├── version
└── help
```

No provider other than Codex is registered. Inherited account, cloud, sync, watch, autosave,
analytics, telemetry, Lore, cross-agent reconstruction, and other provider implementations are
kept in place only as an unsupported archive; see [ARCHIVED-UPSTREAM.md](docs/ARCHIVED-UPSTREAM.md).

## Build and verify

Go 1.26.5 is required.

```bash
go build -o bin/csessions .
go vet ./...
go test ./...
```

The native `~/.codex/sessions` tree is read-only source data. Tests must use synthetic fixtures
and isolated XDG/HOME directories; never copy real transcript text or credentials into fixtures,
logs, snapshots, or issues.

## Design documents

- [v0.1 implementation plan](docs/V0.1-IMPLEMENTATION-PLAN.md)
- [resume and TUI behavior](docs/RESUME-TUI.md)
- [search behavior](docs/SESSION-SEARCH.md)
- [derived index](docs/SESSIONS-DB.md)
- [archived upstream inventory](docs/ARCHIVED-UPSTREAM.md)

This module retains the upstream module path to preserve history and avoid a disruptive import
rewrite before v0.1. The project remains licensed under Apache-2.0; see
[LICENSE.txt](../LICENSE.txt).
