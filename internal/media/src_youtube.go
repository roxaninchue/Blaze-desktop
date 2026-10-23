package media

// YouTube and "Download by link" (mobile LinkSource.kt + YouTubeStoreSource.kt):
// search through InnerTube WEB (videos only); streams from the app clients that hand
// out plain https urls without signature JavaScript (VISIONOS first, then ANDROID_VR,
// ANDROID, IOS), each probed ~1.5 MB in before it's trusted. Caption tracks come with
// the streams. Direct links to media files (or a page with og:video / og:audio) work
// too. Same client list and order as mobile v0.34.0.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ytClient is one InnerTube client.
type ytClient struct {
	Name    string
	ID      int
	Version string
	UA      string
	Extra   map[string]any
}

var ytClients = []ytClient{
	{"VISIONOS", 101, "1.02", "Mozilla/5.0 (Macintosh; Intel Mac OS X 15_7_3) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
		map[string]any{"deviceMake": "Apple", "deviceModel": "RealityDevice17,1", "osName": "visionOS", "osVersion": "26.5.23O471"}},
	{"ANDROID_VR", 28, "1.65.10", "com.google.android.apps.youtube.vr.oculus/1.65.10 (Linux; U; Android 12L; eureka-user Build/SQ3A.220605.009.A1) gzip",
		map[string]any{"deviceMake": "Oculus", "deviceModel": "Quest 3", "androidSdkVersion": 32, "osName": "Android", "osVersion": "12L"}},
	{"ANDROID", 3, "21.26.364", "com.google.android.youtube/21.26.364 (Linux; U; Android 14) gzip",
		map[string]any{"androidSdkVersion": 34, "osName": "Android", "osVersion": "14"}},
	{"IOS", 5, "21.26.4", "com.google.ios.youtube/21.26.4 (iPhone16,2; U; CPU iOS 18_3_2 like Mac OS X;)",
		map[string]any{"deviceMake": "Apple", "deviceModel": "iPhone16,2", "osName": "iPhone", "osVersion": "18.3.2.22D82"}},
}

