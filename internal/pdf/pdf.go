// Package pdf writes simple PDF documents: text in Helvetica (WinAnsi, enough for German),
// filled rectangles, lines and images (JPEG, PNG with transparency). Coordinates are points
// from the top left corner of an A4 page.
package pdf

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // decode JPEG logos
	_ "image/png"  // decode PNG logos
	"strings"
)

// Page size A4 in points.
const (
	PageW = 595.28
	PageH = 841.89
)

// Font is one of the two built-in fonts.
type Font int

const (
	Regular Font = iota
	Bold
)

// Color is an RGB color with parts from 0 to 1.
type Color struct{ R, G, B float64 }

// Hex parses "#rrggbb".
func Hex(s string) Color {
	var r, g, b int
	_, _ = fmt.Sscanf(strings.TrimPrefix(s, "#"), "%02x%02x%02x", &r, &g, &b)
	return Color{float64(r) / 255, float64(g) / 255, float64(b) / 255}
}

type img struct {
	w, h  int
	data  []byte // stream content
	dct   bool   // a JPEG as it is
	gray  bool
	smask []byte // alpha, compressed (nil = opaque)
}

// Doc is a document being written.
type Doc struct {
	Title  string
	pages  []*bytes.Buffer
	cur    int
	images []img
}

// New starts an empty document.
func New() *Doc { return &Doc{cur: -1} }

// AddPage adds a page and writes to it.
func (d *Doc) AddPage() {
	d.pages = append(d.pages, &bytes.Buffer{})
	d.cur = len(d.pages) - 1
}

// Pages is the number of pages.
func (d *Doc) Pages() int { return len(d.pages) }

// SetPage writes to an earlier page (0-based), e.g. for page numbers.
func (d *Doc) SetPage(i int) { d.cur = i }

func (d *Doc) w() *bytes.Buffer {
	if d.cur < 0 {
		d.AddPage()
	}
	return d.pages[d.cur]
}

func num(f float64) string {
	s := strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.2f", f), "0"), ".")
	if s == "" || s == "-" || s == "-0" {
		return "0"
	}
	return s
}

// Text writes s with its baseline at y.
func (d *Doc) Text(x, y float64, s string, f Font, size float64, c Color) {
	fmt.Fprintf(d.w(), "BT /F%d %s Tf %s %s %s rg %s %s Td (%s) Tj ET\n", int(f)+1, num(size), num(c.R), num(c.G), num(c.B), num(x), num(PageH-y), escape(encode(s)))
}

// Rect fills a rectangle (top left corner at x, y).
func (d *Doc) Rect(x, y, w, h float64, fill Color) {
	fmt.Fprintf(d.w(), "%s %s %s rg %s %s %s %s re f\n", num(fill.R), num(fill.G), num(fill.B), num(x), num(PageH-y-h), num(w), num(h))
}

// Line draws a line.
func (d *Doc) Line(x1, y1, x2, y2, width float64, c Color) {
	fmt.Fprintf(d.w(), "%s %s %s RG %s w %s %s m %s %s l S\n", num(c.R), num(c.G), num(c.B), num(width), num(x1), num(PageH-y1), num(x2), num(PageH-y2))
}

// AddImage takes a JPEG or PNG and returns its id and size in pixels.
func (d *Doc) AddImage(data []byte) (id, w, h int, err error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, 0, errors.New("Bild nicht lesbar (PNG oder JPEG)")
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > 16_000_000 {
		return 0, 0, 0, errors.New("Bild zu groß")
	}
	m, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return 0, 0, 0, errors.New("Bild nicht lesbar")
	}
	// A JPEG in RGB or gray goes in as it is.
	if format == "jpeg" && (cfg.ColorModel == color.YCbCrModel || cfg.ColorModel == color.GrayModel) {
		d.images = append(d.images, img{w: cfg.Width, h: cfg.Height, data: data, dct: true, gray: cfg.ColorModel == color.GrayModel})
		return len(d.images) - 1, cfg.Width, cfg.Height, nil
	}
	b := m.Bounds()
	rgb := make([]byte, 0, b.Dx()*b.Dy()*3)
	alpha := make([]byte, 0, b.Dx()*b.Dy())
	opaque := true
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bb, a := m.At(x, y).RGBA()
			if a == 0 {
				rgb = append(rgb, 255, 255, 255)
			} else { // colors come premultiplied
				rgb = append(rgb, byte(r*0xff/a), byte(g*0xff/a), byte(bb*0xff/a))
			}
			alpha = append(alpha, byte(a>>8))
			if a != 0xffff {
				opaque = false
			}
		}
	}
	im := img{w: b.Dx(), h: b.Dy(), data: deflate(rgb)}
	if !opaque {
		im.smask = deflate(alpha)
	}
	d.images = append(d.images, im)
	return len(d.images) - 1, b.Dx(), b.Dy(), nil
}

