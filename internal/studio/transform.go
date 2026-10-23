package studio

// Move, resize and colour edits from the Design studio (v0.20 phase 1). The preview's
// handles send raw numbers as they were dragged ("12.6px 40.2px", "319.99px", "1.0400");
// before a value is written into someone's source it is cleaned up here: whole pixels,
// sane bounds, one notation per property, and nothing that isn't a move, a size, a scale,
// a turn or a colour.

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Transform bounds. A drag can't throw an element further than this, or shrink it away.
const (
	maxOffset = 20000.0
	minSize   = 1.0
	maxSize   = 20000.0
	minScale  = 0.05
	maxScale  = 20.0
)

// TransformProps are the CSS properties the move and resize handles may write.
var TransformProps = []string{"translate", "width", "height", "scale", "rotate", "max-width"}

// IsTransform reports whether every property in props is one the handles write.
func IsTransform(props map[string]string) bool {
	if len(props) == 0 {
		return false
	}
	for k := range props {
		if !containsStr(TransformProps, k) {
			return false
		}
	}
	return true
}

// NormalizeTransform cleans the values a move or resize sent. An empty value removes the
// property (a reset). It returns an error naming the first value it can't read.
func NormalizeTransform(props map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(props))
	for _, k := range sortedKeys(props) {
		v := strings.TrimSpace(props[k])
		if v == "" || v == "none" || v == "auto" {
			if k == "translate" || k == "scale" || k == "rotate" {
				out[k] = "" // back to no transform: drop it from the source
			} else {
				out[k] = v
			}
			continue
		}
		switch k {
		case "translate":
			x, y, err := readPair(v)
			if err != nil {
				return nil, fmt.Errorf("translate %q: %w", v, err)
			}
			x, y = math.Round(clamp(x, -maxOffset, maxOffset)), math.Round(clamp(y, -maxOffset, maxOffset))
			if x == 0 && y == 0 {
				out[k] = ""
			} else {
				out[k] = px(x) + " " + px(y)
			}
		case "width", "height", "max-width":
			if strings.HasSuffix(v, "%") {
				n, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
				if err != nil || n < 0 || n > 100 {
					return nil, fmt.Errorf("%s %q: not a percentage", k, v)
				}
				out[k] = trimNum(math.Round(n*10)/10) + "%"
				continue
			}
			n, err := readPx(v)
			if err != nil {
				return nil, fmt.Errorf("%s %q: %w", k, v, err)
			}
			out[k] = px(clamp(n, minSize, maxSize))
		case "scale":
			n, err := strconv.ParseFloat(strings.Fields(v)[0], 64)
			if err != nil {
				return nil, fmt.Errorf("scale %q: not a number", v)
			}
			n = clamp(n, minScale, maxScale)
			n = math.Round(n*100) / 100
			if n == 1 {
				out[k] = ""
			} else {
				out[k] = trimNum(n)
			}
		case "rotate":
			s := strings.TrimSuffix(v, "deg")
			n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, fmt.Errorf("rotate %q: not degrees", v)
			}
			n = math.Mod(n, 360)
			if n > 180 {
				n -= 360
			} else if n < -180 {
				n += 360
			}
			n = math.Round(n*10) / 10
			if n == 0 {
				out[k] = ""
			} else {
				out[k] = trimNum(n) + "deg"
			}
		default:
			return nil, fmt.Errorf("%s isn't a move or a resize", k)
		}
	}
	return out, nil
}

// NormalizeStyle cleans a style edit from the studio: transform values via
// NormalizeTransform and colours via NormHex (lowercase hex; rgba() becomes #rrggbbaa).
// Everything else passes through untouched, since the inspector's own sliders already
// send plain CSS ("18px", "0px").
func NormalizeStyle(props map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(props))
	tf := map[string]string{}
	for k, v := range props {
		switch {
		case containsStr(TransformProps, k):
			tf[k] = v
		case isColorProp(k):
			if h, ok := NormHex(v); ok {
				out[k] = h
			} else {
				out[k] = strings.TrimSpace(v)
			}
		default:
			out[k] = v
		}
	}
	if len(tf) > 0 {
		n, err := NormalizeTransform(tf)
		if err != nil {
			return nil, err
		}
		for k, v := range n {
			out[k] = v
		}
	}
	return out, nil
}

func isColorProp(k string) bool {
	return k == "color" || k == "background-color" || k == "background" || k == "border-color" || k == "fill" || k == "stroke" || k == "outline-color"
}

// NormHex reads a colour the picker or a person typed (#rgb, #rgba, #rrggbb, #rrggbbaa,
// with or without '#', or rgb()/rgba()) and returns it as lowercase hex. Fully opaque
// colours are #rrggbb; anything with alpha is #rrggbbaa.
func NormHex(s string) (string, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "", false
	}
	if m := rgbRe.FindStringSubmatch(s); m != nil && m[0] == s {
		c := RGB{u8(m[1]), u8(m[2]), u8(m[3])}
		a := 1.0
		if m[4] != "" {
			if strings.HasSuffix(m[4], "%") {
				f, _ := strconv.ParseFloat(strings.TrimSuffix(m[4], "%"), 64)
				a = f / 100
			} else {
				a, _ = strconv.ParseFloat(m[4], 64)
			}
		}
		return withAlpha(c, a), true
	}
	h := strings.TrimPrefix(s, "#")
	for _, r := range h {
		if !((r >= '0' && r <= '9') || (r >= 'a' && r <= 'f')) {
			return "", false
		}
	}
	switch len(h) {
	case 3, 4:
		var b strings.Builder
		for _, r := range h {
			b.WriteRune(r)
			b.WriteRune(r)
		}
		h = b.String()
	case 6, 8:
	default:
		return "", false
	}
	if len(h) == 8 && h[6:] == "ff" {
		h = h[:6]
	}
	return "#" + h, true
}

func withAlpha(c RGB, a float64) string {
	a = clamp(a, 0, 1)
	if a >= 0.999 {
		return c.Hex()
	}
	return fmt.Sprintf("%s%02x", c.Hex(), int(math.Round(a*255)))
}

func readPair(v string) (float64, float64, error) {
	f := strings.Fields(strings.ReplaceAll(v, ",", " "))
	if len(f) == 0 || len(f) > 3 {
		return 0, 0, fmt.Errorf("want two lengths")
	}
	x, err := readPx(f[0])
	if err != nil {
		return 0, 0, err
	}
	y := 0.0
	if len(f) > 1 {
		if y, err = readPx(f[1]); err != nil {
			return 0, 0, err
		}
	}
	return x, y, nil
}

func readPx(v string) (float64, error) {
	v = strings.TrimSpace(v)
	v = strings.TrimSuffix(v, "px")
	n, err := strconv.ParseFloat(v, 64)
	if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return 0, fmt.Errorf("not a pixel length")
	}
	return n, nil
}

func clamp(v, lo, hi float64) float64 { return math.Max(lo, math.Min(hi, v)) }

func px(v float64) string {
	r := math.Round(v)
	if r == 0 {
		r = 0 // no "-0px"
	}
	return strconv.FormatFloat(r, 'f', -1, 64) + "px"
}

func trimNum(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }
