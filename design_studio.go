package main

// v0.20 Design studio (phase 1): the studio layout's mode (Page now; 3D, Motion and
// Audio as the next phases land), the swatches the colour wheel offers, and cleaning
// move/resize/colour edits before they are written into the source. The layout itself is
// frontend/dist/dstudio.js; the colour wheel is colorwheel.js.

import (
	"os"
	"path/filepath"
	"strings"

	"blaze-desktop/internal/studio"
)

// StudioModes are the studio's modes, in rail order.
var StudioModes = []string{"page", "3d", "motion", "audio"}

// normStudioMode maps anything unknown to "page".
func normStudioMode(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if containsStr(StudioModes, m) {
		return m
	}
	return "page"
}

// SetStudioMode remembers the studio mode for this project folder.
func (a *App) SetStudioMode(m string) string {
	m = normStudioMode(m)
	a.pv.mu.Lock()
	if a.pv.state.Mode != m {
		a.pv.state.Mode = m
		a.pv.saveLocked()
	}
	a.pv.mu.Unlock()
	return m
}

// PaletteView is what the colour wheel offers as swatches: the brand kit first, then the
// colours the previewed page already uses (most used first).
type PaletteView struct {
	Brand []string `json:"brand"`
	Page  []string `json:"page"`
}

// DesignPalette reads the brand kit and the previewed file's colours.
func (a *App) DesignPalette() PaletteView {
	a.pv.mu.Lock()
	b, t, folder := a.pv.state.Brand, a.pv.state.Target, a.pv.folder
	a.pv.mu.Unlock()
	var src string
	if t.Kind == "file" && t.Path != "" && folder != "" {
		if data, err := os.ReadFile(filepath.Join(folder, filepath.FromSlash(t.Path))); err == nil && len(data) < 2<<20 {
			src = string(data)
		}
	}
	return paletteFor(b, src)
}

func paletteFor(b studio.Brand, src string) PaletteView {
	v := PaletteView{Brand: []string{}, Page: []string{}}
	seen := map[string]bool{}
	add := func(list *[]string, c string, max int) {
		h, ok := studio.NormHex(c)
		if !ok || seen[h] || len(*list) >= max {
			return
		}
		seen[h] = true
		*list = append(*list, h)
	}
	for _, c := range append([]string{b.Ink, b.Paper}, b.Accents...) {
		add(&v.Brand, c, 10)
	}
	if src != "" {
		for _, c := range studio.Palette(src) {
			add(&v.Page, c, 12)
		}
	}
	return v
}

// cleanEdit tidies a studio edit before it's written: whole-pixel moves and sizes,
// lowercase hex colours. A value it can't read stops the edit with a plain reason.
func cleanEdit(e studio.Edit) (studio.Edit, error) {
	if e.Kind != "style" || len(e.Props) == 0 {
		return e, nil
	}
	p, err := studio.NormalizeStyle(e.Props)
	if err != nil {
		return e, err
	}
	e.Props = p
	return e, nil
}

// transformSummary words a move or resize for the versions strip.
func transformSummary(what string, props map[string]string) string {
	_, mv := props["translate"]
	_, w := props["width"]
	_, h := props["height"]
	_, sc := props["scale"]
	reset := true
	for _, v := range props {
		if v != "" {
			reset = false
		}
	}
	switch {
	case reset:
		return "Reset position of " + what
	case mv && (w || h || sc):
		return "Moved and resized " + what
	case mv:
		return "Moved " + what + " to " + props["translate"]
	case w && h:
		return "Resized " + what + " to " + strings.TrimSuffix(props["width"], "px") + " × " + strings.TrimSuffix(props["height"], "px")
	case w:
		return "Resized " + what + " to " + props["width"] + " wide"
	case h:
		return "Resized " + what + " to " + props["height"] + " tall"
	case sc:
		return "Scaled " + what + " to " + props["scale"] + "×"
	}
	return "Turned " + what
}
