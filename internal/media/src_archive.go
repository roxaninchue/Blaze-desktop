package media

// Internet Archive sources (mobile ArchiveSources.kt), no key: CC / public-domain
// audio, the Feature Films collection, and the Blender open movies.

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func archiveThumb(id string) string { return "https://archive.org/services/img/" + id }
func archiveFile(id, name string) string {
	parts := strings.Split(name, "/")
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(url.QueryEscape(p), "+", "%20")
	}
	return "https://archive.org/download/" + id + "/" + strings.Join(parts, "/")
}

var reMin = regexp.MustCompile(`(\d+)\s*min`)

// ArchiveSeconds: "14:48" / "00:03:12" / "3237.5" / "3 minutes" → seconds.
func ArchiveSeconds(v string) int {
	s := strings.TrimSpace(v)
	if s == "" {
		return 0
	}
	if c := ParseClock(s); c > 0 {
		return c
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int(f)
	}
	if m := reMin.FindStringSubmatch(s); m != nil {
		n, _ := strconv.Atoi(m[1])
		return n * 60
	}
	return 0
}

func astr(o map[string]any, k string) string {
	switch v := o[k].(type) {
	case []any:
		if len(v) > 0 {
			return str(v[0])
		}
		return ""
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	}
	return fmt.Sprint(o[k])
}

// ArchiveSearch parses advancedsearch JSON.
func ArchiveSearch(js, source string, page, rows int, square bool, defLicence string, requireFree bool) (Page, error) {
	var root struct {
		Response *struct {
			NumFound int              `json:"numFound"`
			Docs     []map[string]any `json:"docs"`
		} `json:"response"`
	}
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		return Page{}, err
	}
	if root.Response == nil {
		return Page{Items: []Item{}}, nil
	}
	out := []Item{}
	for _, d := range root.Response.Docs {
		id := astr(d, "identifier")
		if id == "" {
			continue
		}
		lic := astr(d, "licenseurl")
		if requireFree && !LicenceFree(lic) {
			continue
		}
		sec := ArchiveSeconds(astr(d, "runtime"))
		dt := ""
		if sec > 0 {
			dt = ClockText(int64(sec) * 1000)
		}
		views := ""
		if n, err := strconv.ParseInt(astr(d, "downloads"), 10, 64); err == nil {
			views = commas(n) + " downloads"
		}
		out = append(out, Item{Source: source, ID: id, Title: orStr(astr(d, "title"), id), Author: astr(d, "creator"), DurationSec: sec,
			DurationText: dt, Thumb: archiveThumb(id), Licence: orStr(LicenceLabel(lic), defLicence), LicenceURL: lic, Views: views, Square: square})
	}
	next := ""
	if page*rows < root.Response.NumFound {
		next = itoa(page + 1)
	}
	return Page{out, next}, nil
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	var b []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b = append(b, ',')
		}
		b = append(b, s[i])
	}
	return string(b)
}

var archiveAudioExt = map[string]string{"mp3": "audio/mpeg", "ogg": "audio/ogg", "flac": "audio/flac", "m4a": "audio/mp4", "opus": "audio/ogg"}
var archivePref = []string{"vbr mp3", "320kbps mp3", "256kbps mp3", "192kbps mp3", "128kbps mp3", "mp3", "ogg vorbis", "flac", "96kbps mp3", "64kbps mp3"}

