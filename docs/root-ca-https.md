# Task 2: Installable Root CA for HTTPS

## Purpose

HTTPS used to rely on a one-off self-signed server certificate. Browsers always warned because nothing on the device trusted that leaf. EasySync now maintains a **persistent root CA** under the data directory, issues the server certificate from that CA, and exposes the root as a downloadable `.crt` so you can install trust once on each device.

## How it works

On `-https` startup (or when `/api/ca.crt` is first requested):

1. Ensure `data/easysync-root-ca.crt` + `data/easysync-root-ca-key.pem` (private key stays server-side only).
2. Ensure `data/https-cert.pem` + `data/https-key.pem` as a **leaf** signed by that root (not self-signed).
3. Serve HTTPS with the leaf; clients that installed the root can verify the chain.

| File | Role |
|------|------|
| `data/easysync-root-ca.crt` | Installable root certificate (public) |
| `data/easysync-root-ca-key.pem` | Root private key (never download) |
| `data/https-cert.pem` | Server leaf certificate |
| `data/https-key.pem` | Server private key |

## Download the root CA

```text
GET /api/ca.crt
```

Examples:

```powershell
# After starting the server (HTTP or HTTPS)
Invoke-WebRequest http://127.0.0.1:7788/api/ca.crt -OutFile easysync-root-ca.crt
```

```bash
curl -fkO https://SERVER:7788/api/ca.crt
# or over plain HTTP if still available:
curl -O http://SERVER:7788/api/ca.crt
```

You can also open `/api/ca.crt` in a browser and save the file, or copy `data/easysync-root-ca.crt` from the host.

## Install on devices

### Windows

1. Double-click `easysync-root-ca.crt`.
2. **Install Certificate…** → **Local Machine** (or Current User).
3. Place in **Trusted Root Certification Authorities**.
4. Restart the browser and open `https://…` again.

### Android

1. Transfer the `.crt` to the phone.
2. Settings → Security → Encryption & credentials → **Install a certificate** → **CA certificate**.
3. Select the file and confirm. Some browsers (Chrome) use the system store; others may need their own trust settings.

### iOS / iPadOS

1. AirDrop / download the `.crt` and open it (Profile Downloaded).
2. Settings → General → VPN & Device Management → install the profile.
3. Settings → General → About → Certificate Trust Settings → enable full trust for **EasySync Root CA**.

### macOS

1. Open the `.crt` (adds it to Keychain Access).
2. Find **EasySync Root CA** → Get Info → Trust → **When using this certificate: Always Trust**.

### Linux (system trust, distro-dependent)

```bash
sudo cp easysync-root-ca.crt /usr/local/share/ca-certificates/easysync-root-ca.crt
sudo update-ca-certificates
```

Firefox often uses its own certificate store: Settings → Privacy & Security → Certificates → View Certificates → Authorities → Import.

## Operator notes

- Installing the root is **required** for browsers to stop warning; EasySync cannot force OS/browser trust automatically.
- Prefer keeping the same `data/` directory when migrating hosts so the same CA continues to work on already-trusted clients.
- Operator-supplied `-tls-cert` / `-tls-key` still override the leaf files; a root CA is still generated for download convenience.
- Root validity is 10 years; leaf is 1 year and is reissued if it no longer chains to the current CA.

## Verify chain (developers)

```powershell
$env:GOCACHE=(Join-Path (Get-Location) '.gocache')
go test ./... -run TestEnsureTLSMaterial
```

## Rollback

```bash
git revert <this-task-commit>
```
