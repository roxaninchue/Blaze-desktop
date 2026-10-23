package media

// Subtitles (mobile Subs.kt + SubsLoader.kt): YouTube caption tracks, WebVTT, SubRip,
// YouTube json3 and srv1/srv3 XML parsed into cues and written back as one clean
// WebVTT per track under ~/.blaze/media/subs/<video>/ (index.json lists them), so a
// video gets its subtitles back next time, offline too. The player loads them as
// <track> elements served by Handler.

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Cue is one subtitle.
type Cue struct {
	StartMs int64  `json:"s"`
	EndMs   int64  `json:"e"`
	Text    string `json:"t"`
}

// Subtitle is one track a source offers or Blaze saved.
type Subtitle struct {
	ID   string `json:"id"`
	URL  string `json:"url"` // remote url, or a local file path once saved
	Lang string `json:"lang"`
	Name string `json:"name"`
	Auto bool   `json:"auto"`
}

// YtTracks reads captions.playerCaptionsTracklistRenderer.captionTracks.
func YtTracks(player map[string]any) []Subtitle {
	arr, _ := anyPath(player, "captions", "playerCaptionsTracklistRenderer", "captionTracks").([]any)
	var out []Subtitle
	seen := map[string]bool{}
	for _, x := range arr {
		t, _ := x.(map[string]any)
		u := str(t["baseUrl"])
		if u == "" {
			continue
		}
		lang := str(t["languageCode"])
		vss := str(t["vssId"])
		auto := str(t["kind"]) == "asr" || strings.HasPrefix(vss, "a.")
		name := orStr(runs(t["name"]), lang)
		if vss == "" {
			if auto {
				vss = "a." + lang
			} else {
				vss = "." + lang
			}
		}
		id := "yt-" + reSafe.ReplaceAllString(vss, "_")
		if !seen[id] {
			seen[id] = true
			out = append(out, Subtitle{id, strings.ReplaceAll(u, `\u0026`, "&"), lang, name, auto})
		}
	}
	return out
}

func anyPath(m map[string]any, path ...string) any {
	var cur any = m
	for _, p := range path {
		o, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = o[p]
	}
	return cur
}

var reSafe = regexp.MustCompile(`[^A-Za-z0-9._-]`)
var reFmt = regexp.MustCompile(`([?&])fmt=[^&]*&?`)

// SubWithFormat asks a caption url for fmt (json3 | vtt | srv1).
func SubWithFormat(u, fmtS string) string {
	base := strings.TrimRight(reFmt.ReplaceAllString(u, "$1"), "&?")
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	return base + sep + "fmt=" + fmtS
}

// ParseSubs reads any subtitle text; empty when it's none of the formats.
func ParseSubs(text string) []Cue {
	t := strings.TrimPrefix(text, "\uFEFF")
	t = strings.ReplaceAll(strings.ReplaceAll(t, "\r\n", "\n"), "\r", "\n")
	t = strings.TrimLeft(t, " \t\n")
	switch {
	case strings.HasPrefix(t, "{"):
		return parseJSON3(t)
	case strings.HasPrefix(t, "WEBVTT"):
		return parseVtt(t)
	case strings.HasPrefix(t, "<"):
		return parseXML(t)
	}
	if c := parseSrt(t); len(c) > 0 {
		return c
	}
	return parseVtt("WEBVTT\n\n" + t)
}

var reClock = regexp.MustCompile(`^(?:(\d+):)?(\d{1,2}):(\d{1,2})(?:\.(\d{1,3}))?$`)

// ClockMs: "00:01:02,345", "01:02.345", "62.5" → ms; -1 when it isn't a time.
func ClockMs(s string) int64 {
	t := strings.ReplaceAll(strings.TrimSpace(s), ",", ".")
	if m := reClock.FindStringSubmatch(t); m != nil {
		h, _ := strconv.ParseInt(m[1], 10, 64)
		mi, _ := strconv.ParseInt(m[2], 10, 64)
		se, _ := strconv.ParseInt(m[3], 10, 64)
		fr := (m[4] + "000")[:3]
		f, _ := strconv.ParseInt(fr, 10, 64)
		return ((h*60+mi)*60+se)*1000 + f
	}
	if v, err := strconv.ParseFloat(t, 64); err == nil {
		return int64(v * 1000)
	}
	return -1
}

var reArrow = regexp.MustCompile(`^\s*(\S+)\s+-->\s+(\S+)`)
var reBlank = regexp.MustCompile(`\n\s*\n`)

func blocks(text string) [][]string {
	var out [][]string
	for _, b := range reBlank.Split(text, -1) {
		out = append(out, strings.Split(strings.Trim(b, "\n"), "\n"))
	}
	return out
}

