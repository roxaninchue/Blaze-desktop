package home

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Net does one GET: status and body. Swappable for tests.
type Net func(ctx context.Context, url string, headers map[string]string) (int, string, error)

// HTTPNet is the real network (20 s timeout, 2 MB cap).
func HTTPNet(lang string) Net {
	hc := &http.Client{Timeout: 20 * time.Second}
	return func(ctx context.Context, u string, h map[string]string) (int, string, error) {
		req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
		if err != nil {
			return 0, "", err
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Desktop) BlazeHome/1.0")
		if lang != "" {
			req.Header.Set("Accept-Language", lang)
		}
		for k, v := range h {
			req.Header.Set(k, v)
		}
		res, err := hc.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer res.Body.Close()
		b, err := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		return res.StatusCode, string(b), err
	}
}

// Fetcher fetches the web widgets.
type Fetcher struct {
	Net     Net
	Lang    string         // "en"
	Country string         // "US" (news region, °F)
	Loc     *time.Location // for match times
	// Locate finds an approximate place for "use my location" (desktop has no GPS); nil = ask for a city.
	Locate func(ctx context.Context) (Place, error)
	// Now, when set, gives the current conditions from Blaze Cloud's weather (the same numbers the
	// chat's get_weather gets). The card then shows those, with their source and time; the days
	// ahead still come from the forecast.
	Now func(ctx context.Context, lat, lon float64, fahrenheit bool) (Current, error)
}

// Current is one weather reading from Blaze Cloud.
type Current struct {
	Temp      float64
	Unit      string // "°C"
	Condition string // "Clear night"
	FeelsLike *float64
	Wind      float64
	WindUnit  string
	Source    string // "Open-Meteo"
	Observed  time.Time
}

// Result is a fetch's content plus config it learned (a city's coordinates, a team id) to save on the widget.
type Result struct {
	Data    Data
	Learned map[string]string
}

func (f Fetcher) get(ctx context.Context, u, what string, h map[string]string) (string, error) {
	code, body, err := f.Net(ctx, u, h)
	if err != nil {
		return "", err
	}
	if code < 200 || code > 299 {
		return "", SourceError{HTTPLine(code, what)}
	}
	return body, nil
}

