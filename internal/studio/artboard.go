package studio

// Artboards (v0.9.0): "design anything". A design that isn't a web page (a slide, a
// social post, a poster, an app screen, an email, an icon) is still one HTML file,
// with a fixed frame declared in <meta name="blaze-artboard" content="<id> <W>x<H>">.
// The preview draws that frame scaled to fit, the critic and write-back work as for any
// page, and Export renders it to PNG or PDF with the computer's own Chrome or Edge.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Format is a kind of design with a fixed frame (CSS pixels at 96 dpi).
type Format struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	W     int    `json:"w"`
	H     int    `json:"h"`
	Print string `json:"print,omitempty"` // @page size for print formats ("A4"); "" = screen
	Hint  string `json:"hint"`            // what it's for, for the menu and the model
}

// Formats lists the artboards New design offers, most common first.
var Formats = []Format{
	{ID: "post", Name: "Social post", W: 1080, H: 1350, Hint: "Instagram/LinkedIn portrait post"},
	{ID: "story", Name: "Story", W: 1080, H: 1920, Hint: "Instagram/TikTok story, keep text out of the top and bottom 250 px"},
	{ID: "slide", Name: "Slide", W: 1920, H: 1080, Hint: "16:9 presentation slide"},
	{ID: "poster-a4", Name: "Poster A4", W: 794, H: 1123, Print: "A4", Hint: "printable A4 poster or flyer"},
	{ID: "poster-a3", Name: "Poster A3", W: 1123, H: 1587, Print: "A3", Hint: "printable A3 poster"},
	{ID: "phone", Name: "App screen", W: 390, H: 844, Hint: "one phone app screen"},
	{ID: "email", Name: "Email", W: 600, H: 1200, Hint: "HTML email, 600 px wide, table layout and inline styles for mail clients"},
	{ID: "icon", Name: "Icon / logo", W: 1024, H: 1024, Hint: "app icon or logo mark, readable down to 32 px"},
	{ID: "banner", Name: "Banner", W: 1500, H: 500, Hint: "X/LinkedIn header banner"},
}

// FormatByID finds a format ("" when unknown).
func FormatByID(id string) (Format, bool) {
	for _, f := range Formats {
		if f.ID == id {
			return f, true
		}
	}
	return Format{}, false
}

var boardMetaRe = regexp.MustCompile(`(?is)<meta\s+[^>]*name\s*=\s*["']blaze-artboard["'][^>]*>`)
var boardContentRe = regexp.MustCompile(`(?is)content\s*=\s*["']([^"']*)["']`)
var boardSizeRe = regexp.MustCompile(`^(\d{2,5})x(\d{2,5})$`)

// ParseArtboard reads a page's artboard meta. ok is false for ordinary web pages.
func ParseArtboard(page []byte) (Format, bool) {
	m := boardMetaRe.Find(page)
	if m == nil {
		return Format{}, false
	}
	c := boardContentRe.FindSubmatch(m)
	if c == nil {
		return Format{}, false
	}
	f := Format{}
	for _, tok := range strings.Fields(string(c[1])) {
		if s := boardSizeRe.FindStringSubmatch(tok); s != nil {
			f.W, _ = strconv.Atoi(s[1])
			f.H, _ = strconv.Atoi(s[2])
		} else if known, ok := FormatByID(tok); ok {
			f = Format{ID: known.ID, Name: known.Name, W: f.W, H: f.H, Print: known.Print, Hint: known.Hint}
			if f.W == 0 {
				f.W, f.H = known.W, known.H
			}
		}
	}
	if f.W < 16 || f.H < 16 || f.W > 10000 || f.H > 10000 {
		return Format{}, false
	}
	if f.ID == "" {
		f.ID, f.Name = "custom", fmt.Sprintf("%d×%d", f.W, f.H)
	}
	return f, true
}

