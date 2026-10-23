package home

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Parsers for the board's free, keyless sources. Pure (strings in, Data out), ported from Blaze
// Mobile's HomeParsers.kt and tested on fixtures.

// SourceError carries a plain line for the card.
type SourceError struct{ Msg string }

func (e SourceError) Error() string { return e.Msg }

func srcErr(f string, a ...any) error { return SourceError{fmt.Sprintf(f, a...)} }

// HTTPLine is a plain line for a failed request.
func HTTPLine(code int, what string) string {
	switch {
	case code == 404:
		return "Couldn't find " + what + "."
	case code == 429:
		return "The source is busy (too many requests). I'll try again later."
	case code >= 500 && code <= 599:
		return "The source is having trouble. I'll try again later."
	}
	return fmt.Sprintf("Couldn't update %s (error %d).", what, code)
}

// ---------------- dates ----------------

// ParseDate reads RFC 822 (RSS), ISO 8601 (Atom, APIs) or a zoneless date-time (UTC). 0 = unreadable.
func ParseDate(s string) int64 {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0
	}
	for _, l := range []string{time.RFC1123Z, time.RFC1123, "Mon, 2 Jan 2006 15:04:05 -0700", "Mon, 2 Jan 2006 15:04:05 MST", "02 Jan 2006 15:04:05 MST",
		time.RFC3339, time.RFC3339Nano, "2006-01-02T15:04Z07:00", "2006-01-02T15:04:05", "2006-01-02 15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if v, err := time.Parse(l, t); err == nil {
			return v.UnixMilli()
		}
	}
	return 0
}

// Relative is "just now", "12 min ago", "3 h ago", "2 d ago" or "18 Oct".
func Relative(t, now int64) string {
	if t <= 0 {
		return ""
	}
	m := (now - t) / 60000
	switch {
	case m < 0:
		return ""
	case m < 1:
		return "just now"
	case m < 60:
		return fmt.Sprintf("%d min ago", m)
	case m < 24*60:
		return fmt.Sprintf("%d h ago", m/60)
	case m < 7*24*60:
		return fmt.Sprintf("%d d ago", m/(24*60))
	}
	return time.UnixMilli(t).Format("2 Jan")
}

// DayTime is "Sat 18 Oct · 17:30" in loc.
func DayTime(t int64, loc *time.Location) string {
	if t <= 0 {
		return ""
	}
	if loc == nil {
		loc = time.Local
	}
	return time.UnixMilli(t).In(loc).Format("Mon 2 Jan · 15:04")
}

// ---------------- RSS / Atom ----------------

// Entry is one feed item.
type Entry struct {
	Title, Link, Source, Image string
	Published                  int64
}

var (
	itemRe  = regexp.MustCompile(`(?is)<(item|entry)\b[^>]*>(.*?)</(?:item|entry)>`)
	cdataRe = regexp.MustCompile(`(?s)<!\[CDATA\[(.*?)]]>`)
	tagsRe  = regexp.MustCompile(`(?s)<[^>]+>`)
	spaceRe = regexp.MustCompile(`\s+`)
	imgRe   = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)`)
	encRe   = regexp.MustCompile(`(?is)<enclosure\b[^>]*type="image[^>]*>`)
	urlAttr = regexp.MustCompile(`(?i)url\s*=\s*"([^"]+)"`)
)

func tag(s, name string) string {
	r := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(name) + `\b[^>]*?(?:/>|>(.*?)</` + regexp.QuoteMeta(name) + `>)`)
	m := r.FindStringSubmatch(s)
	if m == nil {
		return ""
	}
	return m[1]
}

func attr(s, name, a string) string {
	r := regexp.MustCompile(`(?is)<` + regexp.QuoteMeta(name) + `\b([^>]*)>`)
	all := r.FindAllStringSubmatch(s, -1)
	if len(all) == 0 {
		return ""
	}
	pick := all[0][1]
	for _, m := range all {
		if strings.Contains(m[1], `rel="alternate"`) {
			pick = m[1]
			break
		}
	}
	v := regexp.MustCompile(`(?i)\b` + a + `\s*=\s*"([^"]*)"`).FindStringSubmatch(pick)
	if v == nil {
		return ""
	}
	return html.UnescapeString(v[1])
}

