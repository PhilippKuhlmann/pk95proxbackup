#!/usr/bin/env bash
# PK95 ProxBackup – installation on a Proxmox VE host with one command:
#
#   bash -c "$(curl -fsSL https://raw.githubusercontent.com/PhilippKuhlmann/pk95proxbackup/main/install/proxmox.sh)"
#
# Creates a Debian 13 VM from the official cloud image. On first boot cloud-init runs
# install/appliance.sh inside the VM: it installs Proxmox Backup Server and PK95 ProxBackup.
# Afterwards the script shows the address and the setup code of the web interface.
#
# Asks step by step (Enter keeps the suggestion). Environment variables become the suggestions,
# e.g. VMID=260 IP=192.168.1.60/24 GW=192.168.1.1 DATA_DISK=500; without a terminal the script
# runs unattended with them.
set -euo pipefail

REPO="${PK95PB_REPO:-PhilippKuhlmann/pk95proxbackup}"
RAW="${PK95PB_RAW:-https://raw.githubusercontent.com/${REPO}/main}"
IMAGE_URL="${IMAGE_URL:-https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2}"
IMAGE_DIR="${IMAGE_DIR:-/var/lib/vz/template/pk95}"

blue() { printf '\033[1;34m==> %s\033[0m\n' "$*"; }
bold() { printf '\033[1m%s\033[0m\n' "$*"; }
fail() { printf '\033[1;31mFehler: %s\033[0m\n' "$*" >&2; exit 1; }

# --- checks --------------------------------------------------------------------------------
[[ $(id -u) -eq 0 ]] || fail "Bitte als root auf dem Proxmox-Host ausführen."
command -v qm >/dev/null && command -v pvesm >/dev/null && command -v pvesh >/dev/null \
  || fail "qm/pvesm/pvesh fehlen – das Skript läuft nur auf einem Proxmox-VE-Host."
[[ "$(dpkg --print-architecture)" == amd64 ]] || fail "Nur für amd64-Hosts."
curl -fsI --retry 2 "${RAW}/install/appliance.sh" >/dev/null \
  || fail "${RAW}/install/appliance.sh ist nicht erreichbar (Repository öffentlich? Internet?). Es wurde nichts verändert."

# --- questions -----------------------------------------------------------------------------
TTY=0
{ exec 3</dev/tty; } 2>/dev/null && TTY=1

ask() {
  local var=$1 question=$2 default=$3 check=$4 hint=$5 value
  while :; do
    value=""
    if (( TTY )); then read -r -u 3 -p "  ${question} [${default:-leer}]: " value || value=""; fi
    value="${value:-$default}"
    if "$check" "$value"; then printf -v "$var" '%s' "$value"; return; fi
    (( TTY )) || fail "${question}: ${hint} (${value:-leer})"
    printf '    \033[33m%s\033[0m\n' "$hint"
  done
}

choose() {
  local var=$1 question=$2 default=$3; shift 3
  local items=("$@") i value
  (( ${#items[@]} )) || fail "${question}: nichts zur Auswahl gefunden."
  if (( TTY )); then for i in "${!items[@]}"; do printf '    %d) %s\n' $((i + 1)) "${items[$i]}"; done; fi
  while :; do
    value=""
    (( TTY )) && { read -r -u 3 -p "  ${question} [${default}]: " value || value=""; }
    value="${value:-$default}"
    [[ "$value" =~ ^[0-9]+$ ]] && (( value >= 1 && value <= ${#items[@]} )) && value="${items[$((value - 1))]}"
    for i in "${items[@]}"; do [[ "$i" == "$value" ]] && { printf -v "$var" '%s' "$value"; return; }; done
    (( TTY )) || fail "${question}: ${value} gibt es nicht."
    printf '    \033[33mBitte eine Nummer oder einen Namen aus der Liste.\033[0m\n'
  done
}

id_free() {
  [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 100 && $1 <= 999999999 )) || return 1
  ! qm status "$1" >/dev/null 2>&1 && ! { command -v pct >/dev/null && pct status "$1" >/dev/null 2>&1; }
}
hostname_ok() { [[ "$1" =~ ^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$ ]]; }
cores_ok() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 64 )); }
memory_ok() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 2048 )); }
disk_ok() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 16 )); }
data_ok() { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 == 0 || $1 >= 32 )); }
vlan_ok() { [[ -z "$1" ]] || { [[ "$1" =~ ^[0-9]+$ ]] && (( $1 >= 1 && $1 <= 4094 )); }; }
ipv4() { [[ "$1" =~ ^([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})\.([0-9]{1,3})$ ]] \
  && (( BASH_REMATCH[1] <= 255 && BASH_REMATCH[2] <= 255 && BASH_REMATCH[3] <= 255 && BASH_REMATCH[4] <= 255 )); }
