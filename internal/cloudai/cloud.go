// Package cloudai is Blaze Cloud on the desktop (v0.19): every model call goes through the
// Blaze gateway (one OpenAI-compatible endpoint, signed in with the Blaze account), with
// model "blaze-auto" and a per-request task hint. The gateway holds the provider keys and
// picks the model, so a default user needs no keys at all.
//
// It is a port of Blaze Android's agent/Cloud.kt (v0.25+), ThinkBudget.kt and EmptyReply.kt:
// the same task classification, retry policy, backoff and the same plain lines.
//
// The user can choose Custom instead (their own providers and keys, Settings › Models);
// route.go decides per request which way it goes.
package cloudai

import (
	"encoding/json"
	"regexp"
	"strings"

	"blaze-desktop/internal/llm"
)

// Model is the gateway's "pick for me" model; the task hint does the real choosing.
const Model = "blaze-auto"

// Tasks the gateway knows.
var Tasks = []string{"chat", "quick", "code", "vision", "long", "agent"}

// LongChars is about 12k tokens, the gateway's own "long" cut-off (gateway/src/router.ts LONG_CHARS).
const LongChars = 48_000

// MaxTries per request: the first + two quiet retries.
const MaxTries = 3

// The lines a person sees. Same words as the phone.
const (
	SignedOut   = "Sign in to your Blaze account and I'm ready to go."
	Expired     = "Your Blaze sign-in has expired. Sign in again and I'll carry on."
	Unreachable = "I couldn't reach Blaze Cloud just now. Check your internet connection, then Retry."
	Busy        = "Blaze Cloud is busy right now. Retry in a moment."
	Waitlisted  = "You're on the Blaze waiting list. You'll get access as soon as you're approved."
	Rejected    = "Your Blaze account doesn't have access to Blaze AI. If you think this is a mistake, contact the Blaze team."
	Allowance   = "You've used today's free messages. They come back tomorrow."
	EmptyFailed = "The AI came back without an answer, a few times in a row. Retry and I'll ask again."
)

// EmptyTries is how many empty replies one request tolerates (EmptyReply.TRIES).
const EmptyTries = 3

// EmptyCode is the Error code for "the model answered with nothing".
const EmptyCode = -2

// Nudge is added as a user turn when asking again after an empty reply.
const Nudge = "[Blaze] Your last reply came back empty (no answer text). Answer the user now in one or two sentences, " +
	"from what you already know and the tool results above. Don't call a tool unless the answer truly needs one."

// Error is a Blaze Cloud failure: Line is what the chat shows, Raw is the gateway's own text (details).
type Error struct {
	Line       string
	Code       int
	RetryAfter int
	Raw        string
}

func (e *Error) Error() string { return e.Line }

// PlainLine is the line the chat shows (llm.Plainer).
func (e *Error) PlainLine() string { return e.Line }

// Access is the waiting-list line for a gateway 403 (by error code, or a line already made plain), else "".
func Access(code int, text string) string {
	if code != 403 {
		return ""
	}
	t := strings.ToLower(text)
	switch {
	case strings.Contains(t, "access_rejected") || strings.Contains(t, "doesn't have access to blaze"):
		return Rejected
	case strings.Contains(t, "waitlisted") || strings.Contains(t, "waiting list"):
		return Waitlisted
	}
	return ""
}

var codeCue = regexp.MustCompile("(?i)(```|\\b(code|coding|function|bug|stack ?trace|compile|kotlin|python|javascript|typescript|java|rust|golang|sql|regex|script|refactor|debug)\\b)")

func lastUserText(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func userImages(msgs []llm.Message) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			n := 0
			for _, a := range msgs[i].Attach {
				if a.Kind == "image" {
					n++
				}
			}
			return n
		}
	}
	return 0
}

// TaskOf is the gateway task for a request whose wire messages are raw:
// vision > long > agent (tools) > code > quick (lite) > chat.
func TaskOf(raw []byte, lastUser string, hasTools, lite bool) string {
	switch {
	case hasImagePart(raw):
		return "vision"
	case len(raw) > LongChars:
		return "long"
	case hasTools:
		return "agent"
	case codeCue.MatchString(lastUser):
		return "code"
	case lite:
		return "quick"
	}
	return "chat"
}

