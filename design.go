package main

import (
	"bytes"
	"crypto/sha1"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"io"
	"mime"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/studio"
)

//go:embed picker.js
var pickerJS []byte

// Design state for the current project folder: what to preview and the saved versions.
type DesignTarget struct {
	Kind string `json:"kind"` // "file" or "url"
	Path string `json:"path"` // file: path relative to the folder
	URL  string `json:"url"`  // url: a local dev server
}

type Version struct {
	N       int               `json:"n"`
	Summary string            `json:"summary"`
	When    string            `json:"when"`
	Target  DesignTarget      `json:"target"`
	Files   map[string]string `json:"files,omitempty"`  // rel path -> content, every tracked design file at that moment
	Parent  int               `json:"parent,omitempty"` // the version it was made from (the timeline branches on restore)
	Kind    string            `json:"kind,omitempty"`   // chat | edit | restore | fix | mix
	Card    *studio.Card      `json:"card,omitempty"`   // the critic's score card
}

type DesignState struct {
	Target   DesignTarget `json:"target"`
	Versions []Version    `json:"versions"`
	Tracked  []string     `json:"tracked"`
	Head     int          `json:"head,omitempty"` // the version the live files are (new versions branch from it)
	Mode     string       `json:"mode,omitempty"` // the studio mode last used here: page | 3d | motion | audio (design_studio.go)

	Brand    studio.Brand `json:"brand"`              // the project's brand kit (studio_app.go)
	SiteID   string       `json:"site_id,omitempty"`  // shared-comments id for shipped pages
	Comments []Comment    `json:"comments,omitempty"` // pinned comments from the phone preview
	Shipped  []Shipped    `json:"shipped,omitempty"`  // where it was published
}

// Comment is a pin left on the preview (phone tap-to-comment or a shared page).
type Comment struct {
	ID       string  `json:"id"`
	Sel      string  `json:"sel"`
	Text     string  `json:"text"`
	X        float64 `json:"x"` // where on the page, 0..1 of its width
	Y        float64 `json:"y"` // px from the top of the page
	Path     string  `json:"path,omitempty"`
	Who      string  `json:"who,omitempty"`
	When     string  `json:"when"`
	Resolved bool    `json:"resolved,omitempty"`
	From     string  `json:"from,omitempty"` // phone | share
}

// Shipped is one publish.
type Shipped struct {
	Provider string `json:"provider"`
	URL      string `json:"url"`
	When     string `json:"when"`
	Version  int    `json:"version,omitempty"`
}

type preview struct {
	mu     sync.Mutex
	port   int
	folder string
	state  DesignState

	live    map[chan string]bool // hot-reload listeners (/__blaze/live)
	lan     net.Listener         // the phone preview server, when on
	lanPort int
	token   string                     // the phone preview's secret
	dirPath func(handle string) string // a Directions branch's folder (studio_app.go)
	onNote  func(c Comment)            // a new comment arrived
}

var designExt = regexp.MustCompile(`(?i)\.(html?|css|scss|svg|jsx|tsx|vue|svelte)$`)
var localURL = regexp.MustCompile(`https?://(?:localhost|127\.0\.0\.1|0\.0\.0\.0):(\d{2,5})[^\s"'\]]*`)

func designDir(folder string) string {
	h := sha1.Sum([]byte(folder))
	d := filepath.Join(config.Dir(), "desktop", "design", hex.EncodeToString(h[:6]))
	_ = os.MkdirAll(d, 0o700)
	return d
}

func (p *preview) load(folder string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.folder = folder
	p.state = DesignState{}
	b, _ := os.ReadFile(filepath.Join(designDir(folder), "state.json"))
	_ = json.Unmarshal(b, &p.state)
}

func (p *preview) saveLocked() {
	b, _ := json.Marshal(p.state)
	_ = os.WriteFile(filepath.Join(designDir(p.folder), "state.json"), b, 0o600)
}

// start runs the preview server on a free local port.
func (p *preview) start() {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return
	}
	p.port = ln.Addr().(*net.TCPAddr).Port
	go http.Serve(ln, http.HandlerFunc(p.serve))
}

func (p *preview) base() string { return "http://localhost:" + strconv.Itoa(p.port) }

