package home

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Spec is a widget asked for in plain words ("show me Arsenal scores").
type Spec struct {
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Category string            `json:"category"`
	Config   map[string]string `json:"config,omitempty"`
	Size     string            `json:"size,omitempty"`
}

// Widget turns a spec into an unsaved widget.
func (s Spec) Widget() Widget {
	return Widget{Type: s.Type, Title: s.Title, Category: s.Category, Config: s.Config, Size: NormSize(s.Size)}
}

func capWords(s string) string {
	f := strings.Fields(strings.TrimSpace(s))
	for i, w := range f {
		if len(w) <= 3 && strings.ToUpper(w) == w {
			continue
		}
		f[i] = strings.ToUpper(w[:1]) + w[1:]
	}
	return strings.Join(f, " ")
}

var currencies = map[string]bool{}
var coins = map[string]bool{}

func init() {
	for _, c := range strings.Fields("USD EUR GBP JPY CNY INR ZAR NGN KES ZMW GHS BRL MXN CAD AUD CHF AED SAR EGP TRY RUB PKR BDT IDR PHP VND THB MYR SGD KRW UGX TZS MWK BWP XOF XAF MAD ETB") {
		currencies[c] = true
	}
	for _, c := range strings.Fields("bitcoin btc ethereum eth solana sol dogecoin doge xrp ripple cardano ada bnb litecoin ltc tether usdt toncoin ton tron trx polkadot dot shiba avalanche chainlink") {
		coins[c] = true
	}
}

var (
	reWeather   = regexp.MustCompile(`\bweather(?: (?:in|for|at) (.+))?$`)
	reCoin1     = regexp.MustCompile(`\b(?:track |show |watch )?(?:the )?([a-z]+)(?: coin)? (?:price|prices)\b`)
	reCoin2     = regexp.MustCompile(`\b(?:price of|track|watch) ([a-z]+)\b`)
	reFx        = regexp.MustCompile(`\b([a-z]{3}) ?(?:to|/|→|in) ?([a-z]{3})\b`)
	reStock     = regexp.MustCompile(`\b([A-Za-z.]{1,6}) (?:stock|shares)\b`)
	reSports1   = regexp.MustCompile(`^(?:show (?:me )?)?(?:the )?(.+?) (?:scores?|results|fixtures|games|matches)$`)
	reSports2   = regexp.MustCompile(`\b(?:scores?|results|fixtures) (?:for|of) (?:the )?(.+)$`)
	reRepo      = regexp.MustCompile(`(?:github\.com/)?([A-Za-z0-9-]{1,39}/[A-Za-z0-9._-]{1,100})`)
	reSchedule  = regexp.MustCompile(`\b(next )?(routines?|reminders?|schedule|scheduled jobs|my jobs)\b`)
	reDeadlines = regexp.MustCompile(`\b(deadlines?|exams?|assignments?|homework|due dates?)\b`)
	reRecents   = regexp.MustCompile(`\b(recents?|recent (chats|projects|folders)|pick up|where i left off|continue)\b`)
	reNews1     = regexp.MustCompile(`\b(?:news|headlines|stories|articles) (?:about|on|from|for) (.+)$`)
	reNews2     = regexp.MustCompile(`^(?:show (?:me )?)?(?:the )?(?:latest )?(.+?) news$`)
	reNewsTop   = regexp.MustCompile(`^(?:top |latest )?(news|headlines)$`)
	reFeed      = regexp.MustCompile(`^https?://\S+\.(xml|rss)\b|/feed/?$|/rss/?$`)
)

