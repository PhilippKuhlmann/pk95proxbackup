# PK95 ProxBackup

Backup-Appliance für Proxmox im Design von PK95 Core. Eine Debian-13-VM mit **Proxmox Backup Server**
und einer eigenen Weboberfläche, über die du Sicherungen verwaltest und einrichtest:

- **Sichern:** Sicherungsjobs in Proxmox VE anlegen, bearbeiten und sofort starten; einzelne VMs und
  Container „jetzt sichern“; Übersicht, welche Gäste wann wohin gesichert wurden und welche in keinem Job sind.
- **Sicherungskette je Gast** (VMs & Container): Sicherung → Kopie auf einem zweiten Backup-Server → USB-Platte →
  Restore-Test, jedes Glied mit Alter und Ampel. Was auf USB-Platten liegt, merkt sich die Appliance beim Lesen und nach
  jeder Kopie – auch abgezogene Platten zählen. Reißt die Kette (z. B. gesichert, aber seit drei Wochen auf keiner
  Platte), erscheint ein Hinweis in der Übersicht.
- **Wiederherstellen** aus jeder Sicherung als neuer Gast (das Original bleibt unberührt).
- **Einzelne Disk zurückholen:** aus einer VM-Sicherung nur eine Platte zurückholen und zusätzlich an eine VM hängen
  (die eigene oder eine andere auf demselben Proxmox VE) – an der VM wird nichts überschrieben, eine laufende VM sieht
  die Disk sofort. Proxmox VE stellt nur ganze VMs wieder her; die Appliance spielt die Sicherung darum unter einer
  freien ID auf dem Knoten der Ziel-VM zurück, hängt die Disk um (`move_disk` an eine andere VM) und löscht die
  Hilfs-VM mit den übrigen Disks wieder. Der Dialog zeigt den nötigen Platz und den Fortschritt; braucht das Recht
  VM.Config.Disk und einen Speicher, der Disks umhängen kann (LVM, LVM-Thin, ZFS, Verzeichnis, Ceph …).
- **Dateien aus Sicherungen:** Ordner durchklicken, Dateien einzeln oder Ordner als ZIP laden. Der geöffnete Ordner
  bleibt beim Wechsel des Stands offen (fehlt er dort, zeigt die Appliance den Ordner darüber). **Versionen** einer Datei:
  in welchen Ständen es sie gibt, wann sie sich geändert hat oder verschwunden ist – jede Fassung direkt ladbar.
- **Datastores** auf einer freien (virtuellen) Platte, einer **NFS-/SMB-Freigabe** oder in einem Verzeichnis,
  mit Aufbewahrung (keep-last/daily/weekly/…), Prüfung (Verify) und Speicherfreigabe (Garbage Collection) nach Zeitplan.
- **Cloud-Speicher (S3):** Datastore im Bucket eines S3-Anbieters (Proxmox Backup Server 4) als Kopie außer Haus.
  Der Assistent unter Datastores → „Cloud (S3)“ kennt Hetzner Object Storage, Backblaze B2, Wasabi, Amazon S3,
  Cloudflare R2 und eigene S3-Server (MinIO, Ceph …): Anbieter und Schlüssel eintragen, Bucket wählen und prüfen,
  Cache-Verzeichnis (64–128 GiB empfohlen), Aufbewahrung und auf Wunsch den nächtlichen Kopierjob anlegen. Falsche
  Schlüssel fallen sofort auf; der Secret Key wird nur an den Backup-Server weitergegeben. Die Sicherungskette zählt
  die Cloud als Kopie außer Haus.
