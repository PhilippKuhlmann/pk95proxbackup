#!/usr/bin/env bash
# PK95 ProxBackup – installs (or updates) the appliance on Debian 13:
#   * Proxmox Backup Server (pbs-no-subscription), unless it is already there or INSTALL_PBS=0
#   * NFS/SMB tools, QEMU guest agent
#   * the pk95proxbackup binary from the latest GitHub release as systemd service (HTTPS on 443)
#
# Runs on first boot of the VM made by install/proxmox.sh, but also on its own:
#   on a fresh Debian 13 VM, or directly on an existing Proxmox Backup Server:
#     bash -c "$(curl -fsSL https://raw.githubusercontent.com/PhilippKuhlmann/pk95proxbackup/main/install/appliance.sh)"
# Running it again updates the binary.
set -euo pipefail

REPO="${PK95PB_REPO:-PhilippKuhlmann/pk95proxbackup}"
RELEASE="${PK95PB_VERSION:-latest}"
DATA_DIR=/var/lib/pk95proxbackup
KEYRING_URL=https://enterprise.proxmox.com/debian/proxmox-archive-keyring-trixie.gpg
KEYRING_SHA256=136673be77aba35dcce385b28737689ad64fd785a797e57897589aed08db6e45

blue() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
fail() {
  printf '\033[1;31mFehler: %s\033[0m\n' "$*" >&2
  mkdir -p "$DATA_DIR" && echo "$*" > "$DATA_DIR/install-failed"
  exit 1
}

[[ $(id -u) -eq 0 ]] || fail "Bitte als root ausführen."
# Read os-release in a subshell: it defines VERSION, NAME, … and would overwrite ours.
CODENAME="$(. /etc/os-release && echo "${VERSION_CODENAME:-}")"
PRETTY="$(. /etc/os-release && echo "${PRETTY_NAME:-unbekannt}")"
[[ "$CODENAME" == trixie ]] || fail "Benötigt Debian 13 (trixie), gefunden: ${PRETTY}."
case "$(dpkg --print-architecture)" in
  amd64) ARCH=amd64 ;;
  arm64) ARCH=arm64; [[ "${INSTALL_PBS:-1}" == 0 ]] || command -v proxmox-backup-manager >/dev/null || fail "Proxmox Backup Server gibt es nur für amd64. Mit INSTALL_PBS=0 nur die Oberfläche installieren." ;;
  *) fail "Architektur $(dpkg --print-architecture) wird nicht unterstützt." ;;
esac
rm -f "$DATA_DIR/install-failed"
export DEBIAN_FRONTEND=noninteractive

blue "Pakete aktualisieren"
apt-get update -q
apt-get install -y -q curl ca-certificates nfs-common cifs-utils qemu-guest-agent e2fsprogs xfsprogs >/dev/null
systemctl enable --now qemu-guest-agent >/dev/null 2>&1 || true

if ! command -v proxmox-backup-manager >/dev/null && [[ "${INSTALL_PBS:-1}" != 0 ]]; then
  blue "Installiere Proxmox Backup Server (pbs-no-subscription)"
  curl -fsSL --retry 3 -o /tmp/proxmox-keyring.gpg "$KEYRING_URL"
  echo "${KEYRING_SHA256}  /tmp/proxmox-keyring.gpg" | sha256sum -c --quiet - || fail "Prüfsumme des Proxmox-Schlüsselbunds stimmt nicht."
  install -m 0644 /tmp/proxmox-keyring.gpg /usr/share/keyrings/proxmox-archive-keyring.gpg
  rm -f /tmp/proxmox-keyring.gpg
  cat > /etc/apt/sources.list.d/proxmox.sources <<'EOF'
Types: deb
URIs: http://download.proxmox.com/debian/pbs
Suites: trixie
Components: pbs-no-subscription
Signed-By: /usr/share/keyrings/proxmox-archive-keyring.gpg
EOF
  apt-get update -q
  apt-get install -y -q proxmox-backup-server
  # Without a subscription the enterprise repository only produces errors.
  rm -f /etc/apt/sources.list.d/pbs-enterprise.list /etc/apt/sources.list.d/pbs-enterprise.sources
  apt-get update -q >/dev/null || true
fi

blue "Lade PK95 ProxBackup (${RELEASE})"
if [[ -n "${PK95PB_BINARY:-}" ]]; then
  install -m 0755 "$PK95PB_BINARY" /usr/local/bin/pk95proxbackup.new
else
  if [[ "$RELEASE" == latest ]]; then base="https://github.com/${REPO}/releases/latest/download"; else base="https://github.com/${REPO}/releases/download/${RELEASE}"; fi
  tmp="$(mktemp -d)"
  curl -fsSL --retry 3 -o "$tmp/pk95proxbackup-linux-${ARCH}" "$base/pk95proxbackup-linux-${ARCH}" || fail "Download von ${base} fehlgeschlagen (gibt es schon ein Release?)."
  curl -fsSL --retry 3 -o "$tmp/SHA256SUMS" "$base/SHA256SUMS" || fail "SHA256SUMS fehlt im Release."
  (cd "$tmp" && grep " pk95proxbackup-linux-${ARCH}\$" SHA256SUMS | sha256sum -c --quiet -) || fail "Prüfsumme des Programms stimmt nicht."
  install -m 0755 "$tmp/pk95proxbackup-linux-${ARCH}" /usr/local/bin/pk95proxbackup.new
  rm -rf "$tmp"
fi
mv /usr/local/bin/pk95proxbackup.new /usr/local/bin/pk95proxbackup

blue "Richte den Dienst ein"
mkdir -p "$DATA_DIR" /etc/pk95proxbackup && chmod 700 "$DATA_DIR" /etc/pk95proxbackup
cat > /etc/systemd/system/pk95proxbackup.service <<'EOF'
[Unit]
Description=PK95 ProxBackup (Weboberfläche der Backup-Appliance)
After=network-online.target proxmox-backup-proxy.service
Wants=network-online.target

[Service]
Environment=PK95PB_DATA_DIR=/var/lib/pk95proxbackup
EnvironmentFile=-/etc/pk95proxbackup/env
ExecStart=/usr/local/bin/pk95proxbackup
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable pk95proxbackup >/dev/null 2>&1
systemctl restart pk95proxbackup

# The console shows the address and (until setup is done) the code; agetty reads /etc/issue.d.
for _ in $(seq 1 30); do [[ -s "$DATA_DIR/setup-code" || -s "$DATA_DIR/pk95proxbackup.db" ]] && break; sleep 1; done
ip="$(hostname -I | awk '{print $1}')"
echo
echo "PK95 ProxBackup läuft: https://${ip}"
if [[ -s "$DATA_DIR/setup-code" ]]; then
  echo "Einrichtungscode: $(cat "$DATA_DIR/setup-code")"
fi
systemctl restart getty@tty1 serial-getty@ttyS0 >/dev/null 2>&1 || true
