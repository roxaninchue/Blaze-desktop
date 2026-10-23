// Package llm talks to any OpenAI-compatible chat endpoint with streaming and
// tool calls, and routes each turn across the user's models (see router.go).
package llm

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
)

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
	// Provider extras that must be sent back unchanged (Gemini 3 thought signatures live here).
	ExtraContent json.RawMessage `json:"extra_content,omitempty"`
}

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
	Attach     []Attach   `json:"attach,omitempty"` // what the user attached (attach.go); images are sent as image parts
}

type Tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string         `json:"name"`
		Description string         `json:"description"`
		Parameters  map[string]any `json:"parameters"`
	} `json:"function"`
}

type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
}

type Result struct {
	Text      string
	ToolCalls []ToolCall
	Route     string // "Groq · model"
	Provider  string
	Model     string
	Note      string // e.g. "Gemini was rate limited, Groq answered"
	Keyless   bool   // answered by the keyless model, without tools
	Usage     *Usage
	Headers   map[string]string // x-ratelimit-* of the reply
	Repairs   []string          // what the tool-call gatekeeper fixed
	Finish    string            // the provider's finish_reason: "stop", "length", "tool_calls"…
	Cut       bool              // the stream broke off before the reply ended
}

// Truncated reports a reply the model didn't get to finish: it hit its output-token
// limit, or the stream broke off. The app offers Continue for these.
func (r *Result) Truncated() bool {
	return r != nil && (r.Cut || r.Finish == "length" || r.Finish == "max_tokens")
}

type HTTPError struct {
	Code       int
	Body       string
	RetryAfter int // seconds, from the Retry-After header
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Code, e.Body) }

var client = &http.Client{Transport: &http.Transport{
	Proxy:                 http.ProxyFromEnvironment,
	DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
	TLSHandshakeTimeout:   15 * time.Second,
	ResponseHeaderTimeout: 120 * time.Second,
	IdleConnTimeout:       90 * time.Second,
	MaxIdleConnsPerHost:   4,
}}

// stallTimeout is the longest wait for the next streamed chunk before giving up.
var stallTimeout = 120 * time.Second

// AuthHeaders for a provider's key.
func AuthHeaders(pid, key string) map[string]string {
	h := map[string]string{}
	if key == "" {
		return h
	}
	h["Authorization"] = "Bearer " + key
	if pid == "anthropic" {
		h["x-api-key"] = key
		h["anthropic-version"] = "2023-06-01"
	}
	return h
}

// Chat does one streamed completion against one provider/model.
func Chat(ctx context.Context, p config.Provider, key, model string, msgs []Message, tools []Tool, onText func(string)) (*Result, error) {
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, &HTTPError{Code: 0, Body: "no service address set for " + p.Name}
	}
	if model == "" {
		return nil, &HTTPError{Code: 404, Body: "no model chosen for " + p.Name}
	}
	if owner := KeyOwner(key); owner != "" && owner != p.ID && p.ID != config.Custom && !config.Keyless(p.ID) {
		return nil, &HTTPError{Code: 401, Body: fmt.Sprintf("the key saved under %s is a %s key", p.Name, config.Name(owner))}
	}
	body := map[string]any{"model": model, "messages": wire(KeepRecentImages(msgs, 3), Info(p.ID, model).Vision, IsLocal(p)), "stream": true}
	if len(tools) > 0 {
		body["tools"] = AdaptTools(tools, p.ID)
		body["tool_choice"] = ToolChoice(ctx)
	}
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(p.BaseURL, "/")+"/chat/completions", bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	for k, v := range AuthHeaders(p.ID, key) {
		req.Header.Set(k, v)
	}
	if p.ID == "openrouter" {
		req.Header.Set("HTTP-Referer", "https://blazeinchue.pages.dev")
		req.Header.Set("X-Title", "Blaze Desktop")
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	hdr := rateHeaders(resp.Header)
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8000))
		ra, _ := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After")))
		return &Result{Headers: hdr}, &HTTPError{resp.StatusCode, cleanErr(string(raw)), ra}
	}
	// Watchdog: a stream that goes quiet for too long is cancelled.
	alive := make(chan struct{}, 1)
	go func() {
		t := time.NewTimer(stallTimeout)
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
				t.Reset(stallTimeout)
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
	var res *Result
	if strings.Contains(resp.Header.Get("Content-Type"), "event-stream") || !strings.Contains(resp.Header.Get("Content-Type"), "json") {
		res, err = ParseStream(resp.Body, onText, tick)
	} else {
		res, err = parseWhole(resp.Body, onText)
	}
	if res != nil {
		res.Headers = hdr
	}
	if err != nil && ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return res, &HTTPError{Code: 0, Body: "stopped sending (no data for " + stallTimeout.String() + ")"}
	}
	return res, err
}