- **Bandbreite und Zeitfenster:** je Backup-Server Grenzen für Empfangen und Senden – immer oder zu festen Zeiten
  (z. B. Mo–Fr 7–19 Uhr), für alle oder bestimmte Adressen (Traffic Control von PBS). Je Kopierjob eine Obergrenze und
  „spätestens fertig um“: Läuft die Kopie dann noch, hält die Appliance sie an, der nächste Lauf setzt fort. Für
  Cloud-Ziele ein Upload-Limit.
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
- **Datastore vergrößern:** Läuft ein Backup-Server als VM in einem verbundenen Proxmox VE (oder die Appliance
  selbst), vergrößert ein Klick beim Datastore die virtuelle Platte in Proxmox VE und danach Partition und Dateisystem
  (ext4, xfs) auf dem Backup-Server – im laufenden Betrieb. Braucht den SSH-Zugang zum Backup-Server und das Recht
  VM.Config.Disk; Wechsel-, Archiv- und Cloud-Datastores sowie Datastores auf LVM oder ZFS bleiben außen vor.
- **Wer belegt den Platz?** (Datastores → Diagramm-Symbol, auch aus der Platzprognose): PBS dedupliziert über alle
  Sicherungen eines Datastores, die Größe einer Sicherung sagt deshalb wenig. Einmal am Tag zählt ein Skript auf dem
  Backup-Server (Perl, ist auf jedem Debian dabei) aus den Indexdateien aller Stände, welche Datenblöcke welcher Gast
  nutzt: **Anteil** (gemeinsame Blöcke zählen anteilig, alles zusammen ergibt den belegten Platz), **frei beim
  Löschen** (nur dieser Gast nutzt die Blöcke), Größe der neuesten Sicherung, Zahl der Stände und **Wachstum über 30
  Tage** aus dem täglichen Verlauf. Gäste, die es in Proxmox VE nicht mehr gibt, sind markiert – ihre alten Stände
  sind oft der schnellste Weg zu Platz. Braucht beim externen PBS den SSH-Zugang; Wechsel-, Archiv- und
  Cloud-Datastores bleiben außen vor.
- **Sicherungen auf der Übersicht:** je Sicherungsjob in Proxmox VE, Kopierjob und Wechselplatten-Satz eine Zeile mit
  Link zur Einstellung und den letzten 14 Läufen als farbige Kästchen (grün, gelb bei einzelnen Fehlern, rot);
  der Tooltip zeigt Zeit, Ergebnis, gesicherte Server, Dauer und Datenmenge. Die Läufe eines Sicherungsjobs auf allen
  Knoten zählen als ein Lauf; beim ersten Start liest die Appliance die vorhandenen Läufe rückwirkend ein.
- **Platzprognose:** Die Appliance merkt sich täglich die Belegung jedes Datastores (beim ersten Start ein Jahr
  rückwirkend aus dem Verlauf des Backup-Servers, Wechselplatten an den Tagen, an denen sie stecken) und rechnet das
  Wachstum der letzten Wochen weiter. Datastores zeigt „voll ca. …“, ein Klick öffnet Verlauf, Prognose und was hilft
  (Speicherfreigabe, kürzere Aufbewahrung, nur die neuesten Stände auf die Platten, mehr Platz). 30 und 7 Tage vorher
  kommt eine Meldung, die Übersicht zeigt es ebenfalls.
- **Festplatten (SMART):** alle Platten der Backup-Server und der Proxmox-VE-Knoten werden täglich und gleich nach dem
  Einstecken geprüft – schwebende oder unlesbare Sektoren, ersetzte Sektoren, Selbsttest, SSD-Verschleiß, Temperatur.
  Lesefehler im Flash-Speicher einer SSD: ein oder zwei alte, zu denen seit 30 Tagen keiner dazukam, sind in Ordnung
  (nur ein Hinweis im Befund); neu hinzugekommene oder ab drei sind gelb (beobachten); ab 10 oder fünf neue in
  30 Tagen sind rot (bald tauschen).
  Wird eine Platte schlechter, kommt eine Meldung. Archiv-Platten, die über ein Jahr nicht angesteckt waren, erscheinen
  als Hinweis in der Übersicht.
- **Abgezogen ohne Aushängen:** Wird eine Wechsel- oder Archiv-Platte im eingehängten Zustand gezogen, meldet die
  Appliance das, entfernt den verwaisten Mount und liest die Platte nach der nächsten Kopie zur Kontrolle zurück.
  Bleibt ein Mount zurück, obwohl der Backup-Server den Datastore schon als ausgehängt meldet, löst sie ihn ebenfalls
  (mit Kommandozeilen-Zugang zum Server).
