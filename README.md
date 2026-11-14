# PK95 ProxBackup

Backup-Appliance für Proxmox im Design von PK95 Core. Eine Debian-13-VM mit **Proxmox Backup Server**
und einer eigenen Weboberfläche, über die du Sicherungen verwaltest und einrichtest:

- **Sichern:** Sicherungsjobs in Proxmox VE anlegen, bearbeiten und sofort starten; einzelne VMs und
  Container „jetzt sichern“; Übersicht, welche Gäste wann wohin gesichert wurden und welche in keinem Job sind.
- **Wiederherstellen** aus jeder Sicherung als neuer Gast (das Original bleibt unberührt).
- **Datastores** auf einer freien (virtuellen) Platte, einer **NFS-/SMB-Freigabe** oder in einem Verzeichnis,
  mit Aufbewahrung (keep-last/daily/weekly/…), Prüfung (Verify) und Speicherfreigabe (Garbage Collection) nach Zeitplan.
- **Vorhandener PBS:** weitere Backup-Server per API-Token einbinden; Kopierjobs zwischen den Servern (z. B. Außer-Haus-Kopie).
  Der Kopierjob-Dialog verbindet die beiden Server selbst: Er zeigt die Adresse, unter der das Ziel die Quelle erreicht
  (TCP 8007 vom Ziel zur Quelle), prüft die Verbindung vom Ziel aus und legt auf der Quelle einen eigenen Token an,
  der nur die kopierten Datastores lesen darf.
- **PBS in Proxmox VE eintragen** mit einem Klick: Die Appliance legt auf dem PBS einen eigenen Token an, der nur diesen
  Datastore benutzen darf, und trägt den Speicher in Proxmox VE ein.
- **Wechselplatten (USB):** Sätze aus zwei oder mehr Platten, die täglich oder wöchentlich getauscht werden – am PBS
  oder per USB-Durchreichung an dieser VM. Kopieren beim Anstecken und/oder zur Uhrzeit, aufräumen, aushängen,
  Meldung „kann abgezogen werden“, Erinnerung, wenn eine Platte lange nicht dran war. Mit **Wechselplan** bekommt
  jede Platte ihre Wochentage – auch „1. bis 5. Freitag im Monat“ oder gerade/ungerade Kalenderwoche (Vorlage
  „Mo–Do + Freitag 1–5“) –, die Liste zeigt „heute dran“, und auf Wunsch meldet die Appliance eine falsche Platte.
  Lässt sich eine Platte nicht mehr lesen (beschädigtes Dateisystem), steht dort die Empfehlung **„Neu aufsetzen“**:
  formatieren, neu befüllen und danach zur Kontrolle von der Platte zurücklesen (ein defektes USB-Gehäuse fällt so auf).
  Liegt auf den Platten nur die neueste Sicherung, kann die Appliance das auf Wunsch selbst tun und die Kopie wiederholen.
- **Archiv-Platten (USB):** einmalige Sicherung auf eine eigene Platte zum Mitnehmen oder Einlagern über Jahre –
  nur die neueste Sicherung je Server, auf Wunsch nur ausgewählte Server, danach vollständig geprüft und ausgehängt.
  Später anstecken und „Prüfen“ liest alles erneut; zum Wiederherstellen erscheint die Platte als Quelle.
- **Fremde Platten einlesen:** USB-Platte mit dem Datastore eines anderen Backup-Servers (Kundenplatte, altes Archiv)
  einbinden, ohne etwas zu löschen – danach wiederherstellen und automatisch per Restore-Test prüfen.
- **Restore-Tests nach Zeitplan:** wöchentlich (auch alle 2–4 Wochen) oder monatlich – am ersten bis vierten oder
  letzten Wochentag bzw. an einem Datum (auch Monatsletzter), jeden Monat oder alle 2, 3, 4, 6 oder 12 Monate ab einem
  Startmonat. Der Dialog zeigt den Plan in Klartext und die nächsten Termine.
- **Festplatten (SMART):** alle Platten der Backup-Server und der Proxmox-VE-Knoten werden täglich und gleich nach dem
  Einstecken geprüft – schwebende oder unlesbare Sektoren, ersetzte Sektoren, Selbsttest, SSD-Verschleiß, Temperatur.
  Wird eine Platte schlechter, kommt eine Meldung. Archiv-Platten, die über ein Jahr nicht angesteckt waren, erscheinen
  als Hinweis in der Übersicht.
- **Abgezogen ohne Aushängen:** Wird eine Wechsel- oder Archiv-Platte im eingehängten Zustand gezogen, meldet die
  Appliance das, entfernt den verwaisten Mount und liest die Platte nach der nächsten Kopie zur Kontrolle zurück.