func hasImagePart(raw []byte) bool {
	var arr []struct {
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &arr) != nil {
		return false
	}
	for _, m := range arr {
		var parts []struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(m.Content, &parts) != nil {
			continue
		}
		for _, p := range parts {
			if p.Type == "image_url" {
				return true
			}
		}
	}
	return false
}

// Task is the task hint for one request. quickCall marks the small background calls
// (titles, learning, summaries: mobile's liteTurn); for a plain chat turn without tools
// the phone's adaptive think budget decides whether it is "quick".
func Task(msgs []llm.Message, tools []llm.Tool, quickCall bool) string {
	raw := llm.Wire(msgs, true, false)
	last := lastUserText(msgs)
	lite := quickCall || (len(tools) == 0 && Level(last, userImages(msgs)) == Quick)
	return TaskOf(raw, last, len(tools) > 0, lite)
}

// Tag adds the task hint the gateway reads (blaze_task; it strips it before calling a provider).
func Tag(body map[string]any, task string) map[string]any {
	body["model"] = Model
	body["blaze_task"] = task
	return body
}

// ---------------- retry policy ----------------

var goneCues = []string{"not found", "model_not_found", "no longer available", "decommissioned", "unknown model",
	"invalid model", "doesn't offer that model", "isn't available", "paid_model", "paid-only"}

// Gone: the model went away or was refused; the gateway re-routes on the next try.
func Gone(t string) bool {
	t = strings.ToLower(t)
	for _, c := range goneCues {
		if strings.Contains(t, c) {
			return true
		}
	}
	return false
}

func allowance(t string) bool {
	t = strings.ToLower(t)
	return strings.Contains(t, "allowance") || strings.Contains(t, "quota_exceeded")
}

// Retryable: no answer (network, timeout: code 0), busy (429 other than the daily allowance),
// the gateway's side (5xx), or a model that went away / was refused (404, "model not found").
func Retryable(code int, text string) bool {
	if code == 401 || code == 403 {
		return false
	}
	if code == 429 && allowance(text) {
		return false
	}
	return code == 0 || code == 408 || code == 429 || (code >= 500 && code <= 599) || code == 404 || Gone(text)
}

// BackoffMs is the wait before try attempt+1 (0-based): 1 s, then 3 s, then 7 s; a server's
// Retry-After wins when it is 1..20 s.
func BackoffMs(attempt, retryAfterSec int) int64 {
	if retryAfterSec >= 1 && retryAfterSec <= 20 {
		return int64(retryAfterSec) * 1000
	}
	switch attempt {
	case 0:
		return 1_000
	case 1:
		return 3_000
	}
	return 7_000
}

// Friendly is the one line a person sees when every try failed: no model names, no settings.
func Friendly(code int, text string) string {
	if a := Access(code, text); a != "" {
		return a
	}
	switch {
	case code == 401 || code == 403:
		return Expired
	case code == 429 && allowance(text):
		return Allowance
	case code == 429 || (code >= 500 && code <= 599) || Gone(text) || code == 404:
		return Busy
	}
	return Unreachable
}

// ---------------- empty replies (EmptyReply.kt) ----------------

var (
	thinkTags  = regexp.MustCompile(`(?is)<(think|thought|thinking)>.*?(</(think|thought|thinking)>|$)`)
	followTags = regexp.MustCompile(`(?i)<{1,2}[\s/(\[]*follow[\s_-]?ups?\s*:[^>]*>{0,3}`)
)

// Visible is what a person would see of a reply's text.
func Visible(s string) string {
	return strings.TrimSpace(followTags.ReplaceAllString(thinkTags.ReplaceAllString(s, ""), ""))
}

// IsEmpty: no visible text and no tool calls. It never counts as an answer.
func IsEmpty(r *llm.Result) bool {
	return r == nil || (len(r.ToolCalls) == 0 && Visible(r.Text) == "")
}

// WithNudge is msgs plus the nudge as a user turn.
func WithNudge(msgs []llm.Message) []llm.Message {
	return append(append([]llm.Message(nil), msgs...), llm.Message{Role: "user", Content: Nudge})
}
