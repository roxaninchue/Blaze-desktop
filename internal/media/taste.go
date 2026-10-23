package media

// Taste (mobile Taste.kt): what's played and downloaded becomes Store ideas and one
// line for Blaze's memory. Pure; persistence is in Media.

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// TasteEvent is one play or download.
type TasteEvent struct {
	Kind   string `json:"k"`
	Title  string `json:"t"`
	Author string `json:"a"`
	Weight int    `json:"w"`
	At     int64  `json:"at"`
}

const (
	TastePlay     = 1
	TasteDownload = 3
	tasteMax      = 400
	halfLifeDays  = 30.0
	TasteSubject  = "music and video taste"
)

var StarterAudio = []string{"Top hits this week", "Afrobeats", "Amapiano", "Chill lo-fi beats", "Gospel worship", "Hip hop new releases",
	"R&B slow jams", "Zambian music", "Acoustic covers", "Workout mix", "Piano for focus", "Throwback 2000s hits"}
var StarterVideo = []string{"Trending music videos", "Football highlights", "Movie trailers", "Tech reviews", "Comedy sketches",
	"Documentaries", "Cooking recipes", "How it's made", "Travel vlogs", "Science explained", "Gaming highlights", "Car reviews"}

var tasteNoise = map[string]bool{}

func init() {
	for _, w := range strings.Fields("official video music lyric lyrics audio visualizer visualiser hd 4k hq remastered remaster feat ft the and with from for you your this that live version full new song songs mv clip topic vevo prod edit remix mix cover part episode ep vol a an of in on to my me is it at by vs x") {
		tasteNoise[w] = true
	}
}

func decay(at, now int64) float64 {
	d := float64(now - at)
	if d < 0 {
		d = 0
	}
	return math.Pow(0.5, d/86_400_000.0/halfLifeDays)
}

// CleanAuthor: "Jared Benjamin - Topic" / "JaredBenjaminVEVO" → "Jared Benjamin".
func CleanAuthor(a string) string {
	return strings.TrimSpace(reOfficial.ReplaceAllString(reVevo.ReplaceAllString(reTopic.ReplaceAllString(a, ""), ""), ""))
}

var reBrackets = regexp.MustCompile(`\(.*?\)|\[.*?]`)
var reNonWord = regexp.MustCompile(`[^\p{L}\p{N}&']+`)

// TasteWords are the meaningful words of a title.
func TasteWords(title string) []string {
	var out []string
	for _, w := range reNonWord.Split(reBrackets.ReplaceAllString(strings.ToLower(title), " "), -1) {
		if len([]rune(w)) <= 2 || tasteNoise[w] || allDigits(w) {
			continue
		}
		out = append(out, w)
	}
	return out
}

func allDigits(s string) bool {
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

// PushTaste appends (at most 400 kept).
func PushTaste(ev []TasteEvent, e TasteEvent) []TasteEvent {
	if strings.TrimSpace(e.Title) == "" && strings.TrimSpace(e.Author) == "" {
		return ev
	}
	ev = append(append([]TasteEvent{}, ev...), e)
	if len(ev) > tasteMax {
		ev = ev[len(ev)-tasteMax:]
	}
	return ev
}

type scored struct {
	k string
	v float64
}

func top(score map[string]float64, keep func(string) bool, n int) []string {
	var l []scored
	for k, v := range score {
		if keep == nil || keep(k) {
			l = append(l, scored{k, v})
		}
	}
	sort.SliceStable(l, func(i, j int) bool {
		if l[i].v != l[j].v {
			return l[i].v > l[j].v
		}
		return l[i].k < l[j].k
	})
	var out []string
	for i, s := range l {
		if i >= n {
			break
		}
		out = append(out, s.k)
	}
	return out
}

// TopAuthors are favourite artists / channels for kind.
func TopAuthors(ev []TasteEvent, kind string, now int64, n int) []string {
	score := map[string]float64{}
	name := map[string]string{}
	for _, e := range ev {
		if e.Kind != kind {
			continue
		}
		a := CleanAuthor(e.Author)
		if a == "" {
			continue
		}
		k := strings.ToLower(a)
		score[k] += float64(e.Weight) * decay(e.At, now)
		if _, ok := name[k]; !ok {
			name[k] = a
		}
	}
	keys := top(score, nil, n)
	for i, k := range keys {
		keys[i] = name[k]
	}
	return keys
}

// TopStyles are title words seen in 2+ different items.
func TopStyles(ev []TasteEvent, kind string, now int64, n int) []string {
	score := map[string]float64{}
	items := map[string]map[string]bool{}
	authors := map[string]bool{}
	for _, e := range ev {
		if e.Kind == kind {
			for _, p := range strings.Fields(strings.ToLower(CleanAuthor(e.Author))) {
				authors[p] = true
			}
		}
	}
	for _, e := range ev {
		if e.Kind != kind {
			continue
		}
		seen := map[string]bool{}
		for _, w := range TasteWords(e.Title) {
			if seen[w] || authors[w] {
				continue
			}
			seen[w] = true
			score[w] += float64(e.Weight) * decay(e.At, now)
			if items[w] == nil {
				items[w] = map[string]bool{}
			}
			items[w][strings.ToLower(e.Title)] = true
		}
	}
	return top(score, func(k string) bool { return len(items[k]) >= 2 }, n)
}

func titleCase(s string) string {
	r := []rune(s)
	if len(r) == 0 {
		return s
	}
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Ideas are the Store's scrollable ideas: what they like, then starters. Never empty.
func Ideas(ev []TasteEvent, kind string, now int64, max int) []string {
	video := kind == "video"
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	for _, a := range TopAuthors(ev, kind, now, 4) {
		if video {
			add(a)
		} else {
			add(a + " mix")
		}
	}
	for _, s := range TopStyles(ev, kind, now, 3) {
		if video {
			add(titleCase(s))
		} else {
			add(titleCase(s) + " music")
		}
	}
	st := StarterAudio
	if video {
		st = StarterVideo
	}
	for _, s := range st {
		add(s)
	}
	if len(out) > max {
		out = out[:max]
	}
	return out
}

// TasteSummary is one memory line, or "" when there isn't enough yet.
func TasteSummary(ev []TasteEvent, now int64) string {
	var parts []string
	ma, ms := TopAuthors(ev, "audio", now, 5), TopStyles(ev, "audio", now, 3)
	va, vs := TopAuthors(ev, "video", now, 4), TopStyles(ev, "video", now, 3)
	if len(ma)+len(ms) > 0 {
		parts = append(parts, "Music: "+strings.Join(append(ma, ms...), ", "))
	}
	if len(va)+len(vs) > 0 {
		parts = append(parts, "Videos: "+strings.Join(append(va, vs...), ", "))
	}
	if len(parts) == 0 || len(ev) < 5 {
		return ""
	}
	return "Listens to / watches in Blaze Music and Video (learned from plays and downloads): " + strings.Join(parts, ". ") + "."
}