func parseSrt(text string) []Cue {
	var out []Cue
	for _, lines := range blocks(text) {
		for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
			lines = lines[1:]
		}
		ti := -1
		for i, l := range lines {
			if reArrow.MatchString(l) {
				ti = i
				break
			}
		}
		if ti < 0 || ti > 1 {
			continue
		}
		m := reArrow.FindStringSubmatch(lines[ti])
		a, b := ClockMs(m[1]), ClockMs(m[2])
		body := CleanSub(strings.Join(lines[ti+1:], "\n"))
		if a >= 0 && b > a && body != "" {
			out = append(out, Cue{a, b, body})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return out
}

func parseVtt(text string) []Cue {
	var out []Cue
	for _, lines := range blocks(text) {
		if len(lines) > 0 && (strings.HasPrefix(lines[0], "NOTE") || strings.HasPrefix(lines[0], "STYLE") || strings.HasPrefix(lines[0], "REGION")) {
			continue
		}
		ti := -1
		for i, l := range lines {
			if reArrow.MatchString(l) {
				ti = i
				break
			}
		}
		if ti < 0 {
			continue
		}
		m := reArrow.FindStringSubmatch(lines[ti])
		a, b := ClockMs(m[1]), ClockMs(m[2])
		body := CleanSub(strings.Join(lines[ti+1:], "\n"))
		if a >= 0 && b > a && body != "" {
			out = append(out, Cue{a, b, body})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return dedupeRolling(out)
}

func parseJSON3(text string) []Cue {
	var o struct {
		Events []struct {
			T    *int64 `json:"tStartMs"`
			D    int64  `json:"dDurationMs"`
			Segs []struct {
				U string `json:"utf8"`
			} `json:"segs"`
		} `json:"events"`
	}
	if json.Unmarshal([]byte(text), &o) != nil {
		return nil
	}
	var raw []Cue
	for _, e := range o.Events {
		if e.T == nil || e.Segs == nil {
			continue
		}
		var b strings.Builder
		for _, s := range e.Segs {
			b.WriteString(s.U)
		}
		body := CleanSub(b.String())
		if *e.T >= 0 && e.D > 0 && body != "" {
			raw = append(raw, Cue{*e.T, *e.T + e.D, body})
		}
	}
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].StartMs < raw[j].StartMs })
	return clipCues(raw)
}

var (
	reXMLText = regexp.MustCompile(`(?s)<text\b([^>]*)>(.*?)</text>`)
	reXMLP    = regexp.MustCompile(`(?s)<p\b([^>]*)>(.*?)</p>`)
	reAttr    = regexp.MustCompile(`(\w+)="([^"]*)"`)
	reTags    = regexp.MustCompile(`<[^>]*>`)
	reASS     = regexp.MustCompile(`\{\\[^}]*\}`)
	reHSpace  = regexp.MustCompile(`[ \t]+`)
)

func attrs(s string) map[string]string {
	m := map[string]string{}
	for _, a := range reAttr.FindAllStringSubmatch(s, -1) {
		m[a[1]] = a[2]
	}
	return m
}

func entities(s string) string {
	return strings.NewReplacer("&amp;#39;", "'", "&#39;", "'", "&quot;", "\"", "&lt;", "<", "&gt;", ">", "&nbsp;", " ", "&amp;", "&").Replace(s)
}

func parseXML(text string) []Cue {
	var out []Cue
	for _, m := range reXMLText.FindAllStringSubmatch(text, -1) {
		at := attrs(m[1])
		st, err := strconv.ParseFloat(at["start"], 64)
		if err != nil {
			continue
		}
		d := 2.0
		if v, err := strconv.ParseFloat(at["dur"], 64); err == nil {
			d = v
		}
		body := entities(CleanSub(m[2]))
		a := int64(st * 1000)
		dd := int64(d * 1000)
		if dd < 1 {
			dd = 1
		}
		if body != "" {
			out = append(out, Cue{a, a + dd, body})
		}
	}
	if len(out) == 0 {
		for _, m := range reXMLP.FindAllStringSubmatch(text, -1) {
			at := attrs(m[1])
			a, err := strconv.ParseInt(at["t"], 10, 64)
			if err != nil {
				continue
			}
			d := int64(2000)
			if v, err := strconv.ParseInt(at["d"], 10, 64); err == nil {
				d = v
			}
			if d < 1 {
				d = 1
			}
			body := CleanSub(entities(reTags.ReplaceAllString(m[2], "")))
			if body != "" {
				out = append(out, Cue{a, a + d, body})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].StartMs < out[j].StartMs })
	return clipCues(out)
}

