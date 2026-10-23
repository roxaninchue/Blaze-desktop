package cloudai

// The Blaze gateway client: one OpenAI-compatible POST /v1/chat/completions with the
// Blaze account session token as the bearer, model blaze-auto + blaze_task, and the
// phone's retry loop (Cloud.drive) around it.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/llm"
)

// DefaultBase is the deployed gateway (gateway/ in Blaze Mobile's repo, `GET /health`).
const DefaultBase = "https://ai.blaze-app.workers.dev/v1"

// ProviderID is how Blaze Cloud shows up in usage and routes.
const ProviderID = "blaze-cloud"

// Name is what the model chip and Settings call it.
const Name = "Blaze Cloud"

// BaseFrom picks the gateway base: BLAZE_GATEWAY_URL, then the saved choice, then DefaultBase.
// It always ends in /v1 (a bare host gets it added).
func BaseFrom(saved string) string {
	b := strings.TrimSpace(os.Getenv("BLAZE_GATEWAY_URL"))
	if b == "" {
		b = strings.TrimSpace(saved)
	}
	if b == "" {
		return DefaultBase
	}
	b = strings.TrimRight(b, "/")
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	return b
}

// Gateway talks to Blaze Cloud.
type Gateway struct {
	Base  string
	Token func() string // the Blaze account session token ("" when signed out)
	HC    *http.Client
	// Sleep waits between tries (tests replace it). It returns early when ctx ends.
	Sleep func(ctx context.Context, d time.Duration) error
	// Stall is the longest wait for the next streamed chunk (default 90 s).
	Stall time.Duration

	mu        sync.Mutex
	lastRoute string    // "groq/llama-3.3-70b-versatile" of the last call (blaze_avoid names it)
	lastError string    // raw text of the last failure (diagnostics only)
	lastWarm  time.Time // v0.19.1: the last Prewarm
	quota     *Quota    // v0.19.9: the newest allowance (usage meter)
}

// NewGateway uses base with a shared keep-alive client.
func NewGateway(base string, token func() string) *Gateway {
	return &Gateway{Base: strings.TrimRight(base, "/"), Token: token, HC: &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 110 * time.Second,
		// v0.19.7 (first word faster): HTTP/2, so every request of a turn (and the warm-up) shares
		// one warm connection instead of opening new TLS sessions, and idle connections are kept
		// for 5 minutes; the keep-warm ping refreshes them while the window is in use.
		ForceAttemptHTTP2:   true,
		IdleConnTimeout:     5 * time.Minute,
		MaxIdleConnsPerHost: 4,
	}}}
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// LastError is the raw text of the last gateway failure (for Details; people see the line).
func (g *Gateway) LastError() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastError
}

func (g *Gateway) setErr(s string) {
	if len(s) > 300 {
		s = s[:300]
	}
	g.mu.Lock()
	g.lastError = s
	g.mu.Unlock()
}

// Attempt is one try: streamed or not, with the "answer now" nudge or not, and the routes to skip.
type Attempt struct {
	N      int
	Stream bool
	Nudge  bool
	Avoid  []string
}