// Parse understands the common asks; ok=false means "let Blaze work it out".
func Parse(text string) (Spec, bool) {
	t := strings.TrimRight(strings.TrimSpace(text), ".!?")
	l := strings.ToLower(t)
	if l == "" {
		return Spec{}, false
	}
	orig := func(m []int, g int) string { return strings.TrimSpace(t[m[2*g]:m[2*g+1]]) }
	if m := reWeather.FindStringSubmatchIndex(l); m != nil {
		city := ""
		if m[2] >= 0 {
			city = capWords(orig(m, 1))
		}
		if city != "" {
			return Spec{Weather, "Weather in " + city, "Weather", map[string]string{"city": city}, ""}, true
		}
		return Spec{Weather, "Weather", "Weather", map[string]string{"use_location": "true"}, ""}, true
	}
	if m := reCoin1.FindStringSubmatch(l); m != nil && coins[m[1]] {
		return Crypto(m[1]), true
	}
	if m := reCoin2.FindStringSubmatch(l); m != nil && coins[m[1]] {
		return Crypto(m[1]), true
	}
	if m := reFx.FindStringSubmatch(l); m != nil {
		a, b := strings.ToUpper(m[1]), strings.ToUpper(m[2])
		if currencies[a] && currencies[b] {
			return Spec{Prices, a + " to " + b, "Finance", map[string]string{"kind": "fx", "base": a, "symbols": b}, ""}, true
		}
	}
	if m := reStock.FindStringSubmatch(t); m != nil {
		s := strings.ToUpper(m[1])
		if l := strings.ToLower(s); l != "the" && l != "my" && l != "a" {
			return Spec{Prices, s + " stock", "Finance", map[string]string{"kind": "stock", "symbols": s}, ""}, true
		}
	}
	if m := reSports1.FindStringSubmatchIndex(l); m != nil {
		team := strings.TrimPrefix(strings.TrimPrefix(orig(m, 1), "the "), "The ")
		if team != "" {
			return Spec{Sports, capWords(team), "Sports", map[string]string{"team": capWords(team)}, ""}, true
		}
	}
	if m := reSports2.FindStringSubmatchIndex(l); m != nil {
		team := capWords(orig(m, 1))
		return Spec{Sports, team, "Sports", map[string]string{"team": team}, ""}, true
	}
	if strings.Contains(l, "github") || strings.Contains(l, "repo") {
		if m := reRepo.FindStringSubmatch(t); m != nil {
			if r := Repo(m[1]); r != "" {
				return Spec{GitHub, r, "Tech", map[string]string{"repo": r}, ""}, true
			}
		}
	}
	if reDeadlines.MatchString(l) {
		return Spec{Deadlines, "Deadlines", "Today", nil, ""}, true
	}
	if reSchedule.MatchString(l) {
		return Spec{Schedule, "Schedule", "Today", nil, ""}, true
	}
	if reRecents.MatchString(l) {
		return Spec{Recents, Label(Recents), "Pick up", nil, ""}, true
	}
	if m := reNews1.FindStringSubmatchIndex(l); m != nil {
		return NewsSpec(orig(m, 1)), true
	}
	if m := reNews2.FindStringSubmatchIndex(l); m != nil {
		return NewsSpec(orig(m, 1)), true
	}
	if reNewsTop.MatchString(l) {
		return Spec{News, "Top stories", "News", map[string]string{"query": ""}, ""}, true
	}
	if reFeed.MatchString(l) {
		return Spec{News, "Feed", "News", map[string]string{"feed": t}, ""}, true
	}
	return Spec{}, false
}

// NewsSpec is a news topic card.
func NewsSpec(q string) Spec {
	return Spec{News, capWords(q) + " news", "News", map[string]string{"query": q}, ""}
}

// Crypto is a coin price card.
func Crypto(w string) Spec {
	id := CoinID(w)
	return Spec{Prices, Pretty(id), "Finance", map[string]string{"kind": "crypto", "ids": id, "vs": "usd"}, ""}
}

// Starters are the generic first board on desktop: weather, top stories, schedule, pick up where you left
// off and the "tell me what you're into" card (mobile's starters, with Recents for the desktop).
func Starters() []Spec {
	return []Spec{
		{Weather, "Weather", "Weather", map[string]string{"use_location": "true"}, ""},
		{News, "Top stories", "News", map[string]string{"query": ""}, ""},
		{Schedule, "Schedule", "Today", nil, ""},
		{Recents, Label(Recents), "Pick up", nil, ""},
		{Into, "Tell me what you're into", "For you", nil, "S"},
	}
}

