package studio

// The critic (v0.8.0): every new version is rendered at phone, tablet and desktop
// widths and checked inside the page (contrast, alt text, labels, tap targets,
// overflow, DOM size, image weight, load time). This scores the reports and writes the
// fix request Blaze sends itself when auto-fix is on.

import (
	"fmt"
	"sort"
	"strings"
)

// Finding is one element with a problem.
type Finding struct {
	Sel   string  `json:"sel"`
	Text  string  `json:"text,omitempty"`
	FG    string  `json:"fg,omitempty"`
	BG    string  `json:"bg,omitempty"`
	Ratio float64 `json:"ratio,omitempty"`
	Need  float64 `json:"need,omitempty"`
	W     int     `json:"w,omitempty"`
	H     int     `json:"h,omitempty"`
}

// Audit is what the page reported at one width.
type Audit struct {
	Width      int       `json:"width"`
	Contrast   []Finding `json:"contrast"`
	NoAlt      []Finding `json:"no_alt"`
	Unlabeled  []Finding `json:"unlabeled"`
	SmallTaps  []Finding `json:"small_taps"`
	OverflowPx int       `json:"overflow_px"`
	Nodes      int       `json:"nodes"`
	ImgBytes   int64     `json:"img_bytes"`
	LoadMs     int       `json:"load_ms"`
}

// Card is the score card shown on a version.
type Card struct {
	Version int      `json:"version"`
	Score   int      `json:"score"`
	A11y    int      `json:"a11y"`
	Layout  int      `json:"layout"`
	Speed   int      `json:"speed"`
	Issues  []string `json:"issues"`
	Fixed   bool     `json:"fixed,omitempty"`  // a fix was requested for this version
	FixOf   int      `json:"fix_of,omitempty"` // this version is the critic's fix of that one
}

func capAt(v, max int) int {
	if v > max {
		return max
	}
	return v
}

func width(w int) string {
	switch {
	case w <= 480:
		return "phone"
	case w <= 1024:
		return "tablet"
	}
	return "desktop"
}

// Score turns the audits into a card. Each part is out of 100; the score weights
// accessibility 50%, layout 30%, speed 20%.
func Score(audits []Audit) Card {
	a11y, layout, speed := 100, 100, 100
	var issues []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			issues = append(issues, s)
		}
	}
	sort.Slice(audits, func(i, j int) bool { return audits[i].Width < audits[j].Width })
	var contrast, noAlt, unl map[string]Finding = map[string]Finding{}, map[string]Finding{}, map[string]Finding{}
	for _, a := range audits {
		for _, f := range a.Contrast {
			contrast[f.Sel] = f
		}
		for _, f := range a.NoAlt {
			noAlt[f.Sel] = f
		}
		for _, f := range a.Unlabeled {
			unl[f.Sel] = f
		}
		if width(a.Width) == "phone" && len(a.SmallTaps) > 0 {
			a11y -= capAt(3*len(a.SmallTaps), 15)
			add(fmt.Sprintf("%d tap target%s smaller than 44px on phone (%s)", len(a.SmallTaps), plural(len(a.SmallTaps)), sels(a.SmallTaps, 3)))
		}
		if a.OverflowPx > 2 {
			layout -= 25
			add(fmt.Sprintf("Scrolls sideways on %s (%dpx too wide)", width(a.Width), a.OverflowPx))
		}
		if width(a.Width) == "desktop" {
			if a.Nodes > 3000 {
				speed -= 15
				add(fmt.Sprintf("%d elements on the page (heavy)", a.Nodes))
			} else if a.Nodes > 1500 {
				speed -= 6
			}
			if a.ImgBytes > 3<<20 {
				speed -= 20
				add(fmt.Sprintf("%.1f MB of images: compress or resize them", float64(a.ImgBytes)/(1<<20)))
			} else if a.ImgBytes > 1<<20 {
				speed -= 8
				add(fmt.Sprintf("%.1f MB of images", float64(a.ImgBytes)/(1<<20)))
			}
			if a.LoadMs > 3000 {
				speed -= 12
				add(fmt.Sprintf("Took %.1fs to load", float64(a.LoadMs)/1000))
			}
		}
	}
	if n := len(contrast); n > 0 {
		a11y -= capAt(8*n, 40)
		var fs []Finding
		for _, f := range contrast {
			fs = append(fs, f)
		}
		sort.Slice(fs, func(i, j int) bool { return fs[i].Ratio < fs[j].Ratio })
		add(fmt.Sprintf("%d text element%s with low contrast (worst %.1f:1 on %s, needs %.1f:1)", n, plural(n), fs[0].Ratio, fs[0].Sel, fs[0].Need))
	}
	if n := len(noAlt); n > 0 {
		a11y -= capAt(6*n, 24)
		add(fmt.Sprintf("%d image%s without alt text", n, plural(n)))
	}
	if n := len(unl); n > 0 {
		a11y -= capAt(6*n, 24)
		add(fmt.Sprintf("%d control%s without a label (%s)", n, plural(n), selsMap(unl, 3)))
	}
	clamp := func(v int) int {
		if v < 0 {
			return 0
		}
		return v
	}
	a11y, layout, speed = clamp(a11y), clamp(layout), clamp(speed)
	return Card{Score: (a11y*50 + layout*30 + speed*20) / 100, A11y: a11y, Layout: layout, Speed: speed, Issues: issues}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func sels(fs []Finding, n int) string {
	var out []string
	for i, f := range fs {
		if i == n {
			out = append(out, "…")
			break
		}
		out = append(out, f.Sel)
	}
	return strings.Join(out, ", ")
}

func selsMap(m map[string]Finding, n int) string {
	var fs []Finding
	for _, f := range m {
		fs = append(fs, f)
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].Sel < fs[j].Sel })
	return sels(fs, n)
}

// NeedsFix reports whether the critic should fix this card on its own.
func (c Card) NeedsFix(threshold int) bool {
	return c.Score < threshold && len(c.Issues) > 0 && c.FixOf == 0 && !c.Fixed
}

// FixPrompt is the message Blaze sends itself to fix a version.
func FixPrompt(c Card, audits []Audit, file string) string {
	var w strings.Builder
	fmt.Fprintf(&w, "[Critic fix] Version %d of %s scored %d/100 (accessibility %d, layout %d, speed %d). Fix these in the source, keep the design otherwise identical, then say in one line what you fixed:\n", c.Version, file, c.Score, c.A11y, c.Layout, c.Speed)
	for _, i := range c.Issues {
		w.WriteString("- " + i + "\n")
	}
	seen := map[string]bool{}
	for _, a := range audits {
		for _, f := range a.Contrast {
			if !seen["c"+f.Sel] && len(seen) < 12 {
				seen["c"+f.Sel] = true
				fmt.Fprintf(&w, "  contrast: %s text %s on %s = %.1f:1 (needs %.1f:1)\n", f.Sel, f.FG, f.BG, f.Ratio, f.Need)
			}
		}
		for _, f := range a.NoAlt {
			if !seen["a"+f.Sel] && len(seen) < 12 {
				seen["a"+f.Sel] = true
				fmt.Fprintf(&w, "  no alt: %s\n", f.Sel)
			}
		}
	}
	return w.String()
}
