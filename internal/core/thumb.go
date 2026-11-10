package core

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
)

// mailJPEG loads a PNG screenshot and returns it as JPEG, at most maxW pixels wide
// (box filter), so mails with many servers stay small.
func mailJPEG(path string, maxW int) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	src, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w > maxW {
		nh := h * maxW / w
		dst := image.NewRGBA(image.Rect(0, 0, maxW, nh))
		for y := 0; y < nh; y++ {
			y0, y1 := y*h/nh, (y+1)*h/nh
			for x := 0; x < maxW; x++ {
				x0, x1 := x*w/maxW, (x+1)*w/maxW
				var r, g, bl, n uint32
				for yy := y0; yy < max(y1, y0+1); yy++ {
					for xx := x0; xx < max(x1, x0+1); xx++ {
						cr, cg, cb, _ := src.At(b.Min.X+xx, b.Min.Y+yy).RGBA()
						r, g, bl, n = r+cr, g+cg, bl+cb, n+1
					}
				}
				dst.SetRGBA(x, y, color.RGBA{uint8(r / n >> 8), uint8(g / n >> 8), uint8(bl / n >> 8), 255})
			}
		}
		src = dst
	}
	var out bytes.Buffer
	if err := jpeg.Encode(&out, src, &jpeg.Options{Quality: 78}); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
