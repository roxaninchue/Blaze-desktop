// Package router is BlazeRouter: a free AI gateway built into Blaze Desktop, a native
// Go port of the core of OmniRoute (github.com/diegosouzapw/OmniRoute, MIT).
//
// It runs a local server on 127.0.0.1 (port 20128, or the next free one) with one
// OpenAI-compatible endpoint that routes each request across the user's providers:
// combos with fallback chains and routing strategies, a circuit breaker, retries,
// rate-limit and quota detection, key rings, usage and request logs, prompt
// compression, and a dashboard at /dashboard. It builds on Blaze's own model router
// (internal/llm): the same keys (Blaze's shared key store, also Blaze CLI's), the
// same health and cool-downs, the same usage counts.
package router

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

// Options connect the router to the app.
type Options struct {
	Config  func() *config.Config // Blaze's live config (keys, chain, chosen models)
	Version string
	// Changed runs after the dashboard changes keys, models or combos (the app refreshes its UI).
	Changed func()
	// ConnectOpenRouter runs Blaze's one-click OpenRouter sign-in; returns the model or an error.
	ConnectOpenRouter func() (model string, err error)
	// AddKey tests and saves a key the way Blaze always has (returns the model it will use).
	AddKey func(provider, key string) (model, note string, err error)
}

type Router struct {
	opt Options
	log *reqLog

	mu      sync.Mutex
	set     *Settings
	srv     *http.Server
	port    int
	running bool
	lastErr string
	token   string // the dashboard's session token (this run only)
	started time.Time
}

// New loads the settings (migrating Blaze's old model settings the first time) and
// prepares the server without starting it.
func New(opt Options) *Router {
	r := &Router{opt: opt, log: openLog(), set: loadSettings()}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	r.token = hex.EncodeToString(b)
	if r.set.APIKey == "" {
		r.set.APIKey = NewKey()
		_ = r.set.save()
	}
	if cfg := r.cfg(); cfg != nil && r.set.migrate(cfg) {
		_ = r.set.save()
	}
	for id, base := range r.set.Bases {
		config.SetBase(id, base)
	}
	config.KeyHook = func(provider string) string {
		if provider == config.RouterID {
			return r.APIKey()
		}
		return ""
	}
	return r
}

func (r *Router) cfg() *config.Config {
	if r.opt.Config != nil {
		if c := r.opt.Config(); c != nil {
			return c
		}
	}
	return config.Load()
}

func (r *Router) changed() {
	if r.opt.Changed != nil {
		go r.opt.Changed()
	}
}

// Settings is a copy of the current settings.
func (r *Router) Settings() Settings {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := *r.set
	s.Combos = append([]Combo(nil), r.set.Combos...)
	return s
}

// APIKey is the local key clients send.
func (r *Router) APIKey() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.set.APIKey
}

// Token is the dashboard's session token for this run.
func (r *Router) Token() string { return r.token }

// Start opens the server on the saved port, or the next free one.
func (r *Router) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		return nil
	}
	var ln net.Listener
	var err error
	port := r.set.Port
	for i := 0; i < 20; i++ {
		ln, err = net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port+i))
		if err == nil {
			port += i
			break
		}
	}
	if err != nil {
		r.lastErr = "Couldn't open a port near " + fmt.Sprint(r.set.Port) + ": " + err.Error()
		return errors.New(r.lastErr)
	}
	r.port, r.running, r.lastErr, r.started = port, true, "", time.Now()
	r.srv = &http.Server{Handler: r.Handler(), ReadHeaderTimeout: 10 * time.Second}
	srv := r.srv
	config.SetBase(config.RouterID, fmt.Sprintf("http://127.0.0.1:%d/v1/", port))
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			r.mu.Lock()
			r.running, r.lastErr = false, err.Error()
			r.mu.Unlock()
			config.SetBase(config.RouterID, "")
		}
	}()
	return nil
}

