// Package flashcards is the spaced-repetition deck behind the tutor skill and the
// Learn view (v0.15+). Cards live in ~/.blaze/flashcards.json, shared by every Blaze
// on this computer. Scheduling is a small SM-2: grade 0 (again) .. 3 (easy).
package flashcards

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

// Card is one flashcard.
type Card struct {
	ID       string    `json:"id"`
	Deck     string    `json:"deck"`
	Front    string    `json:"front"`
	Back     string    `json:"back"`
	Source   string    `json:"source,omitempty"` // the PDF, slides or notes it came from
	Ease     float64   `json:"ease"`             // SM-2 ease factor (starts 2.5)
	Interval int       `json:"interval"`         // days
	Reps     int       `json:"reps"`             // successful reviews in a row
	Due      time.Time `json:"due"`
	Added    time.Time `json:"added"`
	Lapses   int       `json:"lapses,omitempty"`
}

// Deck is a deck's summary for the Learn view.
type Deck struct {
	Name  string `json:"name"`
	Cards int    `json:"cards"`
	Due   int    `json:"due"`
	New   int    `json:"new"`
}

var mu sync.Mutex

// Now is the clock (tests move it).
var Now = time.Now

func file() string { return filepath.Join(config.Dir(), "flashcards.json") }

func load() []Card {
	b, err := os.ReadFile(file())
	if err != nil {
		return nil
	}
	var cs []Card
	_ = json.Unmarshal(b, &cs)
	return cs
}

func save(cs []Card) error {
	if err := os.MkdirAll(filepath.Dir(file()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(cs, "", "  ")
	tmp := file() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, file())
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func clean(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		s = s[:n]
	}
	return s
}

// Add puts new cards in deck (due now). Exact duplicates (same deck and front) are skipped.
// Returns how many were added.
func Add(deck, source string, cards [][2]string) (int, error) {
	deck = clean(deck, 80)
	if deck == "" {
		deck = "General"
	}
	mu.Lock()
	defer mu.Unlock()
	cs := load()
	have := map[string]bool{}
	for _, c := range cs {
		have[strings.ToLower(c.Deck+"\x00"+c.Front)] = true
	}
	now := Now()
	n := 0
	for _, fb := range cards {
		f, b := clean(fb[0], 600), clean(fb[1], 2000)
		if f == "" || b == "" || have[strings.ToLower(deck+"\x00"+f)] {
			continue
		}
		have[strings.ToLower(deck+"\x00"+f)] = true
		cs = append(cs, Card{ID: newID(), Deck: deck, Front: f, Back: b, Source: clean(source, 200), Ease: 2.5, Due: now, Added: now})
		n++
	}
	if n == 0 {
		return 0, nil
	}
	return n, save(cs)
}

// Decks lists every deck with its due and new counts, most due first.
func Decks() []Deck {
	mu.Lock()
	defer mu.Unlock()
	now := Now()
	by := map[string]*Deck{}
	for _, c := range load() {
		d := by[c.Deck]
		if d == nil {
			d = &Deck{Name: c.Deck}
			by[c.Deck] = d
		}
		d.Cards++
		if !c.Due.After(now) {
			d.Due++
		}
		if c.Reps == 0 && c.Lapses == 0 && c.Interval == 0 {
			d.New++
		}
	}
	out := []Deck{}
	for _, d := range by {
		out = append(out, *d)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Due != out[j].Due {
			return out[i].Due > out[j].Due
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// Due returns up to limit cards due now in deck ("" = every deck), oldest due first.
func Due(deck string, limit int) []Card {
	mu.Lock()
	defer mu.Unlock()
	now := Now()
	out := []Card{}
	for _, c := range load() {
		if (deck == "" || c.Deck == deck) && !c.Due.After(now) {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Due.Before(out[j].Due) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Review grades a card: 0 again, 1 hard, 2 good, 3 easy (SM-2). Returns the card as rescheduled.
func Review(id string, grade int) (Card, error) {
	if grade < 0 || grade > 3 {
		return Card{}, errors.New("grade is 0 (again), 1 (hard), 2 (good) or 3 (easy)")
	}
	mu.Lock()
	defer mu.Unlock()
	cs := load()
	for i := range cs {
		if cs[i].ID != id {
			continue
		}
		c := &cs[i]
		q := []float64{1, 3, 4, 5}[grade] // SM-2 quality
		if grade == 0 {
			c.Reps, c.Interval = 0, 0
			c.Lapses++
			c.Due = Now().Add(10 * time.Minute)
		} else {
			switch c.Reps {
			case 0:
				c.Interval = 1
			case 1:
				c.Interval = 3
			default:
				c.Interval = int(float64(c.Interval)*c.Ease + 0.5)
			}
			if grade == 1 {
				c.Interval = max(1, c.Interval*2/3)
			}
			if grade == 3 {
				c.Interval = c.Interval*13/10 + 1
			}
			c.Reps++
			c.Due = Now().AddDate(0, 0, c.Interval)
		}
		c.Ease += 0.1 - (5-q)*(0.08+(5-q)*0.02)
		if c.Ease < 1.3 {
			c.Ease = 1.3
		}
		out := *c
		return out, save(cs)
	}
	return Card{}, errors.New("no such card")
}

// Delete removes a card.
func Delete(id string) error {
	mu.Lock()
	defer mu.Unlock()
	cs := load()
	out := cs[:0]
	for _, c := range cs {
		if c.ID != id {
			out = append(out, c)
		}
	}
	return save(out)
}