// Image draws an image added with AddImage.
func (d *Doc) Image(id int, x, y, w, h float64) {
	fmt.Fprintf(d.w(), "q %s 0 0 %s %s %s cm /Im%d Do Q\n", num(w), num(h), num(x), num(PageH-y-h), id+1)
}

func deflate(b []byte) []byte {
	var out bytes.Buffer
	z, _ := zlib.NewWriterLevel(&out, zlib.BestCompression)
	_, _ = z.Write(b)
	_ = z.Close()
	return out.Bytes()
}

// Bytes writes the finished document.
func (d *Doc) Bytes() []byte {
	if len(d.pages) == 0 {
		d.AddPage()
	}
	var out bytes.Buffer
	var offsets []int
	obj := func(body string, stream []byte) int {
		offsets = append(offsets, out.Len())
		n := len(offsets)
		fmt.Fprintf(&out, "%d 0 obj\n%s", n, body)
		if stream != nil {
			out.WriteString("\nstream\n")
			out.Write(stream)
			out.WriteString("\nendstream")
		}
		out.WriteString("\nendobj\n")
		return n
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	f1 := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>", nil)
	f2 := obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>", nil)
	var xobj []string
	for i, im := range d.images {
		cs, filter := "/DeviceRGB", "/FlateDecode"
		if im.dct {
			filter = "/DCTDecode"
			if im.gray {
				cs = "/DeviceGray"
			}
		}
		smask := ""
		if im.smask != nil {
			s := obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /Length %d >>", im.w, im.h, len(im.smask)), im.smask)
			smask = fmt.Sprintf(" /SMask %d 0 R", s)
		}
		n := obj(fmt.Sprintf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 /Filter %s%s /Length %d >>", im.w, im.h, cs, filter, smask, len(im.data)), im.data)
		xobj = append(xobj, fmt.Sprintf("/Im%d %d 0 R", i+1, n))
	}
	res := obj(fmt.Sprintf("<< /Font << /F1 %d 0 R /F2 %d 0 R >> /XObject << %s >> >>", f1, f2, strings.Join(xobj, " ")), nil)
	pagesID := len(offsets) + 1 + 2*len(d.pages) // after the page and content objects
	var kids []string
	for _, p := range d.pages {
		c := deflate(p.Bytes())
		cn := obj(fmt.Sprintf("<< /Length %d /Filter /FlateDecode >>", len(c)), c)
		pn := obj(fmt.Sprintf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources %d 0 R /Contents %d 0 R >>", pagesID, num(PageW), num(PageH), res, cn), nil)
		kids = append(kids, fmt.Sprintf("%d 0 R", pn))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), len(d.pages)), nil)
	cat := obj(fmt.Sprintf("<< /Type /Catalog /Pages %d 0 R >>", pagesID), nil)
	info := obj(fmt.Sprintf("<< /Title (%s) /Producer (PK95 ProxBackup) >>", escape(encode(d.Title))), nil)
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offsets)+1)
	for _, o := range offsets {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root %d 0 R /Info %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets)+1, cat, info, xref)
	return out.Bytes()
}

// ---- text ----

// winAnsi maps the characters outside Latin-1 that WinAnsiEncoding has.
var winAnsi = map[rune]byte{'€': 0x80, '‚': 0x82, '„': 0x84, '…': 0x85, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '–': 0x96, '—': 0x97, '™': 0x99}