func rateHeaders(h http.Header) map[string]string {
	out := map[string]string{}
	for k, v := range h {
		lk := strings.ToLower(k)
		if strings.Contains(lk, "ratelimit") && len(v) > 0 {
			out[lk] = v[0]
		}
	}
	return out
}

// ---------------- stream parsing ----------------

type rawCall struct {
	Index    *int            `json:"index"`
	ID       string          `json:"id"`
	Type     string          `json:"type"`
	Function json.RawMessage `json:"function"`
	Name     string          `json:"name"`      // a few servers put these at the top level
	Args     json.RawMessage `json:"arguments"` // ditto
	Extra    json.RawMessage `json:"extra_content"`
}

type rawFn struct {
	Name *string         `json:"name"`
	Args json.RawMessage `json:"arguments"`
}

// argText turns an arguments field (a JSON string, an object, or null) into text.
func argText(r json.RawMessage) string {
	t := bytes.TrimSpace(r)
	if len(t) == 0 || string(t) == "null" {
		return ""
	}
	if t[0] == '"' {
		var s string
		if json.Unmarshal(t, &s) == nil {
			return s
		}
		return ""
	}
	return string(t) // an object sent as-is (some Gemini/Ollama replies)
}

type accCall struct {
	id, name string
	args     strings.Builder
	extra    json.RawMessage
}

// streamAcc stitches streamed tool-call fragments back together.
//
// Providers disagree on how they stream calls. OpenAI sends the id + name once,
// then argument fragments under the same index. Gemini's OpenAI layer sends each
// call whole in its own chunk, often every one with index 0 or no index at all,
// sometimes with arguments as an object. So a fragment that brings a new id, or
// a name for a slot whose call already has a name and complete arguments, starts
// a new call instead of being glued onto the previous one.
type streamAcc struct {
	calls  []*accCall
	slotOf map[int]int
}

func (s *streamAcc) add(pos int, rc rawCall) {
	if s.slotOf == nil {
		s.slotOf = map[int]int{}
	}
	idx := pos
	if rc.Index != nil {
		idx = *rc.Index
	}
	var fn rawFn
	if len(rc.Function) > 0 {
		_ = json.Unmarshal(rc.Function, &fn)
	}
	name := ""
	if fn.Name != nil {
		name = *fn.Name
	} else if rc.Name != "" {
		name = rc.Name
	}
	argsRaw := fn.Args
	if len(argsRaw) == 0 {
		argsRaw = rc.Args
	}
	args := argText(argsRaw)

	slot, ok := s.slotOf[idx]
	if ok {
		cur := s.calls[slot]
		idChanged := rc.ID != "" && cur.id != "" && rc.ID != cur.id
		nameRestart := name != "" && cur.name != "" && completeJSON(cur.args.String())
		if idChanged || nameRestart {
			ok = false
		}
	} else if rc.Index == nil && rc.ID == "" && name == "" && len(s.calls) > 0 {
		// An index-less continuation fragment: it belongs to the last call.
		slot, ok = len(s.calls)-1, true
	}
	if !ok {
		slot = len(s.calls)
		s.calls = append(s.calls, &accCall{})
		s.slotOf[idx] = slot
	}
	c := s.calls[slot]
	if rc.ID != "" {
		c.id = rc.ID
	}
	// Some servers repeat the full name on every fragment; don't double it.
	if name != "" && name != c.name {
		c.name += name
	}
	c.args.WriteString(args)
	if len(rc.Extra) > 0 && string(rc.Extra) != "null" {
		c.extra = append(json.RawMessage(nil), rc.Extra...)
	}
}

// calls returns the stitched calls. Calls without a name are dropped here; argument
// repair and name resolution happen in NormalizeCalls.
func (s *streamAcc) result() []ToolCall {
	var out []ToolCall
	seen := map[string]bool{}
	for i, c := range s.calls {
		if strings.TrimSpace(c.name) == "" {
			continue
		}
		var tc ToolCall
		tc.Type = "function"
		tc.ID = c.id
		if tc.ID == "" {
			tc.ID = fmt.Sprintf("call_%d", i)
		}
		if seen[tc.ID] {
			tc.ID = fmt.Sprintf("%s_%d", tc.ID, i)
		}
		seen[tc.ID] = true
		tc.Function.Name = strings.TrimSpace(c.name)
		tc.Function.Arguments = c.args.String()
		tc.ExtraContent = c.extra
		out = append(out, tc)
	}
	return out
}