var webClient = ytClient{"WEB", 1, "2.20260114.08.00", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36", nil}

const webUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

func ytContext(c ytClient) map[string]any {
	cl := map[string]any{"clientName": c.Name, "clientVersion": c.Version, "hl": "en", "gl": "US"}
	for k, v := range c.Extra {
		cl[k] = v
	}
	return map[string]any{"client": cl}
}

func ytPost(endpoint string, body map[string]any, c ytClient, visitor string) (string, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", "https://www.youtube.com/youtubei/v1/"+endpoint+"?prettyPrint=false", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.UA)
	req.Header.Set("X-YouTube-Client-Name", itoa(c.ID))
	req.Header.Set("X-YouTube-Client-Version", c.Version)
	req.Header.Set("Origin", "https://www.youtube.com")
	if visitor != "" {
		req.Header.Set("X-Goog-Visitor-Id", visitor)
	}
	r, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer r.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if r.StatusCode < 200 || r.StatusCode > 299 {
		extra := ""
		if strings.Contains(strings.ToLower(string(out)), "sorry") {
			extra = " (bot check)"
		}
		return "", &StoreError{Msg: fmt.Sprintf("%s: YouTube answered %d%s", c.Name, r.StatusCode, extra), Code: r.StatusCode}
	}
	return string(out), nil
}

func ytVisitor() string {
	c := ytClients[0]
	js, err := ytPost("visitor_id", map[string]any{"context": ytContext(c)}, c, "")
	if err != nil {
		return ""
	}
	var o struct {
		RC struct {
			V string `json:"visitorData"`
		} `json:"responseContext"`
	}
	_ = json.Unmarshal([]byte(js), &o)
	return o.RC.V
}

func ytSearchRaw(q, cont string) (string, error) {
	body := map[string]any{"context": ytContext(webClient)}
	if cont != "" {
		body["continuation"] = cont
	} else {
		body["query"] = q
		body["params"] = "EgIQAQ=="
	}
	return ytPost("search", body, webClient, "")
}

func ytPlaylistRaw(listID, cont string) (string, error) {
	body := map[string]any{"context": ytContext(webClient)}
	if cont != "" {
		body["continuation"] = cont
	} else {
		body["browseId"] = "VL" + listID
	}
	return ytPost("browse", body, webClient, "")
}

func ytPlayer(c ytClient, vid, visitor string) (string, error) {
	return ytPost("player", map[string]any{"context": ytContext(c), "videoId": vid, "contentCheckOk": true, "racyCheckOk": true}, c, visitor)
}

func ytOembed(vid string) (string, string) {
	js, err := httpGet("https://www.youtube.com/oembed?format=json&url="+url.QueryEscape("https://www.youtube.com/watch?v="+vid), webUA)
	if err != nil {
		return "", ""
	}
	var o struct {
		T string `json:"title"`
		A string `json:"author_name"`
	}
	_ = json.Unmarshal([]byte(js), &o)
	return o.T, o.A
}

// ProbeAt: read ~1.5 MB in for files over 2 MB (unknown size counts as big).
func ProbeAt(length int64) int64 {
	if length >= 1 && length <= 2_000_000 {
		return 0
	}
	return 1_500_000
}

// reachable is true when a stream url really downloads past the first bytes.
func reachable(u, ua string, length int64) bool {
	at := ProbeAt(length)
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", at, at+1023))
	c := &http.Client{Timeout: 10 * time.Second}
	r, err := c.Do(req)
	if err != nil {
		return false
	}
	defer r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode > 299 {
		return false
	}
	n, _ := r.Body.Read(make([]byte, 1024))
	return n > 0
}

var (
	reURL     = regexp.MustCompile(`https?://[^\s<>"']+`)
	reYtBare  = regexp.MustCompile(`(?:www\.|m\.|music\.)?(?:youtube\.com|youtu\.be)/[^\s<>"']+`)
	reYtID    = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)
	reYtPath  = regexp.MustCompile(`^/(?:shorts|embed|live|v|e)/([^/?#]+)`)
	reCodecs  = regexp.MustCompile(`codecs="([^"]+)"`)
	mediaExts = map[string]string{"mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac", "ogg": "audio/ogg", "oga": "audio/ogg",
		"opus": "audio/ogg", "flac": "audio/flac", "wav": "audio/wav", "mp4": "video/mp4", "m4v": "video/mp4", "webm": "video/webm",
		"mkv": "video/x-matroska", "mov": "video/quicktime", "3gp": "video/3gpp"}
	reOg = []*regexp.Regexp{
		regexp.MustCompile(`(?i)<meta[^>]+property=["']og:(?:video|audio)(?::(?:secure_)?url)?["'][^>]+content=["']([^"']+)["']`),
		regexp.MustCompile(`(?i)<meta[^>]+content=["']([^"']+)["'][^>]+property=["']og:(?:video|audio)(?::(?:secure_)?url)?["']`),
		regexp.MustCompile(`(?i)<(?:video|audio|source)[^>]+src=["']([^"']+\.(?:mp4|m4a|mp3|webm|ogg|opus|flac|wav|m4v)[^"']*)["']`),
	}
)

// FirstURL is the first link in text (bare YouTube links get https://).
func FirstURL(text string) string {
	t := strings.TrimSpace(text)
	m := reURL.FindString(t)
	if m == "" {
		if b := reYtBare.FindString(t); b != "" {
			m = "https://" + b
		}
	}
	return strings.TrimRight(m, ".,)]!?")
}

