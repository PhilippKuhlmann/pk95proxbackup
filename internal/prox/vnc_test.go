package prox

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"net"
	"testing"
)

// fakeVNC speaks the server side of RFB 3.8 with VNC authentication and sends a 4×2 image.
func fakeVNC(t *testing.T, c net.Conn, password string) {
	defer c.Close()
	_, _ = c.Write([]byte("RFB 003.008\n"))
	ver := make([]byte, 12)
	if _, err := io.ReadFull(c, ver); err != nil {
		t.Error(err)
		return
	}
	_, _ = c.Write([]byte{1, 2}) // one type: VNC auth
	sel := make([]byte, 1)
	_, _ = io.ReadFull(c, sel)
	challenge := []byte("0123456789abcdef")
	_, _ = c.Write(challenge)
	resp := make([]byte, 16)
	_, _ = io.ReadFull(c, resp)
	want, _ := vncAuth(challenge, password)
	if !bytes.Equal(resp, want) {
		_ = binary.Write(c, binary.BigEndian, uint32(1))
		return
	}
	_ = binary.Write(c, binary.BigEndian, uint32(0))
	_, _ = io.ReadFull(c, make([]byte, 1)) // ClientInit
	si := []byte{0, 4, 0, 2}
	si = append(si, make([]byte, 16)...)
	si = binary.BigEndian.AppendUint32(si, 4)
	si = append(si, "test"...)
	_, _ = c.Write(si)
	_, _ = io.ReadFull(c, make([]byte, 20+8+10)) // SetPixelFormat, SetEncodings, Request
	msg := []byte{2, 0}                          // a bell first
	msg = append(msg, 0, 0)
	msg = binary.BigEndian.AppendUint16(msg[:3], 1)
	msg = append([]byte{2}, msg[1:]...)
	upd := []byte{0, 0, 0, 1, 0, 0, 0, 0, 0, 4, 0, 2, 0, 0, 0, 0}
	for i := 0; i < 8; i++ {
		upd = append(upd, byte(i*30), 10, 200, 0) // B G R X
	}
	_, _ = c.Write(append([]byte{2}, upd...))
}

func TestRFBCapture(t *testing.T) {
	client, server := net.Pipe()
	go fakeVNC(t, server, "PVEVNC:ticketvalue")
	img, err := rfbCapture(client, "PVEVNC:ticketvalue")
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds() != image.Rect(0, 0, 4, 2) {
		t.Fatalf("size %v", img.Bounds())
	}
	if got := img.At(1, 0).(color.RGBA); got.R != 200 || got.G != 10 || got.B != 30 {
		t.Fatalf("pixel %v", got)
	}
	if LooksBlank(img) {
		t.Error("a picture with several colours is not blank")
	}
	black := image.NewRGBA(image.Rect(0, 0, 640, 480))
	if !LooksBlank(black) {
		t.Error("black screen must be blank")
	}
}

func TestRFBWrongPassword(t *testing.T) {
	client, server := net.Pipe()
	go fakeVNC(t, server, "richtig")
	if _, err := rfbCapture(client, "falsch"); err == nil {
		t.Fatal("wrong password accepted")
	}
}
