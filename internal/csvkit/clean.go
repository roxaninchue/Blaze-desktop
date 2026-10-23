package csvkit

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Column types.
const (
	TypeDate     = "date"
	TypeNumber   = "number"
	TypeCategory = "category"
	TypeText     = "text"
	TypeEmpty    = "empty"
)

// Column describes one cleaned column.
type Column struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Currency  string `json:"currency,omitempty"`   // a currency symbol or code seen in the cells
	Scale     int    `json:"scale,omitempty"`      // decimal places numbers are written with
	DateOrder string `json:"date_order,omitempty"` // how ambiguous a/b/yyyy dates were read: "DMY", "MDY" or "DMY+MDY"
	Missing   int    `json:"missing"`
	Distinct  int    `json:"distinct"`
	Unparsed  int    `json:"unparsed,omitempty"` // cells that didn't fit the column's type (kept as written)
}

// Stats says what cleaning did.
type Stats struct {
	RowsIn        int    `json:"rows_in"`
	RowsOut       int    `json:"rows_out"`
	BlankRows     int    `json:"blank_rows"`
	HeaderRepeats int    `json:"header_repeats"`
	Duplicates    int    `json:"duplicates"`
	Ragged        int    `json:"ragged_rows"`
	Trimmed       int    `json:"trimmed_cells"`
	Dates         int    `json:"dates_normalised"`
	Numbers       int    `json:"numbers_normalised"`
	CaseFixed     int    `json:"case_fixed"`
	Filled        int    `json:"filled_cells"`
	FillRule      string `json:"fill_rule,omitempty"` // e.g. "Total = Qty × Unit Price"
	Unparsed      int    `json:"unparsed_cells"`
}

// Table is a cleaned CSV: typed columns and rows of normalised strings.
type Table struct {
	Columns []Column   `json:"columns"`
	Rows    [][]string `json:"rows"`
	Stats   Stats      `json:"stats"`
	num     [][]float64
}

// Num is the numeric value of a cell (NaN when blank or not a number column).
func (t *Table) Num(r, c int) float64 {
	if t.num == nil || t.num[r] == nil {
		return math.NaN()
	}
	return t.num[r][c]
}

// Index finds a column by name (case-insensitive), -1 when absent.
func (t *Table) Index(name string) int {
	name = strings.ToLower(collapse(name))
	for i, c := range t.Columns {
		if strings.ToLower(c.Name) == name {
			return i
		}
	}
	return -1
}

// Clean trims cells, drops blank rows and repeated headers, infers column types, normalises
// dates to ISO, numbers to plain decimals and category spellings to one form, fills a cell
// that a clear arithmetic rule between columns gives (Total = Qty × Price) and drops rows
// that are exact duplicates once cleaned. The source order of the remaining rows is kept.
func Clean(raw *Raw) *Table {
	t := &Table{}
	w := len(raw.Header)
	t.Stats.RowsIn = len(raw.Rows)
	t.Stats.Ragged = raw.Ragged
	var rows [][]string
	for _, rec := range raw.Rows {
		row := make([]string, w)
		blank := true
		for i := 0; i < w; i++ {
			v := collapse(rec[i])
			if v != rec[i] {
				t.Stats.Trimmed++
			}
			row[i] = v
			if v != "" {
				blank = false
			}
		}
		if blank {
			t.Stats.BlankRows++
			continue
		}
		if isHeader(row, raw.Header) {
			t.Stats.HeaderRepeats++
			continue
		}
		rows = append(rows, row)
	}
	t.Columns = make([]Column, w)
	num := make([][]float64, len(rows))
	for r := range num {
		num[r] = make([]float64, w)
		for c := range num[r] {
			num[r][c] = math.NaN()
		}
	}
	for c := 0; c < w; c++ {
		col := Column{Name: raw.Header[c]}
		vals := make([]string, len(rows))
		for r := range rows {
			vals[r] = rows[r][c]
		}
		col.Type = inferType(vals)
		switch col.Type {
		case TypeDate:
			out, order, changed, bad := normaliseDates(vals)
			col.DateOrder, col.Unparsed = order, bad
			t.Stats.Dates += changed
			for r := range rows {
				rows[r][c] = out[r]
			}
		case TypeNumber:
			scale, cur, bad := 0, "", 0
			for r, v := range vals {
				if v == "" {
					continue
				}
				n, ok := ParseNumber(v)
				if !ok {
					bad++
					continue
				}
				num[r][c] = n.Value
				if n.Decimals > scale {
					scale = n.Decimals
				}
				if cur == "" {
					cur = n.Currency
				}
			}
			if scale > 6 {
				scale = 6
			}
			col.Scale, col.Currency, col.Unparsed = scale, cur, bad
			for r := range rows {
				if !math.IsNaN(num[r][c]) {
					s := formatNum(num[r][c], scale)
					if s != rows[r][c] {
						t.Stats.Numbers++
					}
					rows[r][c] = s
				}
			}
		case TypeCategory:
			canon := canonicalCase(vals)
			for r, v := range vals {
				if v == "" {
					continue
				}
				if k := canon[strings.ToLower(v)]; k != v {
					t.Stats.CaseFixed++
					rows[r][c] = k
				}
			}
		}
		t.Stats.Unparsed += col.Unparsed
		t.Columns[c] = col
	}
	t.Rows, t.num = rows, num
	t.fillProducts()
	// Exact duplicates, judged on the cleaned row.
	seen := map[string]bool{}
	keep := t.Rows[:0]
	keepN := t.num[:0]
	for r, row := range t.Rows {
		k := strings.Join(row, "\x1f")
		if seen[k] {
			t.Stats.Duplicates++
			continue
		}
		seen[k] = true
		keep = append(keep, row)
		keepN = append(keepN, t.num[r])
	}
	t.Rows, t.num = keep, keepN
	for c := range t.Columns {
		d := map[string]bool{}
		miss := 0
		for _, row := range t.Rows {
			if row[c] == "" {
				miss++
			} else {
				d[row[c]] = true
			}
		}
		t.Columns[c].Missing, t.Columns[c].Distinct = miss, len(d)
	}
	t.Stats.RowsOut = len(t.Rows)
	return t
}