- **Fremde Platten:** Steckt an einem Backup-Server mit Wechselplatten eine USB-Platte, die zu keinem Datastore gehört
  (neu, von anderswo oder eine Platte des Satzes, deren Datastore nicht mehr passt), zeigt die Seite Wechselplatten sie
  an – zum Hinzufügen oder um sie als fehlende Platte des Satzes neu einzurichten (formatieren und neu befüllen).
- **Zustand der Backup-Server:** laufende und installierte PBS-Version („Dienste neu laden“ per Klick), Neustart nötig
  wegen neuem Kernel, verfügbare Updates – geprüft alle sechs Stunden, zu sehen unter Verbindungen.
- **Systemupdates:** unter Einstellungen → Aktualisierung je Backup-Server nach Updates suchen, die Debian- und
  Proxmox-Pakete per Klick einspielen (`apt dist-upgrade` im Hintergrund, geänderte Konfigurationsdateien bleiben,
  das Protokoll läuft mit) und den Server neu starten. Der Neustart fragt nach, wenn gerade Sicherungen, Kopien oder
  Prüfungen laufen. Bei einem angebundenen PBS braucht das Einspielen den SSH-Zugang (als root); er lässt sich direkt
  dort einrichten – die Appliance zeigt den Befehl für ihren Schlüssel und schaltet SSH erst ein, wenn die Verbindung
  klappt.
- **Automatische Updates:** je Backup-Server nach Plan (Wochentage, Uhrzeit) einspielen und bei Bedarf (neuer
  Kernel) neu starten. Läuft dabei noch eine Sicherung, Kopie oder Prüfung, wartet die Appliance bis zu drei Stunden;
  sonst verschiebt sie den Neustart auf den nächsten Termin und meldet das.
- **Monatsbericht:** am 1. jedes Monats per Mail, Telegram oder Push – Sicherungen je Server, USB-Kopien,
  Restore-Tests, Festplatten, Backup-Server, Archiv-Platten und alle Hinweise des Vormonats, vollständig als
  HTML-Datei (druckbar als PDF); jederzeit unter Benachrichtigungen → Monatsbericht ansehbar.
- **Update per Klick:** die Appliance sieht zweimal täglich bei GitHub nach (übertragen wird nur die Versionsnummer),
  prüft die Prüfsumme, testet das neue Programm vor dem Einsetzen und kehrt von selbst zur alten Version zurück, wenn
  die neue dreimal nicht startet. Auf Wunsch installiert sie neue Versionen selbst nach Plan (Wochentage, Uhrzeit);
  kopiert sie gerade auf eine USB- oder Archiv-Platte, läuft ein Restore-Test oder ein Systemupdate, wartet sie bis zu
  drei Stunden, sonst folgt das Update zum nächsten Termin. Nach dem Update meldet sie, was neu ist.
- **Proxmox-VE-Hosts sichern:** Proxmox VE sichert seine Knoten nicht selbst. Die Appliance meldet sich jede Nacht
  per SSH als root an jedem Knoten an und ruft dort `proxmox-backup-client` auf – mit dem Zugang, den Proxmox VE für
  den gewählten PBS-Speicher ohnehin hat (`/etc/pve/priv/storage/<id>.pw`, ein vorhandener Schlüssel verschlüsselt
  auch diese Sicherung); kein Kennwort verlässt den Knoten. Gesichert werden `/etc` samt `/etc/pve`, `/root` (ohne
  ISO- und Disk-Images) und ein Ordner „Systeminfo“ mit allem für den Neuaufbau: Versionen, Netzwerk, Platten,
  ZFS/LVM, Pakete, Bootloader, Crontabs und eine konsistente Kopie von `config.db`. Die Sicherung liegt als
  `host/<knoten>` neben den Gästen und wandert mit auf zweiten Server und Wechselplatten. Unter Sichern →
  Proxmox-VE-Hosts: Stände je Knoten, Dateien durchsuchen und laden (nur Administratoren), Befehle zum Zurückholen
  und eine Anleitung zum Neuaufbau. Schlägt eine Sicherung fehl, versucht es die Appliance stündlich bis zu dreimal
  und meldet sich dann.
