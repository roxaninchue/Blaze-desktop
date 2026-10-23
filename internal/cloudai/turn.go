package cloudai

// v0.19.1 (Blaze Mobile 0.45.1 TurnModel.kt): one route for the whole turn. Each step of a turn
// is its own gateway request, so after a rate limit or a health mark step 2 could land on another
// model than step 1, with other tool habits and a cold prompt cache. Every request of a turn now
// carries:
//
//   - X-Blaze-Turn: <id>, the same id for every step of the turn;
//   - X-Blaze-Prefer: provider/model, the route that answered the turn's first step, tried first
//     while it is healthy (Blaze Gateway 0.6.1; the gateway still falls back on an error).
//
// Both are plain headers: a gateway that doesn't read them ignores them.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	TurnHeader   = "X-Blaze-Turn"
	PreferHeader = "X-Blaze-Prefer"
)

// Turn is one user turn's route memory. Safe for concurrent use.
type Turn struct {
	ID string

	mu       sync.Mutex
	steps    []Step
	pin      string
	last     string
	switches int
}

// NewTurn makes a turn with a random id.
func NewTurn() *Turn {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return &Turn{ID: "t" + hex.EncodeToString(b)}
}

// Served records the route ("provider/model") that answered one step; "" is ignored.
func (t *Turn) Served(route string) {
	r := strings.TrimSpace(route)
	if t == nil || r == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.pin == "" {
		t.pin = r
	}
	if t.last != "" && t.last != r {
		t.switches++
	}
	t.last = r
}

// Step is one gateway attempt of a turn, for the "What happened" view (v0.19.7). Provider and
// Model are what the gateway's X-Blaze-Provider / X-Blaze-Model said.
type Step struct {
	Started   time.Time `json:"started"`
	Seconds   float64   `json:"seconds"`
	Provider  string    `json:"provider,omitempty"`
	Model     string    `json:"model,omitempty"`
	Status    int       `json:"status,omitempty"`
	TTFBms    int       `json:"ttfb_ms,omitempty"`
	Hedged    bool      `json:"hedged,omitempty"`
	Replay    bool      `json:"replay,omitempty"`
	Finish    string    `json:"finish,omitempty"`
	TokensIn  int       `json:"tokens_in,omitempty"`
	TokensOut int       `json:"tokens_out,omitempty"`
	Error     string    `json:"error,omitempty"`
	// Gateway 0.6.9 timings (v0.19.8).
	RequestID    string `json:"request_id,omitempty"`     // X-Request-Id
	ServerTTFTms int    `json:"server_ttft_ms,omitempty"` // X-Blaze-TTFT-Ms: gateway receive -> first byte
	ServerTiming string `json:"server_timing,omitempty"`  // Server-Timing
	Lane         string `json:"lane,omitempty"`           // X-Blaze-Lane ("fast")
	ToolsDropped bool   `json:"tools_dropped,omitempty"`  // X-Blaze-Tools-Dropped
	ClientTTFTms int    `json:"client_ttft_ms,omitempty"` // request sent -> first streamed word, measured here
	FastLane     bool   `json:"fast_lane,omitempty"`      // X-Blaze-Fast-Lane (gateway 0.7.0)
	Fallback     bool   `json:"fallback,omitempty"`       // X-Blaze-Fallback: an earlier model failed first
}

// Record adds one attempt (at most 200 per turn).
func (t *Turn) Record(s Step) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.steps) < 200 {
		t.steps = append(t.steps, s)
	}
}

// Steps is a copy of the attempts so far.
func (t *Turn) Steps() []Step {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]Step(nil), t.steps...)
}

// Pin is the route of the turn's first answered step ("" until then).
func (t *Turn) Pin() string {
	if t == nil {
		return ""
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pin
}

// Switches counts steps answered by another route than the step before.
func (t *Turn) Switches() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.switches
}

// Apply sets the turn headers on a gateway request.
func (t *Turn) Apply(h http.Header) {
	if t == nil {
		return
	}
	if id := headerSafe(t.ID); id != "" {
		h.Set(TurnHeader, id)
	}
	if p := headerSafe(t.Pin()); p != "" {
		h.Set(PreferHeader, p)
	}
}

// headerSafe keeps visible ASCII only, at most 120 characters.
func headerSafe(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '!' && r <= '~' {
			b.WriteRune(r)
			if b.Len() >= 120 {
				break
			}
		}
	}
	return b.String()
}

type turnKey struct{}

// WithTurn attaches t to ctx; every gateway request made with that ctx carries the turn headers.
func WithTurn(ctx context.Context, t *Turn) context.Context {
	return context.WithValue(ctx, turnKey{}, t)
}

// TurnFrom is the turn attached to ctx, or nil.
func TurnFrom(ctx context.Context) *Turn {
	t, _ := ctx.Value(turnKey{}).(*Turn)
	return t
}
