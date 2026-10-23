package media

// Jamendo (mobile JamendoSource.kt): Creative Commons music through the v3 API. Needs
// a free client_id (https://devportal.jamendo.com), pasted in the Store or saved by
// the jamendo_setup tool into ~/.blaze/media/jamendo.json, or the JAMENDO_CLIENT_ID
// environment variable. Nothing is hard-coded.

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

const JamendoDevPortal = "https://devportal.jamendo.com"

// JamendoSearch parses a tracks reply.
func JamendoSearch(js string, offset, limit int) (Page, error) {
	var root struct {
		Headers *struct {
			Status  string `json:"status"`
			Code    int    `json:"code"`
			Error   string `json:"error_message"`
			Results int    `json:"results_count"`
		} `json:"headers"`
		Results []map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		return Page{}, err
	}
	if h := root.Headers; h != nil && h.Status == "failed" {
		msg := orStr(h.Error, "Jamendo refused the request")
		if h.Code == 5 || strings.Contains(strings.ToLower(msg), "client") {
			msg = "Jamendo didn't accept the client_id. Check it in the Music Store."
		}
		return Page{}, &StoreError{Msg: msg}
	}
	out := []Item{}
	for _, t := range root.Results {
		id := str(t["id"])
		if id == "" {
			continue
		}
		lic := str(t["license_ccurl"])
		sec := 0
		if f, ok := t["duration"].(float64); ok {
			sec = int(f)
		}
		dt := ""
		if sec > 0 {
			dt = ClockText(int64(sec) * 1000)
		}
		allowed := "true"
		if b, ok := t["audiodownload_allowed"].(bool); ok && !b {
			allowed = "false"
		}
		out = append(out, Item{Source: "jamendo", ID: id, Title: str(t["name"]), Author: str(t["artist_name"]), DurationSec: sec, DurationText: dt,
			Thumb: orStr(str(t["image"]), str(t["album_image"])), Licence: orStr(LicenceLabel(lic), "Creative Commons"), LicenceURL: lic, Square: true,
			Extra: map[string]string{"album": str(t["album_name"]), "audio": str(t["audio"]), "download": str(t["audiodownload"]), "allowed": allowed}})
	}
	count := len(root.Results)
	if root.Headers != nil {
		count = root.Headers.Results
	}
	next := ""
	if count >= limit {
		next = itoa(offset + limit)
	}
	return Page{out, next}, nil
}

var reFormatQ = regexp.MustCompile(`([?&]format=)[^&]*`)

// JamendoWithFormat sets format= on a stream url.
func JamendoWithFormat(u, f string) string {
	if reFormatQ.MatchString(u) {
		return reFormatQ.ReplaceAllString(u, "${1}"+f)
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
	}
	return u + sep + "format=" + f
}

// JamendoDetail: mp32 (VBR, the download url) and mp31 (96 kbps stream).
func JamendoDetail(it Item) Detail {
	var fm []Format
	est := func(kbps int) int64 { return int64(kbps) * 1000 * int64(it.DurationSec) / 8 }
	if it.Extra["allowed"] != "false" && it.Extra["download"] != "" {
		fm = append(fm, Format{Key: "mp32", URL: it.Extra["download"], Mime: "audio/mpeg", Codecs: "mp3", Bitrate: 192000, ContentLength: est(192), HasAudio: true, Name: it.Title})
	}
	if s := it.Extra["audio"]; s != "" {
		fm = append(fm, Format{Key: "mp31", URL: JamendoWithFormat(s, "mp31"), Mime: "audio/mpeg", Codecs: "mp3", Bitrate: 96000, ContentLength: est(96), HasAudio: true, Name: it.Title})
	}
	return Detail{ID: it.ID, Title: it.Title, Author: it.Author, LengthSec: it.DurationSec, Thumb: it.Thumb, Formats: fm, Licence: it.Licence, LicenceURL: it.LicenceURL}
}

type jamendoSource struct {
	dir string
	mu  sync.Mutex
	id  string
}

func newJamendo(dir string) *jamendoSource {
	j := &jamendoSource{dir: dir}
	if b, err := os.ReadFile(filepath.Join(dir, "jamendo.json")); err == nil {
		var o struct {
			ID string `json:"client_id"`
		}
		_ = json.Unmarshal(b, &o)
		j.id = o.ID
	}
	return j
}

func (j *jamendoSource) ClientID() string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return orStr(strings.TrimSpace(j.id), strings.TrimSpace(os.Getenv("JAMENDO_CLIENT_ID")))
}

func (j *jamendoSource) SetClientID(id string) {
	j.mu.Lock()
	j.id = strings.TrimSpace(id)
	b, _ := json.Marshal(map[string]string{"client_id": j.id})
	j.mu.Unlock()
	_ = os.WriteFile(filepath.Join(j.dir, "jamendo.json"), b, 0o600)
}

// Works checks a client_id with a one-track request.
func (j *jamendoSource) Works(id string) bool {
	js, err := httpGet("https://api.jamendo.com/v3.0/tracks/?client_id="+url.QueryEscape(strings.TrimSpace(id))+"&format=json&limit=1", "")
	if err != nil {
		return false
	}
	var o struct {
		Headers struct {
			Status string `json:"status"`
		} `json:"headers"`
	}
	return json.Unmarshal([]byte(js), &o) == nil && o.Headers.Status == "success"
}

func (j *jamendoSource) ID() string        { return "jamendo" }
func (j *jamendoSource) Name() string      { return "Jamendo" }
func (j *jamendoSource) Kinds() []string   { return []string{"audio"} }
func (j *jamendoSource) Browsable() bool   { return true }
func (j *jamendoSource) UserAgent() string { return blazeUA }
func (j *jamendoSource) SetupNeeded() string {
	if j.ClientID() == "" {
		return "Jamendo needs a free client_id. Blaze can get it for you, or make one on the Jamendo dev portal and paste it below."
	}
	return ""
}
func (j *jamendoSource) Search(kind, query, next string) (Page, error) {
	id := j.ClientID()
	if id == "" {
		return Page{}, &StoreError{Msg: j.SetupNeeded()}
	}
	offset, _ := strconv.Atoi(next)
	u := "https://api.jamendo.com/v3.0/tracks/?client_id=" + url.QueryEscape(id) + "&format=json&limit=30&offset=" + itoa(offset) +
		"&include=licenses&audioformat=mp32&imagesize=300"
	if q := strings.TrimSpace(query); q == "" {
		u += "&order=popularity_month"
	} else {
		u += "&search=" + url.QueryEscape(q)
	}
	js, err := httpGet(u, "")
	if err != nil {
		return Page{}, err
	}
	return JamendoSearch(js, offset, 30)
}
func (j *jamendoSource) Detail(it Item, kind string) (Detail, error) { return JamendoDetail(it), nil }
func (j *jamendoSource) Choices(kind string, d Detail) []Choice {
	var out []Choice
	for _, f := range d.Formats {
		label := "Low \u00b7 MP3 96 kbps"
		if f.Key == "mp32" {
			label = "High \u00b7 MP3 VBR"
		}
		out = append(out, Choice{ID: f.Key, Label: label, Detail: "mp3 \u00b7 " + SizeText(f.ContentLength) + " (about)", Keys: []string{f.Key}, Ext: "mp3", Bytes: f.ContentLength, Kbps: f.Kbps()})
	}
	return out
}
func (j *jamendoSource) DefaultChoice(kind string, c []Choice) *Choice {
	if len(c) == 0 {
		return nil
	}
	return &c[0]
}
func (j *jamendoSource) Refresh(string, string) (*Detail, error) { return nil, nil }