- **Notfallhandbuch** (Verwaltung → Notfallhandbuch): ein druckbares Dokument für den Tag, an dem alles weg ist –
  Ansprechpartner, Reihenfolge beim Wiederherstellen und wo was liegt (eigene Angaben), was bei welchem Ausfall zu tun
  ist (Gast kaputt, Knoten weg, Appliance weg, alles weg), Adressen und Fingerabdrücke der Backup-Server, Datastores,
  Proxmox-VE-Knoten mit Host-Sicherung, Speicher mit Verschlüsselung und Löschschutz, wo welcher Server gesichert ist,
  Wechselplatten mit Wechselplan, Archiv-Platten, Schlüssel-Fingerabdrücke und Konten – ohne Passwörter und ohne
  Schlüssel. Nach „Als ausgedruckt vermerken“ prüft die Appliance alle sechs Stunden, ob der Ausdruck noch stimmt,
  und meldet, welche Abschnitte sich geändert haben; nach einem halben Jahr erinnert sie ans Durchsehen.
- **Sicherung der Appliance selbst:** Datenbank, SSH-Schlüssel, Zertifikat und die Konfiguration des lokalen PBS
  (`/etc/proxmox-backup`, Mount-Units) werden täglich mit einem Wiederherstellungs-Passwort verschlüsselt (AES-256-GCM,
  scrypt) als Gruppe `host/pk95proxbackup` in den Datastore geschrieben und wandern so auf die Wechselplatten. Eine
  neue Appliance lässt sich bei der Ersteinrichtung aus der Datei oder später aus einem Datastore wiederherstellen.
- **Verschlüsselte Sicherungen:** Proxmox VE verschlüsselt jede Sicherung vor dem Versand (AES-256-GCM), auf dem
  Backup-Server, den Wechsel- und Archiv-Platten liegen nur verschlüsselte Daten. Einschalten je Speicher unter
  Schutz → Verschlüsselung oder beim Eintragen eines Datastores; ein Schlüssel kann für mehrere Speicher und
  Cluster gelten. Die Appliance verwahrt die Schlüssel (auch in ihrer eigenen Sicherung), trägt sie für
  Wiederherstellungen und Restore-Tests selbst ein und erinnert, bis eine Kopie außer Haus liegt – als
  Papierschlüssel mit QR-Code oder als Datei. Beim Import prüft sie den Fingerabdruck und findet Tippfehler.
- **Löschschutz:** Proxmox VE darf je Speicher nur noch sichern und lesen, aber nichts löschen – übernimmt
  jemand Proxmox VE (Erpressersoftware), bleiben die Sicherungen auf dem Backup-Server. Die letzte Sicherung jedes
  Monats und Jahres lässt sich automatisch schützen (wird weder aufgeräumt noch gelöscht), einzelne Stände per Klick.
  Verschwinden alle Sicherungen eines Servers oder mehr als ein Fünftel eines Datastores auf einmal, kommt Alarm.
- **Prüfergebnisse verständlich:** Schlägt eine Prüfung (Verify) fehl, liest die Appliance das Protokoll. Stände, die
  das Aufräumen während der stundenlangen Prüfung entfernt hat, gelten nicht als Fehler („Ohne Befund“ unter Aufgaben,
  keine Fehlermeldung). Echte Schäden erscheinen je Server mit Datum und Fehler – in der Übersicht, beim Datastore und
  als Meldung, mit dem Rat, die betroffenen Server neu zu sichern.
- **Auffällige Sicherungen:** Die Appliance liest aus jeder Sicherung auf den Backup-Servern, wie viele neue Daten
  hinzukamen. Sind es mindestens 1 GiB und mehr als das Fünffache des Üblichen (Median der letzten 14 Sicherungen) –
  typisch für einen Verschlüsselungstrojaner – oder ist eine Sicherung nur noch halb so groß wie sonst, kommt eine
  Warnung (Übersicht, VMs & Container, Mail/Telegram/Push). Die letzte unauffällige Sicherung des Servers wird
  geschützt, bis jemand die Meldung als geprüft markiert; beim Wiederherstellen sind beide Stände markiert.
