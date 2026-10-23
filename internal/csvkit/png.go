package csvkit

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/gofont/goregular"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"
)

// PNG renders the chart as a PNG at scale times its size (2 = crisp on high-DPI screens).
// Pure Go: the Go fonts and golang.org/x/image's rasteriser.
func (c *Chart) PNG(scale float64) ([]byte, error) {
	if scale <= 0 {
		scale = 2
	}
	pngMu.Lock() // font faces keep scratch buffers; one render at a time
	defer pngMu.Unlock()
	w, h := int(math.Ceil(c.W*scale)), int(math.Ceil(c.H*scale))
	pc := &pngCanvas{img: image.NewRGBA(image.Rect(0, 0, w, h)), k: scale}
	c.Draw(pc)
	var buf bytes.Buffer
	if err := png.Encode(&buf, pc.img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

type pngCanvas struct {
	img *image.RGBA
	k   float64
}

func hexColor(s string) color.RGBA {
	if len(s) == 7 && s[0] == '#' {
		v, err := strconv.ParseUint(s[1:], 16, 32)
		if err == nil {
			return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
		}
	}
	return color.RGBA{0, 0, 0, 255}
}

// fill paints polygons (in chart units) with anti-aliasing, rasterising only their bounding box.
func (p *pngCanvas) fill(polys [][][2]float64, col string) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, pg := range polys {
		for _, q := range pg {
			x, y := q[0]*p.k, q[1]*p.k
			minX, minY, maxX, maxY = math.Min(minX, x), math.Min(minY, y), math.Max(maxX, x), math.Max(maxY, y)
		}
	}
	box := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX))+1, int(math.Ceil(maxY))+1).Intersect(p.img.Bounds())
	if box.Empty() {
		return
	}
	r := vector.NewRasterizer(box.Dx(), box.Dy())
	for _, pg := range polys {
		for i, q := range pg {
			x, y := float32(q[0]*p.k-float64(box.Min.X)), float32(q[1]*p.k-float64(box.Min.Y))
			if i == 0 {
				r.MoveTo(x, y)
			} else {
				r.LineTo(x, y)
			}
		}
		r.ClosePath()
	}
	r.Draw(p.img, box, image.NewUniform(hexColor(col)), image.Point{})
}

func (p *pngCanvas) Rect(x, y, w, h float64, fill string) {
	p.fill([][][2]float64{{{x, y}, {x + w, y}, {x + w, y + h}, {x, y + h}}}, fill)
}

func seg(x1, y1, x2, y2, w float64) [][2]float64 {
	dx, dy := x2-x1, y2-y1
	l := math.Hypot(dx, dy)
	if l == 0 {
		return nil
	}
	nx, ny := -dy/l*w/2, dx/l*w/2
	return [][2]float64{{x1 + nx, y1 + ny}, {x2 + nx, y2 + ny}, {x2 - nx, y2 - ny}, {x1 - nx, y1 - ny}}
}

func (p *pngCanvas) Line(x1, y1, x2, y2, w float64, col string) {
	if s := seg(x1, y1, x2, y2, w); s != nil {
		p.fill([][][2]float64{s}, col)
	}
}

func (p *pngCanvas) Polyline(pts [][2]float64, w float64, col string) {
	for i := 1; i < len(pts); i++ {
		a, b := pts[i-1], pts[i]
		p.Line(a[0], a[1], b[0], b[1], w, col)
	}
	for _, q := range pts { // round joins
		p.Circle(q[0], q[1], w/2, col)
	}
}

func (p *pngCanvas) Circle(x, y, rad float64, fill string) {
	const n = 32
	pg := make([][2]float64, 0, n)
	for i := 0; i < n; i++ {
		a := 2 * math.Pi * float64(i) / n
		pg = append(pg, [2]float64{x + rad*math.Cos(a), y + rad*math.Sin(a)})
	}
	p.fill([][][2]float64{pg}, fill)
}

var (
	pngMu     sync.Mutex
	fontMu    sync.Mutex
	fontFaces = map[[2]int]font.Face{}
	fontsOnce sync.Once
	fReg      *opentype.Font
	fBold     *opentype.Font
)

