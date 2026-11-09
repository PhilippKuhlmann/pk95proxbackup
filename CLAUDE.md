# PK95 ProxBackup

Backup appliance for Proxmox: a Debian 13 VM with Proxmox Backup Server and this web UI (single Go
binary, embedded vanilla-JS SPA in the PK95 Core design). Manages PBS datastores, Proxmox VE backup
jobs/restores, NFS/SMB shares (systemd mount units) and USB rotation sets (removable datastores).

- Run tests: `go test -race ./...` (PBS and PVE are simulated by `internal/fake`)
- Format: `gofmt -w .`; check JS: `node --check web/app.js`
- Demo UI with simulated servers: `.claude/launch.json` → `demo`, or
  `PK95PB_DEMO=1 PK95PB_NO_TLS=1 PK95PB_LISTEN=127.0.0.1:18095 PK95PB_HTTP_REDIRECT= PK95PB_DATA_DIR=data/demo go run .`
- UI language: German. Code/comments: English.
- Design: copy of PK95 Core (`~/code/PK95Core/resources/views`, Tailwind classes ported to `web/style.css`).
  Space Grotesk, cerulean palette, radii 2–4 px. Keep it in sync with Core, not with pk95dns.
- New PBS/PVE API calls go into `internal/prox` and must also be simulated in `internal/fake`.
- Mutating API requests need the header `X-PK95: 1` (CSRF guard); `web/app.js` sets it in `api()`.
- Layout: `internal/core` (logic, monitor, rotation), `internal/api` (HTTP), `internal/app` (wiring, TLS),
  `install/proxmox.sh` (creates the VM on a PVE host), `install/appliance.sh` (installs inside Debian 13).