// YoutubeID is the 11-character id of any YouTube link, or "".
func YoutubeID(link string) string {
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	for _, p := range []string{"www.", "m.", "music."} {
		host = strings.TrimPrefix(host, p)
	}
	ok := func(s string) string {
		if reYtID.MatchString(s) {
			return s
		}
		return ""
	}
	switch host {
	case "youtu.be":
		p := strings.Trim(u.Path, "/")
		if i := strings.Index(p, "/"); i >= 0 {
			p = p[:i]
		}
		return ok(p)
	case "youtube.com", "youtube-nocookie.com":
		if v := ok(u.Query().Get("v")); v != "" {
			return v
		}
		if m := reYtPath.FindStringSubmatch(u.Path); m != nil {
			return ok(m[1])
		}
	}
	return ""
}

// ExtOf is a media file extension in a url, or "".
func ExtOf(u string) string {
	s := u
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = s[strings.LastIndex(s, "/")+1:]
	i := strings.LastIndex(s, ".")
	if i < 0 {
		return ""
	}
	e := strings.ToLower(s[i+1:])
	if _, ok := mediaExts[e]; ok {
		return e
	}
	return ""
}

// FileTitle is a readable title from a url's file name.
func FileTitle(u string) string {
	s := u
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimRight(s, "/")
	s = s[strings.LastIndex(s, "/")+1:]
	if d, err := url.PathUnescape(s); err == nil {
		s = d
	}
	if i := strings.LastIndex(s, "."); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(strings.NewReplacer("_", " ", "-", " ").Replace(s))
	if s == "" {
		s = "Download"
	}
	if r := []rune(s); len(r) > 80 {
		s = string(r[:80])
	}
	return s
}

// MediaInPage finds og:video / og:audio / <video src> in a page, made absolute.
func MediaInPage(html, base string) string {
	b, _ := url.Parse(base)
	for _, p := range reOg {
		if m := p.FindStringSubmatch(html); m != nil {
			s := strings.ReplaceAll(m[1], "&amp;", "&")
			abs := s
			if b != nil {
				if r, err := b.Parse(s); err == nil {
					abs = r.String()
				}
			}
			if YoutubeID(abs) == "" {
				return abs
			}
		}
	}
	return ""
}

// YtSearchParse reads InnerTube search JSON → items (videos only, no live) + next token.
func YtSearchParse(js, source string) Page {
	var root any
	_ = json.Unmarshal([]byte(js), &root)
	items := []Item{}
	next := ""
	seen := map[string]bool{}
	var walk func(x any)
	walk = func(x any) {
		switch v := x.(type) {
		case map[string]any:
			if r, ok := v["videoRenderer"].(map[string]any); ok {
				id := str(r["videoId"])
				l := runs(r["lengthText"])
				badges, _ := json.Marshal(r["badges"])
				live := strings.Contains(strings.ToUpper(string(badges)), "LIVE") || l == ""
				if id != "" && !live && !seen[id] {
					seen[id] = true
					sec := ParseClock(l)
					if sec < 0 {
						sec = 0
					}
					items = append(items, Item{Source: source, ID: id, Title: runs(r["title"]), Author: orStr(runs(r["ownerText"]), runs(r["longBylineText"])),
						DurationSec: sec, DurationText: l, Thumb: "https://i.ytimg.com/vi/" + id + "/mqdefault.jpg", Licence: "YouTube",
						Views: orStr(runs(r["shortViewCountText"]), runs(r["viewCountText"]))})
				}
				return
			}
			if c, ok := v["continuationCommand"].(map[string]any); ok && str(c["token"]) != "" {
				next = str(c["token"])
			}
			for _, k := range sortedKeys(v) {
				walk(v[k])
			}
		case []any:
			for _, y := range v {
				walk(y)
			}
		}
	}
	walk(root)
	return Page{items, next}
}

