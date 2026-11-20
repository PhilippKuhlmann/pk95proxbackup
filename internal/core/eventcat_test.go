package core

import "testing"

// Titles as the appliance writes them, and the group each lands in.
func TestEventCategory(t *testing.T) {
	for _, c := range []struct{ source, title, want string }{
		{"PBS01", "Datastore local angelegt", "changes"},
		{"PBS01", "Datastore local entfernt", "changes"},
		{"PBS01", "Datastore local zu 80 % belegt", "space"},
		{"PBS01", "Datastore local ist voraussichtlich in 20 Tagen voll", "space"},
		{"PBS01", "Datastore local vergrößert", "changes"},
		{"Cluster", "Datensicherung unvollständig", "backups"},
		{"Appliance", "Konfiguration der Appliance nicht gesichert", "backups"},
		{"PBS01", "Kopierjob pk95-sync-x um 06:00 angehalten", "copies"},
		{"USB-Sicherung", "Falsche Festplatte angesteckt: „USB2“", "usb"},
		{"Archiv-Sicherung", "Archiv-Sicherung „Jahr 2026“ fehlgeschlagen", "usb"},
		{"Wiederherstellungstest", "Wiederherstellungstest konnte nicht starten", "checks"},
		{"PBS01", "Beschädigte Sicherungen auf PBS01 / local", "checks"},
		{"PBS01", "Prüfjob fehlgeschlagen", "checks"},
		{"Sicherungen", "Auffällige Sicherung: VM 102 (nextcloud)", "protection"},
		{"PBS01", "37 Sicherungen auf local gelöscht", "protection"},
		{"Cluster", "Löschschutz für pbs01 ausgeschaltet", "protection"},
		{"srv-pve01", "Festplatte bald tauschen: Samsung SSD 990 PRO", "disks"},
		{"PBS01", "Backup-Server nicht erreichbar", "reach"},
		{"Freigaben", "Freigabe NAS ist nicht mehr eingehängt", "reach"},
		{"Appliance", "Version 1.29.0 verfügbar", "updates"},
		{"PBS01", "Neustart nötig: neuer Kernel installiert", "updates"},
		{"PBS01", "Automatische Updates ausgefallen", "updates"},
		{"Appliance", "Zwei-Faktor-Anmeldung ausgeschaltet", "security"},
		{"Appliance", "Konto „kunde“ angelegt", "security"},
		{"Monatsbericht", "Monatsbericht Oktober 2026 versandt", "report"},
		{"PBS01", "Speicher freigeben fehlgeschlagen", "tasks"},
		{"Cluster", "Sicherungsjob gespeichert", "changes"},
	} {
		if got := EventCategory(c.source, c.title); got != c.want {
			t.Errorf("%q / %q: got %s, want %s", c.source, c.title, got, c.want)
		}
	}
}
