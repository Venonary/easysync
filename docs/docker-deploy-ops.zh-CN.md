# EasySync Docker Compose 部署与运维手册（中文）

本文说明如何在已有 Docker 工作区（本环境为 `/workspace`）上，以 **HTTPS + 端口 7788** 部署 EasySync，完成数据迁移、证书获取、备份换机与日常更新测试。

> 与旧版裸二进制等价：`./easysync-linux-amd64 -https -addr :7788`（数据目录默认 `./data`）。

---

## 1. 环境前提

| 项 | 要求 |
|----|------|
| 主机 | 可 SSH 登录的 Linux（示例：`cspl@192.168.50.234`） |
| Docker | Docker Engine + Docker Compose v2+ |
| 权限 | 用户在 `docker` 组，可写 `/workspace`（或你的 compose 根目录） |
| 端口 | 宿主机 **7788** 未被占用（若旧二进制仍在跑，先停掉） |

检查：

```bash
docker --version
docker compose version
ss -tlnp | grep 7788 || true
```

---

## 2. 一键部署步骤（从零到 HTTPS）

### 2.1 放置项目

将本仓库放到 Docker 工作区（与 vaultwarden 等并列）：

```bash
# 推荐路径（本机已采用）
export EASYSYNC_DIR=/workspace/easysync
mkdir -p "$EASYSYNC_DIR"
# 把源码同步到 $EASYSYNC_DIR（git clone / scp / rsync 任选）
cd "$EASYSYNC_DIR"
```

必要文件：`Dockerfile`、`docker-compose.yml`、`docker-entrypoint.sh`、Go 源码、`frontend/`（无需本机预装 Node，镜像内会构建）。

### 2.2 迁移旧数据（从裸二进制）

旧版若在 `~/Downloads` 运行且使用默认 `-data data`，数据在：

```text
~/Downloads/data/
  store.json          # 会话与设置
  uploads/            # 附件
  https-cert.pem      # 旧服务端证书（可选）
  https-key.pem
```

迁移到 compose 的 bind-mount 目录：

```bash
# 若旧进程仍占 7788，先结束
pkill -f 'easysync-linux-amd64' || true

export EASYSYNC_DIR=/workspace/easysync
mkdir -p "$EASYSYNC_DIR/data"
# 保留目标已有文件时用 rsync；首次可用 cp -a
rsync -a ~/Downloads/data/ "$EASYSYNC_DIR/data/"
# 或：cp -a ~/Downloads/data/. "$EASYSYNC_DIR/data/"

ls -la "$EASYSYNC_DIR/data"
# 应看到 store.json、uploads/
```

说明：

- 新版本启用 Root CA 后，会在 `data/` 下生成 `easysync-root-ca.crt` / `easysync-root-ca-key.pem`，并按需重签 `https-cert.pem`。
- 若已将 Root CA 安装到客户端，**务必备份并复用同一份 `easysync-root-ca.*`**，否则客户端需重新安装 CA。

### 2.3 构建并启动

```bash
cd /workspace/easysync
docker compose up -d --build
docker compose ps
docker compose logs -f --tail=50
```

`docker-compose.yml` 默认：

- 端口：`7788:7788`
- 命令：`-addr :7788 -data /data -https`
- 数据：`./data` → 容器 `/data`（bind-mount，便于整目录备份）
- 健康检查：对 `https://127.0.0.1:7788/api/health` 探测（跳过证书校验）

### 2.4 验证

```bash
# 健康（服务端自签/私有 CA，用 -k）
curl -sk https://127.0.0.1:7788/api/health
# 期望：{"status":"ok"}

# 下载可安装的 Root CA
curl -sk https://127.0.0.1:7788/api/ca.crt -o easysync-root-ca.crt
openssl x509 -in easysync-root-ca.crt -noout -subject -dates

# 数据是否挂载成功
ls -la /workspace/easysync/data/
# store.json、uploads/、easysync-root-ca.crt、https-cert.pem 等

# 浏览器（局域网）
# https://192.168.50.234:7788/
```

首次用浏览器访问会出现「不受信任」提示：安装 Root CA 后可消除（见下一节）。

---

## 3. HTTPS 与证书

### 3.1 自动生成的内容（数据目录）

| 文件 | 用途 |
|------|------|
| `easysync-root-ca.crt` | **安装到客户端** 的根证书（公开） |
| `easysync-root-ca-key.pem` | 根私钥（**绝不可外泄/提交 Git**） |
| `https-cert.pem` / `https-key.pem` | 服务端叶子证书与私钥 |