// CleanSub takes tags out and tidies spaces, dropping empty lines.
func CleanSub(s string) string {
	s = reASS.ReplaceAllString(reTags.ReplaceAllString(s, ""), "")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
	var keep []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(reHSpace.ReplaceAllString(l, " "))
		if l != "" {
			keep = append(keep, l)
		}
	}
	return strings.Join(keep, "\n")
}

func clipCues(c []Cue) []Cue {
	var out []Cue
	for i, x := range c {
		end := x.EndMs
		if i+1 < len(c) && c[i+1].StartMs < end {
			end = c[i+1].StartMs
		}
		if end > x.StartMs {
			x.EndMs = end
			out = append(out, x)
		}
	}
	return out
}

// dedupeRolling: YouTube auto-caption VTT repeats the previous line on top of each new one.
func dedupeRolling(c []Cue) []Cue {
	var out []Cue
	for _, x := range c {
		body := x.Text
		if n := len(out); n > 0 {
			prev := out[n-1]
			pl := strings.Split(prev.Text, "\n")
			cl := strings.Split(body, "\n")
			if len(cl) > 1 && cl[0] == pl[len(pl)-1] {
				body = strings.Join(cl[1:], "\n")
			}
			if body == prev.Text && x.StartMs <= prev.EndMs+50 {
				if x.EndMs > prev.EndMs {
					out[n-1].EndMs = x.EndMs
				}
				continue
			}
			if x.EndMs-x.StartMs < 30 && prev.Text == body {
				continue
			}
		}
		if strings.TrimSpace(body) != "" {
			x.Text = body
			out = append(out, x)
		}
	}
	return clipCues(out)
}

// VttClock is HH:MM:SS.mmm.
func VttClock(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d:%02d.%03d", ms/3600000, (ms/60000)%60, (ms/1000)%60, ms%1000)
}

// ToVtt writes cues as WebVTT.
func ToVtt(c []Cue) string {
	var b strings.Builder
	b.WriteString("WEBVTT\n\n")
	esc := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	for i, x := range c {
		fmt.Fprintf(&b, "%d\n%s --> %s\n%s\n\n", i+1, VttClock(x.StartMs), VttClock(x.EndMs), esc.Replace(x.Text))
	}
	return b.String()
}

// IsSubtitleFile says whether Blaze can load the file as subtitles.
func IsSubtitleFile(name string) bool {
	switch strings.ToLower(strings.TrimPrefix(filepath.Ext(name), ".")) {
	case "srt", "vtt", "webvtt":
		return true
	}
	return false
}

var langNames = map[string]string{"en": "English", "fr": "French", "es": "Spanish", "pt": "Portuguese", "de": "German", "it": "Italian",
	"nl": "Dutch", "ru": "Russian", "zh": "Chinese", "ja": "Japanese", "ko": "Korean", "ar": "Arabic", "hi": "Hindi", "sw": "Swahili",
	"ny": "Chichewa", "bem": "Bemba", "zu": "Zulu", "xh": "Xhosa", "af": "Afrikaans", "tr": "Turkish", "pl": "Polish", "uk": "Ukrainian",
	"id": "Indonesian", "vi": "Vietnamese", "th": "Thai", "sv": "Swedish", "no": "Norwegian", "da": "Danish", "fi": "Finnish", "el": "Greek", "he": "Hebrew"}

func baseLang(code string) string {
	f := strings.FieldsFunc(code, func(r rune) bool { return r == '-' || r == '_' })
	if len(f) == 0 {
		return ""
	}
	return strings.ToLower(f[0])
}

// LangName is a code's English name (the code when unknown).
func LangName(code string) string {
	if code == "" {
		return ""
	}
	if n, ok := langNames[baseLang(code)]; ok {
		return n
	}
	return code
}

// LangCode turns a language name or code into a code ("French" → "fr").
func LangCode(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	for c, l := range langNames {
		if strings.ToLower(l) == n {
			return c
		}
	}
	return n
}

// SubLabel is "English", "English (auto-generated)" or the file name.
func SubLabel(t Subtitle) string {
	base := orStr(orStr(t.Name, LangName(t.Lang)), "Subtitles")
	if t.Auto && !strings.Contains(strings.ToLower(base), "auto") && !strings.Contains(base, "(AI)") {
		return base + " (auto-generated)"
	}
	return base
}

// SameLang compares base language codes.
func SameLang(a, b string) bool {
	return a != "" && b != "" && baseLang(a) == baseLang(b)
}

// OrderSubs: the viewer's language first, manual before auto, then by name.
func OrderSubs(ts []Subtitle, pref string) []Subtitle {
	out := append([]Subtitle{}, ts...)
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if SameLang(a.Lang, pref) != SameLang(b.Lang, pref) {
			return SameLang(a.Lang, pref)
		}
		if a.Auto != b.Auto {
			return !a.Auto
		}
		return strings.ToLower(SubLabel(a)) < strings.ToLower(SubLabel(b))
	})
	return out
}

