package csvkit

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// Blank is the group label for rows whose group cell is empty.
const Blank = "(blank)"

// Group is one row of a group-by summary.
type Group struct {
	Key   string  `json:"key"`
	Sum   float64 `json:"sum"`
	Count int     `json:"count"` // rows in the group with a value
	Rows  int     `json:"rows"`  // rows in the group
	Mean  float64 `json:"mean"`
	Min   float64 `json:"min"`
	Max   float64 `json:"max"`
}

// Summary is a value column summarised by a group column.
type Summary struct {
	By     string  `json:"by"`
	Value  string  `json:"value"` // "" = row counts only
	Agg    string  `json:"agg"`   // sum, count or mean: what the chart shows
	Scale  int     `json:"scale"`
	Groups []Group `json:"groups"`
	Total  Group   `json:"total"`
	ByDate bool    `json:"by_date,omitempty"` // groups are dates in order (a line chart)
}

// Pick chooses the group and value columns when the caller didn't: the value is the numeric
// column named most like an amount (total, revenue, sales, amount...), else the last numeric
// one; the group is the leftmost category column. Missing pieces come back as -1.
func (t *Table) Pick() (by, value int) {
	by, value = -1, -1
	prefer := []string{"revenue", "sales", "total", "amount", "value", "price", "cost", "qty", "quantity", "count"}
	bestRank := len(prefer)
	for i, c := range t.Columns {
		if c.Type != TypeNumber {
			continue
		}
		n := strings.ToLower(c.Name)
		rank := len(prefer)
		for j, p := range prefer {
			if strings.Contains(n, p) {
				rank = j
				break
			}
		}
		// The best-named column wins (the first of equals); with no name match, the last one.
		if rank < bestRank || rank == len(prefer) && bestRank == len(prefer) {
			value, bestRank = i, rank
		}
	}
	for i, c := range t.Columns {
		if c.Type == TypeCategory {
			by = i
			break
		}
	}
	return
}

// GroupBy sums, counts and averages column value by column by (value -1 = count rows).
// Groups are sorted by the chosen aggregate, largest first; date groups are in date order.
func (t *Table) GroupBy(by, value int, agg string) (*Summary, error) {
	if by < 0 || by >= len(t.Columns) {
		return nil, fmt.Errorf("no column to group by")
	}
	if value >= len(t.Columns) {
		return nil, fmt.Errorf("no such value column")
	}
	if value >= 0 && t.Columns[value].Type != TypeNumber {
		return nil, fmt.Errorf("%q isn't a number column", t.Columns[value].Name)
	}
	agg = strings.ToLower(strings.TrimSpace(agg))
	switch agg {
	case "", "sum", "total":
		agg = "sum"
	case "count", "n", "rows":
		agg = "count"
	case "mean", "avg", "average":
		agg = "mean"
	default:
		return nil, fmt.Errorf("agg must be sum, count or mean")
	}
	if value < 0 {
		agg = "count"
	}
	s := &Summary{By: t.Columns[by].Name, Agg: agg, ByDate: t.Columns[by].Type == TypeDate}
	if value >= 0 {
		s.Value, s.Scale = t.Columns[value].Name, t.Columns[value].Scale
	}
	idx := map[string]int{}
	tot := Group{Key: "Total", Min: math.Inf(1), Max: math.Inf(-1)}
	for r, row := range t.Rows {
		k := row[by]
		if s.ByDate && len(k) > 10 {
			k = k[:10]
		}
		if k == "" {
			k = Blank
		}
		i, ok := idx[k]
		if !ok {
			i = len(s.Groups)
			idx[k] = i
			s.Groups = append(s.Groups, Group{Key: k, Min: math.Inf(1), Max: math.Inf(-1)})
		}
		g := &s.Groups[i]
		g.Rows++
		tot.Rows++
		if value < 0 {
			continue
		}
		v := t.Num(r, value)
		if math.IsNaN(v) {
			continue
		}
		for _, x := range []*Group{g, &tot} {
			x.Sum += v
			x.Count++
			x.Min = math.Min(x.Min, v)
			x.Max = math.Max(x.Max, v)
		}
	}
	fix := func(g *Group) {
		if value < 0 {
			g.Count = g.Rows
		}
		if g.Count > 0 {
			g.Mean = g.Sum / float64(g.Count)
		}
		if math.IsInf(g.Min, 0) {
			g.Min, g.Max = 0, 0
		}
		g.Sum = round(g.Sum, s.Scale+2)
		g.Mean = round(g.Mean, s.Scale+2)
	}
	for i := range s.Groups {
		fix(&s.Groups[i])
	}
	fix(&tot)
	s.Total = tot
	if s.ByDate {
		sort.SliceStable(s.Groups, func(i, j int) bool {
			a, b := s.Groups[i].Key, s.Groups[j].Key
			if (a == Blank) != (b == Blank) {
				return b == Blank
			}
			return a < b
		})
	} else {
		sort.SliceStable(s.Groups, func(i, j int) bool {
			a, b := s.Groups[i], s.Groups[j]
			if (a.Key == Blank) != (b.Key == Blank) {
				return b.Key == Blank // the blank group goes last
			}
			if va, vb := a.Of(s.Agg), b.Of(s.Agg); va != vb {
				return va > vb
			}
			return a.Key < b.Key
		})
	}
	return s, nil
}

// Of is the group's value for an aggregate.
func (g Group) Of(agg string) float64 {
	switch agg {
	case "count":
		return float64(g.Count)
	case "mean":
		return g.Mean
	}
	return g.Sum
}

// Label is a short title for the summary ("Total by Store").
func (s *Summary) Label() string {
	switch {
	case s.Value == "":
		return "Rows by " + s.By
	case s.Agg == "count":
		return "Count of " + s.Value + " by " + s.By
	case s.Agg == "mean":
		return "Average " + s.Value + " by " + s.By
	}
	return s.Value + " by " + s.By
}

// Fmt writes a number of this summary's aggregate (counts as whole numbers).
func (s *Summary) Fmt(v float64, agg string) string {
	if agg == "count" {
		return fmt.Sprintf("%d", int(math.Round(v)))
	}
	sc := s.Scale
	if agg == "mean" && sc < 2 {
		sc = 2
	}
	return thousands(formatNum(round(v, sc), sc))
}

// round rounds half away from zero at sc decimals, nudging past binary representation error
// (25.065 is stored as 25.06499…, which should still round to 25.07).
func round(v float64, sc int) float64 {
	p := math.Pow10(sc)
	x := v * p
	return math.Round(x+math.Copysign(1e-9*math.Max(1, math.Abs(x)/1e6), x)) / p
}

// thousands adds comma separators to a plain decimal string.
func thousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	intPart, frac := s, ""
	if i := strings.IndexByte(s, '.'); i >= 0 {
		intPart, frac = s[:i], s[i:]
	}
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	out := b.String() + frac
	if neg {
		out = "-" + out
	}
	return out
}
