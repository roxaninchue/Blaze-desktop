package csvkit

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
)

// MaxReportRows is how many cleaned rows the Markdown and PDF reports list.
const MaxReportRows = 500

func mdCell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func mdRow(cells []string) string {
	var c []string
	for _, x := range cells {
		c = append(c, mdCell(x))
	}
	return "| " + strings.Join(c, " | ") + " |\n"
}

func mdAlign(n int, right []bool) string {
	var c []string
	for i := 0; i < n; i++ {
		if i < len(right) && right[i] {
			c = append(c, "---:")
		} else {
			c = append(c, "---")
		}
	}
	return "| " + strings.Join(c, " | ") + " |\n"
}

func (r *Report) title() string {
	return strings.TrimSuffix(r.Name, filepath.Ext(r.Name))
}

// summaryRows is the group table: header and rows (with a total row).
func (r *Report) summaryRows() (hdr []string, rows [][]string) {
	s := r.Summary
	if s.Value == "" {
		hdr = []string{s.By, "Rows"}
		for _, g := range s.Groups {
			rows = append(rows, []string{g.Key, fmt.Sprint(g.Rows)})
		}
		rows = append(rows, []string{"Total", fmt.Sprint(s.Total.Rows)})
		return
	}
	hdr = []string{s.By, "Sum of " + s.Value, "Count", "Mean " + s.Value}
	add := func(g Group) []string {
		return []string{g.Key, s.Fmt(g.Sum, "sum"), fmt.Sprint(g.Count), s.Fmt(g.Mean, "mean")}
	}
	for _, g := range s.Groups {
		rows = append(rows, add(g))
	}
	rows = append(rows, add(s.Total))
	return
}

// Markdown is the report as Markdown: the summary slot, the chart (PNG, with the SVG linked),
// the group table, what cleaning did, the columns and the cleaned rows.
func (r *Report) Markdown() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s: data report\n\n", r.title())
	src := r.Name
	clean := filepath.Base(r.Files.Cleaned)
	fmt.Fprintf(&b, "Source: `%s` · cleaned copy: `%s` · %s rows · generated %s\n\n", src, orDash(clean), thousands(fmt.Sprint(r.Rows)), r.Generated.Format("2006-01-02 15:04"))
	b.WriteString("## Summary\n\n" + SummaryMarker + "\n")
	if r.Written != "" {
		b.WriteString(r.Written + "\n")
	} else {
		b.WriteString("_" + SummaryPending + "_\n")
	}
	b.WriteString(SummaryEndMarker + "\n\n")
	fmt.Fprintf(&b, "## %s\n\n", r.Summary.Label())
	if r.Files.PNG != "" {
		fmt.Fprintf(&b, "![%s](%s)\n\n", r.Summary.Label(), filepath.Base(r.Files.PNG))
	}
	if r.Files.SVG != "" {
		fmt.Fprintf(&b, "Chart as SVG: [%s](%s)\n\n", filepath.Base(r.Files.SVG), filepath.Base(r.Files.SVG))
	}
	hdr, rows := r.summaryRows()
	right := []bool{false, true, true, true}
	b.WriteString(mdRow(hdr) + mdAlign(len(hdr), right))
	for i, row := range rows {
		if i == len(rows)-1 {
			for j := range row {
				row[j] = "**" + row[j] + "**"
			}
		}
		b.WriteString(mdRow(row))
	}
	b.WriteString("\n## Cleaning\n\n")
	for _, n := range r.Notes {
		b.WriteString("- " + n + "\n")
	}
	b.WriteString("\n## Columns\n\n" + mdRow([]string{"Column", "Type", "Blank", "Distinct"}) + mdAlign(4, []bool{false, false, true, true}))
	for _, c := range r.Columns {
		ty := c.Type
		if c.Currency != "" {
			ty += " (" + c.Currency + ")"
		}
		b.WriteString(mdRow([]string{c.Name, ty, fmt.Sprint(c.Missing), fmt.Sprint(c.Distinct)}))
	}
	t := r.table
	n := len(t.Rows)
	if n > MaxReportRows {
		n = MaxReportRows
	}
	fmt.Fprintf(&b, "\n## Cleaned data\n\n")
	if n < len(t.Rows) {
		fmt.Fprintf(&b, "First %d of %d rows; the full table is in `%s`.\n\n", n, len(t.Rows), clean)
	}
	var names []string
	var al []bool
	for _, c := range t.Columns {
		names = append(names, c.Name)
		al = append(al, c.Type == TypeNumber)
	}
	b.WriteString(mdRow(names) + mdAlign(len(names), al))
	for _, row := range t.Rows[:n] {
		b.WriteString(mdRow(row))
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" || s == "." {
		return "-"
	}
	return s
}

// ---------- PDF ----------

const (
	pageW, pageH = 595.0, 842.0 // A4
	margin       = 40.0
)

type pdfFlow struct {
	p *PDF
	y float64
}

func (f *pdfFlow) need(h float64) {
	if f.p.Pages() == 0 || f.y+h > pageH-margin {
		f.p.AddPage()
		f.y = margin
	}
}

func (f *pdfFlow) heading(s string) {
	f.need(40)
	f.y += 22
	f.p.Text(margin, f.y, s, 13, true, colInk, 0, 0)
	f.y += 8
}

// para writes wrapped text.
func (f *pdfFlow) para(s string, size float64, col string, indent float64) {
	for _, ln := range wrap(s, size, pageW-2*margin-indent) {
		f.need(size + 4)
		f.y += size + 3
		f.p.Text(margin+indent, f.y, ln, size, false, col, 0, 0)
	}
}