// ArchiveDetail parses metadata JSON → playable files.
func ArchiveDetail(js, kind, defLicence string) (Detail, error) {
	var root struct {
		Metadata map[string]any   `json:"metadata"`
		Files    []map[string]any `json:"files"`
	}
	if err := json.Unmarshal([]byte(js), &root); err != nil {
		return Detail{}, err
	}
	md := root.Metadata
	if md == nil {
		md = map[string]any{}
	}
	id := astr(md, "identifier")
	var fm []Format
	maxLen := 0
	for _, f := range root.Files {
		name := astr(f, "name")
		fmtS := strings.ToLower(astr(f, "format"))
		ext := ""
		if i := strings.LastIndex(name, "."); i >= 0 {
			ext = strings.ToLower(name[i+1:])
		}
		size, _ := strconv.ParseInt(astr(f, "size"), 10, 64)
		l := ArchiveSeconds(astr(f, "length"))
		if l > maxLen {
			maxLen = l
		}
		base := name[strings.LastIndex(name, "/")+1:]
		if i := strings.LastIndex(base, "."); i >= 0 {
			base = base[:i]
		}
		if kind == "audio" {
			mime, ok := archiveAudioExt[ext]
			if !ok || strings.Contains(fmtS, "spectrogram") || strings.Contains(fmtS, "image") {
				continue
			}
			br := 0
			if l > 0 && size > 0 {
				br = int(size * 8 / int64(l))
			}
			fm = append(fm, Format{Key: name, URL: archiveFile(id, name), Mime: mime, Codecs: fmtS, Bitrate: br, ContentLength: size, HasAudio: true, Name: orStr(astr(f, "title"), base)})
		} else {
			if ext != "mp4" && ext != "m4v" {
				continue
			}
			if !(strings.Contains(fmtS, "mpeg4") || strings.Contains(fmtS, "h.264") || strings.Contains(fmtS, "mp4")) {
				continue
			}
			h, _ := strconv.Atoi(astr(f, "height"))
			fm = append(fm, Format{Key: name, URL: archiveFile(id, name), Mime: "video/mp4", Codecs: fmtS, ContentLength: size, Height: h, HasAudio: true, HasVideo: true, Name: base})
		}
	}
	lic := astr(md, "licenseurl")
	length := ArchiveSeconds(astr(md, "runtime"))
	if length <= 0 {
		length = maxLen
	}
	return Detail{ID: id, Title: orStr(astr(md, "title"), id), Author: astr(md, "creator"), LengthSec: length, Thumb: archiveThumb(id), Formats: fm,
		Licence: orStr(LicenceLabel(lic), defLicence), LicenceURL: lic}, nil
}

func prefRank(codec string) int {
	for i, p := range archivePref {
		if p == codec {
			return i
		}
	}
	return len(archivePref)
}

// ArchiveAudioChoices lists every track × format, best formats first.
func ArchiveAudioChoices(d Detail) []Choice {
	var fs []Format
	for _, f := range d.Formats {
		if !f.HasVideo {
			fs = append(fs, f)
		}
	}
	stem := func(k string) string {
		if i := strings.LastIndex(k, "."); i >= 0 {
			return k[:i]
		}
		return k
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if stem(fs[i].Key) != stem(fs[j].Key) {
			return stem(fs[i].Key) < stem(fs[j].Key)
		}
		return prefRank(fs[i].Codecs) < prefRank(fs[j].Codecs)
	})
	if len(fs) > 60 {
		fs = fs[:60]
	}
	var out []Choice
	for _, f := range fs {
		c := f.Container()
		label := map[string]string{"mp3": "MP3", "ogg": "Ogg", "flac": "FLAC", "mp4": "M4A"}[c]
		if label == "" {
			label = strings.ToUpper(c)
		}
		if f.Kbps() > 0 && c != "flac" {
			label += " " + itoa(f.Kbps()) + " kbps"
		}
		ext := c
		if c == "mp4" {
			ext = "m4a"
		}
		out = append(out, Choice{ID: "f:" + f.Key, Label: orStr(f.Name, d.Title), Detail: joinNonEmpty(" \u00b7 ", label, SizeText(f.ContentLength)),
			Keys: []string{f.Key}, Ext: ext, Bytes: f.ContentLength, Kbps: f.Kbps(), Title: f.Name})
	}
	return out
}

// ArchiveVideoChoices lists the mp4 files, smallest first.
func ArchiveVideoChoices(d Detail) []Choice {
	var fs []Format
	for _, f := range d.Formats {
		if f.HasVideo {
			fs = append(fs, f)
		}
	}
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].Height != fs[j].Height {
			return fs[i].Height < fs[j].Height
		}
		return fs[i].ContentLength < fs[j].ContentLength
	})
	var out []Choice
	for _, f := range fs {
		h := "MP4"
		if f.Height > 0 {
			h = itoa(f.Height) + "p"
		}
		if len(fs) > 1 && f.Name != d.Title {
			n := f.Name
			if r := []rune(n); len(r) > 28 {
				n = string(r[:28])
			}
			h += " \u00b7 " + n
		}
		out = append(out, Choice{ID: "f:" + f.Key, Label: h, Detail: joinNonEmpty(" \u00b7 ", "mp4", SizeText(f.ContentLength)), Keys: []string{f.Key}, Ext: "mp4", Bytes: f.ContentLength, Height: f.Height})
	}
	return out
}