var (
	reInterested = regexp.MustCompile(`(?i)interested in ([^.\n]+)`)
	reLives      = regexp.MustCompile(`(?i)\b(?:lives in|based in|located in) ([A-Z][a-z]+(?: [A-Z][a-z]+)?)`)
	reCrypto     = regexp.MustCompile(`(?i)\b(bitcoin|btc|crypto)\b`)
	reCode       = regexp.MustCompile(`(?i)\b(github|programming|developer|coding|golang|kotlin)\b`)
	reStudy      = regexp.MustCompile(`(?i)\b(student|university|college|exam|assignment|semester)\b`)
)

// FromMemories seeds a board from what Blaze remembers when it is confident (≥2 hits), else nil.
func FromMemories(texts []string) []Spec {
	var out []Spec
	add := func(s Spec) {
		for _, o := range out {
			if o.Type == s.Type && fmt.Sprint(o.Config) == fmt.Sprint(s.Config) {
				return
			}
		}
		out = append(out, s)
	}
	all := strings.Join(texts, "\n")
	if m := reLives.FindStringSubmatch(all); m != nil {
		add(Spec{Weather, "Weather in " + m[1], "Weather", map[string]string{"city": m[1]}, ""})
	}
	for _, m := range reInterested.FindAllStringSubmatch(all, 3) {
		for _, p := range strings.FieldsFunc(m[1], func(r rune) bool { return r == ';' || r == ',' }) {
			p = strings.TrimSpace(p[strings.Index(p, ":")+1:])
			if len(p) >= 2 && len(p) <= 40 {
				if s, ok := Parse(p); ok {
					add(s)
				} else {
					add(NewsSpec(p))
				}
			}
		}
	}
	if reCrypto.MatchString(all) {
		add(Crypto("bitcoin"))
	}
	if reStudy.MatchString(all) {
		add(Spec{Deadlines, "Deadlines", "Today", nil, ""})
	}
	if reCode.MatchString(all) {
		add(NewsSpec("Programming"))
	}
	if len(out) < 2 {
		return nil
	}
	return out
}

var reName = regexp.MustCompile(`(?i)\b(?:my name is|user'?s name is|the user is called|call me|name:)\s+([A-Z][\p{L}'-]{1,30})`)

// NameFrom finds the user's first name in memories ("My name is Ada", "User's name is Ada").
func NameFrom(texts []string) string {
	for _, t := range texts {
		if m := reName.FindStringSubmatch(t); m != nil {
			return m[1]
		}
	}
	return ""
}

// ---------------- desktop cards (data passed in) ----------------

// Job is a scheduled job or reminder (sched.Job without the package dependency).
type Job struct {
	Name, When, Prompt, Command string
}

var reOnce = regexp.MustCompile(`(?i)^once (\d{4}-\d{2}-\d{2}) (\d{1,2}:\d{2})`)

// scheduleLines: upcoming one-time reminders by time (note "Reminder"), then the repeating jobs.
func scheduleLines(jobs []Job, now time.Time) []Line {
	type item struct {
		at   time.Time
		line Line
	}
	var once []item
	var repeat []Line
	for _, j := range jobs {
		what := j.Prompt
		if what == "" {
			what = j.Command
		}
		if len([]rune(what)) > 90 {
			what = string([]rune(what)[:89]) + "…"
		}
		if m := reOnce.FindStringSubmatch(strings.TrimSpace(j.When)); m != nil {
			t, err := time.ParseInLocation("2006-01-02 15:04", m[1]+" "+m[2], now.Location())
			if err != nil || t.Before(now) {
				continue
			}
			once = append(once, item{t, Line{T: j.Name, S: t.Format("Mon 2 Jan · 15:04"), N: "Reminder"}})
			continue
		}
		repeat = append(repeat, Line{T: j.Name, S: strings.TrimSpace(j.When + " · " + what)})
	}
	for i := 1; i < len(once); i++ {
		for k := i; k > 0 && once[k].at.Before(once[k-1].at); k-- {
			once[k], once[k-1] = once[k-1], once[k]
		}
	}
	var lines []Line
	for _, o := range once {
		lines = append(lines, o.line)
	}
	return append(lines, repeat...)
}

