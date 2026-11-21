package api_test

import (
	"io"
	"strings"
	"testing"
	"time"

	"pk95proxbackup/internal/fake"
)

func TestProofDocument(t *testing.T) {
	fake.SeedHistory = true
	t.Cleanup(func() { fake.SeedHistory = false })
	admin := login(t)
	from := time.Now().AddDate(0, 0, -30).Format("2006-01-02")
	to := time.Now().Format("2006-01-02")
	resp, err := admin.http.Get(admin.base + "/api/proof?from=" + from + "&to=" + to + "&for=Muster+GmbH&guests=vm/102,vm/100")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	doc := string(b)
	if resp.StatusCode != 200 {
		t.Fatalf("%d %s", resp.StatusCode, doc)
	}
	for _, want := range []string{"Nachweis der Datensicherung · Muster GmbH", "nextcloud", "dc01", "Tage mit Sicherung", "Wo die Sicherungen heute liegen", "Unterschrift"} {
		if !strings.Contains(doc, want) {
			t.Errorf("document lacks %q", want)
		}
	}
	if strings.Contains(doc, "fileserver") {
		t.Error("guest outside the choice listed")
	}
	if code, _ := admin.do("GET", "/api/proof?from=gestern&to="+to, nil, false); code != 400 {
		t.Fatalf("bad date: %d", code)
	}
}
