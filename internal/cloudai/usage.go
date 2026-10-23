package cloudai

// The usage meter (v0.19.9, gateway 0.7.0, the same contract as Blaze CLI's Honest meter): what the
// account has used today and what is left, from the headers on every reply and from GET /v1/usage
// (free). Only the caller's own numbers.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Quota is today's allowance. A limit or left of -1 means no cap ("none").
type Quota struct {
	Plan          string    `json:"plan,omitempty"`
	RequestsUsed  int       `json:"requests_used"`
	RequestsLimit int       `json:"requests_limit"`
	RequestsLeft  int       `json:"requests_left"`
	TokensUsed    int       `json:"tokens_used"`
	TokensLimit   int       `json:"tokens_limit"`
	TokensLeft    int       `json:"tokens_left"`
	ResetsAt      string    `json:"resets_at,omitempty"`
	LastTokens    int       `json:"last_reply_tokens,omitempty"` // tokens the last reply used, when reported
	At            time.Time `json:"at"`
}

func atoiOr(s string, def int) int {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return def
	case strings.EqualFold(s, "none"):
		return -1
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// QuotaFrom reads the per-reply allowance headers; nil when the gateway sent none.
func QuotaFrom(h http.Header) *Quota {
	if h.Get("X-Blaze-Requests-Limit") == "" && h.Get("X-RateLimit-Remaining-Requests") == "" {
		return nil
	}
	return &Quota{
		Plan:          h.Get("X-Blaze-Plan"),
		RequestsUsed:  atoiOr(h.Get("X-Blaze-Requests-Used"), 0),
		RequestsLimit: atoiOr(h.Get("X-Blaze-Requests-Limit"), 0),
		RequestsLeft:  atoiOr(h.Get("X-RateLimit-Remaining-Requests"), 0),
		TokensUsed:    atoiOr(h.Get("X-Blaze-Tokens-Used"), 0),
		TokensLimit:   atoiOr(h.Get("X-Blaze-Tokens-Limit"), -1),
		TokensLeft:    atoiOr(h.Get("X-Blaze-Tokens-Remaining"), -1),
		ResetsAt:      h.Get("X-Blaze-Quota-Reset"),
		At:            time.Now(),
	}
}

// noteQuota keeps the newest allowance, adding this reply's tokens (the headers are as of its start).
func (g *Gateway) noteQuota(q *Quota, replyTokens int) {
	if q == nil {
		return
	}
	if replyTokens > 0 {
		q.LastTokens = replyTokens
		q.TokensUsed += replyTokens
		if q.TokensLeft >= 0 {
			q.TokensLeft = max(0, q.TokensLeft-replyTokens)
		}
	}
	g.mu.Lock()
	g.quota = q
	g.mu.Unlock()
}

// LastQuota is the newest allowance seen (nil before any reply or Usage call).
func (g *Gateway) LastQuota() *Quota {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.quota == nil {
		return nil
	}
	c := *g.quota
	return &c
}

// Usage asks GET /v1/usage (charges nothing).
func (g *Gateway) Usage(ctx context.Context) (*Quota, error) {
	tok := ""
	if g.Token != nil {
		tok = g.Token()
	}
	if tok == "" {
		return nil, ErrSignedOut
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	u := strings.TrimSuffix(strings.TrimRight(g.Base, "/"), "/v1") + "/v1/usage"
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "Blaze-Desktop")
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 401 {
		return nil, ErrSignedOut
	}
	if resp.StatusCode != 200 {
		return nil, ErrUnavailable
	}
	var b struct {
		Plan     string `json:"plan"`
		ResetsAt string `json:"resets_at"`
		Requests struct {
			Used      int `json:"used"`
			Limit     int `json:"limit"`
			Remaining int `json:"remaining"`
		} `json:"requests"`
		Tokens struct {
			Used      int  `json:"used"`
			Limit     *int `json:"limit"`
			Remaining *int `json:"remaining"`
		} `json:"tokens"`
		LastReply *struct {
			Tokens *int `json:"tokens"`
		} `json:"last_reply"`
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	q := &Quota{Plan: b.Plan, RequestsUsed: b.Requests.Used, RequestsLimit: b.Requests.Limit, RequestsLeft: b.Requests.Remaining,
		TokensUsed: b.Tokens.Used, TokensLimit: -1, TokensLeft: -1, ResetsAt: b.ResetsAt, At: time.Now()}
	if b.Tokens.Limit != nil {
		q.TokensLimit = *b.Tokens.Limit
	}
	if b.Tokens.Remaining != nil {
		q.TokensLeft = *b.Tokens.Remaining
	}
	if b.LastReply != nil && b.LastReply.Tokens != nil {
		q.LastTokens = *b.LastReply.Tokens
	}
	g.mu.Lock()
	g.quota = q
	g.mu.Unlock()
	c := *q
	return &c, nil
}

// ErrUnavailable: Blaze Cloud didn't answer the usage request.
var ErrUnavailable = errors.New("unavailable")
