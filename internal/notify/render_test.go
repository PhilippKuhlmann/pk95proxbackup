package notify

import (
	"strings"
	"testing"
)

func sample(status string) Message {
	return Message{Severity: "warning", Source: "Cluster", Title: "Sicherung mit Fehlern: 1 von 2 gesichert", Body: "1 von 2", Host: "pk95", URL: "https://10.0.0.5",
		Details: &Details{Status: status, RowsTitle: "Gesicherte VMs und Container", Link: "/sicherungen",
			Facts: []Fact{{Label: "Ziel", Value: "pbs-backup"}},
			Rows:  []Row{{Kind: "VM", ID: "100", Name: "dc<01>", Status: "ok", Size: "32 GiB"}, {Kind: "CT", ID: "110", Name: "pihole", Status: "error", Info: "kaputt"}},
			Log:   []string{"ERROR: Backup of VM 110 failed - kaputt"}}}
}

func TestHTMLColorsAndContent(t *testing.T) {
	for status, color := range map[string]string{"ok": "#16a34a", "warning": "#d97706", "error": "#dc2626"} {
		h := HTML(sample(status))
		if !strings.Contains(h, "border-left:6px solid "+color) {
			t.Errorf("%s: band color %s missing", status, color)
		}
	}
	h := HTML(sample("warning"))
	for _, want := range []string{"dc&lt;01&gt;", "pihole", "kaputt", "pbs-backup", "https://10.0.0.5/sicherungen", "1 erfolgreich", "1 fehlgeschlagen", "Technische Details für Ihren IT-Betreuer"} {
		if !strings.Contains(h, want) {
			t.Errorf("html misses %q", want)
		}
	}
	if strings.Contains(h, "dc<01>") {
		t.Error("guest name not escaped")
	}
	if s := Subject(sample("error")); !strings.Contains(s, "❌") {
		t.Errorf("subject %q", s)
	}
	txt := Text(sample("warning"))
	if !strings.Contains(txt, "FEHL CT 110 pihole – kaputt") || !strings.Contains(txt, "OK   VM 100 dc<01> – 32 GiB") {
		t.Errorf("text:\n%s", txt)
	}
}
