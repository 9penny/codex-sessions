# Archived upstream implementation inventory

Codex Sessions keeps inherited SpecStory source and documents in place as unsupported reference
material. “Archived” means logically unreachable from the `csessions` product; it does not mean
moved, deleted, vendored, or exempt from compilation and tests.

## Active v0.1 surface

- Commands: `resume`, `search`, `reindex`, `version`, and `help`
- Provider: `pkg/providers/codexcli`
- Local index and TUI: `pkg/sessionindex` and the active local paths under `pkg/cmd`
- Native input: `~/.codex/sessions`
- Local XDG configuration, data, and cache paths under `csessions`

The factory registry exposes exactly `codex`. The command-tree test fixes the complete supported
command set, and network-isolation tests audit supported subprocesses.

## Provider archive

The following packages remain in their original locations but are not imported by the active
factory registry:

- `pkg/providers/antigravitycli`
- `pkg/providers/claudecode`
- `pkg/providers/copilotide`
- `pkg/providers/cursorcli`
- `pkg/providers/cursoride`
- `pkg/providers/deepseektui`
- `pkg/providers/droidcli`
- `pkg/providers/geminicli`
- `pkg/providers/vscode`

Their dependencies remain in `go.mod` where required to compile and test the archive. Dependency
reduction is intentionally deferred.

## Feature archive

Inherited implementations for these areas remain in place but are not registered in the executable
command tree or navigation:

- account login/logout and SpecStory Cloud sync
- analytics, PostHog, OpenTelemetry, and version checks
- run/watch autosave and markdown export flows
- Lore and cloud skill management
- cross-agent reconstruction and portability
- inherited check/list/sync provider utilities
- provenance experiments and IDE integration support

This includes implementation under `pkg/cloud`, `pkg/analytics`, `pkg/telemetry`, `pkg/skills`,
`pkg/provenance`, retained command files under `pkg/cmd`, and the gated inherited functions in
`main.go`. Keeping a symbol buildable does not make it reachable: only `createLocalCommandTree`
defines the executable surface.

## Documentation archive

Documents not listed as current in [docs/README.md](README.md), plus the repository-level `lore/`
and `workthreads/` trees, are retained upstream design/history. They may mention SpecStory commands,
cloud endpoints, other agents, old storage paths, or behavior that `csessions` does not support.

## Archive rules

- Do not add archived commands or providers to `createLocalCommandTree` or the active registry.
- Do not call archived network/account initialization from active startup or shutdown.
- Keep archived packages compiling and retain their focused tests.
- Do not move or delete archive files merely for cleanup.
- Update this inventory if a future version deliberately reactivates or removes an area.
- Preserve Apache-2.0 attribution and upstream Git history.