// Stop closes the server (requests in flight get two seconds to finish).
func (r *Router) Stop() {
	r.mu.Lock()
	srv := r.srv
	r.srv, r.running = nil, false
	r.mu.Unlock()
	config.SetBase(config.RouterID, "")
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// SetEnabled turns the server on or off and remembers it.
func (r *Router) SetEnabled(on bool) error {
	r.mu.Lock()
	r.set.Enabled = &on
	s := r.set
	r.mu.Unlock()
	_ = s.save()
	r.changed()
	if on {
		return r.Start()
	}
	r.Stop()
	return nil
}

// Running is true while the server listens.
func (r *Router) Running() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// URL is the server's base address ("" when stopped).
func (r *Router) URL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.running {
		return ""
	}
	return fmt.Sprintf("http://127.0.0.1:%d", r.port)
}

// DashboardURL opens the dashboard already signed in for this run (the token rides
// in the fragment, which never reaches a server log).
func (r *Router) DashboardURL(page string) string {
	u := r.URL()
	if u == "" {
		return ""
	}
	if page != "" {
		page = "&p=" + page
	}
	return u + "/dashboard/#t=" + r.token + page
}

// Status is the top-bar dot: green ready, amber running but nothing can answer
// (no keys, or everything resting), red stopped.
type Status struct {
	Running bool   `json:"running"`
	Level   string `json:"level"` // green, amber, red
	Note    string `json:"note"`
	URL     string `json:"url"`
	Port    int    `json:"port"`
	Ready   int    `json:"ready"` // providers that can answer now
	Models  int    `json:"models"`
	Version string `json:"version"`
}

func (r *Router) Status() Status {
	s := Status{Running: r.Running(), URL: r.URL(), Version: r.opt.Version}
	r.mu.Lock()
	s.Port, s.Note = r.port, r.lastErr
	r.mu.Unlock()
	if !s.Running {
		s.Level = "red"
		if s.Note == "" {
			s.Note = "BlazeRouter is off"
		}
		return s
	}
	ts := r.autoTargets(r.cfg())
	prov := map[string]bool{}
	resting := 0
	for _, t := range ts {
		prov[t.Provider] = true
		if llm.CoolingFor(t.Provider, t.Model) > 0 {
			resting++
		}
	}
	s.Ready, s.Models = len(prov), len(ts)
	switch {
	case len(ts) == 0:
		s.Level, s.Note = "amber", "Running, but no provider has a key yet"
	case resting == len(ts):
		s.Level, s.Note = "amber", "Running; every model is resting after rate limits"
	default:
		s.Level, s.Note = "green", fmt.Sprintf("Running on port %d · %d provider%s ready", s.Port, s.Ready, plural(s.Ready))
	}
	return s
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// authorized checks the local key (Bearer, x-api-key, x-goog-api-key or ?key=) or the
// dashboard's token.
func (r *Router) authorized(req *http.Request) bool {
	k := strings.TrimSpace(strings.TrimPrefix(req.Header.Get("Authorization"), "Bearer "))
	for _, c := range []string{k, req.Header.Get("x-api-key"), req.Header.Get("x-goog-api-key"), req.URL.Query().Get("key"), req.Header.Get("X-Blaze-Token")} {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(c), []byte(r.APIKey())) == 1 || subtle.ConstantTimeCompare([]byte(c), []byte(r.token)) == 1 {
			return true
		}
	}
	return false
}

// localHost guards against DNS rebinding: only requests addressed to this computer.
func localHost(host string) bool {
	h := host
	if x, _, err := net.SplitHostPort(host); err == nil {
		h = x
	}
	h = strings.Trim(h, "[]")
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

func clientName(ua string) string {
	low := strings.ToLower(ua)
	for _, c := range []struct{ k, n string }{{"cursor", "Cursor"}, {"claude", "Claude Code"}, {"anthropic", "Anthropic SDK"}, {"cline", "Cline"}, {"roo", "Roo Code"},
		{"continue", "Continue"}, {"aider", "Aider"}, {"opencode", "OpenCode"}, {"codex", "Codex"}, {"kilo", "Kilo Code"}, {"go-http-client", "Blaze"},
		{"blaze", "Blaze"}, {"openai/python", "OpenAI Python"}, {"openai/js", "OpenAI JS"}, {"python", "Python"}, {"curl", "curl"}, {"node", "Node"},
		{"mozilla", "Browser"}} {
		if strings.Contains(low, c.k) {
			return c.n
		}
	}
	if f := strings.Fields(ua); len(f) > 0 {
		return trunc(f[0], 24)
	}
	return ""
}
