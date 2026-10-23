package media

// Video smarts (mobile VideoSmart.kt): data cost, episodes grouped into series, what
// plays next, a learned "Skip intro" per series, smart-download cleanup and the text
// for a recap and AI subtitle translation. Pure.

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// CostLine is "≈ 245 MB to watch" ("" when unknown).
func CostLine(bytes int64, kind string) string {
	if bytes <= 0 {
		return ""
	}
	verb := "to listen"
	if kind == "video" {
		verb = "to watch"
	}
	return "\u2248 " + SizeText(bytes) + " " + verb
}

// Episode is a parsed episode marker.
type Episode struct {
	Series  string
	Key     string
	Season  int
	Episode int
}

var epPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)^(.*?)[\s._\-:|]*\bS(\d{1,2})\s*[._\-]?\s*E(\d{1,3})\b`),
	regexp.MustCompile(`(?i)^(.*?)[\s._\-:|]*\b(\d{1,2})x(\d{1,3})\b`),
	regexp.MustCompile(`(?i)^(.*?)[\s._\-:|]*\bSeason\s*(\d{1,2})[\s,._\-:|]*(?:Episode|Ep\.?)\s*(\d{1,3})\b`),
}
var epSingle = regexp.MustCompile(`(?i)^(.*?)[\s._\-:|(\[]*\b(?:Episode|Ep\.?|Part|Pt\.?|Chapter|#)\s*(\d{1,3})\b`)
var epTrail = regexp.MustCompile(`[\s._\-:|(\[]+$`)

func cleanSeries(s string) string {
	return strings.TrimSpace(spaces.ReplaceAllString(epTrail.ReplaceAllString(s, ""), " "))
}

// ParseEpisode finds "Show S01E02", "Show 1x02", "Show - Episode 3", "Show Part 2".
func ParseEpisode(title string) *Episode {
	t := strings.TrimSpace(strings.ReplaceAll(title, "_", " "))
	if !strings.Contains(t, " ") && strings.Count(t, ".") >= 2 {
		t = strings.ReplaceAll(t, ".", " ")
	}
	for _, p := range epPatterns {
		if m := p.FindStringSubmatch(t); m != nil {
			s := cleanSeries(m[1])
			if s == "" {
				return nil
			}
			a, _ := strconv.Atoi(m[2])
			b, _ := strconv.Atoi(m[3])
			return &Episode{s, strings.ToLower(s), a, b}
		}
	}
	if m := epSingle.FindStringSubmatch(t); m != nil {
		s := cleanSeries(m[1])
		if s == "" {
			return nil
		}
		b, _ := strconv.Atoi(m[2])
		return &Episode{s, strings.ToLower(s), 1, b}
	}
	return nil
}

// Series lists series with at least two episodes, in order; series by name.
func Series(videos []Track) []Group {
	type pe struct {
		e Episode
		t Track
	}
	by := map[string][]pe{}
	var order []string
	for _, v := range videos {
		e := ParseEpisode(v.Title)
		if e == nil {
			continue
		}
		if _, ok := by[e.Key]; !ok {
			order = append(order, e.Key)
		}
		by[e.Key] = append(by[e.Key], pe{*e, v})
	}
	var out []Group
	for _, k := range order {
		l := by[k]
		if len(l) < 2 {
			continue
		}
		sort.SliceStable(l, func(i, j int) bool {
			if l[i].e.Season != l[j].e.Season {
				return l[i].e.Season < l[j].e.Season
			}
			return l[i].e.Episode < l[j].e.Episode
		})
		g := Group{Name: l[0].e.Series}
		for _, x := range l {
			g.Tracks = append(g.Tracks, x.t)
		}
		out = append(out, g)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// NextEpisode is what plays after cur: the next episode of its series, else nil.
func NextEpisode(cur Track, videos []Track) *Track {
	e := ParseEpisode(cur.Title)
	if e == nil {
		return nil
	}
	for _, s := range Series(videos) {
		if strings.ToLower(s.Name) != e.Key {
			continue
		}
		for i, t := range s.Tracks {
			if t.Key == cur.Key && i+1 < len(s.Tracks) {
				n := s.Tracks[i+1]
				return &n
			}
		}
	}
	return nil
}

// LearnIntro learns where a series' intro ends from an early forward jump.
func LearnIntro(known, fromMs, toMs int64) int64 {
	jump := toMs - fromMs
	if fromMs > 180000 || jump < 20000 || jump > 150000 || toMs > 300000 {
		return known
	}
	if known <= 0 {
		return toMs
	}
	return (known + toMs) / 2
}

// ShowSkip: "Skip intro" from 2 s in until 3 s before the intro's end.
func ShowSkip(posMs, introEnd int64) bool {
	return introEnd > 0 && posMs >= 2000 && posMs < introEnd-3000
}

// ToClear: downloaded episodes finished more than graceMs ago (smart downloads).
func ToClear(downloads []Track, finished map[string]int64, now, graceMs int64) []Track {
	var out []Track
	for _, d := range downloads {
		at := finished[d.Key]
		if d.Kind == "video" && d.Source == "download" && ParseEpisode(d.Title) != nil && at > 0 && now-at >= graceMs {
			out = append(out, d)
		}
	}
	return out
}

// Mmss is m:ss / h:mm:ss.
func Mmss(ms int64) string {
	t := ms / 1000
	if t < 0 {
		t = 0
	}
	if t >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", t/3600, (t/60)%60, t%60)
	}
	return fmt.Sprintf("%d:%02d", t/60, t%60)
}

// Transcript is what was said up to pos (the last maxChars of it).
func Transcript(cues []Cue, posMs int64, maxChars int) string {
	var b strings.Builder
	last := ""
	for _, c := range cues {
		if c.StartMs > posMs {
			break
		}
		line := strings.ReplaceAll(c.Text, "\n", " ")
		if line == last {
			continue
		}
		last = line
		b.WriteString("[" + Mmss(c.StartMs) + "] " + line + "\n")
	}
	s := b.String()
	if len(s) <= maxChars {
		return s
	}
	s = s[len(s)-maxChars:]
	if i := strings.Index(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// RecapPrompt asks for a spoiler-free recap.
func RecapPrompt(title, transcript string) string {
	return "Here is what has been said so far in the video \u201c" + title + "\u201d (timestamps in brackets). Write a short recap for someone " +
		"coming back to it: 3 to 6 bullet points of what happened, in order, then one line on where it left off. Plain words, " +
		"no spoilers beyond this point, no preamble.\n\n" + transcript
}

// Batches splits cues into model-call-sized groups.
func Batches(cues []Cue, maxChars int) [][]Cue {
	var out [][]Cue
	var cur []Cue
	n := 0
	for _, c := range cues {
		if len(cur) > 0 && n+len(c.Text) > maxChars {
			out = append(out, cur)
			cur, n = nil, 0
		}
		cur = append(cur, c)
		n += len(c.Text) + 6
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

// TranslatePrompt asks for "n|translation" lines.
func TranslatePrompt(batch []Cue, language string) string {
	var b strings.Builder
	b.WriteString("Translate these subtitle lines into " + language + ". Keep each line's number and the format ")
	b.WriteString("\"number|translation\", one per line, same count, nothing else. Keep names, keep it short enough to read as subtitles.\n\n")
	for i, c := range batch {
		b.WriteString(itoa(i+1) + "|" + strings.ReplaceAll(c.Text, "\n", " ") + "\n")
	}
	return b.String()
}

var reTrLine = regexp.MustCompile(`^\s*(\d+)\s*[|:.)\-]\s*(.+)$`)

// ApplyTranslation puts the reply's lines back on batch (skipped lines keep the original).
func ApplyTranslation(batch []Cue, reply string) []Cue {
	m := map[int]string{}
	for _, l := range strings.Split(reply, "\n") {
		if g := reTrLine.FindStringSubmatch(l); g != nil {
			n, _ := strconv.Atoi(g[1])
			m[n] = strings.TrimSpace(g[2])
		}
	}
	out := make([]Cue, len(batch))
	for i, c := range batch {
		if t := m[i+1]; t != "" {
			c.Text = t
		}
		out[i] = c
	}
	return out
}

// TranslatedShare is how much came back changed.
func TranslatedShare(before, after []Cue) float64 {
	if len(before) == 0 {
		return 0
	}
	n := 0
	for i := range before {
		if i < len(after) && before[i].Text != after[i].Text {
			n++
		}
	}
	return float64(n) / float64(len(before))
}

var reNonW = regexp.MustCompile(`\W+`)

// FindVideo is the best library match for q: most words in the title, shortest title wins ties.
func FindVideo(videos []Track, q string) *Track {
	var words []string
	for _, w := range reNonW.Split(strings.ToLower(q), -1) {
		if len(w) > 1 {
			words = append(words, w)
		}
	}
	if len(words) == 0 {
		return nil
	}
	best, bestN := -1, 0
	for i, v := range videos {
		n := 0
		lt := strings.ToLower(v.Title)
		for _, w := range words {
			if strings.Contains(lt, w) {
				n++
			}
		}
		if n > bestN || (n == bestN && n > 0 && len(v.Title) < len(videos[best].Title)) {
			best, bestN = i, n
		}
	}
	if best < 0 {
		return nil
	}
	t := videos[best]
	return &t
}
