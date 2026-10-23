package csvkit

import (
	"bytes"
	"fmt"
	"math"
	"strings"
)

// PDF is a small PDF 1.4 writer: pages of text in the built-in Helvetica fonts (WinAnsi),
// filled rectangles, lines and polylines. No dependencies, nothing embedded. Coordinates are
// points from the top-left corner, y down, like the chart and SVG.
type PDF struct {
	W, H  float64
	pages []*bytes.Buffer
	cur   *bytes.Buffer
	// Origin and Scale map chart units onto the page while a chart is drawn (Canvas methods).
	OX, OY, S float64
}

// NewPDF starts a document with pages of w × h points (A4 is 595 × 842).
func NewPDF(w, h float64) *PDF { return &PDF{W: w, H: h, S: 1} }

// AddPage starts a new page.
func (p *PDF) AddPage() {
	p.cur = &bytes.Buffer{}
	p.pages = append(p.pages, p.cur)
}

// Pages is the number of pages so far.
func (p *PDF) Pages() int { return len(p.pages) }

func num(v float64) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	if s == "-0" || s == "" {
		return "0"
	}
	return s
}

func rgb(col string) string {
	c := hexColor(col)
	return num(float64(c.R)/255) + " " + num(float64(c.G)/255) + " " + num(float64(c.B)/255)
}

// winAnsi encodes s for a WinAnsi Type1 font and escapes it for a PDF string.
func winAnsi(s string) string {
	var b strings.Builder
	for _, r := range s {
		var c byte
		switch {
		case r >= 32 && r <= 126:
			c = byte(r)
		case r >= 0xA0 && r <= 0xFF:
			c = byte(r)
		default:
			c = map[rune]byte{'€': 0x80, '…': 0x85, '–': 0x96, '—': 0x97, '‘': 0x91, '’': 0x92, '“': 0x93, '”': 0x94, '•': 0x95, '™': 0x99}[r]
			if c == 0 {
				c = '?'
			}
		}
		switch c {
		case '(', ')', '\\':
			b.WriteByte('\\')
			b.WriteByte(c)
		default:
			if c < 32 || c > 126 {
				fmt.Fprintf(&b, "\\%03o", c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

func (p *PDF) tx(x float64) float64 { return p.OX + x*p.S }
func (p *PDF) ty(y float64) float64 { return p.H - (p.OY + y*p.S) }

// Rect fills a rectangle.
func (p *PDF) Rect(x, y, w, h float64, fill string) {
	fmt.Fprintf(p.cur, "%s rg %s %s %s %s re f\n", rgb(fill), num(p.tx(x)), num(p.ty(y+h)), num(w*p.S), num(h*p.S))
}

// Line strokes a line.
func (p *PDF) Line(x1, y1, x2, y2, w float64, col string) {
	fmt.Fprintf(p.cur, "%s RG %s w %s %s m %s %s l S\n", rgb(col), num(w*p.S), num(p.tx(x1)), num(p.ty(y1)), num(p.tx(x2)), num(p.ty(y2)))
}

// Polyline strokes connected segments with round joins.
func (p *PDF) Polyline(pts [][2]float64, w float64, col string) {
	if len(pts) < 2 {
		return
	}
	fmt.Fprintf(p.cur, "%s RG %s w 1 j 1 J %s %s m", rgb(col), num(w*p.S), num(p.tx(pts[0][0])), num(p.ty(pts[0][1])))
	for _, q := range pts[1:] {
		fmt.Fprintf(p.cur, " %s %s l", num(p.tx(q[0])), num(p.ty(q[1])))
	}
	p.cur.WriteString(" S 0 j 0 J\n")
}

// Circle fills a circle (four Bézier arcs).
func (p *PDF) Circle(x, y, r float64, fill string) {
	cx, cy, rr := p.tx(x), p.ty(y), r*p.S
	k := 0.5523 * rr
	fmt.Fprintf(p.cur, "%s rg %s %s m %s %s %s %s %s %s c %s %s %s %s %s %s c %s %s %s %s %s %s c %s %s %s %s %s %s c f\n", rgb(fill),
		num(cx+rr), num(cy),
		num(cx+rr), num(cy+k), num(cx+k), num(cy+rr), num(cx), num(cy+rr),
		num(cx-k), num(cy+rr), num(cx-rr), num(cy+k), num(cx-rr), num(cy),
		num(cx-rr), num(cy-k), num(cx-k), num(cy-rr), num(cx), num(cy-rr),
		num(cx+k), num(cy-rr), num(cx+rr), num(cy-k), num(cx+rr), num(cy))
}

// Text writes s with its anchor (0 start, 1 middle, 2 end) at (x, y) on the baseline.
func (p *PDF) Text(x, y float64, s string, size float64, bold bool, col string, anchor int, angle float64) {
	if s == "" {
		return
	}
	f := "F1"
	if bold {
		f = "F2"
	}
	sz := size * p.S
	w := TextWidth(s, sz, bold) * float64(anchor) / 2
	th := angle * math.Pi / 180
	cs, sn := math.Cos(th), math.Sin(th)
	X, Y := p.tx(x)-w*cs, p.ty(y)-w*sn
	fmt.Fprintf(p.cur, "BT %s rg /%s %s Tf %s %s %s %s %s %s Tm (%s) Tj ET\n", rgb(col), f, num(sz), num(cs), num(sn), num(-sn), num(cs), num(X), num(Y), winAnsi(s))
}

// Bytes writes the document.
func (p *PDF) Bytes() []byte {
	if len(p.pages) == 0 {
		p.AddPage()
	}
	var out bytes.Buffer
	var offs []int
	obj := func(body string) {
		offs = append(offs, out.Len())
		fmt.Fprintf(&out, "%d 0 obj\n%s\nendobj\n", len(offs), body)
	}
	out.WriteString("%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")
	n := len(p.pages)
	// 1 catalog, 2 pages, 3-4 fonts, then page/content pairs from 5.
	obj("<< /Type /Catalog /Pages 2 0 R >>")
	var kids []string
	for i := 0; i < n; i++ {
		kids = append(kids, fmt.Sprintf("%d 0 R", 5+2*i))
	}
	obj(fmt.Sprintf("<< /Type /Pages /Kids [%s] /Count %d >>", strings.Join(kids, " "), n))
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>")
	obj("<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>")
	for i, pg := range p.pages {
		obj(fmt.Sprintf("<< /Type /Page /Parent 2 0 R /MediaBox [0 0 %s %s] /Resources << /Font << /F1 3 0 R /F2 4 0 R >> >> /Contents %d 0 R >>",
			num(p.W), num(p.H), 6+2*i))
		obj(fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", pg.Len(), pg.String()))
	}
	xref := out.Len()
	fmt.Fprintf(&out, "xref\n0 %d\n0000000000 65535 f \n", len(offs)+1)
	for _, o := range offs {
		fmt.Fprintf(&out, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&out, "trailer\n<< /Size %d /Root 1 0 R /Info << /Producer (Blaze Desktop csvkit) >> >>\nstartxref\n%d\n%%%%EOF\n", len(offs)+1, xref)
	return out.Bytes()
}
