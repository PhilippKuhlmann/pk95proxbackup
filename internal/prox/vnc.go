package prox

import (
	"bufio"
	"context"
	"crypto/des"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Screenshot grabs the screen of a running VM through the VNC console of Proxmox VE
// (vncproxy + vncwebsocket, the same path the noVNC console in the browser uses).
func (p *PVE) Screenshot(ctx context.Context, node string, vmid int) (image.Image, error) {
	var proxy struct {
		Port   json.RawMessage `json:"port"`
		Ticket string          `json:"ticket"`
	}
	if err := p.Do(ctx, http.MethodPost, guestPath(node, "qemu", vmid)+"/vncproxy", Params{"websocket": true}, &proxy); err != nil {
		return nil, fmt.Errorf("VNC-Konsole: %w", err)
	}
	port := strings.Trim(string(proxy.Port), `"`)
	if port == "" || proxy.Ticket == "" {
		return nil, errors.New("VNC-Konsole: keine Zugangsdaten erhalten")
	}
	path := "/api2/json" + guestPath(node, "qemu", vmid) + "/vncwebsocket?port=" + url.QueryEscape(port) + "&vncticket=" + url.QueryEscape(proxy.Ticket)
	ws, err := p.dialWebsocket(ctx, path)
	if err != nil {
		return nil, err
	}
	defer ws.Close()
	return rfbCapture(ws, proxy.Ticket)
}

// ---- minimal websocket client (RFC 6455), binary frames only ----

type wsConn struct {
	conn net.Conn
	br   *bufio.Reader
	buf  []byte
}

func (p *PVE) dialWebsocket(ctx context.Context, path string) (*wsConn, error) {
	tr := pinnedTransport(p.Finger)
	d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 10 * time.Second}, Config: tr.TLSClientConfig}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(p.Host, strconv.Itoa(p.Port)))
	if err != nil {
		return nil, fmt.Errorf("VNC-Websocket: %w", err)
	}
	deadline := time.Now().Add(45 * time.Second)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = raw.SetDeadline(deadline)
	keyBytes := make([]byte, 16)
	_, _ = rand.Read(keyBytes)
	key := base64.StdEncoding.EncodeToString(keyBytes)
	req := "GET " + path + " HTTP/1.1\r\n" +
		"Host: " + net.JoinHostPort(p.Host, strconv.Itoa(p.Port)) + "\r\n" +
		"Upgrade: websocket\r\nConnection: Upgrade\r\n" +
		"Sec-WebSocket-Key: " + key + "\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Protocol: binary\r\n" +
		"Authorization: " + p.auth + "\r\n\r\n"
	if _, err := io.WriteString(raw, req); err != nil {
		raw.Close()
		return nil, err
	}
	br := bufio.NewReader(raw)
	resp, err := http.ReadResponse(br, &http.Request{Method: http.MethodGet})
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("VNC-Websocket: %w", err)
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		raw.Close()
		return nil, fmt.Errorf("VNC-Websocket abgelehnt: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	h := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		raw.Close()
		return nil, errors.New("VNC-Websocket: falsche Antwort des Servers")
	}
	return &wsConn{conn: raw, br: br}, nil
}

func (w *wsConn) Close() error { return w.conn.Close() }

// Write sends one masked binary frame.
func (w *wsConn) Write(p []byte) (int, error) {
	hdr := []byte{0x82}
	switch n := len(p); {
	case n < 126:
		hdr = append(hdr, byte(0x80|n))
	case n < 1<<16:
		hdr = append(hdr, 0x80|126, byte(n>>8), byte(n))
	default:
		hdr = append(hdr, 0x80|127)
		hdr = binary.BigEndian.AppendUint64(hdr, uint64(n))
	}
	mask := make([]byte, 4)
	_, _ = rand.Read(mask)
	hdr = append(hdr, mask...)
	out := make([]byte, len(p))
	for i := range p {
		out[i] = p[i] ^ mask[i%4]
	}
	if _, err := w.conn.Write(append(hdr, out...)); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read returns payload bytes of data frames (pings are answered, close ends the stream).
func (w *wsConn) Read(p []byte) (int, error) {
	for len(w.buf) == 0 {
		var h [2]byte
		if _, err := io.ReadFull(w.br, h[:]); err != nil {
			return 0, err
		}
		op := h[0] & 0x0f
		n := uint64(h[1] & 0x7f)
		switch n {
		case 126:
			var b [2]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return 0, err
			}
			n = uint64(binary.BigEndian.Uint16(b[:]))
		case 127:
			var b [8]byte
			if _, err := io.ReadFull(w.br, b[:]); err != nil {
				return 0, err
			}
			n = binary.BigEndian.Uint64(b[:])
		}
		if n > 64<<20 {
			return 0, errors.New("VNC-Websocket: Frame zu groß")
		}
		var mask []byte
		if h[1]&0x80 != 0 {
			mask = make([]byte, 4)
			if _, err := io.ReadFull(w.br, mask); err != nil {
				return 0, err
			}
		}
		payload := make([]byte, n)
		if _, err := io.ReadFull(w.br, payload); err != nil {
			return 0, err
		}
		if mask != nil {
			for i := range payload {
				payload[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x8:
			return 0, io.EOF
		case 0x9: // ping → pong
			_, _ = w.conn.Write([]byte{0x8a, 0x80, 0, 0, 0, 0})
		case 0x0, 0x1, 0x2:
			w.buf = payload
		}
	}
	n := copy(p, w.buf)
	w.buf = w.buf[n:]
	return n, nil
}

// ---- RFB (VNC) ----

func rfbCapture(rw io.ReadWriter, password string) (image.Image, error) {
	r := bufio.NewReader(rw)
	ver := make([]byte, 12)
	if _, err := io.ReadFull(r, ver); err != nil {
		return nil, fmt.Errorf("VNC: %w", err)
	}
	if !strings.HasPrefix(string(ver), "RFB ") {
		return nil, fmt.Errorf("VNC: unbekanntes Protokoll %q", ver)
	}
	if _, err := rw.Write([]byte("RFB 003.008\n")); err != nil {
		return nil, err
	}
	nb, err := r.ReadByte()
	if err != nil {
		return nil, err
	}
	if nb == 0 {
		return nil, errors.New("VNC: Verbindung abgelehnt")
	}
	types := make([]byte, nb)
	if _, err := io.ReadFull(r, types); err != nil {
		return nil, err
	}
	chosen := byte(0)
	for _, t := range types {
		if t == 2 || (t == 1 && chosen == 0) {
			chosen = t
		}
	}
	switch chosen {
	case 2:
		if _, err := rw.Write([]byte{2}); err != nil {
			return nil, err
		}
		challenge := make([]byte, 16)
		if _, err := io.ReadFull(r, challenge); err != nil {
			return nil, err
		}
		resp, err := vncAuth(challenge, password)
		if err != nil {
			return nil, err
		}
		if _, err := rw.Write(resp); err != nil {
			return nil, err
		}
	case 1:
		if _, err := rw.Write([]byte{1}); err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("VNC: kein unterstütztes Anmeldeverfahren (%v)", types)
	}
	var result uint32
	if err := binary.Read(r, binary.BigEndian, &result); err != nil {
		return nil, err
	}
	if result != 0 {
		return nil, errors.New("VNC: Anmeldung abgelehnt")
	}
	if _, err := rw.Write([]byte{1}); err != nil { // ClientInit: shared
		return nil, err
	}
	var si struct {
		W, H uint16
		PF   [16]byte
		NL   uint32
	}
	if err := binary.Read(r, binary.BigEndian, &si); err != nil {
		return nil, err
	}
	if _, err := io.CopyN(io.Discard, r, int64(si.NL)); err != nil {
		return nil, err
	}
	if si.W == 0 || si.H == 0 || int(si.W)*int(si.H) > 16<<20 {
		return nil, fmt.Errorf("VNC: ungültige Bildgröße %dx%d", si.W, si.H)
	}
	// 32 bit true colour, little endian: B G R X
	setPF := []byte{0, 0, 0, 0, 32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0, 0, 0, 0}
	setEnc := []byte{2, 0, 0, 1, 0, 0, 0, 0} // raw only
	req := []byte{3, 0, 0, 0, 0, 0, byte(si.W >> 8), byte(si.W), byte(si.H >> 8), byte(si.H)}
	if _, err := rw.Write(append(append(setPF, setEnc...), req...)); err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, int(si.W), int(si.H)))
	covered := 0
	for covered < int(si.W)*int(si.H) {
		t, err := r.ReadByte()
		if err != nil {
			return nil, err
		}
		switch t {
		case 0: // FramebufferUpdate
			var hdr struct {
				Pad byte
				N   uint16
			}
			if err := binary.Read(r, binary.BigEndian, &hdr); err != nil {
				return nil, err
			}
			for i := 0; i < int(hdr.N); i++ {
				var rect struct {
					X, Y, W, H uint16
					Enc        int32
				}
				if err := binary.Read(r, binary.BigEndian, &rect); err != nil {
					return nil, err
				}
				if rect.Enc != 0 {
					return nil, fmt.Errorf("VNC: unerwartete Kodierung %d", rect.Enc)
				}
				row := make([]byte, int(rect.W)*4)
				for y := 0; y < int(rect.H); y++ {
					if _, err := io.ReadFull(r, row); err != nil {
						return nil, err
					}
					for x := 0; x < int(rect.W); x++ {
						px := row[x*4:]
						img.SetRGBA(int(rect.X)+x, int(rect.Y)+y, color.RGBA{R: px[2], G: px[1], B: px[0], A: 255})
					}
				}
				covered += int(rect.W) * int(rect.H)
			}
			if covered < int(si.W)*int(si.H) {
				// ask again for the rest
				if _, err := rw.Write(req); err != nil {
					return nil, err
				}
			}
		case 2: // bell
		case 3: // ServerCutText
			var hdr struct {
				Pad [3]byte
				N   uint32
			}
			if err := binary.Read(r, binary.BigEndian, &hdr); err != nil {
				return nil, err
			}
			if _, err := io.CopyN(io.Discard, r, int64(hdr.N)); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("VNC: unerwartete Nachricht %d", t)
		}
	}
	return img, nil
}

// vncAuth encrypts the challenge with the password (DES, key bits mirrored per byte).
func vncAuth(challenge []byte, password string) ([]byte, error) {
	key := make([]byte, 8)
	copy(key, password)
	for i, b := range key {
		var m byte
		for j := 0; j < 8; j++ {
			if b&(1<<j) != 0 {
				m |= 1 << (7 - j)
			}
		}
		key[i] = m
	}
	c, err := des.NewCipher(key)
	if err != nil {
		return nil, err
	}
	out := make([]byte, 16)
	c.Encrypt(out[:8], challenge[:8])
	c.Encrypt(out[8:], challenge[8:])
	return out, nil
}

// LooksBlank reports whether an image is (almost) one colour – a black or empty screen.
func LooksBlank(img image.Image) bool {
	b := img.Bounds()
	seen := map[color.RGBA]int{}
	total := 0
	step := max(1, min(b.Dx(), b.Dy())/80) // ~80×80 samples, every pixel of tiny images
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			r, g, bl, _ := img.At(x, y).RGBA()
			seen[color.RGBA{uint8(r >> 12), uint8(g >> 12), uint8(bl >> 12), 0}]++
			total++
		}
	}
	top := 0
	for _, n := range seen {
		if n > top {
			top = n
		}
	}
	return total > 0 && float64(top)/float64(total) > 0.995
}
