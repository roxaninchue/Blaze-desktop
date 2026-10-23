package media

// What the agent and the views ask the service to do beyond the library itself:
// playing by words ("play something chill"), the music_ops / video_ops / media_download
// tools (mobile MusicTools.kt, VideoTools.kt, MediaFetch.kt), playlist import and the
// tag fixer (MusicExtras.kt), and the video subtitle actions (VideoActivity.kt).
// Player commands go to the webview as "media:cmd" events; it owns <audio>/<video>.

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Cmd is a player command for the webview.
type Cmd struct {
	Op      string   `json:"op"` // play, queue, pause, resume, toggle, next, prev, stop, shuffle, repeat, volume, seek, eq, crossfade, sleep, dj, video, subs, subs_off
	Keys    []string `json:"keys,omitempty"`
	Shuffle bool     `json:"shuffle,omitempty"`
	Value   string   `json:"value,omitempty"`
	Num     float64  `json:"num,omitempty"`
	Title   string   `json:"title,omitempty"`
}

func (m *Media) send(c Cmd) { m.emit("media:cmd", c) }

// Moods map words people say to words found in titles, genres, albums and artists.
var Moods = map[string][]string{
	"chill":     {"chill", "lofi", "lo-fi", "lo fi", "acoustic", "ambient", "relax", "calm", "slow", "mellow", "soft", "jazz", "piano", "soul", "r&b", "rnb", "downtempo", "sleep", "study", "unplugged", "ballad"},
	"relax":     {"chill", "lofi", "acoustic", "ambient", "relax", "calm", "slow", "mellow", "piano", "sleep"},
	"focus":     {"lofi", "lo-fi", "piano", "instrumental", "ambient", "classical", "study", "focus", "beats"},
	"study":     {"lofi", "lo-fi", "piano", "instrumental", "ambient", "classical", "study", "focus", "beats"},
	"sleep":     {"sleep", "ambient", "piano", "calm", "relax", "lullaby", "slow"},
	"happy":     {"happy", "pop", "dance", "summer", "sunshine", "good", "party", "afrobeat", "amapiano"},
	"party":     {"party", "dance", "club", "remix", "amapiano", "afrobeat", "edm", "house", "hip hop", "hip-hop", "dancehall"},
	"dance":     {"dance", "club", "house", "edm", "amapiano", "afrobeat", "disco", "dancehall"},
	"workout":   {"workout", "gym", "run", "running", "hype", "trap", "drill", "edm", "rock", "hip hop", "hip-hop", "power"},
	"energetic": {"hype", "energy", "rock", "edm", "trap", "drill", "dance", "metal", "punk"},
	"sad":       {"sad", "heartbreak", "alone", "cry", "tears", "ballad", "blues", "slow", "lonely"},
	"romantic":  {"love", "heart", "baby", "romance", "r&b", "rnb", "soul", "slow jam", "kiss"},
	"gospel":    {"gospel", "worship", "praise", "jesus", "lord", "god", "hymn", "christian"},
	"worship":   {"gospel", "worship", "praise", "jesus", "lord", "god", "hymn", "christian"},
}

var fillerWords = map[string]bool{"play": true, "some": true, "something": true, "music": true, "songs": true, "song": true, "a": true, "an": true, "the": true, "me": true, "my": true, "for": true, "please": true, "put": true, "on": true, "tunes": true, "track": true, "tracks": true, "vibes": true, "vibe": true, "mood": true, "kind": true, "of": true, "stuff": true, "bit": true}

// MoodWords finds the mood words in a request ("something chill" → chill's list).
func MoodWords(q string) []string {
	var out []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		if l, ok := Moods[strings.Trim(w, ".,!?\"'")]; ok {
			out = append(out, l...)
		}
	}
	return uniq(out)
}

// MatchMusic picks songs for a request: a library search on the meaningful words
// first, else mood words scored against title/genre/album/artist. ok=false when
// nothing fits.
func MatchMusic(songs []Track, query string) ([]Track, string, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, "", false
	}
	var words []string
	for _, w := range strings.Fields(strings.ToLower(q)) {
		w = strings.Trim(w, ".,!?\"'")
		if w != "" && !fillerWords[w] {
			words = append(words, w)
		}
	}
	if len(words) > 0 {
		if found := Search(songs, strings.Join(words, " ")); len(found) > 0 {
			return found, "matching \u201c" + strings.Join(words, " ") + "\u201d", true
		}
	}
	mood := MoodWords(q)
	if len(mood) == 0 {
		return nil, "", false
	}
	type sc struct {
		t Track
		s int
	}
	var l []sc
	for _, t := range songs {
		hay := strings.ToLower(t.Title + " " + t.Genre + " " + t.Album + " " + t.Artist)
		g := strings.ToLower(t.Genre)
		s := 0
		for _, w := range mood {
			if strings.Contains(hay, w) {
				s++
				if strings.Contains(g, w) {
					s++
				}
			}
		}
		if s > 0 {
			l = append(l, sc{t, s})
		}
	}
	if len(l) == 0 {
		return nil, "", false
	}
	sort.SliceStable(l, func(i, j int) bool { return l[i].s > l[j].s })
	out := make([]Track, len(l))
	for i, x := range l {
		out[i] = x.t
	}
	return out, "that fit \u201c" + q + "\u201d", true
}