// ArchiveDefaultVideo: the biggest at or under 720p (originals can be several GB).
func ArchiveDefaultVideo(c []Choice) *Choice {
	var best *Choice
	for i := range c {
		if c[i].Height >= 1 && c[i].Height <= 720 && (best == nil || c[i].Height > best.Height) {
			best = &c[i]
		}
	}
	if best != nil {
		return best
	}
	for i := range c {
		if best == nil || c[i].Bytes < best.Bytes {
			best = &c[i]
		}
	}
	return best
}

func joinNonEmpty(sep string, parts ...string) string {
	var k []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			k = append(k, p)
		}
	}
	return strings.Join(k, sep)
}

type archiveSource struct {
	id, name    string
	kinds       []string
	square      bool
	defLicence  string
	requireFree bool
	query       func(words string) string
}

var reArchiveWords = regexp.MustCompile(`[():"\\\[\]{}^~*?]`)

func archiveWords(q string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(reArchiveWords.ReplaceAllString(q, " "), " "))
}

func (s *archiveSource) ID() string          { return s.id }
func (s *archiveSource) Name() string        { return s.name }
func (s *archiveSource) Kinds() []string     { return s.kinds }
func (s *archiveSource) SetupNeeded() string { return "" }
func (s *archiveSource) Browsable() bool     { return true }
func (s *archiveSource) UserAgent() string   { return blazeUA }
func (s *archiveSource) Search(kind, query, next string) (Page, error) {
	page, _ := strconv.Atoi(next)
	if page < 1 {
		page = 1
	}
	rows := 30
	u := "https://archive.org/advancedsearch.php?q=" + url.QueryEscape(s.query(archiveWords(query)))
	for _, f := range []string{"identifier", "title", "creator", "licenseurl", "runtime", "downloads"} {
		u += "&fl%5B%5D=" + f
	}
	u += "&sort%5B%5D=" + url.QueryEscape("downloads desc") + "&rows=" + itoa(rows) + "&page=" + itoa(page) + "&output=json"
	js, err := httpGet(u, "")
	if err != nil {
		return Page{}, err
	}
	return ArchiveSearch(js, s.id, page, rows, s.square, s.defLicence, s.requireFree)
}
func (s *archiveSource) Detail(it Item, kind string) (Detail, error) {
	js, err := httpGet("https://archive.org/metadata/"+url.PathEscape(it.ID), "")
	if err != nil {
		return Detail{}, err
	}
	d, err := ArchiveDetail(js, kind, s.defLicence)
	if err == nil && d.Licence == "" {
		d.Licence, d.LicenceURL = it.Licence, it.LicenceURL
	}
	return d, err
}
func (s *archiveSource) Choices(kind string, d Detail) []Choice {
	if kind == "video" {
		return ArchiveVideoChoices(d)
	}
	return ArchiveAudioChoices(d)
}
func (s *archiveSource) DefaultChoice(kind string, c []Choice) *Choice {
	if kind == "video" {
		return ArchiveDefaultVideo(c)
	}
	if len(c) == 0 {
		return nil
	}
	return &c[0]
}
func (s *archiveSource) Refresh(string, string) (*Detail, error) { return nil, nil }

// BlenderIDs are the Blender Studio open movies on archive.org.
var BlenderIDs = []string{"BigBuckBunny_124", "ElephantsDream", "Sintel", "Tears-of-Steel", "CosmosLaundromatFirstCycle",
	"spring_blenderopenmovie", "agent327operationbarbershop", "sprite-fright", "Caminandes1LlamaDrama", "Caminandes2GranDillama", "CaminandesLlamigos"}

func newArchiveSources() []Source {
	pre := func(w string) string {
		if w == "" {
			return ""
		}
		return "(" + w + ") AND "
	}
	return []Source{
		&archiveSource{id: "archive-audio", name: "Internet Archive", kinds: []string{"audio"}, square: true, requireFree: true,
			query: func(w string) string {
				return pre(w) + "mediatype:audio AND licenseurl:(*creativecommons.org* OR *publicdomain*)"
			}},
		&archiveSource{id: "archive-films", name: "Public-domain films", kinds: []string{"video"}, defLicence: "Public domain",
			query: func(w string) string { return pre(w) + "collection:feature_films AND mediatype:movies" }},
		&archiveSource{id: "blender", name: "Blender open movies", kinds: []string{"video"}, requireFree: true,
			query: func(w string) string {
				q := "identifier:(" + strings.Join(BlenderIDs, " OR ") + ")"
				if w != "" {
					q += " AND (" + w + ")"
				}
				return q
			}},
	}
}