// YtDetailParse reads a player reply → formats (ciphered ones skipped) + captions.
func YtDetailParse(js, vid string) (Detail, error) {
	var root map[string]any
	if json.Unmarshal([]byte(js), &root) != nil {
		return Detail{}, serr("YouTube answered with something that isn't a player reply")
	}
	ps, _ := root["playabilityStatus"].(map[string]any)
	if st := str(ps["status"]); st != "OK" {
		return Detail{}, &StoreError{Msg: orStr(str(ps["reason"]), "YouTube says this video can't be played ("+st+")")}
	}
	vd, _ := root["videoDetails"].(map[string]any)
	sd, ok := root["streamingData"].(map[string]any)
	if !ok {
		return Detail{}, serr("YouTube gave no streams for this video")
	}
	var out []Format
	for _, key := range []string{"formats", "adaptiveFormats"} {
		arr, _ := sd[key].([]any)
		for _, x := range arr {
			f, _ := x.(map[string]any)
			u := str(f["url"])
			if u == "" {
				continue
			}
			mt := str(f["mimeType"])
			mime := strings.TrimSpace(strings.SplitN(mt, ";", 2)[0])
			codecs := sub1(reCodecs, mt)
			video := strings.HasPrefix(mime, "video/")
			audio := strings.HasPrefix(mime, "audio/") || (video && strings.Contains(codecs, ","))
			cl, _ := strconv.ParseInt(str(f["contentLength"]), 10, 64)
			out = append(out, Format{Key: str(f["itag"]), URL: u, Mime: mime, Codecs: codecs, Bitrate: num(f["bitrate"]), ContentLength: cl,
				Height: num(f["height"]), QualityLabel: str(f["qualityLabel"]), HasAudio: audio, HasVideo: video, FPS: num(f["fps"])})
		}
	}
	if len(out) == 0 {
		return Detail{}, serr("YouTube only offered protected streams for this video")
	}
	thumb := "https://i.ytimg.com/vi/" + vid + "/hqdefault.jpg"
	if th, ok := anyPath(vd, "thumbnail", "thumbnails").([]any); ok && len(th) > 0 {
		if m, ok := th[len(th)-1].(map[string]any); ok && str(m["url"]) != "" {
			thumb = str(m["url"])
		}
	}
	ls, _ := strconv.Atoi(str(vd["lengthSeconds"]))
	return Detail{ID: "yt:" + vid, Title: orStr(str(vd["title"]), "YouTube video"), Author: str(vd["author"]), LengthSec: ls, Thumb: thumb,
		Formats: out, Licence: "YouTube", Subtitles: YtTracks(root)}, nil
}

func num(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case string:
		i, _ := strconv.Atoi(n)
		return i
	}
	return 0
}