// isHeader: the row repeats the header (cells in columns Parse added may be blank).
func isHeader(row, hdr []string) bool {
	for i, h := range hdr {
		if !strings.EqualFold(row[i], h) && !(row[i] == "" && strings.HasPrefix(h, "Column ")) {
			return false
		}
	}
	return true
}

// inferType: date or number when at least 80% of the filled cells read as one; category when
// a text column repeats a small set of values; text otherwise.
func inferType(vals []string) string {
	n, dates, nums := 0, 0, 0
	distinct := map[string]bool{}
	for _, v := range vals {
		if v == "" {
			continue
		}
		n++
		if _, ok := parseDateParts(v); ok {
			dates++
		}
		if _, ok := ParseNumber(v); ok {
			nums++
		}
		distinct[strings.ToLower(v)] = true
	}
	switch {
	case n == 0:
		return TypeEmpty
	case dates*5 >= n*4:
		return TypeDate
	case nums*5 >= n*4:
		return TypeNumber
	case len(distinct) <= 50 && len(distinct)*2 <= n:
		return TypeCategory
	}
	return TypeText
}

// canonicalCase maps each lower-cased spelling to one form: the most common spelling that
// already mixes upper and lower case, else Title Case.
func canonicalCase(vals []string) map[string]string {
	counts := map[string]map[string]int{}
	for _, v := range vals {
		if v == "" {
			continue
		}
		k := strings.ToLower(v)
		if counts[k] == nil {
			counts[k] = map[string]int{}
		}
		counts[k][v]++
	}
	out := map[string]string{}
	for k, vs := range counts {
		best, bestN := "", 0
		var names []string
		for v := range vs {
			names = append(names, v)
		}
		sort.Strings(names)
		for _, v := range names {
			if mixedCase(v) && vs[v] > bestN {
				best, bestN = v, vs[v]
			}
		}
		if best == "" {
			if len(names) == 1 && !hasLetter(names[0]) {
				best = names[0]
			} else {
				best = titleCase(k)
			}
		}
		out[k] = best
	}
	return out
}

func mixedCase(s string) bool {
	up, lo := false, false
	for _, r := range s {
		up = up || unicode.IsUpper(r)
		lo = lo || unicode.IsLower(r)
	}
	return up && lo
}

func hasLetter(s string) bool {
	for _, r := range s {
		if unicode.IsLetter(r) {
			return true
		}
	}
	return false
}

func titleCase(s string) string {
	rs := []rune(s)
	start := true
	for i, r := range rs {
		if unicode.IsLetter(r) {
			if start {
				rs[i] = unicode.ToUpper(r)
			}
			start = false
		} else {
			start = r == ' ' || r == '-' || r == '/' || r == '(' || r == '&'
		}
	}
	return string(rs)
}

