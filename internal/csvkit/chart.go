package csvkit

import (
	"fmt"
	"math"
	"strings"
)

// Chart kinds.
const (
	Bar  = "bar"
	Line = "line"
)

// Chart is a laid-out chart in its own units (top-left origin, y down). Draw paints it on any
// Canvas, so the SVG, the PNG and the PDF page are the same picture.
type Chart struct {
	Kind   string               `json:"kind"`
	Title  string               `json:"title"`
	XLabel string               `json:"x_label"`
	YLabel string               `json:"y_label"`
	Labels []string             `json:"labels"`
	Values []float64            `json:"values"`
	Format func(float64) string `json:"-"`
	W, H   float64              `json:"-"`
}

// Canvas is what a chart is painted on. Colours are #rrggbb; text y is the baseline.
type Canvas interface {
	Rect(x, y, w, h float64, fill string)
	Line(x1, y1, x2, y2, width float64, color string)
	Polyline(pts [][2]float64, width float64, color string)
	Circle(x, y, r float64, fill string)
	// Text draws s with its anchor point at (x, y): anchor 0 start, 1 middle, 2 end; angle in
	// degrees, counter-clockwise as seen on screen.
	Text(x, y float64, s string, size float64, bold bool, color string, anchor int, angle float64)
}

// Palette.
const (
	colInk    = "#1f2328"
	colMuted  = "#59636e"
	colGrid   = "#e3e6ea"
	colAxis   = "#9aa3ad"
	colAccent = "#e8590c"
	colBg     = "#ffffff"
)

// NewChart builds a chart of a summary: bars for groups, a line for groups that are dates.
// kind "" picks for you.
func NewChart(s *Summary, kind string) *Chart {
	c := &Chart{Kind: kind, Title: s.Label(), XLabel: s.By, W: 760, H: 420}
	if c.Kind == "" {
		c.Kind = Bar
		if s.ByDate {
			c.Kind = Line
		}
	}
	switch s.Agg {
	case "count":
		c.YLabel = "Rows"
		if s.Value != "" {
			c.YLabel = "Count"
		}
	case "mean":
		c.YLabel = "Average " + s.Value
	default:
		c.YLabel = s.Value
	}
	for _, g := range s.Groups {
		c.Labels = append(c.Labels, g.Key)
		c.Values = append(c.Values, g.Of(s.Agg))
	}
	agg := s.Agg
	c.Format = func(v float64) string { return s.Fmt(v, agg) }
	return c
}

// niceTicks gives round axis ticks covering [lo, hi].
func niceTicks(lo, hi float64, want int) (ticks []float64, step float64) {
	if hi <= lo {
		hi = lo + 1
	}
	raw := (hi - lo) / float64(want)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	switch f := raw / mag; {
	case f <= 1:
		step = mag
	case f <= 2:
		step = 2 * mag
	case f <= 2.5:
		step = 2.5 * mag
	case f <= 5:
		step = 5 * mag
	default:
		step = 10 * mag
	}
	start := math.Floor(lo/step) * step
	end := math.Ceil(hi/step) * step
	for v := start; v <= end+step/2; v += step {
		ticks = append(ticks, round(v, 10))
	}
	return
}

func tickLabel(v, step float64) string {
	dec := 0
	if step < 1 {
		dec = int(math.Ceil(-math.Log10(step)))
	}
	if step == 2.5*math.Pow(10, math.Floor(math.Log10(step))) && step < 10 {
		dec++
	}
	if dec > 6 {
		dec = 6
	}
	return thousands(formatNum(v, dec))
}

// shorten cuts a label to fit width at size, with an ellipsis.
func shorten(s string, size, width float64) string {
	if TextWidth(s, size, false) <= width {
		return s
	}
	rs := []rune(s)
	for len(rs) > 1 && TextWidth(string(rs)+"…", size, false) > width {
		rs = rs[:len(rs)-1]
	}
	return strings.TrimSpace(string(rs)) + "…"
}