func keysOf(ts []Track) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Key
	}
	return out
}

func (m *Media) listByName(name string) (SmartList, bool) {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return SmartList{}, false
	}
	var all []SmartList
	for _, p := range m.Playlists() {
		all = append(all, SmartList{p.ID, p.Name, "your playlist", p.Keys})
	}
	all = append(all, m.Mixes()...)
	for _, l := range all {
		if strings.ToLower(l.Name) == n {
			return l, true
		}
	}
	for _, l := range all {
		ln := strings.ToLower(l.Name)
		if strings.Contains(ln, n) || strings.Contains(n, ln) {
			return l, true
		}
	}
	return SmartList{}, false
}

// MusicPlay runs the music_play tool (play / queue / pause / next / what's playing…).
func (m *Media) MusicPlay(a map[string]any) string {
	action := strings.ToLower(strings.TrimSpace(argS(a, "action")))
	query := strings.TrimSpace(argS(a, "query"))
	if action == "" {
		action = "play"
	}
	switch action {
	case "play", "queue":
		if pl := argS(a, "playlist"); pl != "" {
			l, ok := m.listByName(pl)
			if !ok {
				return "No playlist or mix called \u201c" + pl + "\u201d. Use music_ops mixes to list them."
			}
			if len(l.Keys) == 0 {
				return l.Name + " is empty."
			}
			m.send(Cmd{Op: action, Keys: l.Keys, Shuffle: argB(a, "shuffle")})
			return verb(action) + " " + l.Name + " (" + itoa(len(l.Keys)) + " songs) in Blaze Music."
		}
		songs := m.Songs()
		if len(songs) == 0 {
			return "There are no songs in Blaze Music yet. Its library reads the Music folder (and folders added in Music › Folders). Offer to download songs with media_download, or import a playlist with music_ops import."
		}
		if query == "" && action == "play" {
			if p := m.Player(); p.Kind == "audio" && p.Key != "" {
				m.send(Cmd{Op: "resume"})
				return "Resumed \u201c" + p.Title + "\u201d."
			}
			picks := m.DJ(25)
			m.send(Cmd{Op: "play", Keys: keysOf(picks)})
			return "Playing a Blaze mix of " + itoa(len(picks)) + " songs the user plays most."
		}
		found, why, ok := MatchMusic(songs, query)
		if !ok {
			if len(MoodWords(query)) > 0 {
				picks := m.DJ(25)
				m.send(Cmd{Op: action, Keys: keysOf(picks), Shuffle: true})
				return "No songs in the library are tagged or titled like \u201c" + query + "\u201d, so I " + strings.ToLower(verb(action)) + " a shuffled mix of the user's favourites instead (" + itoa(len(picks)) + " songs). media_download can fetch songs that fit."
			}
			return "Nothing in the library matches \u201c" + query + "\u201d. media_download can get it from YouTube (ask first)."
		}
		if len(found) > 100 {
			found = found[:100]
		}
		shuffle := argB(a, "shuffle") || len(MoodWords(query)) > 0
		m.send(Cmd{Op: action, Keys: keysOf(found), Shuffle: shuffle})
		first := found[0].Title
		if found[0].Artist != "" {
			first += " \u2014 " + found[0].Artist
		}
		if action == "queue" {
			return "Added " + itoa(len(found)) + " song(s) " + why + " to the queue (first: " + first + ")."
		}
		return "Playing " + itoa(len(found)) + " song(s) " + why + " in Blaze Music, starting with " + first + "."
	case "pause", "resume", "toggle", "next", "previous", "prev", "stop":
		op := action
		if op == "previous" {
			op = "prev"
		}
		p := m.Player()
		if p.Key == "" && action != "stop" {
			return "Nothing is playing in Blaze yet. Use action=play."
		}
		m.send(Cmd{Op: op})
		return map[string]string{"pause": "Paused.", "resume": "Playing.", "toggle": "Toggled play/pause.", "next": "Skipped to the next song.",
			"previous": "Back to the previous song.", "prev": "Back to the previous song.", "stop": "Stopped."}[action]
	case "shuffle":
		on := !hasKey(a, "on") || argB(a, "on")
		m.send(Cmd{Op: "shuffle", Value: onOff(on)})
		return "Shuffle " + onOff(on) + "."
	case "repeat":
		mode := strings.ToLower(orStr(argS(a, "mode"), "all"))
		if mode != "off" && mode != "one" {
			mode = "all"
		}
		m.send(Cmd{Op: "repeat", Value: mode})
		return "Repeat " + mode + "."
	case "volume":
		v := argF(a, "level", -1)
		if v < 0 {
			return "Give level 0-100."
		}
		if v > 1 {
			v /= 100
		}
		if v > 1 {
			v = 1
		}
		m.send(Cmd{Op: "volume", Num: v})
		return fmt.Sprintf("Volume %d%%.", int(v*100+0.5))
	case "seek":
		s := argF(a, "seconds", -1)
		if s < 0 {
			return "Give seconds."
		}
		m.send(Cmd{Op: "seek", Num: s})
		return "Jumped to " + Mmss(int64(s*1000)) + "."
	case "now", "status":
		return m.NowPlaying()
	}
	return "Unknown action. Use play, queue, pause, resume, next, previous, stop, shuffle, repeat, volume, seek or now."
}