func face(px float64, bold bool) font.Face {
	fontsOnce.Do(func() {
		fReg, _ = opentype.Parse(goregular.TTF)
		fBold, _ = opentype.Parse(gobold.TTF)
	})
	key := [2]int{int(math.Round(px * 4)), 0}
	f := fReg
	if bold {
		key[1], f = 1, fBold
	}
	fontMu.Lock()
	defer fontMu.Unlock()
	if fc, ok := fontFaces[key]; ok {
		return fc
	}
	fc, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(key[0]) / 4, DPI: 72, Hinting: font.HintingNone})
	if err != nil {
		return nil
	}
	fontFaces[key] = fc
	return fc
}

func (p *pngCanvas) Text(x, y float64, s string, size float64, bold bool, col string, anchor int, angle float64) {
	// The Go fonts run a little wider than Helvetica, which the layout measured with; scale
	// them so the PNG matches the SVG and the PDF.
	px := size * p.k * 0.94
	fc := face(px, bold)
	if fc == nil || s == "" {
		return
	}
	m := fc.Metrics()
	asc, desc := float64(m.Ascent.Ceil()), float64(m.Descent.Ceil())
	tw := float64(font.MeasureString(fc, s).Ceil())
	mw, mh := int(tw)+4, int(asc+desc)+4
	mask := image.NewAlpha(image.Rect(0, 0, mw, mh))
	d := font.Drawer{Dst: mask, Src: image.Opaque, Face: fc, Dot: fixed.P(2, int(asc)+2)}
	d.DrawString(s)
	ax := 2 + tw*float64(anchor)/2
	ay := asc + 2
	X, Y := x*p.k, y*p.k
	c := hexColor(col)
	src := image.NewUniform(c)
	if angle == 0 {
		off := image.Pt(int(math.Round(X-ax)), int(math.Round(Y-ay)))
		r := mask.Bounds().Add(off)
		draw.DrawMask(p.img, r, src, image.Point{}, mask, image.Point{}, draw.Over)
		return
	}
	th := angle * math.Pi / 180
	cs, sn := math.Cos(th), math.Sin(th)
	fwd := func(mx, my float64) (float64, float64) {
		dx, dy := mx-ax, my-ay
		return X + dx*cs + dy*sn, Y - dx*sn + dy*cs
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range [][2]float64{{0, 0}, {float64(mw), 0}, {0, float64(mh)}, {float64(mw), float64(mh)}} {
		dx, dy := fwd(q[0], q[1])
		minX, minY, maxX, maxY = math.Min(minX, dx), math.Min(minY, dy), math.Max(maxX, dx), math.Max(maxY, dy)
	}
	b := p.img.Bounds()
	for dy := int(math.Floor(minY)); dy <= int(math.Ceil(maxY)); dy++ {
		for dx := int(math.Floor(minX)); dx <= int(math.Ceil(maxX)); dx++ {
			if !(image.Pt(dx, dy).In(b)) {
				continue
			}
			ux, uy := float64(dx)+0.5-X, float64(dy)+0.5-Y
			mx := ux*cs - uy*sn + ax - 0.5
			my := ux*sn + uy*cs + ay - 0.5
			a := bilinear(mask, mx, my)
			if a <= 0 {
				continue
			}
			o := p.img.RGBAAt(dx, dy)
			blend := func(fg, bg uint8) uint8 { return uint8(float64(fg)*a + float64(bg)*(1-a) + 0.5) }
			p.img.SetRGBA(dx, dy, color.RGBA{blend(c.R, o.R), blend(c.G, o.G), blend(c.B, o.B), 255})
		}
	}
}

func bilinear(m *image.Alpha, x, y float64) float64 {
	x0, y0 := int(math.Floor(x)), int(math.Floor(y))
	fx, fy := x-float64(x0), y-float64(y0)
	at := func(i, j int) float64 {
		if !(image.Pt(i, j).In(m.Bounds())) {
			return 0
		}
		return float64(m.AlphaAt(i, j).A) / 255
	}
	return at(x0, y0)*(1-fx)*(1-fy) + at(x0+1, y0)*fx*(1-fy) + at(x0, y0+1)*(1-fx)*fy + at(x0+1, y0+1)*fx*fy
}