func wrap(s string, size, width float64) []string {
	var out []string
	cur := ""
	for _, w := range strings.Fields(s) {
		next := w
		if cur != "" {
			next = cur + " " + w
		}
		if TextWidth(next, size, false) > width && cur != "" {
			out = append(out, cur)
			cur = w
		} else {
			cur = next
		}
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

// table draws rows with a bold header, right-aligning the columns in right; columns shrink
// (cells are cut with an ellipsis) to fit the page width. The header repeats on new pages.
func (f *pdfFlow) table(hdr []string, rows [][]string, right []bool, size float64, boldLast bool) {
	avail := pageW - 2*margin
	n := len(hdr)
	ws := make([]float64, n)
	for i, h := range hdr {
		ws[i] = TextWidth(h, size, true)
	}
	for k, row := range rows {
		bold := boldLast && k == len(rows)-1
		for i := 0; i < n && i < len(row); i++ {
			ws[i] = math.Max(ws[i], TextWidth(row[i], size, bold))
		}
	}
	const pad = 8.0
	total := 0.0
	for i := range ws {
		ws[i] += pad
		total += ws[i]
	}
	if total > avail { // shrink the widest columns first
		for total > avail {
			wi := 0
			for i := range ws {
				if ws[i] > ws[wi] {
					wi = i
				}
			}
			cut := math.Min(total-avail, ws[wi]*0.1+1)
			ws[wi] -= cut
			total -= cut
		}
	}
	rowH := size + 6
	drawRow := func(cells []string, bold bool, bg string) {
		f.need(rowH)
		if bg != "" {
			f.p.Rect(margin, f.y, total, rowH, bg)
		}
		x := margin
		for i := 0; i < n; i++ {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			wd := ws[i] - pad
			c = shortenB(c, size, wd, bold)
			if i < len(right) && right[i] {
				f.p.Text(x+ws[i]-pad/2, f.y+rowH-4.5, c, size, bold, colInk, 2, 0)
			} else {
				f.p.Text(x+pad/2, f.y+rowH-4.5, c, size, bold, colInk, 0, 0)
			}
			x += ws[i]
		}
		f.y += rowH
	}
	f.need(rowH * 2)
	f.y += 4
	drawRow(hdr, true, "#f1f3f5")
	for i, row := range rows {
		if f.y+rowH > pageH-margin {
			f.need(rowH * 2)
			drawRow(hdr, true, "#f1f3f5")
		}
		bg := ""
		if i%2 == 1 {
			bg = "#fafbfc"
		}
		last := boldLast && i == len(rows)-1
		if last {
			f.p.Line(margin, f.y, margin+total, f.y, 0.8, colAxis)
		}
		drawRow(row, last, bg)
	}
}

func shortenB(s string, size, width float64, bold bool) string {
	if TextWidth(s, size, bold) <= width+0.01 {
		return s
	}
	rs := []rune(s)
	for len(rs) > 0 && TextWidth(string(rs)+"…", size, bold) > width {
		rs = rs[:len(rs)-1]
	}
	if len(rs) == 0 {
		return ""
	}
	return string(rs) + "…"
}

// PDF is the report as an A4 PDF: title, summary slot, chart (vector), group table, cleaning
// notes, then the cleaned rows.
func (r *Report) PDF() []byte {
	p := NewPDF(pageW, pageH)
	f := &pdfFlow{p: p}
	f.need(100)
	f.y += 20
	p.Text(margin, f.y, shortenB(r.title()+": data report", 20, pageW-2*margin, true), 20, true, colInk, 0, 0)
	f.y += 6
	f.para(fmt.Sprintf("Source: %s · cleaned copy: %s · %s rows · generated %s", r.Name, orDash(filepath.Base(r.Files.Cleaned)),
		thousands(fmt.Sprint(r.Rows)), r.Generated.Format("2006-01-02 15:04")), 9, colMuted, 0)
	f.heading("Summary")
	if r.Written != "" {
		for _, para := range strings.Split(r.Written, "\n") {
			if strings.TrimSpace(para) != "" {
				f.para(strings.TrimSpace(para), 10, colInk, 0)
			}
		}
	} else {
		f.para(SummaryPending, 10, colMuted, 0)
	}
	// The chart, scaled to the text width.
	c := r.chart
	sc := (pageW - 2*margin) / c.W
	ch := c.H * sc
	f.need(ch + 20)
	f.y += 14
	p.OX, p.OY, p.S = margin, f.y, sc
	c.Draw(p)
	p.OX, p.OY, p.S = 0, 0, 1
	f.y += ch
	hdr, rows := r.summaryRows()
	f.heading(r.Summary.Label())
	f.table(hdr, rows, []bool{false, true, true, true}, 10, true)
	f.heading("Cleaning")
	for _, n := range r.Notes {
		lines := wrap(n, 9.5, pageW-2*margin-12)
		for i, ln := range lines {
			f.need(13)
			f.y += 12.5
			if i == 0 {
				p.Text(margin+2, f.y, "•", 9.5, false, colMuted, 0, 0)
			}
			p.Text(margin+12, f.y, ln, 9.5, false, colInk, 0, 0)
		}
	}
	t := r.table
	n := len(t.Rows)
	if n > MaxReportRows {
		n = MaxReportRows
	}
	p.AddPage()
	f.y = margin - 22
	f.heading("Cleaned data")
	if n < len(t.Rows) {
		f.para(fmt.Sprintf("First %d of %d rows; the full table is in %s.", n, len(t.Rows), filepath.Base(r.Files.Cleaned)), 9, colMuted, 0)
	}
	var names []string
	var al []bool
	for _, col := range t.Columns {
		names = append(names, col.Name)
		al = append(al, col.Type == TypeNumber)
	}
	size := 8.5
	if len(names) > 8 {
		size = 7
	}
	f.table(names, t.Rows[:n], al, size, false)
	return p.Bytes()
}
