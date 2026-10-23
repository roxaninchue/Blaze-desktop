package media

// Match scoring and quality picking for media_download and playlist import (mobile
// MediaFetch.kt MediaPick). Pure.

import (
	"math"
	"regexp"
	"sort"
	"strings"
)

var pickJunk = []string{"live", "cover", "remix", "karaoke", "reaction", "8d", "slowed", "sped up", "nightcore", "instrumental", "tutorial", "lesson", "mashup", "bass boosted", "1 hour", "10 hours", "loop"}
var pickJunkRe = func() []*regexp.Regexp {
	var out []*regexp.Regexp
	for _, j := range pickJunk {
		out = append(out, regexp.MustCompile(`\b`+regexp.QuoteMeta(j)+`\b`))
	}
	return out
}()
var pickStop = map[string]bool{"the": true, "ft": true, "feat": true, "official": true, "video": true, "audio": true, "lyrics": true, "lyric": true, "music": true, "hd": true, "hq": true, "by": true, "and": true}
var rePickClean = regexp.MustCompile(`[^\p{L}\p{N} ]`)
var reLongOK = regexp.MustCompile(`\b(mix|album|full|playlist|hour)\b`)

// PickWords are the words that matter for matching.
func PickWords(s string) []string {
	var out []string
	for _, w := range strings.Split(rePickClean.ReplaceAllString(strings.ToLower(s), " "), " ") {
		if len([]rune(w)) > 1 && !pickStop[w] {
			out = append(out, w)
		}
	}
	return out
}

// PickScore scores one result for a query.
func PickScore(query, kind string, it Item) float64 {
	q := uniq(PickWords(query))
	if len(q) == 0 {
		return 0
	}
	hay := map[string]bool{}
	for _, w := range PickWords(it.Title + " " + it.Author) {
		hay[w] = true
	}
	hit := 0
	for _, w := range q {
		if hay[w] {
			hit++
			continue
		}
		if len([]rune(w)) >= 4 {
			for h := range hay {
				if strings.HasPrefix(h, w) {
					hit++
					break
				}
			}
		}
	}
	s := float64(hit) / float64(len(q))
	tl := strings.ToLower(it.Title + " ")
	ql := strings.ToLower(query)
	for i, j := range pickJunk {
		if pickJunkRe[i].MatchString(tl) && !strings.Contains(ql, j) {
			s -= 0.3
		}
	}
	if strings.HasSuffix(it.Author, " - Topic") || strings.Contains(strings.ToUpper(it.Author), "VEVO") {
		s += 0.1
	}
	if kind == "audio" && (strings.Contains(tl, "official audio") || strings.Contains(tl, "official music video") || strings.Contains(tl, "lyric")) {
		s += 0.05
	}
	if kind == "audio" && it.DurationSec > 15*60 && !reLongOK.MatchString(ql) {
		s -= 0.4
	}
	return s
}

func uniq(l []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, x := range l {
		if !seen[x] {
			seen[x] = true
			out = append(out, x)
		}
	}
	return out
}

// Ranked is a scored result.
type Ranked struct {
	Item  Item
	Score float64
}

// PickRank scores and sorts (earlier results get a tiny edge).
func PickRank(query, kind string, items []Item) []Ranked {
	out := make([]Ranked, len(items))
	for i, it := range items {
		out[i] = Ranked{it, PickScore(query, kind, it) - float64(i)*0.02}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// PickConfident: nearly every word found, nothing else close (or the runner-up is the same song).
func PickConfident(r []Ranked) bool {
	if len(r) == 0 || r[0].Score < 0.8 {
		return false
	}
	if len(r) == 1 || r[0].Score-r[1].Score >= 0.15 {
		return true
	}
	wa, wb := map[string]bool{}, map[string]bool{}
	for _, w := range PickWords(r[0].Item.Title) {
		wa[w] = true
	}
	for _, w := range PickWords(r[1].Item.Title) {
		wb[w] = true
	}
	inter := 0
	for w := range wa {
		if wb[w] {
			inter++
		}
	}
	mx := len(wa)
	if len(wb) > mx {
		mx = len(wb)
	}
	if mx < 1 {
		mx = 1
	}
	return float64(inter)/float64(mx) >= 0.8 && math.Abs(float64(r[0].Item.DurationSec-r[1].Item.DurationSec)) <= 8
}

var reHeight = regexp.MustCompile(`(\d{3,4})`)

// PickChoice picks the option for a quality word ("best", "high", "small", "720").
func PickChoice(kind string, c []Choice, quality, format string) *Choice {
	if len(c) == 0 {
		return nil
	}
	if kind == "video" {
		h := 720
		if m := reHeight.FindStringSubmatch(quality); m != nil {
			h = 0
			for _, d := range m[1] {
				h = h*10 + int(d-'0')
			}
		} else if quality == "best" {
			h = 10000
		} else if quality == "small" || quality == "low" {
			h = 360
		}
		var best *Choice
		for i := range c {
			if c[i].Height >= 1 && c[i].Height <= h && (best == nil || c[i].Height > best.Height) {
				best = &c[i]
			}
		}
		if best != nil {
			return best
		}
		for i := range c {
			if best == nil || c[i].Height < best.Height {
				best = &c[i]
			}
		}
		return best
	}
	var pool []int
	for i := range c {
		if (format == "opus") == (c[i].Ext == "opus") {
			pool = append(pool, i)
		}
	}
	if len(pool) == 0 {
		for i := range c {
			pool = append(pool, i)
		}
	}
	small := quality == "small" || quality == "low" || quality == "smaller"
	bi := pool[0]
	for _, i := range pool {
		if (small && c[i].Kbps < c[bi].Kbps) || (!small && c[i].Kbps > c[bi].Kbps) {
			bi = i
		}
	}
	return &c[bi]
}
