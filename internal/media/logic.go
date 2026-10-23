// Package media is Blaze Desktop's Music and Video apps, ported from Blaze Mobile
// (app/src/main/java/com/blaze/assistant/mediaapps, v0.34.0): the library (the user's
// music and video folders plus what Blaze downloaded), playlists, likes, recents, the
// play log and the smart side built on it (daily mixes, smart playlists, Wrapped,
// Blaze DJ, taste), lyrics (LRCLIB), subtitles (YouTube captions, .srt/.vtt), the
// Store sources (YouTube, Jamendo, Internet Archive, Blender open movies, a link) and
// the download queue. Playback itself is the webview's <audio>/<video>; the files and
// streams reach it through Handler (Wails asset handler and a loopback server).
//
// Everything is kept under ~/.blaze/media (new, additive: the CLI never reads it).
package media

import (
	"math/rand"
	"regexp"
	"sort"
	"strings"
)

// Track is one playable thing: a file in the user's folders, a Blaze download, or a
// Store stream (never saved). Mirrors mobile's Track (MediaLogic.kt).
type Track struct {
	Key         string `json:"k"`   // stable id: "file:" + absolute path
	Path        string `json:"u"`   // absolute file path ("" for streams)
	Title       string `json:"t"`   // display title
	Artist      string `json:"a"`   // artist / channel
	Album       string `json:"al"`  // album
	DurationMs  int64  `json:"d"`   // 0 = unknown
	Art         string `json:"art"` // cover file path or https url
	Source      string `json:"src"` // download | folder | stream
	AddedAt     int64  `json:"at"`  // epoch ms
	VideoID     string `json:"vid"` // source item id ("yt:<id>", archive id…)
	Kind        string `json:"kind"`
	Licence     string `json:"lic"`
	StoreSource string `json:"ss"`
	TrackNo     int    `json:"n,omitempty"`
	Year        int    `json:"y,omitempty"`
	Genre       string `json:"g,omitempty"`
	Size        int64  `json:"sz,omitempty"`
	MTime       int64  `json:"mt,omitempty"` // file mtime (ms), for the scan cache
}

// Repeat cycles off → all → one → off.
type Repeat int

const (
	RepeatOff Repeat = iota
	RepeatAll
	RepeatOne
)

func (r Repeat) Next() Repeat { return (r + 1) % 3 }

// ShuffleOrder is a shuffle over size items that starts with start (the song the user
// picked plays first), the rest in a random order fixed by seed.
func ShuffleOrder(size, start int, seed int64) []int {
	if size <= 0 {
		return []int{}
	}
	s := clamp(start, 0, size-1)
	rest := make([]int, 0, size-1)
	for i := 0; i < size; i++ {
		if i != s {
			rest = append(rest, i)
		}
	}
	r := rand.New(rand.NewSource(seed))
	r.Shuffle(len(rest), func(i, j int) { rest[i], rest[j] = rest[j], rest[i] })
	return append([]int{s}, rest...)
}

func order(size int, o []int) []int {
	if len(o) == size {
		return o
	}
	out := make([]int, size)
	for i := range out {
		out[i] = i
	}
	return out
}

func indexOf(o []int, v int) int {
	for i, x := range o {
		if x == v {
			return i
		}
	}
	return -1
}

// NextIndex is what plays after current (-1 = the queue ends). Repeat one stays.
func NextIndex(current, size int, rep Repeat, ord []int) int {
	if size <= 0 || current < 0 || current >= size {
		return -1
	}
	if rep == RepeatOne {
		return current
	}
	o := order(size, ord)
	p := indexOf(o, current)
	switch {
	case p < 0:
		return -1
	case p+1 < len(o):
		return o[p+1]
	case rep == RepeatAll:
		return o[0]
	}
	return -1
}

// PrevIndex is the index before current (-1 = none).
func PrevIndex(current, size int, rep Repeat, ord []int) int {
	if size <= 0 || current < 0 || current >= size {
		return -1
	}
	o := order(size, ord)
	p := indexOf(o, current)
	switch {
	case p < 0:
		return -1
	case p > 0:
		return o[p-1]
	case rep == RepeatAll:
		return o[len(o)-1]
	}
	return -1
}

// UpNext lists up to count indices after current, wrapping only with repeat all.
func UpNext(current, size int, rep Repeat, ord []int, count int) []int {
	o := order(size, ord)
	p := indexOf(o, current)
	if p < 0 {
		if len(o) > count {
			return o[:count]
		}
		return o
	}
	out := append([]int{}, o[p+1:]...)
	if rep == RepeatAll {
		out = append(out, o[:p]...)
	}
	if len(out) > count {
		out = out[:count]
	}
	return out
}

// Move moves one item; out-of-range moves leave the list unchanged.
func Move[T any](list []T, from, to int) []T {
	if from < 0 || from >= len(list) || to < 0 || to >= len(list) || from == to {
		return list
	}
	m := append([]T{}, list...)
	x := m[from]
	m = append(m[:from], m[from+1:]...)
	m = append(m[:to], append([]T{x}, m[to:]...)...)
	return m
}

// Playlist is a user playlist of track keys.
type Playlist struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Keys    []string `json:"keys"`
	Updated int64    `json:"u"`
}

var spaces = regexp.MustCompile(`\s+`)

// CleanName trims and collapses spaces, at most 60 characters.
func CleanName(name string) string {
	s := spaces.ReplaceAllString(strings.TrimSpace(name), " ")
	if r := []rune(s); len(r) > 60 {
		s = string(r[:60])
	}
	return s
}

