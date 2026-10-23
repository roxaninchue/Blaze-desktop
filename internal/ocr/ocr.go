// Package ocr reads images on this computer, with nothing sent anywhere: the OS's own
// text recognition (Windows.Media.Ocr, macOS Vision, Tesseract on Linux when installed)
// and a pure-Go readout of size and main colours. Private vision uses it so cloud
// models get text about a picture instead of the picture.
package ocr

import (
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"sort"
	"strings"
	"time"
)

// ErrNoEngine means this computer has no text recognition Blaze can use.
var ErrNoEngine = errors.New("no on-device text recognition available")

// Engine names the OCR this OS offers ("" when none) and a note for Settings.
func Engine() (name, note string) { return engine() }

// Text recognises the text in an image file, line by line, on this computer.
func Text(ctx context.Context, path string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	s, err := recognise(ctx, path)
	return strings.TrimSpace(s), err
}

// Summary is a one-line readout of an image's size, brightness and main colours,
// made in Go from the pixels ("" for formats Go can't decode, like WebP).
func Summary(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return ""
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return ""
	}
	// Sample about 40k pixels into a 4-bit-per-channel histogram.
	step := 1
	for (w/step)*(h/step) > 40000 {
		step++
	}
	counts := map[uint32]int{}
	var lum float64
	n := 0
	for y := b.Min.Y; y < b.Max.Y; y += step {
		for x := b.Min.X; x < b.Max.X; x += step {
			c := color.NRGBAModel.Convert(img.At(x, y)).(color.NRGBA)
			if c.A < 32 {
				continue
			}
			counts[uint32(c.R>>4)<<8|uint32(c.G>>4)<<4|uint32(c.B>>4)]++
			lum += 0.2126*float64(c.R) + 0.7152*float64(c.G) + 0.0722*float64(c.B)
			n++
		}
	}
	if n == 0 {
		return fmt.Sprintf("%d×%d px, transparent", w, h)
	}
	type kv struct {
		k uint32
		v int
	}
	var all []kv
	for k, v := range counts {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
	var cols []string
	for _, e := range all {
		if len(cols) == 5 {
			break
		}
		pct := e.v * 100 / n
		if pct < 2 && len(cols) > 0 {
			break
		}
		r, g, bl := (e.k>>8&15)*17, (e.k>>4&15)*17, (e.k&15)*17
		cols = append(cols, fmt.Sprintf("#%02x%02x%02x %d%%", r, g, bl, pct))
	}
	tone := "mid-tone"
	switch avg := lum / float64(n); {
	case avg < 85:
		tone = "mostly dark"
	case avg > 170:
		tone = "mostly light"
	}
	return fmt.Sprintf("%d×%d px, %s; main colours %s", w, h, tone, strings.Join(cols, ", "))
}

// Describe is what Private vision sends in place of a picture: the readout plus the
// recognised text. It never fails outright; what it couldn't do is said in the text.
func Describe(ctx context.Context, path string) string {
	var b strings.Builder
	if s := Summary(path); s != "" {
		b.WriteString("Image: " + s + ".\n")
	}
	name, _ := Engine()
	txt, err := Text(ctx, path)
	switch {
	case errors.Is(err, ErrNoEngine):
		b.WriteString("Text: not read (this computer has no on-device text recognition; on Linux install tesseract).")
	case err != nil:
		b.WriteString("Text: couldn't be read (" + err.Error() + ").")
	case txt == "":
		b.WriteString("Text (" + name + " OCR): none found. It's likely a photo, illustration or graphic; the picture itself can't be described without sending it.")
	default:
		if len(txt) > 6000 {
			txt = txt[:6000] + "\n…"
		}
		b.WriteString("Text (" + name + " OCR, line by line):\n" + txt)
	}
	return b.String()
}