- **Mails zu Sicherungsjobs im eigenen Design:** je Proxmox VE umschaltbar („Appliance übernimmt“ unter
  Sicherungsjobs). Die Jobs in Proxmox VE schicken dann keine eigene Mail mehr, die Appliance meldet jeden Lauf mit
  allen Servern, Ergebnis, Dauer und Datenmenge; andere Proxmox-Meldungen bleiben unverändert, zurückstellen geht jederzeit.
- **Überwachung und Benachrichtigung** (E-Mail, Telegram, ntfy, Gotify, Webhook): fehlgeschlagene Aufgaben, Server offline,
  Datastore fast voll, Freigabe weg, Wechselplatte fertig oder überfällig. Telegram: Bot bei @BotFather anlegen,
  ihm schreiben (oder in die Gruppe aufnehmen), „Chat-ID ermitteln“ liest die Chat-ID aus; Screenshots und Berichte
  der Restore-Tests kommen als Bilder und Datei mit.
- **Was wohin** (Benachrichtigungen → Was wohin): je Kanal (E-Mail, Telegram, Push) und Art – Sicherungen, Kopierjobs,
  USB- und Archiv-Platten, Prüfungen, Auffällige Sicherungen, Speicherplatz, Festplatten, Erreichbarkeit, Updates,
  Konten, Änderungen an der Einrichtung, Monatsbericht – **Alles**, **Nur Probleme** (Warnungen und Fehler, z. B.
  Updates nur, wenn etwas schiefgeht) oder **Aus**; dazu je Kanal eine Mindeststufe.
- **Ruhe bei Updates und Neustarts:** Je Update kommt eine Meldung mit dem Ergebnis („Updates eingespielt und neu
  gestartet, nach etwa 2 Minuten wieder erreichbar“) statt einer je Schritt; die Schritte stehen unter Ereignisse.
  Startet die Appliance einen Backup-Server selbst neu, meldet sie ihn nicht als ausgefallen – erst wenn er nach
  15 Minuten nicht zurück ist.
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
- **Löschschutz** (Schutz → Löschschutz): Der Token, mit dem Proxmox VE sich am Backup-Server anmeldet, hat
  dann die Rollen `DatastoreBackup` und `DatastoreReader` statt `DatastorePowerUser`. Aufbewahrung gehört dann in den
  Datastore (Prune-Job des Backup-Servers); eine eigene Aufbewahrung im Sicherungsjob von Proxmox VE schlägt fehl –
  die Seite zeigt solche Jobs an. Feste Datastores werden alle zehn Minuten auf verschwundene Sicherungen geprüft.
- **Konten und Rollen** (Einstellungen → Konten): *Administrator* darf alles; *Bediener* erledigt den Alltag (jetzt
  sichern, wiederherstellen, Restore-Tests und Kopien starten, USB-Platten ein- und aushängen, prüfen, freigeben), aber
  keine Einstellungen; *Dateien wiederherstellen* (für Kunden) sieht nur Übersicht und Wiederherstellen und darf
  einzelne Dateien und Ordner aus Sicherungen herunterladen, aber keine ganzen VMs zurückspielen und nichts starten
  oder ändern; *Nur lesen* sieht alles, ändert nichts und liest weder Dateien aus Sicherungen noch Schlüssel.
  Je Konto lässt sich die Zwei-Faktor-Anmeldung vorschreiben – bis sie eingerichtet ist, sieht das Konto nur „Mein
  Konto“. Das letzte Administrator-Konto lässt sich weder löschen noch herabstufen. Bestehende Konten sind nach dem
  Update Administratoren.
- **Protokoll** (Einstellungen → Protokoll): jede Änderung, An- und Abmeldungen, abgelehnte Versuche und jeder Zugriff
  auf Schlüssel und Dateien aus Sicherungen, mit Konto, Uhrzeit und Adresse; 400 Tage aufbewahrt.
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
