#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
goreleaser="$repo_root/.goreleaser.yml"
workflow="$repo_root/.github/workflows/release.yml"
installer="$repo_root/install.sh"

require_fixed() {
  local file=$1
  local text=$2
  if ! grep -Fq -- "$text" "$file"; then
    printf 'release config: %s is missing %q\n' "${file#"$repo_root/"}" "$text" >&2
    return 1
  fi
}

reject_regex() {
  local file=$1
  local pattern=$2
  if grep -Eiq -- "$pattern" "$file"; then
    printf 'release config: %s contains forbidden pattern %q\n' "${file#"$repo_root/"}" "$pattern" >&2
    return 1
  fi
}

require_fixed "$goreleaser" 'project_name: CodexSessions'
require_fixed "$goreleaser" 'binary: csessions'
require_fixed "$goreleaser" 'goos:'
require_fixed "$goreleaser" '      - linux'
require_fixed "$goreleaser" 'checksum:'
reject_regex "$goreleaser" 'SpecStoryCLI|binary:[[:space:]]+specstory|POSTHOG|darwin|windows'

require_fixed "$workflow" "      - 'v*'"
require_fixed "$workflow" 'args: release --clean'
require_fixed "$workflow" 'GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}'
reject_regex "$workflow" 'specstory-cli/v|POSTHOG|SLACK|HOMEBREW|specstoryai|curl[[:space:]]+-X[[:space:]]+POST'

require_fixed "$installer" 'REPO="9penny/codex-sessions"'
require_fixed "$installer" 'BINARY_NAME="csessions"'
require_fixed "$installer" 'FILENAME="CodexSessions_${OS}_${ARCH}.tar.gz"'
require_fixed "$installer" 'DOWNLOAD_BASE_URL=${DOWNLOAD_BASE_URL:-"https://github.com/$REPO/releases/download/$VERSION"}'
require_fixed "$installer" 'checksums.txt'
reject_regex "$installer" 'SpecStoryCLI|BINARY_NAME="specstory"|specstoryai/getspecstory'

printf 'release configuration contract: pass\n'