// YtChoices: music = AAC qualities then one Opus; video = H.264 ≤1080p merged with the
// best AAC (needs ffmpeg on the desktop: canMerge) and progressive files.
func YtChoices(kind string, d Detail, canMerge bool) []Choice {
	est := func(f Format) int64 {
		if f.ContentLength > 0 {
			return f.ContentLength
		}
		return int64(f.Bitrate) * int64(d.LengthSec) / 8
	}
	byBitrate := func(l []Format) []Format {
		sort.SliceStable(l, func(i, j int) bool { return l[i].Bitrate > l[j].Bitrate })
		return l
	}
	var aac, opus []Format
	for _, f := range d.Formats {
		if f.HasAudio && !f.HasVideo && f.Mime == "audio/mp4" {
			aac = append(aac, f)
		}
		if f.HasAudio && !f.HasVideo && f.Mime == "audio/webm" {
			opus = append(opus, f)
		}
	}
	aac, opus = byBitrate(aac), byBitrate(opus)
	if kind != "video" {
		var out []Choice
		seen := map[int]bool{}
		for i, f := range aac {
			if seen[f.Kbps()] {
				continue
			}
			seen[f.Kbps()] = true
			lv := "Smaller"
			if i == 0 {
				lv = "High"
			}
			out = append(out, Choice{ID: "a" + f.Key, Label: lv + " \u00b7 AAC " + itoa(f.Kbps()) + " kbps", Detail: "m4a \u00b7 " + SizeText(est(f)), Keys: []string{f.Key}, Ext: "m4a", Bytes: est(f), Kbps: f.Kbps()})
		}
		if len(opus) > 0 {
			f := opus[0]
			out = append(out, Choice{ID: "o" + f.Key, Label: "Opus " + itoa(f.Kbps()) + " kbps", Detail: "opus \u00b7 " + SizeText(est(f)), Keys: []string{f.Key}, Ext: "opus", Bytes: est(f), Kbps: f.Kbps()})
		}
		if len(out) == 0 {
			for _, f := range d.Formats {
				if f.Muxed() && f.Mime == "video/mp4" {
					return []Choice{{ID: "p" + f.Key, Label: "Audio \u00b7 from the video", Detail: "m4a", Keys: []string{f.Key}, Ext: "m4a", Bytes: est(f)}}
				}
			}
		}
		return out
	}
	var out []Choice
	if len(aac) > 0 && canMerge {
		best := map[int]Format{}
		for _, f := range d.Formats {
			if f.HasVideo && !f.HasAudio && f.Mime == "video/mp4" && strings.HasPrefix(f.Codecs, "avc1") && f.Height >= 1 && f.Height <= 1080 {
				if b, ok := best[f.Height]; !ok || f.Bitrate > b.Bitrate {
					best[f.Height] = f
				}
			}
		}
		for _, v := range best {
			b := est(v) + est(aac[0])
			out = append(out, Choice{ID: "v" + v.Key, Label: orStr(v.QualityLabel, itoa(v.Height)+"p"), Detail: "mp4 \u00b7 " + SizeText(b), Keys: []string{v.Key, aac[0].Key}, Ext: "mp4", Bytes: b, Height: v.Height})
		}
	}
	var prog []Format
	for _, f := range d.Formats {
		if f.Muxed() && f.Mime == "video/mp4" {
			prog = append(prog, f)
		}
	}
	sort.SliceStable(prog, func(i, j int) bool { return prog[i].Height > prog[j].Height })
	for _, p := range prog {
		dup := false
		for _, c := range out {
			if c.Height == p.Height {
				dup = true
			}
		}
		if !dup {
			out = append(out, Choice{ID: "p" + p.Key, Label: orStr(p.QualityLabel, itoa(p.Height)+"p") + " \u00b7 one file", Detail: "mp4 \u00b7 " + SizeText(est(p)), Keys: []string{p.Key}, Ext: "mp4", Bytes: est(p), Height: p.Height})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Height > out[j].Height })
	return out
}

// youtubeSource is the built-in YouTube source (id "youtube") and the Link source.
type youtubeSource struct {
	mu       sync.Mutex
	lastUA   string
	canMerge func() bool
	link     bool
}

func (y *youtubeSource) ID() string {
	if y.link {
		return "link"
	}
	return "youtube"
}
func (y *youtubeSource) Name() string {
	if y.link {
		return "Link"
	}
	return "YouTube"
}
func (y *youtubeSource) Kinds() []string     { return []string{"audio", "video"} }
func (y *youtubeSource) SetupNeeded() string { return "" }
func (y *youtubeSource) Browsable() bool     { return false }
func (y *youtubeSource) UserAgent() string {
	y.mu.Lock()
	defer y.mu.Unlock()
	return orStr(y.lastUA, ytClients[0].UA)
}

func (y *youtubeSource) Search(kind, query, next string) (Page, error) {
	if y.link {
		it, err := ResolveLink(query)
		if err != nil {
			return Page{}, err
		}
		return Page{Items: []Item{it}}, nil
	}
	q := strings.TrimSpace(query)
	if q == "" && next == "" {
		return Page{Items: []Item{}}, nil
	}
	js, err := ytSearchRaw(q, next)
	if err != nil {
		return Page{}, err
	}
	p := YtSearchParse(js, "youtube")
	if len(p.Items) == 0 && next == "" {
		return p, serr("YouTube found nothing for \u201c%s\u201d.", q)
	}
	return p, nil
}

