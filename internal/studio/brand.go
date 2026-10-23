package studio

// Brand kit (v0.8.0): a project's colours, fonts, corner radius and logo, pulled from a
// website (its HTML and stylesheets) or a logo image, and written into every design
// prompt so new work follows it without being told.

import (
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Brand is a project's visual identity.
type Brand struct {
	Name    string   `json:"name,omitempty"`
	Source  string   `json:"source,omitempty"` // the URL or file it came from
	Paper   string   `json:"paper,omitempty"`  // background
	Ink     string   `json:"ink,omitempty"`    // text
	Accents []string `json:"accents,omitempty"`
	Fonts   []string `json:"fonts,omitempty"` // headings first, then body
	Radius  string   `json:"radius,omitempty"`
	Logo    string   `json:"logo,omitempty"` // URL or path
	Notes   string   `json:"notes,omitempty"`
}

// Empty reports whether nothing is set.
func (b Brand) Empty() bool {
	return b.Paper == "" && b.Ink == "" && len(b.Accents) == 0 && len(b.Fonts) == 0 && b.Radius == "" && b.Logo == "" && b.Notes == ""
}

// Prompt is the block the design prompt carries.
func (b Brand) Prompt() string {
	if b.Empty() {
		return ""
	}
	var w strings.Builder
	w.WriteString("BRAND KIT (follow it in every design unless the user says otherwise")
	if b.Source != "" {
		w.WriteString("; from " + b.Source)
	}
	w.WriteString(")\n")
	if b.Name != "" {
		w.WriteString("- Name: " + b.Name + "\n")
	}
	if b.Paper != "" || b.Ink != "" {
		w.WriteString("- Background " + orDash(b.Paper) + ", text " + orDash(b.Ink) + "\n")
	}
	if len(b.Accents) > 0 {
		w.WriteString("- Accent colours: " + strings.Join(b.Accents, ", ") + " (first is primary)\n")
	}
	if len(b.Fonts) > 0 {
		w.WriteString("- Fonts: " + strings.Join(b.Fonts, ", ") + "\n")
	}
	if b.Radius != "" {
		w.WriteString("- Corner radius: " + b.Radius + "\n")
	}
	if b.Logo != "" {
		w.WriteString("- Logo: " + b.Logo + "\n")
	}
	if b.Notes != "" {
		w.WriteString("- Notes: " + b.Notes + "\n")
	}
	return w.String()
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

var linkCSSRe = regexp.MustCompile(`(?i)<link[^>]+rel=["']?stylesheet[^>]*>`)
var hrefRe = regexp.MustCompile(`(?i)href=["']([^"']+)["']`)
var styleBlockRe = regexp.MustCompile(`(?is)<style[^>]*>(.*?)</style>`)
var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var ogSiteRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:site_name["'][^>]+content=["']([^"']+)`)
var iconRe = regexp.MustCompile(`(?i)<link[^>]+rel=["'][^"']*icon[^"']*["'][^>]*>`)
var ogImgRe = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)`)
var logoImgRe = regexp.MustCompile(`(?i)<img[^>]+(?:class|alt|id|src)=["'][^"']*logo[^"']*["'][^>]*>`)
var srcRe = regexp.MustCompile(`(?i)src=["']([^"']+)["']`)

// Stylesheets lists the stylesheet URLs a page links, resolved against base.
func Stylesheets(page, base string) []string {
	var out []string
	b, _ := url.Parse(base)
	for _, l := range linkCSSRe.FindAllString(page, -1) {
		if m := hrefRe.FindStringSubmatch(l); m != nil {
			if u, err := url.Parse(m[1]); err == nil && b != nil {
				out = append(out, b.ResolveReference(u).String())
			}
		}
	}
	return out
}

// BrandFromPage reads a brand from a page's HTML and its CSS (inline and fetched).
func BrandFromPage(page string, css []string, base string) Brand {
	all := page + "\n" + strings.Join(css, "\n")
	for _, m := range styleBlockRe.FindAllStringSubmatch(page, -1) {
		all += "\n" + m[1]
	}
	br := Brand{Source: base}
	if m := ogSiteRe.FindStringSubmatch(page); m != nil {
		br.Name = strings.TrimSpace(m[1])
	} else if m := titleRe.FindStringSubmatch(page); m != nil {
		t := plain(m[1])
		for _, sep := range []string{" | ", " - ", " — ", " · "} {
			if i := strings.LastIndex(t, sep); i > 0 {
				t = strings.TrimSpace(t[i+len(sep):])
				break
			}
		}
		if len(t) <= 40 {
			br.Name = t
		}
	}
	br.setPalette(Palette(all))
	f := Fonts(all)
	if len(f) > 3 {
		f = f[:3]
	}
	br.Fonts = f
	if r := Radii(all); len(r) > 0 {
		br.Radius = r[0]
	}
	bu, _ := url.Parse(base)
	resolve := func(s string) string {
		u, err := url.Parse(s)
		if err != nil || bu == nil {
			return s
		}
		return bu.ResolveReference(u).String()
	}
	if m := logoImgRe.FindString(page); m != "" {
		if s := srcRe.FindStringSubmatch(m); s != nil {
			br.Logo = resolve(s[1])
		}
	}
	if br.Logo == "" {
		if m := iconRe.FindString(page); m != "" {
			if s := hrefRe.FindStringSubmatch(m); s != nil {
				br.Logo = resolve(s[1])
			}
		} else if m := ogImgRe.FindStringSubmatch(page); m != nil {
			br.Logo = resolve(m[1])
		}
	}
	return br
}

// setPalette sorts colours into background, text and accents.
func (br *Brand) setPalette(cols []string) {
	var lights, darks, accents []RGB
	for _, h := range cols {
		c, _ := ParseColor(h)
		l := c.Luminance()
		switch {
		case c.saturation() > 0.28 && l > 0.03 && l < 0.85:
			accents = append(accents, c)
		case l >= 0.6:
			lights = append(lights, c)
		case l <= 0.08:
			darks = append(darks, c)
		}
	}
	if len(lights) > 0 {
		br.Paper = lights[0].Hex()
	}
	if len(darks) > 0 {
		br.Ink = darks[0].Hex()
	}
	br.Accents = distinct(accents, 3)
}

func distinct(cs []RGB, n int) []string {
	var keep []RGB
	for _, c := range cs {
		dup := false
		for _, k := range keep {
			if dist(c, k) < 48 {
				dup = true
				break
			}
		}
		if !dup {
			keep = append(keep, c)
		}
		if len(keep) == n {
			break
		}
	}
	out := make([]string, len(keep))
	for i, k := range keep {
		out[i] = k.Hex()
	}
	return out
}

// BrandFromImage reads a palette from a logo: its most common opaque colours, with the
// saturated ones as accents.
func BrandFromImage(r io.Reader, source string) (Brand, error) {
	img, _, err := image.Decode(r)
	if err != nil {
		return Brand{}, err
	}
	bd := img.Bounds()
	step := 1
	if px := bd.Dx() * bd.Dy(); px > 250000 {
		step = 2
		for (bd.Dx()/step)*(bd.Dy()/step) > 250000 {
			step++
		}
	}
	type bucket struct {
		n       int
		r, g, b int
	}
	buckets := map[uint16]*bucket{}
	for y := bd.Min.Y; y < bd.Max.Y; y += step {
		for x := bd.Min.X; x < bd.Max.X; x += step {
			r, g, b, a := img.At(x, y).RGBA()
			if a < 0xC000 {
				continue
			}
			r8, g8, b8 := int(r>>8), int(g>>8), int(b>>8)
			k := uint16(r8>>4)<<8 | uint16(g8>>4)<<4 | uint16(b8>>4)
			bk := buckets[k]
			if bk == nil {
				bk = &bucket{}
				buckets[k] = bk
			}
			bk.n++
			bk.r += r8
			bk.g += g8
			bk.b += b8
		}
	}
	type col struct {
		c RGB
		n int
	}
	var cols []col
	for _, b := range buckets {
		cols = append(cols, col{RGB{uint8(b.r / b.n), uint8(b.g / b.n), uint8(b.b / b.n)}, b.n})
	}
	sort.Slice(cols, func(i, j int) bool { return cols[i].n > cols[j].n })
	var hexes []string
	for _, c := range cols {
		hexes = append(hexes, c.c.Hex())
	}
	br := Brand{Source: source, Logo: source}
	br.setPalette(hexes)
	return br, nil
}