func encode(s string) []byte {
	out := make([]byte, 0, len(s))
	for _, r := range s {
		switch {
		case r < 0x80:
			out = append(out, byte(r))
		case r >= 0xa0 && r <= 0xff:
			out = append(out, byte(r))
		case winAnsi[r] != 0:
			out = append(out, winAnsi[r])
		case r == '→':
			out = append(out, '-', '>')
		default:
			out = append(out, '?')
		}
	}
	return out
}

func escape(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		switch c {
		case '(', ')', '\\':
			s.WriteByte('\\')
			s.WriteByte(c)
		case '\n', '\r':
			s.WriteByte(' ')
		default:
			s.WriteByte(c)
		}
	}
	return s.String()
}

// Width is the width of s in points.
func Width(s string, f Font, size float64) float64 {
	w := 0
	for _, c := range encode(s) {
		w += charWidth(c, f)
	}
	return float64(w) * size / 1000
}

// Wrap breaks s into lines no wider than width (at spaces; long words are cut).
func Wrap(s string, f Font, size, width float64) []string {
	var lines []string
	for _, para := range strings.Split(s, "\n") {
		line := ""
		for _, wd := range strings.Fields(para) {
			try := wd
			if line != "" {
				try = line + " " + wd
			}
			if Width(try, f, size) <= width {
				line = try
				continue
			}
			if line != "" {
				lines = append(lines, line)
			}
			for Width(wd, f, size) > width && len([]rune(wd)) > 1 {
				r := []rune(wd)
				n := len(r) - 1
				for n > 1 && Width(string(r[:n]), f, size) > width {
					n--
				}
				lines = append(lines, string(r[:n]))
				wd = string(r[n:])
			}
			line = wd
		}
		lines = append(lines, line)
	}
	return lines
}

// Helvetica and Helvetica-Bold widths (Adobe AFM) for 32–126.
var widths = [2][95]int{
	{278, 278, 355, 556, 556, 889, 667, 191, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 278, 278, 584, 584, 584, 556,
		1015, 667, 667, 722, 722, 667, 611, 778, 722, 278, 500, 667, 556, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 278, 278, 278, 469, 556,
		333, 556, 556, 500, 556, 556, 278, 556, 556, 222, 222, 500, 222, 833, 556, 556,
		556, 556, 333, 500, 278, 556, 500, 722, 500, 500, 500, 334, 260, 334, 584},
	{278, 333, 474, 556, 556, 889, 722, 238, 333, 333, 389, 584, 278, 333, 278, 278,
		556, 556, 556, 556, 556, 556, 556, 556, 556, 556, 333, 333, 584, 584, 584, 611,
		975, 722, 722, 722, 722, 667, 611, 778, 722, 278, 556, 722, 611, 833, 722, 778,
		667, 778, 722, 667, 611, 722, 667, 944, 667, 667, 611, 333, 278, 333, 584, 556,
		333, 556, 611, 556, 611, 556, 333, 611, 611, 278, 278, 556, 278, 889, 611, 611,
		611, 611, 389, 556, 333, 611, 556, 778, 556, 556, 500, 389, 280, 389, 584},
}

// latinBase maps the Latin-1 letters 0xC0–0xFF to a letter of the same width.
const latinBase = "AAAAAAACEEEEIIIIDNOOOOOxOUUUUYPsaaaaaaaceeeeiiiidnooooo/ouuuuypy"

func charWidth(c byte, f Font) int {
	switch {
	case c >= 32 && c <= 126:
		return widths[f][c-32]
	case c == 0xdf: // ß
		return 611
	case c >= 0xc0:
		return widths[f][latinBase[c-0xc0]-32]
	}
	bold := f == Bold
	switch c {
	case 0x80, 0x96: // € –
		return 556
	case 0x85, 0x97: // … —
		return 1000
	case 0x84, 0x93, 0x94: // „ “ ”
		if bold {
			return 500
		}
		return 333
	case 0x82, 0x91, 0x92: // ‚ ‘ ’
		if bold {
			return 278
		}
		return 222
	case 0x95: // •
		return 350
	case 0xb0: // °
		return 400
	case 0xa0, 0xb7: // nbsp ·
		return 278
	}
	return 556
}
