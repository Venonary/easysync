#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT_DIR"

TAG=${1:-$(date +%Y%m%d%H%M)}
OUTDIR=release_artifacts
rm -rf "$OUTDIR" && mkdir -p "$OUTDIR"

echo "Building frontend..."
npm --prefix frontend ci
npm --prefix frontend run build

MATRIX=(
  "windows amd64 zip .exe"
  "windows arm64 zip .exe"
  "linux amd64 tar.gz ''"
  "linux arm64 tar.gz ''"
  "darwin amd64 tar.gz ''"
  "darwin arm64 tar.gz ''"
)

for entry in "${MATRIX[@]}"; do
  read -r GOOS GOARCH ARCHIVE EXT <<<"$entry"
  echo "Building for ${GOOS}/${GOARCH}..."
  BIN_NAME=easysync
  if [ "$GOOS" = "windows" ]; then BIN_NAME=${BIN_NAME}${EXT}; fi
  GOOS=$GOOS GOARCH=$GOARCH go build -o build/${BIN_NAME} .

  PKG_DIR=tmp_pkg
  rm -rf $PKG_DIR && mkdir -p $PKG_DIR
  cp build/${BIN_NAME} $PKG_DIR/
  mkdir -p $PKG_DIR/frontend
  cp -r frontend/dist/* $PKG_DIR/frontend/
  if [ -d data ]; then cp -r data $PKG_DIR/; fi
  cp README.md $PKG_DIR/ || true

  NAME=easysync-${TAG}-${GOOS}-${GOARCH}
  if [ "$ARCHIVE" = "zip" ]; then
    zip -r "$OUTDIR/${NAME}.zip" -j "$PKG_DIR"/* || true
    (cd $PKG_DIR && zip -r "../$OUTDIR/${NAME}.zip" .)
  else
    tar -czf "$OUTDIR/${NAME}.tar.gz" -C $PKG_DIR .
  fi
  echo "Packaged $OUTDIR/${NAME}.*"
done

echo "Artifacts in $OUTDIR"
