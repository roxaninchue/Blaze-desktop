package media

// Lyrics (mobile Lyrics.kt + Extras.kt Lrc): LRCLIB (lrclib.net, free, no key; synced
// when available), cached under ~/.blaze/media/lyrics so they work offline afterwards.
// "Not found" is cached for a day.

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// LyricLine is one synced line.
type LyricLine struct {
	Ms   int64  `json:"ms"`
	Text string `json:"text"`
}

// LyricsResult is what the Lyrics view shows.
type LyricsResult struct {
	Synced       []LyricLine `json:"synced"`
	Plain        string      `json:"plain"`
	Instrumental bool        `json:"instrumental"`
	Source       string      `json:"source"`
}

func (r LyricsResult) Empty() bool {
	return len(r.Synced) == 0 && strings.TrimSpace(r.Plain) == "" && !r.Instrumental
}

var lrcTag = regexp.MustCompile(`\[(\d{1,3}):(\d{1,2})(?:[.:](\d{1,3}))?]`)

// ParseLrc: "[01:02.50]Hello" → (62500, "Hello"); several tags repeat a line; sorted.
func ParseLrc(text string) []LyricLine {
	var out []LyricLine
	for _, raw := range strings.Split(strings.ReplaceAll(text, "\r", ""), "\n") {
		tags := lrcTag.FindAllStringSubmatchIndex(raw, -1)
		if len(tags) == 0 {
			continue
		}
		words := strings.TrimSpace(raw[tags[len(tags)-1][1]:])
		for _, t := range tags {
			m, _ := strconv.ParseInt(raw[t[2]:t[3]], 10, 64)
			s, _ := strconv.ParseInt(raw[t[4]:t[5]], 10, 64)
			var ms int64
			if t[6] >= 0 {
				frac := raw[t[6]:t[7]]
				v, _ := strconv.ParseInt(frac, 10, 64)
				switch len(frac) {
				case 1:
					ms = v * 100
				case 2:
					ms = v * 10
				default:
					ms = v
				}
			}
			out = append(out, LyricLine{m*60000 + s*1000 + ms, words})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Ms < out[j].Ms })
	return out
}

// LrcCurrent is the line being sung at pos (-1 before the first).
func LrcCurrent(lines []LyricLine, pos int64) int {
	lo, hi, ans := 0, len(lines)-1, -1
	for lo <= hi {
		mid := (lo + hi) / 2
		if lines[mid].Ms <= pos {
			ans, lo = mid, mid+1
		} else {
			hi = mid - 1
		}
	}
	return ans
}

var lyricNoise = regexp.MustCompile(`(?i)\s*[(\[][^)\]]*(official|video|audio|lyrics?|hd|4k|remaster(ed)?|visuali[sz]er)[^)\]]*[)\]]`)

// CleanTitle for a lookup: "Song (Official Video) [HD]" → "Song"; "Artist - Song" → "Song".
func CleanTitle(title, artist string) string {
	t := lyricNoise.ReplaceAllString(title, "")
	if artist != "" && strings.HasPrefix(strings.ToLower(t), strings.ToLower(artist)+" - ") {
		t = t[len(artist)+3:]
	} else if i := strings.Index(t, " - "); i >= 0 {
		t = t[i+3:]
	}
	return strings.TrimSpace(t)
}

// ParseLrclib reads one LRCLIB record.
func ParseLrclib(js string) LyricsResult {
	var o struct {
		Synced       *string `json:"syncedLyrics"`
		Plain        *string `json:"plainLyrics"`
		Instrumental bool    `json:"instrumental"`
	}
	_ = json.Unmarshal([]byte(js), &o)
	r := LyricsResult{Instrumental: o.Instrumental, Source: "LRCLIB"}
	if o.Synced != nil {
		r.Synced = ParseLrc(*o.Synced)
	}
	if o.Plain != nil {
		r.Plain = *o.Plain
	}
	return r
}

func lyricsKey(t Track) string {
	h := fnv.New32a()
	h.Write([]byte(strings.ToLower(t.Artist + "|" + t.Title)))
	return fmt.Sprintf("%08x", h.Sum32())
}

// Lyrics finds lyrics for t (cache first).
func (m *Media) Lyrics(t Track) (LyricsResult, error) {
	dir := filepath.Join(m.dir, "lyrics")
	_ = os.MkdirAll(dir, 0o755)
	f := filepath.Join(dir, lyricsKey(t)+".json")
	if st, err := os.Stat(f); err == nil && (st.Size() > 2 || time.Since(st.ModTime()) < 24*time.Hour) {
		if st.Size() <= 2 {
			return LyricsResult{Source: "LRCLIB"}, nil
		}
		b, _ := os.ReadFile(f)
		return ParseLrclib(string(b)), nil
	}
	title := CleanTitle(t.Title, t.Artist)
	u := "https://lrclib.net/api/get?track_name=" + url.QueryEscape(title) + "&artist_name=" + url.QueryEscape(t.Artist)
	if t.DurationMs > 0 {
		u += "&duration=" + itoa(int(t.DurationMs/1000))
	}
	body, err := httpGet(u, "")
	if err != nil {
		se, ok := err.(*StoreError)
		if !ok || se.Code != 404 {
			return LyricsResult{}, err
		}
		body = ""
		s, err := httpGet("https://lrclib.net/api/search?q="+url.QueryEscape(strings.TrimSpace(t.Artist+" "+title)), "")
		if err != nil {
			return LyricsResult{}, err
		}
		var arr []json.RawMessage
		if json.Unmarshal([]byte(s), &arr) == nil && len(arr) > 0 {
			body = string(arr[0])
		}
	}
	r := LyricsResult{Source: "LRCLIB"}
	if body != "" {
		r = ParseLrclib(body)
	}
	if r.Empty() {
		_ = os.WriteFile(f, []byte("{}"), 0o644)
	} else {
		_ = os.WriteFile(f, []byte(body), 0o644)
	}
	return r, nil
}
