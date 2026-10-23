package main

// v0.19 Blaze Music and Blaze Video, ported from Blaze Mobile (mediaapps/). The
// service is internal/media (library, Store, downloads, lyrics, subtitles, smarts);
// this file binds it to the window (Media* methods, "media:*" events), serves files
// to the webview (the asset server's Handler and a loopback server, see
// internal/media/serve.go) and registers the agent's tools: music_play (new on the
// desktop: play / queue / pause / next / "play something chill"), music_ops,
// video_ops, media_download and jamendo_setup (same as mobile).
//
// Nothing here runs until the UI (music.js) or a tool first asks: mediaGet starts the
// service lazily, so app.go needs no startup hook. State lives in ~/.blaze/media.

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/media"
	"blaze-desktop/internal/mind"
	"blaze-desktop/internal/notify"
	"blaze-desktop/internal/tools"
)

var (
	mediaOnce sync.Once
	mediaSvc  *media.Media
	mediaSrv  *media.Server
)

// mediaGet starts the media service on first use.
func mediaGet() *media.Media {
	mediaOnce.Do(func() {
		m := media.New(filepath.Join(config.Dir(), "media"))
		m.Emit = func(ev string, data any) {
			if a := theApp; a != nil && a.ctx != nil {
				wr.EventsEmit(a.ctx, ev, data)
			}
		}
		m.Ask = func(prompt string) (string, error) {
			a := theApp
			if a == nil || a.cfg == nil {
				return "", errNoModel
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			r, err := llm.CompleteWith(ctx, a.cfg, []llm.Message{{Role: "user", Content: prompt}}, nil, llm.Opts{})
			if err != nil {
				return "", err
			}
			return r.Text, nil
		}
		m.Remember = func(subject, line string) {
			if d := mindDB(); d != nil && theApp != nil {
				theApp.mindSave(d, mind.Memory{Kind: "preference", Subject: subject, Text: line})
			}
		}
		m.Notify = func(title, body string) {
			if a := theApp; a != nil {
				_, _ = a.send(notify.Note{Title: title, Body: body, Urgency: "low", Kind: "job"})
			}
		}
		if s, err := m.Listen(); err == nil {
			mediaSrv = s
		}
		mediaSvc = m
		m.StartDownloads()
	})
	return mediaSvc
}

type mediaErr string

func (e mediaErr) Error() string { return string(e) }

const errNoModel = mediaErr("no model is set up yet")

// mediaHandler is the Wails asset server's fallback: /media/* from the library.
func mediaHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/media/") {
			http.NotFound(w, r)
			return
		}
		mediaGet().Handler("").ServeHTTP(w, r)
	})
}

// MediaInfo is what the player needs to start.
type MediaInfo struct {
	Base   string    `json:"base"`  // "http://127.0.0.1:port" ("" = same origin via the asset server)
	Token  string    `json:"token"` // ?t= for the loopback server
	EQ     []string  `json:"eq"`    // preset names in order
	Bands  []int     `json:"bands"`
	Sleep  []int     `json:"sleep"`
	Speeds []float64 `json:"speeds"`
}

// MediaInit starts the service (scan when stale, resume downloads, smart cleanup).
func (a *App) MediaInit() MediaInfo {
	m := mediaGet()
	lib := m.Library()
	if lib.LastScan == 0 || time.Since(time.UnixMilli(lib.LastScan)) > 10*time.Minute {
		m.Scan()
	}
	go m.SmartCleanup()
	info := MediaInfo{EQ: media.EQOrder, Bands: media.EQBands, Sleep: media.SleepPresetsMin, Speeds: media.Speeds}
	if mediaSrv != nil {
		info.Base, info.Token = mediaSrv.Base, mediaSrv.Token
	}
	return info
}

// MediaEQ is a preset's gains (dB) at the player's bands.
func (a *App) MediaEQ(preset string) []float64 {
	mb := media.BandLevels(preset, media.EQBands, -1500, 1500)
	out := make([]float64, len(mb))
	for i, v := range mb {
		out[i] = float64(v) / 100
	}
	return out
}

func (a *App) MediaLibrary() media.Library { return mediaGet().Library() }
func (a *App) MediaScan()                  { mediaGet().Scan() }

// MediaAddFolder asks for a folder and adds it to the scan.
func (a *App) MediaAddFolder() []string {
	d, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Add a music or video folder"})
	m := mediaGet()
	if err == nil && d != "" {
		p := m.Prefs()
		m.SetFolders(append(p.Folders, d), p.NoDefaults)
	}
	return m.Folders()
}

