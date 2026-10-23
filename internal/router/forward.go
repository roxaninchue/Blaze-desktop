package router

// Forwarding: send an OpenAI-format request to the first target that takes it.
//
//   - Targets come from plan (combos.go), already ordered by the strategy and the
//     circuit breaker (resting models last).
//   - Each provider can hold several keys (Blaze's key ring). A key that runs out of
//     quota on a model rests and the next key carries on with the same model.
//   - Outages and timeouts get one quick retry with a short backoff; rate limits,
//     auth errors and refusals move straight on to the next target.
//   - Health, cool-downs, key rests and the rate-limit budget are Blaze's own
//     (internal/llm), shared with Blaze's chats.
//   - Once an answer has started streaming to the client it is never restarted on
//     another model.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
)

var upstreamClient = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   4,
}}

// retryBackoff is the pause before the one retry after an outage (shortened in tests).
var retryBackoff = 400 * time.Millisecond

// Attempt is one try in a request's route, for the log and error messages.
type Attempt struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Code     int    `json:"code"`
	Error    string `json:"error"`
}

// upstream is a target that accepted the request: its response body is still unread.
type upstream struct {
	resp     *http.Response
	t        Target
	plan     Plan
	start    time.Time
	chars    int
	saved    int
	attempts []Attempt
	first    []byte // lines already read from a stream while checking it didn't start with an error
}

// RouteError is returned when no target could take the request.
type RouteError struct {
	Code     int
	Msg      string
	Attempts []Attempt
}

func (e *RouteError) Error() string { return e.Msg }

// dispatch sends body (an OpenAI chat request) along the plan and returns the first
// upstream that accepted it.
func (r *Router) dispatch(ctx context.Context, body map[string]any, pl Plan, stream bool) (*upstream, error) {
	if pl.Chaos {
		return r.dispatchChaos(ctx, body, pl, stream)
	}
	cfg := r.cfg()
	raw, _ := json.Marshal(body)
	tokens := llm.Estimate(len(raw))
	var attempts []Attempt
	if len(pl.Targets) == 0 {
		msg := "No model is set up in BlazeRouter yet. Add a free key (Gemini, Groq, OpenRouter…) on the dashboard's Providers page."
		if pl.Combo != "" && pl.Combo != "auto" {
			msg = fmt.Sprintf("Combo %q has no usable models: add keys for its providers or edit it on the dashboard.", pl.Combo)
		}
		return nil, &RouteError{Code: 503, Msg: msg}
	}
	triedReady := false
	lastCode := 0
	for i, t := range pl.Targets {
		cooling := llm.CoolingFor(t.Provider, t.Model) > 0
		if cooling && (triedReady || i > 0) {
			// Circuit open: skip it. Only when every target rests is the first one tried anyway.
			attempts = append(attempts, Attempt{t.Provider, t.Model, 0, "resting after failures"})
			continue
		}
		if w, why := llm.Wait(t.key(), tokens); w > 0 && len(pl.Targets) > 1 {
			attempts = append(attempts, Attempt{t.Provider, t.Model, 429, "resting (" + why + ")"})
			continue
		}
		triedReady = triedReady || !cooling
		p, ok := config.ProviderByID(t.Provider)
		if !ok || p.BaseURL == "" {
			continue
		}
		keys := cfg.AllKeys(t.Provider)
		if len(keys) == 0 {
			keys = []string{""}
		}
		var lastErr error
		usedKey := false
		for _, key := range keys {
			if len(keys) > 1 && llm.KeyResting(t.Provider, t.Model, key) {
				continue
			}
			usedKey = true
			b := r.requestBody(body, t, stream)
			llm.Spend(t.key(), tokens)
			t0 := time.Now()
			up, err := r.send(ctx, p, key, b, stream)
			if err != nil && ctx.Err() == nil && retryable(err) {
				select {
				case <-ctx.Done():
				case <-time.After(retryBackoff):
				}
				t0 = time.Now()
				up, err = r.send(ctx, p, key, b, stream)
			}
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			if err == nil {
				noteGood(pl.Combo, t)
				up.t, up.plan, up.start, up.chars, up.attempts = t, pl, t0, len(b), attempts
				return up, nil
			}
			lastErr = err
			if llm.IsQuota(err) && len(keys) > 1 {
				var he *llm.HTTPError
				ra := 0
				if errors.As(err, &he) {
					ra = he.RetryAfter
				}
				d := llm.Limited(t.key(), err.Error(), ra)
				if d < time.Hour && llm.IsDaily(err.Error()) {
					d = time.Hour
				}
				llm.RestKey(t.Provider, t.Model, key, d)
				llm.ClearBudget(t.key())
				continue
			}
			break
		}
		if !usedKey {
			attempts = append(attempts, Attempt{t.Provider, t.Model, 429, "every key is resting"})
			continue
		}
		if lastErr == nil {
			continue
		}
		llm.ReportFail(t.Provider, t.Model, lastErr)
		a := Attempt{Provider: t.Provider, Model: t.Model, Error: trunc(lastErr.Error(), 240)}
		var he *llm.HTTPError
		if errors.As(lastErr, &he) {
			a.Code = he.Code
			lastCode = he.Code
		}
		attempts = append(attempts, a)
	}
	code := 502
	if lastCode == 429 || allRateLimited(attempts) {
		code = 429
	}
	var parts []string
	for _, a := range attempts {
		parts = append(parts, fmt.Sprintf("%s/%s: %s", a.Provider, a.Model, a.Error))
	}
	return nil, &RouteError{Code: code, Msg: "BlazeRouter: no model could answer. " + strings.Join(parts, "; "), Attempts: attempts}
}

