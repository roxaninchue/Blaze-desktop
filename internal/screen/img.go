package screen

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"math/bits"
)

// Fit scales img down (area average) so its longest side is at most max.
func Fit(img image.Image, max int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if max <= 0 || (w <= max && h <= max) {
		return img
	}
	var nw, nh int
	if w >= h {
		nw, nh = max, h*max/w
	} else {
		nw, nh = w*max/h, max
	}
	if nw < 1 {
		nw = 1
	}
	if nh < 1 {
		nh = 1
	}
	return resize(img, nw, nh)
}

func toRGBA(img image.Image) *image.RGBA {
	if r, ok := img.(*image.RGBA); ok && r.Bounds().Min == (image.Point{}) {
		return r
	}
	b := img.Bounds()
	r := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	draw.Draw(r, r.Bounds(), img, b.Min, draw.Src)
	return r
}

// resize is a box filter: every source pixel lands in exactly one output pixel.
func resize(img image.Image, nw, nh int) *image.RGBA {
	src := toRGBA(img)
	w, h := src.Bounds().Dx(), src.Bounds().Dy()
	dst := image.NewRGBA(image.Rect(0, 0, nw, nh))
	sum := make([]uint32, nw*4)
	cnt := make([]uint32, nw)
	for oy := 0; oy < nh; oy++ {
		y0, y1 := oy*h/nh, (oy+1)*h/nh
		if y1 <= y0 {
			y1 = y0 + 1
		}
		for i := range sum {
			sum[i] = 0
		}
		for i := range cnt {
			cnt[i] = 0
		}
		for y := y0; y < y1 && y < h; y++ {
			row := src.Pix[y*src.Stride:]
			for x := 0; x < w; x++ {
				ox := x * nw / w
				p := row[x*4:]
				sum[ox*4] += uint32(p[0])
				sum[ox*4+1] += uint32(p[1])
				sum[ox*4+2] += uint32(p[2])
				cnt[ox]++
			}
		}
		drow := dst.Pix[oy*dst.Stride:]
		for ox := 0; ox < nw; ox++ {
			c := cnt[ox]
			if c == 0 {
				c = 1
			}
			drow[ox*4] = uint8(sum[ox*4] / c)
			drow[ox*4+1] = uint8(sum[ox*4+1] / c)
			drow[ox*4+2] = uint8(sum[ox*4+2] / c)
			drow[ox*4+3] = 255
		}
	}
	return dst
}

// Crop cuts r (in image coordinates) out of img.
func Crop(img image.Image, r Rect) image.Image {
	rr := image.Rect(r.X, r.Y, r.X+r.W, r.Y+r.H).Intersect(img.Bounds())
	if rr.Empty() {
		return img
	}
	out := image.NewRGBA(image.Rect(0, 0, rr.Dx(), rr.Dy()))
	draw.Draw(out, out.Bounds(), img, rr.Min, draw.Src)
	return out
}

// Blur pixelates each rectangle (image coordinates) beyond reading: password fields.
func Blur(img image.Image, rects []Rect) image.Image {
	if len(rects) == 0 {
		return img
	}
	m := toRGBA(img)
	for _, r := range rects {
		rr := image.Rect(r.X-4, r.Y-4, r.X+r.W+4, r.Y+r.H+4).Intersect(m.Bounds())
		if rr.Empty() {
			continue
		}
		// one flat colour: the average of the box, so nothing can be recovered
		var s [3]uint64
		var n uint64
		for y := rr.Min.Y; y < rr.Max.Y; y++ {
			for x := rr.Min.X; x < rr.Max.X; x++ {
				p := m.RGBAAt(x, y)
				s[0] += uint64(p.R)
				s[1] += uint64(p.G)
				s[2] += uint64(p.B)
				n++
			}
		}
		if n == 0 {
			continue
		}
		c := color.RGBA{uint8(s[0] / n), uint8(s[1] / n), uint8(s[2] / n), 255}
		draw.Draw(m, rr, &image.Uniform{c}, image.Point{}, draw.Src)
	}
	return m
}

// JPEG encodes at quality q.
func JPEG(img image.Image, q int) ([]byte, error) {
	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: q})
	return buf.Bytes(), err
}

// DHash is a 64-bit perceptual hash of a tiny greyscale thumbnail (9x8).
func DHash(img image.Image) uint64 {
	t := resize(img, 9, 8)
	var h uint64
	for y := 0; y < 8; y++ {
		for x := 0; x < 8; x++ {
			a, b := lum(t, x, y), lum(t, x+1, y)
			h <<= 1
			if a > b {
				h |= 1
			}
		}
	}
	return h
}

func lum(m *image.RGBA, x, y int) int {
	p := m.Pix[y*m.Stride+x*4:]
	return int(p[0])*299 + int(p[1])*587 + int(p[2])*114
}

// Near is true when two hashes differ in at most 4 bits (the same picture).
func Near(a, b uint64) bool { return bits.OnesCount64(a^b) <= 4 }

// blank is true when the image is a single colour (a failed capture is all black).
func blank(img image.Image) bool {
	m := toRGBA(img)
	if len(m.Pix) < 4 {
		return true
	}
	r, g, b := m.Pix[0], m.Pix[1], m.Pix[2]
	step := len(m.Pix) / 4 / 4000
	if step < 1 {
		step = 1
	}
	for i := 0; i < len(m.Pix); i += 4 * step {
		if m.Pix[i] != r || m.Pix[i+1] != g || m.Pix[i+2] != b {
			return false
		}
	}
	return true
}