// versionFor reads ?__v=N from the page itself or, for its assets, from the Referer.
func versionFor(r *http.Request) int {
	if v := r.URL.Query().Get("__v"); v != "" {
		n, _ := strconv.Atoi(v)
		return n
	}
	if ref, err := url.Parse(r.Referer()); err == nil {
		n, _ := strconv.Atoi(ref.Query().Get("__v"))
		return n
	}
	return 0
}

func (p *preview) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.URL.Path == "/__blaze/picker.js" {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(pickerJS)
		return
	}
	if p.special(w, r) {
		return
	}
	p.mu.Lock()
	st := p.state
	folder := p.folder
	p.mu.Unlock()
	// A Directions branch: /__dir/<handle>/<path> serves from that specialist's copy.
	if strings.HasPrefix(r.URL.Path, "/__dir/") && p.dirPath != nil {
		rest := strings.TrimPrefix(r.URL.Path, "/__dir/")
		h, sub, _ := strings.Cut(rest, "/")
		if d := p.dirPath(h); d != "" {
			folder = d
			r2 := r.Clone(r.Context())
			r2.URL.Path = "/" + sub
			r = r2
			st.Versions = nil
			st.Target = DesignTarget{Kind: "file", Path: st.Target.Path}
		} else {
			http.Error(w, "that direction is gone", 404)
			return
		}
	}

	if st.Target.Kind == "url" && st.Target.URL != "" {
		p.proxy(w, r, st.Target.URL)
		return
	}
	rel := strings.TrimPrefix(filepath.Clean("/"+r.URL.Path), "/")
	if rel == "" || rel == "." {
		rel = st.Target.Path
	}
	var data []byte
	if n := versionFor(r); n > 0 {
		for _, v := range st.Versions {
			if v.N == n {
				if c, ok := v.Files[filepath.ToSlash(rel)]; ok {
					data = []byte(c)
				}
			}
		}
	}
	if data == nil {
		full := filepath.Join(folder, filepath.FromSlash(rel))
		if !strings.HasPrefix(full, folder) {
			http.Error(w, "outside the project", 403)
			return
		}
		if st, err := os.Stat(full); err == nil && st.IsDir() {
			full = filepath.Join(full, "index.html")
		}
		b, err := os.ReadFile(full)
		if err != nil {
			http.Error(w, "not found", 404)
			return
		}
		data = b
		rel = full
	}
	ct := mime.TypeByExtension(filepath.Ext(rel))
	if ct == "" {
		ct = http.DetectContentType(data)
	}
	w.Header().Set("Content-Type", ct)
	if strings.HasPrefix(ct, "text/html") {
		data = inject(data)
	} else if strings.HasPrefix(ct, "image/svg") && r.URL.Query().Get("wrap") == "1" {
		// An SVG opened as the page: wrap it so it can be selected and edited too.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		data = inject([]byte("<!doctype html><html><body style=\"margin:0;display:grid;place-items:center;min-height:100vh\">" + string(data) + "</body></html>"))
	}
	w.Write(data)
}

func inject(html []byte) []byte {
	tag := []byte(`<script src="/__blaze/picker.js"></script>`)
	if i := bytes.LastIndex(bytes.ToLower(html), []byte("</body>")); i >= 0 {
		return append(append(append([]byte{}, html[:i]...), tag...), html[i:]...)
	}
	return append(html, tag...)
}

func (p *preview) proxy(w http.ResponseWriter, r *http.Request, target string) {
	u, err := url.Parse(target)
	if err != nil {
		http.Error(w, "bad url", 500)
		return
	}
	rp := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: u.Scheme, Host: u.Host})
	orig := rp.Director
	rp.Director = func(req *http.Request) {
		orig(req)
		req.Host = u.Host
		req.Header.Del("Accept-Encoding")
		if req.URL.Path == "/" && u.Path != "" && u.Path != "/" {
			req.URL.Path = u.Path
		}
	}
	rp.ModifyResponse = func(resp *http.Response) error {
		if !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/html") {
			return nil
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return err
		}
		b = inject(b)
		resp.Body = io.NopCloser(bytes.NewReader(b))
		resp.ContentLength = int64(len(b))
		resp.Header.Set("Content-Length", strconv.Itoa(len(b)))
		resp.Header.Del("Content-Security-Policy")
		return nil
	}
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		w.Write([]byte(`<body style="font:14px system-ui;padding:40px;color:#555">The dev server at ` + target + ` isn't answering yet. It may still be starting.</body>`))
	}
	rp.ServeHTTP(w, r)
}

