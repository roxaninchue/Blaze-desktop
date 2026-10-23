package home

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// Store keeps the board in one JSON file (~/.blaze/desktop/home.json): widgets, their last content and a
// few board keys. Writes are atomic (temp file + rename). Safe for concurrent use.
type Store struct {
	path string
	mu   sync.Mutex
	b    board
}

type board struct {
	Version int             `json:"version"`
	Seeded  bool            `json:"seeded"`
	Widgets []Widget        `json:"widgets"`
	Data    map[string]Data `json:"data"`
	Removed []string        `json:"removed_categories,omitempty"`
	Last    int64           `json:"last_refresh,omitempty"`
}

// Open reads the board at path (a missing file is an empty board; a broken one is kept aside).
func Open(path string) *Store {
	s := &Store{path: path, b: board{Version: 1, Data: map[string]Data{}}}
	if raw, err := os.ReadFile(path); err == nil {
		var b board
		if json.Unmarshal(raw, &b) == nil {
			if b.Data == nil {
				b.Data = map[string]Data{}
			}
			s.b = b
		} else {
			_ = os.Rename(path, path+".broken")
		}
	}
	return s
}

func (s *Store) save() error {
	_ = os.MkdirAll(filepath.Dir(s.path), 0o700)
	raw, err := json.MarshalIndent(s.b, "", " ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Snapshot is the board for the UI.
type Snapshot struct {
	Widgets []Widget        `json:"widgets"`
	Data    map[string]Data `json:"data"`
	Stacks  []Stack         `json:"stacks"`
	Seeded  bool            `json:"seeded"`
	Last    int64           `json:"last_refresh"`
}

func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.sorted()
	d := make(map[string]Data, len(s.b.Data))
	for k, v := range s.b.Data {
		d[k] = v
	}
	return Snapshot{Widgets: ws, Data: d, Stacks: Stacks(ws), Seeded: s.b.Seeded, Last: s.b.Last}
}

func (s *Store) sorted() []Widget {
	ws := append([]Widget(nil), s.b.Widgets...)
	sort.SliceStable(ws, func(i, j int) bool { return ws[i].Order < ws[j].Order })
	return ws
}

// All is every widget in board order.
func (s *Store) All() []Widget {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sorted()
}

// Get is one widget.
func (s *Store) Get(id string) (Widget, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, w := range s.b.Widgets {
		if w.ID == id {
			return w, true
		}
	}
	return Widget{}, false
}

// DataOf is a widget's last content.
func (s *Store) DataOf(id string) (Data, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.b.Data[id]
	return d, ok
}

// idSeq keeps ids made in the same millisecond apart (a random suffix alone collided now and
// then when several widgets were added at once, e.g. seeding the starters).
var idSeq atomic.Uint32

func newID(now time.Time) string {
	return "w" + strconv.FormatInt(now.UnixMilli(), 36) + fmt.Sprintf("%03d", rand.Intn(1000)) + strconv.FormatUint(uint64(idSeq.Add(1)), 36)
}

func sameCfg(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// Add puts a widget at the end of the board, or returns the existing identical one (shown again if hidden).
func (s *Store) Add(w Widget) Widget {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, o := range s.b.Widgets {
		if o.Type == w.Type && sameCfg(o.Config, w.Config) {
			if o.Hidden {
				s.b.Widgets[i].Hidden = false
				_ = s.save()
			}
			return s.b.Widgets[i]
		}
	}
	now := time.Now()
	w.ID = newID(now)
	w.Created = now.UnixMilli()
	w.Size = NormSize(w.Size)
	if w.Category == "" {
		w.Category = DefaultCategory(w.Type)
	}
	max := -1
	for _, o := range s.b.Widgets {
		if o.Order > max {
			max = o.Order
		}
	}
	w.Order = max + 1
	s.b.Widgets = append(s.b.Widgets, w)
	_ = s.save()
	return w
}

// Seed adds specs to an empty board once and marks it seeded.
func (s *Store) Seed(specs []Spec) bool {
	s.mu.Lock()
	if s.b.Seeded || len(s.b.Widgets) > 0 {
		s.b.Seeded = true
		_ = s.save()
		s.mu.Unlock()
		return false
	}
	s.b.Seeded = true
	s.mu.Unlock()
	for _, sp := range specs {
		s.Add(sp.Widget())
	}
	return true
}

// Remove deletes a widget and its content, remembering its category (suggestions skip it).
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, w := range s.b.Widgets {
		if w.ID == id {
			if w.Category != "" {
				s.b.Removed = append(s.b.Removed, w.Category)
			}
			s.b.Widgets = append(s.b.Widgets[:i], s.b.Widgets[i+1:]...)
			delete(s.b.Data, id)
			_ = s.save()
			return
		}
	}
}

// Update changes a widget in place (id kept).
func (s *Store) Update(w Widget) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, o := range s.b.Widgets {
		if o.ID == w.ID {
			w.Size = NormSize(w.Size)
			s.b.Widgets[i] = w
			_ = s.save()
			return
		}
	}
}

// Move shifts a widget delta places in board order. False when it can't move.
func (s *Store) Move(id string, delta int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	ws := s.sorted()
	from := -1
	for i, w := range ws {
		if w.ID == id {
			from = i
		}
	}
	to := from + delta
	if from < 0 || to < 0 || to >= len(ws) || delta == 0 {
		return false
	}
	w := ws[from]
	ws = append(ws[:from], ws[from+1:]...)
	ws = append(ws[:to], append([]Widget{w}, ws[to:]...)...)
	s.renumber(ws)
	return true
}

// Reorder sets board order from ids (unknown ids ignored; missing ones keep their place after).
func (s *Store) Reorder(ids []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	ws := s.sorted()
	sort.SliceStable(ws, func(i, j int) bool {
		pi, oki := pos[ws[i].ID]
		pj, okj := pos[ws[j].ID]
		if oki && okj {
			return pi < pj
		}
		return oki && !okj
	})
	s.renumber(ws)
}

func (s *Store) renumber(ws []Widget) {
	for i := range ws {
		ws[i].Order = i
	}
	s.b.Widgets = ws
	_ = s.save()
}

// SaveData stores a fetch result. A failed fetch keeps the last good content and its time, adding the error.
func (s *Store) SaveData(id string, d Data, now time.Time) Data {
	s.mu.Lock()
	defer s.mu.Unlock()
	if d.Error != "" && d.Empty() {
		if old, ok := s.b.Data[id]; ok && !old.Empty() {
			old.Error = d.Error
			if d.Action != "" {
				old.Action = d.Action
			}
			d = old
		}
	} else {
		d.Fetched = now.UnixMilli()
	}
	s.b.Data[id] = d
	if d.Fetched > s.b.Last {
		s.b.Last = d.Fetched
	}
	_ = s.save()
	return d
}

// RemovedCategories are the stacks the user removed widgets from.
func (s *Store) RemovedCategories() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.b.Removed...)
}
