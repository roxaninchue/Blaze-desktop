package bgrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IDPrefix marks the desktop's own runs: GET /v1/runs lists every run of the account (the phone's
// too), and the desktop only ever picks up its own.
const IDPrefix = "desk-"

// NewClientMsgID is a fresh client_msg_id (4-128 of [A-Za-z0-9._:-]).
func NewClientMsgID() string {
	b := make([]byte, 10)
	_, _ = rand.Read(b)
	return IDPrefix + time.Now().UTC().Format("20060102T150405") + "-" + hex.EncodeToString(b)
}

// Entry is a run the desktop started and hasn't shown the final of yet.
type Entry struct {
	RunID       string `json:"run_id"`
	ClientMsgID string `json:"client_msg_id"`
	Conv        string `json:"conv"`             // the chat it belongs to
	Action      bool   `json:"action,omitempty"` // it had an ack
	Intent      string `json:"intent,omitempty"` // the ack's intent (reminder, send, search, …)
	// LocalIntent: the gateway sent no ack, so the app's own read of the request (an action) gave
	// the ack and the intent; it outranks the receipt's intent when the final is shown.
	LocalIntent bool `json:"local_intent,omitempty"`
	// Ask is the user's request (clipped), so a specialty pill can say what the help is for when
	// its event carries no detail.
	Ask     string    `json:"ask,omitempty"`
	Created time.Time `json:"created"`
}

// Store keeps the unfinished runs on disk, so a final that lands while Blaze is closed is
// shown in the right chat on the next start.
type Store struct {
	Path string
	mu   sync.Mutex
}

func (s *Store) load() []Entry {
	var out []Entry
	if s.Path == "" {
		return nil
	}
	b, err := os.ReadFile(s.Path)
	if err != nil {
		return nil
	}
	_ = json.Unmarshal(b, &out)
	return out
}

func (s *Store) save(es []Entry) {
	if s.Path == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(s.Path), 0o700)
	b, _ := json.Marshal(es)
	tmp := s.Path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, s.Path)
	}
}

// Add records e (replacing an entry with the same run id).
func (s *Store) Add(e Entry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es := s.load()
	out := es[:0]
	for _, x := range es {
		if x.RunID != e.RunID {
			out = append(out, x)
		}
	}
	s.save(append(out, e))
}

// Remove forgets run id.
func (s *Store) Remove(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	es := s.load()
	out := es[:0]
	for _, x := range es {
		if x.RunID != id {
			out = append(out, x)
		}
	}
	s.save(out)
}

// All is every unfinished run.
func (s *Store) All() []Entry {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Notifier sends at most ONE desktop notification per final, and only when the window isn't
// focused. No "working" notices ever go through it.
type Notifier struct {
	Focused func() bool             // the window is on screen and focused
	Send    func(chat, text string) // shows one desktop notification (chat: the chat it opens)
	mu      sync.Mutex
	sent    map[string]bool
}

// Land is called once a final is in the chat. It returns true when it notified.
func (n *Notifier) Land(key, chat string, f Final) bool {
	if n == nil || n.Send == nil {
		return false
	}
	if n.Focused != nil && n.Focused() {
		return false
	}
	n.mu.Lock()
	if n.sent == nil {
		n.sent = map[string]bool{}
	}
	if key != "" && n.sent[key] {
		n.mu.Unlock()
		return false
	}
	if key != "" {
		n.sent[key] = true
	}
	n.mu.Unlock()
	n.Send(chat, f.Notification())
	return true
}

// Manager follows runs in the background and delivers each final exactly once.
type Manager struct {
	API      API
	Store    *Store
	Follower func(e Entry) *Follower // a follower for e (its device tools run in e.Conv's chat)
	// Deliver puts the final in its chat (and saves it). Called once per run.
	Deliver  func(e Entry, f Final)
	Notifier *Notifier
	// OnLost is told when a follow gave up on the network (the run stays for the next resume).
	OnLost func(e Entry, err error)

	mu        sync.Mutex
	inflight  map[string]bool
	delivered map[string]bool
	wg        sync.WaitGroup
}

func (m *Manager) claim(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.inflight == nil {
		m.inflight, m.delivered = map[string]bool{}, map[string]bool{}
	}
	if m.inflight[id] || m.delivered[id] {
		return false
	}
	m.inflight[id] = true
	return true
}

func (m *Manager) release(id string, delivered bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.inflight, id)
	if delivered {
		m.delivered[id] = true
	}
}