应用在 `-https` 启动时调用 `EnsureTLSMaterial`：无 CA 则生成；叶子证书不匹配或不含所需 IP/域名时用**同一 Root CA**重签叶子。

### 3.1.1 浏览器报 `ERR_CERT_COMMON_NAME_INVALID` / “证书来自 localhost”

根证书装对了仍可能出现。原因：Docker 容器内只能看到容器网卡 IP，叶子证书 SAN 若只有 `localhost`，用 `https://192.168.50.234:7788` 访问就会名称不匹配。

处理：在 `docker-compose.yml` 配置宿主机局域网 IP（可多值逗号分隔）：

```yaml
environment:
  EASYSYNC_TLS_SANS: "192.168.50.234"
command: ["-addr", ":7788", "-data", "/data", "-https", "-tls-sans", "192.168.50.234"]
```

然后重建/重启：

```bash
cd /workspace/easysync
docker compose up -d --build
# 确认叶子 SAN 含该 IP（Root CA 不变，客户端无需重装 CA）
openssl x509 -in data/https-cert.pem -noout -ext subjectAltName
```

### 3.2 获取证书的两种方式

```bash
# 方式 A：HTTP(S) API
curl -sk https://127.0.0.1:7788/api/ca.crt -o easysync-root-ca.crt

# 方式 B：直接从数据目录拷贝
cp /workspace/easysync/data/easysync-root-ca.crt ~/
```

### 3.3 客户端安装（概要）

- **Windows**：双击 `.crt` → 安装到「受信任的根证书颁发机构」
- **macOS**：钥匙串访问 → 系统 → 信任该证书
- **Android**：设置 → 安全 → 加密与凭据 → 从存储设备安装
- **iOS**：通过「描述文件」安装后，在「证书信任设置」中启用完全信任

更细说明见同目录 [root-ca-https.md](root-ca-https.md)。

---

## 4. 日常运维命令

```bash
cd /workspace/easysync

# 状态
docker compose ps
docker compose logs --tail=100

# 停止 / 启动 / 重启（不会删数据，因 data 在 bind-mount）
docker compose stop
docker compose start
docker compose restart

# 停止并移除容器（默认不删 ./data）
docker compose down
# 切勿对 bind-mount 使用会误删宿主机目录的脚本；compose down 不会删除 ./data
```

---

## 5. 更新到新版本

```bash
cd /workspace/easysync

# 1. 先备份（强烈建议）
./scripts/backup-easysync.sh   # 若已放置脚本；或见下文 tar 命令

# 2. 同步新代码（git pull / 覆盖源码）
# git pull

# 3. 重建并滚动
docker compose up -d --build

# 4. 验证
curl -sk https://127.0.0.1:7788/api/health
docker compose ps
```

前端与后端均在镜像多阶段构建内完成，宿主机不必安装 Node/Go。

---

## 6. 备份与换机迁移

### 6.1 备份什么

**最少备份整个 `./data` 目录**，即包含：

- 业务：`store.json`、`uploads/`
- TLS：`easysync-root-ca.crt`、`easysync-root-ca-key.pem`、`https-cert.pem`、`https-key.pem`

可选再备份：`docker-compose.yml`、`.env`（若有自定义）、整仓源码。

### 6.2 制作备份包

TLS 私钥（`easysync-root-ca-key.pem` 等）在容器内常为 **uid 10001、权限 0600**，宿主机用户直接 `tar` 可能 `Permission denied`。推荐用脚本（内部用临时 Alpine 容器以 root 打包）：

```bash
cd /workspace/easysync
mkdir -p /workspace/backups
bash scripts/backup-easysync.sh
# 产物：/workspace/backups/easysync-data-YYYYMMDD-HHMMSS.tgz

# 等价手写命令：
STAMP=$(date +%Y%m%d-%H%M%S)
docker run --rm \
  -v /workspace/easysync/data:/data:ro \
  -v /workspace/backups:/backup \
  alpine:3.21 \
  tar czf "/backup/easysync-data-${STAMP}.tgz" -C /data .

# 可选：再备份 compose 配置（不含私钥时也有用）
tar czf "/workspace/backups/easysync-compose-${STAMP}.tgz" \
  docker-compose.yml Dockerfile docker-entrypoint.sh

ls -lh /workspace/backups/easysync-*.tgz
```

建议：

- 将 `.tgz` 拷到另一台机器、网盘或移动硬盘
- **不要**把含 `*-key.pem` 的备份提交到公开仓库
- 定期备份（例如每次升级前、每周一次）