// Text unwraps CDATA, strips tags, decodes entities and squashes whitespace.
func Text(s string) string {
	t := cdataRe.ReplaceAllString(strings.TrimSpace(s), "$1")
	t = html.UnescapeString(t)
	t = tagsRe.ReplaceAllString(t, " ")
	return strings.TrimSpace(spaceRe.ReplaceAllString(t, " "))
}

func feedImage(b string) string {
	raw := attr(b, "media:thumbnail", "url")
	if raw == "" {
		raw = attr(b, "media:content", "url")
	}
	if raw == "" {
		if e := encRe.FindString(b); e != "" {
			if m := urlAttr.FindStringSubmatch(e); m != nil {
				raw = m[1]
			}
		}
	}
	if raw == "" {
		raw = Text(tag(b, "News:Image"))
	}
	if raw == "" {
		d := html.UnescapeString(cdataRe.ReplaceAllString(tag(b, "description"), "$1"))
		if m := imgRe.FindStringSubmatch(d); m != nil {
			raw = m[1]
		}
	}
	u := html.UnescapeString(strings.TrimSpace(raw))
	switch {
	case strings.Contains(u, "bing.com/th?"):
		return "https://" + u[strings.Index(u, "://")+3:] + "&w=480&h=270&c=7"
	case strings.HasPrefix(u, "https://"):
		return u
	case strings.HasPrefix(u, "http://"):
		return "https://" + strings.TrimPrefix(u, "http://")
	case strings.HasPrefix(u, "//"):
		return "https:" + u
	}
	return ""
}

// ParseFeed reads RSS 2.0 <item> or Atom <entry>, in feed order.
func ParseFeed(xml string, max int) []Entry {
	var out []Entry
	for _, m := range itemRe.FindAllStringSubmatch(xml, -1) {
		if len(out) >= max {
			break
		}
		b := m[2]
		title := Text(tag(b, "title"))
		if title == "" {
			continue
		}
		link := Text(tag(b, "link"))
		if link == "" {
			link = attr(b, "link", "href")
		}
		src := Text(tag(b, "source"))
		if src == "" {
			src = Text(tag(tag(b, "author"), "name"))
		}
		if src == "" {
			src = Text(tag(b, "News:Source"))
		}
		date := ""
		for _, k := range []string{"pubDate", "published", "updated", "dc:date"} {
			if date = Text(tag(b, k)); date != "" {
				break
			}
		}
		if src != "" && strings.HasSuffix(title, " - "+src) {
			title = strings.TrimSpace(strings.TrimSuffix(title, " - "+src))
		}
		out = append(out, Entry{Title: title, Link: link, Source: src, Published: ParseDate(date), Image: feedImage(b)})
	}
	return out
}

// FeedTitle is the feed's own title.
func FeedTitle(xml string) string {
	head := xml
	for _, k := range []string{"<item", "<entry"} {
		if i := strings.Index(head, k); i >= 0 {
			head = head[:i]
		}
	}
	return Text(tag(head, "title"))
}

func words(t string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.FieldsFunc(strings.ToLower(t), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) }) {
		if len([]rune(w)) > 2 {
			out[w] = true
		}
	}
	return out
}

// BorrowImages gives entries without a picture the picture of the same story in other (≥60% of the
// shorter headline's words shared, at least 3 words), like mobile does with Bing News.
func BorrowImages(entries, other []Entry) []Entry {
	type pic struct {
		w   map[string]bool
		img string
	}
	var pics []pic
	for _, o := range other {
		if o.Image != "" {
			pics = append(pics, pic{words(o.Title), o.Image})
		}
	}
	if len(pics) == 0 {
		return entries
	}
	out := append([]Entry(nil), entries...)
	for i, e := range out {
		if e.Image != "" {
			continue
		}
		w := words(e.Title)
		if len(w) < 3 {
			continue
		}
		best, score := "", 0.0
		for _, p := range pics {
			n := 0
			for k := range w {
				if p.w[k] {
					n++
				}
			}
			d := len(w)
			if len(p.w) < d {
				d = len(p.w)
			}
			if d < 1 {
				d = 1
			}
			if s := float64(n) / float64(d); s > score {
				best, score = p.img, s
			}
		}
		if score >= 0.6 {
			out[i].Image = best
		}
	}
	return out
}

