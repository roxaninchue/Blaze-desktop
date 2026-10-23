// Package csvkit is Blaze Desktop's local CSV workflow (W2, local half): read a messy CSV,
// clean it, infer column types, summarise a value by group, draw a bar or line chart (SVG,
// PNG) and export a Markdown and PDF report. Everything runs on this computer with no model
// calls; the written summary is a slot (Report.Summary) a model fills in later.
package csvkit

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// MaxBytes is the largest file Read accepts (the table lives in memory).
const MaxBytes = 64 << 20

// Raw is a parsed file before cleaning: a header and rows padded to the header's width.
type Raw struct {
	Header    []string
	Rows      [][]string
	Delimiter rune
	BOM       string // "", "utf-8", "utf-16le" or "utf-16be"
	Ragged    int    // rows that were shorter or longer than the header
	Extra     int    // columns added for rows longer than the header
}

// Parse reads CSV bytes: strips a BOM (UTF-16 is decoded), sniffs the delimiter, reads quoted
// fields leniently and evens out ragged rows. The first non-blank row is the header.
func Parse(b []byte) (*Raw, error) {
	if len(b) > MaxBytes {
		return nil, fmt.Errorf("the file is over %d MB", MaxBytes>>20)
	}
	r := &Raw{}
	b, r.BOM = decodeBOM(b)
	if !utf8.Valid(b) {
		b = latin1(b)
	}
	b = bytes.ReplaceAll(b, []byte("\r\n"), []byte("\n"))
	b = bytes.ReplaceAll(b, []byte("\r"), []byte("\n"))
	if len(bytes.TrimSpace(b)) == 0 {
		return nil, errors.New("the file is empty")
	}
	r.Delimiter = Sniff(b)
	cr := csv.NewReader(bytes.NewReader(b))
	cr.Comma = r.Delimiter
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	cr.ReuseRecord = false
	var recs [][]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			var pe *csv.ParseError
			if errors.As(err, &pe) && rec != nil {
				recs = append(recs, rec)
				continue
			}
			return nil, err
		}
		recs = append(recs, rec)
	}
	// The header is the first row with anything in it.
	start := 0
	for start < len(recs) && blankRow(recs[start]) {
		start++
	}
	if start >= len(recs) {
		return nil, errors.New("the file has no rows")
	}
	r.Header = headerNames(recs[start])
	w := len(r.Header)
	for _, rec := range recs[start+1:] {
		if len(rec) != w {
			r.Ragged++
		}
		// A longer row keeps its extra cells only when they hold something.
		if len(rec) > w {
			last := len(rec)
			for last > w && strings.TrimSpace(rec[last-1]) == "" {
				last--
			}
			rec = rec[:last]
			for len(rec) > len(r.Header) {
				r.Header = append(r.Header, fmt.Sprintf("Column %d", len(r.Header)+1))
				r.Extra++
			}
		}
		r.Rows = append(r.Rows, rec)
	}
	w = len(r.Header)
	for i, rec := range r.Rows {
		if len(rec) < w {
			r.Rows[i] = append(append(make([]string, 0, w), rec...), make([]string, w-len(rec))...)
		}
	}
	return r, nil
}

// Sniff picks the delimiter (comma, semicolon, tab or pipe) that splits the first lines into
// the most consistent number of fields, ignoring delimiters inside quotes.
func Sniff(b []byte) rune {
	lines := sampleLines(b, 30)
	best, bestScore := ',', -1.0
	for _, d := range []rune{',', ';', '\t', '|'} {
		counts := map[int]int{}
		n := 0
		for _, ln := range lines {
			c := countOutsideQuotes(ln, d)
			counts[c]++
			n++
		}
		if n == 0 {
			continue
		}
		mode, modeN := 0, 0
		for c, k := range counts {
			if k > modeN || k == modeN && c > mode {
				mode, modeN = c, k
			}
		}
		if mode == 0 {
			continue
		}
		// Consistency first, then more fields.
		score := float64(modeN)/float64(n)*100 + float64(mode)/100
		if score > bestScore {
			best, bestScore = d, score
		}
	}
	return best
}

func sampleLines(b []byte, max int) []string {
	var out []string
	inQ := false
	var cur strings.Builder
	for _, c := range string(b) {
		if c == '"' {
			inQ = !inQ
		}
		if c == '\n' && !inQ {
			if strings.TrimSpace(cur.String()) != "" {
				out = append(out, cur.String())
			}
			cur.Reset()
			if len(out) >= max {
				return out
			}
			continue
		}
		cur.WriteRune(c)
	}
	if strings.TrimSpace(cur.String()) != "" && len(out) < max {
		out = append(out, cur.String())
	}
	return out
}

func countOutsideQuotes(s string, d rune) int {
	n, inQ := 0, false
	for _, c := range s {
		switch {
		case c == '"':
			inQ = !inQ
		case c == d && !inQ:
			n++
		}
	}
	return n
}

func decodeBOM(b []byte) ([]byte, string) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return b[3:], "utf-8"
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}):
		return utf16Decode(b[2:], false), "utf-16le"
	case bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		return utf16Decode(b[2:], true), "utf-16be"
	}
	return b, ""
}

func utf16Decode(b []byte, be bool) []byte {
	u := make([]uint16, 0, len(b)/2)
	for i := 0; i+1 < len(b); i += 2 {
		if be {
			u = append(u, uint16(b[i])<<8|uint16(b[i+1]))
		} else {
			u = append(u, uint16(b[i+1])<<8|uint16(b[i]))
		}
	}
	return []byte(string(utf16.Decode(u)))
}

// latin1 turns bytes that aren't UTF-8 into UTF-8 as Windows-1252/Latin-1 (old Excel exports).
func latin1(b []byte) []byte {
	var sb strings.Builder
	for _, c := range b {
		if c >= 0x80 && c <= 0x9F {
			if r, ok := cp1252[c]; ok {
				sb.WriteRune(r)
				continue
			}
		}
		sb.WriteRune(rune(c))
	}
	return []byte(sb.String())
}

var cp1252 = map[byte]rune{0x80: '€', 0x82: '‚', 0x84: '„', 0x85: '…', 0x91: '\'', 0x92: '\'', 0x93: '"', 0x94: '"', 0x96: '–', 0x97: '—', 0x99: '™'}

func blankRow(rec []string) bool {
	for _, c := range rec {
		if strings.TrimSpace(c) != "" {
			return false
		}
	}
	return true
}

// headerNames trims header cells, names blank ones "Column N" and makes repeats unique.
func headerNames(rec []string) []string {
	out := make([]string, len(rec))
	seen := map[string]int{}
	for i, c := range rec {
		n := collapse(c)
		if n == "" {
			n = fmt.Sprintf("Column %d", i+1)
		}
		k := strings.ToLower(n)
		if seen[k] > 0 {
			n = fmt.Sprintf("%s (%d)", n, seen[k]+1)
		}
		seen[k]++
		out[i] = n
	}
	return out
}

// collapse trims a cell and squeezes runs of whitespace to one space.
func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00a0", " ")), " ")
}
