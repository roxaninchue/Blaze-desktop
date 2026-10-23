package media

// Serving media to the webview. Handler answers:
//
//	/media/file?k=<key>          a library file, with Range (seeking, big videos)
//	/media/art?k=<key>           a track's cover (file), or a redirect to its https url
//	/media/sub?v=<key>&id=<id>   a subtitle track as WebVTT (saved, or a sidecar .srt)
//	/media/s/<id>                a Store stream, proxied with Range and the source's
//	                             user-agent (stream urls are tied to it, and WebAudio
//	                             needs same-origin or CORS to hear them)
//
// It is mounted on the Wails asset server (AssetServer.Handler) and on a loopback
// server with a random token (127.0.0.1 only); the player uses the loopback one
// because WebKitGTK and WKWebView stream <video> more reliably over http than over a
// custom scheme. Only paths the library knows are served, never an arbitrary file.

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type streamEntry struct {
	URL, UA string
	Mime    string
	Expires time.Time
}

type streamTable struct {
	mu sync.Mutex
	m  map[string]streamEntry
}

func newStreamTable() *streamTable { return &streamTable{m: map[string]streamEntry{}} }

func (s *streamTable) add(e streamEntry) string {
	b := make([]byte, 9)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.Expires) {
			delete(s.m, k)
		}
	}
	if e.Expires.IsZero() {
		e.Expires = now.Add(8 * time.Hour)
	}
	s.m[id] = e
	return id
}

func (s *streamTable) get(id string) (streamEntry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[id]
	return e, ok && time.Now().Before(e.Expires)
}

// AddStream registers a stream url for /media/s/<id> and returns that path.
func (m *Media) AddStream(u, ua, mime string) string {
	return "/media/s/" + m.streams.add(streamEntry{URL: u, UA: ua, Mime: mime})
}

// Handler serves /media/*. token != "" requires ?t=token (the loopback server).
func (m *Media) Handler(token string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/media/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Range")
		w.Header().Set("Access-Control-Expose-Headers", "Content-Range, Content-Length, Accept-Ranges")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if token != "" && r.URL.Query().Get("t") != token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		q := r.URL.Query()
		switch {
		case r.URL.Path == "/media/file":
			t, ok := m.Find(q.Get("k"))
			if !ok || t.Path == "" {
				http.NotFound(w, r)
				return
			}
			serveFile(w, r, t.Path, mimeOf(t.Path))
		case r.URL.Path == "/media/art":
			t, ok := m.Find(q.Get("k"))
			if !ok || t.Art == "" {
				http.NotFound(w, r)
				return
			}
			if strings.HasPrefix(t.Art, "http") {
				http.Redirect(w, r, t.Art, http.StatusFound)
				return
			}
			w.Header().Set("Cache-Control", "max-age=3600")
			serveFile(w, r, t.Art, "")
		case r.URL.Path == "/media/sub":
			m.serveSub(w, r, q.Get("v"), q.Get("id"))
		case strings.HasPrefix(r.URL.Path, "/media/s/"):
			m.proxy(w, r, strings.TrimPrefix(r.URL.Path, "/media/s/"))
		default:
			http.NotFound(w, r)
		}
	})
}

func mimeOf(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".mp3":
		return "audio/mpeg"
	case ".m4a", ".aac":
		return "audio/mp4"
	case ".flac":
		return "audio/flac"
	case ".ogg", ".oga", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".mov":
		return "video/quicktime"
	case ".ogv":
		return "video/ogg"
	case ".vtt":
		return "text/vtt; charset=utf-8"
	}
	return ""
}

func serveFile(w http.ResponseWriter, r *http.Request, p, mime string) {
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		http.NotFound(w, r)
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	http.ServeContent(w, r, filepath.Base(p), st.ModTime(), f)
}

func (m *Media) serveSub(w http.ResponseWriter, r *http.Request, videoKey, id string) {
	var list []Subtitle
	list = append(list, m.SavedSubs(videoKey)...)
	if t, ok := m.Find(videoKey); ok && t.Path != "" {
		list = append(list, SidecarSubs(t.Path)...)
	}
	for _, s := range list {
		if s.ID != id {
			continue
		}
		b, err := os.ReadFile(s.URL)
		if err != nil {
			break
		}
		text := DecodeText(b)
		if !strings.HasPrefix(strings.TrimPrefix(strings.TrimSpace(text), "\uFEFF"), "WEBVTT") {
			text = ToVtt(ParseSubs(text))
		}
		w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
		_, _ = io.WriteString(w, text)
		return
	}
	http.NotFound(w, r)
}

func (m *Media) proxy(w http.ResponseWriter, r *http.Request, id string) {
	e, ok := m.streams.get(id)
	if !ok {
		http.Error(w, "This stream link expired. Open it again from the Store.", http.StatusGone)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", e.URL, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	req.Header.Set("User-Agent", orStr(e.UA, webUA))
	if rg := r.Header.Get("Range"); rg != "" {
		req.Header.Set("Range", rg)
	}
	resp, err := (&http.Client{}).Do(req) // no timeout: a stream can run for an hour
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	for _, h := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	if ct := resp.Header.Get("Content-Type"); e.Mime != "" && (ct == "" || strings.HasPrefix(ct, "application/octet")) {
		w.Header().Set("Content-Type", e.Mime)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// Server is the loopback server; Base is "http://127.0.0.1:<port>" and Token its key.
type Server struct {
	Base, Token string
	ln          net.Listener
}

// Listen starts the loopback server.
func (m *Media) Listen() (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	s := &Server{Base: "http://" + ln.Addr().String(), Token: hex.EncodeToString(b), ln: ln}
	srv := &http.Server{Handler: m.Handler(s.Token), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	return s, nil
}

// Close stops the loopback server.
func (s *Server) Close() {
	if s != nil && s.ln != nil {
		_ = s.ln.Close()
	}
}