// FeedData turns entries into card content.
func FeedData(entries []Entry, headline string, now int64) Data {
	d := Data{Headline: headline}
	for _, e := range entries {
		sub := []string{}
		if e.Source != "" {
			sub = append(sub, e.Source)
		}
		if r := Relative(e.Published, now); r != "" {
			sub = append(sub, r)
		}
		d.Lines = append(d.Lines, Line{T: e.Title, S: strings.Join(sub, " · "), U: SafeURL(e.Link), I: e.Image})
	}
	return d
}

// NewsURL is Google News RSS for a language and region (top stories when query is blank).
func NewsURL(query, lang, country string) string {
	if lang == "" {
		lang = "en"
	}
	if country == "" {
		country = "US"
	}
	tail := "hl=" + lang + "-" + country + "&gl=" + country + "&ceid=" + country + ":" + lang
	if strings.TrimSpace(query) == "" {
		return "https://news.google.com/rss?" + tail
	}
	return "https://news.google.com/rss/search?q=" + url.QueryEscape(strings.TrimSpace(query)) + "&" + tail
}

// SafeURL keeps only http(s) links.
func SafeURL(u string) string {
	u = strings.TrimSpace(u)
	if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "http://") {
		return u
	}
	return ""
}

// ---------------- Open-Meteo ----------------

