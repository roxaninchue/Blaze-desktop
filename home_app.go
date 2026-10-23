package main

// v0.19 Home tab: Blaze Mobile's Home board on the desktop (internal/home). The web cards (weather,
// news, prices, sports, GitHub) use free keyless sources; Schedule, Deadlines and Pick-up cards are read on
// this computer (sched, mind, the main chat, projects, recent folders). The board lives in a new file,
// ~/.blaze/desktop/home.json.

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/home"
	"blaze-desktop/internal/mind"
	"blaze-desktop/internal/sched"
)

var (
	homeOnce  sync.Once
	homeStore *home.Store
	homeNet   = home.HTTPNet("")
	homePlace struct {
		sync.Mutex
		p  home.Place
		at time.Time
	}
)

func homeBoard() *home.Store {
	homeOnce.Do(func() { homeStore = home.Open(filepath.Join(config.Dir(), "desktop", "home.json")) })
	return homeStore
}

func memoryTexts() []string {
	d := mindDB()
	if d == nil {
		return nil
	}
	var out []string
	for _, m := range d.All() {
		out = append(out, m.Text)
	}
	return out
}

// HomeView is the board plus the greeting line.
type HomeView struct {
	home.Snapshot
	Greeting string `json:"greeting"`
	Name     string `json:"name"`
	Date     string `json:"date"`
}

// HomeBoard returns the board, seeding it on first open: from memory when Blaze knows enough, else the
// generic starters (weather, top stories, schedule, pick up, "tell me what you're into").
func (a *App) HomeBoard() HomeView {
	s := homeBoard()
	mems := memoryTexts()
	if !s.Snapshot().Seeded {
		specs := home.FromMemories(mems)
		if specs != nil {
			specs = append(specs, home.Spec{Type: home.Recents, Title: home.Label(home.Recents), Category: "Pick up"})
		} else {
			specs = home.Starters()
		}
		s.Seed(specs)
	}
	now := time.Now()
	return HomeView{Snapshot: s.Snapshot(), Greeting: home.Greeting(now), Name: home.NameFrom(mems), Date: now.Format("Monday 2 January")}
}

// splitLocale: "en-ZM" / "en_US.UTF-8" → ("en", "ZM").
func splitLocale(l string) (string, string) {
	l = strings.SplitN(strings.SplitN(l, ".", 2)[0], "@", 2)[0]
	p := strings.FieldsFunc(l, func(r rune) bool { return r == '-' || r == '_' })
	lang, cc := "en", ""
	if len(p) > 0 && len(p[0]) == 2 {
		lang = strings.ToLower(p[0])
	}
	if len(p) > 1 && len(p[len(p)-1]) == 2 {
		cc = strings.ToUpper(p[len(p)-1])
	}
	return lang, cc
}

var errNoPlace = errors.New("no place")

func homeLocate(ctx context.Context) (home.Place, error) {
	homePlace.Lock()
	defer homePlace.Unlock()
	if homePlace.p.Name != "" && time.Since(homePlace.at) < 6*time.Hour {
		return homePlace.p, nil
	}
	code, body, err := homeNet(ctx, home.IPLocateURL, nil)
	if err != nil || code != 200 {
		return home.Place{}, errNoPlace
	}
	p, ok := home.ParseIPLocate(body)
	if !ok {
		return home.Place{}, errNoPlace
	}
	homePlace.p, homePlace.at = p, time.Now()
	return p, nil
}

func homeJobs() []home.Job {
	var out []home.Job
	for _, j := range sched.Load() {
		out = append(out, home.Job{Name: j.Name, When: j.When, Prompt: j.Prompt, Command: j.Command})
	}
	return out
}

