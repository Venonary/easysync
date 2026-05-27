# Release packages and usage

This project produces multi-platform release packages containing the backend binary and the built frontend (`dist/`). The GitHub Actions workflow `/.github/workflows/release.yml` builds the frontend, cross-compiles the backend for several platforms, packages each artifact, and uploads them to the created GitHub Release.

What each package contains
- `easysync-<tag>-<os>-<arch>.zip` or `.tar.gz`
  - `easysync` (binary; on Windows it is `easysync.exe`)
  - `frontend/` (built SPA in `index.html`, assets, etc.)
  - `data/` (optional example data if present in repo)
  - `README.md`

Quick Start per platform

- Windows
  - Unzip the release package.
  - Double-click `easysync.exe` or run in PowerShell:
    ```powershell
    .\easysync.exe -addr :7788
    ```
  - Open browser at `http://127.0.0.1:7788` (or `https://` if started with `-https`).

- Linux
  - Untar the package and make the binary executable:
    ```bash
    tar -xzf easysync-...tar.gz
    chmod +x easysync
    ./easysync -addr :7788
    ```
  - Visit `http://127.0.0.1:7788`.

- macOS
  - Same as Linux. If you get a Gatekeeper warning the binary is unsigned — run from Terminal or right-click -> Open and approve.

TLS / HTTPS notes
- The releases include the server only; by default `easysync` will serve the embedded frontend and will generate self-signed certs under the `data/` directory if started with `-https`.
- Browsers will not trust the self-signed certificate; you'll see a warning. For production use obtain a CA-signed certificate (Let's Encrypt) or instruct users to import the cert (not recommended). For local testing use `-https` and accept the browser warning.

Recommended Distribution
- Use the GitHub Actions workflow to attach platform-specific archives to a Release. Provide a short `README` in the Release describing how to run and whether TLS is enabled.

Local packaging scripts
- `scripts/package-release.sh` — Build and package artifacts on Unix-like systems (and WSL).
- `scripts/package-release.ps1` — Build and package artifacts on Windows PowerShell.

If you want, I can:
- Add a signed installer (NSIS) for Windows.
- Add Docker images for containerized deployment.
- Add CI step to create checksums and GPG signatures for artifacts.
