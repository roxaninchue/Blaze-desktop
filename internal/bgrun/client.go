// Package bgrun is "ack now, work in background, report when done" (v0.19.14) on the desktop:
// the Blaze gateway's Cloud runs (POST /v1/runs, gateway 0.8.38 RUNS_ACK_CONTRACT) and the same
// behaviour for local / own-key turns.
//
//   - Create answers at once with an ack: a template line ("Got it, setting that reminder for
//     9:00 tomorrow. I'll confirm when it's set.") for action requests, nothing for greetings,
//     thanks, small talk and plain questions. The chat shows the line as a plain Blaze bubble and
//     never edits it; it never says the work is done.
//   - The run keeps going on the gateway even when the window closes. The desktop follows it,
//     answers tool calls only this computer can run (needs_device), and on start-up picks up runs
//     that finished (or wait for it) while it was away.
//   - It ends in exactly one final: done (the reply, with a short "Done ✓ …" confirmation once
//     any device tool came back OK) or failed (an honest reason and what to try).
//   - No "working" notices. When the window isn't focused as the final lands: exactly one desktop
//     notification, worded like a text from Blaze. Nothing when it is focused.
//
// Everything here is plain Go with small interfaces, so it is tested with fakes (no network).
package bgrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Ack is the gateway's instant acknowledgement (null for greetings, thanks, chat and questions).
type Ack struct {
	Intent  string         `json:"intent"`
	Line    string         `json:"line"`
	Details map[string]any `json:"details,omitempty"`
}

// CreateReq is the body of POST /v1/runs.
type CreateReq struct {
	ClientMsgID string          `json:"client_msg_id"`
	TZ          string          `json:"tz"` // IANA zone of the user, on every create
	Messages    json.RawMessage `json:"messages"`
	Tools       any             `json:"tools,omitempty"`
	Task        string          `json:"task,omitempty"`
	ToolChoice  any             `json:"tool_choice,omitempty"`
}

// Created is the 202 (or 200 + existing for a known client_msg_id).
type Created struct {
	RunID       string `json:"run_id"`
	ClientMsgID string `json:"client_msg_id"`
	Status      string `json:"status"`
	Existing    bool   `json:"existing"`
	Ack         *Ack   `json:"ack"`
	// Delivery (gateway 0.8.45, STEPS_DELIVERY_CONTRACT.md): received = the message is stored
	// durably (Sent); read != null = a worker already picked it up (Read). Read only through
	// internal/runevents.
	Received *Receipt0 `json:"received,omitempty"`
	Read     *Receipt0 `json:"read,omitempty"`
}

// Receipt0 is a delivery receipt (received / read).
type Receipt0 struct {
	MessageID   string `json:"message_id,omitempty"`
	ClientMsgID string `json:"client_msg_id,omitempty"`
	AtMs        int64  `json:"at_ms,omitempty"`
}

// ToolCall is one pending call the gateway wants this computer to run.
type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// ToolResult answers one pending call.
type ToolResult struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
}

// Receipt is the done event's confirmation.
type Receipt struct {
	Intent  string         `json:"intent"`
	Details map[string]any `json:"details,omitempty"`
	Summary string         `json:"summary"`
}

// Event is one entry of a run's event log. Only the fields the desktop reads are kept: provider
// and model are deliberately not decoded (Cloud mode never shows them).
type Event struct {
	Seq     int      `json:"seq"`
	Type    string   `json:"type"`
	Status  string   `json:"status,omitempty"`
	Step    int      `json:"step,omitempty"`
	Text    string   `json:"text,omitempty"`  // delta
	Reply   string   `json:"reply,omitempty"` // done
	Receipt *Receipt `json:"receipt,omitempty"`
	Code    string   `json:"code,omitempty"`   // failed / error
	Reason  string   `json:"reason,omitempty"` // failed
	Try     string   `json:"try,omitempty"`    // failed
	// Message is a string on the older error event but an object ({role, content}) on the message
	// event, so it is kept raw (a strict string field failed to decode every view after a reply).
	Message json.RawMessage `json:"message,omitempty"`
	Line    string          `json:"line,omitempty"`   // ack
	Intent  string          `json:"intent,omitempty"` // ack
	Kind    string          `json:"kind,omitempty"`   // step: step | specialty (gateway 0.8.45)
	// specialty_loaded / specialty_updated / step(kind specialty): what the help is for, when the
	// gateway sends it (the pill shows it in plain words); Name / OwnerName are the specialty's
	// names, used only to keep them out of the pill.
	Detail    string `json:"detail,omitempty"`
	Task      string `json:"task,omitempty"`
	Name      string `json:"name,omitempty"`
	OwnerName string `json:"_name,omitempty"`
}