// UniqueName is a name not already taken ("Road trip", "Road trip 2", …).
func UniqueName(lists []Playlist, name string) string {
	base := CleanName(name)
	if base == "" {
		base = "New playlist"
	}
	taken := map[string]bool{}
	for _, l := range lists {
		taken[strings.ToLower(l.Name)] = true
	}
	if !taken[strings.ToLower(base)] {
		return base
	}
	for n := 2; ; n++ {
		c := base + " " + itoa(n)
		if !taken[strings.ToLower(c)] {
			return c
		}
	}
}

func PlCreate(lists []Playlist, name, id string, now int64) []Playlist {
	return append(append([]Playlist{}, lists...), Playlist{ID: id, Name: UniqueName(lists, name), Keys: []string{}, Updated: now})
}

func PlRename(lists []Playlist, id, name string, now int64) []Playlist {
	n := CleanName(name)
	if n == "" {
		return lists
	}
	var others []Playlist
	for _, l := range lists {
		if l.ID != id {
			others = append(others, l)
		}
	}
	out := make([]Playlist, len(lists))
	for i, l := range lists {
		if l.ID == id {
			l.Name = UniqueName(others, n)
			l.Updated = now
		}
		out[i] = l
	}
	return out
}

func PlDelete(lists []Playlist, id string) []Playlist {
	out := []Playlist{}
	for _, l := range lists {
		if l.ID != id {
			out = append(out, l)
		}
	}
	return out
}

// PlAdd adds keys at the end, skipping ones already there.
func PlAdd(lists []Playlist, id string, keys []string, now int64) []Playlist {
	out := make([]Playlist, len(lists))
	for i, l := range lists {
		if l.ID == id {
			have := append([]string{}, l.Keys...)
			for _, k := range keys {
				if !contains(have, k) {
					have = append(have, k)
				}
			}
			l.Keys, l.Updated = have, now
		}
		out[i] = l
	}
	return out
}

func PlRemoveAt(lists []Playlist, id string, index int, now int64) []Playlist {
	out := make([]Playlist, len(lists))
	for i, l := range lists {
		if l.ID == id && index >= 0 && index < len(l.Keys) {
			k := append([]string{}, l.Keys[:index]...)
			l.Keys, l.Updated = append(k, l.Keys[index+1:]...), now
		}
		out[i] = l
	}
	return out
}

func PlMove(lists []Playlist, id string, from, to int, now int64) []Playlist {
	out := make([]Playlist, len(lists))
	for i, l := range lists {
		if l.ID == id {
			l.Keys, l.Updated = Move(l.Keys, from, to), now
		}
		out[i] = l
	}
	return out
}

// PlForget drops a deleted song from every playlist.
func PlForget(lists []Playlist, key string) []Playlist {
	out := make([]Playlist, len(lists))
	for i, l := range lists {
		if contains(l.Keys, key) {
			k := []string{}
			for _, x := range l.Keys {
				if x != key {
					k = append(k, x)
				}
			}
			l.Keys = k
		}
		out[i] = l
	}
	return out
}

// PushRecent puts key first, no duplicates, at most max.
func PushRecent(list []string, key string, max int) []string {
	out := []string{key}
	for _, x := range list {
		if x != key {
			out = append(out, x)
		}
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// ResumeAt resumes a video where it was left unless barely started or practically over.
func ResumeAt(savedMs, durMs int64) int64 {
	if savedMs < 5000 || (durMs > 0 && savedMs > durMs-10000) {
		return 0
	}
	return savedMs
}

// KeepPosition: finished videos are forgotten so they start over.
func KeepPosition(posMs, durMs int64) bool { return ResumeAt(posMs, durMs) > 0 }

// SleepPresetsMin are the sleep timer's choices (minutes).
var SleepPresetsMin = []int{5, 10, 15, 30, 45, 60, 90}

// Speeds are the playback speeds the speed button cycles through.
var Speeds = []float64{0.5, 0.75, 1, 1.25, 1.5, 1.75, 2}

// Group is a named group of tracks (artist, album, series).
type Group struct {
	Name   string  `json:"name"`
	Tracks []Track `json:"tracks"`
}

// Artists groups by artist, sorted by name.
func Artists(ts []Track) []Group {
	return groupBy(ts, func(t Track) string { return orStr(t.Artist, "Unknown artist") })
}

// Albums groups by album (else artist, else Singles).
func Albums(ts []Track) []Group {
	return groupBy(ts, func(t Track) string { return orStr(t.Album, orStr(t.Artist, "Singles")) })
}

func groupBy(ts []Track, key func(Track) string) []Group {
	idx := map[string]int{}
	var out []Group
	for _, t := range ts {
		k := key(t)
		i, ok := idx[k]
		if !ok {
			i = len(out)
			idx[k] = i
			out = append(out, Group{Name: k})
		}
		out[i].Tracks = append(out[i].Tracks, t)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// Search keeps tracks whose title/artist/album hold every word of q.
func Search(ts []Track, q string) []Track {
	words := strings.Fields(strings.ToLower(q))
	if len(words) == 0 {
		return ts
	}
	out := []Track{}
	for _, t := range ts {
		hay := strings.ToLower(t.Title + " " + t.Artist + " " + t.Album)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, t)
		}
	}
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func orStr(a, b string) string {
	if strings.TrimSpace(a) == "" {
		return b
	}
	return a
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}
