#!/usr/bin/env bash

set -euo pipefail

REPO="9penny/codex-sessions"
BINARY_NAME="csessions"
INSTALL_DIR=${INSTALL_DIR:-/usr/local/bin}

os=$(uname -s)
case "$os" in
  Linux) OS="Linux" ;;
  *)
    printf 'Unsupported operating system: %s (Codex Sessions supports Linux and WSL)\n' "$os" >&2
    exit 1
    ;;
esac

arch=$(uname -m)
case "$arch" in
  x86_64|amd64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    printf 'Unsupported architecture: %s\n' "$arch" >&2
    exit 1
    ;;
esac

if [[ -z ${VERSION:-} ]]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" |
    sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' |
    head -n 1)
fi
if [[ ! $VERSION =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  printf 'Could not resolve a valid Codex Sessions release version.\n' >&2
  exit 1
fi

FILENAME="CodexSessions_${OS}_${ARCH}.tar.gz"
DOWNLOAD_BASE_URL=${DOWNLOAD_BASE_URL:-"https://github.com/$REPO/releases/download/$VERSION"}
tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/csessions-install.XXXXXX")
cleanup() {
  if [[ -n ${tmp_dir:-} && -d $tmp_dir && $tmp_dir == *csessions-install.* ]]; then
    rm -rf -- "$tmp_dir"
  fi
}
trap cleanup EXIT

curl -fsSL "$DOWNLOAD_BASE_URL/$FILENAME" -o "$tmp_dir/$FILENAME"
curl -fsSL "$DOWNLOAD_BASE_URL/checksums.txt" -o "$tmp_dir/checksums.txt"
(
  cd "$tmp_dir"
  awk -v file="$FILENAME" '$2 == file { print; found=1 } END { exit !found }' \
    checksums.txt > checksums.selected
  sha256sum -c checksums.selected
  tar -xzf "$FILENAME"
  test -x "$BINARY_NAME"
)

if [[ -d $INSTALL_DIR && -w $INSTALL_DIR ]]; then
  install -m 0755 "$tmp_dir/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
else
  sudo install -d -m 0755 "$INSTALL_DIR"
  sudo install -m 0755 "$tmp_dir/$BINARY_NAME" "$INSTALL_DIR/$BINARY_NAME"
fi

printf 'Installed Codex Sessions %s to %s/%s\n' "$VERSION" "$INSTALL_DIR" "$BINARY_NAME"
printf 'Run: %s --version\n' "$BINARY_NAME"
