package notify

import "slices"

// Category is a kind of message the user can switch on or off per channel.
type Category struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
}

// Categories in the order the settings page shows them.
var Categories = []Category{
	{"backups", "Sicherungen", "Ergebnis der Sicherungsjobs in Proxmox VE, Sicherung der Appliance-Konfiguration"},
	{"copies", "Kopierjobs", "Kopien zwischen Backup-Servern und in die Cloud"},
	{"usb", "USB- und Archiv-Platten", "Kopie fertig, Wechsel fällig, falsche oder beschädigte Platte"},
	{"checks", "Prüfungen und Restore-Tests", "Beschädigte Sicherungen, Ergebnis der Restore-Tests"},
	{"protection", "Auffällige Sicherungen und Löschschutz", "Ungewöhnlich viele neue Daten, viele Sicherungen auf einmal gelöscht"},
	{"space", "Speicherplatz", "Datastore bald oder ganz voll"},
	{"disks", "Festplatten", "SMART-Werte der Platten unter den Sicherungen"},
	{"reach", "Erreichbarkeit", "Backup-Server, Proxmox VE oder Freigabe nicht erreichbar"},
	{"tasks", "Andere fehlgeschlagene Aufgaben", "Fehler, die in keine andere Gruppe gehören"},
	{"updates", "Updates und Neustarts", "Neue Versionen, eingespielte Updates, Neustarts"},
	{"security", "Anmeldung, Konten und Schlüssel", "Zwei-Faktor-Anmeldung, Konten, Passwörter, Schlüssel"},
	{"changes", "Änderungen an der Einrichtung", "Datastore angelegt, Job gespeichert, Speicher eingetragen …"},
	{"report", "Monatsbericht", "Der Bericht am 1. jedes Monats"},
}

// Channel names in Settings.Channels.
const (
	ChannelMail     = "mail"
	ChannelTelegram = "telegram"
	ChannelHook     = "hook"
)

// Levels of a category in one channel.
const (
	LevelAll      = "all"      // every message of this kind
	LevelProblems = "problems" // warnings and errors only
	LevelOff      = "off"      // nothing
)

// ChannelPrefs say which messages one channel gets.
type ChannelPrefs struct {
	MinSeverity string            `json:"min_severity,omitempty"` // "" = the general setting
	Off         []string          `json:"off,omitempty"`          // categories switched off (older settings)
	Levels      map[string]string `json:"levels,omitempty"`       // category → all, problems, off
}

// Level is the level of a category in this channel.
func (p ChannelPrefs) Level(cat string) string {
	if l := p.Levels[cat]; l == LevelAll || l == LevelProblems || l == LevelOff {
		return l
	}
	if slices.Contains(p.Off, cat) {
		return LevelOff
	}
	return LevelAll
}

// ChannelWants reports whether a channel gets a message. Messages without category (test
// messages) always go out; the monthly report ignores the severity.
func (s Settings) ChannelWants(ch string, m Message) bool {
	if m.Category == "" {
		return true
	}
	p := s.Channels[ch]
	switch p.Level(m.Category) {
	case LevelOff:
		return false
	case LevelProblems:
		if rank[m.Severity] < rank["warning"] {
			return false
		}
	}
	if m.Category == "report" {
		return true
	}
	min := p.MinSeverity
	if min == "" {
		min = s.MinSeverity
	}
	return rank[m.Severity] >= rank[min]
}

// WantsAny reports whether at least one enabled channel gets the message.
func (s Settings) WantsAny(m Message) bool {
	return (s.MailEnabled && s.ChannelWants(ChannelMail, m)) || (s.TelegramEnabled && s.ChannelWants(ChannelTelegram, m)) ||
		(s.HookEnabled && s.ChannelWants(ChannelHook, m))
}
