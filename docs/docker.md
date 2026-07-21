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

```bash
# Build and run (HTTP on host port 7788)
docker compose up -d --build

# Health
curl -s http://127.0.0.1:7788/api/health
# => {"status":"ok"}
```

Plain Docker:

```bash
docker build -t easysync:local .
docker run -d --name easysync \
  -p 7788:7788 \
  -v easysync-data:/data \
  easysync:local
```

## HTTPS in Docker

```yaml
# docker-compose.yml command override:
command: ["-addr", ":7788", "-data", "/data", "-https"]
```

Then download the CA from `https://host:7788/api/ca.crt` (or copy from the volume) and install on clients. See [root-ca-https.md](root-ca-https.md).

## Migrate to another device

1. **Stop** the old container: `docker compose down` (do **not** pass `-v` if you want to keep data).
2. **Export volume** (example with a temporary helper container):

```bash
docker run --rm -v easysync-data:/data -v "$(pwd):/backup" alpine \
  tar czf /backup/easysync-data.tgz -C /data .
```

3. On the new host: install Docker, copy this repo (or the image), restore:

```bash
docker volume create easysync-data
docker run --rm -v easysync-data:/data -v "$(pwd):/backup" alpine \
  tar xzf /backup/easysync-data.tgz -C /data
docker compose up -d --build
```

4. Open the new host’s IP/port; root CA trust remains valid if the volume (and thus CA) was preserved.

### Bind-mount alternative

```yaml
volumes:
  - ./data:/data
```

Then migrate by copying the `./data` folder with scp/rsync.

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
3. `alpine:3.21` — non-root user `easysync` (uid 10001), volume `/data`

## Rollback

```bash
git revert <this-task-commit>
```
