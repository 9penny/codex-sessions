#!/usr/bin/env bash
# Build Codex Sessions for supported Linux/WSL targets.
# Output goes to the first argument (absolute, or relative to the current directory; default: dist).
# Run from anywhere.

set -e

# Output path: absolute or relative to CWD (where you run the script), default dist
OUTPUT_PATH=${1:-dist}
# Version to embed in the binary; falls back to git tag or "dev"
VERSION="${2:-$(git describe --tags --always --dirty 2>/dev/null || echo "dev")}"
START_DIR="$(pwd)"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLI_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
# Create the output dir before canonicalizing it — `cd` into a not-yet-existing
# directory would fail the whole script on first run.
if [[ "$OUTPUT_PATH" = /* ]]; then
  DEST_DIR="$OUTPUT_PATH"
else
  DEST_DIR="$START_DIR/$OUTPUT_PATH"
fi
mkdir -p "$DEST_DIR"
DEST_DIR="$(cd "$DEST_DIR" && pwd)"

cd "$CLI_DIR"
rm -f "$DEST_DIR"/csessions_*

LDFLAGS="-s -w -X main.version=$VERSION"

# os goarch filename_arch
for target in \
  "linux amd64 x86_64" \
  "linux arm64 arm64"
do
  read -r os goarch filename_arch file_ext <<< "$target"
  out="$DEST_DIR/csessions_${os}_${filename_arch}${file_ext}"
  echo "Building $out..."
  CGO_ENABLED=0 GOOS="$os" GOARCH="$goarch" go build -ldflags="$LDFLAGS" -o "$out" .
done

echo "Done. Binaries in $DEST_DIR"