// ScheduleData is the Schedule card (mobile's "Next routine"): what runs next, then the rest.
func ScheduleData(jobs []Job, now time.Time) Data {
	lines := scheduleLines(jobs, now)
	if len(lines) == 0 {
		return Data{Headline: "Schedule", Change: "Nothing scheduled. Ask Blaze, e.g. “every weekday at 9, brief me”.", Action: "ask:Set up a morning briefing for me every weekday at 9"}
	}
	return Data{Headline: "Next up", Value: lines[0].T, Change: lines[0].S, Lines: lines[1:]}
}

var reDue = regexp.MustCompile(`(?i)\b(due|deadline|exam|test|assignment|submit|submission|quiz|presentation)\b`)

// DeadlinesData is upcoming reminders plus memories that mention a deadline.
func DeadlinesData(jobs []Job, memories []string, now time.Time) Data {
	var lines []Line
	for _, l := range scheduleLines(jobs, now) {
		if l.N == "Reminder" {
			lines = append(lines, l)
		}
	}
	for _, m := range memories {
		if len(lines) >= 8 {
			break
		}
		if reDue.MatchString(m) {
			if len([]rune(m)) > 120 {
				m = string([]rune(m)[:119]) + "…"
			}
			lines = append(lines, Line{T: m, N: "From memory"})
		}
	}
	if len(lines) == 0 {
		return Data{Headline: "Deadlines", Change: "No deadlines yet. Tell Blaze about one, e.g. “my report is due Friday”.", Action: "ask:Help me track my deadlines"}
	}
	return Data{Headline: "Deadlines", Value: lines[0].T, Change: lines[0].S, Lines: lines[1:]}
}

// Recent is something to pick up: the last chat turn, a project, a folder.
type Recent struct {
	Title, Sub, Kind, Target string // Kind: chat, project, folder
}

// RecentsData lists where the user left off. Lines carry "blaze:<kind>:<target>" links the UI opens.
func RecentsData(items []Recent) Data {
	var lines []Line
	for _, r := range items {
		if strings.TrimSpace(r.Title) == "" {
			continue
		}
		lines = append(lines, Line{T: r.Title, S: r.Sub, U: "blaze:" + r.Kind + ":" + r.Target, N: map[string]string{"chat": "Chat", "project": "Project", "folder": "Folder"}[r.Kind]})
	}
	if len(lines) == 0 {
		return Data{Headline: Label(Recents), Change: "Nothing yet. Your chats, projects and folders show up here."}
	}
	return Data{Headline: Label(Recents), Lines: lines}
}

// CustomData is a Blaze-composed card: config "text", "value", "url" and "items" (one per line,
// "title | sub | url").
func CustomData(w Widget) Data {
	d := Data{Headline: w.Title, Value: firstRunes(w.Opt("value"), 40), Change: firstRunes(w.Opt("text"), 600), Link: SafeURL(w.Opt("url"))}
	for _, row := range strings.Split(w.Config["items"], "\n") {
		p := strings.Split(row, "|")
		if strings.TrimSpace(p[0]) == "" {
			continue
		}
		l := Line{T: strings.TrimSpace(p[0])}
		if len(p) > 1 {
			l.S = strings.TrimSpace(p[1])
		}
		if len(p) > 2 {
			l.U = SafeURL(p[2])
		}
		d.Lines = append(d.Lines, l)
		if len(d.Lines) == 12 {
			break
		}
	}
	return d
}
