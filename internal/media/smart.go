package media

// The smart side of Blaze Music (mobile MusicSmart.kt): smart playlists, daily mixes,
// Wrapped, Blaze DJ's picks, equalizer presets, the fade curves, the tag fixer, iTunes
// covers and playlist import parsing (YouTube and Spotify). Pure.

import (
	"encoding/json"
	"math"
	"math/rand"
	"regexp"
	"sort"
	"strings"
)

// PlayEvent is one stretch of listening: Ms of song Key that started at At.
type PlayEvent struct {
	Key string
	At  int64
	Ms  int64
}

func (e PlayEvent) MarshalJSON() ([]byte, error) { return json.Marshal([]any{e.Key, e.At, e.Ms}) }
func (e *PlayEvent) UnmarshalJSON(b []byte) error {
	var a []json.RawMessage
	if err := json.Unmarshal(b, &a); err != nil || len(a) < 3 {
		return err
	}
	_ = json.Unmarshal(a[0], &e.Key)
	_ = json.Unmarshal(a[1], &e.At)
	_ = json.Unmarshal(a[2], &e.Ms)
	return nil
}

// Stat is a song's listening.
type Stat struct {
	Plays   int
	LastAt  int64
	TotalMs int64
}

// Stats: a listen counts as a play from 30 s.
func Stats(ev []PlayEvent) map[string]Stat {
	out := map[string]Stat{}
	for _, e := range ev {
		s := out[e.Key]
		if e.Ms >= 30000 {
			s.Plays++
		}
		if e.At > s.LastAt {
			s.LastAt = e.At
		}
		s.TotalMs += e.Ms
		out[e.Key] = s
	}
	return out
}

// SmartList is a smart playlist or a daily mix.
type SmartList struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	About string   `json:"about"`
	Keys  []string `json:"keys"`
}

const day = int64(86_400_000)

// SmartLists are rebuilt from the library and the play log every time; empty ones are left out.
func SmartLists(songs []Track, ev []PlayEvent, liked []string, now int64) []SmartList {
	st := Stats(ev)
	var rec []PlayEvent
	for _, e := range ev {
		if now-e.At <= 14*day {
			rec = append(rec, e)
		}
	}
	rs := Stats(rec)
	have := map[string]bool{}
	for _, s := range songs {
		have[s.Key] = true
	}
	pick := func(keep func(Track) bool, less func(a, b Track) bool, n int) []string {
		var l []Track
		for _, s := range songs {
			if keep(s) {
				l = append(l, s)
			}
		}
		sort.SliceStable(l, func(i, j int) bool { return less(l[i], l[j]) })
		keys := []string{}
		for i, t := range l {
			if n > 0 && i >= n {
				break
			}
			keys = append(keys, t.Key)
		}
		return keys
	}
	byPlays := func(m map[string]Stat) func(a, b Track) bool {
		return func(a, b Track) bool { return m[a.Key].Plays > m[b.Key].Plays }
	}
	newest := func(a, b Track) bool { return a.AddedAt > b.AddedAt }
	var likedHave []string
	for _, k := range liked {
		if have[k] {
			likedHave = append(likedHave, k)
		}
	}
	out := []SmartList{
		{"smart:top", "Most played", "Your 50 most played songs", pick(func(t Track) bool { return st[t.Key].Plays >= 2 }, byPlays(st), 50)},
		{"smart:repeat", "On repeat", "What you've played most in the last two weeks", pick(func(t Track) bool { return rs[t.Key].Plays >= 2 }, byPlays(rs), 30)},
		{"smart:new", "Recently added", "Added in the last 30 days", pick(func(t Track) bool { return t.AddedAt > 0 && now-t.AddedAt <= 30*day }, newest, 0)},
		{"smart:forgotten", "Forgotten favourites", "Songs you loved but haven't played in 6 weeks", pick(func(t Track) bool {
			s, ok := st[t.Key]
			return ok && s.Plays >= 3 && now-s.LastAt >= 42*day
		}, byPlays(st), 40)},
		{"smart:never", "Never played", "In your library, never listened to", pick(func(t Track) bool { _, ok := st[t.Key]; return !ok }, newest, 100)},
		{"smart:liked", "Liked, newest first", "Every song you liked", likedHave},
	}
	var res []SmartList
	for _, l := range out {
		if len(l.Keys) > 0 {
			res = append(res, l)
		}
	}
	return res
}