// Drive is the phone's retry loop (Cloud.drive), pure so it can be tested without a network:
// a request too big is trimmed once (trim → true), an access 403 / expired 401 surfaces at once,
// other non-retryable errors surface as their plain line, retryable ones back off up to MaxTries;
// a stream that broke before answering (code 0) also skips the route that served it. An EMPTY reply
// is a failed try too: first asked again on the same route without streaming and with the nudge,
// then on another model (its route avoided), and the third fails with EmptyFailed.
func Drive(ctx context.Context, streaming bool, call func(Attempt) (*llm.Result, error), served func() string,
	trim func(error) bool, sleep func(context.Context, time.Duration) error) (*llm.Result, error) {
	if sleep == nil {
		sleep = sleepCtx
	}
	if trim == nil {
		trim = func(error) bool { return false }
	}
	var avoid []string
	addAvoid := func(r string) {
		if r == "" {
			return
		}
		for _, x := range avoid {
			if x == r {
				return
			}
		}
		avoid = append(avoid, r)
	}
	stream, nudge := streaming, false
	errs, empties, n := 0, 0, 0
	var last *llm.HTTPError
	for errs < MaxTries && empties < EmptyTries && n < MaxTries+EmptyTries+1 {
		a := Attempt{N: n, Stream: stream, Nudge: nudge, Avoid: append([]string(nil), avoid...)}
		n++
		r, err := call(a)
		if err == nil {
			if !IsEmpty(r) {
				return r, nil
			}
			empties++
			if empties == 1 {
				stream, nudge = false, true
			} else {
				addAvoid(served())
				stream, nudge = streaming, true
			}
			continue
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if trim(err) {
			continue
		}
		var he *llm.HTTPError
		if !errors.As(err, &he) {
			he = &llm.HTTPError{Code: 0, Body: err.Error()}
		}
		if line := Access(he.Code, he.Body); line != "" {
			return nil, &Error{Line: line, Code: he.Code, Raw: he.Body}
		}
		if he.Code == 401 || he.Code == 403 {
			return nil, &Error{Line: Expired, Code: he.Code, Raw: he.Body}
		}
		if !Retryable(he.Code, he.Body) {
			if llm.IsShapeError(he) {
				return nil, he // the caller flattens the tool turns and asks once more
			}
			return nil, &Error{Line: Friendly(he.Code, he.Body), Code: he.Code, RetryAfter: he.RetryAfter, Raw: he.Body}
		}
		last = he
		errs++
		if he.Code == 0 {
			addAvoid(served())
		}
		if errs < MaxTries {
			if err := sleep(ctx, time.Duration(BackoffMs(errs-1, he.RetryAfter))*time.Millisecond); err != nil {
				return nil, err
			}
		}
	}
	if empties >= EmptyTries || last == nil {
		return nil, &Error{Line: EmptyFailed, Code: EmptyCode}
	}
	return nil, &Error{Line: Friendly(last.Code, last.Body), Code: last.Code, RetryAfter: last.RetryAfter, Raw: last.Body}
}

// Complete answers one request through Blaze Cloud. quickCall marks small background calls.
func (g *Gateway) Complete(ctx context.Context, msgs []llm.Message, tools []llm.Tool, onText func(string), quickCall bool) (*llm.Result, error) {
	token := ""
	if g.Token != nil {
		token = strings.TrimSpace(g.Token())
	}
	if token == "" {
		return nil, &Error{Line: SignedOut, Code: 401}
	}
	msgs = llm.Sanitize(msgs)
	task := Task(msgs, tools, quickCall)
	r, err := g.drive(ctx, token, msgs, tools, onText, task)
	if err != nil && llm.IsShapeError(err) {
		// The request's shape was refused (tool turns): once more with them as plain text.
		r, err = g.drive(ctx, token, llm.Flatten(msgs), tools, onText, task)
		if err != nil {
			var he *llm.HTTPError
			if errors.As(err, &he) {
				err = &Error{Line: Busy, Code: he.Code, Raw: he.Body}
			}
		} else {
			r.Route += " · healed"
		}
	}
	if err != nil {
		var ce *Error
		if errors.As(err, &ce) && ce.Raw != "" {
			g.setErr(ce.Raw)
		}
		return nil, err
	}
	return r, nil
}

func (g *Gateway) drive(ctx context.Context, token string, msgs []llm.Message, tools []llm.Tool, onText func(string), task string) (*llm.Result, error) {
	trimmed := false
	started := false
	wrapped := func(s string) {
		started = true
		if onText != nil {
			onText(s)
		}
	}
	sleep := g.Sleep
	if sleep == nil {
		sleep = sleepCtx
	}
	return Drive(ctx, onText != nil, func(a Attempt) (*llm.Result, error) {
		m := msgs
		if a.Nudge {
			m = WithNudge(m)
		}
		var cb func(string)
		if a.Stream {
			cb = wrapped
		}
		r, err := g.call(ctx, token, m, tools, cb, task, a.Avoid)
		if err == nil && !a.Stream && onText != nil && r != nil && r.Text != "" {
			onText(r.Text) // a retry without streaming still shows its answer
		}
		if err != nil && started {
			// Words are already on screen: never restart underneath them.
			return nil, &Error{Line: Busy, Code: 0, Raw: err.Error()}
		}
		if err == nil && trimmed {
			r.Route += " · trimmed"
		}
		return r, err
	}, func() string {
		g.mu.Lock()
		defer g.mu.Unlock()
		return g.lastRoute
	}, func(err error) bool {
		g.setErr(err.Error())
		if !trimmed && llm.TooLarge(err) {
			trimmed = true
			msgs = llm.Shrink(msgs, 10)
			return true
		}
		return false
	}, sleep)
}

// call is one HTTP request to the gateway.
func (g *Gateway) call(ctx context.Context, token string, msgs []llm.Message, tools []llm.Tool, onText func(string), task string, avoid []string) (res *llm.Result, rerr error) {
	// v0.19.7: every attempt goes into the turn's "What happened" log (TurnFrom(ctx).Steps).
	st := Step{Started: time.Now()}
	defer func() {
		st.Seconds = time.Since(st.Started).Seconds()
		if rerr != nil {
			st.Error = rerr.Error()
			var he *llm.HTTPError
			if errors.As(rerr, &he) && st.Status == 0 {
				st.Status = he.Code
			}
		} else if res != nil {
			st.Finish = res.Finish
			if res.Usage != nil {
				st.TokensIn, st.TokensOut = res.Usage.Prompt, res.Usage.Completion
			}
		}
		TurnFrom(ctx).Record(st)
	}()
	if onText != nil {
		inner := onText
		onText = func(t string) {
			if st.ClientTTFTms == 0 {
				st.ClientTTFTms = int(time.Since(st.Started).Milliseconds())
			}
			inner(t)
		}
	}
	body := map[string]any{"messages": json.RawMessage(llm.Wire(msgs, true, false))}
	Tag(body, task)
	if onText != nil {
		body["stream"] = true
	}
	if len(avoid) > 0 {
		body["blaze_avoid"] = avoid
	}
	if len(tools) > 0 {
		body["tools"] = llm.AdaptTools(tools, "")
		body["tool_choice"] = llm.ToolChoice(ctx)
		body["blaze_tools"] = "auto" // gateway 0.6.9 drops them on turns with no action intent
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", g.Base+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, &llm.HTTPError{Code: 0, Body: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Blaze-Desktop")
	if onText != nil {
		req.Header.Set("Accept", "text/event-stream")
		// No compression on the stream: a gzip layer can hold back the first words until a
		// block fills (v0.19.7).
		req.Header.Set("Accept-Encoding", "identity")
	}
	turn := TurnFrom(ctx)
	turn.Apply(req.Header) // v0.19.1: X-Blaze-Turn / X-Blaze-Prefer
	g.mu.Lock()
	g.lastRoute = ""
	g.mu.Unlock()
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return nil, ctx.Err()
		}
		return nil, &llm.HTTPError{Code: 0, Body: "couldn't reach Blaze Cloud: " + err.Error()}
	}
	defer resp.Body.Close()
	prov, model := resp.Header.Get("X-Blaze-Provider"), resp.Header.Get("X-Blaze-Model")
	st.Provider, st.Model, st.Status = prov, model, resp.StatusCode
	st.TTFBms, _ = strconv.Atoi(resp.Header.Get("X-Blaze-TTFB-Ms"))
	st.Hedged = resp.Header.Get("X-Blaze-Hedged") != ""
	st.FastLane = resp.Header.Get("X-Blaze-Fast-Lane") == "1"
	st.Fallback = resp.Header.Get("X-Blaze-Fallback") == "1"
	quota := QuotaFrom(resp.Header)
	st.RequestID = resp.Header.Get("X-Request-Id")
	st.ServerTTFTms, _ = strconv.Atoi(resp.Header.Get("X-Blaze-TTFT-Ms"))
	st.ServerTiming = resp.Header.Get("Server-Timing")
	st.Lane = resp.Header.Get("X-Blaze-Lane")
	st.ToolsDropped = resp.Header.Get("X-Blaze-Tools-Dropped") == "1"
	st.Replay = resp.Header.Get("X-Blaze-Replay") != ""
	if prov != "" && model != "" {
		g.mu.Lock()
		g.lastRoute = prov + "/" + model
		g.mu.Unlock()
	}
	if resp.StatusCode >= 300 {
		g.noteQuota(quota, 0) // a 429 still says what's left
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
		ra, _ := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
		return nil, &llm.HTTPError{Code: resp.StatusCode, Body: gatewayErr(raw), RetryAfter: ra}
	}
	stall := g.Stall
	if stall <= 0 {
		stall = 90 * time.Second
	}
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(stall)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-alive:
				if !t.Stop() {
					select {
					case <-t.C:
					default:
					}
				}
				t.Reset(stall)
			case <-t.C:
				cancel()
				return
			}
		}
	}()
	tick := func() {
		select {
		case alive <- struct{}{}:
		default:
		}
	}
	var r *llm.Result
	ct := resp.Header.Get("Content-Type")
	if strings.Contains(ct, "event-stream") {
		r, err = llm.ParseStream(resp.Body, onText, tick)
	} else {
		raw, rerr := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
		if rerr != nil {
			return nil, &llm.HTTPError{Code: 0, Body: rerr.Error()}
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			// An empty 200 is a hiccup: the normal retry path takes over.
			return nil, &llm.HTTPError{Code: 503, Body: "Blaze Cloud sent an empty reply"}
		}
		r, err = llm.ParseWhole(raw, onText)
	}
	if err != nil {
		var he *llm.HTTPError
		if errors.As(err, &he) {
			return nil, he
		}
		if ctx.Err() != nil && errors.Is(ctx.Err(), context.Canceled) {
			return nil, &llm.HTTPError{Code: 0, Body: "Blaze Cloud stopped sending (no data for " + stall.String() + ")"}
		}
		return nil, &llm.HTTPError{Code: 0, Body: err.Error()}
	}
	r.Headers = llm.RateHeaders(resp.Header)
	if quota != nil {
		toks := 0
		if r.Usage != nil {
			toks = r.Usage.Prompt + r.Usage.Completion
		}
		g.noteQuota(quota, toks)
	}
	if prov != "" && model != "" {
		turn.Served(prov + "/" + model)
	}
	// Blaze Cloud is one product, like on Blaze Mobile: the person never sees which provider
	// or model answered (v0.19.1). The served route stays in the turn log (turn.Served) and in
	// lastRoute, so blaze_avoid and diagnostics still work.
	r.Provider = ProviderID
	r.Model = Model
	r.Route = Name
	return r, nil
}

