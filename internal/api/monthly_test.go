package api_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/core"
	"pk95proxbackup/internal/notify"
)

func TestMonthlyReport(t *testing.T) {
	cl, c := setup(t)
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	// Two nights: dc01 fails once, fileserver always fine; one USB copy.
	rows := func(dcOK bool) []notify.Row {
		st, info := "ok", ""
		if !dcOK {
			st, info = "error", "VM 100 - unable to freeze"
		}
		return []notify.Row{{Kind: "VM", ID: "100", Name: "dc01", Status: st, Info: info}, {Kind: "VM", ID: "101", Name: "fileserver", Status: "ok"}}
	}
	c.EventDetails("warning", "Cluster", "Datensicherung unvollständig", "", &notify.Details{Status: "warning", RowsTitle: "Gesicherte Server", Rows: rows(false)})
	c.EventDetails("info", "Cluster", "Datensicherung erfolgreich", "", &notify.Details{Status: "ok", RowsTitle: "Gesicherte Server", Rows: rows(true)})
	c.EventDetails("info", "USB-Sicherung", "USB-Sicherung auf „Platte A (gerade Tage)“ fertig", "", &notify.Details{Status: "ok", Facts: []notify.Fact{{Label: "Festplatte", Value: "„Platte A (gerade Tage)“"}}})
	month := time.Now().Format("2006-01")

	rep, err := c.BuildMonthlyReport(t.Context(), month)
	if err != nil {
		t.Fatal(err)
	}
	if rep.BackupRuns != 2 || rep.BackupRunsOK != 1 || len(rep.Guests) != 2 {
		t.Fatalf("backups: %+v", rep)
	}
	if g := rep.Guests[0]; g.ID != "100" || g.OK != 1 || g.Failed != 1 || g.Status != "warning" {
		t.Fatalf("dc01: %+v", g)
	}
	var a core.USBMonth
	for _, u := range rep.USB {
		if strings.HasPrefix(u.Label, "Platte A") {
			a = u
		}
	}
	if a.Copies != 1 || a.Set != "USB täglich" {
		t.Fatalf("usb: %+v", rep.USB)
	}

	resp, err := cl.http.Get(cl.base + "/api/report/monthly?month=" + month)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	html := string(body)
	if resp.StatusCode != 200 || !strings.Contains(html, "Monatsbericht") || !strings.Contains(html, "dc01") || !strings.Contains(html, "fileserver") || !strings.Contains(html, "Platte A") {
		t.Fatalf("report page: %d\n%.400s", resp.StatusCode, html)
	}

	// Send: by Telegram (simulated), also when only errors are reported otherwise.
	ns := notify.Defaults()
	ns.MinSeverity = "critical"
	ns.TelegramEnabled, ns.TelegramToken, ns.TelegramChat = true, "123456:ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghij", "184467001"
	_ = c.St.SetSetting("notify", ns)
	if code, out := cl.do("POST", "/api/report/monthly/send?month="+month, nil, true); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/report/monthly/settings", map[string]any{"enabled": true, "to": "kein-mail"}, true); code != 422 {
		t.Fatalf("bad address: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/report/monthly/settings", map[string]any{"enabled": true, "to": "chef@example.org"}, true); code != 200 || out["enabled"] != true {
		t.Fatalf("settings: %d %v", code, out)
	}
}
