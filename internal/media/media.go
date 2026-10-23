package media

// Media is the running Music + Video service: the library and everything saved with
// it (mobile MediaLib.kt + MediaPrefs.kt + Taste.kt), the Store sources and the
// download queue. One per app; methods are safe from any goroutine.

import (
	"encoding/json"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefs are the Music / Video settings (mobile MediaPrefs + SubsLoader pref).
type Prefs struct {
	Folders        []string         `json:"folders"`          // extra music + video folders to scan
	NoDefaults     bool             `json:"no_defaults"`      // don't scan ~/Music and ~/Videos by default
	EQ             string           `json:"eq"`               // preset name, "Flat" = off
	Crossfade      int              `json:"crossfade_s"`      // 0 = off
	SleepFade      *bool            `json:"sleep_fade"`       // default on
	DJ             bool             `json:"dj"`               // Blaze DJ keeps the music going
	Autoplay       *bool            `json:"autoplay"`         // next episode (default on)
	SmartDownloads bool             `json:"smart_downloads"`  // clear watched downloaded episodes after a day
	DataSaver      bool             `json:"data_saver"`       // smallest streams (desktop: off by default; no metered detection)
	SubsPref       string           `json:"subs_pref"`        // "" never chosen, "off", or a language code
	Intros         map[string]int64 `json:"intros"`           // series key → intro end (ms)
	Finished       map[string]int64 `json:"finished"`         // video key → watched to the end at
	Volume         float64          `json:"volume,omitempty"` // 0..1 (0 = default 1)
}

func (p Prefs) SleepFadeOn() bool { return p.SleepFade == nil || *p.SleepFade }
func (p Prefs) AutoplayOn() bool  { return p.Autoplay == nil || *p.Autoplay }

// Media is the service.
type Media struct {
	dir string

	mu           sync.Mutex
	prefs        Prefs
	downloads    []Track // what Blaze downloaded (library.json)
	scanned      []Track // the user's folders (scan.json cache)
	playlists    []Playlist
	recent       []string
	recentVideos []string
	liked        []string
	plays        []PlayEvent
	positions    map[string]int64
	art          map[string]string    // key → cover file (tag fixer)
	tags         map[string][2]string // key → (title, artist) for folder files (tag fixer, renames)
	taste        []TasteEvent
	sinceMemory  int
	imports      map[string]string // video id → playlist id
	scanning     bool
	lastScan     int64

	dl      *downloader
	sources []Source
	yt      *youtubeSource
	link    *youtubeSource
	jamendo *jamendoSource

	streams *streamTable
	player  PlayerState

	// Hooks set by the host app.
	Emit     func(event string, data any)        // tell the UI (Wails events)
	Ask      func(prompt string) (string, error) // one model call (recap, translation)
	Remember func(subject, line string)          // keep the taste line in Blaze's memory
	Notify   func(title, body string)            // a desktop notification (download done)
	Now      func() time.Time                    // tests
	HomeDir  string                              // tests ("" = os.UserHomeDir)
	ffmpeg   string
}

// PlayerState is what the webview player reports (for the agent's "what's playing").
type PlayerState struct {
	Kind    string   `json:"kind"` // audio | video | ""
	Key     string   `json:"key"`
	Title   string   `json:"title"`
	Artist  string   `json:"artist"`
	Playing bool     `json:"playing"`
	PosMs   int64    `json:"pos_ms"`
	DurMs   int64    `json:"dur_ms"`
	Index   int      `json:"index"`
	Count   int      `json:"count"`
	Shuffle bool     `json:"shuffle"`
	Repeat  string   `json:"repeat"`
	Volume  float64  `json:"volume"`
	SubID   string   `json:"sub_id"`
	UpNext  []string `json:"up_next"`
	Updated int64    `json:"updated"`
}

// New opens (or creates) dir and loads everything saved there.
func New(dir string) *Media {
	m := &Media{dir: dir, positions: map[string]int64{}, art: map[string]string{}, tags: map[string][2]string{}, imports: map[string]string{}, streams: newStreamTable()}
	_ = os.MkdirAll(dir, 0o755)
	m.readJSON("prefs.json", &m.prefs)
	m.readJSON("library.json", &m.downloads)
	m.readJSON("scan.json", &m.scanned)
	m.readJSON("playlists.json", &m.playlists)
	m.readJSON("recent.json", &m.recent)
	m.readJSON("recent-videos.json", &m.recentVideos)
	m.readJSON("liked.json", &m.liked)
	m.readJSON("plays.json", &m.plays)
	m.readJSON("positions.json", &m.positions)
	m.readJSON("art.json", &m.art)
	m.readJSON("tags.json", &m.tags)
	m.readJSON("taste.json", &m.taste)
	m.readJSON("imports.json", &m.imports)
	if m.positions == nil {
		m.positions = map[string]int64{}
	}
	if m.art == nil {
		m.art = map[string]string{}
	}
	if m.tags == nil {
		m.tags = map[string][2]string{}
	}
	if m.imports == nil {
		m.imports = map[string]string{}
	}
	if m.prefs.EQ == "" {
		m.prefs.EQ = "Flat"
	}
	m.ffmpeg, _ = exec.LookPath("ffmpeg")
	m.yt = &youtubeSource{canMerge: m.CanMerge}
	m.link = &youtubeSource{canMerge: m.CanMerge, link: true}
	m.jamendo = newJamendo(dir)
	m.sources = append([]Source{m.yt, m.jamendo}, newArchiveSources()...)
	m.sources = append(m.sources, m.link)
	m.dl = newDownloader(m)
	return m
}

// Dir is ~/.blaze/media.
func (m *Media) Dir() string { return m.dir }

// CanMerge: YouTube's separate video + audio streams need ffmpeg on the desktop.
func (m *Media) CanMerge() bool { return m.ffmpeg != "" }

func (m *Media) now() int64 {
	if m.Now != nil {
		return m.Now().UnixMilli()
	}
	return time.Now().UnixMilli()
}

func (m *Media) emit(ev string, data any) {
	if m.Emit != nil {
		m.Emit(ev, data)
	}
}

func (m *Media) readJSON(name string, v any) {
	if b, err := os.ReadFile(filepath.Join(m.dir, name)); err == nil {
		_ = json.Unmarshal(b, v)
	}
}

// writeJSON saves atomically (tmp + rename). It doesn't lock.
func (m *Media) writeJSON(name string, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	f := filepath.Join(m.dir, name)
	tmp := f + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, f)
	}
}