// threeD reports whether the previewed page is a 3D mockup (a Three.js page built from a
// blaze-parts block, see the three-d-design skill). Those open in Design on their own
// unless the user chose Never, since picking and editing parts is the point of them.
func (p *preview) threeD(st DesignState) bool {
	if st.Target.Kind != "file" || st.Target.Path == "" {
		return false
	}
	p.mu.Lock()
	full := filepath.Join(p.folder, filepath.FromSlash(st.Target.Path))
	p.mu.Unlock()
	b, err := os.ReadFile(full)
	return err == nil && len(b) < 4<<20 && studio.HasParts(string(b))
}

// noteTurn records a version when the turn changed design files or found a dev server.
func (p *preview) noteTurn(changed []string, urls []string, summary string) (bool, DesignState) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(changed) == 0 && len(urls) == 0 {
		return false, p.state
	}
	if len(urls) > 0 {
		p.state.Target = DesignTarget{Kind: "url", URL: urls[len(urls)-1]}
	}
	for _, c := range changed {
		if !containsStr(p.state.Tracked, c) {
			p.state.Tracked = append(p.state.Tracked, c)
		}
		low := strings.ToLower(c)
		if p.state.Target.Kind != "url" && (strings.HasSuffix(low, ".html") || strings.HasSuffix(low, ".htm") || (strings.HasSuffix(low, ".svg") && !strings.HasSuffix(strings.ToLower(p.state.Target.Path), ".html"))) {
			p.state.Target = DesignTarget{Kind: "file", Path: c}
		}
	}
	if p.state.Target.Kind == "" {
		// Only CSS changed with no page yet: preview the folder's index.html if there is one.
		if _, err := os.Stat(filepath.Join(p.folder, "index.html")); err == nil {
			p.state.Target = DesignTarget{Kind: "file", Path: "index.html"}
		}
	}
	kind := "chat"
	switch {
	case strings.HasPrefix(summary, "[Critic fix]"):
		kind, summary = "fix", "Critic fix: "+strings.TrimSpace(strings.TrimPrefix(summary, "[Critic fix]"))
	case strings.HasPrefix(summary, "[Directions mix]"):
		kind, summary = "mix", "Mixed directions: "+strings.TrimSpace(strings.TrimPrefix(summary, "[Directions mix]"))
	}
	p.recordLocked(summary, kind)
	return true, p.state
}

// recordLocked saves the tracked files as a new version on top of Head.
func (p *preview) recordLocked(summary, kind string) Version {
	files := map[string]string{}
	for _, t := range p.state.Tracked {
		if b, err := os.ReadFile(filepath.Join(p.folder, filepath.FromSlash(t))); err == nil && len(b) < 2<<20 {
			files[t] = string(b)
		}
	}
	n := 1
	if len(p.state.Versions) > 0 {
		n = p.state.Versions[len(p.state.Versions)-1].N + 1
	}
	summary = strings.TrimSpace(summary)
	if len(summary) > 90 {
		summary = summary[:90] + "…"
	}
	parent := p.state.Head
	if parent == 0 && len(p.state.Versions) > 0 {
		parent = p.state.Versions[len(p.state.Versions)-1].N
	}
	v := Version{N: n, Summary: summary, When: time.Now().Format("15:04"), Target: p.state.Target, Files: files, Parent: parent, Kind: kind}
	if kind == "fix" {
		if pv := p.versionLocked(parent); pv != nil && pv.Card != nil {
			pv.Card.Fixed = true
			v.Card = &studio.Card{FixOf: parent}
		}
	}
	p.state.Versions = append(p.state.Versions, v)
	if len(p.state.Versions) > 60 {
		p.state.Versions = p.state.Versions[len(p.state.Versions)-60:]
	}
	p.state.Head = n
	learnTaste(p.state.Versions)
	p.saveLocked()
	go p.broadcast("reload")
	return v
}

