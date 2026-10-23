package studio

// Taste memory (v0.8.0): Blaze learns the user's fonts, colours, radii and spacing from
// what they keep and what they undo. A value that a version introduced and the next
// version still has counts as kept (+1); one the next version dropped, or that the user
// undid or restored away from, counts as disliked (-1). Direct edits in the preview
// count double: the user chose that value by hand.

import (
	"regexp"
	"sort"
	"strings"
	"time"
)

// Pref is one learned value.
type Pref struct {
	Kind  string    `json:"kind"` // font | color | radius | spacing | size
	Value string    `json:"value"`
	Score float64   `json:"score"`
	Seen  time.Time `json:"seen"`
}

// Taste is everything learned for one user (shared by all projects).
type Taste struct {
	Prefs []Pref `json:"prefs"`
}

// KindFor maps a CSS property to a taste kind ("" = not tracked).
func KindFor(prop string) string {
	p := strings.ToLower(strings.TrimSpace(prop))
	switch {
	case p == "font-family":
		return "font"
	case p == "color" || strings.HasPrefix(p, "background") || strings.HasSuffix(p, "color") || p == "fill" || p == "stroke":
		return "color"
	case p == "border-radius":
		return "radius"
	case strings.HasPrefix(p, "padding") || strings.HasPrefix(p, "margin") || p == "gap" || p == "row-gap" || p == "column-gap":
		return "spacing"
	case p == "font-size":
		return "size"
	}
	return ""
}

// Observe adds delta to a value's score.
func (t *Taste) Observe(kind, value string, delta float64) {
	value = normValue(kind, value)
	if kind == "" || value == "" {
		return
	}
	for i := range t.Prefs {
		if t.Prefs[i].Kind == kind && t.Prefs[i].Value == value {
			t.Prefs[i].Score += delta
			t.Prefs[i].Seen = time.Now()
			return
		}
	}
	t.Prefs = append(t.Prefs, Pref{Kind: kind, Value: value, Score: delta, Seen: time.Now()})
	if len(t.Prefs) > 400 { // forget the weakest, oldest
		sort.Slice(t.Prefs, func(i, j int) bool {
			ai, aj := abs(t.Prefs[i].Score), abs(t.Prefs[j].Score)
			if ai != aj {
				return ai > aj
			}
			return t.Prefs[i].Seen.After(t.Prefs[j].Seen)
		})
		t.Prefs = t.Prefs[:300]
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func normValue(kind, v string) string {
	v = strings.TrimSpace(v)
	switch kind {
	case "color":
		if c, ok := ParseColor(v); ok {
			return c.Hex()
		}
		return ""
	case "font":
		for _, f := range strings.Split(v, ",") {
			f = strings.Trim(strings.TrimSpace(f), `"'`)
			if f != "" && !genericFonts[strings.ToLower(f)] {
				return f
			}
		}
		return ""
	}
	if len(v) > 24 {
		return ""
	}
	return strings.ToLower(v)
}

// ObserveEdit learns from a hand edit's style props (kept by choice: +2).
func (t *Taste) ObserveEdit(props map[string]string, delta float64) {
	for k, v := range props {
		t.Observe(KindFor(k), v, delta)
	}
}

var spaceRe = regexp.MustCompile(`(?i)(?:padding|margin|gap)\s*:\s*([0-9.]+(?:px|rem|em))`)
var sizeRe = regexp.MustCompile(`(?i)font-size\s*:\s*([0-9.]+(?:px|rem|em))`)

// Values lists the taste values a source uses, by kind.
func Values(src string) map[string][]string {
	out := map[string][]string{"color": Palette(src), "font": Fonts(src), "radius": Radii(src)}
	for _, pair := range []struct {
		k  string
		re *regexp.Regexp
	}{{"spacing", spaceRe}, {"size", sizeRe}} {
		c := map[string]int{}
		for _, m := range pair.re.FindAllStringSubmatch(src, -1) {
			c[strings.ToLower(m[1])]++
		}
		out[pair.k] = rank(c)
	}
	return out
}

// Compare learns from three consecutive versions' sources: values that `mid` introduced
// (absent in `before`) are kept (+1) when `after` still has them and disliked (-1)
// when it dropped them.
func (t *Taste) Compare(before, mid, after string) {
	b, m, a := Values(before), Values(mid), Values(after)
	for kind, vals := range m {
		for _, v := range vals {
			if contains(b[kind], v) {
				continue
			}
			if contains(a[kind], v) {
				t.Observe(kind, v, 1)
			} else {
				t.Observe(kind, v, -1)
			}
		}
	}
}

// Undo learns from the user restoring `kept` instead of `dropped`: values only the
// dropped one had are disliked.
func (t *Taste) Undo(dropped, kept string) {
	d, k := Values(dropped), Values(kept)
	for kind, vals := range d {
		for _, v := range vals {
			if !contains(k[kind], v) {
				t.Observe(kind, v, -1)
			}
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Top returns the liked (score ≥ 1) and disliked (score ≤ -1) values of a kind, strongest first.
func (t *Taste) Top(kind string, n int) (liked, disliked []string) {
	ps := append([]Pref(nil), t.Prefs...)
	sort.Slice(ps, func(i, j int) bool { return abs(ps[i].Score) > abs(ps[j].Score) })
	for _, p := range ps {
		if p.Kind != kind {
			continue
		}
		if p.Score >= 1 && len(liked) < n {
			liked = append(liked, p.Value)
		}
		if p.Score <= -1 && len(disliked) < n {
			disliked = append(disliked, p.Value)
		}
	}
	return
}

// Prompt is the block the design prompt carries.
func (t *Taste) Prompt() string {
	var likes, avoid []string
	for _, k := range []struct{ kind, label string }{{"font", "fonts"}, {"color", "colours"}, {"radius", "corner radius"}, {"spacing", "spacing"}, {"size", "text sizes"}} {
		l, d := t.Top(k.kind, 4)
		if len(l) > 0 {
			likes = append(likes, k.label+" "+strings.Join(l, ", "))
		}
		if len(d) > 0 {
			avoid = append(avoid, k.label+" "+strings.Join(d, ", "))
		}
	}
	if len(likes) == 0 && len(avoid) == 0 {
		return ""
	}
	var w strings.Builder
	w.WriteString("TASTE (learned from what the user keeps and undoes; lean on it when they haven't said otherwise)\n")
	if len(likes) > 0 {
		w.WriteString("- Likes: " + strings.Join(likes, "; ") + "\n")
	}
	if len(avoid) > 0 {
		w.WriteString("- Avoid: " + strings.Join(avoid, "; ") + "\n")
	}
	return w.String()
}