// MediaRemoveFolder drops one of the user's folders (the defaults turn off together).
func (a *App) MediaRemoveFolder(dir string) []string {
	m := mediaGet()
	p := m.Prefs()
	var keep []string
	for _, f := range p.Folders {
		if f != dir {
			keep = append(keep, f)
		}
	}
	m.SetFolders(keep, p.NoDefaults)
	return m.Folders()
}

// MediaDefaultFolders turns scanning ~/Music and ~/Videos on or off.
func (a *App) MediaDefaultFolders(on bool) []string {
	m := mediaGet()
	m.SetFolders(m.Prefs().Folders, !on)
	return m.Folders()
}

func (a *App) MediaMixes() []media.SmartList             { return mediaGet().Mixes() }
func (a *App) MediaDJ(n int) []media.Track               { return mediaGet().DJ(n) }
func (a *App) MediaWrapped(period string) *media.Wrapped { return mediaGet().WrappedFor(period) }
func (a *App) MediaLike(key string) bool                 { return mediaGet().ToggleLike(key) }
func (a *App) MediaPlayed(key string)                    { mediaGet().Played(key) }
func (a *App) MediaLogPlay(key string, at, ms int64)     { mediaGet().LogPlay(key, at, ms) }
func (a *App) MediaDuration(key string, ms int64)        { mediaGet().Duration(key, ms) }
func (a *App) MediaSavePosition(key string, pos, dur int64) {
	mediaGet().SavePosition(key, pos, dur)
}
func (a *App) MediaReport(s media.PlayerState) { mediaGet().SetPlayer(s) }
func (a *App) MediaIdeas(kind string) []string { return mediaGet().Ideas(kind) }

// ---- playlists ----

func (a *App) MediaPlaylistCreate(name string, keys []string) []media.Playlist {
	m := mediaGet()
	id := m.NewPlaylistID()
	return m.EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist {
		return media.PlAdd(media.PlCreate(l, name, id, now), id, keys, now)
	})
}
func (a *App) MediaPlaylistRename(id, name string) []media.Playlist {
	return mediaGet().EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist { return media.PlRename(l, id, name, now) })
}
func (a *App) MediaPlaylistDelete(id string) []media.Playlist {
	return mediaGet().EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist { return media.PlDelete(l, id) })
}
func (a *App) MediaPlaylistAdd(id string, keys []string) []media.Playlist {
	return mediaGet().EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist { return media.PlAdd(l, id, keys, now) })
}
func (a *App) MediaPlaylistRemoveAt(id string, i int) []media.Playlist {
	return mediaGet().EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist { return media.PlRemoveAt(l, id, i, now) })
}
func (a *App) MediaPlaylistMove(id string, from, to int) []media.Playlist {
	return mediaGet().EditPlaylists(func(l []media.Playlist, now int64) []media.Playlist { return media.PlMove(l, id, from, to, now) })
}

// MediaDelete removes a file; files in the user's own folders need confirmed=true (the UI asks).
func (a *App) MediaDelete(key string, confirmed bool) string {
	if err := mediaGet().Delete(key, confirmed); err != nil {
		return err.Error()
	}
	return ""
}

// MediaRename sets a track's title and artist (kept by Blaze; files aren't rewritten).
func (a *App) MediaRename(key, title, artist string) bool {
	return mediaGet().SetTags(key, strings.TrimSpace(title), strings.TrimSpace(artist))
}

// LyricsView is lyrics or why there are none.
type LyricsView struct {
	media.LyricsResult
	Error string `json:"error"`
}

func (a *App) MediaLyrics(key string) LyricsView {
	m := mediaGet()
	t, ok := m.Find(key)
	if !ok {
		return LyricsView{Error: "This song isn't in the library."}
	}
	r, err := m.Lyrics(t)
	if err != nil {
		return LyricsView{Error: "Couldn't load lyrics (" + err.Error() + ")."}
	}
	return LyricsView{LyricsResult: r}
}

// MediaSetPref changes one setting: eq, crossfade, sleep_fade, dj, autoplay,
// smart_downloads, data_saver, subs_pref, volume.
func (a *App) MediaSetPref(name, value string) media.Prefs {
	on := value == "true" || value == "on" || value == "1"
	return mediaGet().UpdatePrefs(func(p *media.Prefs) {
		switch name {
		case "eq":
			p.EQ = value
		case "crossfade":
			p.Crossfade = atoiSafe(value)
		case "sleep_fade":
			p.SleepFade = &on
		case "dj":
			p.DJ = on
		case "autoplay":
			p.Autoplay = &on
		case "smart_downloads":
			p.SmartDownloads = on
		case "data_saver":
			p.DataSaver = on
		case "subs_pref":
			p.SubsPref = value
		case "volume":
			p.Volume = float64(atoiSafe(value)) / 100
		}
	})
}