func (m *Media) home() string {
	if m.HomeDir != "" {
		return m.HomeDir
	}
	h, _ := os.UserHomeDir()
	return h
}

// MusicDir and VideoDir are where downloads land (mobile: Music/Blaze/Music, Movies/Blaze/Videos).
func (m *Media) MusicDir() string { return filepath.Join(m.home(), "Music", "Blaze", "Music") }
func (m *Media) VideoDir() string {
	v := "Videos"
	if runtime.GOOS == "darwin" {
		v = "Movies"
	}
	return filepath.Join(m.home(), v, "Blaze", "Videos")
}

// Folders are the folders scanned: ~/Music and ~/Videos (or ~/Movies) unless turned
// off, the user's own, and always Blaze's download folders.
func (m *Media) Folders() []string {
	m.mu.Lock()
	p := m.prefs
	m.mu.Unlock()
	var out []string
	seen := map[string]bool{}
	add := func(d string) {
		d = filepath.Clean(d)
		if d != "." && !seen[d] {
			seen[d] = true
			out = append(out, d)
		}
	}
	if !p.NoDefaults {
		add(filepath.Join(m.home(), "Music"))
		if runtime.GOOS == "darwin" {
			add(filepath.Join(m.home(), "Movies"))
		} else {
			add(filepath.Join(m.home(), "Videos"))
		}
	}
	for _, f := range p.Folders {
		if strings.TrimSpace(f) != "" {
			add(f)
		}
	}
	add(m.MusicDir())
	add(m.VideoDir())
	return out
}

