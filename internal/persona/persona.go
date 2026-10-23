// Package persona is Blaze's shared voice (v0.19.8): the same system prompt in every Blaze app,
// published by the downloads site at https://blazeinchue.pages.dev/persona.json. It is sent as the
// FIRST system message, byte-identical every turn (prompt caching); app instructions follow in a
// second system message. A bundled copy of v1 is the fallback; the site's copy is cached on disk
// and refetched at most once a day.
package persona

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

//go:embed v1.json
var bundled []byte

// URL is the latest persona.
var URL = "https://blazeinchue.pages.dev/persona.json"

// Eval is one of the persona's checks. Context and Expect are kept raw: persona 1.0.2 uses both a
// list of prior messages and a plain description under "context" (v0.19.13; 1.0.2 failed to
// parse before, so the live persona never replaced the bundled one).
type Eval struct {
	ID      string          `json:"id"`
	Context json.RawMessage `json:"context,omitempty"`
	User    string          `json:"user"`
	Expect  json.RawMessage `json:"expect,omitempty"`
}

// Persona is persona.json.
type Persona struct {
	Version string `json:"version"`
	Name    string `json:"name"`
	System  string `json:"system"`
	Evals   []Eval `json:"evals"`
}

var (
	mu  sync.RWMutex
	cur Persona
)

func parse(b []byte) (Persona, error) {
	var p Persona
	if err := json.Unmarshal(b, &p); err != nil {
		return p, err
	}
	if strings.TrimSpace(p.System) == "" || p.Version == "" || len(p.System) > 20000 {
		return p, errors.New("not a persona")
	}
	return p, nil
}

func init() {
	p, err := parse(bundled)
	if err != nil {
		panic("bundled persona: " + err.Error())
	}
	cur = p
}

// Current is the persona in use.
func Current() Persona {
	mu.RLock()
	defer mu.RUnlock()
	return cur
}

// System is the first system message.
func System() string { return Current().System }

func cachePath(dir string) string { return filepath.Join(dir, "persona.json") }

// Load uses the cached site copy in dir when it is valid (else the bundled v1).
func Load(dir string) {
	if b, err := os.ReadFile(cachePath(dir)); err == nil {
		if p, err := parse(b); err == nil {
			mu.Lock()
			cur = p
			mu.Unlock()
		}
	}
}

// Refresh fetches the site copy when the cache is a day old or missing. Quiet on failure.
func Refresh(ctx context.Context, dir string, hc *http.Client) error {
	if fi, err := os.Stat(cachePath(dir)); err == nil && time.Since(fi.ModTime()) < 24*time.Hour {
		return nil
	}
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "Blaze-Desktop")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return errors.New(resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	if err != nil {
		return err
	}
	p, err := parse(b)
	if err != nil {
		return err
	}
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(cachePath(dir), b, 0o644); err != nil {
		return err
	}
	mu.Lock()
	cur = p
	mu.Unlock()
	return nil
}
