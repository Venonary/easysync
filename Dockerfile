# syntax=docker/dockerfile:1

# ---- frontend ----
FROM node:22-alpine AS frontend
WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# ---- backend ----
FROM golang:1.24-alpine AS backend
WORKDIR /src
COPY go.mod ./
COPY . .
COPY --from=frontend /src/frontend/dist ./frontend/dist
# Module has no external deps today; build embeds frontend/dist.
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/easysync .

# ---- runtime ----
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec \
  && adduser -D -H -u 10001 easysync
WORKDIR /app

COPY --from=backend /out/easysync /app/easysync
COPY docker-entrypoint.sh /app/docker-entrypoint.sh
RUN chmod 755 /app/docker-entrypoint.sh

# Persistent data: store.json, uploads, TLS CA/leaf keys
VOLUME ["/data"]

ENV EASYSYNC_ADDR=":7788" \
    EASYSYNC_DATA="/data" \
    EASYSYNC_HTTPS="false"

EXPOSE 7788

# Entrypoint starts as root, chowns /data for uid 10001, then drops to easysync.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- "http://127.0.0.1${EASYSYNC_ADDR}/api/health" || exit 1

ENTRYPOINT ["/app/docker-entrypoint.sh"]
CMD ["-addr", ":7788", "-data", "/data"]