func allRateLimited(as []Attempt) bool {
	if len(as) == 0 {
		return false
	}
	for _, a := range as {
		if a.Code != 429 && a.Code != 402 {
			return false
		}
	}
	return true
}

func retryable(err error) bool {
	var he *llm.HTTPError
	if errors.As(err, &he) {
		return he.Code == 0 || he.Code >= 500
	}
	return true // network error
}

// requestBody is the client's request with the target's model, compression applied,
// and Gemini's stricter tool schemas cleaned up.
func (r *Router) requestBody(body map[string]any, t Target, stream bool) []byte {
	out := make(map[string]any, len(body)+1)
	for k, v := range body {
		out[k] = v
	}
	out["model"] = t.Model
	out["stream"] = stream
	if t.Provider == "gemini" {
		if raw, ok := out["tools"]; ok {
			b, _ := json.Marshal(raw)
			var ts []llm.Tool
			if json.Unmarshal(b, &ts) == nil && len(ts) > 0 {
				out["tools"] = llm.AdaptTools(ts, "gemini")
			}
		}
	}
	b, _ := json.Marshal(out)
	return b
}

// send makes one call. A 2xx answer comes back unread (a stream is checked for an
// error in its first event, which some providers send with a 200).
func (r *Router) send(ctx context.Context, p config.Provider, key string, body []byte, stream bool) (*upstream, error) {
	if owner := llm.KeyOwner(key); owner != "" && owner != p.ID && p.ID != config.Custom {
		return nil, &llm.HTTPError{Code: 401, Body: fmt.Sprintf("the key saved under %s is a %s key", p.Name, config.Name(owner))}
	}
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	}
	for k, v := range llm.AuthHeaders(p.ID, key) {
		req.Header.Set(k, v)
	}
	if p.ID == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://blazeinchue.pages.dev")
		req.Header.Set("X-Title", "BlazeRouter")
	}
	resp, err := upstreamClient.Do(req)
	if err != nil {
		return nil, &llm.HTTPError{Code: 0, Body: err.Error()}
	}
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
		resp.Body.Close()
		ra, _ := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
		return nil, &llm.HTTPError{Code: resp.StatusCode, Body: errText(raw), RetryAfter: ra}
	}
	up := &upstream{resp: resp}
	if !isSSE(resp) {
		return up, nil
	}
	// Read up to the first data event: an error there means try the next target.
	br := bufio.NewReaderSize(resp.Body, 64<<10)
	var first bytes.Buffer
	for first.Len() < 1<<20 {
		line, err := br.ReadBytes('\n')
		first.Write(line)
		t := bytes.TrimSpace(line)
		if bytes.HasPrefix(t, []byte("data:")) {
			d := bytes.TrimSpace(t[5:])
			var ev struct {
				Error   json.RawMessage `json:"error"`
				Choices []any           `json:"choices"`
			}
			if json.Unmarshal(d, &ev) == nil && len(ev.Error) > 0 && string(ev.Error) != "null" && len(ev.Choices) == 0 {
				resp.Body.Close()
				return nil, streamErr(ev.Error)
			}
			break
		}
		if err != nil {
			break
		}
	}
	up.first = first.Bytes()
	up.resp.Body = readCloser{io.MultiReader(br), resp.Body}
	return up, nil
}

