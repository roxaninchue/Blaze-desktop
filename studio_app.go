package main

// v0.8.0 Design studio: the app side of internal/studio.
//
//   1  Edits that write back      ApplyEdit / SwapColor / UndoEdit
//   2  Directions as live branches StartDirections / Directions / MixDirections / PickDirection
//   3  Taste memory + brand kit    learnTaste / BrandFromURL / BrandFromImage / Taste
//   4  Copy any window            CopyWindow / CopyFromImage
//   5  The critic                 SaveCritique (the page audits itself in picker.js)
//   6  Talk while you point       frontend (studio.js) over Transcribe / RecStart
//   7  States board               frontend + picker.js (?__state=…)
//   8  Visual version history     Version.Parent/Head + RestoreElement
//   9  Preview on your phone      PhonePreview (LAN server, QR, hot reload, comments)
//  10  One-click ship             Ship (Cloudflare Pages / GitHub Pages) + shared comments

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/cloud"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/screen"
	"blaze-desktop/internal/studio"
)

// StudioPrefs are the Design studio's settings (desktop.json "studio").
type StudioPrefs struct {
	AutoFix   *bool `json:"auto_fix,omitempty"`  // let the critic fix a version on its own (default on)
	Threshold int   `json:"threshold,omitempty"` // fix below this score (default 85)
	NoTaste   bool  `json:"no_taste,omitempty"`  // don't learn or use taste
	Comments  *bool `json:"comments,omitempty"`  // shipped pages get pinned comments (default on)
}

func (p StudioPrefs) autoFix() bool { return p.AutoFix == nil || *p.AutoFix }
func (p StudioPrefs) threshold() int {
	if p.Threshold <= 0 || p.Threshold > 100 {
		return 85
	}
	return p.Threshold
}
func (p StudioPrefs) comments() bool { return p.Comments == nil || *p.Comments }

// ---------------- taste memory (shared by every project) ----------------

var tasteMu sync.Mutex
var taste *studio.Taste
var tasteOff bool

func tasteFile() string { return filepath.Join(config.Dir(), "desktop", "taste.json") }

func withTaste(f func(t *studio.Taste)) {
	tasteMu.Lock()
	defer tasteMu.Unlock()
	if tasteOff {
		return
	}
	if taste == nil {
		taste = &studio.Taste{}
		b, _ := os.ReadFile(tasteFile())
		_ = json.Unmarshal(b, taste)
	}
	f(taste)
	_ = os.MkdirAll(filepath.Dir(tasteFile()), 0o700)
	b, _ := json.Marshal(taste)
	_ = os.WriteFile(tasteFile(), b, 0o600)
}

func joinFiles(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		b.WriteString(m[k])
		b.WriteString("\n")
	}
	return b.String()
}

// learnTaste runs when a version is saved: the parent's new values are kept or dropped.
func learnTaste(vs []Version) {
	if len(vs) < 2 {
		return
	}
	by := map[int]Version{}
	for _, v := range vs {
		by[v.N] = v
	}
	cur := vs[len(vs)-1]
	mid, ok := by[cur.Parent]
	if !ok {
		return
	}
	before := by[mid.Parent] // zero Version (no files) when it's the first
	b, m, c := joinFiles(before.Files), joinFiles(mid.Files), joinFiles(cur.Files)
	go withTaste(func(t *studio.Taste) { t.Compare(b, m, c) })
}

func tasteUndo(dropped, kept string) { go withTaste(func(t *studio.Taste) { t.Undo(dropped, kept) }) }

// TasteView is Settings › Design › Taste.
type TasteView struct {
	Prompt string        `json:"prompt"`
	Prefs  []studio.Pref `json:"prefs"`
	Off    bool          `json:"off"`
}

