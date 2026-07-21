#!/bin/sh
set -e

DATA_DIR="${EASYSYNC_DATA:-/data}"

# Named volumes are root-owned by default. Ensure the runtime user can
# create store.json, uploads/, and TLS material under /data.
mkdir -p "$DATA_DIR" "$DATA_DIR/uploads"
chown -R easysync:easysync "$DATA_DIR"
chmod 755 "$DATA_DIR"

exec su-exec easysync /app/easysync "$@"