type readCloser struct {
	io.Reader
	c io.Closer
}

func (r readCloser) Close() error { return r.c.Close() }

func isSSE(resp *http.Response) bool {
	return strings.Contains(resp.Header.Get("Content-Type"), "event-stream")
}

func streamErr(raw json.RawMessage) error {
	var e struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	}
	_ = json.Unmarshal(raw, &e)
	code := 500
	switch v := e.Code.(type) {
	case float64:
		code = int(v)
	case string:
		if n, err := strconv.Atoi(v); err == nil {
			code = n
		}
	}
	if code < 400 {
		code = 500
	}
	if e.Message == "" {
		e.Message = string(raw)
	}
	return &llm.HTTPError{Code: code, Body: e.Message}
}

// errText pulls the message out of a provider's error body.
func errText(raw []byte) string {
	var e struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error != nil {
		switch v := e.Error.(type) {
		case string:
			return v
		case map[string]any:
			if m, ok := v["message"].(string); ok && m != "" {
				return m
			}
		}
	}
	s := strings.TrimSpace(string(raw))
	return trunc(s, 300)
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// usageOf reads token counts from an OpenAI chunk or response.
type chunkInfo struct {
	Choices []struct {
		Delta *struct {
			Content   *string         `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"delta"`
		Message *struct {
			Content   *string         `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *llm.Usage `json:"usage"`
}

// tally counts what came back, for usage when the provider sends no counts.
type tally struct {
	outChars int
	usage    *llm.Usage
}

func (t *tally) add(data []byte) {
	var c chunkInfo
	if json.Unmarshal(data, &c) != nil {
		return
	}
	if c.Usage != nil && (c.Usage.Prompt > 0 || c.Usage.Completion > 0) {
		t.usage = c.Usage
	}
	for _, ch := range c.Choices {
		for _, d := range []*struct {
			Content   *string         `json:"content"`
			ToolCalls json.RawMessage `json:"tool_calls"`
		}{ch.Delta, ch.Message} {
			if d == nil {
				continue
			}
			if d.Content != nil {
				t.outChars += len(*d.Content)
			}
			t.outChars += len(d.ToolCalls)
		}
	}
}

// finish records a finished request: health, usage, and the request log.
func (r *Router) finish(up *upstream, tl *tally, status int, err error, client, format string, stream bool) {
	ms := time.Since(up.start).Milliseconds()
	res := &llm.Result{Usage: tl.usage}
	if tl.usage == nil {
		res.Text = strings.Repeat(" ", tl.outChars)
	}
	if err == nil {
		llm.ReportOK(up.t.Provider, up.t.Model, ms)
	}
	llm.RecordUsage(up.t.Provider, up.t.Model, up.chars, res)
	in, out := llm.Estimate(up.chars), tl.outChars/4+1
	if tl.usage != nil {
		if tl.usage.Prompt > 0 {
			in = tl.usage.Prompt
		}
		if tl.usage.Completion > 0 {
			out = tl.usage.Completion
		}
	}
	e := Entry{Time: time.Now(), Asked: up.plan.Asked, Combo: up.plan.Combo, Provider: up.t.Provider, Model: up.t.Model,
		Status: status, Ms: ms, In: in, Out: out, Stream: stream, Client: client, Format: format, Saved: up.saved, Fallbacks: len(up.attempts)}
	if err != nil {
		e.Error = trunc(err.Error(), 200)
	}
	if len(up.attempts) > 0 {
		e.Route = up.attempts
	}
	r.log.add(e)
}
