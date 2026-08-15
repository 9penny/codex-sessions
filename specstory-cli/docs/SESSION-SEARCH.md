# Session Search

`csessions search [query...]` opens the shared TUI directly in all-projects search with the input
focused. Command arguments seed the initial query.

## Search representation

The derived index stores two redacted representations:

- readable user/assistant text for English, technical-token matching, and snippets;
- deterministic Han unigram/bigram tokens in a search-only field for useful Chinese substring
  matching.

Queries use the same deterministic normalization. Search-only tokens never appear in snippets or
preview, and reasoning, tool arguments, and tool output are never searchable.

## Interaction

- `enter` or `r`: resume the highlighted native Codex session
- `n`: start a new Codex session in the highlighted session's project
- `space`: open masked native preview
- `h`: include or exclude visibly labelled background sessions
- `/`: edit the query
- `q` or `esc`: quit or return

Search begins at two characters, runs asynchronously with cancellation, and returns newest-first
session rows. Highlighted snippets are fetched only for the visible window. Default results exclude
background sessions; the process-local `h` state changes both browse and search visibility.

See [RESUME-TUI.md](RESUME-TUI.md) for the shared model and [SESSIONS-DB.md](SESSIONS-DB.md) for
the derived-data boundary.