// Track records a new run (so a restart can pick it up) before anything else happens to it.
func (m *Manager) Track(e Entry) {
	if m.Store != nil {
		m.Store.Add(e)
	}
}

// Busy is true while run id is being followed here.
func (m *Manager) Busy(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inflight[id]
}

// Finish records a final the caller shows itself (a foreground follow): exactly once, with the
// notification when the window isn't focused. false: it was already delivered.
func (m *Manager) Finish(e Entry, f Final) bool {
	if !m.claim(e.RunID) {
		return false
	}
	m.land(e, f, false)
	m.release(e.RunID, true)
	return true
}

// Hold claims run id for a foreground follow, so a resume (start-up, refocus) can't pick the same
// run up and deliver it behind the turn's back. false: it is already followed or delivered.
func (m *Manager) Hold(id string) bool { return m.claim(id) }

// FinishHeld records the final of a run the caller holds (Hold) and shows itself.
func (m *Manager) FinishHeld(e Entry, f Final) {
	m.land(e, f, false)
	m.release(e.RunID, true)
}

// Unhold gives a held run back (the foreground stopped watching; a background follow or the
// next resume takes it).
func (m *Manager) Unhold(id string) { m.release(id, false) }

// Land delivers a final the caller has (a local background turn) exactly once.
func (m *Manager) Land(e Entry, f Final) bool {
	if !m.claim(e.RunID) {
		return false
	}
	m.land(e, f, true)
	m.release(e.RunID, true)
	return true
}

func (m *Manager) land(e Entry, f Final, deliver bool) {
	if deliver && m.Deliver != nil {
		m.Deliver(e, f)
	}
	if m.Store != nil {
		m.Store.Remove(e.RunID)
	}
	m.Notifier.Land(e.RunID, e.Conv, f)
	if m.API != nil && e.ClientMsgID != "" {
		// The final is in the chat: the gateway can forget the run now.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = m.API.Delete(ctx, e.RunID)
		cancel()
	}
}

// Follow follows e in the background (from `after`) and delivers its final. A run already being
// followed or already delivered is left alone.
func (m *Manager) Follow(ctx context.Context, e Entry, after int) bool {
	if !m.claim(e.RunID) {
		return false
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		f, err := m.Follower(e).Follow(ctx, e.RunID, after, e.Action)
		if f.Intent == "" || e.LocalIntent {
			f.Intent = e.Intent
		}
		f.LocalIntent = e.LocalIntent
		if err != nil {
			m.release(e.RunID, false)
			if m.OnLost != nil && ctx.Err() == nil {
				m.OnLost(e, err)
			}
			return
		}
		m.land(e, f, true)
		m.release(e.RunID, true)
	}()
	return true
}

// Wait waits for the background follows (tests).
func (m *Manager) Wait() { m.wg.Wait() }

// Resume runs on start-up and when the app comes back: GET /v1/runs, then every run of ours
// that is unfinished here gets followed: a final that landed meanwhile is shown, a run waiting
// for this computer (needs_device) gets its tool calls answered. Runs the gateway no longer has
// are dropped after their 6 h life. It returns how many runs it picked up.
func (m *Manager) Resume(ctx context.Context) (int, error) {
	if m.Store == nil {
		return 0, nil
	}
	local := m.Store.All()
	if len(local) == 0 {
		return 0, nil
	}
	remote, err := m.API.List(ctx)
	if err != nil {
		return 0, err
	}
	byID := map[string]Summary{}
	for _, r := range remote {
		byID[r.ID] = r
	}
	n := 0
	for _, e := range local {
		r, ok := byID[e.RunID]
		if !ok || (r.ClientMsgID != "" && !strings.HasPrefix(r.ClientMsgID, IDPrefix)) {
			if time.Since(e.Created) > 7*time.Hour {
				m.Store.Remove(e.RunID)
			}
			continue
		}
		if m.Follow(ctx, e, 0) {
			n++
		}
	}
	return n, nil
}