func shuffled[T any](l []T, r *rand.Rand) []T {
	c := append([]T{}, l...)
	r.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
	return c
}

// DailyMixes: up to 3 mixes, each from one of your most-listened artists and filled
// with your other most played songs, reshuffled every day (dayNo = days since epoch).
func DailyMixes(songs []Track, ev []PlayEvent, dayNo int64, size int) []SmartList {
	if size <= 0 {
		size = 25
	}
	st := Stats(ev)
	by := map[string][]Track{}
	var order []string
	for _, s := range songs {
		if strings.TrimSpace(s.Artist) == "" {
			continue
		}
		a := strings.ToLower(s.Artist)
		if _, ok := by[a]; !ok {
			order = append(order, a)
		}
		by[a] = append(by[a], s)
	}
	type at struct {
		a  string
		ms int64
	}
	var arts []at
	for _, a := range order {
		var ms int64
		for _, t := range by[a] {
			ms += st[t.Key].TotalMs
		}
		if ms > 0 {
			arts = append(arts, at{a, ms})
		}
	}
	sort.SliceStable(arts, func(i, j int) bool { return arts[i].ms > arts[j].ms })
	if len(arts) > 3 {
		arts = arts[:3]
	}
	pop := append([]Track{}, songs...)
	sort.SliceStable(pop, func(i, j int) bool { return st[pop[i].Key].TotalMs > st[pop[j].Key].TotalMs })
	var out []SmartList
	for i, a := range arts {
		r := rand.New(rand.NewSource(dayNo*31 + int64(i)))
		core := shuffled(by[a.a], r)
		if len(core) > size/2 {
			core = core[:size/2]
		}
		inCore := map[string]bool{}
		for _, t := range core {
			inCore[t.Key] = true
		}
		var fill []Track
		for _, t := range pop {
			if strings.ToLower(t.Artist) != a.a && !inCore[t.Key] {
				fill = append(fill, t)
				if len(fill) >= size*2 {
					break
				}
			}
		}
		fill = shuffled(fill, r)
		if n := size - len(core); len(fill) > n {
			fill = fill[:n]
		}
		mix := shuffled(append(core, fill...), r)
		keys := make([]string, len(mix))
		for j, t := range mix {
			keys[j] = t.Key
		}
		if len(keys) >= 3 {
			out = append(out, SmartList{"mix:" + itoa(i), "Daily Mix " + itoa(i+1), by[a.a][0].Artist + " and more", keys})
		}
	}
	return out
}

// Wrapped is listening over a period.
type Wrapped struct {
	Label       string      `json:"label"`
	Minutes     int64       `json:"minutes"`
	Plays       int         `json:"plays"`
	Songs       int         `json:"songs"`
	Artists     int         `json:"artists"`
	TopSongs    []TopSong   `json:"top_songs"`
	TopArtists  []TopArtist `json:"top_artists"`
	BusiestHour int         `json:"busiest_hour"`
}
type TopSong struct {
	Track Track `json:"track"`
	Plays int   `json:"plays"`
}
type TopArtist struct {
	Name    string `json:"name"`
	Minutes int64  `json:"minutes"`
}

// MakeWrapped sums listening between from and to; nil when nothing was played.
func MakeWrapped(ev []PlayEvent, songs []Track, from, to int64, label string, hourOf func(int64) int) *Wrapped {
	var in []PlayEvent
	for _, e := range ev {
		if e.At >= from && e.At < to {
			in = append(in, e)
		}
	}
	if len(in) == 0 {
		return nil
	}
	by := map[string]Track{}
	for _, s := range songs {
		by[s.Key] = s
	}
	st := Stats(in)
	w := &Wrapped{Label: label}
	for k, s := range st {
		if t, ok := by[k]; ok {
			w.Songs++
			if s.Plays > 0 {
				w.TopSongs = append(w.TopSongs, TopSong{t, s.Plays})
			}
		}
		w.Plays += s.Plays
	}
	sort.SliceStable(w.TopSongs, func(i, j int) bool {
		if w.TopSongs[i].Plays != w.TopSongs[j].Plays {
			return w.TopSongs[i].Plays > w.TopSongs[j].Plays
		}
		return w.TopSongs[i].Track.Title < w.TopSongs[j].Track.Title
	})
	if len(w.TopSongs) > 5 {
		w.TopSongs = w.TopSongs[:5]
	}
	art := map[string]int64{}
	hours := make([]int, 24)
	var total int64
	for _, e := range in {
		total += e.Ms
		if t, ok := by[e.Key]; ok && strings.TrimSpace(t.Artist) != "" {
			art[t.Artist] += e.Ms
		}
		hours[clamp(hourOf(e.At), 0, 23)]++
	}
	w.Minutes = total / 60000
	w.Artists = len(art)
	for a, ms := range art {
		w.TopArtists = append(w.TopArtists, TopArtist{a, ms / 60000})
	}
	sort.SliceStable(w.TopArtists, func(i, j int) bool {
		if w.TopArtists[i].Minutes != w.TopArtists[j].Minutes {
			return w.TopArtists[i].Minutes > w.TopArtists[j].Minutes
		}
		return w.TopArtists[i].Name < w.TopArtists[j].Name
	})
	if len(w.TopArtists) > 5 {
		w.TopArtists = w.TopArtists[:5]
	}
	for h := range hours {
		if hours[h] > hours[w.BusiestHour] {
			w.BusiestHour = h
		}
	}
	return w
}