- **Zustand der Backup-Server:** laufende und installierte PBS-Version („Dienste neu laden“ per Klick), Neustart nötig
  wegen neuem Kernel, verfügbare Updates – geprüft alle sechs Stunden, zu sehen unter Verbindungen.
- **Monatsbericht:** am 1. jedes Monats per Mail, Telegram oder Push – Sicherungen je Server, USB-Kopien,
  Restore-Tests, Festplatten, Backup-Server, Archiv-Platten und alle Hinweise des Vormonats, vollständig als
  HTML-Datei (druckbar als PDF); jederzeit unter Einstellungen → Benachrichtigungen ansehbar.
- **Update per Klick:** die Appliance sieht zweimal täglich bei GitHub nach (übertragen wird nur die Versionsnummer),
  prüft die Prüfsumme, testet das neue Programm vor dem Einsetzen und kehrt von selbst zur alten Version zurück, wenn
  die neue dreimal nicht startet.
- **Sicherung der Appliance selbst:** Datenbank, SSH-Schlüssel, Zertifikat und die Konfiguration des lokalen PBS
  (`/etc/proxmox-backup`, Mount-Units) werden täglich mit einem Wiederherstellungs-Passwort verschlüsselt (AES-256-GCM,
  scrypt) als Gruppe `host/pk95proxbackup` in den Datastore geschrieben und wandern so auf die Wechselplatten. Eine
  neue Appliance lässt sich bei der Ersteinrichtung aus der Datei oder später aus einem Datastore wiederherstellen.
- **Verschlüsselte Sicherungen:** Proxmox VE verschlüsselt jede Sicherung vor dem Versand (AES-256-GCM), auf dem
  Backup-Server, den Wechsel- und Archiv-Platten liegen nur verschlüsselte Daten. Einschalten je Speicher unter
  Einstellungen → Verschlüsselung oder beim Eintragen eines Datastores; ein Schlüssel kann für mehrere Speicher und
  Cluster gelten. Die Appliance verwahrt die Schlüssel (auch in ihrer eigenen Sicherung), trägt sie für
  Wiederherstellungen und Restore-Tests selbst ein und erinnert, bis eine Kopie außer Haus liegt – als
  Papierschlüssel mit QR-Code oder als Datei. Beim Import prüft sie den Fingerabdruck und findet Tippfehler.
- **Löschschutz:** Proxmox VE darf je Speicher nur noch sichern und lesen, aber nichts löschen – übernimmt
  jemand Proxmox VE (Erpressersoftware), bleiben die Sicherungen auf dem Backup-Server. Die letzte Sicherung jedes
  Monats und Jahres lässt sich automatisch schützen (wird weder aufgeräumt noch gelöscht), einzelne Stände per Klick.
  Verschwinden alle Sicherungen eines Servers oder mehr als ein Fünftel eines Datastores auf einmal, kommt Alarm.
- **Mails zu Sicherungsjobs im eigenen Design:** je Proxmox VE umschaltbar („Appliance übernimmt“ unter
  Sicherungsjobs). Die Jobs in Proxmox VE schicken dann keine eigene Mail mehr, die Appliance meldet jeden Lauf mit
  allen Servern, Ergebnis, Dauer und Datenmenge; andere Proxmox-Meldungen bleiben unverändert, zurückstellen geht jederzeit.
- **Überwachung und Benachrichtigung** (E-Mail, Telegram, ntfy, Gotify, Webhook): fehlgeschlagene Aufgaben, Server offline,
  Datastore fast voll, Freigabe weg, Wechselplatte fertig oder überfällig. Telegram: Bot bei @BotFather anlegen,
  ihm schreiben (oder in die Gruppe aufnehmen), „Chat-ID ermitteln“ liest die Chat-ID aus; Screenshots und Berichte
  der Restore-Tests kommen als Bilder und Datei mit.

```
Proxmox-VE-Host
└─ VM pk95-backup (Debian 13)
   ├─ Proxmox Backup Server   Port 8007 (eigene PBS-Oberfläche, root@pam)
   ├─ PK95 ProxBackup         Port 443  (diese Oberfläche, spricht mit PBS und PVE über deren API)
   ├─ /mnt/pk95/<freigabe>    NFS/SMB über systemd-Mount-Units
   └─ USB-Platten             durchgereichter USB-Anschluss → Wechsel-Datastores
```

**Voraussetzungen:** Proxmox VE 8 oder 9, Proxmox Backup Server 4 (die Appliance installiert ihn selbst; ein
vorhandener PBS sollte ebenfalls auf 4.x sein – Wechsel-Datastores und „beim Anstecken kopieren“ gibt es erst dort).