type chunk struct {
	Choices []struct {
		Delta        *delta  `json:"delta"`
		Message      *delta  `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage *Usage          `json:"usage"`
	Error json.RawMessage `json:"error"`
}

type delta struct {
	Content   *string         `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

// ParseStream reads an SSE chat stream. tick (optional) is called on every line.
func ParseStream(r io.Reader, onText func(string), tick func()) (*Result, error) {
	res := &Result{}
	acc := &streamAcc{}
	var text strings.Builder
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if !strings.HasPrefix(line, "data:") {
			continue // keep-alive comments and blank lines are not progress (live 0.19.14: a
			// stream that only pinged kept a turn waiting for minutes)
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var ch chunk
		if json.Unmarshal([]byte(data), &ch) != nil {
			continue
		}
		if tick != nil && chunkProgress(ch, data) {
			tick() // only real output resets the stall watchdog, never an empty heartbeat chunk
		}
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			msg := cleanErr(`{"error":` + string(ch.Error) + `}`)
			code := 500
			var e struct {
				Code any `json:"code"`
			}
			if json.Unmarshal(ch.Error, &e) == nil {
				switch v := e.Code.(type) {
				case float64:
					code = int(v)
				case string:
					if n, err := strconv.Atoi(v); err == nil {
						code = n
					}
				}
			}
			if code < 400 {
				code = 500
			}
			return res, &HTTPError{Code: code, Body: msg}
		}
		if ch.Usage != nil {
			res.Usage = ch.Usage
		}
		for _, c := range ch.Choices {
			if c.FinishReason != nil && *c.FinishReason != "" {
				res.Finish = *c.FinishReason
			}
			d := c.Delta
			if d == nil {
				d = c.Message
			}
			if d == nil {
				continue
			}
			if d.Content != nil && *d.Content != "" {
				text.WriteString(*d.Content)
				if onText != nil {
					onText(*d.Content)
				}
			}
			if len(d.ToolCalls) > 0 && string(d.ToolCalls) != "null" {
				// Decode element by element so one odd fragment never drops the whole chunk.
				var arr []json.RawMessage
				if json.Unmarshal(d.ToolCalls, &arr) != nil {
					continue
				}
				for i, el := range arr {
					var rc rawCall
					if json.Unmarshal(el, &rc) != nil {
						continue
					}
					acc.add(i, rc)
				}
			}
		}
	}
	err := sc.Err()
	res.Text = text.String()
	res.ToolCalls = acc.result()
	if err != nil && !errors.Is(err, io.EOF) {
		if res.Text == "" && len(res.ToolCalls) == 0 {
			return nil, err
		}
		// Cut off mid-stream: keep what arrived, but a half-written call is not safe to run.
		res.Cut = true
		var keep []ToolCall
		for _, c := range res.ToolCalls {
			if completeJSON(c.Function.Arguments) || strings.TrimSpace(c.Function.Arguments) == "" {
				keep = append(keep, c)
			}
		}
		res.ToolCalls = keep
	}
	return res, nil
}

// chunkProgress is a stream chunk that carries output: text, a tool call, a finish, usage, an
// error or reasoning. An empty delta ({"delta":{}}) or a role-only one is a heartbeat.
func chunkProgress(ch chunk, raw string) bool {
	if ch.Usage != nil || (len(ch.Error) > 0 && string(ch.Error) != "null") || strings.Contains(raw, `"reasoning`) {
		return true
	}
	for _, c := range ch.Choices {
		if c.FinishReason != nil && *c.FinishReason != "" {
			return true
		}
		d := c.Delta
		if d == nil {
			d = c.Message
		}
		if d != nil && ((d.Content != nil && *d.Content != "") || (len(d.ToolCalls) > 0 && string(d.ToolCalls) != "null")) {
			return true
		}
	}
	return false
}

func parseWhole(r io.Reader, onText func(string)) (*Result, error) {
	raw, err := io.ReadAll(io.LimitReader(r, 32<<20))
	if err != nil {
		return nil, err
	}
	// Re-use the stream parser by wrapping the single object as one SSE event.
	return ParseStream(strings.NewReader("data: "+string(bytes.ReplaceAll(raw, []byte("\n"), nil))+"\n"), onText, nil)
}

func cleanErr(s string) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &e) == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	var arr []struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &arr) == nil && len(arr) > 0 && arr[0].Error.Message != "" {
		return arr[0].Error.Message
	}
	var e2 struct {
		Error string `json:"error"`
	}
	if json.Unmarshal([]byte(s), &e2) == nil && e2.Error != "" {
		return e2.Error
	}
	if len(s) > 300 {
		s = s[:300]
	}
	return strings.TrimSpace(s)
}

// KeyOwner names the provider a key belongs to when its prefix is unmistakable.
func KeyOwner(key string) string {
	k := strings.TrimSpace(key)
	switch {
	case strings.HasPrefix(k, "nvapi-"):
		return "nvidia"
	case strings.HasPrefix(k, "gsk_"):
		return "groq"
	case strings.HasPrefix(k, "sk-or-"):
		return "openrouter"
	case strings.HasPrefix(k, "AIza"):
		return "gemini"
	case strings.HasPrefix(k, "sk-ant-"):
		return "anthropic"
	case strings.HasPrefix(k, "xai-"):
		return "xai"
	case strings.HasPrefix(k, "csk-"):
		return "cerebras"
	}
	return ""
}