// WrappedText is Wrapped as a sentence for the agent.
func WrappedText(w *Wrapped) string {
	if w == nil {
		return "Nothing played yet in this period. Wrapped counts songs you play in Blaze Music."
	}
	var b strings.Builder
	b.WriteString(w.Label + ": " + i64(w.Minutes) + " minutes, " + itoa(w.Plays) + " plays, " + itoa(w.Songs) + " songs, " + itoa(w.Artists) + " artists.\n")
	if len(w.TopSongs) > 0 {
		var p []string
		for _, s := range w.TopSongs {
			p = append(p, s.Track.Title+" ("+itoa(s.Plays)+")")
		}
		b.WriteString("Top songs: " + strings.Join(p, "; ") + "\n")
	}
	if len(w.TopArtists) > 0 {
		var p []string
		for _, a := range w.TopArtists {
			p = append(p, a.Name+" ("+i64(a.Minutes)+" min)")
		}
		b.WriteString("Top artists: " + strings.Join(p, "; ") + "\n")
	}
	h := itoa(w.BusiestHour)
	if len(h) == 1 {
		h = "0" + h
	}
	b.WriteString("You listen most around " + h + ":00.")
	return b.String()
}

func i64(n int64) string { return itoa(int(n)) }

// DJPicks: n songs to add when the queue runs out, weighted by plays and likes, never
// one of the last 20 played, a little randomness.
func DJPicks(songs []Track, ev []PlayEvent, liked map[string]bool, recent []string, n int, seed int64) []Track {
	st := Stats(ev)
	avoid := map[string]bool{}
	for i, k := range recent {
		if i >= 20 {
			break
		}
		avoid[k] = true
	}
	var pool []Track
	for _, s := range songs {
		if !avoid[s.Key] {
			pool = append(pool, s)
		}
	}
	if len(pool) == 0 {
		pool = songs
	}
	r := rand.New(rand.NewSource(seed))
	type sc struct {
		t Track
		w float64
	}
	var l []sc
	for _, t := range pool {
		s := st[t.Key]
		w := 1 + float64(s.Plays)*2 + float64(s.TotalMs)/600000
		if liked[t.Key] {
			w += 6
		}
		l = append(l, sc{t, w * (0.5 + r.Float64())})
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].w > l[j].w })
	var out []Track
	for i, x := range l {
		if i >= n {
			break
		}
		out = append(out, x.t)
	}
	return out
}

// EQPoint is (frequency Hz, gain millibels).
type EQPoint struct{ Hz, MB int }

// EQOrder keeps the presets in mobile's order.
var EQOrder = []string{"Flat", "Bass boost", "Bass reducer", "Vocal", "Treble boost", "Rock", "Pop", "Jazz", "Classical", "Hip-hop", "Small speaker"}

