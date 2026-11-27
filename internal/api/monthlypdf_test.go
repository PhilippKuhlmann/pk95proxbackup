package api_test

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"pk95proxbackup/internal/notify"
)

// smtpSink is a minimal mail server that keeps what it receives.
type smtpSink struct {
	mu    sync.Mutex
	mails []sunkMail
	port  int
}

type sunkMail struct {
	to   []string
	data string
}

func newSMTPSink(t *testing.T) *smtpSink {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	s := &smtpSink{port: ln.Addr().(*net.TCPAddr).Port}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(conn)
		}
	}()
	return s
}

func (s *smtpSink) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(l string) { _, _ = io.WriteString(conn, l+"\r\n") }
	say("220 sink")
	var m sunkMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250 sink")
		case strings.HasPrefix(cmd, "MAIL"):
			m = sunkMail{}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT"):
			m.to = append(m.to, strings.Trim(strings.TrimPrefix(strings.TrimSpace(line)[8:], ":"), "<> "))
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil || l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			m.data = b.String()
			s.mu.Lock()
			s.mails = append(s.mails, m)
			s.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

// pdfText inflates the content streams of a PDF, enough to look for text in it.
func pdfText(b []byte) string {
	var out strings.Builder
	re := regexp.MustCompile(`(?s)/Length (\d+) /Filter /FlateDecode >>\nstream\n`)
	for _, m := range re.FindAllSubmatchIndex(b, -1) {
		n, _ := strconv.Atoi(string(b[m[2]:m[3]]))
		z, err := zlib.NewReader(bytes.NewReader(b[m[1] : m[1]+n]))
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(z)
		out.Write(data)
	}
	return out.String()
}

func logoPNG() []byte {
	m := image.NewNRGBA(image.Rect(0, 0, 60, 20))
	for x := 0; x < 60; x++ {
		for y := 0; y < 20; y++ {
			m.Set(x, y, color.NRGBA{31, 115, 214, uint8(x * 4)})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, m)
	return b.Bytes()
}

func TestMonthlyPDFForCustomer(t *testing.T) {
	cl, c := setup(t)
	if code, out := cl.do("POST", "/api/setup", map[string]string{"code": "ABCD-EFGH", "username": "admin", "password": "0123456789"}, false); code != 200 {
		t.Fatalf("setup: %d %v", code, out)
	}
	c.EventDetails("info", "Cluster", "Datensicherung erfolgreich", "", &notify.Details{Status: "ok", RowsTitle: "Gesicherte Server",
		Rows: []notify.Row{{Kind: "VM", ID: "100", Name: "dc01", Status: "ok"}}})
	month := time.Now().Format("2006-01")

	// Only to the customer needs the customer's address.
	if code, out := cl.do("PUT", "/api/report/monthly/settings", map[string]any{"customer_only": true}, true); code != 422 {
		t.Fatalf("customer only without address: %d %v", code, out)
	}
	if code, out := cl.do("PUT", "/api/report/monthly/settings", map[string]any{"enabled": true, "to": "chef@kunde.example", "company": "IT-Service Muster",
		"customer": "Musterfirma GmbH", "contact": "Telefon 0123 456789", "customer_only": true, "copy_to_me": true}, true); code != 200 || out["customer"] != "Musterfirma GmbH" {
		t.Fatalf("settings: %d %v", code, out)
	}
	// Logo: PNG or JPEG only.
	put := func(body []byte) int {
		req, _ := http.NewRequest("PUT", cl.base+"/api/report/monthly/logo", bytes.NewReader(body))
		req.Header.Set("X-PK95", "1")
		resp, err := cl.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put([]byte("<svg></svg>")); code != 415 {
		t.Fatalf("svg logo: %d", code)
	}
	if code := put(logoPNG()); code != 200 {
		t.Fatalf("png logo: %d", code)
	}

	resp, err := cl.http.Get(cl.base + "/api/report/monthly.pdf?month=" + month)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(b, []byte("%PDF")) || !bytes.Contains(b, []byte("/SMask")) {
		t.Fatalf("pdf: %d %s %.20q", resp.StatusCode, resp.Header.Get("Content-Type"), b)
	}
	if out := os.Getenv("PK95_PDF_OUT"); out != "" {
		_ = os.WriteFile(out, b, 0o600)
	}
	text := pdfText(b)
	for _, want := range []string{"IT-Service Muster", "Musterfirma GmbH", "Kurz gesagt", "dc01", "Seite 1 von", "Telefon 0123 456789"} {
		if !strings.Contains(text, want) {
			t.Errorf("PDF without %q", want)
		}
	}

	// Sending: one mail to the customer, a copy to the usual recipient, nothing else.
	sink := newSMTPSink(t)
	ns := notify.Defaults()
	ns.MailEnabled, ns.SMTPHost, ns.SMTPPort, ns.SMTPSec, ns.MailFrom, ns.MailTo = true, "127.0.0.1", sink.port, "none", "backup@example.org", "it@service.example"
	_ = c.St.SetSetting("notify", ns)
	if code, out := cl.do("POST", "/api/report/monthly/send?month="+month, nil, true); code != 200 {
		t.Fatalf("send: %d %v", code, out)
	}
	// A test: only to the given address (or the usual recipients), nothing to the customer.
	if code, out := cl.do("POST", "/api/report/monthly/test?month="+month, map[string]string{"to": "a@b.example, c@d.example"}, true); code != 422 {
		t.Fatalf("two addresses: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/report/monthly/test?month="+month, map[string]string{"to": "ich@service.example"}, true); code != 200 || out["to"] != "ich@service.example" {
		t.Fatalf("test: %d %v", code, out)
	}
	if code, out := cl.do("POST", "/api/report/monthly/test?month="+month, map[string]string{}, true); code != 200 || out["to"] != "it@service.example" {
		t.Fatalf("test to the usual recipients: %d %v", code, out)
	}
	sink.mu.Lock()
	defer sink.mu.Unlock()
	if len(sink.mails) != 4 || sink.mails[0].to[0] != "chef@kunde.example" || sink.mails[1].to[0] != "it@service.example" ||
		len(sink.mails[2].to) != 1 || sink.mails[2].to[0] != "ich@service.example" || sink.mails[3].to[0] != "it@service.example" {
		t.Fatalf("mails: %+v", sink.mails)
	}
	if !strings.Contains(sink.mails[2].data, "application/pdf") || !strings.Contains(sink.mails[2].data, "Content-ID: <brand-logo>") {
		t.Fatal("the test is not the customer's mail")
	}
	subject := ""
	for _, l := range strings.Split(sink.mails[2].data, "\r\n") {
		if strings.HasPrefix(l, "Subject: ") {
			subject, _ = new(mime.WordDecoder).DecodeHeader(strings.TrimPrefix(l, "Subject: "))
		}
	}
	if !strings.Contains(subject, "[IT-Service Muster]") || !strings.Contains(subject, "[Test] Monatsbericht") || !strings.Contains(subject, "Musterfirma GmbH") {
		t.Fatalf("subject: %q", subject)
	}
	m := sink.mails[0].data
	if !strings.Contains(m, "application/pdf") || !strings.Contains(m, "Monatsbericht-"+month+".pdf") || !strings.Contains(m, "Content-ID: <brand-logo>") {
		t.Fatalf("customer mail:\n%.1500s", m)
	}
	if strings.Contains(m, "Details ansehen") {
		t.Fatal("customer mail links to the appliance")
	}
}