// ResolveLink turns a pasted link into one item (YouTube or a direct file).
func ResolveLink(link string) (Item, error) {
	u := FirstURL(link)
	if u == "" {
		return Item{}, serr("That doesn't look like a link. Paste a YouTube link or a link to a music or video file.")
	}
	if vid := YoutubeID(u); vid != "" {
		t, a := ytOembed(vid)
		return Item{Source: "youtube", ID: vid, Title: orStr(t, "YouTube video"), Author: a, Thumb: "https://i.ytimg.com/vi/" + vid + "/mqdefault.jpg", Licence: "YouTube", Extra: map[string]string{"url": u}}, nil
	}
	host := ""
	if pu, err := url.Parse(u); err == nil {
		host = strings.TrimPrefix(pu.Hostname(), "www.")
	}
	return Item{Source: "link", ID: u, Title: FileTitle(u), Author: host, Licence: "From a link", Extra: map[string]string{"url": u}}, nil
}

func ytKey(id string) string {
	if strings.HasPrefix(id, "yt:") {
		return id
	}
	return "yt:" + id
}

func (y *youtubeSource) Detail(it Item, kind string) (Detail, error) {
	if y.link && !strings.HasPrefix(it.ID, "yt:") {
		return directDetail(it.ID)
	}
	d, err := y.youtube(strings.TrimPrefix(ytKey(it.ID), "yt:"), kind)
	if err != nil {
		return d, err
	}
	d.Title, d.Author = orStr(d.Title, it.Title), orStr(d.Author, it.Author)
	return d, nil
}

func (y *youtubeSource) youtube(vid, kind string) (Detail, error) {
	var errs []string
	visitor := ytVisitor()
	for _, c := range ytClients {
		js, err := ytPlayer(c, vid, visitor)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		d, err := YtDetailParse(js, vid)
		if err != nil {
			errs = append(errs, c.Name+": "+err.Error())
			continue
		}
		ch := YtChoices(kind, d, true)
		if len(ch) == 0 {
			errs = append(errs, c.Name+": no downloadable stream")
			continue
		}
		probe := formatsFor(d, ch[0].Keys[:1])
		if len(probe) == 0 || !reachable(probe[0].URL, c.UA, probe[0].ContentLength) {
			errs = append(errs, c.Name+": stream refused")
			continue
		}
		y.mu.Lock()
		y.lastUA = c.UA
		y.mu.Unlock()
		return d, nil
	}
	why := ""
	for _, e := range errs {
		l := strings.ToLower(e)
		for _, w := range []string{"bot", "age", "private", "unavailable", "sign in"} {
			if strings.Contains(l, w) && why == "" {
				why = ": " + e + "."
			}
		}
	}
	if why == "" {
		why = ". It may be private, age-restricted, live, or blocked on this network."
	}
	return Detail{}, serr("YouTube didn't hand out this video%s Try again in a bit.", why)
}

// YtCaptions is just the caption tracks of a video (downloaded videos played later).
func YtCaptions(vid string) []Subtitle {
	visitor := ytVisitor()
	for _, c := range ytClients {
		js, err := ytPlayer(c, strings.TrimPrefix(vid, "yt:"), visitor)
		if err != nil {
			continue
		}
		var root map[string]any
		if json.Unmarshal([]byte(js), &root) == nil {
			if t := YtTracks(root); len(t) > 0 {
				return t
			}
		}
	}
	return nil
}

