package media

// The Store (mobile storeapi/StoreApi.kt + StoreSources.kt): several sources behind
// one interface. Every source turns a search into Items and an item into Formats /
// Choices; the download queue and the views only see these types. Same sources as
// mobile: YouTube (built in, InnerTube), Jamendo (needs a free client_id), Internet
// Archive audio (CC / public domain), public-domain feature films, the Blender open
// movies, and "Download by link".

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// Item is one search result.
type Item struct {
	Source       string            `json:"source"`
	ID           string            `json:"id"`
	Title        string            `json:"title"`
	Author       string            `json:"author"`
	DurationSec  int               `json:"duration_sec"`
	DurationText string            `json:"duration_text"`
	Thumb        string            `json:"thumb"`
	Licence      string            `json:"licence"`
	LicenceURL   string            `json:"licence_url"`
	Views        string            `json:"views"`
	Square       bool              `json:"square"`
	Extra        map[string]string `json:"extra,omitempty"`
}

// Page is one page of results.
type Page struct {
	Items []Item `json:"items"`
	Next  string `json:"next"`
}

// Format is one downloadable file or stream of an item.
type Format struct {
	Key           string `json:"key"`
	URL           string `json:"url"`
	Mime          string `json:"mime"`
	Codecs        string `json:"codecs"`
	Bitrate       int    `json:"bitrate"`
	ContentLength int64  `json:"content_length"`
	Height        int    `json:"height"`
	QualityLabel  string `json:"quality_label"`
	HasAudio      bool   `json:"has_audio"`
	HasVideo      bool   `json:"has_video"`
	FPS           int    `json:"fps"`
	Name          string `json:"name"`
}

// Container is "mp4", "webm", "mp3"…
func (f Format) Container() string {
	c := f.Mime
	if i := strings.Index(c, "/"); i >= 0 {
		c = c[i+1:]
	}
	if i := strings.Index(c, ";"); i >= 0 {
		c = c[:i]
	}
	if c == "mpeg" {
		return "mp3"
	}
	return c
}
func (f Format) Muxed() bool { return f.HasAudio && f.HasVideo }
func (f Format) Kbps() int   { return (f.Bitrate + 500) / 1000 }

// Detail is an item with its formats.
type Detail struct {
	ID         string     `json:"id"`
	Title      string     `json:"title"`
	Author     string     `json:"author"`
	LengthSec  int        `json:"length_sec"`
	Thumb      string     `json:"thumb"`
	Formats    []Format   `json:"formats"`
	Licence    string     `json:"licence"`
	LicenceURL string     `json:"licence_url"`
	Subtitles  []Subtitle `json:"subtitles,omitempty"`
}

// Choice is one Download option: one stream, or video + audio merged.
type Choice struct {
	ID     string   `json:"id"`
	Label  string   `json:"label"`
	Detail string   `json:"detail"`
	Keys   []string `json:"keys"`
	Ext    string   `json:"ext"`
	Bytes  int64    `json:"bytes"`
	Height int      `json:"height"`
	Kbps   int      `json:"kbps"`
	Title  string   `json:"title"`
}

// Source is one Store source.
type Source interface {
	ID() string
	Name() string
	Kinds() []string
	SetupNeeded() string // "" when ready
	Browsable() bool
	UserAgent() string
	Search(kind, query, next string) (Page, error)
	Detail(it Item, kind string) (Detail, error)
	Choices(kind string, d Detail) []Choice
	DefaultChoice(kind string, c []Choice) *Choice
	Refresh(itemID, kind string) (*Detail, error) // nil = urls don't expire
}

// StreamPick is the streams to play right away.
func StreamPick(s Source, kind string, d Detail) []Format {
	c := s.DefaultChoice(kind, s.Choices(kind, d))
	if c == nil {
		return nil
	}
	return formatsFor(d, c.Keys)
}

func formatsFor(d Detail, keys []string) []Format {
	var out []Format
	for _, k := range keys {
		for _, f := range d.Formats {
			if f.Key == k {
				out = append(out, f)
				break
			}
		}
	}
	return out
}

// StoreError is a plain message for the screen.
type StoreError struct {
	Msg  string
	Code int
}

func (e *StoreError) Error() string { return e.Msg }
func serr(f string, a ...any) error { return &StoreError{Msg: fmt.Sprintf(f, a...)} }

const blazeUA = "Blaze/1 (Desktop; +https://blazeinchue.pages.dev)"

var client = &http.Client{Timeout: 30 * time.Second}

// httpGet fetches text (at most 4 MB).
func httpGet(u, ua string) (string, error) {
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", orStr(ua, blazeUA))
	req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.5")
	r, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if r.StatusCode < 200 || r.StatusCode > 299 {
		return string(b), &StoreError{Msg: fmt.Sprintf("The server answered %d", r.StatusCode), Code: r.StatusCode}
	}
	return string(b), nil
}

// LicenceLabel turns a licence url into "CC BY 4.0", "Public domain"…
func LicenceLabel(u string) string {
	l := strings.ToLower(strings.TrimSpace(u))
	if l == "" {
		return ""
	}
	if strings.Contains(l, "publicdomain/zero") {
		return "CC0 (public domain)"
	}
	if strings.Contains(l, "publicdomain") {
		return "Public domain"
	}
	m := reCC.FindStringSubmatch(l)
	if m == nil {
		if strings.Contains(l, "creativecommons") {
			return "Creative Commons"
		}
		return u
	}
	return "CC " + strings.ToUpper(m[1]) + " " + m[2]
}

var reCC = regexp.MustCompile(`creativecommons\.org/licenses/([a-z\-]+)/(\d(?:\.\d)?)`)

// LicenceFree: only public domain and Creative Commons count as free to keep.
func LicenceFree(u string) bool {
	l := strings.ToLower(u)
	return strings.Contains(l, "creativecommons.org") || strings.Contains(l, "publicdomain")
}

// ClockText: 3725000 ms → "1:02:05", 65000 → "1:05".
func ClockText(ms int64) string { return Mmss(ms) }

// ParseClock: "1:02:05" / "4:13" / "45" → seconds; -1 when it isn't a clock.
func ParseClock(s string) int {
	parts := strings.Split(strings.TrimSpace(s), ":")
	if len(parts) == 0 || len(parts) > 3 {
		return -1
	}
	n := 0
	for _, p := range parts {
		if p == "" || !allDigits(p) {
			return -1
		}
		v := 0
		for _, c := range p {
			v = v*10 + int(c-'0')
		}
		n = n*60 + v
	}
	return n
}

// SizeText: bytes → "4.1 MB".
func SizeText(b int64) string {
	switch {
	case b <= 0:
		return ""
	case b >= 1_000_000_000:
		return fmt.Sprintf("%.1f GB", float64(b)/1e9)
	case b >= 1_000_000:
		return fmt.Sprintf("%.1f MB", float64(b)/1e6)
	}
	return fmt.Sprintf("%d KB", b/1000)
}

// SourceInfo is a source as the Store view lists it.
type SourceInfo struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Kinds     []string `json:"kinds"`
	Setup     string   `json:"setup"`
	Browsable bool     `json:"browsable"`
}