// MessageText is the error event's message ("" when it isn't a string).
func (e Event) MessageText() string {
	var s string
	if len(e.Message) > 0 && json.Unmarshal(e.Message, &s) == nil {
		return s
	}
	return ""
}

// RunError is the view's error for a failed run.
type RunError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// View is GET /v1/runs/{id}.
type View struct {
	ID          string     `json:"id"`
	ClientMsgID string     `json:"client_msg_id"`
	Status      string     `json:"status"`
	Events      []Event    `json:"events"`
	NextAfter   int        `json:"next_after"`
	More        bool       `json:"more"`
	Pending     []ToolCall `json:"pending_tool_calls,omitempty"`
	Reply       string     `json:"reply,omitempty"`
	Error       *RunError  `json:"error,omitempty"`
	RetryInMs   int        `json:"retry_in_ms,omitempty"`
}

// Summary is one row of GET /v1/runs.
type Summary struct {
	ID          string `json:"id"`
	ClientMsgID string `json:"client_msg_id"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at,omitempty"`
}

// Statuses.
const (
	StQueued      = "queued"
	StRunning     = "running"
	StNeedsDevice = "needs_device"
	StDone        = "done"
	StFailed      = "failed"
	StCancelled   = "cancelled"
	StExpired     = "expired"
)

// Terminal is true for a run that will not change any more.
func Terminal(st string) bool {
	return st == StDone || st == StFailed || st == StCancelled || st == StExpired
}

// API is the gateway's runs surface. HTTP implements it; tests use a fake.
type API interface {
	Create(ctx context.Context, req CreateReq) (*Created, error)
	Get(ctx context.Context, id string, after int) (*View, error)
	List(ctx context.Context) ([]Summary, error)
	ToolResults(ctx context.Context, id string, res []ToolResult) error
	Delete(ctx context.Context, id string) error
}

// HTTPError is a non-2xx answer from the gateway.
type HTTPError struct {
	Code int
	Body string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("gateway %d: %s", e.Code, e.Body) }

// StatusOf is the HTTP status of err (0 for a network error or anything else).
func StatusOf(err error) int {
	var he *HTTPError
	if errors.As(err, &he) {
		return he.Code
	}
	return 0
}

// HTTP talks to the gateway. Base ends in /v1.
type HTTP struct {
	Base  string
	Token func() string
	HC    *http.Client
}

func (h *HTTP) do(ctx context.Context, method, path string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(h.Base, "/")+path, rd)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	tok := ""
	if h.Token != nil {
		tok = strings.TrimSpace(h.Token())
	}
	if tok == "" {
		return &HTTPError{Code: 401, Body: "signed out"}
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", "Blaze-Desktop")
	hc := h.HC
	if hc == nil {
		hc = &http.Client{Timeout: 30 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode >= 300 {
		return &HTTPError{Code: resp.StatusCode, Body: clip(string(raw), 400)}
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Create is POST /v1/runs.
func (h *HTTP) Create(ctx context.Context, req CreateReq) (*Created, error) {
	var c Created
	if err := h.do(ctx, "POST", "/runs", req, &c); err != nil {
		return nil, err
	}
	if c.RunID == "" {
		return nil, &HTTPError{Code: 502, Body: "no run_id in the answer"}
	}
	return &c, nil
}

// Get is GET /v1/runs/{id}?after=.
func (h *HTTP) Get(ctx context.Context, id string, after int) (*View, error) {
	var v View
	err := h.do(ctx, "GET", "/runs/"+url.PathEscape(id)+"?after="+strconv.Itoa(after), nil, &v)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// List is GET /v1/runs.
func (h *HTTP) List(ctx context.Context) ([]Summary, error) {
	var out struct {
		Runs []Summary `json:"runs"`
	}
	if err := h.do(ctx, "GET", "/runs", nil, &out); err != nil {
		return nil, err
	}
	return out.Runs, nil
}

// ToolResults is POST /v1/runs/{id}/tool_results.
func (h *HTTP) ToolResults(ctx context.Context, id string, res []ToolResult) error {
	return h.do(ctx, "POST", "/runs/"+url.PathEscape(id)+"/tool_results", map[string]any{"results": res}, nil)
}

// Delete is DELETE /v1/runs/{id} (the desktop has shown the final).
func (h *HTTP) Delete(ctx context.Context, id string) error {
	return h.do(ctx, "DELETE", "/runs/"+url.PathEscape(id), nil, nil)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
