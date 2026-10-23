package cloudai

// v0.19.1 (Blaze Mobile 0.45.1 LlmClient.prewarm): open the connection to Blaze Cloud before Send,
// so DNS and TLS are done when the first request goes out. It is a GET of the gateway's public
// /health (no account token, no model call, no quota), at most once a minute, on the same
// keep-alive client the chat requests use. The body is read to the end and closed so the
// connection goes back to the pool instead of being dropped.

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// PrewarmEvery is the shortest gap between two warm-ups.
const PrewarmEvery = 25 * time.Second // v0.19.7: was a minute; gateway 0.6.9 asks for 20-30 s

// HealthURL is the gateway root's /health for a base like https://host/v1.
func HealthURL(base string) string {
	b := strings.TrimRight(base, "/")
	b = strings.TrimSuffix(b, "/v1")
	return b + "/health"
}

// WarmURL is the gateway's warm-up endpoint for a base like https://host/v1.
func WarmURL(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1") + "/v1/warm"
}

// Prewarm opens (or refreshes) a pooled connection to the gateway. It returns false when it was
// skipped (a warm-up ran less than PrewarmEvery ago) or failed; it never returns an error to show.
func (g *Gateway) Prewarm(ctx context.Context) bool {
	now := time.Now()
	g.mu.Lock()
	if !g.lastWarm.IsZero() && now.Sub(g.lastWarm) < PrewarmEvery {
		g.mu.Unlock()
		return false
	}
	g.lastWarm = now
	g.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// v0.19.8 (gateway 0.6.9): signed in, POST /v1/warm, which also loads the account and opens
	// TLS to the first providers; signed out, the public /health.
	tok := ""
	if g.Token != nil {
		tok = g.Token()
	}
	method, u := "GET", HealthURL(g.Base)
	if tok != "" {
		method, u = "POST", WarmURL(g.Base)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", "Blaze-Desktop")
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	return resp.StatusCode < 500
}
