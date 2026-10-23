package studio

// Colours: parse the notations a page uses, swap one colour for another across a file
// (the "swap colours" edit and brand palettes), and read a file's palette and fonts.

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// RGB is a colour without alpha.
type RGB struct{ R, G, B uint8 }

func (c RGB) Hex() string { return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B) }

var hexRe = regexp.MustCompile(`#([0-9a-fA-F]{8}|[0-9a-fA-F]{6}|[0-9a-fA-F]{3,4})\b`)
var rgbRe = regexp.MustCompile(`rgba?\(\s*(\d{1,3})\s*[, ]\s*(\d{1,3})\s*[, ]\s*(\d{1,3})\s*(?:[,/]\s*([\d.]+%?)\s*)?\)`)

// ParseColor reads #rgb, #rrggbb, #rrggbbaa, rgb() and rgba().
func ParseColor(s string) (RGB, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	if m := hexRe.FindStringSubmatch(s); m != nil && m[0] == s {
		return hexToRGB(m[1]), true
	}
	if m := rgbRe.FindStringSubmatch(s); m != nil && m[0] == s {
		return RGB{u8(m[1]), u8(m[2]), u8(m[3])}, true
	}
	if c, ok := named[s]; ok {
		return c, true
	}
	return RGB{}, false
}

var named = map[string]RGB{"white": {255, 255, 255}, "black": {0, 0, 0}, "red": {255, 0, 0}, "blue": {0, 0, 255}, "green": {0, 128, 0}, "gray": {128, 128, 128}, "grey": {128, 128, 128}, "orange": {255, 165, 0}, "yellow": {255, 255, 0}, "purple": {128, 0, 128}}

func u8(s string) uint8 {
	n, _ := strconv.Atoi(s)
	if n > 255 {
		n = 255
	}
	return uint8(n)
}

func hexToRGB(h string) RGB {
	if len(h) == 3 || len(h) == 4 {
		h = string([]byte{h[0], h[0], h[1], h[1], h[2], h[2]})
	}
	v, _ := strconv.ParseUint(h[:6], 16, 32)
	return RGB{uint8(v >> 16), uint8(v >> 8), uint8(v)}
}

// SwapColor replaces every notation of old in src with new (as hex, keeping any alpha
// an rgba()/#rrggbbaa had). It returns the new text and how many it replaced.
func SwapColor(src, old, nw string) (string, int) {
	oc, ok := ParseColor(old)
	if !ok {
		return src, 0
	}
	nc, ok := ParseColor(nw)
	if !ok {
		return src, 0
	}
	n := 0
	src = hexRe.ReplaceAllStringFunc(src, func(m string) string {
		h := m[1:]
		if hexToRGB(h) != oc {
			return m
		}
		n++
		out := nc.Hex()
		if len(h) == 8 {
			out += strings.ToLower(h[6:])
		} else if len(h) == 4 {
			out += strings.ToLower(strings.Repeat(h[3:], 2))
		}
		return out
	})
	src = rgbRe.ReplaceAllStringFunc(src, func(m string) string {
		p := rgbRe.FindStringSubmatch(m)
		if (RGB{u8(p[1]), u8(p[2]), u8(p[3])}) != oc {
			return m
		}
		n++
		if p[4] != "" {
			return fmt.Sprintf("rgba(%d, %d, %d, %s)", nc.R, nc.G, nc.B, p[4])
		}
		return nc.Hex()
	})
	return src, n
}

// Palette lists a text's colours, most used first.
func Palette(src string) []string {
	counts := map[string]int{}
	for _, m := range hexRe.FindAllStringSubmatch(src, -1) {
		counts[hexToRGB(m[1]).Hex()]++
	}
	for _, m := range rgbRe.FindAllStringSubmatch(src, -1) {
		counts[RGB{u8(m[1]), u8(m[2]), u8(m[3])}.Hex()]++
	}
	return rank(counts)
}

var fontRe = regexp.MustCompile(`(?i)font-family\s*:\s*([^;}\n<>]+)`)
var jsxFontRe = regexp.MustCompile(`fontFamily\s*:\s*["'\x60]([^"'\x60]+)`)
var gfontRe = regexp.MustCompile(`fonts\.googleapis\.com/css2?\?([^"')\s]+)`)
var genericFonts = map[string]bool{"serif": true, "sans-serif": true, "monospace": true, "system-ui": true, "cursive": true, "fantasy": true, "inherit": true, "initial": true, "ui-sans-serif": true, "ui-serif": true, "ui-monospace": true, "-apple-system": true, "blinkmacsystemfont": true, "segoe ui": true, "roboto": false, "var(": true}

// Fonts lists the first named family of each font stack, most used first.
func Fonts(src string) []string {
	counts := map[string]int{}
	add := func(stack string) {
		for _, f := range strings.Split(stack, ",") {
			f = strings.Trim(strings.TrimSpace(f), `"'`)
			lf := strings.ToLower(f)
			if f == "" || genericFonts[lf] || strings.HasPrefix(lf, "var(") {
				continue
			}
			counts[f]++
			return
		}
	}
	for _, m := range fontRe.FindAllStringSubmatch(src, -1) {
		add(m[1])
	}
	for _, m := range jsxFontRe.FindAllStringSubmatch(src, -1) {
		add(m[1])
	}
	for _, m := range gfontRe.FindAllStringSubmatch(src, -1) {
		for _, q := range strings.Split(m[1], "&") {
			if strings.HasPrefix(q, "family=") {
				f := strings.SplitN(strings.TrimPrefix(q, "family="), ":", 2)[0]
				counts[strings.ReplaceAll(strings.ReplaceAll(f, "+", " "), "%20", " ")] += 2
			}
		}
	}
	return rank(counts)
}

var radiusRe = regexp.MustCompile(`(?i)border-radius\s*:\s*([0-9.]+(?:px|rem|em|%))`)

// Radii lists border-radius values, most used first.
func Radii(src string) []string {
	counts := map[string]int{}
	for _, m := range radiusRe.FindAllStringSubmatch(src, -1) {
		counts[m[1]]++
	}
	return rank(counts)
}

func rank(counts map[string]int) []string {
	out := make([]string, 0, len(counts))
	for k := range counts {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool {
		if counts[out[i]] != counts[out[j]] {
			return counts[out[i]] > counts[out[j]]
		}
		return out[i] < out[j]
	})
	return out
}

// Luminance is the WCAG relative luminance.
func (c RGB) Luminance() float64 {
	ch := func(v uint8) float64 {
		x := float64(v) / 255
		if x <= 0.03928 {
			return x / 12.92
		}
		return math.Pow((x+0.055)/1.055, 2.4)
	}
	return 0.2126*ch(c.R) + 0.7152*ch(c.G) + 0.0722*ch(c.B)
}

// Contrast is the WCAG contrast ratio of two colours.
func Contrast(a, b RGB) float64 {
	la, lb := a.Luminance(), b.Luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// saturation is HSL saturation in 0..1.
func (c RGB) saturation() float64 {
	r, g, b := float64(c.R)/255, float64(c.G)/255, float64(c.B)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	l := (mx + mn) / 2
	if mx == mn {
		return 0
	}
	d := mx - mn
	if l > 0.5 {
		return d / (2 - mx - mn)
	}
	return d / (mx + mn)
}

func dist(a, b RGB) float64 {
	dr, dg, db := float64(a.R)-float64(b.R), float64(a.G)-float64(b.G), float64(a.B)-float64(b.B)
	return math.Sqrt(dr*dr + dg*dg + db*db)
}