// ---- prefs ----

func (m *Media) Prefs() Prefs { m.mu.Lock(); defer m.mu.Unlock(); return m.prefs }

// UpdatePrefs changes settings under the lock and saves them.
func (m *Media) UpdatePrefs(fn func(p *Prefs)) Prefs {
	m.mu.Lock()
	fn(&m.prefs)
	p := m.prefs
	m.writeJSON("prefs.json", p)
	m.mu.Unlock()
	m.emit("media:prefs", p)
	return p
}

// ---- the library ----

func (m *Media) withArt(t Track) Track {
	if a, ok := m.art[t.Key]; ok {
		if _, err := os.Stat(a); err == nil {
			t.Art = a
		}
	}
	if tg, ok := m.tags[t.Key]; ok {
		t.Title, t.Artist = orStr(tg[0], t.Title), orStr(tg[1], t.Artist)
	}
	return t
}

// all is downloads + scanned, deduped by key (a download's own data wins), art applied.
func (m *Media) all() []Track {
	seen := map[string]bool{}
	var out []Track
	for _, t := range m.downloads {
		if !seen[t.Key] {
			seen[t.Key] = true
			out = append(out, m.withArt(t))
		}
	}
	for _, t := range m.scanned {
		if !seen[t.Key] {
			seen[t.Key] = true
			out = append(out, m.withArt(t))
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].AddedAt > out[j].AddedAt })
	return out
}