// EQPresets are mobile's curves; the desktop applies them to five WebAudio peaking filters.
var EQPresets = map[string][]EQPoint{
	"Flat":          {{60, 0}, {14000, 0}},
	"Bass boost":    {{60, 600}, {230, 350}, {910, 0}, {3600, 0}, {14000, 0}},
	"Bass reducer":  {{60, -600}, {230, -300}, {910, 0}, {3600, 0}, {14000, 0}},
	"Vocal":         {{60, -200}, {230, 0}, {910, 350}, {3600, 400}, {14000, 100}},
	"Treble boost":  {{60, 0}, {230, 0}, {910, 0}, {3600, 350}, {14000, 600}},
	"Rock":          {{60, 450}, {230, 250}, {910, -150}, {3600, 250}, {14000, 450}},
	"Pop":           {{60, -100}, {230, 200}, {910, 400}, {3600, 200}, {14000, -100}},
	"Jazz":          {{60, 300}, {230, 150}, {910, -100}, {3600, 150}, {14000, 300}},
	"Classical":     {{60, 350}, {230, 200}, {910, -150}, {3600, 200}, {14000, 350}},
	"Hip-hop":       {{60, 550}, {230, 350}, {910, 0}, {3600, 150}, {14000, 250}},
	"Small speaker": {{60, -400}, {230, 200}, {910, 300}, {3600, 300}, {14000, 0}},
}

// EQBands are the desktop's filter centres.
var EQBands = []int{60, 230, 910, 3600, 14000}

// BandLevels is the preset's gain at each centre (mB), linear in log-frequency, clamped.
func BandLevels(preset string, centers []int, min, max int) []int {
	pts, ok := EQPresets[preset]
	if !ok {
		pts = EQPresets["Flat"]
	}
	out := make([]int, len(centers))
	for i, f := range centers {
		var g float64
		switch {
		case f <= pts[0].Hz:
			g = float64(pts[0].MB)
		case f >= pts[len(pts)-1].Hz:
			g = float64(pts[len(pts)-1].MB)
		default:
			j := 0
			for k := range pts {
				if pts[k].Hz <= f {
					j = k
				}
			}
			a, b := pts[j], pts[j+1]
			t := (math.Log(float64(f)) - math.Log(float64(a.Hz))) / (math.Log(float64(b.Hz)) - math.Log(float64(a.Hz)))
			g = float64(a.MB) + float64(b.MB-a.MB)*t
		}
		out[i] = clamp(int(math.Round(g)), min, max)
	}
	return out
}

// FadeVolume fades in over the first fadeMs and out over the last fadeMs (1 when off).
func FadeVolume(posMs, durMs, fadeMs int64) float64 {
	if fadeMs <= 0 || durMs <= 0 || durMs < fadeMs*3 {
		return 1
	}
	in := math.Min(math.Max(float64(posMs)/float64(fadeMs), 0), 1)
	out := math.Min(math.Max(float64(durMs-posMs)/float64(fadeMs), 0), 1)
	return math.Max(math.Min(in, out), 0.05)
}

// SleepVolume: full until the last fadeMs before the timer, then down to silence.
func SleepVolume(leftMs, fadeMs int64) float64 {
	if leftMs >= fadeMs {
		return 1
	}
	return math.Min(math.Max(float64(leftMs)/float64(fadeMs), 0), 1)
}

var (
	tagNoise    = regexp.MustCompile(`(?i)\s*[(\[]\s*(official\s*(music\s*)?(video|audio|lyric\s*video|visuali[sz]er)|lyrics?|lyric video|audio|hd|hq|4k|1080p|720p|video\s*oficial|clip officiel|visuali[sz]er|explicit|remastered(\s*\d{4})?|full song)\s*[)\]]`)
	tagTrailing = regexp.MustCompile(`(?i)\s*[-|]\s*(official\s*(music\s*)?(video|audio)|lyrics?|audio)\s*$`)
	tagSplit    = regexp.MustCompile(`^(.+?)\s+[-\x{2013}\x{2014}|]\s+(.+)$`)
	reTopic     = regexp.MustCompile(`(?i)\s*-\s*topic$`)
	reVevo      = regexp.MustCompile(`(?i)vevo$`)
	reOfficial  = regexp.MustCompile(`(?i)\s*official$`)
)

// FixTags: "Artist - Title (Official Video)" / "ArtistVEVO" / "Artist - Topic" → (artist, title).
func FixTags(title, artist string) (string, string) {
	t := strings.TrimSpace(spaces.ReplaceAllString(tagTrailing.ReplaceAllString(tagNoise.ReplaceAllString(title, ""), ""), " "))
	a := strings.TrimSpace(reOfficial.ReplaceAllString(reVevo.ReplaceAllString(reTopic.ReplaceAllString(artist, ""), ""), ""))
	if m := tagSplit.FindStringSubmatch(t); m != nil {
		left, right := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
		if strings.EqualFold(right, a) {
			t = left
		} else {
			a, t = left, right
		}
	}
	t = strings.Trim(t, "\"\u201c\u201d ")
	return orStr(a, artist), orStr(t, title)
}

