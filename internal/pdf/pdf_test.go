package pdf

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestDocument(t *testing.T) {
	d := New()
	d.Title = "Monatsbericht März"
	d.AddPage()
	d.Text(40, 60, "Größe „Überblick“ – 5 € (Prüfung)", Bold, 14, Hex("#1f3d6e"))
	d.Rect(40, 80, 200, 20, Hex("#dcfce7"))
	d.Line(40, 110, 300, 110, 0.5, Hex("#e5e7eb"))
	// a logo with transparency
	m := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	m.Set(0, 0, color.NRGBA{255, 0, 0, 255})
	m.Set(1, 0, color.NRGBA{0, 0, 255, 128})
	var b bytes.Buffer
	_ = png.Encode(&b, m)
	id, w, h, err := d.AddImage(b.Bytes())
	if err != nil || w != 4 || h != 2 {
		t.Fatal(err, w, h)
	}
	d.Image(id, 40, 120, 40, 20)
	d.AddPage()
	d.Text(40, 60, "Seite 2", Regular, 10, Color{})
	out := d.Bytes()
	if !bytes.HasPrefix(out, []byte("%PDF-1.4")) || !bytes.HasSuffix(out, []byte("%%EOF\n")) {
		t.Fatal("frame")
	}
	// Every xref entry points at its object.
	s := string(out)
	i := strings.LastIndex(s, "startxref\n")
	x, _ := strconv.Atoi(strings.Fields(s[i+10:])[0])
	lines := strings.Split(s[x:], "\n")
	n, _ := strconv.Atoi(strings.Fields(lines[1])[1])
	for k := 1; k < n; k++ {
		off, _ := strconv.Atoi(strings.Fields(lines[2+k])[0])
		if !strings.HasPrefix(s[off:], fmt.Sprintf("%d 0 obj", k)) {
			t.Fatalf("object %d not at %d", k, off)
		}
	}
	if !regexp.MustCompile(`/Count 2`).MatchString(s) || !strings.Contains(s, "/SMask") {
		t.Fatal("pages or transparency missing")
	}
	if got := encode("Größe „Ü“ –"); !bytes.Equal(got, []byte{'G', 'r', 0xf6, 0xdf, 'e', ' ', 0x84, 0xdc, 0x93, ' ', 0x96}) {
		t.Fatalf("encoding: % x", got)
	}
}

func TestWrap(t *testing.T) {
	lines := Wrap("Alle Sicherungen erfolgreich, Restore-Tests bestanden, Platten in Ordnung.", Regular, 10, 150)
	if len(lines) < 2 {
		t.Fatal(lines)
	}
	for _, l := range lines {
		if Width(l, Regular, 10) > 150 {
			t.Fatalf("too wide: %q", l)
		}
	}
	if w := Width("Hallo", Regular, 10); w < 22.7 || w > 22.9 { // H 722 a 556 l 222 l 222 o 556 = 2278
		t.Fatal(w)
	}
}