func (a *App) Taste() TasteView {
	var v TasteView
	withTaste(func(t *studio.Taste) {
		v.Prompt = t.Prompt()
		v.Prefs = append(v.Prefs, t.Prefs...)
	})
	sort.Slice(v.Prefs, func(i, j int) bool { return abs(v.Prefs[i].Score) > abs(v.Prefs[j].Score) })
	if len(v.Prefs) > 40 {
		v.Prefs = v.Prefs[:40]
	}
	v.Off = a.prefs.Studio.NoTaste
	return v
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func (a *App) ForgetTaste() TasteView {
	tasteMu.Lock()
	taste = &studio.Taste{}
	_ = os.Remove(tasteFile())
	tasteMu.Unlock()
	return a.Taste()
}

func (a *App) SetStudioPrefs(p StudioPrefs) StudioPrefs {
	a.mu.Lock()
	a.prefs.Studio = p
	a.mu.Unlock()
	tasteMu.Lock()
	tasteOff = p.NoTaste
	tasteMu.Unlock()
	a.savePrefs()
	return p
}

func (a *App) StudioPrefs() StudioPrefs { a.mu.Lock(); defer a.mu.Unlock(); return a.prefs.Studio }

// studioPrompt is what the system prompt adds for design work.
func (a *App) studioPrompt() string {
	var b strings.Builder
	a.pv.mu.Lock()
	br := a.pv.state.Brand
	a.pv.mu.Unlock()
	b.WriteString(br.Prompt())
	if !a.prefs.Studio.NoTaste {
		withTaste(func(t *studio.Taste) { b.WriteString(t.Prompt()) })
	}
	if f, ok := a.artboard(); ok {
		b.WriteString(f.Prompt())
	} else if a.prefs.Folder != "" {
		b.WriteString("PROJECT STACK: " + studio.DetectStack(a.prefs.Folder).Describe() + " (build designs in it)\n")
	}
	b.WriteString(`- A message starting with [Critic fix] comes from Blaze's own design critic: fix exactly the listed issues in the source, keep the look, one line on what changed.
- A message starting with [Copy window] has a screenshot: rebuild what it shows as editable code in the project's stack, matching layout, spacing, type and colours closely (use the brand kit only where the screenshot has no opinion). Real text, no images of text.
- A message starting with [Directions mix] names parts to take from each Directions branch (folders given). Read those files and combine the named parts into the project's own files, then say in one line what came from where.
`)
	return b.String()
}

// ---------------- 1. edits that write back ----------------

// EditReq is a change made in the preview.
type EditReq struct {
	File string      `json:"file"` // project-relative; "" = the previewed page
	Edit studio.Edit `json:"edit"`
}

// EditResult says what happened.
type EditResult struct {
	OK       bool   `json:"ok"`
	File     string `json:"file,omitempty"`
	Diff     string `json:"diff,omitempty"`
	Add      int    `json:"add,omitempty"`
	Del      int    `json:"del,omitempty"`
	Version  int    `json:"version,omitempty"`
	Fallback bool   `json:"fallback,omitempty"` // couldn't write it directly: send it to the model
	Error    string `json:"error,omitempty"`
}

var lastEdit struct {
	sync.Mutex
	props map[string]string
	n     int
}

// editCandidates lists the files an element may live in: the previewed page first, then
// the project's other template files (components for a dev server).
func (a *App) editCandidates(file string) []string {
	a.pv.mu.Lock()
	t := a.pv.state.Target
	tracked := append([]string(nil), a.pv.state.Tracked...)
	a.pv.mu.Unlock()
	var out []string
	add := func(f string) {
		if f == "" || containsStr(out, f) {
			return
		}
		if _, ok := studio.LangFor(f); ok {
			out = append(out, f)
		}
	}
	add(file)
	if t.Kind == "file" {
		add(t.Path)
	}
	for _, f := range tracked {
		add(f)
	}
	for _, f := range walkFiles(a.prefs.Folder, 3000) {
		l := strings.ToLower(f.rel)
		if strings.HasSuffix(l, ".min.js") || strings.Contains(l, "/dist/") || strings.HasPrefix(l, "dist/") || strings.HasPrefix(l, "build/") {
			continue
		}
		if designExt.MatchString(l) || strings.HasSuffix(l, ".jsx") || strings.HasSuffix(l, ".tsx") {
			add(f.rel)
		}
		if len(out) > 400 {
			break
		}
	}
	return out
}

// ApplyEdit writes a preview edit into the element's source file and saves a version.
func (a *App) ApplyEdit(req EditReq) EditResult {
	// Moves, resizes and colours from the studio land as whole pixels and plain hex.
	if e, err := cleanEdit(req.Edit); err != nil {
		return EditResult{Error: "Couldn't use that value: " + err.Error()}
	} else {
		req.Edit = e
	}
	folder := a.prefs.Folder
	cands := a.editCandidates(req.File)
	type hit struct {
		rel, old, nw string
	}
	var hits []hit
	var lastErr error
	for i, rel := range cands {
		full := filepath.Join(folder, filepath.FromSlash(rel))
		b, err := os.ReadFile(full)
		if err != nil || len(b) > 2<<20 {
			continue
		}
		lang, _ := studio.LangFor(rel)
		out, err := studio.Apply(string(b), lang, req.Edit)
		if err != nil {
			if err != studio.ErrNotFound {
				lastErr = err
				if i == 0 {
					break // the element is in the page but the change is too complex
				}
			}
			continue
		}
		if out == string(b) {
			continue
		}
		hits = append(hits, hit{rel, string(b), out})
		if i == 0 || len(hits) > 1 {
			break // the previewed page wins; two other files matching is ambiguous
		}
	}
	if len(hits) != 1 {
		msg := "couldn't find that element in the source"
		if len(hits) > 1 {
			msg = "that element matches more than one file"
		}
		if lastErr == studio.ErrTooComplex {
			msg = "that change is too involved to make by hand"
		}
		return EditResult{Fallback: true, Error: msg}
	}
	h := hits[0]
	full := filepath.Join(folder, filepath.FromSlash(h.rel))
	if err := os.WriteFile(full, []byte(h.nw), 0o644); err != nil {
		return EditResult{Error: err.Error()}
	}
	v := a.recordEdit([]string{h.rel}, editSummary(req.Edit))
	if req.Edit.Kind == "style" {
		props := req.Edit.Props
		go withTaste(func(t *studio.Taste) { t.ObserveEdit(props, 2) })
		lastEdit.Lock()
		lastEdit.props, lastEdit.n = props, v.N
		lastEdit.Unlock()
	}
	d := studio.Diff(h.rel, h.old, h.nw)
	ad, dl := studio.DiffStat(d)
	a.emit("design", map[string]any{"edit": true, "version": v.N})
	return EditResult{OK: true, File: h.rel, Diff: d, Add: ad, Del: dl, Version: v.N}
}

func editSummary(e studio.Edit) string {
	what := "<" + e.Target.Tag + ">"
	if len(e.Target.Classes) > 0 {
		what = e.Target.Tag + "." + e.Target.Classes[0]
	}
	switch e.Kind {
	case "text":
		t := e.NewText
		if len(t) > 40 {
			t = t[:40] + "…"
		}
		return "Edited text of " + what + ": " + strconv.Quote(t)
	case "style":
		if studio.IsTransform(e.Props) {
			return transformSummary(what, e.Props)
		}
		var parts []string
		for _, k := range sortedKeys(e.Props) {
			parts = append(parts, k+" "+e.Props[k])
		}
		return "Styled " + what + ": " + strings.Join(parts, ", ")
	case "attr":
		return "Changed " + strings.Join(sortedKeys(e.Props), ", ") + " on " + what
	case "remove":
		return "Removed " + what
	}
	return "Edited " + what
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// recordEdit tracks the files and saves a version for a direct edit.
func (a *App) recordEdit(files []string, summary string) Version {
	a.pv.mu.Lock()
	defer a.pv.mu.Unlock()
	for _, f := range files {
		if !containsStr(a.pv.state.Tracked, f) {
			a.pv.state.Tracked = append(a.pv.state.Tracked, f)
		}
	}
	return a.pv.recordLocked(summary, "edit")
}

// UndoEdit goes back to the version before the last one (and learns the edit was unwanted).
func (a *App) UndoEdit() string {
	a.pv.mu.Lock()
	head := a.pv.versionLocked(a.pv.state.Head)
	parent := 0
	if head != nil {
		parent = head.Parent
	}
	a.pv.mu.Unlock()
	if parent == 0 {
		return "Nothing to undo."
	}
	lastEdit.Lock()
	if head != nil && lastEdit.n == head.N && lastEdit.props != nil {
		props := lastEdit.props
		go withTaste(func(t *studio.Taste) { t.ObserveEdit(props, -3) }) // +2 when made, net -1
		lastEdit.props = nil
	}
	lastEdit.Unlock()
	return a.RestoreVersion(parent)
}

// SwapColor replaces one colour with another across the project's design files.
func (a *App) SwapColor(old, nw string) EditResult {
	folder := a.prefs.Folder
	var changed []string
	var diffs strings.Builder
	total := 0
	for _, f := range walkFiles(folder, 3000) {
		l := strings.ToLower(f.rel)
		if !(designExt.MatchString(l) || strings.HasSuffix(l, ".jsx") || strings.HasSuffix(l, ".tsx") || strings.HasSuffix(l, ".less")) || strings.Contains(l, "node_modules/") || strings.HasPrefix(l, "dist/") || strings.HasPrefix(l, "build/") {
			continue
		}
		full := filepath.Join(folder, filepath.FromSlash(f.rel))
		b, err := os.ReadFile(full)
		if err != nil || len(b) > 2<<20 {
			continue
		}
		out, n := studio.SwapColor(string(b), old, nw)
		if n == 0 {
			continue
		}
		if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
			return EditResult{Error: err.Error()}
		}
		total += n
		changed = append(changed, f.rel)
		diffs.WriteString(studio.Diff(f.rel, string(b), out))
	}
	if total == 0 {
		return EditResult{Error: "that colour isn't written anywhere in the project's design files"}
	}
	v := a.recordEdit(changed, fmt.Sprintf("Swapped %s for %s (%d place%s)", old, nw, total, plural(total)))
	go withTaste(func(t *studio.Taste) { t.Observe("color", nw, 2); t.Observe("color", old, -1) })
	d := diffs.String()
	ad, dl := studio.DiffStat(d)
	a.emit("design", map[string]any{"edit": true, "version": v.N})
	return EditResult{OK: true, File: strings.Join(changed, ", "), Diff: d, Add: ad, Del: dl, Version: v.N}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// ---------------- 8. restore one element from an old version ----------------

func (a *App) RestoreElement(n int, t studio.Target) EditResult {
	a.pv.mu.Lock()
	v := a.pv.versionLocked(n)
	target := a.pv.state.Target
	var files map[string]string
	if v != nil {
		files = v.Files
	}
	a.pv.mu.Unlock()
	if v == nil {
		return EditResult{Error: "that version is gone"}
	}
	order := []string{}
	if target.Kind == "file" {
		order = append(order, target.Path)
	}
	for rel := range files {
		if !containsStr(order, rel) {
			order = append(order, rel)
		}
	}
	for _, rel := range order {
		old, ok := files[rel]
		if !ok {
			continue
		}
		lang, ok := studio.LangFor(rel)
		if !ok {
			continue
		}
		el, err := studio.Extract(old, lang, t)
		if err != nil {
			continue
		}
		full := filepath.Join(a.prefs.Folder, filepath.FromSlash(rel))
		curB, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		out, err := studio.ReplaceElement(string(curB), lang, t, el)
		if err != nil {
			return EditResult{Error: "that element isn't in the current page any more"}
		}
		if out == string(curB) {
			return EditResult{Error: "it's the same in both versions"}
		}
		if err := os.WriteFile(full, []byte(out), 0o644); err != nil {
			return EditResult{Error: err.Error()}
		}
		nv := a.recordEdit([]string{rel}, fmt.Sprintf("Restored <%s> from version %02d", t.Tag, n))
		d := studio.Diff(rel, string(curB), out)
		ad, dl := studio.DiffStat(d)
		a.emit("design", map[string]any{"edit": true, "version": nv.N})
		return EditResult{OK: true, File: rel, Diff: d, Add: ad, Del: dl, Version: nv.N}
	}
	return EditResult{Error: "that element wasn't in version " + strconv.Itoa(n)}
}

// ---------------- 5. the critic ----------------

// CritiqueResult is the score card, plus the fix request when Blaze should fix it itself.
type CritiqueResult struct {
	Card studio.Card `json:"card"`
	Fix  string      `json:"fix,omitempty"`
}

// SaveCritique scores what the page reported at each width and keeps the card on the version.
func (a *App) SaveCritique(n int, audits []studio.Audit) CritiqueResult {
	card := studio.Score(audits)
	card.Version = n
	a.pv.mu.Lock()
	v := a.pv.versionLocked(n)
	file := a.pv.state.Target.Path
	if v != nil {
		if v.Card != nil {
			card.FixOf, card.Fixed = v.Card.FixOf, v.Card.Fixed
		}
		v.Card = &card
		a.pv.saveLocked()
	}
	a.pv.mu.Unlock()
	res := CritiqueResult{Card: card}
	if a.prefs.Studio.autoFix() && card.NeedsFix(a.prefs.Studio.threshold()) && v != nil {
		res.Fix = studio.FixPrompt(card, audits, orStr(file, "the page"))
	}
	return res
}

// MarkFixRequested notes that the critic's fix was sent (so it fixes once).
func (a *App) MarkFixRequested(n int) {
	a.pv.mu.Lock()
	if v := a.pv.versionLocked(n); v != nil && v.Card != nil {
		v.Card.Fixed = true
		a.pv.saveLocked()
	}
	a.pv.mu.Unlock()
}

// ---------------- 3. brand kit ----------------

func (a *App) BrandKit() studio.Brand {
	a.pv.mu.Lock()
	defer a.pv.mu.Unlock()
	return a.pv.state.Brand
}

func (a *App) SetBrand(b studio.Brand) studio.Brand {
	a.pv.mu.Lock()
	a.pv.state.Brand = b
	a.pv.saveLocked()
	a.pv.mu.Unlock()
	return b
}

func (a *App) ClearBrand() studio.Brand { return a.SetBrand(studio.Brand{}) }

// BrandResult is a brand read from somewhere, or why it couldn't be.
type BrandResult struct {
	Brand studio.Brand `json:"brand"`
	Error string       `json:"error,omitempty"`
}

var webClient = &http.Client{Timeout: 15 * time.Second}

func fetchText(u string, max int64) (string, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 (Blaze brand kit)")
	resp, err := webClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%s answered %s", u, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max))
	return string(b), err
}

// BrandFromURL reads a brand from a website and makes it the project's brand kit.
func (a *App) BrandFromURL(u string) BrandResult {
	u = strings.TrimSpace(u)
	if !strings.HasPrefix(u, "http") {
		u = "https://" + u
	}
	page, err := fetchText(u, 3<<20)
	if err != nil {
		return BrandResult{Error: "Couldn't open " + u + ": " + err.Error()}
	}
	var css []string
	for i, s := range studio.Stylesheets(page, u) {
		if i == 6 {
			break
		}
		if c, err := fetchText(s, 2<<20); err == nil {
			css = append(css, c)
		}
	}
	b := studio.BrandFromPage(page, css, u)
	if b.Empty() {
		return BrandResult{Error: "That page didn't show any colours or fonts to learn from."}
	}
	return BrandResult{Brand: a.SetBrand(b)}
}

// BrandFromImageData reads a brand palette from a logo (base64 PNG/JPEG/GIF).
func (a *App) BrandFromImageData(b64, name string) BrandResult {
	if i := strings.Index(b64, ","); i >= 0 && strings.HasPrefix(b64, "data:") {
		b64 = b64[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return BrandResult{Error: "That file couldn't be read."}
	}
	// Keep the logo in the project so designs can use it.
	rel := ""
	if a.prefs.Folder != "" && name != "" {
		rel = "assets/" + filepath.Base(name)
		full := filepath.Join(a.prefs.Folder, filepath.FromSlash(rel))
		if _, err := os.Stat(full); err != nil {
			_ = os.MkdirAll(filepath.Dir(full), 0o755)
			_ = os.WriteFile(full, data, 0o644)
		}
	}
	b, err := studio.BrandFromImage(bytes.NewReader(data), orStr(rel, name))
	if err != nil {
		return BrandResult{Error: "That isn't a PNG, JPEG or GIF image (SVG logos: use a URL instead)."}
	}
	cur := a.BrandKit()
	if len(cur.Fonts) > 0 { // keep fonts and notes the image can't tell us
		b.Fonts, b.Notes, b.Name = cur.Fonts, cur.Notes, cur.Name
	}
	return BrandResult{Brand: a.SetBrand(b)}
}

// ---------------- 4. copy any window ----------------

// CopyWindow takes a screenshot of the window behind Blaze and asks Blaze to rebuild it.
func (a *App) CopyWindow(note string) string {
	if sv.r == nil {
		return "Screen capture isn't available on this computer."
	}
	if a.prefs.Vision.Off || a.prefs.Vision.NoPixels {
		return "Screenshots are turned off (Settings › Notifications › Screen vision)."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shot, err := sv.r.Look(ctx, screen.LookOpts{})
	if err != nil {
		return "Couldn't take a screenshot: " + err.Error()
	}
	name := "window-" + time.Now().Format("150405") + ".jpg"
	return a.copyImage(shot.JPEG, name, "image/jpeg", fmt.Sprintf("%s — %q", shot.Win.App, shot.Win.Title), note)
}

// CopyFromImage rebuilds a dropped or pasted screenshot (base64) as code.
func (a *App) CopyFromImage(b64, name, note string) string {
	mime := "image/png"
	if i := strings.Index(b64, ","); i >= 0 && strings.HasPrefix(b64, "data:") {
		if strings.Contains(b64[:i], "jpeg") || strings.Contains(b64[:i], "jpg") {
			mime = "image/jpeg"
		}
		b64 = b64[i+1:]
	}
	data, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "That image couldn't be read."
	}
	if strings.HasSuffix(strings.ToLower(name), ".jpg") || strings.HasSuffix(strings.ToLower(name), ".jpeg") {
		mime = "image/jpeg"
	}
	return a.copyImage(data, orStr(filepath.Base(name), "screenshot.png"), mime, "a screenshot", note)
}

func (a *App) copyImage(data []byte, name, mime, what, note string) string {
	dir := filepath.Join(designDir(a.prefs.Folder), "copies")
	_ = os.MkdirAll(dir, 0o700)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err.Error()
	}
	st := studio.DetectStack(a.prefs.Folder)
	target := "a new page (design/copy.html for plain HTML, or a new component for a framework app)"
	text := fmt.Sprintf("[Copy window] Rebuild %s as editable code in this project's stack (%s), as %s. Match the layout, spacing, type sizes and colours closely; real text, no screenshots inside the page.", what, st.Describe(), target)
	if strings.TrimSpace(note) != "" {
		text += "\nAlso: " + strings.TrimSpace(note)
	}
	a.SendWith(text, nil, []llm.Attach{{Kind: "image", Path: path, Name: name, Size: int64(len(data)), Mime: mime}})
	return "ok"
}

// ---------------- 2. directions as live branches ----------------

// DirView is one Directions branch.
type DirView struct {
	Handle string `json:"handle"`
	Letter string `json:"letter"`
	Angle  string `json:"angle"`
	Status string `json:"status"`
	Now    string `json:"now"`
	URL    string `json:"url"`  // its preview
	Path   string `json:"path"` // its folder
	Review string `json:"review,omitempty"`
}

var dirAngles = []string{
	"Bold and editorial: big confident type, strong contrast, generous whitespace",
	"Warm and friendly: softer colours, rounded shapes, approachable copy",
	"Minimal and precise: restrained palette, tight grid, quiet details",
}

// StartDirections sends 3 specialists out on separate worktrees, one design direction each.
func (a *App) StartDirections(brief string) string {
	if a.agents == nil {
		return "Background agents aren't ready yet."
	}
	folder := a.prefs.Folder
	if folder == "" {
		return "Open a project folder first."
	}
	a.DiscardDirections()
	a.pv.mu.Lock()
	t := a.pv.state.Target
	brand := a.pv.state.Brand.Prompt()
	a.pv.mu.Unlock()
	page := "index.html"
	if t.Kind == "file" && t.Path != "" {
		page = t.Path
	}
	st := studio.DetectStack(folder)
	brief = strings.TrimSpace(brief)
	if brief == "" {
		brief = "Redesign the page so it looks great and works on phone, tablet and desktop."
	}
	a.mu.Lock()
	conv := a.convID
	a.mu.Unlock()
	steps := a.cfg.MaxSteps
	if steps <= 0 {
		steps = 40
	}
	for i, angle := range dirAngles {
		letter := string(rune('a' + i))
		task := fmt.Sprintf(`You are design direction %s of 3 for this project (%s). Goal: %s

Your direction: %s. Make it clearly different from a generic page; commit to it.
Work on %s in this folder (create it if missing; for a framework app change the page's real components and keep the app building). Keep all content and links; change the design. Use only files in this folder.
%s
When done, reply in at most 3 lines: the direction's name and what makes it distinct. Don't start servers.`, strings.ToUpper(letter), st.Describe(), brief, angle, page, brand)
		spec := &agents.Agent{Handle: agents.DirectionPrefix + letter, Task: task, Conv: conv, Folder: folder, Kind: agents.Specialist, Mode: "build", Workdir: folder, MaxSteps: steps}
		if _, _, err := a.agents.DispatchAgent(spec); err != nil {
			return "Couldn't start direction " + strings.ToUpper(letter) + ": " + err.Error()
		}
	}
	a.emit("directions", a.Directions())
	return "ok"
}

// Directions lists the live branches.
func (a *App) Directions() []DirView {
	if a.agents == nil {
		return nil
	}
	a.mu.Lock()
	conv := a.convID
	a.mu.Unlock()
	a.pv.mu.Lock()
	t := a.pv.state.Target
	a.pv.mu.Unlock()
	page := "index.html"
	if t.Kind == "file" && t.Path != "" {
		page = t.Path
	}
	var out []DirView
	for _, v := range a.agents.List(conv) {
		if !agents.IsDirection(v.Handle) || v.Review == "applied" || v.Review == "discarded" {
			continue
		}
		letter := strings.ToUpper(strings.TrimPrefix(v.Handle, agents.DirectionPrefix))
		angle := ""
		if i := int(letter[0] - 'A'); len(letter) == 1 && i >= 0 && i < len(dirAngles) {
			angle = strings.SplitN(dirAngles[i], ":", 2)[0]
		}
		d := DirView{Handle: v.Handle, Letter: letter, Angle: angle, Status: v.Status, Now: v.Now, Path: v.Path, Review: v.Review}
		if v.Path != "" {
			d.URL = a.pv.base() + "/__dir/" + v.Handle + "/" + page
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Letter < out[j].Letter })
	return out
}

// dirFolder resolves a branch's folder for the preview server.
func (a *App) dirFolder(handle string) string {
	if a.agents == nil || !agents.IsDirection(handle) {
		return ""
	}
	v, ok := a.agents.Get(handle)
	if !ok {
		return ""
	}
	return v.Path
}

// MixDirections asks Blaze to combine named parts ("header from B, colours from C").
func (a *App) MixDirections(instruction string) string {
	ds := a.Directions()
	if len(ds) == 0 {
		return "There are no directions to mix."
	}
	for _, d := range ds {
		if agents.Active(d.Status) {
			return "Direction " + d.Letter + " is still working. Mix when all three are done."
		}
	}
	var b strings.Builder
	b.WriteString("[Directions mix] " + strings.TrimSpace(instruction) + "\nThe branches (read-only; copy what's asked into the project's own files):\n")
	for _, d := range ds {
		fmt.Fprintf(&b, "- %s (%s): %s\n", d.Letter, d.Angle, d.Path)
	}
	a.SendWith(b.String(), nil, nil)
	return "ok"
}

// PickDirection applies one branch whole and discards the others.
func (a *App) PickDirection(handle string) string {
	if a.agents == nil {
		return "Background agents aren't ready yet."
	}
	msg, err := a.agents.ApplySpecialist(handle, "apply")
	if err != nil {
		return err.Error()
	}
	for _, d := range a.Directions() {
		if d.Handle != handle {
			if agents.Active(d.Status) {
				_ = a.agents.Cancel(d.Handle)
			}
			_, _ = a.agents.ApplySpecialist(d.Handle, "discard")
		}
	}
	a.pv.mu.Lock()
	a.pv.recordLocked("Picked direction "+strings.ToUpper(strings.TrimPrefix(handle, agents.DirectionPrefix)), "mix")
	a.pv.mu.Unlock()
	a.emit("design", map[string]any{"edit": true})
	a.emit("directions", a.Directions())
	_ = msg
	return "ok"
}

// DiscardDirections drops every branch.
func (a *App) DiscardDirections() string {
	if a.agents == nil {
		return "ok"
	}
	for _, d := range a.Directions() {
		if agents.Active(d.Status) {
			_ = a.agents.Cancel(d.Handle)
		}
		if _, err := a.agents.ApplySpecialist(d.Handle, "discard"); err != nil {
			_ = a.agents.Discard(d.Handle)
		}
	}
	a.emit("directions", []DirView{})
	return "ok"
}

// ---------------- 9. preview on your phone ----------------

// PhoneView is the phone preview's address.
type PhoneView struct {
	On    bool   `json:"on"`
	URL   string `json:"url,omitempty"`
	QR    string `json:"qr,omitempty"` // data: URL of a PNG
	Error string `json:"error,omitempty"`
}

func lanIP() string {
	var best string
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		low := strings.ToLower(ifc.Name)
		if strings.Contains(low, "docker") || strings.Contains(low, "veth") || strings.Contains(low, "vmnet") || strings.Contains(low, "virtualbox") || strings.HasPrefix(low, "br-") {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, ad := range addrs {
			ipn, ok := ad.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || !ipn.IP.IsPrivate() {
				continue
			}
			ip := ipn.IP.String()
			if strings.HasPrefix(ip, "192.168.") || best == "" {
				best = ip
			}
		}
	}
	return best
}

func qrDataURL(s string) string {
	c, err := qr.Encode(s, qr.M)
	if err != nil {
		return ""
	}
	var b bytes.Buffer
	if err := png.Encode(&b, qrImage(c, 6)); err != nil {
		return ""
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(b.Bytes())
}

// qrImage draws c with scale pixels per module and a 4-module quiet zone. rsc.io/qr's own
// Image() sizes the bounds for Scale but reads At(x, y) as module coordinates, so it
// drew the code tiny in the top-left corner.
func qrImage(c *qr.Code, scale int) image.Image {
	d := (c.Size + 8) * scale
	img := image.NewGray(image.Rect(0, 0, d, d))
	for y := 0; y < d; y++ {
		for x := 0; x < d; x++ {
			v := uint8(0xFF)
			if c.Black(x/scale-4, y/scale-4) {
				v = 0
			}
			img.Pix[y*img.Stride+x] = v
		}
	}
	return img
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// PhonePreview turns the phone preview on or off. On, the preview is also served on
// the local network behind a secret link (the QR code); pages hot-reload on every
// change and a long press leaves a comment pinned to that spot.
func (a *App) PhonePreview(on bool) PhoneView {
	p := a.pv
	p.mu.Lock()
	defer p.mu.Unlock()
	if !on {
		if p.lan != nil {
			p.lan.Close()
			p.lan = nil
		}
		return PhoneView{}
	}
	ip := lanIP()
	if ip == "" {
		return PhoneView{Error: "This computer isn't on a local network (Wi-Fi or Ethernet), so a phone can't reach it."}
	}
	if p.lan == nil {
		ln, err := net.Listen("tcp", "0.0.0.0:0")
		if err != nil {
			return PhoneView{Error: "Couldn't open the phone preview: " + err.Error()}
		}
		p.lan = ln
		p.lanPort = ln.Addr().(*net.TCPAddr).Port
		if p.token == "" {
			p.token = randHex(12)
		}
		go http.Serve(ln, http.HandlerFunc(p.serveLAN))
	}
	path := "/"
	if p.state.Target.Kind == "file" {
		path += p.state.Target.Path
	}
	u := fmt.Sprintf("http://%s:%d%s?k=%s", ip, p.lanPort, path, p.token)
	return PhoneView{On: true, URL: u, QR: qrDataURL(u)}
}

// serveLAN is the phone's door: the secret once in the link, then a cookie.
func (p *preview) serveLAN(w http.ResponseWriter, r *http.Request) {
	p.mu.Lock()
	tok := p.token
	p.mu.Unlock()
	ok := false
	if k := r.URL.Query().Get("k"); k != "" && k == tok {
		http.SetCookie(w, &http.Cookie{Name: "bzk", Value: tok, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.SetCookie(w, &http.Cookie{Name: "bzphone", Value: "1", Path: "/", SameSite: http.SameSiteLaxMode})
		ok = true
	} else if c, err := r.Cookie("bzk"); err == nil && c.Value == tok {
		ok = true
	}
	if !ok || tok == "" {
		http.Error(w, "This preview link has expired. Scan the QR code in Blaze again.", 403)
		return
	}
	p.serve(w, r)
}

// special serves Blaze's own endpoints: hot reload and comments.
func (p *preview) special(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case "/__blaze/live":
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no streaming", 500)
			return true
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-store")
		ch := make(chan string, 4)
		p.mu.Lock()
		if p.live == nil {
			p.live = map[chan string]bool{}
		}
		p.live[ch] = true
		p.mu.Unlock()
		defer func() { p.mu.Lock(); delete(p.live, ch); p.mu.Unlock() }()
		fmt.Fprint(w, ": hi\n\n")
		fl.Flush()
		tick := time.NewTicker(25 * time.Second)
		defer tick.Stop()
		for {
			select {
			case <-r.Context().Done():
				return true
			case m := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", m)
				fl.Flush()
			case <-tick.C:
				fmt.Fprint(w, ": ping\n\n")
				fl.Flush()
			}
		}
	case "/__blaze/comment":
		if r.Method == http.MethodPost {
			var c Comment
			if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&c); err != nil || strings.TrimSpace(c.Text) == "" {
				http.Error(w, "bad comment", 400)
				return true
			}
			c.ID, c.When, c.From = randHex(4), time.Now().Format("15:04"), "phone"
			if len(c.Text) > 1000 {
				c.Text = c.Text[:1000]
			}
			p.mu.Lock()
			p.state.Comments = append(p.state.Comments, c)
			if len(p.state.Comments) > 200 {
				p.state.Comments = p.state.Comments[len(p.state.Comments)-200:]
			}
			p.saveLocked()
			cb := p.onNote
			p.mu.Unlock()
			if cb != nil {
				cb(c)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(c)
			return true
		}
		p.mu.Lock()
		cs := append([]Comment(nil), p.state.Comments...)
		p.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(cs)
		return true
	}
	return false
}

// broadcast tells every open preview (phone, browser) to reload.
func (p *preview) broadcast(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for ch := range p.live {
		select {
		case ch <- msg:
		default:
		}
	}
}

// watchDesign reloads previews when tracked files change outside Blaze (an editor).
func (a *App) watchDesign() {
	seen := map[string]time.Time{}
	for range time.Tick(time.Second) {
		a.pv.mu.Lock()
		n := len(a.pv.live)
		folder := a.pv.folder
		files := append([]string(nil), a.pv.state.Tracked...)
		if a.pv.state.Target.Kind == "file" && a.pv.state.Target.Path != "" {
			files = append(files, a.pv.state.Target.Path)
		}
		a.pv.mu.Unlock()
		if n == 0 {
			continue
		}
		changed := false
		for _, f := range files {
			st, err := os.Stat(filepath.Join(folder, filepath.FromSlash(f)))
			if err != nil {
				continue
			}
			if old, ok := seen[f]; ok && !st.ModTime().Equal(old) {
				changed = true
			}
			seen[f] = st.ModTime()
		}
		if changed {
			a.pv.broadcast("reload")
		}
	}
}

// Comments lists the pins (phone and shared pages), newest first.
func (a *App) Comments() []Comment {
	a.pv.mu.Lock()
	cs := append([]Comment(nil), a.pv.state.Comments...)
	site := a.pv.state.SiteID
	a.pv.mu.Unlock()
	if site != "" {
		cs = append(cs, a.sharedComments(site)...)
	}
	for i, j := 0, len(cs)-1; i < j; i, j = i+1, j-1 {
		cs[i], cs[j] = cs[j], cs[i]
	}
	return cs
}

func (a *App) ResolveComment(id string) []Comment {
	a.pv.mu.Lock()
	for i := range a.pv.state.Comments {
		if a.pv.state.Comments[i].ID == id {
			a.pv.state.Comments[i].Resolved = true
		}
	}
	a.pv.saveLocked()
	a.pv.mu.Unlock()
	return a.Comments()
}

// ---------------- 10. one-click ship ----------------

func cloudBase() string { return cloud.BaseURL(config.Dir()) }

func (a *App) sharedComments(site string) []Comment {
	resp, err := webClient.Get(cloudBase() + "/v1/comments/" + site)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	var cs []Comment
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&cs)
	for i := range cs {
		cs[i].From = "share"
	}
	return cs
}

// ShipInfo is what the Ship menu shows.
type ShipInfo struct {
	GitHub   bool      `json:"github"` // the folder has a GitHub remote
	Repo     string    `json:"repo,omitempty"`
	Node     bool      `json:"node"` // npx is there (Cloudflare)
	Stack    string    `json:"stack"`
	Shipped  []Shipped `json:"shipped"`
	Comments bool      `json:"comments"`
}

func (a *App) ShipInfo() ShipInfo {
	folder := a.prefs.Folder
	info := ShipInfo{Stack: studio.DetectStack(folder).Describe(), Comments: a.prefs.Studio.comments()}
	if out, err := studio.ExecRunner(func(c *exec.Cmd) { quiet(c) })(context.Background(), folder, "git", "remote", "get-url", "origin"); err == nil {
		if o, r, ok := studio.GitHubRepo(out); ok {
			info.GitHub, info.Repo = true, o+"/"+r
		}
	}
	_, err := exec.LookPath("npx")
	info.Node = err == nil
	a.pv.mu.Lock()
	info.Shipped = append(info.Shipped, a.pv.state.Shipped...)
	a.pv.mu.Unlock()
	return info
}

var shipping sync.Mutex

// Ship publishes the project's site. Progress comes as "ship" events.
func (a *App) Ship(provider string) string {
	if !shipping.TryLock() {
		return "Already publishing."
	}
	folder := a.prefs.Folder
	if folder == "" {
		shipping.Unlock()
		return "Open a project folder first."
	}
	go func() {
		defer shipping.Unlock()
		say := func(stage, line string) { a.emit("ship", map[string]any{"stage": stage, "line": line}) }
		fail := func(err error) { a.emit("ship", map[string]any{"stage": "error", "line": err.Error()}) }
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		run := studio.ExecRunner(func(c *exec.Cmd) { quiet(c) })
		st := studio.DetectStack(folder)
		dest, err := os.MkdirTemp("", "blaze-ship-")
		if err != nil {
			fail(err)
			return
		}
		defer os.RemoveAll(dest)
		if st.Build != "" && st.Framework != "html" {
			say("build", "Building with npm run build…")
		} else {
			say("build", "Collecting the site's files…")
		}
		n, err := studio.Stage(ctx, folder, dest, st, run)
		if err != nil {
			fail(err)
			return
		}
		a.pv.mu.Lock()
		if a.pv.state.SiteID == "" {
			a.pv.state.SiteID = randHex(8)
			a.pv.saveLocked()
		}
		site := a.pv.state.SiteID
		head := a.pv.state.Head
		a.pv.mu.Unlock()
		if a.prefs.Studio.comments() {
			_ = studio.InjectComments(dest, cloudBase(), site)
		}
		var url string
		switch provider {
		case "github":
			say("deploy", fmt.Sprintf("Pushing %d files to the gh-pages branch…", n))
			url, err = studio.ShipGitHub(ctx, folder, dest, run)
		default:
			provider = "cloudflare"
			say("deploy", fmt.Sprintf("Uploading %d files to Cloudflare Pages (a browser may open to sign in)…", n))
			url, err = studio.ShipCloudflare(ctx, dest, studio.Slug(filepath.Base(folder)), run)
		}
		if err != nil {
			fail(err)
			return
		}
		a.pv.mu.Lock()
		a.pv.state.Shipped = append([]Shipped{{Provider: provider, URL: url, When: time.Now().Format("2 Jan 15:04"), Version: head}}, a.pv.state.Shipped...)
		if len(a.pv.state.Shipped) > 20 {
			a.pv.state.Shipped = a.pv.state.Shipped[:20]
		}
		a.pv.saveLocked()
		a.pv.mu.Unlock()
		line := "Live at " + url
		if provider == "github" {
			line += " (GitHub Pages can take a minute the first time)"
		}
		a.emit("ship", map[string]any{"stage": "done", "line": line, "url": url})
	}()
	return "ok"
}

// ---------------- studio state for the UI ----------------

// StudioView is everything the studio panels show at once.
type StudioView struct {
	Brand    studio.Brand `json:"brand"`
	Prefs    StudioPrefs  `json:"prefs"`
	Stack    string       `json:"stack"`
	Head     int          `json:"head"`
	Phone    bool         `json:"phone"`
	Comments int          `json:"comments"` // unresolved local pins
}

func (a *App) Studio() StudioView {
	a.pv.mu.Lock()
	v := StudioView{Brand: a.pv.state.Brand, Head: a.pv.state.Head, Phone: a.pv.lan != nil}
	for _, c := range a.pv.state.Comments {
		if !c.Resolved {
			v.Comments++
		}
	}
	a.pv.mu.Unlock()
	v.Prefs = a.prefs.Studio
	v.Stack = studio.DetectStack(a.prefs.Folder).Describe()
	return v
}

// startStudio wires the preview server to the app (called from startup).
func (a *App) startStudio() {
	tasteMu.Lock()
	tasteOff = a.prefs.Studio.NoTaste
	tasteMu.Unlock()
	a.pv.mu.Lock()
	a.pv.dirPath = a.dirFolder
	a.pv.onNote = func(c Comment) { a.emit("comment", c) }
	a.pv.mu.Unlock()
	go a.watchDesign()
}