// NowPlaying is the player state as a sentence.
func (m *Media) NowPlaying() string {
	p := m.Player()
	if p.Key == "" || time.Since(time.UnixMilli(p.Updated)) > 10*time.Minute {
		return "Nothing is playing in Blaze Music or Video."
	}
	st := "Paused"
	if p.Playing {
		st = "Playing"
	}
	s := fmt.Sprintf("%s: \u201c%s\u201d", st, p.Title)
	if p.Artist != "" {
		s += " by " + p.Artist
	}
	s += fmt.Sprintf(" (%s of %s", Mmss(p.PosMs), Mmss(p.DurMs))
	if p.Count > 1 {
		s += fmt.Sprintf(", %d of %d in the queue", p.Index+1, p.Count)
	}
	s += ")."
	if p.Shuffle {
		s += " Shuffle on."
	}
	if p.Repeat != "" && p.Repeat != "off" {
		s += " Repeat " + p.Repeat + "."
	}
	if len(p.UpNext) > 0 {
		s += " Up next: " + strings.Join(p.UpNext, "; ") + "."
	}
	return s
}

func verb(action string) string {
	if action == "queue" {
		return "Queued"
	}
	return "Playing"
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// MusicOps runs music_ops (mobile MusicTools.kt, plus sleep).
func (m *Media) MusicOps(a map[string]any) string {
	switch argS(a, "action") {
	case "dj":
		on := !hasKey(a, "on") || argB(a, "on")
		m.UpdatePrefs(func(p *Prefs) { p.DJ = on })
		if !on {
			m.send(Cmd{Op: "dj", Value: "off"})
			return "Blaze DJ is off."
		}
		if len(m.Songs()) == 0 {
			return "There are no songs in Blaze Music yet. Download some (media_download) or import a playlist first."
		}
		m.send(Cmd{Op: "dj", Value: "on", Keys: keysOf(m.DJ(10))})
		return "Blaze DJ is on: it plays songs the user loves and keeps adding more when the queue runs out."
	case "mixes":
		var b []string
		for _, l := range m.Mixes() {
			b = append(b, l.Name+": "+l.About+" ("+itoa(len(l.Keys))+" songs)")
		}
		for _, p := range m.Playlists() {
			b = append(b, p.Name+": playlist ("+itoa(len(p.Keys))+" songs)")
		}
		if len(b) == 0 {
			return "No mixes yet: they build from songs played in Blaze Music."
		}
		return strings.Join(b, "\n")
	case "play_list":
		n := argS(a, "name")
		l, ok := m.listByName(n)
		if !ok {
			var names []string
			for _, x := range m.Mixes() {
				names = append(names, x.Name)
			}
			return "No mix or smart playlist called \u201c" + n + "\u201d. Have: " + strings.Join(names, ", ")
		}
		m.send(Cmd{Op: "play", Keys: l.Keys, Shuffle: argB(a, "shuffle")})
		return "Playing " + l.Name + " (" + itoa(len(l.Keys)) + " songs)."
	case "wrapped":
		return WrappedText(m.WrappedFor(orStr(argS(a, "period"), "year")))
	case "eq":
		want := strings.TrimSpace(argS(a, "preset"))
		for _, p := range EQOrder {
			if strings.EqualFold(p, want) {
				m.UpdatePrefs(func(pr *Prefs) { pr.EQ = p })
				m.send(Cmd{Op: "eq", Value: p})
				if p == "Flat" {
					return "Equalizer off (flat)."
				}
				return "Equalizer set to " + p + "."
			}
		}
		return "Presets: " + strings.Join(EQOrder, ", ")
	case "crossfade":
		s := clamp(argI(a, "seconds", 0), 0, 12)
		m.UpdatePrefs(func(p *Prefs) { p.Crossfade = s })
		m.send(Cmd{Op: "crossfade", Num: float64(s)})
		if s == 0 {
			return "No fade between songs."
		}
		return "Songs now fade out and in over " + itoa(s) + " s."
	case "data_saver":
		on := !hasKey(a, "on") || argB(a, "on")
		m.UpdatePrefs(func(p *Prefs) { p.DataSaver = on })
		if on {
			return "Data saver on: Store streams use the smallest quality."
		}
		return "Data saver off."
	case "import":
		u := strings.TrimSpace(argS(a, "url"))
		if u == "" {
			return "Give the playlist link (YouTube or Spotify)."
		}
		return m.ImportPlaylist(u, 100)
	case "fix_tags":
		r := m.FixTags(nil, nil)
		return fmt.Sprintf("Checked %d downloaded songs: %d renamed, %d got real covers.", r.Checked, r.Renamed, r.Covers)
	case "sleep":
		min := argI(a, "minutes", 0)
		m.send(Cmd{Op: "sleep", Num: float64(min)})
		if min <= 0 {
			return "Sleep timer off."
		}
		return "Music stops in " + itoa(min) + " minutes."
	}
	return "Unknown action."
}

// FixResult is what the tag fixer did.
type FixResult struct{ Renamed, Covers, Checked int }

// FixTags cleans "Artist - Title (Official Video)" titles of downloaded songs and
// gives songs whose cover is a video frame a real one from the iTunes Search API.
func (m *Media) FixTags(keys []string, progress func(i, n int)) FixResult {
	var songs []Track
	for _, t := range m.Songs() {
		if t.Source == "download" && (keys == nil || contains(keys, t.Key)) {
			songs = append(songs, t)
		}
	}
	r := FixResult{Checked: len(songs)}
	for i, t := range songs {
		if progress != nil {
			progress(i+1, len(songs))
		}
		a, ti := FixTags(t.Title, t.Artist)
		if (a != t.Artist || ti != t.Title) && m.SetTags(t.Key, ti, a) {
			r.Renamed++
		}
		frame := t.Art == "" || strings.Contains(t.Art, "ytimg") || strings.Contains(t.Art, "youtube_")
		if !frame {
			continue
		}
		js, err := httpGet("https://itunes.apple.com/search?entity=song&limit=1&term="+url.QueryEscape(a+" "+ti), webUA)
		if err != nil {
			continue
		}
		if art := ItunesArt(js); art != "" {
			f := filepath.Join(m.dir, "art", "cover_"+FolderFor(t.Key)+".jpg")
			if downloadTo(art, f) == nil {
				m.SetArt(t.Key, f)
				r.Covers++
			}
		}
	}
	return r
}

func downloadTo(u, f string) error {
	resp, err := client.Get(u)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil || len(b) < 1000 {
		return errors.New("too small")
	}
	_ = os.MkdirAll(filepath.Dir(f), 0o755)
	return os.WriteFile(f, b, 0o644)
}

// ImportPlaylist: a YouTube playlist (every video) or a Spotify playlist / album (each
// "Artist - Title" found on YouTube, clear matches only) → a Blaze playlist that fills
// as the songs download.
func (m *Media) ImportPlaylist(link string, max int) string {
	ytList, spList, spAlbum := YoutubePlaylistID(link), SpotifyPlaylistID(link), SpotifyAlbumID(link)
	var picks []Item
	var skipped []string
	name := ""
	switch {
	case ytList != "":
		next := ""
		for pages := 0; pages < 8 && len(picks) < max; pages++ {
			js, err := ytPlaylistRaw(ytList, next)
			if err != nil {
				break
			}
			items, cont, title := YtPlaylist(js)
			if name == "" {
				name = title
			}
			for _, it := range items {
				if len(picks) < max {
					picks = append(picks, Item{Source: "youtube", ID: it[0], Title: it[1]})
				}
			}
			if cont == "" {
				break
			}
			next = cont
		}
	case spList != "" || spAlbum != "":
		p := "playlist/" + spList
		if spList == "" {
			p = "album/" + spAlbum
		}
		html, err := httpGet("https://open.spotify.com/embed/"+p, webUA)
		if err != nil {
			return "Couldn't open that Spotify link: " + err.Error()
		}
		names, title := SpotifyEmbed(html)
		name = title
		if len(names) > max {
			names = names[:max]
		}
		for _, q := range names {
			res, err := m.yt.Search("audio", q, "")
			if err != nil {
				skipped = append(skipped, q)
				continue
			}
			items := res.Items
			if len(items) > 6 {
				items = items[:6]
			}
			if r := PickRank(q, "audio", items); len(r) > 0 && PickConfident(r) {
				picks = append(picks, r[0].Item)
			} else {
				skipped = append(skipped, q)
			}
		}
	default:
		return "That isn't a YouTube or Spotify playlist link."
	}
	if len(picks) == 0 {
		if len(skipped) > 0 {
			return "Couldn't read any songs from that playlist (no clear YouTube match for " + itoa(len(skipped)) + ")."
		}
		return "Couldn't read any songs from that playlist. It may be private."
	}
	plName := orStr(name, "Imported playlist")
	id := m.NewPlaylistID()
	m.EditPlaylists(func(l []Playlist, now int64) []Playlist { return PlCreate(l, plName, id, now) })
	have := map[string]Track{}
	for _, t := range m.Songs() {
		if t.VideoID != "" {
			have[strings.TrimPrefix(t.VideoID, "yt:")] = t
		}
	}
	quality := "high"
	if m.Prefs().DataSaver {
		quality = "small"
	}
	queued, already := 0, 0
	var failed []string
	for _, p := range picks {
		if t, ok := have[p.ID]; ok {
			m.EditPlaylists(func(l []Playlist, now int64) []Playlist { return PlAdd(l, id, []string{t.Key}, now) })
			already++
			continue
		}
		d, err := m.yt.Detail(p, "audio")
		if err != nil {
			failed = append(failed, orStr(p.Title, p.ID))
			continue
		}
		ch := PickChoice("audio", m.yt.Choices("audio", d), quality, "")
		if ch == nil {
			failed = append(failed, orStr(p.Title, p.ID))
			continue
		}
		m.ExpectImport(p.ID, id)
		p.Title, p.Author, p.Thumb = orStr(p.Title, d.Title), d.Author, d.Thumb
		m.AddDownloadJob(p, d, *ch, "audio")
		queued++
	}
	s := fmt.Sprintf("Imported \u201c%s\u201d: %d downloading", plName, queued)
	if already > 0 {
		s += fmt.Sprintf(", %d already in your library", already)
	}
	s += ". The playlist fills as they finish."
	if len(skipped) > 0 {
		k := skipped
		if len(k) > 5 {
			k = k[:5]
		}
		s += fmt.Sprintf(" No clear match for %d: %s.", len(skipped), strings.Join(k, "; "))
	}
	if len(failed) > 0 {
		s += fmt.Sprintf(" Couldn't get %d.", len(failed))
	}
	return s
}

// MediaDownload runs media_download (mobile MediaFetch.kt): search YouTube, queue clear
// matches, return candidates for unclear ones so the agent asks the user.
func (m *Media) MediaDownload(a map[string]any) string {
	kind := "audio"
	if argS(a, "kind") == "video" {
		kind = "video"
	}
	quality := strings.ToLower(strings.TrimSpace(argS(a, "quality")))
	format := strings.ToLower(argS(a, "format"))
	raw, _ := a["items"].([]any)
	if s, ok := a["items"].(string); ok { // a model that sent one string: one item per line or ";"
		for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ';' }) {
			if strings.TrimSpace(x) != "" {
				raw = append(raw, strings.TrimSpace(x))
			}
		}
	}
	if len(raw) == 0 {
		return "No items given."
	}
	if len(raw) > 25 {
		raw = raw[:25]
	}
	var queued, unsure, failed []string
	for _, x := range raw {
		var id, q string
		switch v := x.(type) {
		case string:
			q = v
		case map[string]any:
			id, q = strings.TrimSpace(argS(v, "id")), strings.TrimSpace(argS(v, "query"))
		}
		var pick Item
		if id != "" {
			pick = Item{Source: "youtube", ID: id, Title: q}
		} else {
			if q == "" {
				continue
			}
			res, err := m.yt.Search(kind, q, "")
			if err != nil {
				failed = append(failed, "\u201c"+q+"\u201d: "+err.Error())
				continue
			}
			items := res.Items
			if len(items) > 6 {
				items = items[:6]
			}
			r := PickRank(q, kind, items)
			if len(r) == 0 {
				failed = append(failed, "\u201c"+q+"\u201d: nothing found")
				continue
			}
			if !PickConfident(r) {
				var c []string
				for i, x := range r {
					if i >= 3 {
						break
					}
					c = append(c, fmt.Sprintf("%s \u2014 %s (%s) id=%s", x.Item.Title, x.Item.Author, x.Item.DurationText, x.Item.ID))
				}
				unsure = append(unsure, "\u201c"+q+"\u201d: "+strings.Join(c, " | "))
				continue
			}
			pick = r[0].Item
		}
		d, err := m.yt.Detail(pick, kind)
		if err != nil {
			failed = append(failed, "\u201c"+orStr(q, id)+"\u201d: "+err.Error())
			continue
		}
		ch := PickChoice(kind, m.yt.Choices(kind, d), quality, format)
		if ch == nil {
			failed = append(failed, "\u201c"+orStr(q, id)+"\u201d: no downloadable stream")
			continue
		}
		pick.Title, pick.Author, pick.Thumb = orStr(pick.Title, d.Title), orStr(pick.Author, d.Author), orStr(pick.Thumb, d.Thumb)
		job := m.AddDownloadJob(pick, d, *ch, kind)
		queued = append(queued, job.Title+" ("+ch.Label+")")
	}
	app := "Music"
	if kind == "video" {
		app = "Video"
	}
	var b []string
	if len(queued) > 0 {
		b = append(b, fmt.Sprintf("Queued %d in Blaze %s (downloading now): %s.", len(queued), app, strings.Join(queued, "; ")))
	}
	if len(unsure) > 0 {
		b = append(b, fmt.Sprintf("Not sure about %d; ask the user which one (or to skip), then call media_download again with {\"id\":...}:\n- %s", len(unsure), strings.Join(unsure, "\n- ")))
	}
	if len(failed) > 0 {
		b = append(b, "Couldn't get: "+strings.Join(failed, "; ")+".")
	}
	if len(b) == 0 {
		return "Nothing to download."
	}
	return strings.Join(b, "\n")
}