// PickSub chooses the track to turn on by itself ("" or "off" = none).
func PickSub(ts []Subtitle, pref string) *Subtitle {
	if pref == "" || pref == "off" {
		return nil
	}
	var auto *Subtitle
	for i := range ts {
		if SameLang(ts[i].Lang, pref) {
			if !ts[i].Auto {
				return &ts[i]
			}
			if auto == nil {
				auto = &ts[i]
			}
		}
	}
	return auto
}

// FolderFor is a safe folder name for a video key.
func FolderFor(key string) string {
	s := sha1.Sum([]byte(key))
	return hex.EncodeToString(s[:10])
}

// DecodeText is UTF-8 when it is, else Windows-1252 (old .srt files).
func DecodeText(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	r := make([]rune, len(b))
	for i, c := range b {
		r[i] = cp1252(c)
	}
	return string(r)
}

var cp1252Tbl = map[byte]rune{0x80: '€', 0x82: '‚', 0x84: '„', 0x85: '…', 0x91: '\u2018', 0x92: '\u2019', 0x93: '\u201c', 0x94: '\u201d', 0x96: '–', 0x97: '—', 0x99: '™'}

func cp1252(c byte) rune {
	if r, ok := cp1252Tbl[c]; ok {
		return r
	}
	return rune(c)
}

// ---- saved tracks ----

func (m *Media) subsDir(videoKey string) string {
	return filepath.Join(m.dir, "subs", FolderFor(videoKey))
}

// SavedSubs lists the tracks kept for a video (URL = local .vtt path).
func (m *Media) SavedSubs(videoKey string) []Subtitle {
	b, err := os.ReadFile(filepath.Join(m.subsDir(videoKey), "index.json"))
	if err != nil {
		return nil
	}
	var l []Subtitle
	_ = json.Unmarshal(b, &l)
	var out []Subtitle
	for _, s := range l {
		if fi, err := os.Stat(s.URL); err == nil && !fi.IsDir() {
			out = append(out, s)
		}
	}
	return out
}

// SaveSubs writes cues as track t of the video and returns it pointing at the file.
func (m *Media) SaveSubs(videoKey string, t Subtitle, cues []Cue) (Subtitle, error) {
	d := m.subsDir(videoKey)
	if err := os.MkdirAll(d, 0o755); err != nil {
		return t, err
	}
	f := filepath.Join(d, reSafe.ReplaceAllString(t.ID, "_")+".vtt")
	if err := os.WriteFile(f, []byte(ToVtt(cues)), 0o644); err != nil {
		return t, err
	}
	t.URL = f
	l := []Subtitle{}
	for _, s := range m.SavedSubs(videoKey) {
		if s.ID != t.ID {
			l = append(l, s)
		}
	}
	l = append(l, t)
	b, _ := json.Marshal(l)
	return t, os.WriteFile(filepath.Join(d, "index.json"), b, 0o644)
}

// SubCues reads a saved track's cues.
func SubCues(t Subtitle) []Cue {
	b, err := os.ReadFile(t.URL)
	if err != nil {
		return nil
	}
	return ParseSubs(DecodeText(b))
}

// FetchYtSubs downloads one YouTube caption track (json3, then vtt, then plain).
func FetchYtSubs(t Subtitle) []Cue {
	for _, f := range []string{"json3", "vtt", ""} {
		u := t.URL
		if f != "" {
			u = SubWithFormat(t.URL, f)
		}
		body, err := httpGet(u, webClient.UA)
		if err != nil {
			continue
		}
		if c := ParseSubs(body); len(c) > 0 {
			return c
		}
	}
	return nil
}

// SidecarSubs finds .srt/.vtt files next to a video ("Film.srt", "Film.en.srt").
func SidecarSubs(videoPath string) []Subtitle {
	dir := filepath.Dir(videoPath)
	base := strings.TrimSuffix(filepath.Base(videoPath), filepath.Ext(videoPath))
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Subtitle
	for _, e := range ents {
		n := e.Name()
		if e.IsDir() || !IsSubtitleFile(n) || !strings.HasPrefix(n, base) {
			continue
		}
		mid := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(n, base), filepath.Ext(n)), ".")
		lang := ""
		if len(mid) >= 2 && len(mid) <= 5 {
			lang = mid
		}
		out = append(out, Subtitle{ID: "file-" + reSafe.ReplaceAllString(n, "_"), URL: filepath.Join(dir, n), Lang: lang, Name: n})
	}
	return out
}
