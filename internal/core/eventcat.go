package core

import (
	"regexp"
	"strings"
)

// eventRules sort a message into a notification category (notify.Categories) by where it
// comes from and its title. The first rule that matches wins; everything else counts as a
// change of the set-up.
var eventRules = []struct {
	cat   string
	match func(source, title string) bool
}{
	{"report", func(s, t string) bool { return s == "Monatsbericht" || strings.HasPrefix(t, "Monatsbericht") }},
	{"protection", func(s, t string) bool {
		return s == anomalySource && strings.HasPrefix(t, "Auffällige Sicherung") || strings.HasPrefix(t, "Löschschutz") || massDeleteRe.MatchString(t)
	}},
	{"checks", func(s, t string) bool {
		return s == "Wiederherstellungstest" || strings.HasPrefix(t, "Beschädigte Sicherungen") || strings.HasPrefix(t, "Prüfung von") ||
			strings.HasPrefix(t, "Prüfjob") || strings.HasPrefix(t, "Prüfung fehlgeschlagen")
	}},
	{"usb", func(s, t string) bool {
		return s == rotationSource || s == archiveSource || s == "Wechselplatten" || s == "Platten" || strings.HasPrefix(t, "Archiv")
	}},
	{"copies", func(s, t string) bool { return strings.HasPrefix(t, "Kopierjob") }},
	{"backups", func(s, t string) bool {
		return strings.HasPrefix(t, "Datensicherung") || strings.HasPrefix(t, "Sicherung fehlgeschlagen") || strings.HasPrefix(t, "Konfiguration der Appliance")
	}},
	{"reach", func(s, t string) bool {
		return strings.Contains(t, "nicht erreichbar") || strings.Contains(t, "wieder erreichbar") || strings.Contains(t, "eingehängt")
	}},
	{"space", func(s, t string) bool {
		return strings.HasPrefix(t, "Datastore") && (strings.Contains(t, " voll") || strings.Contains(t, "belegt"))
	}},
	{"disks", func(s, t string) bool { return strings.HasPrefix(t, "Festplatte") }},
	{"updates", func(s, t string) bool {
		return strings.Contains(t, "Update") || strings.HasPrefix(t, "Version ") || strings.HasPrefix(t, "Auf Version") ||
			strings.Contains(t, "Neustart") || strings.HasPrefix(t, "Dienste")
	}},
	{"security", func(s, t string) bool {
		return strings.Contains(t, "Anmeldung") || strings.HasPrefix(t, "Konto") || strings.HasPrefix(t, "Passwort") ||
			strings.HasPrefix(t, "Rolle") || strings.HasPrefix(t, "Schlüssel") || strings.HasPrefix(t, "Wiederherstellungs-Passwort")
	}},
	{"tasks", func(s, t string) bool { return strings.HasSuffix(t, " fehlgeschlagen") }},
}

var massDeleteRe = regexp.MustCompile(`^\d+ Sicherungen auf .+ gelöscht$`)

// EventCategory names the notification category of a message.
func EventCategory(source, title string) string {
	for _, r := range eventRules {
		if r.match(source, title) {
			return r.cat
		}
	}
	return "changes"
}