// ---------------------------------------------------------------- video

// SubOption is one subtitle the player can turn on.
type SubOption struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Lang   string `json:"lang"`
	Auto   bool   `json:"auto"`
	Loaded bool   `json:"loaded"` // a local file: /media/sub serves it right away
}

// VideoSubs lists a video's subtitles: saved ones, files next to it, and (online)
// YouTube captions, which are fetched when picked.
func (m *Media) VideoSubs(key string, online bool) []SubOption {
	t, _ := m.Find(key)
	var local []Subtitle
	local = append(local, m.SavedSubs(key)...)
	if t.Path != "" {
		local = append(local, SidecarSubs(t.Path)...)
	}
	pref := m.Prefs().SubsPref
	out := []SubOption{}
	seen := map[string]bool{}
	for _, s := range OrderSubs(local, pref) {
		if !seen[s.ID] {
			seen[s.ID] = true
			out = append(out, SubOption{s.ID, SubLabel(s), s.Lang, s.Auto, true})
		}
	}
	if online && strings.HasPrefix(t.VideoID, "yt:") {
		for _, s := range OrderSubs(YtCaptions(t.VideoID), pref) {
			if !seen[s.ID] {
				seen[s.ID] = true
				out = append(out, SubOption{s.ID, SubLabel(s), s.Lang, s.Auto, false})
			}
		}
	}
	return out
}