func ofKind(ts []Track, kind string) []Track {
	out := []Track{}
	for _, t := range ts {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out
}

// Songs and Videos are the library, newest first.
func (m *Media) Songs() []Track  { m.mu.Lock(); defer m.mu.Unlock(); return ofKind(m.all(), "audio") }
func (m *Media) Videos() []Track { m.mu.Lock(); defer m.mu.Unlock(); return ofKind(m.all(), "video") }

// Find is a library track by key.
func (m *Media) Find(key string) (Track, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.findLocked(key)
}

func (m *Media) findLocked(key string) (Track, bool) {
	for _, t := range m.downloads {
		if t.Key == key {
			return m.withArt(t), true
		}
	}
	for _, t := range m.scanned {
		if t.Key == key {
			return m.withArt(t), true
		}
	}
	return Track{}, false
}

// Library is everything the views draw from.
type Library struct {
	Songs        []Track          `json:"songs"`
	Videos       []Track          `json:"videos"`
	Playlists    []Playlist       `json:"playlists"`
	Liked        []string         `json:"liked"`
	Recent       []string         `json:"recent"`
	RecentVideos []string         `json:"recent_videos"`
	Positions    map[string]int64 `json:"positions"`
	Folders      []string         `json:"folders"`
	Scanning     bool             `json:"scanning"`
	LastScan     int64            `json:"last_scan"`
	Prefs        Prefs            `json:"prefs"`
	CanMerge     bool             `json:"can_merge"`
	MusicDir     string           `json:"music_dir"`
	VideoDir     string           `json:"video_dir"`
}

func (m *Media) Library() Library {
	folders := m.Folders()
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.all()
	pos := map[string]int64{}
	for k, v := range m.positions {
		pos[k] = v
	}
	return Library{Songs: ofKind(all, "audio"), Videos: ofKind(all, "video"), Playlists: nonNil(m.playlists), Liked: nonNilS(m.liked),
		Recent: nonNilS(m.recent), RecentVideos: nonNilS(m.recentVideos), Positions: pos, Folders: folders, Scanning: m.scanning,
		LastScan: m.lastScan, Prefs: m.prefs, CanMerge: m.ffmpeg != "", MusicDir: m.MusicDir(), VideoDir: m.VideoDir()}
}

func nonNil(p []Playlist) []Playlist {
	if p == nil {
		return []Playlist{}
	}
	return p
}
func nonNilS(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func (m *Media) changed() { m.emit("media:library", nil) }

// AddDownload puts a finished download in the library (and its import playlist).
func (m *Media) AddDownload(t Track) {
	m.mu.Lock()
	l := []Track{t}
	for _, x := range m.downloads {
		if x.Key != t.Key {
			l = append(l, x)
		}
	}
	m.downloads = l
	m.writeJSON("library.json", m.downloads)
	vid := strings.TrimPrefix(t.VideoID, "yt:")
	if pl, ok := m.imports[vid]; ok {
		delete(m.imports, vid)
		m.writeJSON("imports.json", m.imports)
		m.playlists = PlAdd(m.playlists, pl, []string{t.Key}, m.now())
		m.writeJSON("playlists.json", m.playlists)
	}
	m.mu.Unlock()
	m.changed()
}

// ExpectImport: a song queued by a playlist import lands in that playlist.
func (m *Media) ExpectImport(videoID, playlistID string) {
	m.mu.Lock()
	m.imports[strings.TrimPrefix(videoID, "yt:")] = playlistID
	m.writeJSON("imports.json", m.imports)
	m.mu.Unlock()
}

// Delete removes a file from the disk and the library. Files in the user's own folders
// need allowFolder (the UI asks first); Blaze's downloads don't.
func (m *Media) Delete(key string, allowFolder bool) error {
	m.mu.Lock()
	t, ok := m.findLocked(key)
	m.mu.Unlock()
	if !ok {
		return serr("That isn't in the library.")
	}
	if t.Source != "download" && !allowFolder {
		return serr("That file is in your own folders; Blaze only deletes it when you confirm.")
	}
	if t.Path != "" {
		if err := os.Remove(t.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	m.mu.Lock()
	m.downloads = dropKey(m.downloads, key)
	m.scanned = dropKey(m.scanned, key)
	m.playlists = PlForget(m.playlists, key)
	m.liked = dropS(m.liked, key)
	m.writeJSON("library.json", m.downloads)
	m.writeJSON("scan.json", m.scanned)
	m.writeJSON("playlists.json", m.playlists)
	m.writeJSON("liked.json", m.liked)
	m.mu.Unlock()
	m.changed()
	return nil
}

func dropKey(l []Track, key string) []Track {
	out := []Track{}
	for _, t := range l {
		if t.Key != key {
			out = append(out, t)
		}
	}
	return out
}

func dropS(l []string, s string) []string {
	out := []string{}
	for _, x := range l {
		if x != s {
			out = append(out, x)
		}
	}
	return out
}

// ---- playlists ----

func (m *Media) Playlists() []Playlist { m.mu.Lock(); defer m.mu.Unlock(); return nonNil(m.playlists) }

// EditPlaylists applies one playlist change and saves.
func (m *Media) EditPlaylists(fn func(l []Playlist, now int64) []Playlist) []Playlist {
	m.mu.Lock()
	m.playlists = fn(m.playlists, m.now())
	l := nonNil(m.playlists)
	m.writeJSON("playlists.json", l)
	m.mu.Unlock()
	m.changed()
	return l
}

// NewPlaylistID is "pl" + base36 time + 3 digits (mobile's format).
func (m *Media) NewPlaylistID() string {
	return "pl" + strconv.FormatInt(m.now(), 36) + itoa(100+rand.Intn(900))
}

// ---- liked ----

func (m *Media) ToggleLike(key string) bool {
	m.mu.Lock()
	on := !contains(m.liked, key)
	if on {
		m.liked = append([]string{key}, m.liked...)
	} else {
		m.liked = dropS(m.liked, key)
	}
	m.writeJSON("liked.json", m.liked)
	m.mu.Unlock()
	m.changed()
	return on
}

// ---- tags / art ----

// SetTags renames a track (kept in tags.json; the user's files are never rewritten).
func (m *Media) SetTags(key, title, artist string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.findLocked(key); !ok {
		return false
	}
	hit := false
	for i, t := range m.downloads {
		if t.Key == key {
			m.downloads[i].Title, m.downloads[i].Artist = title, artist
			hit = true
		}
	}
	if hit {
		m.writeJSON("library.json", m.downloads)
	} else {
		m.tags[key] = [2]string{title, artist}
		m.writeJSON("tags.json", m.tags)
	}
	return true
}

// SetArt gives a track a cover file.
func (m *Media) SetArt(key, path string) {
	m.mu.Lock()
	m.art[key] = path
	m.writeJSON("art.json", m.art)
	m.mu.Unlock()
	m.changed()
}

// ---- play log, recents, positions, taste ----

// Played is a track starting (recents + taste).
func (m *Media) Played(key string) {
	m.mu.Lock()
	t, ok := m.findLocked(key)
	if !ok {
		m.mu.Unlock()
		return
	}
	if t.Kind == "video" {
		m.recentVideos = PushRecent(m.recentVideos, key, 50)
		m.writeJSON("recent-videos.json", m.recentVideos)
	} else {
		m.recent = PushRecent(m.recent, key, 50)
		m.writeJSON("recent.json", m.recent)
	}
	m.mu.Unlock()
	m.NoteTaste(t.Kind, t.Title, t.Artist, TastePlay)
}

// LogPlay adds ms of listening (under 3 s and streams are ignored).
func (m *Media) LogPlay(key string, at, ms int64) {
	if ms < 3000 || strings.HasPrefix(key, "stream:") {
		return
	}
	m.mu.Lock()
	m.plays = append(m.plays, PlayEvent{key, at, ms})
	if len(m.plays) > 6000 {
		m.plays = m.plays[len(m.plays)-6000:]
	}
	m.writeJSON("plays.json", m.plays)
	m.mu.Unlock()
}

// Duration teaches the library a track's length (the scanner doesn't read it).
func (m *Media) Duration(key string, ms int64) {
	if ms <= 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.scanned {
		if m.scanned[i].Key == key && m.scanned[i].DurationMs != ms {
			m.scanned[i].DurationMs = ms
			m.writeJSON("scan.json", m.scanned)
		}
	}
	for i := range m.downloads {
		if m.downloads[i].Key == key && m.downloads[i].DurationMs <= 0 {
			m.downloads[i].DurationMs = ms
			m.writeJSON("library.json", m.downloads)
		}
	}
}

// Position is where a video was left (0 = from the start).
func (m *Media) Position(key string) int64 { m.mu.Lock(); defer m.mu.Unlock(); return m.positions[key] }

// SavePosition keeps a resume point (finished videos are forgotten and marked watched).
func (m *Media) SavePosition(key string, pos, dur int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if KeepPosition(pos, dur) {
		m.positions[key] = pos
	} else {
		delete(m.positions, key)
	}
	if len(m.positions) > 300 {
		n := len(m.positions) - 300
		for k := range m.positions {
			if n == 0 {
				break
			}
			delete(m.positions, k)
			n--
		}
	}
	m.writeJSON("positions.json", m.positions)
	if dur > 0 && pos >= dur-10000 {
		if m.prefs.Finished == nil {
			m.prefs.Finished = map[string]int64{}
		}
		m.prefs.Finished[key] = m.now()
		m.writeJSON("prefs.json", m.prefs)
	}
}

// NoteTaste records a play or download for the Store ideas and the memory line.
func (m *Media) NoteTaste(kind, title, author string, weight int) {
	m.mu.Lock()
	now := m.now()
	if n := len(m.taste); n > 0 && m.taste[n-1].Title == title && m.taste[n-1].Kind == kind && now-m.taste[n-1].At < 60000 {
		m.mu.Unlock()
		return
	}
	if r := []rune(title); len(r) > 160 {
		title = string(r[:160])
	}
	if r := []rune(author); len(r) > 80 {
		author = string(r[:80])
	}
	m.taste = PushTaste(m.taste, TasteEvent{kind, title, author, weight, now})
	m.writeJSON("taste.json", m.taste)
	m.sinceMemory++
	line := ""
	if m.sinceMemory >= 5 {
		m.sinceMemory = 0
		line = TasteSummary(m.taste, now)
	}
	m.mu.Unlock()
	if line != "" && m.Remember != nil {
		m.Remember(TasteSubject, line)
	}
}

// Ideas are the Store's scrollable ideas for kind.
func (m *Media) Ideas(kind string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return Ideas(m.taste, kind, m.now(), 12)
}

// ---- smart ----

// Mixes are the daily mixes + smart playlists.
func (m *Media) Mixes() []SmartList {
	m.mu.Lock()
	defer m.mu.Unlock()
	songs := ofKind(m.all(), "audio")
	now := m.now()
	out := append(DailyMixes(songs, m.plays, now/day, 25), SmartLists(songs, m.plays, m.liked, now)...)
	if out == nil {
		return []SmartList{}
	}
	return out
}

// DJ picks n songs to keep the music going.
func (m *Media) DJ(n int) []Track {
	m.mu.Lock()
	defer m.mu.Unlock()
	liked := map[string]bool{}
	for _, k := range m.liked {
		liked[k] = true
	}
	return DJPicks(ofKind(m.all(), "audio"), m.plays, liked, m.recent, n, time.Now().UnixNano())
}

// WrappedFor is Wrapped for "month" | "year" | "all" in local time.
func (m *Media) WrappedFor(period string) *Wrapped {
	now := time.UnixMilli(m.now())
	var from int64
	label := "All time"
	switch period {
	case "month":
		from = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).UnixMilli()
		label = "This month"
	case "all":
	default:
		from = time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location()).UnixMilli()
		label = itoa(now.Year()) + " so far"
	}
	m.mu.Lock()
	songs := ofKind(m.all(), "audio")
	plays := append([]PlayEvent{}, m.plays...)
	m.mu.Unlock()
	return MakeWrapped(plays, songs, from, now.UnixMilli()+1, label, func(at int64) int { return time.UnixMilli(at).Hour() })
}

// ---- player state (reported by the webview) ----

func (m *Media) SetPlayer(s PlayerState) {
	m.mu.Lock()
	s.Updated = m.now()
	m.player = s
	m.mu.Unlock()
}

func (m *Media) Player() PlayerState { m.mu.Lock(); defer m.mu.Unlock(); return m.player }

// Sources lists the Store sources for kind (the Link source isn't a chip).
func (m *Media) Sources(kind string) []SourceInfo {
	out := []SourceInfo{}
	for _, s := range m.sources {
		if s == Source(m.link) {
			continue
		}
		if contains(s.Kinds(), kind) {
			out = append(out, SourceInfo{s.ID(), s.Name(), s.Kinds(), s.SetupNeeded(), s.Browsable()})
		}
	}
	return out
}

// Source is a source by id.
func (m *Media) Source(id string) Source {
	for _, s := range m.sources {
		if s.ID() == id {
			return s
		}
	}
	return nil
}

// JamendoSet checks and saves a Jamendo client_id.
func (m *Media) JamendoSet(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return "Error: give client_id."
	}
	if !m.jamendo.Works(id) {
		return "Jamendo didn't accept that client_id. Check it on " + JamendoDevPortal + " (My applications) and try again."
	}
	m.jamendo.SetClientID(id)
	return "Saved. The Jamendo Store in Blaze Music works now."
}
