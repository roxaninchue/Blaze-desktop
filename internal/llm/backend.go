package llm

// v0.19 (desktop): an optional backend that can take over routing. Blaze Desktop sets
// one (internal/cloudai) so every model call goes through Blaze Cloud by default, or
// through the user's own models when they chose Custom. Blaze CLI never sets it, so
// CLI routing is exactly what it was (the user's providers in ~/.blaze/config.json).
//
// Everything here is additive: no existing function changes shape.

import (
	"bytes"
	"context"
	"encoding/json"
	"sync"
	"time"

	"blaze-desktop/internal/config"
)

// Backend may answer a turn instead of the provider router.
// handled=false means "not mine": the classic router runs with cfg as usual.
type Backend interface {
	Complete(ctx context.Context, cfg *config.Config, msgs []Message, tools []Tool, o Opts) (r *Result, err error, handled bool)
	Quick(ctx context.Context, cfg *config.Config, msgs []Message, timeout time.Duration) (text string, err error, handled bool)
}

var (
	backendMu sync.RWMutex
	backend   Backend
)

// SetBackend installs (or with nil removes) the routing backend.
func SetBackend(b Backend) {
	backendMu.Lock()
	backend = b
	backendMu.Unlock()
}

func currentBackend() Backend {
	backendMu.RLock()
	defer backendMu.RUnlock()
	return backend
}

// CompleteDirect is the classic provider router, never the backend. The backend uses it
// for Custom models; nothing else should need it.
func CompleteDirect(ctx context.Context, cfg *config.Config, msgs []Message, tools []Tool, o Opts) (*Result, error) {
	return complete(ctx, cfg, Sanitize(msgs), tools, o, false, false)
}

// QuickDirect is the classic quick call, never the backend.
func QuickDirect(ctx context.Context, cfg *config.Config, msgs []Message, timeout time.Duration) (string, error) {
	return quick(ctx, cfg, msgs, timeout)
}

// Wire is the request "messages" array for a provider (images as image_url parts when
// vision is true; Private images only ever as their local description unless local).
func Wire(msgs []Message, vision, local bool) json.RawMessage {
	b, _ := json.Marshal(wire(KeepRecentImages(msgs, 3), vision, local))
	return b
}

// CleanErr pulls the human message out of an OpenAI-shaped error body.
func CleanErr(body string) string { return cleanErr(body) }

// RateHeaders keeps the x-ratelimit-* headers of a reply.
var RateHeaders = rateHeaders

// ParseWhole reads a non-streamed chat completion.
func ParseWhole(body []byte, onText func(string)) (*Result, error) {
	return parseWhole(bytes.NewReader(body), onText)
}

// Plainer is an error that already knows the one line the chat shows (Plain uses it as is).
type Plainer interface {
	error
	PlainLine() string
}