// LoadSub makes subtitle id of a video local (fetching a YouTube caption track).
func (m *Media) LoadSub(key, id string) (SubOption, error) {
	for _, o := range m.VideoSubs(key, false) {
		if o.ID == id {
			return o, nil
		}
	}
	t, _ := m.Find(key)
	if !strings.HasPrefix(t.VideoID, "yt:") {
		return SubOption{}, serr("That subtitle track isn't available.")
	}
	for _, s := range YtCaptions(t.VideoID) {
		if s.ID != id {
			continue
		}
		cues := FetchYtSubs(s)
		if len(cues) == 0 {
			return SubOption{}, serr("YouTube didn't send the %s subtitles. Try again, or load a file.", SubLabel(s))
		}
		saved, err := m.SaveSubs(key, s, cues)
		if err != nil {
			return SubOption{}, err
		}
		return SubOption{saved.ID, SubLabel(saved), saved.Lang, saved.Auto, true}, nil
	}
	return SubOption{}, serr("That subtitle track isn't available.")
}

// AddSubFile keeps a .srt/.vtt the user picked with the video.
func (m *Media) AddSubFile(key, path string) (SubOption, error) {
	st, err := os.Stat(path)
	if err != nil {
		return SubOption{}, err
	}
	if st.Size() > 8_000_000 {
		return SubOption{}, serr("That file is too big to be subtitles.")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return SubOption{}, err
	}
	cues := ParseSubs(DecodeText(b))
	name := filepath.Base(path)
	if len(cues) == 0 {
		return SubOption{}, serr("No subtitles found in \u201c%s\u201d. Pick a .srt or .vtt file.", name)
	}
	s, err := m.SaveSubs(key, Subtitle{ID: "file-" + reSafe.ReplaceAllString(name, "_"), Name: name}, cues)
	if err != nil {
		return SubOption{}, err
	}
	return SubOption{s.ID, SubLabel(s), s.Lang, false, true}, nil
}

// someCues is a subtitle track to work from: the one on, else the viewer's language,
// else English, else any (fetching YouTube captions when needed).
func (m *Media) someCues(key, onID string) ([]Cue, bool) {
	t, _ := m.Find(key)
	var local []Subtitle
	local = append(local, m.SavedSubs(key)...)
	if t.Path != "" {
		local = append(local, SidecarSubs(t.Path)...)
	}
	for _, s := range local {
		if s.ID == onID {
			if c := SubCues(s); len(c) > 0 {
				return c, true
			}
		}
	}
	pref := m.Prefs().SubsPref
	if pref == "" || pref == "off" {
		pref = "en"
	}
	for _, s := range OrderSubs(local, pref) {
		if c := SubCues(s); len(c) > 0 {
			return c, true
		}
	}
	if strings.HasPrefix(t.VideoID, "yt:") {
		for _, s := range OrderSubs(YtCaptions(t.VideoID), pref) {
			if c := FetchYtSubs(s); len(c) > 0 {
				_, _ = m.SaveSubs(key, s, c)
				return c, true
			}
		}
	}
	return nil, false
}

// Recap writes "what happened so far" from the subtitles up to posMs.
func (m *Media) Recap(key string, posMs int64, onID string) string {
	t, ok := m.Find(key)
	if !ok {
		return "Nothing is playing."
	}
	cues, ok := m.someCues(key, onID)
	if !ok {
		return "This video has no subtitles, so there's nothing to recap from. Load a .srt or .vtt file first."
	}
	text := Transcript(cues, posMs, 12000)
	if strings.TrimSpace(text) == "" {
		return "Nothing has been said yet at this point."
	}
	if m.Ask == nil {
		return "Blaze couldn't write a recap: no model is set up."
	}
	out, err := m.Ask(RecapPrompt(t.Title, text))
	if err != nil {
		return "Blaze couldn't write a recap: " + err.Error()
	}
	return orStr(strings.TrimSpace(out), "Blaze couldn't write a recap right now. Try again in a moment.")
}

// TranslateSubs AI-translates a video's subtitles into language and saves the track.
func (m *Media) TranslateSubs(key, language, onID string, progress func(i, n int)) (SubOption, string) {
	if _, ok := m.Find(key); !ok {
		return SubOption{}, "Nothing is playing."
	}
	lang := strings.TrimSpace(language)
	if lang == "" {
		return SubOption{}, "Say which language."
	}
	cues, ok := m.someCues(key, onID)
	if !ok {
		return SubOption{}, "This video has no subtitles to translate. Load a .srt or .vtt file first."
	}
	if m.Ask == nil {
		return SubOption{}, "Blaze couldn't translate: no model is set up."
	}
	bs := Batches(cues, 3000)
	var out []Cue
	for i, b := range bs {
		if progress != nil {
			progress(i+1, len(bs))
		}
		reply, err := m.Ask(TranslatePrompt(b, lang))
		if err != nil {
			out = append(out, b...)
			continue
		}
		out = append(out, ApplyTranslation(b, reply)...)
	}
	if TranslatedShare(cues, out) < 0.3 {
		return SubOption{}, "The translation didn't come back. Check the connection and try again."
	}
	code := LangCode(lang)
	name := LangName(code)
	if name == code {
		name = titleCase(lang)
	}
	s, err := m.SaveSubs(key, Subtitle{ID: "ai-" + reSafe.ReplaceAllString(strings.ToLower(lang), "_"), Lang: code, Name: name + " (AI)", Auto: true}, out)
	if err != nil {
		return SubOption{}, err.Error()
	}
	return SubOption{s.ID, SubLabel(s), s.Lang, true, true}, fmt.Sprintf("Subtitles translated to %s and turned on (%d lines).", name, len(out))
}