// Place is a geocoded city.
type Place struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"latitude"`
	Lon     float64 `json:"longitude"`
	Country string  `json:"country"`
}

func ForecastURL(lat, lon float64, fahrenheit bool) string {
	u := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%.4f&longitude=%.4f", lat, lon) +
		"&current=temperature_2m,apparent_temperature,weather_code,wind_speed_10m,is_day" +
		"&daily=weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max&timezone=auto&forecast_days=4"
	if fahrenheit {
		u += "&temperature_unit=fahrenheit&wind_speed_unit=mph"
	}
	return u
}

func GeocodeURL(city, lang string) string {
	if lang == "" {
		lang = "en"
	}
	return "https://geocoding-api.open-meteo.com/v1/search?count=1&format=json&language=" + lang + "&name=" + url.QueryEscape(strings.TrimSpace(city))
}

func ParsePlace(js string) (Place, bool) {
	var o struct {
		Results []Place `json:"results"`
	}
	if json.Unmarshal([]byte(js), &o) != nil || len(o.Results) == 0 {
		return Place{}, false
	}
	return o.Results[0], true
}

// UsesFahrenheit: countries that use °F by default.
func UsesFahrenheit(country string) bool {
	switch strings.ToUpper(country) {
	case "US", "LR", "MM", "BS", "BZ", "KY", "PW", "FM", "MH":
		return true
	}
	return false
}

// WMO is a weather code in plain words.
func WMO(code int) string {
	switch code {
	case 0:
		return "Clear"
	case 1:
		return "Mostly clear"
	case 2:
		return "Partly cloudy"
	case 3:
		return "Cloudy"
	case 45, 48:
		return "Fog"
	case 51, 53, 55:
		return "Drizzle"
	case 56, 57:
		return "Freezing drizzle"
	case 61, 63:
		return "Rain"
	case 65:
		return "Heavy rain"
	case 66, 67:
		return "Freezing rain"
	case 71, 73, 75, 77:
		return "Snow"
	case 80, 81:
		return "Showers"
	case 82:
		return "Heavy showers"
	case 85, 86:
		return "Snow showers"
	case 95:
		return "Thunderstorm"
	case 96, 99:
		return "Thunderstorm with hail"
	}
	return "—"
}

func round(f float64) int { return int(math.Round(f)) }

// ParseForecast reads an Open-Meteo forecast into a card.
func ParseForecast(js, place string) (Data, error) {
	var o struct {
		Error   bool `json:"error"`
		Current *struct {
			T     float64  `json:"temperature_2m"`
			Feels *float64 `json:"apparent_temperature"`
			Code  int      `json:"weather_code"`
			Wind  float64  `json:"wind_speed_10m"`
		} `json:"current"`
		Units struct {
			T    string `json:"temperature_2m"`
			Wind string `json:"wind_speed_10m"`
		} `json:"current_units"`
		Daily *struct {
			Time []string  `json:"time"`
			Hi   []float64 `json:"temperature_2m_max"`
			Lo   []float64 `json:"temperature_2m_min"`
			Code []int     `json:"weather_code"`
			Rain []*int    `json:"precipitation_probability_max"`
		} `json:"daily"`
	}
	if err := json.Unmarshal([]byte(js), &o); err != nil || o.Error || o.Current == nil {
		return Data{}, srcErr("The weather service is busy. I'll try again later.")
	}
	unit, wind := o.Units.T, o.Units.Wind
	if unit == "" {
		unit = "°C"
	}
	if wind == "" {
		wind = "km/h"
	}
	c := o.Current
	change := WMO(c.Code)
	if c.Feels != nil {
		change += fmt.Sprintf(" · feels %d°", round(*c.Feels))
	}
	change += fmt.Sprintf(" · wind %d %s", round(c.Wind), wind)
	d := Data{Headline: place, Value: fmt.Sprintf("%d%s", round(c.T), firstRunes(unit, 2)), Change: change}
	if o.Daily != nil {
		for i, day := range o.Daily.Time {
			name := "Today"
			if i > 0 {
				if t, err := time.Parse("2006-01-02", day); err == nil {
					name = t.Format("Mon")
				} else {
					name = day
				}
			}
			sub := WMO(at(o.Daily.Code, i, -1))
			if i < len(o.Daily.Rain) && o.Daily.Rain[i] != nil && *o.Daily.Rain[i] > 0 {
				sub += fmt.Sprintf(" · rain %d%%", *o.Daily.Rain[i])
			}
			d.Lines = append(d.Lines, Line{T: name, S: sub, N: fmt.Sprintf("%d° / %d°", round(atf(o.Daily.Hi, i)), round(atf(o.Daily.Lo, i)))})
		}
	}
	return d, nil
}

func firstRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[:n]
	}
	return string(r)
}
func at(a []int, i, def int) int {
	if i < len(a) {
		return a[i]
	}
	return def
}
func atf(a []float64, i int) float64 {
	if i < len(a) {
		return a[i]
	}
	return 0
}

// ---------------- money ----------------

// Money is "64,210 USD", "1.23 EUR", "0.0012 USD".
func Money(v float64, cur string) string {
	var s string
	switch {
	case v >= 1000:
		s = groupThousands(strconv.FormatFloat(math.Round(v), 'f', 0, 64))
	case v >= 1:
		s = strconv.FormatFloat(v, 'f', 2, 64)
	case v >= 0.01:
		s = strconv.FormatFloat(v, 'f', 4, 64)
	default:
		s = strconv.FormatFloat(v, 'f', 6, 64)
	}
	return s + " " + cur
}

func groupThousands(s string) string {
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var b strings.Builder
	for i, r := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(r)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// Pct is "▲ 1.2%" / "▼ 0.4%" ("" for NaN).
func Pct(p float64) string {
	if math.IsNaN(p) {
		return ""
	}
	arrow := "▲ "
	if p < 0 {
		arrow = "▼ "
	}
	return arrow + strconv.FormatFloat(math.Abs(p), 'f', 1, 64) + "%"
}

// ---------------- CoinGecko ----------------

var coinIDs = map[string]string{"btc": "bitcoin", "eth": "ethereum", "sol": "solana", "bnb": "binancecoin", "xrp": "ripple",
	"ada": "cardano", "doge": "dogecoin", "usdt": "tether", "usdc": "usd-coin", "trx": "tron", "ton": "the-open-network",
	"dot": "polkadot", "ltc": "litecoin", "avax": "avalanche-2", "shib": "shiba-inu", "matic": "matic-network", "link": "chainlink"}

// CoinID maps "BTC" / "Bitcoin" to a CoinGecko id.
func CoinID(s string) string {
	l := strings.ToLower(strings.TrimSpace(s))
	if v, ok := coinIDs[l]; ok {
		return v
	}
	return strings.ReplaceAll(l, " ", "-")
}

func CoinPriceURL(ids []string, vs string) string {
	return "https://api.coingecko.com/api/v3/simple/price?ids=" + strings.Join(ids, ",") + "&vs_currencies=" + strings.ToLower(vs) + "&include_24hr_change=true"
}

func CoinSearchURL(q string) string {
	return "https://api.coingecko.com/api/v3/search?query=" + url.QueryEscape(strings.TrimSpace(q))
}

func ParseCoinSearch(js string) string {
	var o struct {
		Coins []struct {
			ID string `json:"id"`
		} `json:"coins"`
	}
	if json.Unmarshal([]byte(js), &o) != nil || len(o.Coins) == 0 {
		return ""
	}
	return o.Coins[0].ID
}

// Pretty is "the-open-network" → "The Open Network".
func Pretty(id string) string {
	p := strings.Split(id, "-")
	for i, w := range p {
		if w != "" {
			p[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(p, " ")
}

func ParseCoins(js string, ids []string, vs string) (Data, error) {
	var o map[string]map[string]float64
	if json.Unmarshal([]byte(js), &o) != nil {
		return Data{}, srcErr("Couldn't find that coin. Try its full name, like Bitcoin.")
	}
	v := strings.ToLower(vs)
	var lines []Line
	first := ""
	for _, id := range ids {
		c, ok := o[id]
		if !ok {
			continue
		}
		p, ok := c[v]
		if !ok {
			continue
		}
		if first == "" {
			first = id
		}
		ch, okc := c[v+"_24h_change"]
		if !okc {
			ch = math.NaN()
		}
		lines = append(lines, Line{T: Pretty(id), U: "https://www.coingecko.com/en/coins/" + id, N: strings.TrimSpace(Money(p, strings.ToUpper(v)) + "  " + Pct(ch))})
	}
	if first == "" {
		return Data{}, srcErr("Couldn't find that coin. Try its full name, like Bitcoin.")
	}
	ch, okc := o[first][v+"_24h_change"]
	change := ""
	if okc {
		change = Pct(ch) + " today"
	}
	d := Data{Headline: Pretty(first), Value: Money(o[first][v], strings.ToUpper(v)), Change: change, Link: "https://www.coingecko.com/en/coins/" + first}
	if len(lines) > 1 {
		d.Lines = lines
	}
	return d, nil
}

// ---------------- exchange rates ----------------

func FxURL(base string) string {
	return "https://open.er-api.com/v6/latest/" + strings.ToUpper(strings.TrimSpace(base))
}

func ParseFx(js, base string, targets []string) (Data, error) {
	var o struct {
		Result string             `json:"result"`
		Rates  map[string]float64 `json:"rates"`
	}
	b := strings.ToUpper(base)
	if json.Unmarshal([]byte(js), &o) != nil || o.Result != "success" {
		return Data{}, srcErr("Couldn't get exchange rates for %s.", b)
	}
	var lines []Line
	var names []string
	for _, t := range targets {
		t = strings.ToUpper(strings.TrimSpace(t))
		if t == "" || t == b {
			continue
		}
		names = append(names, t)
		if r, ok := o.Rates[t]; ok {
			lines = append(lines, Line{T: "1 " + b, N: Money(r, t)})
		}
	}
	if len(lines) == 0 {
		return Data{}, srcErr("Couldn't find those currencies.")
	}
	d := Data{Headline: b + " → " + strings.Join(names, ", "), Value: lines[0].N, Change: "1 " + b, Link: "https://www.exchangerate-api.com"}
	if len(lines) > 1 {
		d.Lines = lines
	}
	return d, nil
}

// ---------------- stocks (Yahoo's public chart, best effort) ----------------

func StockURL(sym string) string {
	return "https://query1.finance.yahoo.com/v8/finance/chart/" + url.PathEscape(strings.ToUpper(strings.TrimSpace(sym))) + "?range=1d&interval=1d"
}

func ParseStock(js, sym string) (Line, error) {
	var o struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol   string   `json:"symbol"`
					Exchange string   `json:"fullExchangeName"`
					Currency string   `json:"currency"`
					Price    *float64 `json:"regularMarketPrice"`
					Prev     *float64 `json:"chartPreviousClose"`
					Prev2    *float64 `json:"previousClose"`
				} `json:"meta"`
			} `json:"result"`
		} `json:"chart"`
	}
	s := strings.ToUpper(sym)
	if json.Unmarshal([]byte(js), &o) != nil || len(o.Chart.Result) == 0 {
		return Line{}, srcErr("Couldn't find the stock %s.", s)
	}
	m := o.Chart.Result[0].Meta
	if m.Price == nil {
		return Line{}, srcErr("No price for %s right now.", s)
	}
	prev := m.Prev
	if prev == nil {
		prev = m.Prev2
	}
	pct := math.NaN()
	if prev != nil && *prev != 0 {
		pct = (*m.Price - *prev) / *prev * 100
	}
	cur := m.Currency
	if cur == "" {
		cur = "USD"
	}
	name := m.Symbol
	if name == "" {
		name = s
	}
	return Line{T: name, S: m.Exchange, U: "https://finance.yahoo.com/quote/" + s, N: strings.TrimSpace(Money(*m.Price, cur) + "  " + Pct(pct))}, nil
}

// ---------------- TheSportsDB (free public key "3") ----------------

const sportsBase = "https://www.thesportsdb.com/api/v1/json/3"

type Team struct{ ID, Name, League, Sport string }

func TeamSearchURL(team string) string {
	return sportsBase + "/searchteams.php?t=" + url.QueryEscape(strings.TrimSpace(team))
}
func TeamLastURL(id string) string { return sportsBase + "/eventslast.php?id=" + id }
func TeamNextURL(id string) string { return sportsBase + "/eventsnext.php?id=" + id }

// ParseTeam picks an exact name / short name / alternate name match first, then the first result.
func ParseTeam(js, query string) (Team, bool) {
	var o struct {
		Teams []map[string]any `json:"teams"`
	}
	if json.Unmarshal([]byte(js), &o) != nil || len(o.Teams) == 0 {
		return Team{}, false
	}
	q := strings.ToLower(strings.TrimSpace(query))
	s := func(m map[string]any, k string) string { v, _ := m[k].(string); return v }
	pick := o.Teams[0]
	found := false
	for _, k := range []string{"strTeam", "strTeamShort"} {
		for _, t := range o.Teams {
			if !found && strings.ToLower(s(t, k)) == q {
				pick, found = t, true
			}
		}
	}
	if !found {
		for _, t := range o.Teams {
			for _, a := range strings.Split(strings.ToLower(s(t, "strTeamAlternate")), ",") {
				if !found && strings.TrimSpace(a) == q {
					pick, found = t, true
				}
			}
		}
	}
	return Team{s(pick, "idTeam"), s(pick, "strTeam"), s(pick, "strLeague"), s(pick, "strSport")}, true
}

type Event struct {
	Name, Home, Away, HomeScore, AwayScore, League, Status string
	Time                                                   int64
}

func ParseEvents(js, key string) []Event {
	var o map[string][]map[string]any
	if json.Unmarshal([]byte(js), &o) != nil {
		return nil
	}
	s := func(m map[string]any, k string) string {
		switch v := m[k].(type) {
		case string:
			if v == "null" {
				return ""
			}
			return v
		case float64:
			return strconv.Itoa(int(v))
		}
		return ""
	}
	var out []Event
	for _, e := range o[key] {
		ts := s(e, "strTimestamp")
		if ts == "" {
			ts = strings.TrimSpace(s(e, "dateEvent") + " " + s(e, "strTime"))
		}
		ts = strings.TrimSuffix(ts, "+00:00")
		out = append(out, Event{s(e, "strEvent"), s(e, "strHomeTeam"), s(e, "strAwayTeam"), s(e, "intHomeScore"), s(e, "intAwayScore"),
			s(e, "strLeague"), s(e, "strStatus"), ParseDate(ts)})
	}
	return out
}

func SportsData(t Team, last, next []Event, loc *time.Location) Data {
	link := "https://www.thesportsdb.com/team/" + t.ID
	var lines []Line
	sort.Slice(next, func(i, j int) bool { return next[i].Time < next[j].Time })
	for i, e := range next {
		if i == 3 {
			break
		}
		sub := DayTime(e.Time, loc)
		if e.League != "" {
			sub += " · " + e.League
		}
		lines = append(lines, Line{T: e.Home + " vs " + e.Away, S: sub, U: link, N: "Next"})
	}
	sort.Slice(last, func(i, j int) bool { return last[i].Time > last[j].Time })
	for i, e := range last {
		if i == 3 {
			break
		}
		score := e.Name
		if e.HomeScore != "" && e.AwayScore != "" {
			score = e.Home + " " + e.HomeScore + "–" + e.AwayScore + " " + e.Away
		}
		st := e.Status
		if st == "" {
			st = "Result"
		}
		if e.League != "" {
			st += " · " + e.League
		}
		lines = append(lines, Line{T: score, S: st, U: link})
	}
	if len(lines) == 0 {
		return Data{Headline: t.Name, Change: "No recent or upcoming games listed.", Link: link}
	}
	d := Data{Headline: t.Name, Lines: lines, Link: link}
	if len(last) > 0 {
		top := last[0]
		if top.HomeScore != "" {
			d.Value = top.HomeScore + "–" + top.AwayScore
		}
		d.Change = top.Home + " vs " + top.Away
	}
	return d
}

// ---------------- GitHub public events ----------------

var (
	ownerRe = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)
	repoRe  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

// Repo normalises "github.com/owner/name(.git)" to "owner/name" ("" when it isn't one).
func Repo(s string) string {
	t := strings.TrimSpace(s)
	for _, p := range []string{"https://", "http://", "www.", "github.com/"} {
		t = strings.TrimPrefix(t, p)
	}
	t = strings.Trim(strings.TrimSuffix(t, ".git"), "/")
	p := strings.Split(t, "/")
	if len(p) >= 2 && ownerRe.MatchString(p[0]) && repoRe.MatchString(p[1]) {
		return p[0] + "/" + p[1]
	}
	return ""
}

func RepoEventsURL(repo string) string {
	return "https://api.github.com/repos/" + repo + "/events?per_page=15"
}

func ParseRepoEvents(js, repo string, now int64) (Data, error) {
	var a []struct {
		Type  string `json:"type"`
		Actor struct {
			Login string `json:"login"`
		} `json:"actor"`
		Created string         `json:"created_at"`
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal([]byte(js), &a); err != nil {
		var m struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(js), &m)
		if strings.Contains(strings.ToLower(m.Message), "rate limit") {
			return Data{}, srcErr("GitHub's free limit is used up for now. I'll try again later.")
		}
		return Data{}, srcErr("Couldn't find the repo %s.", repo)
	}
	obj := func(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
	str := func(m map[string]any, k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case float64:
			return strconv.Itoa(int(v))
		}
		return ""
	}
	cap1 := func(s string) string {
		if s == "" {
			return s
		}
		return strings.ToUpper(s[:1]) + s[1:]
	}
	home := "https://github.com/" + repo
	var lines []Line
	for _, e := range a {
		p := e.Payload
		if p == nil {
			p = map[string]any{}
		}
		var what, u string
		switch e.Type {
		case "PushEvent":
			cs, _ := p["commits"].([]any)
			n := len(cs)
			if sz, ok := p["size"].(float64); ok {
				n = int(sz)
			}
			msg := ""
			if len(cs) > 0 {
				if c, ok := cs[len(cs)-1].(map[string]any); ok {
					msg = strings.SplitN(str(c, "message"), "\n", 2)[0]
				}
			}
			if msg == "" {
				msg = fmt.Sprintf("Pushed %d commit%s", n, map[bool]string{true: "", false: "s"}[n == 1])
			}
			what, u = msg, home+"/commits"
		case "PullRequestEvent":
			pr := obj(p, "pull_request")
			what, u = fmt.Sprintf("%s PR #%s: %s", cap1(str(p, "action")), str(pr, "number"), str(pr, "title")), str(pr, "html_url")
		case "IssuesEvent":
			is := obj(p, "issue")
			what, u = fmt.Sprintf("%s issue #%s: %s", cap1(str(p, "action")), str(is, "number"), str(is, "title")), str(is, "html_url")
		case "IssueCommentEvent":
			is := obj(p, "issue")
			what, u = fmt.Sprintf("Commented on #%s: %s", str(is, "number"), str(is, "title")), str(obj(p, "comment"), "html_url")
		case "ReleaseEvent":
			r := obj(p, "release")
			what, u = "Released "+str(r, "tag_name"), str(r, "html_url")
		case "CreateEvent":
			what, u = strings.TrimSpace("Created "+str(p, "ref_type")+" "+str(p, "ref")), home
		case "WatchEvent":
			what, u = "Starred the repo", home
		case "ForkEvent":
			what, u = "Forked the repo", home
		default:
			continue
		}
		sub := []string{}
		if e.Actor.Login != "" {
			sub = append(sub, e.Actor.Login)
		}
		if r := Relative(ParseDate(e.Created), now); r != "" {
			sub = append(sub, r)
		}
		lines = append(lines, Line{T: what, S: strings.Join(sub, " · "), U: SafeURL(u)})
	}
	d := Data{Headline: repo, Lines: lines, Link: home}
	if len(lines) == 0 {
		d.Change = "No recent activity."
	}
	return d, nil
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