func (p *preview) versionLocked(n int) *Version {
	for i := range p.state.Versions {
		if p.state.Versions[i].N == n {
			return &p.state.Versions[i]
		}
	}
	return nil
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// ---------- bound API ----------

type DesignView struct {
	Base     string         `json:"base"`
	Target   DesignTarget   `json:"target"`
	Versions []Version      `json:"versions"`           // without file bodies
	Files    []string       `json:"files"`              // previewable files in the project
	Tracked  []string       `json:"tracked"`            // files this project's chats changed (the Current tab)
	Head     int            `json:"head"`               // the version the live files are
	Artboard *studio.Format `json:"artboard,omitempty"` // the previewed file's fixed frame (v0.9)
	Scratch  bool           `json:"scratch,omitempty"`  // the folder is a scratch design
	Mode     string         `json:"mode"`               // the studio mode (design_studio.go)
}

func (a *App) Design() DesignView {
	a.pv.mu.Lock()
	st := a.pv.state
	a.pv.mu.Unlock()
	v := DesignView{Base: a.pv.base(), Target: st.Target, Tracked: st.Tracked, Head: st.Head, Mode: normStudioMode(st.Mode)}
	for _, x := range st.Versions {
		x.Files = nil
		v.Versions = append(v.Versions, x)
	}
	for _, f := range walkFiles(a.prefs.Folder, 5000) {
		l := strings.ToLower(f.rel)
		if strings.HasSuffix(l, ".html") || strings.HasSuffix(l, ".htm") || strings.HasSuffix(l, ".svg") {
			v.Files = append(v.Files, f.rel)
		}
	}
	// Never leave the picker blank: with no target yet, preview index.html (or the first page).
	if v.Target.Kind == "" && len(v.Files) > 0 {
		pick := v.Files[0]
		for _, f := range v.Files {
			if strings.EqualFold(filepath.Base(f), "index.html") {
				pick = f
				break
			}
		}
		a.pv.mu.Lock()
		if a.pv.state.Target.Kind == "" {
			a.pv.state.Target = DesignTarget{Kind: "file", Path: pick}
			a.pv.saveLocked()
		}
		v.Target = a.pv.state.Target
		a.pv.mu.Unlock()
	}
	if f, ok := a.artboard(); ok {
		v.Artboard = &f
	}
	if rel, err := filepath.Rel(designsDir(), a.prefs.Folder); err == nil && !strings.HasPrefix(rel, "..") && rel != "." {
		v.Scratch = true
	}
	return v
}

// SetDesignTarget picks what to preview: a project file or a local URL.
func (a *App) SetDesignTarget(kind, value string) DesignView {
	a.pv.mu.Lock()
	if kind == "url" {
		if !strings.HasPrefix(value, "http") {
			value = "http://" + value
		}
		a.pv.state.Target = DesignTarget{Kind: "url", URL: value}
	} else {
		a.pv.state.Target = DesignTarget{Kind: "file", Path: value}
	}
	a.pv.saveLocked()
	a.pv.mu.Unlock()
	return a.Design()
}

// RestoreVersion writes a version's files back into the project.
func (a *App) RestoreVersion(n int) string {
	a.pv.mu.Lock()
	defer a.pv.mu.Unlock()
	for _, v := range a.pv.state.Versions {
		if v.N == n {
			if cur := a.pv.versionLocked(a.pv.state.Head); cur != nil && cur.N != n {
				tasteUndo(joinFiles(cur.Files), joinFiles(v.Files))
			}
			for rel, c := range v.Files {
				full := filepath.Join(a.pv.folder, filepath.FromSlash(rel))
				_ = os.MkdirAll(filepath.Dir(full), 0o755)
				if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
					return "Couldn't restore " + rel + ": " + err.Error()
				}
			}
			a.pv.state.Target = v.Target
			a.pv.state.Head = n
			a.pv.saveLocked()
			go a.pv.broadcast("reload")
			return "ok"
		}
	}
	return "That version is gone."
}

func (a *App) ClearVersions() {
	a.pv.mu.Lock()
	a.pv.state.Versions = nil
	a.pv.saveLocked()
	a.pv.mu.Unlock()
}

// OpenPreview pops the preview out to the system browser.
func (a *App) OpenPreview(version int) {
	u := a.pv.base() + "/"
	if a.pv.state.Target.Kind == "file" {
		u += a.pv.state.Target.Path
	}
	if version > 0 {
		u += "?__v=" + strconv.Itoa(version)
	}
	a.OpenURL(u)
}
