package csvkit

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SummaryMarker opens the slot in the Markdown report where the model's written summary goes.
const (
	SummaryMarker    = "<!-- blaze:summary -->"
	SummaryEndMarker = "<!-- /blaze:summary -->"
	SummaryPending   = "The written summary appears here once a model is available. Everything else in this report was computed on this computer."
)

// Options choose what to summarise. Empty fields are picked from the data.
type Options struct {
	GroupBy string // column to group by
	Value   string // number column to total
	Agg     string // sum (default), count or mean
	Chart   string // bar, line or "" (bar; line for dates)
	OutDir  string // where report files go ("" = next to the source)
	// FallbackDir is used when the source's folder can't be written.
	FallbackDir string
	Now         time.Time // the report's timestamp (zero = now)
	// PreviewRows is how many cleaned rows the Report carries for the table view (default 200).
	PreviewRows int
}

// Files are the paths Run wrote.
type Files struct {
	Cleaned string `json:"cleaned"`
	SVG     string `json:"svg"`
	PNG     string `json:"png,omitempty"`
	MD      string `json:"md"`
	PDF     string `json:"pdf"`
}

// Report is everything the table view, the agent tool and the exports need.
type Report struct {
	Source    string     `json:"source"`
	Name      string     `json:"name"`
	Delimiter string     `json:"delimiter"`
	Columns   []Column   `json:"columns"`
	Preview   [][]string `json:"preview"` // first PreviewRows cleaned rows
	Rows      int        `json:"rows"`
	Stats     Stats      `json:"stats"`
	Summary   *Summary   `json:"summary"`
	ChartKind string     `json:"chart_kind"`
	ChartSVG  string     `json:"chart_svg"`
	Notes     []string   `json:"notes"`
	Files     Files      `json:"files"`
	// Written is the model's summary; empty until a model writes it (SetSummary).
	Written   string    `json:"written,omitempty"`
	Generated time.Time `json:"generated"`

	table *Table
	chart *Chart
}

// Analyze parses, cleans and summarises CSV bytes without writing anything.
func Analyze(b []byte, name string, opt Options) (*Report, error) {
	raw, err := Parse(b)
	if err != nil {
		return nil, err
	}
	t := Clean(raw)
	if len(t.Rows) == 0 {
		return nil, errors.New("no rows are left after cleaning")
	}
	by, val := t.Pick()
	if opt.GroupBy != "" {
		if by = t.Index(opt.GroupBy); by < 0 {
			return nil, fmt.Errorf("there's no column %q (columns: %s)", opt.GroupBy, colNames(t))
		}
	}
	if opt.Value != "" {
		if val = t.Index(opt.Value); val < 0 {
			return nil, fmt.Errorf("there's no column %q (columns: %s)", opt.Value, colNames(t))
		}
	}
	if by < 0 { // no category column: a date column still groups
		for i, c := range t.Columns {
			if c.Type == TypeDate {
				by = i
				break
			}
		}
	}
	if by < 0 {
		return nil, fmt.Errorf("found no column to group by; name one (columns: %s)", colNames(t))
	}
	s, err := t.GroupBy(by, val, opt.Agg)
	if err != nil {
		return nil, err
	}
	kind := strings.ToLower(strings.TrimSpace(opt.Chart))
	if kind != "" && kind != Bar && kind != Line {
		return nil, fmt.Errorf("chart must be bar or line")
	}
	ch := NewChart(s, kind)
	now := opt.Now
	if now.IsZero() {
		now = time.Now()
	}
	pr := opt.PreviewRows
	if pr <= 0 {
		pr = 200
	}
	if pr > len(t.Rows) {
		pr = len(t.Rows)
	}
	r := &Report{Name: name, Delimiter: delimName(raw.Delimiter), Columns: t.Columns, Preview: t.Rows[:pr], Rows: len(t.Rows),
		Stats: t.Stats, Summary: s, ChartKind: ch.Kind, ChartSVG: ch.SVG(), Generated: now, table: t, chart: ch}
	r.Notes = notes(raw, t, s)
	return r, nil
}