func atoiSafe(s string) int {
	n := 0
	for _, c := range strings.TrimSpace(s) {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// ---- Store + downloads ----

func (a *App) MediaSources(kind string) []media.SourceInfo { return mediaGet().Sources(kind) }
func (a *App) MediaStoreSearch(kind, source, query, next string) media.StoreResult {
	return mediaGet().StoreSearch(kind, source, query, next)
}
func (a *App) MediaStoreOpen(kind string, it media.Item) media.StoreItemView {
	return mediaGet().StoreOpen(kind, it)
}

// MediaStoreDownload queues a download ("" = queued, else the error).
func (a *App) MediaStoreDownload(kind string, it media.Item, choiceID string) string {
	if _, err := mediaGet().StoreDownload(kind, it, choiceID); err != nil {
		return err.Error()
	}
	return ""
}
func (a *App) MediaDownloads() []media.DlJob    { return mediaGet().Downloads() }
func (a *App) MediaDlPause(id string)           { mediaGet().PauseDownload(id) }
func (a *App) MediaDlResume(id string)          { mediaGet().ResumeDownload(id) }
func (a *App) MediaDlCancel(id string)          { mediaGet().CancelDownload(id) }
func (a *App) MediaDlDismiss(id string)         { mediaGet().DismissDownload(id) }
func (a *App) MediaJamendoSet(id string) string { return mediaGet().JamendoSet(id) }
func (a *App) MediaImport(link string) string   { return mediaGet().ImportPlaylist(link, 100) }
func (a *App) MediaFixTags() string {
	r := mediaGet().FixTags(nil, nil)
	return "Checked " + itoaM(r.Checked) + " downloaded songs: " + itoaM(r.Renamed) + " renamed, " + itoaM(r.Covers) + " got real covers."
}

func itoaM(n int) string {
	if n == 0 {
		return "0"
	}
	s := ""
	for n > 0 {
		s = string(rune('0'+n%10)) + s
		n /= 10
	}
	return s
}

// ---- video ----

func (a *App) MediaSeries() []media.Group { return media.Series(mediaGet().Videos()) }
func (a *App) MediaNextEpisode(key string) *media.Track {
	m := mediaGet()
	t, ok := m.Find(key)
	if !ok {
		return nil
	}
	return media.NextEpisode(t, m.Videos())
}
func (a *App) MediaVideoSubs(key string, online bool) []media.SubOption {
	return mediaGet().VideoSubs(key, online)
}

// SubResult is a subtitle track ready to load, or why not.
type SubResult struct {
	Sub   media.SubOption `json:"sub"`
	Error string          `json:"error"`
	Note  string          `json:"note"`
}

func (a *App) MediaLoadSub(key, id string) SubResult {
	o, err := mediaGet().LoadSub(key, id)
	if err != nil {
		return SubResult{Error: err.Error()}
	}
	return SubResult{Sub: o}
}

// MediaPickSubFile asks for a .srt/.vtt and keeps it with the video.
func (a *App) MediaPickSubFile(key string) SubResult {
	f, err := wr.OpenFileDialog(a.ctx, wr.OpenDialogOptions{Title: "Load subtitles", Filters: []wr.FileFilter{{DisplayName: "Subtitles (*.srt, *.vtt)", Pattern: "*.srt;*.vtt"}}})
	if err != nil || f == "" {
		return SubResult{}
	}
	o, err := mediaGet().AddSubFile(key, f)
	if err != nil {
		return SubResult{Error: err.Error()}
	}
	return SubResult{Sub: o}
}

func (a *App) MediaRecap(key string, posMs int64, subID string) string {
	return mediaGet().Recap(key, posMs, subID)
}

func (a *App) MediaTranslate(key, language, subID string) SubResult {
	m := mediaGet()
	o, msg := m.TranslateSubs(key, language, subID, func(i, n int) {
		m.Emit("media:work", "Translating subtitles to "+language+" · "+itoaM(i)+" of "+itoaM(n))
	})
	m.Emit("media:work", "")
	if o.ID == "" {
		return SubResult{Error: msg}
	}
	return SubResult{Sub: o, Note: msg}
}

// MediaIntro learns (or, with to=-1, sets from) a series' intro end; returns it.
func (a *App) MediaIntro(series string, from, to int64) int64 {
	m := mediaGet()
	var out int64
	m.UpdatePrefs(func(p *media.Prefs) {
		if p.Intros == nil {
			p.Intros = map[string]int64{}
		}
		if from < 0 {
			p.Intros[series] = to
		} else {
			p.Intros[series] = media.LearnIntro(p.Intros[series], from, to)
		}
		if p.Intros[series] <= 0 {
			delete(p.Intros, series)
		}
		out = p.Intros[series]
	})
	return out
}

// ---- agent tools ----

func init() {
	tools.Register("music_play", "Blaze Music player on this PC (the music library from the user's Music folder and Blaze downloads): play songs by title, artist, album, playlist or mood (\"play something chill\", \"play Burna Boy\", playlist=\"Daily Mix 1\"), queue more, pause, resume, next, previous, stop, shuffle, repeat, volume, seek, or now (what's playing). Use it for any music playback request; it opens nothing and plays in Blaze.",
		[][3]string{{"action", "string", "play | queue | pause | resume | toggle | next | previous | stop | shuffle | repeat | volume | seek | now"},
			{"query", "string", "what to play: words from a title / artist / album, or a mood like chill, focus, party, workout, sad, gospel"},
			{"playlist", "string", "a playlist, daily mix or smart playlist name instead of query"},
			{"shuffle", "boolean", "shuffle what plays"}, {"on", "boolean", "for shuffle"}, {"mode", "string", "repeat: off | all | one"},
			{"level", "number", "volume 0-100"}, {"seconds", "number", "seek position"}},
		[]string{"action"}, func(e *tools.Env, args map[string]any) string { return mediaGet().MusicPlay(args) })
	tools.Register("music_ops", "Blaze Music's smart side. action: dj (on = true|false: Blaze DJ keeps playing songs the user loves), mixes (list daily mixes, smart playlists and playlists), play_list (name = a daily mix or smart playlist like \"Daily Mix 1\" / \"Most played\" / \"On repeat\", shuffle), wrapped (period = month|year|all: listening stats), eq (preset = "+strings.Join(media.EQOrder, " | ")+"), crossfade (seconds = 0|3|6|10), data_saver (on), sleep (minutes; 0 = off), import (url = a YouTube or Spotify playlist / album link: downloads every song and builds the playlist), fix_tags (cleans titles / artists of downloaded songs and fetches real covers).",
		[][3]string{{"action", "string", "dj | mixes | play_list | wrapped | eq | crossfade | data_saver | sleep | import | fix_tags"}, {"on", "boolean", ""}, {"name", "string", ""},
			{"shuffle", "boolean", ""}, {"period", "string", ""}, {"preset", "string", ""}, {"seconds", "integer", ""}, {"minutes", "integer", ""}, {"url", "string", ""}},
		[]string{"action"}, func(e *tools.Env, args map[string]any) string { return mediaGet().MusicOps(args) })
	tools.Register("video_ops", "Blaze Video (the video player on this PC; library = the user's Videos folder and Blaze downloads). action: play (query = a video title in the library), series (list series / episodes), subtitles (language = a language name or code, or \"off\"; for the video playing now), translate_subtitles (language: AI-translates the video's subtitles and turns them on), recap (a short recap of the video playing now, from its subtitles, up to where the user is), settings (autoplay / smart_downloads / data_saver true|false).",
		[][3]string{{"action", "string", "play | series | subtitles | translate_subtitles | recap | settings"}, {"query", "string", ""}, {"language", "string", ""},
			{"autoplay", "boolean", ""}, {"smart_downloads", "boolean", ""}, {"data_saver", "boolean", ""}},
		[]string{"action"}, func(e *tools.Env, args map[string]any) string { return mediaGet().VideoOps(args) })
	tools.Register("media_download", "Download songs (into Blaze Music) or videos (into Blaze Video) from YouTube, many at once. Give every song/video the user named as one item (\"Artist - Title\" when known) as {\"query\":...}. Clear matches are queued right away; for items the tool isn't sure about it returns candidates: ask the user which one (or skip), then call again with items as {\"id\":\"<video id>\"}. quality: best, high (default), small; or for video a height like 720 / 480 / 1080. Audio comes as m4a unless format=opus.",
		[][3]string{{"items", "array", "[{\"query\":\"Artist - Title\"} or {\"id\":\"YouTube video id\"}]"}, {"kind", "string", "audio | video"}, {"quality", "string", ""}, {"format", "string", "m4a | opus"}},
		[]string{"items"}, func(e *tools.Env, args map[string]any) string { return mediaGet().MediaDownload(args) })
	tools.Register("jamendo_setup", "Save a Jamendo API client_id for Blaze Music's Jamendo Store (checked against Jamendo before it's saved). Get one free at "+media.JamendoDevPortal+": sign in or sign up (ask the user before creating an account), My applications, create an app (name Blaze Music, website https://blazeinchue.pages.dev), then copy its Client ID here.",
		[][3]string{{"client_id", "string", "The app's Client ID (8 hex characters)"}},
		[]string{"client_id"}, func(e *tools.Env, args map[string]any) string {
			return mediaGet().JamendoSet(tools.ArgS(args, "client_id"))
		})
}