ip_ok() { [[ "$1" == dhcp ]] || { [[ "$1" == */* ]] && ipv4 "${1%/*}" && [[ "${1#*/}" =~ ^[0-9]+$ ]] && (( ${1#*/} >= 8 && ${1#*/} <= 32 )); }; }
dns_ok() { [[ -z "$1" ]] || ipv4 "$1"; }
pw_ok() { (( ${#1} >= 8 )); }

mapfile -t storages < <(pvesm status --content images 2>/dev/null | awk 'NR>1 && $3=="active" {print $1}')
mapfile -t snippet_storages < <(pvesm status --content snippets 2>/dev/null | awk 'NR>1 && $3=="active" {print $1}')
mapfile -t bridges < <(for b in /sys/class/net/*/bridge; do [[ -e "$b" ]] && basename "$(dirname "$b")"; done | grep '^vmbr' || true)
(( ${#bridges[@]} )) || bridges=(vmbr0)
default_storage="${STORAGE:-}"
if [[ -z "$default_storage" ]]; then
  for s in local-lvm local-zfs; do printf '%s\n' "${storages[@]}" | grep -qx "$s" && default_storage="$s" && break; done
  default_storage="${default_storage:-${storages[0]:-}}"
fi

echo
bold "PK95 ProxBackup – Backup-Appliance als VM auf Proxmox VE"
(( TTY )) && echo "  Enter übernimmt den Vorschlag in Klammern."
echo
bold "1. VM"
ask VMID "VM-ID" "${VMID:-$(pvesh get /cluster/nextid)}" id_free "Eine freie ID ab 100 (diese ist vergeben oder ungültig)."
ask VM_HOSTNAME "Hostname" "${VM_HOSTNAME:-pk95-backup}" hostname_ok "Kleinbuchstaben, Ziffern und Bindestriche."
echo
bold "2. Ressourcen"
ask CORES "CPU-Kerne" "${CORES:-2}" cores_ok "Eine Zahl von 1 bis 64."
ask MEMORY "RAM in MB" "${MEMORY:-4096}" memory_ok "Mindestens 2048 MB (Proxmox Backup Server)."
ask DISK "Systemplatte in GB" "${DISK:-32}" disk_ok "Mindestens 16 GB."
ask DATA_DISK "Datenplatte für Sicherungen in GB (0 = keine)" "${DATA_DISK:-0}" data_ok "0 oder mindestens 32 GB. Ohne Datenplatte: NFS/SMB oder USB als Ziel."
(( TTY )) && echo "  Speicher für die Platten:"
choose STORAGE "Speicher" "$default_storage" "${storages[@]}"
echo
bold "3. Netzwerk"
(( TTY )) && echo "  Bridge:"
choose BRIDGE "Bridge" "${BRIDGE:-${bridges[0]}}" "${bridges[@]}"
ask VLAN "VLAN-Tag (leer = keins)" "${VLAN:-}" vlan_ok "Leer lassen oder eine Zahl von 1 bis 4094."
ask IP "IP-Adresse mit Präfix oder dhcp" "${IP:-dhcp}" ip_ok "dhcp oder eine Adresse wie 192.168.1.20/24."
GW="${GW:-}"; DNS="${DNS:-}"
if [[ "$IP" != dhcp ]]; then
  ask GW "Gateway" "${GW:-${IP%.*}.1}" ipv4 "Eine Adresse wie 192.168.1.1."
  ask DNS "DNS-Server" "${DNS:-$GW}" dns_ok "Eine Adresse wie 192.168.1.1."
fi
echo
bold "4. Zugang"
echo "  Das root-Passwort gilt für die Konsole der VM und die Oberfläche des Backup-Servers (Port 8007)."
ROOT_PASSWORD="${ROOT_PASSWORD:-}"
if (( TTY )) && [[ -z "$ROOT_PASSWORD" ]]; then
  while :; do
    read -r -s -u 3 -p "  root-Passwort (mind. 8 Zeichen): " p1 || p1=""; echo
    read -r -s -u 3 -p "  wiederholen: " p2 || p2=""; echo
    [[ "$p1" == "$p2" ]] && pw_ok "$p1" && { ROOT_PASSWORD="$p1"; break; }
    printf '    \033[33mPasswörter stimmen nicht überein oder sind zu kurz.\033[0m\n'
  done
fi
GENERATED_PW=0
if [[ -z "$ROOT_PASSWORD" ]]; then ROOT_PASSWORD="$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 16)"; GENERATED_PW=1; fi
SSHKEYS_FILE=""
if [[ -s /root/.ssh/authorized_keys ]]; then
  if (( TTY )); then
    read -r -u 3 -p "  SSH-Schlüssel dieses Hosts (/root/.ssh/authorized_keys) in die VM übernehmen? [J/n] " a || a=j
    [[ "${a:-j}" =~ ^[JjYy]$|^$ ]] && SSHKEYS_FILE=/root/.ssh/authorized_keys
  elif [[ "${COPY_SSH_KEYS:-1}" == 1 ]]; then
    SSHKEYS_FILE=/root/.ssh/authorized_keys
  fi
fi

# Snippets: cloud-init needs a storage with content "snippets" for the first-boot script.
SNIPPETS="${SNIPPETS:-}"
if [[ -z "$SNIPPETS" ]]; then
  if (( ${#snippet_storages[@]} )); then
    SNIPPETS="${snippet_storages[0]}"
  else
    pvesm status --storage local >/dev/null 2>&1 || fail "Kein Speicher für Snippets gefunden und kein Speicher „local“. Einen Verzeichnis-Speicher mit Inhalt „Snippets“ anlegen."
    echo
    echo "  Für das Startskript braucht cloud-init einen Speicher mit dem Inhalt „Snippets“."
    echo "  Vorschlag: beim Speicher „local“ zusätzlich „Snippets“ erlauben."
    if (( TTY )); then
      read -r -u 3 -p "  „Snippets“ bei local einschalten? [J/n] " a || a=j
      [[ "${a:-j}" =~ ^[JjYy]$|^$ ]] || fail "Abgebrochen, es wurde nichts verändert."
    fi
    SNIPPETS=local
    ENABLE_SNIPPETS=1
  fi
fi

echo
bold "Zusammenfassung"
echo "  VM-ID ${VMID} · Hostname ${VM_HOSTNAME}"
echo "  ${CORES} Kerne · ${MEMORY} MB RAM · System ${DISK} GB$( (( DATA_DISK > 0 )) && echo " · Daten ${DATA_DISK} GB") auf ${STORAGE}"
echo "  Netz ${BRIDGE}${VLAN:+ · VLAN ${VLAN}} · IP ${IP}${GW:+ · Gateway ${GW}}${DNS:+ · DNS ${DNS}}"
echo "  Snippets auf ${SNIPPETS}${ENABLE_SNIPPETS:+ (wird eingeschaltet)}"
if (( TTY )); then
  read -r -u 3 -p "Jetzt installieren? [J/n] " answer || answer=j
  [[ "${answer:-j}" =~ ^[JjYy]$|^$ ]] || fail "Abgebrochen, es wurde nichts verändert."
  exec 3<&-
fi
echo

# --- snippets storage ----------------------------------------------------------------------
if [[ -n "${ENABLE_SNIPPETS:-}" ]]; then
  blue "Erlaube Snippets auf local"
  content="$(pvesh get /storage/local --output-format json | python3 -c 'import json,sys; print(json.load(sys.stdin).get("content",""))')"
  [[ ",${content}," == *,snippets,* ]] || pvesm set local --content "${content:+${content},}snippets"
fi
snip_path="$(pvesm path "${SNIPPETS}:snippets/pk95proxbackup-${VMID}.yaml" 2>/dev/null)" || fail "Pfad für Snippets auf ${SNIPPETS} nicht gefunden."
mkdir -p "$(dirname "$snip_path")"

# --- Debian cloud image --------------------------------------------------------------------
mkdir -p "$IMAGE_DIR"
img="${IMAGE_DIR}/$(basename "$IMAGE_URL")"
blue "Lade das Debian-13-Cloud-Image"
curl -fsSL --retry 3 -o "${img}.sums" "$(dirname "$IMAGE_URL")/SHA512SUMS"
want="$(awk -v f="$(basename "$IMAGE_URL")" '$2==f {print $1}' "${img}.sums")"
[[ -n "$want" ]] || fail "Prüfsumme für $(basename "$IMAGE_URL") nicht gefunden."
if [[ ! -f "$img" ]] || [[ "$(sha512sum "$img" | awk '{print $1}')" != "$want" ]]; then
  curl -fL --retry 3 -o "${img}.part" "$IMAGE_URL"
  [[ "$(sha512sum "${img}.part" | awk '{print $1}')" == "$want" ]] || { rm -f "${img}.part"; fail "Prüfsumme des Images stimmt nicht."; }
  mv "${img}.part" "$img"
fi

# --- first boot script ---------------------------------------------------------------------
cat > "$snip_path" <<EOF
#cloud-config
# PK95 ProxBackup: first boot. Log: /var/log/pk95-install.log
package_update: true
packages:
  - qemu-guest-agent
  - curl
  - ca-certificates
runcmd:
  - [systemctl, enable, --now, qemu-guest-agent]
  - [bash, -c, "curl -fsSL --retry 5 ${RAW}/install/appliance.sh -o /root/pk95-appliance.sh && PK95PB_REPO=${REPO} PK95PB_RAW=${RAW} bash /root/pk95-appliance.sh > /var/log/pk95-install.log 2>&1"]
EOF

# --- VM ------------------------------------------------------------------------------------
blue "Lege VM ${VMID} an"
net="virtio,bridge=${BRIDGE}${VLAN:+,tag=${VLAN}}"
qm create "$VMID" --name "$VM_HOSTNAME" --cores "$CORES" --cpu host --memory "$MEMORY" \
  --machine q35 --ostype l26 --scsihw virtio-scsi-single --net0 "$net" \
  --agent enabled=1 --serial0 socket --vga serial0 --onboot 1 --tags pk95 \
  --description "PK95 ProxBackup – Backup-Appliance (Proxmox Backup Server + Weboberfläche)" >/dev/null
qm set "$VMID" --scsi0 "${STORAGE}:0,import-from=${img},discard=on,iothread=1" >/dev/null
qm resize "$VMID" scsi0 "${DISK}G" >/dev/null
if (( DATA_DISK > 0 )); then
  # backup=0: Proxmox VE must not back up the backups.
  qm set "$VMID" --scsi1 "${STORAGE}:${DATA_DISK},discard=on,iothread=1,backup=0" >/dev/null
fi
qm set "$VMID" --ide2 "${STORAGE}:cloudinit" --boot order=scsi0 >/dev/null
ipcfg="ip=${IP}"; [[ "$IP" != dhcp && -n "$GW" ]] && ipcfg+=",gw=${GW}"
qm set "$VMID" --ciuser root --cipassword "$ROOT_PASSWORD" --ipconfig0 "$ipcfg" \
  ${DNS:+--nameserver "$DNS"} ${SSHKEYS_FILE:+--sshkeys "$SSHKEYS_FILE"} \
  --cicustom "vendor=${SNIPPETS}:snippets/pk95proxbackup-${VMID}.yaml" >/dev/null
qm start "$VMID"

blue "Warte auf die VM (Debian startet, danach werden PBS und die Appliance installiert – 5 bis 15 Minuten)"
for _ in $(seq 1 180); do qm agent "$VMID" ping >/dev/null 2>&1 && break; sleep 5; done
qm agent "$VMID" ping >/dev/null 2>&1 || fail "Der Gast-Agent meldet sich nicht. Konsole der VM ${VMID} prüfen (Log: /var/log/pk95-install.log)."

guest_cat() { qm guest exec "$VMID" -- cat "$1" 2>/dev/null | python3 -c 'import json,sys; d=json.load(sys.stdin); print(d.get("out-data","").strip()) if d.get("exitcode")==0 else None' 2>/dev/null; }
code=""
for _ in $(seq 1 240); do
  code="$(guest_cat /var/lib/pk95proxbackup/setup-code || true)"
  [[ -n "$code" && "$code" != None ]] && break
  if [[ "$(guest_cat /var/lib/pk95proxbackup/install-failed || true)" != "" ]]; then
    fail "Installation in der VM fehlgeschlagen. Details: in der VM /var/log/pk95-install.log"
  fi
  sleep 5
done
ip="$(qm guest cmd "$VMID" network-get-interfaces 2>/dev/null | python3 -c '
import json,sys
for i in json.load(sys.stdin):
    if i.get("name")=="lo": continue
    for a in i.get("ip-addresses",[]):
        if a.get("ip-address-type")=="ipv4": print(a["ip-address"]); sys.exit()' 2>/dev/null || true)"
[[ -n "$code" && "$code" != None ]] || fail "Kein Einrichtungscode erschienen. Log in der VM: /var/log/pk95-install.log (Konsole der VM ${VMID})."

echo
bold "Fertig! PK95 ProxBackup läuft in VM ${VMID}."
echo
echo "  Weboberfläche:     https://${ip:-<IP der VM>}"
echo "  Einrichtungscode:  ${code}"
echo "  Backup-Server:     https://${ip:-<IP der VM>}:8007 (root@pam)"
(( GENERATED_PW )) && echo "  root-Passwort:     ${ROOT_PASSWORD}   (bitte notieren)"
(( DATA_DISK > 0 )) && echo "  Datenplatte:       ${DATA_DISK} GB – in der Oberfläche unter Datastores → „Freie Platte“ einrichten."
echo
echo "  Das Zertifikat ist selbst signiert; der Browser warnt beim ersten Aufruf."
echo "  Danach unter Verbindungen diesen Proxmox VE mit einem API-Token eintragen:"
echo "    pveum user token add root@pam pk95 --privsep 0"
