# Task 4: Docker packaging & data migration

## Purpose

Run EasySync as a single container with **all durable state on a volume** (`/data`), so moving to another machine is: copy the image (or rebuild) + copy the volume.

## What lives on the volume

Mount `/data` (compose volume `easysync-data` by default). Contents include:

| Path | Content |
|------|---------|
| `store.json` | Conversations, messages (incl. soft-deleted), settings |
| `uploads/` | Image/file attachments |
| `easysync-root-ca.crt` / `easysync-root-ca-key.pem` | Root CA (public + private) |
| `https-cert.pem` / `https-key.pem` | Server leaf keypair |

Keeping the same volume preserves chat history **and** the same root CA, so devices that already installed the CA keep trusting the new host.

## Quick start

Default is **HTTPS on host port 7788** with bind-mount `./data:/data` (same flags as `./easysync -https -addr :7788`).

```bash
# Build and run
docker compose up -d --build

# Health (self-signed / private CA — use -k)
curl -sk https://127.0.0.1:7788/api/health
# => {"status":"ok"}

# Installable Root CA
curl -sk https://127.0.0.1:7788/api/ca.crt -o easysync-root-ca.crt
```

中文运维（部署 / 迁移 / 备份换机 / 更新测试）见 **[docker-deploy-ops.zh-CN.md](docker-deploy-ops.zh-CN.md)**。

Plain Docker:

```bash
docker build -t easysync:local .
docker run -d --name easysync \
  -p 7788:7788 \
  -v "$(pwd)/data:/data" \
  easysync:local \
  -addr :7788 -data /data -https
```

## HTTPS notes

Compose and the image `CMD` already pass `-https`. Download the CA from `https://host:7788/api/ca.crt` (or copy `data/easysync-root-ca.crt`) and install on clients. See [root-ca-https.md](root-ca-https.md).

## Migrate to another device

Default layout uses **bind-mount `./data`**:

```bash
# Stop (does not delete ./data)
docker compose down

# Backup
tar czf easysync-data.tgz -C data .
# or: ./scripts/backup-easysync.sh

# On the new host: restore into project data/
mkdir -p data
tar xzf easysync-data.tgz -C data
docker compose up -d --build
```

Root CA trust remains valid if `easysync-root-ca.crt` / `easysync-root-ca-key.pem` were preserved.

Optional named volume workflow is documented in older revisions; prefer bind-mount for simple scp/rsync migration.

## Ports & flags

| Item | Value |
|------|-------|
| Container listen | `:7788` |
| Data dir | `/data` |
| Health | `GET /api/health` |
| Root CA download | `GET /api/ca.crt` |

Flags are the same as the bare binary (`-addr`, `-data`, `-https`, `-tls-cert`, `-tls-key`).

## Build notes

Multi-stage Dockerfile:

1. `node:22-alpine` — `npm ci` + Vite build  
2. `golang:1.24-alpine` — embed `frontend/dist`, static binary  
3. `alpine:3.21` — `docker-entrypoint.sh` starts as root, `chown`s `/data` to uid **10001** (`easysync`), then `su-exec` drops privileges before running the binary

This avoids the common named-volume pitfall where Docker creates `easysync-data` as root-owned and a non-root process cannot write `store.json`.

## Rollback

```bash
git revert <this-task-commit>
```