// ItunesArt is the first song's 600 px cover from an iTunes Search reply.
func ItunesArt(js string) string {
	var r struct {
		Results []struct {
			Art string `json:"artworkUrl100"`
		} `json:"results"`
	}
	if json.Unmarshal([]byte(js), &r) != nil || len(r.Results) == 0 || !strings.HasPrefix(r.Results[0].Art, "http") {
		return ""
	}
	return strings.Replace(r.Results[0].Art, "100x100bb", "600x600bb", 1)
}

var (
	reYtList   = regexp.MustCompile(`[?&]list=([A-Za-z0-9_-]{10,})`)
	reSpList   = regexp.MustCompile(`(?:open\.spotify\.com/(?:intl-[a-z]+/)?(?:embed/)?playlist/|spotify:playlist:)([A-Za-z0-9]{16,})`)
	reSpAlbum  = regexp.MustCompile(`(?:open\.spotify\.com/(?:intl-[a-z]+/)?(?:embed/)?album/|spotify:album:)([A-Za-z0-9]{16,})`)
	reNextData = regexp.MustCompile(`(?s)<script id="__NEXT_DATA__" type="application/json">(.*?)</script>`)
)

func sub1(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return ""
}

func YoutubePlaylistID(u string) string { return sub1(reYtList, u) }
func SpotifyPlaylistID(u string) string { return sub1(reSpList, u) }
func SpotifyAlbumID(u string) string    { return sub1(reSpAlbum, u) }

// SpotifyEmbed reads Spotify's public embed page → ("Artist - Title" list, name).
func SpotifyEmbed(html string) ([]string, string) {
	js := sub1(reNextData, html)
	if js == "" {
		return nil, ""
	}
	var root map[string]any
	if json.Unmarshal([]byte(js), &root) != nil {
		return nil, ""
	}
	ent := dig(root, "props", "pageProps", "state", "data", "entity")
	if ent == nil {
		return nil, ""
	}
	var out []string
	if l, ok := ent["trackList"].([]any); ok {
		for _, x := range l {
			t, _ := x.(map[string]any)
			title := str(t["title"])
			by := strings.ReplaceAll(str(t["subtitle"]), "\u00a0", " ")
			if title == "" {
				continue
			}
			if by == "" {
				out = append(out, title)
			} else {
				out = append(out, by+" - "+title)
			}
		}
	}
	return out, orStr(str(ent["name"]), str(ent["title"]))
}

// YtPlaylist reads one page of an InnerTube playlist → (id, "title") list, next token, name.
func YtPlaylist(js string) ([][2]string, string, string) {
	var root any
	_ = json.Unmarshal([]byte(js), &root)
	var out [][2]string
	next, name := "", ""
	seen := map[string]bool{}
	var walk func(x any)
	walk = func(x any) {
		switch v := x.(type) {
		case map[string]any:
			if r, ok := v["playlistVideoRenderer"].(map[string]any); ok {
				id := str(r["videoId"])
				t := runs(r["title"])
				by := runs(r["shortBylineText"])
				if id != "" && !seen[id] {
					seen[id] = true
					if by != "" && !strings.Contains(strings.ToLower(t), strings.ToLower(by)) {
						t = by + " - " + t
					}
					out = append(out, [2]string{id, t})
				}
				return
			}
			if m, ok := v["playlistMetadataRenderer"].(map[string]any); ok && str(m["title"]) != "" {
				name = str(m["title"])
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
	return out, next, name
}

func sortedKeys(m map[string]any) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

func dig(m map[string]any, path ...string) map[string]any {
	cur := m
	for _, p := range path {
		n, ok := cur[p].(map[string]any)
		if !ok {
			return nil
		}
		cur = n
	}
	return cur
}

func str(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case float64:
		if s == math.Trunc(s) {
			return itoa(int(s))
		}
	case bool:
		if s {
			return "true"
		}
		return "false"
	}
	return ""
}

// runs reads InnerTube text ({simpleText} or {runs:[{text}]}).
func runs(v any) string {
	o, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	if s := str(o["simpleText"]); s != "" {
		return s
	}
	var b strings.Builder
	if a, ok := o["runs"].([]any); ok {
		for _, r := range a {
			if m, ok := r.(map[string]any); ok {
				b.WriteString(str(m["text"]))
			}
		}
	}
	return b.String()
}