// VideoOps runs video_ops (mobile VideoTools.kt).
func (m *Media) VideoOps(a map[string]any) string {
	p := m.Player()
	live := p.Kind == "video" && p.Key != "" && time.Since(time.UnixMilli(p.Updated)) < 30*time.Minute
	switch argS(a, "action") {
	case "play":
		v := FindVideo(m.Videos(), argS(a, "query"))
		if v == nil {
			return "No video like \u201c" + argS(a, "query") + "\u201d in the library. Videos come from the user's Videos folder and the Video Store (downloads)."
		}
		m.send(Cmd{Op: "video", Keys: []string{v.Key}, Title: v.Title})
		return "Playing \u201c" + v.Title + "\u201d in Blaze Video."
	case "series":
		s := Series(m.Videos())
		if len(s) == 0 {
			return "No series in the library (episodes are grouped from titles like \u201cShow S01E02\u201d or \u201cShow - Episode 3\u201d)."
		}
		var b []string
		for _, g := range s {
			b = append(b, fmt.Sprintf("%s: %d episodes (%s \u2026 %s)", g.Name, len(g.Tracks), g.Tracks[0].Title, g.Tracks[len(g.Tracks)-1].Title))
		}
		return strings.Join(b, "\n")
	case "subtitles":
		if !live {
			return "Blaze Video isn't playing anything. Play a video first."
		}
		want := orStr(argS(a, "language"), "off")
		if strings.EqualFold(want, "off") {
			m.UpdatePrefs(func(pr *Prefs) { pr.SubsPref = "off" })
			m.send(Cmd{Op: "subs_off"})
			return "Subtitles off."
		}
		code := LangCode(want)
		for _, o := range m.VideoSubs(p.Key, true) {
			if SameLang(o.Lang, code) || strings.Contains(strings.ToLower(o.Label), strings.ToLower(want)) {
				if !o.Loaded {
					l, err := m.LoadSub(p.Key, o.ID)
					if err != nil {
						return err.Error()
					}
					o = l
				}
				m.UpdatePrefs(func(pr *Prefs) { pr.SubsPref = code })
				m.send(Cmd{Op: "subs", Value: o.ID, Title: o.Label})
				return "Turning on " + o.Label + " subtitles."
			}
		}
		return "No " + want + " subtitles for this video. I can translate the ones it has (video_ops translate_subtitles)."
	case "translate_subtitles":
		if !live {
			return "Blaze Video isn't playing anything. Play a video first."
		}
		o, msg := m.TranslateSubs(p.Key, argS(a, "language"), p.SubID, nil)
		if o.ID != "" {
			m.send(Cmd{Op: "subs", Value: o.ID, Title: o.Label})
		}
		return msg
	case "recap":
		if !live {
			return "Blaze Video isn't playing anything. Play a video first."
		}
		return m.Recap(p.Key, p.PosMs, p.SubID)
	case "settings":
		var out []string
		m.UpdatePrefs(func(pr *Prefs) {
			if hasKey(a, "autoplay") {
				v := argB(a, "autoplay")
				pr.Autoplay = &v
				out = append(out, "autoplay "+onOff(v))
			}
			if hasKey(a, "smart_downloads") {
				pr.SmartDownloads = argB(a, "smart_downloads")
				out = append(out, "smart downloads "+onOff(pr.SmartDownloads))
			}
			if hasKey(a, "data_saver") {
				pr.DataSaver = argB(a, "data_saver")
				out = append(out, "data saver "+onOff(pr.DataSaver))
			}
		})
		pr := m.Prefs()
		if len(out) == 0 {
			return "Autoplay " + onOff(pr.AutoplayOn()) + ", smart downloads " + onOff(pr.SmartDownloads) + ", data saver " + onOff(pr.DataSaver) + "."
		}
		return "Video: " + strings.Join(out, ", ") + "."
	}
	return "Unknown action."
}

// SmartCleanup clears watched downloaded episodes a day after they finished (when on).
func (m *Media) SmartCleanup() int {
	p := m.Prefs()
	if !p.SmartDownloads {
		return 0
	}
	m.mu.Lock()
	dls := append([]Track{}, m.downloads...)
	liked := append([]string{}, m.liked...)
	m.mu.Unlock()
	n := 0
	for _, t := range ToClear(dls, p.Finished, m.now(), 24*3600000) {
		if !contains(liked, t.Key) && m.Delete(t.Key, false) == nil {
			n++
		}
	}
	return n
}

// ---------------------------------------------------------------- arg helpers

func argS(a map[string]any, k string) string {
	switch v := a[k].(type) {
	case string:
		return v
	case float64:
		return fmt.Sprint(v)
	case bool:
		return fmt.Sprint(v)
	}
	return ""
}

func argB(a map[string]any, k string) bool {
	switch v := a[k].(type) {
	case bool:
		return v
	case string:
		return v == "true" || v == "yes" || v == "1" || v == "on"
	case float64:
		return v != 0
	}
	return false
}

func argI(a map[string]any, k string, def int) int { return int(argF(a, k, float64(def))) }

func argF(a map[string]any, k string, def float64) float64 {
	switch v := a[k].(type) {
	case float64:
		return v
	case string:
		var f float64
		if _, err := fmt.Sscan(strings.TrimSuffix(strings.TrimSpace(v), "%"), &f); err == nil {
			return f
		}
	}
	return def
}

func hasKey(a map[string]any, k string) bool { _, ok := a[k]; return ok }