// gatewayErr is the gateway's error as "<code> <message>" so Access sees the code too.
func gatewayErr(raw []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Error.Message != "" {
		if c, ok := e.Error.Code.(string); ok && c != "" {
			return c + " " + e.Error.Message
		}
		return e.Error.Message
	}
	return llm.CleanErr(string(raw))
}

// Status is the account's standing with Blaze Cloud, for Settings › Models.
type Status struct {
	Ready bool   `json:"ready"`          // signed in and allowed
	Line  string `json:"line,omitempty"` // what to tell the person when not ready
	Code  int    `json:"code,omitempty"`
}

// Check asks the gateway whether this account can use it (GET /v1/models; signing in there
// also puts a new account on the waiting list when approval is required).
func (g *Gateway) Check(ctx context.Context) Status {
	token := ""
	if g.Token != nil {
		token = strings.TrimSpace(g.Token())
	}
	if token == "" {
		return Status{Line: SignedOut, Code: 401}
	}
	req, err := http.NewRequestWithContext(ctx, "GET", g.Base+"/models", nil)
	if err != nil {
		return Status{Line: Unreachable}
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", "Blaze-Desktop")
	hc := g.HC
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		return Status{Line: Unreachable}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
	if resp.StatusCode < 300 {
		return Status{Ready: true}
	}
	return Status{Line: Friendly(resp.StatusCode, gatewayErr(raw)), Code: resp.StatusCode}
}
