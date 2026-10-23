package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
	"strings"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
	"rsc.io/qr"
)

var (
	errUnsupported = errors.New("couldn't read that photo; JPEG, PNG, WebP and GIF work")
	errHuge        = errors.New("that photo has too many pixels")
)

const (
	fullSize   = 2048 // longest side kept for the lightbox and the download
	mediumSize = 900  // longest side used on the desk and in lists
	maxPixels  = 60_000_000
)

// processImage decodes an upload, turns it upright, shrinks it and re-encodes it as JPEG.
// Re-encoding also drops all metadata (GPS position, camera details).
func processImage(data []byte) (full, medium []byte, w, h int, err error) {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, nil, 0, 0, errUnsupported
	}
	if cfg.Width*cfg.Height > maxPixels || cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, nil, 0, 0, errHuge
	}
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, nil, 0, 0, errUnsupported
	}
	big := fit(src, fullSize)
	if o := jpegOrientation(data); o > 1 {
		big = orient(big, o)
	}
	small := fit(big, mediumSize)
	if full, err = encodeJPEG(big, 86); err != nil {
		return nil, nil, 0, 0, err
	}
	if medium, err = encodeJPEG(small, 80); err != nil {
		return nil, nil, 0, 0, err
	}
	b := big.Bounds()
	return full, medium, b.Dx(), b.Dy(), nil
}

// fit scales src so its longest side is at most max, flattening transparency onto white.
func fit(src image.Image, max int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	scale := math.Min(1, float64(max)/float64(maxInt(w, h)))
	nw, nh := maxInt(1, int(math.Round(float64(w)*scale))), maxInt(1, int(math.Round(float64(h)*scale)))
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	if nw == w && nh == h {
		draw.Draw(dst, dst.Bounds(), src, b.Min, draw.Over)
	} else {
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), src, b, xdraw.Over, nil)
	}
	return dst
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func encodeJPEG(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q}); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// jpegOrientation reads the EXIF orientation tag (1-8) from a JPEG; 1 if there isn't one.
// Browsers normally fix orientation before uploading; this covers uploads that skipped that.
func jpegOrientation(b []byte) int {
	if len(b) < 4 || b[0] != 0xFF || b[1] != 0xD8 {
		return 1
	}
	for i := 2; i+4 <= len(b); {
		if b[i] != 0xFF {
			return 1
		}
		m := b[i+1]
		switch {
		case m == 0xFF: // fill byte
			i++
			continue
		case m == 0x01 || (m >= 0xD0 && m <= 0xD8): // markers without a length
			i += 2
			continue
		case m == 0xDA || m == 0xD9: // image data starts; no more metadata
			return 1
		}
		n := int(b[i+2])<<8 | int(b[i+3])
		if n < 2 || i+2+n > len(b) {
			return 1
		}
		seg := b[i+4 : i+2+n]
		if m == 0xE1 && len(seg) > 14 && string(seg[:6]) == "Exif\x00\x00" {
			return exifOrientation(seg[6:])
		}
		i += 2 + n
	}
	return 1
}

func exifOrientation(t []byte) int {
	if len(t) < 8 {
		return 1
	}
	var bo binary.ByteOrder
	switch string(t[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return 1
	}
	if bo.Uint16(t[2:]) != 42 {
		return 1
	}
	off := int(bo.Uint32(t[4:]))
	if off < 8 || off+2 > len(t) {
		return 1
	}
	n := int(bo.Uint16(t[off:]))
	for k := 0; k < n; k++ {
		e := off + 2 + k*12
		if e+12 > len(t) {
			return 1
		}
		if bo.Uint16(t[e:]) == 0x0112 {
			if v := int(bo.Uint16(t[e+8:])); v >= 1 && v <= 8 {
				return v
			}
			return 1
		}
	}
	return 1
}

// orient applies an EXIF orientation so the image is upright.
func orient(src *image.RGBA, o int) *image.RGBA {
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	dw, dh := w, h
	if o >= 5 {
		dw, dh = h, w
	}
	dst := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			var dx, dy int
			switch o {
			case 2:
				dx, dy = w-1-x, y
			case 3:
				dx, dy = w-1-x, h-1-y
			case 4:
				dx, dy = x, h-1-y
			case 5:
				dx, dy = y, x
			case 6:
				dx, dy = h-1-y, x
			case 7:
				dx, dy = h-1-y, w-1-x
			case 8:
				dx, dy = y, w-1-x
			default:
				dx, dy = x, y
			}
			si := src.PixOffset(b.Min.X+x, b.Min.Y+y)
			di := dst.PixOffset(dx, dy)
			copy(dst.Pix[di:di+4], src.Pix[si:si+4])
		}
	}
	return dst
}

// qrSVG draws text as a QR code with a white quiet zone.
func qrSVG(text string) ([]byte, error) {
	c, err := qr.Encode(text, qr.M)
	if err != nil {
		return nil, err
	}
	const quiet = 4
	size := c.Size + 2*quiet
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges">`, size, size)
	b.WriteString(`<rect width="100%" height="100%" fill="#fff"/><path fill="#111" d="`)
	for y := 0; y < c.Size; y++ {
		for x := 0; x < c.Size; x++ {
			if c.Black(x, y) {
				fmt.Fprintf(&b, "M%d %dh1v1h-1z", x+quiet, y+quiet)
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String()), nil
}