## Installation auf Proxmox (ein Befehl)

Auf dem **Proxmox-Host** als `root` in der Shell (Weboberfläche → Knoten → „Shell“):

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/PhilippKuhlmann/pk95proxbackup/main/install/proxmox.sh)"
```

Das Skript fragt Schritt für Schritt, Enter übernimmt den Vorschlag:

```
1. VM            VM-ID [nächste freie] · Hostname [pk95-backup]
2. Ressourcen    Kerne [2] · RAM [4096] · Systemplatte [32 GB] · Datenplatte [0 = keine] · Speicher
3. Netzwerk      Bridge · VLAN · IP mit Präfix oder dhcp · Gateway · DNS
4. Zugang        root-Passwort (Konsole und PBS-Oberfläche) · SSH-Schlüssel des Hosts übernehmen
```

Danach lädt es das offizielle Debian-13-Cloud-Image (Prüfsumme wird geprüft), legt die VM an (q35, VirtIO,
Gast-Agent, serielle Konsole, Datenplatte mit `backup=0`) und startet sie. Beim ersten Start installiert
cloud-init darin Proxmox Backup Server und PK95 ProxBackup (`install/appliance.sh`). Am Ende stehen
**Adresse und Einrichtungscode** im Terminal – der Code steht auch auf der Konsole der VM.

Braucht cloud-init einen Speicher für „Snippets“ und gibt es keinen, bietet das Skript an, das beim Speicher
`local` einzuschalten.

| Variable | Standard | Bedeutung |
|---|---|---|
| `VMID`, `VM_HOSTNAME` | nächste freie, `pk95-backup` | |
| `CORES` / `MEMORY` / `DISK` | `2` / `4096` / `32` | Kerne, RAM in MB, Systemplatte in GB |
| `DATA_DISK` | `0` | Datenplatte für Sicherungen in GB (0 = keine) |
| `STORAGE` | `local-lvm`, sonst `local-zfs` | Speicher für die Platten |
| `BRIDGE` / `VLAN` / `IP` / `GW` / `DNS` | `vmbr0` / – / `dhcp` / – / – | Netzwerk |
| `ROOT_PASSWORD` | wird erzeugt und angezeigt | root-Passwort der VM |
| `SNIPPETS` | erster Speicher mit Snippets | Ablage für das cloud-init-Skript |

### Auf einem vorhandenen Debian 13 oder PBS

`install/appliance.sh` installiert (oder aktualisiert) die Appliance auf einem laufenden Debian 13 – auch direkt auf
einem vorhandenen Proxmox Backup Server. PBS wird nur installiert, wenn er fehlt (`INSTALL_PBS=0` verhindert das).

```bash
bash -c "$(curl -fsSL https://raw.githubusercontent.com/PhilippKuhlmann/pk95proxbackup/main/install/appliance.sh)"
```

Erneut ausgeführt, holt das Skript die neueste Version. Ab Version 1.5.0 geht das auch in der Oberfläche unter
Einstellungen → Aktualisierung.

### Ersatz für eine ausgefallene Appliance

1. Neue Appliance installieren (wie oben).
2. Bei der Ersteinrichtung „Aus einer Sicherung wiederherstellen“ wählen, die Datei (`.pk95cfg`, unter
   Einstellungen → Sicherung der Appliance → „Als Datei laden“) und das Wiederherstellungs-Passwort angeben.
3. Gibt es die Datei nicht, aber eine Wechselplatte: Konto anlegen, die Platte unter Wechselplatten →
   „Fremde Platte einlesen“ einbinden, dann Einstellungen → Sicherung der Appliance → „Wiederherstellen“.

Danach sind Verbindungen, Pläne, Konten und die PBS-Konfiguration zurück; Datastore-Platten und Freigaben müssen
wieder angeschlossen sein.

## Erste Schritte

1. `https://<IP>` öffnen (selbst signiertes Zertifikat), Einrichtungscode eingeben, Konto anlegen.
2. **Verbindungen → Proxmox VE hinzufügen.** Token auf einem Knoten anlegen:
   `pveum user token add root@pam pk95 --privsep 0` – Token-ID `root@pam!pk95` und Secret eintragen,
   Fingerabdruck mit „Abrufen“ holen und mit dem in PVE vergleichen.
3. Der **PBS dieser Appliance** trägt sich selbst ein. Bei „Adresse für Proxmox VE“ die IP der VM prüfen.
4. **Datastores → Datastore anlegen** (Datenplatte `sdb`, Freigabe oder Verzeichnis), Aufbewahrung festlegen.
5. **Verbindungen → Backup-Speicher** beim Proxmox VE: den Datastore eintragen.
6. **Sicherungsjobs → Job anlegen** (z. B. täglich 21:00, alles außer der Appliance selbst).
7. Optional **Wechselplatten** und **Benachrichtigungen** einrichten.

