#!/usr/bin/env bash
# Cross-builds the radio binary for linux/amd64 and windows/amd64, and
# packages each with the example configs and a starter Lua script into
# dist/release/goradio-<version>-<os>-<arch>.{tar.gz,zip}.
#
# Usage: scripts/package-release.sh [version]
#   version defaults to "dev" (or override: scripts/package-release.sh v0.1.0)
set -euo pipefail

VERSION="${1:-dev}"
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ASSETS_DIR="$ROOT_DIR/scripts/release-assets"
RELEASE_DIR="$ROOT_DIR/dist/release"

rm -rf "$RELEASE_DIR"
mkdir -p "$RELEASE_DIR"

build_platform() {
  local goos="$1" goarch="$2" ext="$3"
  local name="goradio-${VERSION}-${goos}-${goarch}"
  local out_dir="$RELEASE_DIR/$name"
  mkdir -p "$out_dir"

  echo "==> building ${goos}/${goarch}"
  (cd "$ROOT_DIR" && GOOS="$goos" GOARCH="$goarch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "-s -w -X github.com/goradioserver/goradio/internal/version.Version=${VERSION}" -o "$out_dir/radio${ext}" ./cmd/radio)

  cp "$ROOT_DIR/configs/server.example.yaml" "$out_dir/"
  cp "$ROOT_DIR/configs/station.example.yaml" "$out_dir/"
  cp "$ASSETS_DIR/station.lua" "$out_dir/"
  cp "$ROOT_DIR/.luarc.json" "$out_dir/"
  cp -r "$ROOT_DIR/lua-types" "$out_dir/lua-types"

  sed \
    -e "s|{{VERSION}}|${VERSION}|g" \
    -e "s|{{GOOS}}|${goos}|g" \
    -e "s|{{GOARCH}}|${goarch}|g" \
    -e "s|{{BIN}}|./radio${ext}|g" \
    "$ASSETS_DIR/GETTING_STARTED.txt.tmpl" > "$out_dir/GETTING_STARTED.txt"

  if [ "$goos" = "windows" ]; then
    (cd "$RELEASE_DIR" && zip -qr "${name}.zip" "$name")
    echo "==> packaged $RELEASE_DIR/${name}.zip"
  else
    (cd "$RELEASE_DIR" && tar -czf "${name}.tar.gz" "$name")
    echo "==> packaged $RELEASE_DIR/${name}.tar.gz"
  fi
}

build_platform linux amd64 ""
build_platform windows amd64 ".exe"

echo "done"
