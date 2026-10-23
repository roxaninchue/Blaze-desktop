// Package home is the desktop port of Blaze Mobile's Home board (v0.29+): widgets (a definition: what to
// show and how big) and their last fetched content, the free keyless web sources (Open-Meteo, Google News
// RSS, CoinGecko, open.er-api.com, Yahoo's public chart, TheSportsDB, GitHub's public API), the
// plain-words widget parser, category stacks and the greeting. Everything here is pure Go with an
// injectable network so it is unit tested; the desktop-only cards (schedule, deadlines, recents) are built
// from data the caller passes in (home_app.go reads sched, mind and prefs).
//
// Storage is one new file, ~/.blaze/desktop/home.json (store.go). Nothing existing changes.
package home

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// Widget types. Mobile's phone-only types (apps = Usage access, calendar = the phone's calendar provider)
// are not ported; schedule (Blaze's timed jobs and reminders) and recents are desktop-only.
const (
	Weather   = "weather"
	News      = "news"
	Sports    = "sports"
	Prices    = "prices"
	GitHub    = "github"
	Schedule  = "schedule" // mobile: routines
	Deadlines = "deadlines"
	Recents   = "recents"
	Custom    = "custom"
	Into      = "into" // "tell me what you're into" starter card
)

var allTypes = []string{Weather, News, Sports, Prices, GitHub, Schedule, Deadlines, Recents, Custom, Into}

// Widget is one card on the board.
type Widget struct {
	ID       string            `json:"id"`
	Type     string            `json:"type"`
	Title    string            `json:"title"`
	Category string            `json:"category"`
	Config   map[string]string `json:"config,omitempty"`
	Size     string            `json:"size"` // S, M, L
	Order    int               `json:"order"`
	Hidden   bool              `json:"hidden,omitempty"`
	Created  int64             `json:"created"`
	Taps     int               `json:"taps,omitempty"`
}

// Opt is a trimmed config value.
func (w Widget) Opt(k string) string { return strings.TrimSpace(w.Config[k]) }

// Line is one row of a card: a title, an optional second line, link, right-hand note and picture.
type Line struct {
	T string `json:"t"`
	S string `json:"s,omitempty"`
	U string `json:"u,omitempty"`
	N string `json:"n,omitempty"`
	I string `json:"i,omitempty"`
}

// Data is a widget's content. Error is a plain sentence-case line (never a stack trace); Fetched is unix
// ms of the last successful fetch (0 = never); Action asks the card for a button ("city", "ask:<prompt>").
type Data struct {
	Headline string `json:"headline,omitempty"`
	Value    string `json:"value,omitempty"`
	Change   string `json:"change,omitempty"`
	Lines    []Line `json:"lines,omitempty"`
	Fetched  int64  `json:"fetched,omitempty"`
	Error    string `json:"error,omitempty"`
	Action   string `json:"action,omitempty"`
	Link     string `json:"link,omitempty"`
}

// Empty is true when there is nothing to show but (maybe) an error.
func (d Data) Empty() bool { return d.Headline == "" && d.Value == "" && len(d.Lines) == 0 }

// Failed is a card that only says what went wrong.
func Failed(msg, action string) Data { return Data{Error: msg, Action: action} }

// NormType maps a free word onto a widget type ("" = unknown).
func NormType(t string) string {
	l := strings.ReplaceAll(strings.ToLower(strings.TrimSpace(t)), " ", "_")
	for _, x := range allTypes {
		if l == x {
			return x
		}
	}
	switch l {
	case "forecast":
		return Weather
	case "rss", "feed", "headlines", "topic":
		return News
	case "sport", "scores", "team", "football", "soccer":
		return Sports
	case "price", "crypto", "fx", "currency", "stock", "stocks":
		return Prices
	case "routines", "routine", "reminders", "reminder", "jobs":
		return Schedule
	case "deadline", "exams", "homework":
		return Deadlines
	case "repo", "git":
		return GitHub
	case "recent", "continue":
		return Recents
	case "text", "list", "link", "note":
		return Custom
	}
	return ""
}