### Vorhandenen PBS einbinden

Auf dem PBS:

```bash
proxmox-backup-manager user generate-token root@pam pk95
proxmox-backup-manager acl update / Admin --auth-id 'root@pam!pk95'
```

Für NFS/SMB-Freigaben auf diesem PBS braucht die Appliance zusätzlich SSH (Schlüssel steht unter
Einstellungen und im Server-Dialog). Datastores, Wechselplatten, Kopierjobs und Aufgaben laufen allein über die API.

## Wechselplatten

Jede Platte eines Satzes ist ein **Wechsel-Datastore** (removable datastore, PBS ≥ 3.3) mit einem Kopierjob vom
Quell-Datastore (auch von einem anderen PBS). Ablauf:

1. Platte anstecken – PBS hängt sie ein; mit „beim Anstecken kopieren“ startet die Kopie sofort, sonst zur Uhrzeit.
2. Nach der Kopie räumt die Appliance auf der Platte nach den Regeln des Satzes auf, gibt einmal pro Woche Speicher frei
   und hängt die Platte aus.
3. „Fertig – abziehen“ erscheint (auf Wunsch als Benachrichtigung). Platte abziehen und außer Haus bringen.
4. Erinnerung, wenn die letzte Kopie älter als der Wechselrhythmus ist oder eine Platte lange nicht dran war.

**USB an dieser VM:** Unter Wechselplatten → „USB an diese VM durchreichen“ wird der **Anschluss** des Proxmox-Hosts
durchgereicht (nicht das Gerät). Jede Platte, die später in diesem Anschluss steckt, erscheint in der VM – so lässt
sich wechseln, ohne die VM-Konfiguration anzufassen. Die Appliance findet ihre eigene VM über die SMBIOS-UUID.

Eine neue Platte wird beim Hinzufügen **gelöscht** und formatiert (Bestätigung durch Eintippen des Plattennamens).
Platten, auf denen schon ein Wechsel-Datastore liegt, werden nie gelöscht.

### Archiv-Platten

Für einen Stand, der **nicht wieder überschrieben** wird (Jahresabschluss, Bankschließfach, vor einem großen Umbau):
Wechselplatten → „Archiv-Sicherung“. Die Appliance

1. bereitet die Platte als Wechsel-Datastore vor (oder nimmt einen vorhandenen),
2. kopiert einmalig per `POST /pull` – es bleibt kein Kopierjob auf dem Server zurück –, standardmäßig nur die
   neueste Sicherung je VM/CT, auf Wunsch nur ausgewählte Server,
3. liest alles zurück (Verify, empfohlen), merkt sich den Inhalt mit Namen und Zeitpunkten
4. und hängt die Platte aus. Die Meldung sagt, dass sie abgezogen und beschriftet werden kann.

Der Datastore bleibt im PBS eingetragen: Steckt die Platte Jahre später wieder, wird sie erkannt, „Prüfen“ liest sie
erneut vollständig, und unter Wiederherstellen erscheint sie als „Archiv: <Name>“. Älter als ein Jahr: einmal prüfen.

### Fremde Platte einlesen

Archiv-Platten → „Fremde Platte einlesen“ bindet den Datastore einer Platte ein, die ein anderer Backup-Server
beschrieben hat (`reuse-datastore`, nichts wird gelöscht oder formatiert). Angeboten werden Partitionen (ext4/XFS),
die kein Datastore dieses Servers benutzt. Mit Kommandozeilen-Zugang (lokaler PBS oder SSH) hängt die Appliance die
Partition kurz **nur lesend** ein und sucht die Datastore-Ordner (`.chunks`); sonst wird der Ordner von Hand
angegeben – bei Wechselplatten heißt er wie der Datastore auf dem alten Server. Danach:

- **Wiederherstellen:** die Platte erscheint als Quelle „Archiv: <Name>“ (Dateien oder ganze VMs/CTs).
- **Restore-Tests:** das Kalender-Symbol auf der Karte legt einen Plan für diesen Datastore an. Restore-Tests hängen
  USB-Datastores selbst ein und danach wieder aus.
- Verschlüsselte Sicherungen brauchen zum Wiederherstellen den Schlüssel des alten Systems: unter Einstellungen →
  Verschlüsselung importieren (Datei, Papierschlüssel oder `/etc/pve/priv/storage/<speicher>.enc` des alten Clusters).