### 6.3 在新设备恢复

```bash
# 新机：安装 Docker，放置源码到 /workspace/easysync
cd /workspace/easysync
mkdir -p data
tar xzf /path/to/easysync-data-YYYYMMDD-HHMMSS.tgz -C data
docker compose up -d --build
curl -sk https://127.0.0.1:7788/api/health
```

同一 CA 私钥恢复后，**已安装过 Root CA 的客户端无需重装证书**（IP 变更时若证书 SAN 不含新 IP，应用会按需重签叶子证书，CA 仍有效）。

### 6.4 从本机 Windows 拉取备份示例

```powershell
# 需已配置 SSH 密钥；或用 scp 交互输入密码
scp cspl@192.168.50.234:/workspace/backups/easysync-data-*.tgz D:\backups\
```

---

## 7. 使用与测试清单

部署或更新后建议按序执行：

| # | 操作 | 期望 |
|---|------|------|
| 1 | `curl -sk https://127.0.0.1:7788/api/health` | `{"status":"ok"}` |
| 2 | `curl -sk https://127.0.0.1:7788/api/ca.crt \| head` | PEM（`BEGIN CERTIFICATE`） |
| 3 | 浏览器打开 `https://<主机IP>:7788` | 页面加载（可能需信 CA） |
| 4 | 查看历史会话是否与迁移前一致 | `store.json` 中会话可见 |
| 5 | 打开含图片/附件的消息 | `uploads/` 可访问 |
| 6 | 发送一条文本 + 一张图 | 刷新后仍在；`data/` 有更新 |
| 7 | `docker compose restart` 后再访问 | 数据不丢 |
| 8 | 局域网其他设备安装 CA 后访问 | 无持续证书警告 |

常用调试：

```bash
docker compose logs --tail=200
docker inspect easysync --format '{{.State.Health.Status}}'
ls -la /workspace/easysync/data/
```

---

## 8. 故障排查

| 现象 | 处理 |
|------|------|
| `bind: address already in use` | `ss -tlnp \| grep 7788`，停掉旧 `easysync-linux-amd64` 或其它占用进程 |
| 健康检查 unhealthy | `docker compose logs`；确认命令含 `-https` 且 healthcheck 用 `https://` + `--no-check-certificate` |
| 容器内无法写 data | entrypoint 会 `chown` 到 uid 10001；确认 `./data` 可被 Docker 挂载 |
| 浏览器证书错误 | 安装 `/api/ca.crt`；或临时「继续访问」仅用于自测 |
| 会话丢失 | 检查是否挂载了错误的空 `data/`；从备份 tar 恢复 |
| 构建失败（npm/go） | 确认机器可拉 Docker Hub 镜像；重试 `docker compose build --no-cache` |

---

## 9. 安全提示

1. **Root CA 私钥**（`easysync-root-ca-key.pem`）等同于「能签发你们内网信任的证书」，务必随备份加密保存，勿公开。
2. 文档与脚本中**不要**写死 SSH 密码；使用密钥登录：`ssh-copy-id cspl@主机`。
3. 仅在可信局域网暴露 7788；若上公网，应另配反向代理、防火墙与强认证（本项目默认无账号体系）。
4. `docker compose down -v` 对 **named volume** 会删数据；当前默认是 **bind-mount `./data`**，`down` 不会删目录，但删错宿主机 `data/` 仍会丢数据。

---

## 10. 路径速查（本环境约定）

| 用途 | 路径 |
|------|------|
| Compose 项目 | `/workspace/easysync` |
| 持久数据 | `/workspace/easysync/data` |
| 旧二进制数据源 | `~/Downloads/data` |
| 备份目录建议 | `/workspace/backups` |
| 访问地址 | `https://192.168.50.234:7788` |
| 健康检查 | `GET /api/health` |
| Root CA 下载 | `GET /api/ca.crt` |

---

## 11. 与旧二进制对照

| | 旧版 | Docker Compose（当前） |
|--|------|------------------------|
| 启动 | `./easysync-linux-amd64 -https -addr :7788` | `docker compose up -d` |
| 数据 | `~/Downloads/data` | `/workspace/easysync/data` |
| 证书 | 自签叶子或旧 TLS 文件 | Root CA + 叶子（可 API 下载 CA） |
| 更新 | 替换二进制 | `docker compose up -d --build` |
| 开机自启 | 需 systemd/tmux | `restart: unless-stopped` |

完成迁移并确认健康后，可不再依赖 `~/Downloads/easysync-linux-amd64`；建议保留一份 `data` 备份再清理旧文件。
