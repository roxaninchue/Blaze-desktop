package main

// "What happened" for each reply (v0.19.7): which model and service answered each step, the
// tools it called and how long each took, time to first word, errors, and the turn id that
// matches the gateway's logs (X-Blaze-Turn). Built while the turn runs, sent as one "trace"
// event when it ends, and shown only when the user opens it under the reply.

import (
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/llm"
)

type traceStep struct {
	N        int     `json:"n"`
	Route    string  `json:"route,omitempty"`
	Provider string  `json:"provider,omitempty"`
	Model    string  `json:"model,omitempty"`
	Seconds  float64 `json:"seconds"`
	TTFTms   int64   `json:"ttft_ms,omitempty"` // first streamed word, measured here
	Finish   string  `json:"finish,omitempty"`
	In       int     `json:"tokens_in,omitempty"`
	Out      int     `json:"tokens_out,omitempty"`
	Tools    int     `json:"tools_offered"`
	Messages int     `json:"messages"`
	Note     string  `json:"note,omitempty"`
	Error    string  `json:"error,omitempty"`
}

type traceTool struct {
	Step    int     `json:"step"`
	Name    string  `json:"name"`
	Seconds float64 `json:"seconds"`
	Error   string  `json:"error,omitempty"`
	Blocked bool    `json:"blocked,omitempty"`
}

type turnTrace struct {
	mu       sync.Mutex
	Turn     string         `json:"turn"`
	Seconds  float64        `json:"seconds"`
	TTFTms   int64          `json:"ttft_ms,omitempty"` // first word of the whole reply
	Steps    []traceStep    `json:"steps"`
	Tools    []traceTool    `json:"tools"`
	Attempts []cloudai.Step `json:"attempts,omitempty"` // every Blaze Cloud request, retries included
	Err      string         `json:"error,omitempty"`
}

func (t *turnTrace) step(n int, r *llm.Result, err error, took, ttft time.Duration, tools, msgs int) {
	st := traceStep{N: n, Seconds: took.Seconds(), Tools: tools, Messages: msgs}
	if ttft > 0 {
		st.TTFTms = ttft.Milliseconds()
	}
	if r != nil {
		st.Route, st.Provider, st.Model, st.Finish, st.Note = r.Route, r.Provider, r.Model, r.Finish, r.Note
		if r.Usage != nil {
			st.In, st.Out = r.Usage.Prompt, r.Usage.Completion
		}
	}
	if err != nil {
		st.Error = clip(err.Error(), 400)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.TTFTms == 0 && st.TTFTms > 0 {
		// Time to first word of the reply: earlier steps' time plus this step's TTFT.
		var before float64
		for _, p := range t.Steps {
			before += p.Seconds
		}
		t.TTFTms = int64(before*1000) + st.TTFTms
	}
	if err != nil && t.Err == "" {
		t.Err = st.Error
	}
	if len(t.Steps) < 100 {
		t.Steps = append(t.Steps, st)
	}
}

func (t *turnTrace) tool(step int, name string, start time.Time, res string, blocked bool) {
	tt := traceTool{Step: step, Name: name, Blocked: blocked}
	if !start.IsZero() {
		tt.Seconds = time.Since(start).Seconds()
	}
	if looksFailed(res) {
		tt.Error = clip(firstLine(strings.TrimSpace(res)), 200)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.Tools) < 300 {
		t.Tools = append(t.Tools, tt)
	}
}

// looksFailed: tool results that report an error start with one of these.
func looksFailed(res string) bool {
	l := strings.ToLower(strings.TrimSpace(res))
	for _, p := range []string{"error", "failed", "couldn't", "could not", "not allowed", "refused", "timed out", "not in a private chat"} {
		if strings.HasPrefix(l, p) {
			return true
		}
	}
	return false
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func (a *App) emitTrace(s *session, t *turnTrace, turn *cloudai.Turn, started time.Time) {
	t.mu.Lock()
	t.Seconds = time.Since(started).Seconds()
	raw := turn.Steps()
	t.Attempts = scrubAttempts(raw)
	t.mu.Unlock()
	if traceHook != nil {
		traceHook(t.Turn, raw)
	}
	a.temit(s, "trace", t)
}

// traceHook, when set (tests only), sees each turn's unscrubbed gateway attempts. Nil in the app.
var traceHook func(turn string, raw []cloudai.Step)

// scrubAttempts (v0.19.13): Blaze Cloud is one model to the user. The gateway's X-Blaze-Provider /
// X-Blaze-Model headers name the upstream that answered; they stay in the gateway's own logs
// (matched by request id) and never reach the "What happened" view.
func scrubAttempts(in []cloudai.Step) []cloudai.Step {
	out := make([]cloudai.Step, len(in))
	for i, st := range in {
		st.Provider, st.Model = cloudai.Name, cloudai.Model
		out[i] = st
	}
	return out
}