func (a *App) homeRecents() []home.Recent {
	var out []home.Recent
	h := a.mainHist()
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role == "user" && !strings.HasPrefix(h[i].Content, "[Background agent result") {
			out = append(out, home.Recent{Title: promptTitle(h[i : i+1]), Sub: "Your last message", Kind: "chat", Target: mainConv})
			break
		}
	}
	for i, p := range a.Projects() {
		if i == 3 {
			break
		}
		out = append(out, home.Recent{Title: p.Name, Sub: baseName(p.Folder), Kind: "project", Target: p.ID})
	}
	for i, f := range a.prefs.Recent {
		if i == 2 {
			break
		}
		out = append(out, home.Recent{Title: baseName(f), Sub: f, Kind: "folder", Target: f})
	}
	return out
}

func baseName(p string) string {
	p = strings.TrimRight(p, `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// HomeRefresh fetches one card now and returns its content (locale is the UI's, e.g. "en-ZM").
func (a *App) HomeRefresh(id, locale string) home.Data {
	s := homeBoard()
	w, ok := s.Get(id)
	if !ok {
		return home.Failed("This card was removed.", "")
	}
	now := time.Now()
	var d home.Data
	switch w.Type {
	case home.Schedule:
		d = home.ScheduleData(homeJobs(), now)
	case home.Deadlines:
		d = home.DeadlinesData(homeJobs(), memoryTexts(), now)
	case home.Recents:
		d = home.RecentsData(a.homeRecents())
	default:
		lang, cc := splitLocale(locale)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		f := home.Fetcher{Net: homeNet, Lang: lang, Country: cc, Loc: time.Local, Locate: homeLocate, Now: homeWeatherNow}
		r := f.Fetch(ctx, w, now)
		if len(r.Learned) > 0 {
			if w.Config == nil {
				w.Config = map[string]string{}
			}
			for k, v := range r.Learned {
				w.Config[k] = v
			}
			s.Update(w)
		}
		d = r.Data
	}
	return s.SaveData(id, d, now)
}

// HomeAdd adds a card asked for in plain words ("Arsenal scores", "weather in Lusaka", "bitcoin price").
// Anything it doesn't recognise becomes a news card on that topic, as mobile's interest picker does.
func (a *App) HomeAdd(text string) home.Widget {
	sp, ok := home.Parse(text)
	if !ok {
		if strings.TrimSpace(text) == "" {
			return home.Widget{}
		}
		sp = home.NewsSpec(strings.TrimSpace(text))
	}
	return homeBoard().Add(sp.Widget())
}

// HomeInto takes "what you're into" (comma separated), adds a card for each, removes the starter card
// and remembers the interests (like mobile's interest picker).
func (a *App) HomeInto(text string) int {
	s := homeBoard()
	var parts []string
	for _, p := range strings.FieldsFunc(text, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if p = strings.TrimSpace(p); len(p) >= 2 {
			parts = append(parts, p)
			a.HomeAdd(p)
		}
	}
	if len(parts) == 0 {
		return 0
	}
	for _, w := range s.All() {
		if w.Type == home.Into {
			s.Remove(w.ID)
		}
	}
	if d := mindDB(); d != nil {
		_, _ = d.Save(mind.Memory{Kind: "preference", Subject: "Interests", Text: "Interested in " + strings.Join(parts, "; ") + "."})
	}
	return len(parts)
}

func (a *App) HomeRemove(id string)               { homeBoard().Remove(id) }
func (a *App) HomeMove(id string, delta int) bool { return homeBoard().Move(id, delta) }
func (a *App) HomeReorder(ids []string)           { homeBoard().Reorder(ids) }

// HomeResize sets S, M or L.
func (a *App) HomeResize(id, size string) {
	s := homeBoard()
	if w, ok := s.Get(id); ok {
		w.Size = size
		s.Update(w)
	}
}

// HomeSetCity points a weather card at a city (forgetting the old coordinates).
func (a *App) HomeSetCity(id, city string) {
	s := homeBoard()
	w, ok := s.Get(id)
	city = strings.TrimSpace(city)
	if !ok || city == "" {
		return
	}
	w.Config = map[string]string{"city": city}
	w.Title = "Weather in " + city
	s.Update(w)
}