// Template is a new design's starting file: the frame, the brand's fonts and colours,
// and nothing else, so the first prompt shapes it.
func Template(f Format, title string, b Brand) []byte {
	bg, ink, acc, font := "#ffffff", "#111111", "#2f6bff", "system-ui, -apple-system, 'Segoe UI', sans-serif"
	if b.Paper != "" {
		bg = b.Paper
	}
	if b.Ink != "" {
		ink = b.Ink
	}
	if len(b.Accents) > 0 {
		acc = b.Accents[0]
	}
	if len(b.Fonts) > 0 {
		font = "'" + b.Fonts[0] + "', " + font
	}
	page := ""
	if f.Print != "" {
		page = fmt.Sprintf("\n  @page { size: %s; margin: 0; }\n  @media print { html, body { width: auto; height: auto; } }", f.Print)
	}
	t := html.EscapeString(title)
	return []byte(fmt.Sprintf(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="blaze-artboard" content="%s %dx%d">
<title>%s</title>
<style>
  :root { --bg: %s; --ink: %s; --accent: %s; }
  * { box-sizing: border-box; margin: 0; }
  html, body { width: %dpx; height: %dpx; overflow: hidden; }
  body { background: var(--bg); color: var(--ink); font-family: %s; display: grid; place-items: center; }%s
</style>
</head>
<body>
  <h1>%s</h1>
</body>
</html>
`, f.ID, f.W, f.H, t, bg, ink, acc, f.W, f.H, font, page, t))
}

// Prompt tells the model how to work inside an artboard.
func (f Format) Prompt() string {
	s := fmt.Sprintf("ARTBOARD: this design is a %s (%s), a fixed %d×%d px frame declared by <meta name=\"blaze-artboard\">. Keep that meta and keep html/body exactly %dpx × %dpx with overflow hidden; everything must fit inside, nothing scrolls. One self-contained HTML file: inline CSS, inline SVG for shapes and icons, real text (never images of text).", f.Name, f.Hint, f.W, f.H, f.W, f.H)
	if f.Print != "" {
		s += fmt.Sprintf(" It will be printed on %s: keep @page { size: %s; margin: 0 }, keep text 8 mm or more from the edges, use solid colours that print well.", f.Print, f.Print)
	}
	if f.ID == "email" {
		s += " Mail clients: tables for layout, inline style attributes, no flexbox, grid, web fonts or scripts; alt text on every image."
	}
	return s + "\n"
}

// DesignSlug makes a folder name from a design name.
func DesignSlug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case !dash && b.Len() > 0:
			b.WriteByte('-')
			dash = true
		}
		if b.Len() >= 40 {
			break
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "design"
	}
	return s
}

// ---------------- export ----------------

// ErrNoBrowser means no Chromium-based browser was found to render the export.
var ErrNoBrowser = errors.New("export needs Google Chrome, Microsoft Edge, Chromium or Brave installed")

// FindBrowser locates a Chromium-based browser for headless rendering.
func FindBrowser() string {
	if p := os.Getenv("BLAZE_BROWSER"); p != "" {
		return p
	}
	var cands []string
	switch runtime.GOOS {
	case "windows":
		for _, env := range []string{"ProgramFiles(x86)", "ProgramFiles", "LOCALAPPDATA"} {
			if d := os.Getenv(env); d != "" {
				cands = append(cands,
					filepath.Join(d, "Microsoft", "Edge", "Application", "msedge.exe"),
					filepath.Join(d, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(d, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"))
			}
		}
	case "darwin":
		for _, app := range []string{"Google Chrome.app/Contents/MacOS/Google Chrome", "Microsoft Edge.app/Contents/MacOS/Microsoft Edge", "Chromium.app/Contents/MacOS/Chromium", "Brave Browser.app/Contents/MacOS/Brave Browser"} {
			cands = append(cands, filepath.Join("/Applications", app))
			if h, err := os.UserHomeDir(); err == nil {
				cands = append(cands, filepath.Join(h, "Applications", app))
			}
		}
	default:
		for _, n := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "microsoft-edge", "microsoft-edge-stable", "brave-browser", "brave"} {
			if p, err := exec.LookPath(n); err == nil {
				return p
			}
		}
	}
	for _, c := range cands {
		if st, err := os.Stat(c); err == nil && !st.IsDir() {
			return c
		}
	}
	return ""
}

// HideBrowserWindow is set on Windows so the headless browser never flashes a console.
var HideBrowserWindow = func(*exec.Cmd) {}

// Export renders url (an artboard page) to out as "png" (at 2× for crisp text) or "pdf".
func Export(ctx context.Context, url string, f Format, kind, out string) error {
	br := FindBrowser()
	if br == "" {
		return ErrNoBrowser
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	prof, err := os.MkdirTemp("", "blaze-export-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(prof)
	args := []string{"--headless=new", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--hide-scrollbars",
		"--user-data-dir=" + prof, "--virtual-time-budget=4000", fmt.Sprintf("--window-size=%d,%d", f.W, f.H)}
	if os.Getenv("CI") != "" || os.Geteuid() == 0 {
		args = append(args, "--no-sandbox")
	}
	switch kind {
	case "png":
		args = append(args, "--force-device-scale-factor=2", "--screenshot="+out)
	case "pdf":
		args = append(args, "--no-pdf-header-footer", "--print-to-pdf-no-header", "--print-to-pdf="+out)
	default:
		return fmt.Errorf("unknown export %q", kind)
	}
	args = append(args, url)
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	_ = os.Remove(out)
	c := exec.CommandContext(ctx, br, args...)
	HideBrowserWindow(c)
	var errb bytes.Buffer
	c.Stderr = &errb
	runErr := c.Run()
	if st, err := os.Stat(out); err == nil && st.Size() > 0 {
		return nil
	}
	msg := strings.TrimSpace(errb.String())
	if len(msg) > 300 {
		msg = msg[len(msg)-300:]
	}
	if runErr != nil {
		return fmt.Errorf("%s: %v %s", filepath.Base(br), runErr, msg)
	}
	return fmt.Errorf("%s made no file %s", filepath.Base(br), msg)
}