func peek(u string) (string, int64, error) {
	req, _ := http.NewRequest("GET", u, nil)
	req.Header.Set("User-Agent", webUA)
	req.Header.Set("Range", "bytes=0-0")
	r, err := client.Do(req)
	if err != nil {
		return "", 0, err
	}
	r.Body.Close()
	if r.StatusCode < 200 || r.StatusCode > 299 {
		return "", 0, serr("The link answered %d", r.StatusCode)
	}
	total := int64(0)
	if cr := r.Header.Get("Content-Range"); cr != "" {
		total, _ = strconv.ParseInt(cr[strings.LastIndex(cr, "/")+1:], 10, 64)
	} else if r.StatusCode == 200 {
		total = r.ContentLength
	}
	if total < 0 {
		total = 0
	}
	return strings.ToLower(r.Header.Get("Content-Type")), total, nil
}

func directDetail(u string) (Detail, error) {
	mime, n, err := peek(u)
	if err != nil {
		return Detail{}, err
	}
	target := u
	if strings.HasPrefix(mime, "text/html") {
		page, err := httpGet(u, webUA)
		if err != nil {
			return Detail{}, err
		}
		target = MediaInPage(page, u)
		if target == "" {
			return Detail{}, serr("That page has no music or video file in it. Paste the file's own link, or a YouTube link.")
		}
		if mime, n, err = peek(target); err != nil {
			return Detail{}, err
		}
	}
	m := strings.TrimSpace(strings.SplitN(mime, ";", 2)[0])
	if !strings.HasPrefix(m, "audio/") && !strings.HasPrefix(m, "video/") {
		m = mediaExts[ExtOf(target)]
		if m == "" {
			return Detail{}, serr("That link isn't a music or video file (it's %s).", orStr(mime, "unknown"))
		}
	}
	host := ""
	if pu, err := url.Parse(target); err == nil {
		host = strings.TrimPrefix(pu.Hostname(), "www.")
	}
	f := Format{Key: "file", URL: target, Mime: m, ContentLength: n, HasAudio: true, HasVideo: strings.HasPrefix(m, "video/"), Name: FileTitle(target)}
	return Detail{ID: u, Title: FileTitle(target), Author: host, Formats: []Format{f}, Licence: "From a link"}, nil
}

func (y *youtubeSource) Choices(kind string, d Detail) []Choice {
	for _, f := range d.Formats {
		if f.Key == "file" {
			ext := ExtOf(f.URL)
			if ext == "" {
				ext = f.Container()
				if ext == "mp4" && !f.HasVideo {
					ext = "m4a"
				}
			}
			label := "Audio file"
			if f.HasVideo {
				label = "Video file"
			}
			return []Choice{{ID: "file", Label: label, Detail: joinNonEmpty(" \u00b7 ", ext, SizeText(f.ContentLength)), Keys: []string{"file"}, Ext: ext, Bytes: f.ContentLength}}
		}
	}
	merge := y.canMerge != nil && y.canMerge()
	return YtChoices(kind, d, merge)
}

func (y *youtubeSource) DefaultChoice(kind string, c []Choice) *Choice {
	if len(c) == 0 {
		return nil
	}
	if kind == "video" {
		for i := range c {
			if c[i].Height >= 1 && c[i].Height <= 720 {
				return &c[i]
			}
		}
	}
	return &c[0]
}

func (y *youtubeSource) Refresh(itemID, kind string) (*Detail, error) {
	if !strings.HasPrefix(itemID, "yt:") && y.link {
		d, err := directDetail(itemID)
		if err != nil {
			return nil, err
		}
		return &d, nil
	}
	d, err := y.youtube(strings.TrimPrefix(ytKey(itemID), "yt:"), kind)
	if err != nil {
		return nil, err
	}
	return &d, nil
}

// YtStreamPick is what the webview can play from YouTube right away: a progressive
// mp4 for video (one url), the best AAC for music.
func YtStreamPick(kind string, d Detail) []Format {
	ch := YtChoices(kind, d, false)
	if kind == "video" {
		for _, c := range ch {
			if len(c.Keys) == 1 {
				return formatsFor(d, c.Keys)
			}
		}
		return nil
	}
	if len(ch) == 0 {
		return nil
	}
	return formatsFor(d, ch[0].Keys)
}
