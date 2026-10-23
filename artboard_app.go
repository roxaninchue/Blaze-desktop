package main

// v0.9.0 "design anything": scratch designs with a fixed artboard (no project folder
// needed) and export to PNG or PDF. The studio side is internal/studio/artboard.go.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/studio"
)

// designsDir holds scratch designs, one folder each.
func designsDir() string { return filepath.Join(config.Dir(), "designs") }

// DesignFormats lists the artboards New design offers.
func (a *App) DesignFormats() []studio.Format { return studio.Formats }

// NewDesign makes a scratch design (~/.blaze/designs/<name>/index.html) in a format,
// opens its folder as the current folder and previews it.
func (a *App) NewDesign(formatID, name string) DesignView {
	f, ok := studio.FormatByID(formatID)
	if !ok {
		f = studio.Formats[0]
	}
	if strings.TrimSpace(name) == "" {
		name = f.Name + " " + time.Now().Format("Jan 2 15.04")
	}
	base := studio.DesignSlug(name)
	dir := filepath.Join(designsDir(), base)
	for i := 2; ; i++ {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			break
		}
		dir = filepath.Join(designsDir(), fmt.Sprintf("%s-%d", base, i))
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return a.Design()
	}
	a.pv.mu.Lock()
	brand := a.pv.state.Brand
	a.pv.mu.Unlock()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), studio.Template(f, name, brand), 0o644); err != nil {
		return a.Design()
	}
	a.SetFolder(dir)
	// A new folder starts with no brand; carry the one the user had.
	if brand.Name != "" || len(brand.Accents) > 0 || len(brand.Fonts) > 0 {
		a.SetBrand(brand)
	}
	return a.SetDesignTarget("file", "index.html")
}

// artboard is the previewed file's artboard, if it has one.
func (a *App) artboard() (studio.Format, bool) {
	a.pv.mu.Lock()
	t := a.pv.state.Target
	a.pv.mu.Unlock()
	if t.Kind != "file" || a.prefs.Folder == "" || !strings.HasSuffix(strings.ToLower(t.Path), ".html") {
		return studio.Format{}, false
	}
	b, err := os.ReadFile(filepath.Join(a.prefs.Folder, filepath.FromSlash(t.Path)))
	if err != nil {
		return studio.Format{}, false
	}
	if len(b) > 1<<20 {
		b = b[:1<<20]
	}
	return studio.ParseArtboard(b)
}

// ExportResult is where an export landed, or why it didn't.
type ExportResult struct {
	Path  string `json:"path,omitempty"`
	Error string `json:"error,omitempty"`
}

// ExportDesign renders the previewed artboard to <folder>/exports as "png" or "pdf".
func (a *App) ExportDesign(kind string) ExportResult {
	f, ok := a.artboard()
	if !ok {
		return ExportResult{Error: "Export works on designs with an artboard (New design). For web pages, use Ship."}
	}
	a.pv.mu.Lock()
	t, head := a.pv.state.Target, a.pv.state.Head
	a.pv.mu.Unlock()
	name := strings.TrimSuffix(filepath.Base(t.Path), filepath.Ext(t.Path))
	if name == "index" {
		name = filepath.Base(a.prefs.Folder)
	}
	stamp := time.Now().Format("150405")
	if head > 0 {
		stamp = fmt.Sprintf("v%02d", head)
	}
	out := filepath.Join(a.prefs.Folder, "exports", fmt.Sprintf("%s-%s.%s", name, stamp, kind))
	parts := strings.Split(filepath.ToSlash(t.Path), "/")
	url := a.pv.base() + "/" + strings.Join(parts, "/")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := studio.Export(ctx, url, f, kind, out); err != nil {
		return ExportResult{Error: "Couldn't export: " + err.Error()}
	}
	return ExportResult{Path: out}
}

// RevealFile shows a file in Explorer, Finder or the file manager.
func (a *App) RevealFile(p string) {
	var c *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		c = exec.Command("explorer", "/select,", p)
	case "darwin":
		c = exec.Command("open", "-R", p)
	default:
		c = exec.Command("xdg-open", filepath.Dir(p))
	}
	_ = c.Start()
}