// Run reads the CSV at path, analyses it and writes the cleaned CSV, the chart (SVG and
// PNG) and the Markdown and PDF reports next to it (or in OutDir). The source is never
// changed and existing files of the same name are replaced.
func Run(path string, opt Options) (*Report, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() {
		return nil, errors.New("that's a folder, not a CSV file")
	}
	if st.Size() > MaxBytes {
		return nil, fmt.Errorf("the file is over %d MB", MaxBytes>>20)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	r, err := Analyze(b, filepath.Base(path), opt)
	if err != nil {
		return nil, err
	}
	r.Source = path
	dir := opt.OutDir
	if dir == "" {
		dir = filepath.Dir(path)
	}
	if err := r.writeAll(dir, path); err != nil {
		if opt.FallbackDir == "" || opt.OutDir != "" {
			return nil, err
		}
		if err2 := os.MkdirAll(opt.FallbackDir, 0o755); err2 != nil {
			return nil, err
		}
		if err2 := r.writeAll(opt.FallbackDir, path); err2 != nil {
			return nil, err2
		}
		r.Notes = append(r.Notes, "The source folder couldn't be written, so the files are in "+opt.FallbackDir+".")
	}
	return r, nil
}

func (r *Report) writeAll(dir, src string) error {
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	base = strings.TrimSuffix(base, "-clean")
	f := Files{
		Cleaned: filepath.Join(dir, base+"-clean.csv"),
		SVG:     filepath.Join(dir, base+"-chart.svg"),
		PNG:     filepath.Join(dir, base+"-chart.png"),
		MD:      filepath.Join(dir, base+"-report.md"),
		PDF:     filepath.Join(dir, base+"-report.pdf"),
	}
	if same(f.Cleaned, src) { // never overwrite the file we read
		f.Cleaned = filepath.Join(dir, base+"-clean-2.csv")
	}
	if err := os.WriteFile(f.Cleaned, r.CleanedCSV(), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(f.SVG, []byte(r.ChartSVG), 0o644); err != nil {
		return err
	}
	if png, err := r.chart.PNG(2); err == nil {
		if err := os.WriteFile(f.PNG, png, 0o644); err != nil {
			return err
		}
	} else {
		f.PNG = ""
	}
	r.Files = f
	return r.WriteReports()
}

func same(a, b string) bool {
	sa, e1 := os.Stat(a)
	sb, e2 := os.Stat(b)
	return e1 == nil && e2 == nil && os.SameFile(sa, sb)
}

// SetSummary puts the model's written summary into the report and rewrites the Markdown and
// PDF files (when Run wrote them).
func (r *Report) SetSummary(text string) error {
	r.Written = strings.TrimSpace(text)
	if r.Files.MD == "" {
		return nil
	}
	return r.WriteReports()
}

// WriteReports writes the Markdown and PDF reports to r.Files.
func (r *Report) WriteReports() error {
	if err := os.WriteFile(r.Files.MD, []byte(r.Markdown()), 0o644); err != nil {
		return err
	}
	return os.WriteFile(r.Files.PDF, r.PDF(), 0o644)
}

// CleanedCSV is the cleaned table as comma-separated UTF-8 with a header row.
func (r *Report) CleanedCSV() []byte {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	var hdr []string
	for _, c := range r.table.Columns {
		hdr = append(hdr, c.Name)
	}
	_ = w.Write(hdr)
	_ = w.WriteAll(r.table.Rows)
	w.Flush()
	return buf.Bytes()
}

// Table is the cleaned table behind the report.
func (r *Report) Table() *Table { return r.table }

func colNames(t *Table) string {
	var n []string
	for _, c := range t.Columns {
		n = append(n, c.Name)
	}
	return strings.Join(n, ", ")
}

func delimName(d rune) string {
	switch d {
	case ';':
		return "semicolon"
	case '\t':
		return "tab"
	case '|':
		return "pipe"
	}
	return "comma"
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// notes are the plain-English lines saying what cleaning did.
func notes(raw *Raw, t *Table, s *Summary) []string {
	st := t.Stats
	out := []string{fmt.Sprintf("Read %s (%s-separated%s); %s kept after cleaning.",
		plural(st.RowsIn, "row", "rows"), delimName(raw.Delimiter), map[bool]string{true: ", " + raw.BOM + " BOM removed", false: ""}[raw.BOM != ""], plural(st.RowsOut, "row", "rows"))}
	if st.Duplicates > 0 {
		out = append(out, "Removed "+plural(st.Duplicates, "exact duplicate row", "exact duplicate rows")+" (identical once cleaned).")
	}
	if st.BlankRows > 0 {
		out = append(out, "Removed "+plural(st.BlankRows, "blank row", "blank rows")+".")
	}
	if st.HeaderRepeats > 0 {
		out = append(out, "Removed "+plural(st.HeaderRepeats, "repeated header row", "repeated header rows")+".")
	}
	if st.Ragged > 0 {
		out = append(out, plural(st.Ragged, "row had", "rows had")+" a different number of cells than the header; short rows were padded with blanks.")
	}
	if st.Trimmed > 0 {
		out = append(out, "Trimmed stray spaces in "+plural(st.Trimmed, "cell", "cells")+".")
	}
	for _, c := range t.Columns {
		if c.Type == TypeDate && st.Dates > 0 {
			l := "Wrote the " + c.Name + " column as YYYY-MM-DD"
			switch c.DateOrder {
			case "DMY":
				l += " (numeric dates like 02/09 read day-first)"
			case "MDY":
				l += " (numeric dates like 02/09 read month-first)"
			case "DMY+MDY":
				l += " (dd/mm/yyyy and mm-dd-yy styles each read the way the data shows)"
			}
			out = append(out, l+".")
		}
	}
	if st.Numbers > 0 {
		out = append(out, "Wrote "+plural(st.Numbers, "number", "numbers")+" plainly (currency symbols and thousands separators removed, fixed decimals).")
	}
	if st.CaseFixed > 0 {
		var cols []string
		for _, c := range t.Columns {
			if c.Type == TypeCategory {
				cols = append(cols, c.Name)
			}
		}
		out = append(out, "Made the spelling of "+strings.Join(cols, ", ")+" consistent ("+plural(st.CaseFixed, "cell", "cells")+" changed case).")
	}
	if st.Filled > 0 {
		out = append(out, "Filled "+plural(st.Filled, "blank cell", "blank cells")+" using "+st.FillRule+", which holds on the other rows.")
	}
	if st.Unparsed > 0 {
		out = append(out, plural(st.Unparsed, "cell", "cells")+" didn't match their column's type and were kept as written.")
	}
	for _, g := range s.Groups {
		if g.Key == Blank {
			out = append(out, fmt.Sprintf("%s no %s; grouped as %s.", plural(g.Rows, "row has", "rows have"), s.By, Blank))
		}
	}
	for _, c := range t.Columns {
		if c.Type != TypeEmpty && c.Missing > 0 && c.Name != s.By {
			out = append(out, fmt.Sprintf("%s is blank in %s.", c.Name, plural(c.Missing, "row", "rows")))
		}
	}
	return out
}
