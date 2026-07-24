#!/usr/bin/env bash
# Backup EasySync data dir (store, uploads, TLS CA/leaf) for device migration.
# TLS private keys are often 0600 uid 10001 inside the container, so we tar
# via a short-lived helper container that mounts the same path as root.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DATA_DIR="${EASYSYNC_DATA_DIR:-$ROOT/data}"
BACKUP_DIR="${EASYSYNC_BACKUP_DIR:-/workspace/backups}"
STAMP="$(date +%Y%m%d-%H%M%S)"
OUT_NAME="easysync-data-${STAMP}.tgz"

if [[ ! -d "$DATA_DIR" ]]; then
  echo "data dir not found: $DATA_DIR" >&2
  exit 1
fi

mkdir -p "$BACKUP_DIR"

if command -v docker >/dev/null 2>&1; then
  # Read all files (incl. 0600 keys owned by container uid) as root in helper.
  docker run --rm \
    -v "${DATA_DIR}:/data:ro" \
    -v "${BACKUP_DIR}:/backup" \
    alpine:3.21 \
    tar czf "/backup/${OUT_NAME}" -C /data .
else
  # Fallback: host tar (may fail on unreadable key files without sudo)
  tar czf "${BACKUP_DIR}/${OUT_NAME}" -C "$DATA_DIR" .
fi

OUT="${BACKUP_DIR}/${OUT_NAME}"
echo "wrote $OUT ($(du -h "$OUT" | awk '{print $1}'))"
ls -lh "$OUT"