// Plain turns any error into a card line.
func Plain(err error) string {
	var se SourceError
	if errors.As(err, &se) {
		if se.Msg == "" {
			return "Couldn't update this right now."
		}
		return se.Msg
	}
	var ae actionErr
	if errors.As(err, &ae) {
		return ae.Msg
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() || errors.Is(err, context.DeadlineExceeded) {
		return "The source took too long to answer. I'll try again later."
	}
	var de *net.DNSError
	var oe *net.OpError
	if errors.As(err, &de) || errors.As(err, &oe) {
		return "No connection right now. Showing what I had."
	}
	return "Couldn't update this right now."
}

// Fetch gets one web widget's content. It never panics; a failure is a Data with a plain line.
func (f Fetcher) Fetch(ctx context.Context, w Widget, now time.Time) (r Result) {
	defer func() {
		if p := recover(); p != nil {
			r = Result{Data: Failed("Couldn't update this right now.", "")}
		}
	}()
	var err error
	switch w.Type {
	case Weather:
		r, err = f.weather(ctx, w)
	case News:
		r.Data, err = f.news(ctx, w, now)
	case Prices:
		r, err = f.prices(ctx, w)
	case Sports:
		r, err = f.sports(ctx, w)
	case GitHub:
		r.Data, err = f.github(ctx, w, now)
	case Custom:
		r.Data = CustomData(w)
	case Into:
		r.Data = Data{Headline: "Tell me what you're into", Change: "Say a few topics, teams, coins or cities and I'll fill your board with them.", Action: "into"}
	default:
		r.Data = Failed("This card type isn't supported on the desktop.", "")
	}
	if err != nil {
		return Result{Data: Failed(Plain(err), actionFor(err))}
	}
	return r
}

// actionErr is a failure that asks the card for a button ("city").
type actionErr struct {
	Msg    string
	action string
}

func (e actionErr) Error() string { return e.Msg }

func actionFor(err error) string {
	var a actionErr
	if errors.As(err, &a) {
		return a.action
	}
	return ""
}

func (f Fetcher) weather(ctx context.Context, w Widget) (Result, error) {
	fahr := UsesFahrenheit(f.Country)
	if u := w.Opt("units"); u != "" {
		fahr = strings.HasPrefix(strings.ToLower(u), "f")
	}
	var learned map[string]string
	city := w.Opt("city")
	var lat, lon float64
	place := w.Opt("place")
	switch {
	case city != "" && w.Opt("geo_city") == city && w.Opt("lat") != "":
		lat, _ = strconv.ParseFloat(w.Opt("lat"), 64)
		lon, _ = strconv.ParseFloat(w.Opt("lon"), 64)
		if place == "" {
			place = city
		}
	case city != "":
		body, err := f.get(ctx, GeocodeURL(city, f.Lang), "that city", nil)
		if err != nil {
			return Result{}, err
		}
		p, ok := ParsePlace(body)
		if !ok {
			return Result{}, actionErr{"Couldn't find the city “" + city + "”. Try another spelling.", "city"}
		}
		lat, lon, place = p.Lat, p.Lon, p.Name
		learned = map[string]string{"lat": strconv.FormatFloat(lat, 'f', 4, 64), "lon": strconv.FormatFloat(lon, 'f', 4, 64), "place": p.Name, "geo_city": city}
	case w.Opt("use_location") == "true" && f.Locate != nil:
		p, err := f.Locate(ctx)
		if err != nil || p.Name == "" {
			return Result{}, actionErr{"Type a city to see the weather.", "city"}
		}
		lat, lon, place = p.Lat, p.Lon, p.Name
		if w.Opt("units") == "" && p.Country != "" {
			fahr = UsesFahrenheit(p.Country)
		}
	default:
		return Result{}, actionErr{"Type a city to see the weather.", "city"}
	}
	body, err := f.get(ctx, ForecastURL(lat, lon, fahr), "the weather", nil)
	if err != nil {
		return Result{}, err
	}
	d, err := ParseForecast(body, place)
	if err != nil {
		return Result{}, err
	}
	d.Link = "https://open-meteo.com/"
	if f.Now != nil {
		if c, err := f.Now(ctx, lat, lon, fahr); err == nil {
			d.Value = fmt.Sprintf("%d%s", round(c.Temp), firstRunes(c.Unit, 2))
			ch := c.Condition
			if c.FeelsLike != nil {
				ch += fmt.Sprintf(" · feels %d°", round(*c.FeelsLike))
			}
			if c.WindUnit != "" {
				ch += fmt.Sprintf(" · wind %d %s", round(c.Wind), c.WindUnit)
			}
			if c.Source != "" {
				ch += " · " + c.Source
				if !c.Observed.IsZero() {
					ch += " " + c.Observed.In(time.Local).Format("15:04")
				}
			}
			d.Change = ch
		}
	}
	return Result{Data: d, Learned: learned}, nil
}

func (f Fetcher) news(ctx context.Context, w Widget, now time.Time) (Data, error) {
	feed := w.Opt("feed")
	u := NewsURL(w.Opt("query"), f.Lang, f.Country)
	if feed != "" {
		u = feed
		if !strings.HasPrefix(u, "http") {
			u = "https://" + u
		}
	}
	xml, err := f.get(ctx, u, "that feed", nil)
	if err != nil {
		return Data{}, err
	}
	es := ParseFeed(xml, 20)
	if len(es) == 0 {
		return Data{}, SourceError{"No stories right now."}
	}
	// Google News has no pictures; Bing News' feed for the same query does (mobile v0.29.3).
	if feed == "" {
		q := w.Opt("query")
		if q == "" {
			q = "top stories"
		}
		if b, err := f.get(ctx, "https://www.bing.com/news/search?format=rss&q="+strings.ReplaceAll(q, " ", "+"), "pictures", nil); err == nil {
			es = BorrowImages(es, ParseFeed(b, 30))
		}
	}
	head := w.Title
	if feed != "" {
		if t := FeedTitle(xml); t != "" {
			head = t
		}
	}
	return FeedData(es, head, now.UnixMilli()), nil
}

func split(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' })
}

func (f Fetcher) prices(ctx context.Context, w Widget) (Result, error) {
	switch w.Opt("kind") {
	case "fx":
		base := w.Opt("base")
		if base == "" {
			base = "USD"
		}
		ts := split(w.Opt("symbols"))
		if len(ts) == 0 {
			ts = []string{"EUR"}
		}
		body, err := f.get(ctx, FxURL(base), "exchange rates", nil)
		if err != nil {
			return Result{}, err
		}
		d, err := ParseFx(body, base, ts)
		return Result{Data: d}, err
	case "stock":
		syms := split(w.Opt("symbols"))
		if len(syms) == 0 {
			return Result{}, SourceError{"Add a stock symbol, like AAPL."}
		}
		if len(syms) > 6 {
			syms = syms[:6]
		}
		var lines []Line
		for _, s := range syms {
			body, err := f.get(ctx, StockURL(s), "the stock "+s, nil)
			if err != nil {
				continue
			}
			if l, err := ParseStock(body, s); err == nil {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			return Result{}, SourceError{"Stock prices aren't available right now (the free source didn't answer)."}
		}
		top := lines[0]
		val, change, _ := strings.Cut(top.N, "  ")
		d := Data{Headline: top.T, Value: val, Change: change, Link: top.U}
		if len(lines) > 1 {
			d.Lines = lines
		}
		return Result{Data: d}, nil
	}
	vs := w.Opt("vs")
	if vs == "" {
		vs = "usd"
	}
	raw := split(strings.ReplaceAll(w.Opt("ids"), " ", ""))
	if len(raw) == 0 {
		raw = []string{w.Title}
	}
	if len(raw) > 6 {
		raw = raw[:6]
	}
	ids := make([]string, len(raw))
	for i, x := range raw {
		ids[i] = CoinID(x)
	}
	body, err := f.get(ctx, CoinPriceURL(ids, vs), "that coin", nil)
	if err != nil {
		return Result{}, err
	}
	d, err := ParseCoins(body, ids, vs)
	if err == nil {
		return Result{Data: d}, nil
	}
	// A name CoinGecko doesn't use as an id: look it up once and remember the id.
	for i, id := range ids {
		if b, e := f.get(ctx, CoinSearchURL(id), "that coin", nil); e == nil {
			if x := ParseCoinSearch(b); x != "" {
				ids[i] = x
			}
		}
	}
	body, err = f.get(ctx, CoinPriceURL(ids, vs), "that coin", nil)
	if err != nil {
		return Result{}, err
	}
	d, err = ParseCoins(body, ids, vs)
	return Result{Data: d, Learned: map[string]string{"ids": strings.Join(ids, ",")}}, err
}

func (f Fetcher) sports(ctx context.Context, w Widget) (Result, error) {
	q := w.Opt("team")
	if q == "" {
		q = w.Title
	}
	var learned map[string]string
	var t Team
	if w.Opt("team_id") != "" && w.Opt("team_query") == q {
		t = Team{w.Opt("team_id"), w.Opt("team_name"), w.Opt("league"), w.Opt("sport")}
		if t.Name == "" {
			t.Name = q
		}
	} else {
		body, err := f.get(ctx, TeamSearchURL(q), "that team", nil)
		if err != nil {
			return Result{}, err
		}
		var ok bool
		if t, ok = ParseTeam(body, q); !ok {
			return Result{}, SourceError{"Couldn't find the team “" + q + "”. Try its full name."}
		}
		learned = map[string]string{"team_id": t.ID, "team_name": t.Name, "team_query": q, "league": t.League, "sport": t.Sport}
	}
	var last, next []Event
	if b, err := f.get(ctx, TeamLastURL(t.ID), "results", nil); err == nil {
		last = ParseEvents(b, "results")
	}
	if b, err := f.get(ctx, TeamNextURL(t.ID), "fixtures", nil); err == nil {
		next = ParseEvents(b, "events")
	}
	return Result{Data: SportsData(t, last, next, f.Loc), Learned: learned}, nil
}

func (f Fetcher) github(ctx context.Context, w Widget, now time.Time) (Data, error) {
	repo := Repo(w.Opt("repo"))
	if repo == "" {
		return Data{}, SourceError{"Add a repo like owner/name."}
	}
	code, body, err := f.Net(ctx, RepoEventsURL(repo), map[string]string{"Accept": "application/vnd.github+json"})
	if err != nil {
		return Data{}, err
	}
	switch {
	case code == 403 || code == 429:
		return Data{}, SourceError{"GitHub's free limit is used up for now. I'll try again later."}
	case code == 404:
		return Data{}, SourceError{"Couldn't find the repo " + repo + " (it may be private)."}
	case code < 200 || code > 299:
		return Data{}, SourceError{HTTPLine(code, "the repo")}
	}
	return ParseRepoEvents(body, repo, now.UnixMilli())
}

// IPLocateURL is a free, keyless IP geolocation used for "use my location" on the desktop (city level).
const IPLocateURL = "https://ipwho.is/?fields=success,city,country_code,latitude,longitude"

// ParseIPLocate reads ipwho.is.
func ParseIPLocate(js string) (Place, bool) {
	var o struct {
		Success bool    `json:"success"`
		City    string  `json:"city"`
		CC      string  `json:"country_code"`
		Lat     float64 `json:"latitude"`
		Lon     float64 `json:"longitude"`
	}
	if jsonUnmarshal(js, &o) != nil || !o.Success || o.City == "" {
		return Place{}, false
	}
	return Place{Name: o.City, Lat: o.Lat, Lon: o.Lon, Country: o.CC}, true
}