## NFS und SMB

Freigaben werden als systemd-Mount-Unit (`/etc/systemd/system/mnt-pk95-<kennung>.mount`) unter `/mnt/pk95/<kennung>`
eingehängt, SMB-Zugangsdaten liegen in `/etc/pk95proxbackup/cifs-<kennung>.cred` (0600). Der leere Mountpunkt ist
unveränderlich (`chattr +i`), damit PBS bei fehlender Freigabe nicht ins Systemdateisystem schreibt. PBS schreibt als
`backup` (UID 34): bei NFS braucht das NAS `no_root_squash` bzw. eine passende Zuordnung, SMB wird mit `uid=34` eingehängt.
„Verbindung testen“ hängt testweise ein und schreibt als `backup` eine Datei.

## Sicherheit

- HTTPS mit selbst erzeugtem Zertifikat (`/var/lib/pk95proxbackup/cert.pem`, `key.pem` – durch eigene ersetzbar).
- Anmeldung mit bcrypt, Sitzungscookie `HttpOnly`/`Secure`/`SameSite=Strict`, Sperre nach 10 Fehlversuchen,
  ändernde Anfragen brauchen einen eigenen Header (CSRF-Schutz).
- **Zwei-Faktor-Anmeldung (TOTP):** unter Einstellungen → Konto einschalten, QR-Code mit einer Authenticator-App
  scannen (Microsoft/Google Authenticator, Bitwarden, 1Password …), Code bestätigen. Danach fragt die Anmeldung nach
  Passwort und 6-stelligem Code; jeder Code gilt nur einmal. Dazu gibt es zehn einmalige Wiederherstellungscodes für
  den Fall, dass das Telefon fehlt. Sind Telefon und Codes weg, schaltet root auf der Konsole der VM sie ab:
  `pk95proxbackup -totp-off admin`.
- **Schlüssel der Sicherungen** liegen in der Datenbank der Appliance und in Proxmox VE
  (`/etc/pve/priv/storage/<speicher>.enc`). Ohne Schlüssel sind verschlüsselte Sicherungen verloren: den
  Papierschlüssel getrennt von den Platten aufbewahren. Löschen in der Appliance geht nur mit Passwort und nur,
  solange kein Speicher den Schlüssel verwendet.
- **Löschschutz** (Einstellungen → Löschschutz): Der Token, mit dem Proxmox VE sich am Backup-Server anmeldet, hat
  dann die Rollen `DatastoreBackup` und `DatastoreReader` statt `DatastorePowerUser`. Aufbewahrung gehört dann in den
  Datastore (Prune-Job des Backup-Servers); eine eigene Aufbewahrung im Sicherungsjob von Proxmox VE schlägt fehl –
  die Seite zeigt solche Jobs an. Feste Datastores werden alle zehn Minuten auf verschwundene Sicherungen geprüft.
- PBS und PVE werden über den **SHA-256-Fingerabdruck** ihres Zertifikats gepinnt; ändert er sich, bricht die Verbindung ab.
- API-Tokens und Passwörter liegen in der SQLite-Datenbank unter `/var/lib/pk95proxbackup` (nur root).
- Port 443 nicht ins Internet freigeben; von außen per VPN.

## Entwicklung

```bash
go test -race ./...          # PBS und PVE simuliert (internal/fake)
PK95PB_DEMO=1 PK95PB_NO_TLS=1 PK95PB_LISTEN=127.0.0.1:18095 PK95PB_HTTP_REDIRECT= PK95PB_DATA_DIR=data/demo go run .
```

Der Demo-Modus startet simulierte PBS- und PVE-Server mit Beispieldaten (Gäste, Datastores, Wechselplatten), die
Ersteinrichtung läuft wie echt (Code in `data/demo/setup-code`).

Release: Tag `vX.Y.Z` pushen – der Workflow baut `pk95proxbackup-linux-amd64/arm64` und `SHA256SUMS`, die
`install/appliance.sh` herunterlädt.

| Variable | Standard | Bedeutung |
|---|---|---|
| `PK95PB_LISTEN` | `:443` | HTTPS |
| `PK95PB_HTTP_REDIRECT` | `:80` | leitet auf HTTPS um (leer = aus) |
| `PK95PB_DATA_DIR` | `/var/lib/pk95proxbackup` | Datenbank, Zertifikat, SSH-Schlüssel, Einrichtungscode |
| `PK95PB_NO_TLS` | – | `1` = nur HTTP (Entwicklung) |
| `PK95PB_DEMO` | – | `1` = simulierte Server |

Eigene Einstellungen für den Dienst in `/etc/pk95proxbackup/env`.