// Draw lays the chart out and paints it.
func (c *Chart) Draw(cv Canvas) {
	W, H := c.W, c.H
	cv.Rect(0, 0, W, H, colBg)
	const titleSize, tickSize, labelSize, valueSize = 18.0, 12.0, 12.0, 11.0
	cv.Text(24, 34, shorten(c.Title, titleSize, W-48), titleSize, true, colInk, 0, 0)
	fmtV := c.Format
	if fmtV == nil {
		fmtV = func(v float64) string { return thousands(formatNum(v, 2)) }
	}
	lo, hi := 0.0, 0.0
	for _, v := range c.Values {
		lo, hi = math.Min(lo, v), math.Max(hi, v)
	}
	if c.Kind == Bar {
		hi += (hi - lo) * 0.08 // room for the value labels
	}
	ticks, step := niceTicks(lo, hi, 5)
	tmin, tmax := ticks[0], ticks[len(ticks)-1]
	// Left margin from the widest tick label, plus the rotated y-axis title.
	yw := 0.0
	for _, t := range ticks {
		yw = math.Max(yw, TextWidth(tickLabel(t, step), tickSize, false))
	}
	left := 24 + labelSize + 10 + yw + 8
	right := 24.0
	top := 64.0
	n := len(c.Labels)
	plotW := W - left - right
	slot := plotW / math.Max(1, float64(n))
	// X labels: horizontal when they fit their slot, else rotated 35°.
	shown := make([]bool, n)
	every := 1
	if c.Kind == Line && n > 0 {
		maxLabels := int(plotW / 70)
		if maxLabels < 2 {
			maxLabels = 2
		}
		every = int(math.Ceil(float64(n) / float64(maxLabels)))
	}
	for i := range shown {
		shown[i] = i%every == 0
	}
	labels := make([]string, n)
	maxLW := 0.0
	for i, l := range c.Labels {
		labels[i] = shorten(l, tickSize, 150)
		if shown[i] {
			maxLW = math.Max(maxLW, TextWidth(labels[i], tickSize, false))
		}
	}
	rotate := maxLW > slot*float64(every)-8
	const ang = 35.0
	xlabH := tickSize + 4
	if rotate {
		rad := ang * math.Pi / 180
		xlabH = maxLW*math.Sin(rad) + tickSize*math.Cos(rad) + 4
	}
	bottom := 16 + labelSize + 12 + xlabH + 8
	plotH := H - top - bottom
	y := func(v float64) float64 { return top + plotH - (v-tmin)/(tmax-tmin)*plotH }
	// Grid and y ticks.
	for _, t := range ticks {
		yy := y(t)
		col := colGrid
		if t == 0 {
			col = colAxis
		}
		cv.Line(left, yy, left+plotW, yy, 1, col)
		cv.Text(left-8, yy+tickSize*0.35, tickLabel(t, step), tickSize, false, colMuted, 2, 0)
	}
	// Axis titles.
	cv.Text(24+labelSize, top+plotH/2, shorten(c.YLabel, labelSize, plotH), labelSize, true, colMuted, 1, 90)
	cv.Text(left+plotW/2, H-16, shorten(c.XLabel, labelSize, plotW), labelSize, true, colMuted, 1, 0)
	cx := func(i int) float64 { return left + slot*(float64(i)+0.5) }
	switch c.Kind {
	case Line:
		var pts [][2]float64
		for i, v := range c.Values {
			pts = append(pts, [2]float64{cx(i), y(v)})
		}
		if len(pts) > 1 {
			cv.Polyline(pts, 2.5, colAccent)
		}
		r := 3.5
		if n > 40 {
			r = 2
		}
		for _, p := range pts {
			cv.Circle(p[0], p[1], r, colAccent)
		}
	default:
		bw := math.Min(slot*0.62, 90)
		for i, v := range c.Values {
			y0, y1 := y(0), y(v)
			if y1 > y0 {
				y0, y1 = y1, y0
			}
			cv.Rect(cx(i)-bw/2, y1, bw, math.Max(y0-y1, 0.5), colAccent)
			lab := fmtV(v)
			if TextWidth(lab, valueSize, false) <= slot-2 || n <= 12 {
				ly := y(v) - 6
				if v < 0 {
					ly = y(v) + valueSize + 4
				}
				cv.Text(cx(i), ly, lab, valueSize, false, colInk, 1, 0)
			}
		}
	}
	// X axis line and labels.
	cv.Line(left, top+plotH, left+plotW, top+plotH, 1, colAxis)
	for i := range labels {
		if !shown[i] {
			continue
		}
		ly := top + plotH + 6 + tickSize
		if rotate {
			cv.Text(cx(i)+tickSize*0.3, top+plotH+10, labels[i], tickSize, false, colInk, 2, ang)
		} else {
			cv.Text(cx(i), ly, labels[i], tickSize, false, colInk, 1, 0)
		}
	}
}

// ---------- SVG ----------

type svgCanvas struct{ b strings.Builder }

func xmlEsc(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;").Replace(s)
}

func f2(v float64) string {
	return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%.2f", v), "0"), ".")
}

func (s *svgCanvas) Rect(x, y, w, h float64, fill string) {
	fmt.Fprintf(&s.b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s"/>`+"\n", f2(x), f2(y), f2(w), f2(h), fill)
}
func (s *svgCanvas) Line(x1, y1, x2, y2, w float64, col string) {
	fmt.Fprintf(&s.b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s"/>`+"\n", f2(x1), f2(y1), f2(x2), f2(y2), col, f2(w))
}
func (s *svgCanvas) Polyline(pts [][2]float64, w float64, col string) {
	var p []string
	for _, q := range pts {
		p = append(p, f2(q[0])+","+f2(q[1]))
	}
	fmt.Fprintf(&s.b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%s" stroke-linejoin="round" stroke-linecap="round"/>`+"\n", strings.Join(p, " "), col, f2(w))
}
func (s *svgCanvas) Circle(x, y, r float64, fill string) {
	fmt.Fprintf(&s.b, `<circle cx="%s" cy="%s" r="%s" fill="%s"/>`+"\n", f2(x), f2(y), f2(r), fill)
}
func (s *svgCanvas) Text(x, y float64, t string, size float64, bold bool, col string, anchor int, angle float64) {
	a := [...]string{"start", "middle", "end"}[anchor]
	wt := ""
	if bold {
		wt = ` font-weight="bold"`
	}
	tr := ""
	if angle != 0 {
		tr = fmt.Sprintf(` transform="rotate(%s %s %s)"`, f2(-angle), f2(x), f2(y))
	}
	fmt.Fprintf(&s.b, `<text x="%s" y="%s" font-size="%s"%s fill="%s" text-anchor="%s"%s>%s</text>`+"\n", f2(x), f2(y), f2(size), wt, col, a, tr, xmlEsc(t))
}

// SVG renders the chart as a standalone SVG document.
func (c *Chart) SVG() string {
	s := &svgCanvas{}
	fmt.Fprintf(&s.b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" font-family="Helvetica, Arial, sans-serif" role="img" aria-label="%s">`+"\n",
		f2(c.W), f2(c.H), f2(c.W), f2(c.H), xmlEsc(c.Title))
	fmt.Fprintf(&s.b, "<title>%s</title>\n", xmlEsc(c.Title))
	c.Draw(s)
	s.b.WriteString("</svg>\n")
	return s.b.String()
}
