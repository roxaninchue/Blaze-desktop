package cloudsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Entry is what the last good round left behind for one item: the server rev and the local
// stamp then (SyncEngine.Entry).
type Entry struct {
	Rev     int64  `json:"r"`
	Stamp   string `json:"s"`
	T       int64  `json:"t,omitempty"`
	Deleted bool   `json:"d,omitempty"`
	Skipped bool   `json:"x,omitempty"` // too large / over quota: not resent until it changes
}

// State is the sync state of one device: the account copy's epoch, the pull cursor (the server
// seq of the last item seen) and one Entry per item. Same JSON shape as the phone's
// cloudsync/state.json (JsonSyncState).
type State struct {
	mu     sync.Mutex
	path   string
	Epoch  string           `json:"epoch"`
	Cursor int64            `json:"cursor"`
	Items  map[string]Entry `json:"items"`
}

func skey(kind, id string) string { return kind + "\x00" + id }

// NewState is an empty in-memory state (path "" = Save does nothing; tests).
func NewState() *State { return &State{Items: map[string]Entry{}} }

// LoadState reads the state file at path (a missing or broken file is a fresh start, as on the
// phone). Desktop and CLI share one file: ~/.blaze/cloudsync/state.json.
func LoadState(path string) *State {
	s := &State{path: path, Items: map[string]Entry{}}
	b, err := os.ReadFile(path)
	if err != nil {
		return s
	}
	var f State
	if json.Unmarshal(b, &f) != nil {
		return s
	}
	s.Epoch, s.Cursor = f.Epoch, f.Cursor
	if f.Items != nil {
		s.Items = f.Items
	}
	return s
}

func (s *State) Get(kind, id string) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.Items[skey(kind, id)]
	return e, ok
}

func (s *State) Put(kind, id string, e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Items[skey(kind, id)] = e
}

func (s *State) Remove(kind, id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Items, skey(kind, id))
}

// IDs are the item ids of one kind the state knows, sorted.
func (s *State) IDs(kind string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	p := kind + "\x00"
	for k := range s.Items {
		if strings.HasPrefix(k, p) {
			out = append(out, k[len(p):])
		}
	}
	sort.Strings(out)
	return out
}

func (s *State) ClearItems() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Items = map[string]Entry{}
}

// ForgetKind drops one kind (switched off, so switching it on again starts clean).
func (s *State) ForgetKind(kind string) {
	for _, id := range s.IDs(kind) {
		s.Remove(kind, id)
	}
}

// RetrySkipped gives items skipped for size or quota another try (Sync now).
func (s *State) RetrySkipped() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, e := range s.Items {
		if e.Skipped {
			e.Stamp, e.Skipped = "", false
			s.Items[k] = e
		}
	}
}

// Save writes the state (tmp + rename) when it has a path.
func (s *State) Save() error {
	s.mu.Lock()
	b, err := json.Marshal(s)
	p := s.path
	s.mu.Unlock()
	if err != nil || p == "" {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