// ---------- numbers ----------

// Number is a parsed numeric cell.
type Number struct {
	Value    float64
	Decimals int
	Currency string
}

var currencyRe = regexp.MustCompile(`(?i)^(US\$|A\$|C\$|R\$|USD|EUR|GBP|ZMW|ZAR|KES|NGN|INR|JPY|CNY|AUD|CAD|CHF|[$€£¥₹₦₩₱])`)
var currencyEndRe = regexp.MustCompile(`(?i)(USD|EUR|GBP|ZMW|ZAR|KES|NGN|INR|JPY|CNY|AUD|CAD|CHF|[$€£¥₹₦₩₱])$`)
var thousandsComma = regexp.MustCompile(`^\d{1,3}(,\d{3})+(\.\d+)?$`)
var thousandsDot = regexp.MustCompile(`^\d{1,3}(\.\d{3})+(,\d+)?$`)

// ParseNumber reads "1,234.50", "$3.40", "1.234,5", "(12)", "-4%", "1 200", "3e5".
func ParseNumber(s string) (Number, bool) {
	var n Number
	s = strings.TrimSpace(s)
	if s == "" {
		return n, false
	}
	neg := false
	if strings.HasPrefix(s, "(") && strings.HasSuffix(s, ")") {
		neg, s = true, strings.TrimSpace(s[1:len(s)-1])
	}
	if strings.HasPrefix(s, "-") || strings.HasPrefix(s, "−") {
		neg = !neg
		s = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(s, "-"), "−"))
	} else if strings.HasPrefix(s, "+") {
		s = strings.TrimSpace(s[1:])
	}
	if m := currencyRe.FindString(s); m != "" {
		n.Currency, s = m, strings.TrimSpace(s[len(m):])
	} else if m := currencyEndRe.FindString(s); m != "" {
		n.Currency, s = m, strings.TrimSpace(s[:len(s)-len(m)])
	}
	if strings.HasPrefix(s, "-") && !neg { // "$-3.40"
		neg, s = true, s[1:]
	}
	s = strings.TrimSuffix(s, "%")
	s = strings.NewReplacer(" ", "", "\u00a0", "", "'", "", "_", "").Replace(s)
	if s == "" {
		return n, false
	}
	hasC, hasD := strings.Contains(s, ","), strings.Contains(s, ".")
	switch {
	case hasC && hasD:
		if strings.LastIndex(s, ",") > strings.LastIndex(s, ".") { // 1.234,56
			s = strings.ReplaceAll(s, ".", "")
			s = strings.Replace(s, ",", ".", 1)
		} else {
			s = strings.ReplaceAll(s, ",", "")
		}
	case hasC:
		if thousandsComma.MatchString(s) {
			s = strings.ReplaceAll(s, ",", "")
		} else if strings.Count(s, ",") == 1 {
			s = strings.Replace(s, ",", ".", 1)
		} else {
			return n, false
		}
	case hasD && strings.Count(s, ".") > 1:
		if !thousandsDot.MatchString(s) {
			return n, false
		}
		s = strings.ReplaceAll(s, ".", "")
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r == '.' || r == 'e' || r == 'E' || r == '-' || r == '+') {
			return n, false
		}
	}
	if s[0] == 'e' || s[0] == 'E' {
		return n, false
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(v, 0) || math.IsNaN(v) {
		return n, false
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && !strings.ContainsAny(s, "eE") {
		n.Decimals = len(s) - i - 1
	}
	if neg {
		v = -v
	}
	n.Value = v
	return n, true
}

func formatNum(v float64, scale int) string {
	s := strconv.FormatFloat(v, 'f', scale, 64)
	if s == "-0" || strings.HasPrefix(s, "-0.") && strings.Trim(s[3:], "0") == "" {
		s = s[1:]
	}
	return s
}

// ---------- dates ----------

type dateParts struct {
	y, m, d int
	a, b    int    // the two leading numbers of an a/b/y date (order not known yet)
	family  string // "" when the order is known, else e.g. "/4" (separator + year digits)
	clock   string // "HH:MM" or "HH:MM:SS" when the cell had a time
}

var (
	isoRe     = regexp.MustCompile(`^(\d{4})[-/.](\d{1,2})[-/.](\d{1,2})(?:[ T](\d{1,2}:\d{2}(?::\d{2})?)(?:\.\d+)?(?:Z|[+-]\d{2}:?\d{2})?)?$`)
	abyRe     = regexp.MustCompile(`^(\d{1,2})([-/.])(\d{1,2})([-/.])(\d{4}|\d{2})(?: (\d{1,2}:\d{2}(?::\d{2})?))?$`)
	mdyNameRe = regexp.MustCompile(`^([A-Za-z]{3,9})\.? (\d{1,2})(?:st|nd|rd|th)?,? (\d{4})$`)
	dmyNameRe = regexp.MustCompile(`^(\d{1,2})(?:st|nd|rd|th)?[ -]([A-Za-z]{3,9})\.?,?[ -](\d{4}|\d{2})$`)
	ymdNameRe = regexp.MustCompile(`^(\d{4})[ -]([A-Za-z]{3,9})[ -](\d{1,2})$`)
)

var months = map[string]int{"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6, "jul": 7, "aug": 8, "sep": 9, "sept": 9, "oct": 10, "nov": 11, "dec": 12}

func monthNum(s string) int {
	s = strings.ToLower(s)
	if m, ok := months[s]; ok {
		return m
	}
	if len(s) > 3 {
		full := []string{"january", "february", "march", "april", "may", "june", "july", "august", "september", "october", "november", "december"}
		for i, f := range full {
			if s == f {
				return i + 1
			}
		}
	}
	return 0
}

func year(s string) int {
	y, _ := strconv.Atoi(s)
	if len(s) == 2 {
		if y < 70 {
			y += 2000
		} else {
			y += 1900
		}
	}
	return y
}

func parseDateParts(s string) (dateParts, bool) {
	var p dateParts
	at := func(x string) int { v, _ := strconv.Atoi(x); return v }
	if m := isoRe.FindStringSubmatch(s); m != nil {
		p.y, p.m, p.d, p.clock = at(m[1]), at(m[2]), at(m[3]), m[4]
		return p, validDate(p.y, p.m, p.d)
	}
	if m := abyRe.FindStringSubmatch(s); m != nil && m[2] == m[4] {
		p.a, p.b, p.y, p.clock = at(m[1]), at(m[3]), year(m[5]), m[6]
		p.family = m[2] + strconv.Itoa(len(m[5]))
		return p, validDate(p.y, p.a, p.b) || validDate(p.y, p.b, p.a)
	}
	if m := mdyNameRe.FindStringSubmatch(s); m != nil {
		p.y, p.m, p.d = at(m[3]), monthNum(m[1]), at(m[2])
		return p, p.m > 0 && validDate(p.y, p.m, p.d)
	}
	if m := dmyNameRe.FindStringSubmatch(s); m != nil {
		p.y, p.m, p.d = year(m[3]), monthNum(m[2]), at(m[1])
		return p, p.m > 0 && validDate(p.y, p.m, p.d)
	}
	if m := ymdNameRe.FindStringSubmatch(s); m != nil {
		p.y, p.m, p.d = at(m[1]), monthNum(m[2]), at(m[3])
		return p, p.m > 0 && validDate(p.y, p.m, p.d)
	}
	return p, false
}

func validDate(y, m, d int) bool {
	if y < 1000 || y > 9999 || m < 1 || m > 12 || d < 1 {
		return false
	}
	days := [...]int{31, 28, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}[m-1]
	if m == 2 && (y%4 == 0 && y%100 != 0 || y%400 == 0) {
		days = 29
	}
	return d <= days
}

// normaliseDates writes every date as YYYY-MM-DD (plus " HH:MM" when it had a time). For
// a/b/yyyy cells the order is decided per separator-and-year-length family from the cells
// that can only be read one way (a 15 can't be a month); a family with no such cell borrows
// the order another family settled, and falls back to month-first.
func normaliseDates(vals []string) (out []string, order string, changed, bad int) {
	parts := make([]dateParts, len(vals))
	ok := make([]bool, len(vals))
	type tally struct{ dayFirst, monthFirst int }
	fam := map[string]*tally{}
	for i, v := range vals {
		if v == "" {
			continue
		}
		parts[i], ok[i] = parseDateParts(v)
		if ok[i] && parts[i].family != "" {
			t := fam[parts[i].family]
			if t == nil {
				t = &tally{}
				fam[parts[i].family] = t
			}
			if parts[i].a > 12 {
				t.dayFirst++
			}
			if parts[i].b > 12 {
				t.monthFirst++
			}
		}
	}
	decided := map[string]string{}
	fallback := ""
	var names []string
	for f := range fam {
		names = append(names, f)
	}
	sort.Strings(names)
	for _, f := range names {
		t := fam[f]
		switch {
		case t.dayFirst > 0 && t.monthFirst == 0:
			decided[f] = "DMY"
		case t.monthFirst > 0 && t.dayFirst == 0:
			decided[f] = "MDY"
		}
		if decided[f] != "" && fallback == "" {
			fallback = decided[f]
		}
	}
	if fallback == "" {
		fallback = "MDY"
	}
	seenOrder := map[string]bool{}
	for _, f := range names {
		if decided[f] == "" {
			decided[f] = fallback
		}
		seenOrder[decided[f]] = true
	}
	switch {
	case seenOrder["DMY"] && seenOrder["MDY"]:
		order = "DMY+MDY"
	case seenOrder["DMY"]:
		order = "DMY"
	case seenOrder["MDY"]:
		order = "MDY"
	}
	out = make([]string, len(vals))
	for i, v := range vals {
		if v == "" {
			continue
		}
		if !ok[i] {
			out[i] = v
			bad++
			continue
		}
		p := parts[i]
		if p.family != "" {
			m, d := p.b, p.a
			if decided[p.family] == "MDY" {
				m, d = p.a, p.b
			}
			if !validDate(p.y, m, d) { // the only reading that works
				m, d = d, m
			}
			p.m, p.d = m, d
		}
		s := fmt.Sprintf("%04d-%02d-%02d", p.y, p.m, p.d)
		if p.clock != "" {
			c := p.clock
			if len(c) == 4 || len(c) == 7 {
				c = "0" + c
			}
			s += " " + c
		}
		if s != v {
			changed++
		}
		out[i] = s
	}
	return
}

// ---------- filling from a column rule ----------

// fillProducts finds numeric columns where one is the product of two others on (nearly)
// every complete row, and fills the single missing cell of a row from the other two.
func (t *Table) fillProducts() {
	var numCols []int
	for i, c := range t.Columns {
		if c.Type == TypeNumber {
			numCols = append(numCols, i)
		}
	}
	if len(numCols) < 3 || len(t.Rows) < 5 {
		return
	}
	close := func(x, y float64) bool { return math.Abs(x-y) <= 0.005*math.Max(1, math.Abs(y))+0.006 }
	bestA, bestB, bestC, bestN := -1, -1, -1, 0
	for _, c := range numCols {
		for i, a := range numCols {
			for _, b := range numCols[i+1:] {
				if a == c || b == c {
					continue
				}
				full, hold := 0, 0
				for r := range t.Rows {
					x, y, z := t.num[r][a], t.num[r][b], t.num[r][c]
					if math.IsNaN(x) || math.IsNaN(y) || math.IsNaN(z) {
						continue
					}
					full++
					if close(x*y, z) {
						hold++
					}
				}
				if full >= 5 && hold*10 >= full*9 && hold > bestN {
					bestA, bestB, bestC, bestN = a, b, c, hold
				}
			}
		}
	}
	if bestC < 0 {
		return
	}
	a, b, c := bestA, bestB, bestC
	set := func(r, col int, v float64) bool {
		sc := t.Columns[col].Scale
		rv := math.Round(v*math.Pow10(sc)) / math.Pow10(sc)
		if !close(rv, v) { // e.g. a fractional quantity in a whole-number column
			return false
		}
		t.num[r][col] = rv
		t.Rows[r][col] = formatNum(rv, sc)
		t.Stats.Filled++
		return true
	}
	for r := range t.Rows {
		x, y, z := t.num[r][a], t.num[r][b], t.num[r][c]
		missing := 0
		for _, v := range []float64{x, y, z} {
			if math.IsNaN(v) {
				missing++
			}
		}
		// Only a truly blank cell is filled (never one that held text we couldn't read).
		if missing != 1 {
			continue
		}
		switch {
		case math.IsNaN(z) && t.Rows[r][c] == "":
			set(r, c, x*y)
		case math.IsNaN(x) && t.Rows[r][a] == "" && y != 0:
			set(r, a, z/y)
		case math.IsNaN(y) && t.Rows[r][b] == "" && x != 0:
			set(r, b, z/x)
		}
	}
	if t.Stats.Filled > 0 {
		t.Stats.FillRule = t.Columns[c].Name + " = " + t.Columns[a].Name + " × " + t.Columns[b].Name
	}
}