// DefaultCategory is the stack a type sits in.
func DefaultCategory(t string) string {
	switch t {
	case Weather:
		return "Weather"
	case News:
		return "News"
	case Sports:
		return "Sports"
	case Prices:
		return "Finance"
	case Schedule, Deadlines:
		return "Today"
	case GitHub:
		return "Tech"
	case Recents:
		return "Pick up"
	case Into:
		return "For you"
	}
	return "Notes"
}

// Label names a type for people.
func Label(t string) string {
	switch t {
	case Weather:
		return "Weather"
	case News:
		return "News"
	case Sports:
		return "Sports"
	case Prices:
		return "Prices"
	case GitHub:
		return "GitHub"
	case Schedule:
		return "Schedule"
	case Deadlines:
		return "Deadlines"
	case Recents:
		return "Pick up where you left off"
	case Into:
		return "For you"
	}
	return "Custom"
}

// Online says whether a type needs the network (others are read on this computer).
func Online(t string) bool {
	switch t {
	case Weather, News, Sports, Prices, GitHub:
		return true
	}
	return false
}

// NormSize is S, M or L.
func NormSize(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "S", "SMALL":
		return "S"
	case "L", "LARGE", "BIG":
		return "L"
	}
	return "M"
}

// LinesFor is how many lines a card of this size shows.
func LinesFor(size string) int {
	switch NormSize(size) {
	case "S":
		return 0
	case "L":
		return 6
	}
	return 3
}

// Ago is "Updated 12 min ago", "Updated just now", "Updated 3 h ago", "Updated yesterday".
func Ago(then, now int64) string {
	if then <= 0 {
		return "Not updated yet"
	}
	m := (now - then) / 60000
	if m < 0 {
		m = 0
	}
	switch {
	case m < 1:
		return "Updated just now"
	case m < 60:
		return fmt.Sprintf("Updated %d min ago", m)
	case m < 24*60:
		return fmt.Sprintf("Updated %d h ago", m/60)
	case m < 48*60:
		return "Updated yesterday"
	}
	return fmt.Sprintf("Updated %d days ago", m/(24*60))
}

// FreshMs: content younger than this isn't fetched again when the board opens.
const FreshMs = 50 * 60 * 1000

// StaleMs: older content that couldn't be refreshed shows a quiet "Stale" tag.
const StaleMs = 3 * 60 * 60 * 1000

// Due says whether a widget needs fetching.
func Due(fetched, now, maxAge int64) bool { return fetched <= 0 || now-fetched >= maxAge }

// Stale says whether a card shows the stale tag.
func Stale(fetched, now int64, err string) bool {
	return fetched > 0 && (err != "" || now-fetched > StaleMs)
}

// Stack is the widgets of one category: Main on top, the rest peeking underneath.
type Stack struct {
	Key      string   `json:"key"`
	Category string   `json:"category"`
	Widgets  []Widget `json:"widgets"`
}

// Stacks groups the visible widgets by category (case-insensitive). A stack sits where its first widget
// is on the board and keeps board order. A blank category never stacks.
func Stacks(ws []Widget) []Stack {
	vis := make([]Widget, 0, len(ws))
	for _, w := range ws {
		if !w.Hidden {
			vis = append(vis, w)
		}
	}
	sort.SliceStable(vis, func(i, j int) bool { return vis[i].Order < vis[j].Order })
	var out []Stack
	at := map[string]int{}
	for _, w := range vis {
		k := strings.ToLower(strings.TrimSpace(w.Category))
		if k == "" {
			k = "#" + w.ID
		}
		if i, ok := at[k]; ok {
			out[i].Widgets = append(out[i].Widgets, w)
			continue
		}
		at[k] = len(out)
		out = append(out, Stack{Key: "s:" + k, Category: w.Category, Widgets: []Widget{w}})
	}
	return out
}

// Greeting is "Good morning" (5–11), "Good afternoon" (12–16) or "Good evening", like mobile.
func Greeting(t time.Time) string {
	h := t.Hour()
	switch {
	case h >= 5 && h <= 11:
		return "Good morning"
	case h >= 12 && h <= 16:
		return "Good afternoon"
	}
	return "Good evening"
}
