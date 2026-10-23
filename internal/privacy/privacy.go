// Package privacy keeps what powers an answer out of what the user sees (0.19.16).
//
// Blaze is one assistant to the user: the providers and models behind an answer (Blaze Cloud's
// upstreams, or the routing between them) are never named in the chat chrome: footers, the model
// pill, error cards, fallback notices, status lines, working pills or the "What happened" trace.
// They show only behind an explicit developer flag (BLAZE_DEV=1 / BLAZE_DEBUG=1).
//
// The reply text itself is never touched: a user who asks about a model gets the answer.
// "Google" alone is left alone (it is a connector the user signs in to); Google's model names
// (gemini, gemma, google/...) are caught.
package privacy

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
)

// Names is every provider or model family the chrome must never show (lower case).
var Names = []string{
	"groq", "cerebras", "sambanova", "cohere", "mistral", "mixtral", "codestral", "gemini", "gemma", "google ai", "google/",
	"openrouter", "nvidia", "nim", "z.ai", "zhipu", "glm", "llama", "meta-llama", "qwen", "deepseek", "gpt", "openai",
	"anthropic", "claude", "kimi", "moonshot", "huggingface", "hugging face", "together.ai", "fireworks", "deepinfra",
	"ollama", "pollinations", "phi-", "command-r", "grok", "xai", "x.ai", "perplexity", "sonar", "minimax", "nemotron",
}

// One alternation, longest first; a model family swallows its whole id ("llama-3.3-70b-versatile").
var re = func() *regexp.Regexp {
	parts := make([]string, 0, len(Names))
	for _, n := range Names {
		q := regexp.QuoteMeta(n)
		if strings.HasSuffix(n, "/") || strings.HasSuffix(n, "-") {
			parts = append(parts, q+`[\w.:/+-]*`)
			continue
		}
		parts = append(parts, q+`(?:\d[\w.:/+-]*|[-_/:.][\w.:/+-]*)?`)
	}
	return regexp.MustCompile(`(?i)(?:^|\b)(?:` + strings.Join(parts, "|") + `)\b`)
}()

// Leaks reports whether s names a provider or model.
func Leaks(s string) bool { return re.MatchString(s) }

// Found returns the names s leaks (lower case), for test messages.
func Found(s string) []string {
	var out []string
	for _, m := range re.FindAllString(s, -1) {
		out = append(out, strings.ToLower(m))
	}
	return out
}

// Dev is the explicit developer flag that lets names through.
func Dev() bool {
	for _, k := range []string{"BLAZE_DEV", "BLAZE_DEBUG"} {
		if v := strings.TrimSpace(os.Getenv(k)); v == "1" || strings.EqualFold(v, "true") {
			return true
		}
	}
	return false
}

var spaces = regexp.MustCompile(`\s{2,}`)
var dupModel = regexp.MustCompile(`(?i)\b(the model)(?:[\s,/·|-]+the model\b)+`)

// Scrub replaces every provider or model name in a chrome line with "the model".
func Scrub(s string) string {
	if s == "" || !re.MatchString(s) {
		return s
	}
	out := re.ReplaceAllString(s, "the model")
	out = dupModel.ReplaceAllString(out, "$1")
	out = strings.ReplaceAll(out, "(the model)", "")
	out = spaces.ReplaceAllString(out, " ")
	return strings.TrimSpace(out)
}

// Route is the footer / model-pill label: kept when clean, else just "Blaze".
func Route(s string) string {
	if Leaks(s) {
		return "Blaze"
	}
	return s
}

// chrome lists the events whose strings are Blaze's own chrome (never the model's reply text).
var chrome = map[string]bool{
	"error": true, "route": true, "done": true, "status": true, "model": true, "trace": true, "stopped": true,
	"run_event": true, "thinking": true, "repair": true, "notify": true, "job": true,
}

// dropKeys never reach the screen, whatever the event: the gateway's done event carries them.
var dropKeys = map[string]bool{"provider": true, "model": true, "upstream": true, "provider_id": true, "model_id": true}

// textKeys are reply text (never scrubbed): a user asking about a model gets the answer.
var textKeys = map[string]bool{"text": true, "reply": true, "content": true, "delta": true}

// Event returns what an event may show on screen. Reply text (delta, blaze_msg done replies) is
// left as it is; chrome strings are scrubbed and provider/model fields dropped.
func Event(ev string, d any) any {
	if Dev() || d == nil {
		return d
	}
	if !chrome[ev] {
		if ev == "blaze_msg" {
			return blazeMsg(d)
		}
		return d
	}
	if s, ok := d.(string); ok {
		return Scrub(s)
	}
	b, err := json.Marshal(d)
	if err != nil {
		return d
	}
	var v any
	if json.Unmarshal(b, &v) != nil {
		return d
	}
	return walk(v, "")
}

func walk(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if dropKeys[strings.ToLower(k)] {
				delete(t, k)
				continue
			}
			t[k] = walk(x, strings.ToLower(k))
		}
		return t
	case []any:
		for i, x := range t {
			t[i] = walk(x, key)
		}
		return t
	case string:
		switch key {
		case "route", "label", "name":
			return Route(t)
		case "note":
			if Leaks(t) {
				return "" // "X didn't answer, so Y did": never shown
			}
			return t
		}
		if textKeys[key] {
			return t
		}
		return Scrub(t)
	}
	return v
}

// blazeMsg: a background final. A done reply is the model's text (left alone); a failed one is
// Blaze's own line about the run, scrubbed.
func blazeMsg(d any) any {
	m, ok := d.(map[string]any)
	if !ok {
		return d
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		if dropKeys[k] {
			continue
		}
		out[k] = v
	}
	if k, _ := out["kind"].(string); k != "" && k != "done" {
		if s, ok := out["text"].(string); ok {
			out["text"] = Scrub(s)
		}
	}
	return out
}

// ModelLabel is the model pill's label: "Blaze" unless the developer flag is on.
func ModelLabel(s string) string {
	if Dev() {
		return s
	}
	return Route(s)
}

// Owner names: Blaze is made by Blazeinchue; no person is named as its maker (0.19.16).
var ownerRe = regexp.MustCompile(`(?i)\b(?:luke|lackson|kapapa\w*)\b`)

// Maker is who makes Blaze, wherever the app says so.
const Maker = "Blazeinchue"

// LeaksOwner reports whether s names the owner.
func LeaksOwner(s string) bool { return ownerRe.MatchString(s) }

// Owner questions (0.19.16 guard): a reply to "who made/owns Blaze" or "what's the owner's name"
// always says Blazeinchue and never names a person, whatever the model wrote.
const subj = `(?:the\s+)?(?:blaze(?:\s+app)?|you|this\s+(?:app|assistant|ai|bot))`

var (
	// "who made Blaze", "who really created you", "who is behind Blaze", "who's running this app"
	askWhoRe = regexp.MustCompile(`(?i)\bwho(?:'s|’s|\s+is|\s+are|\s+was|\s+were|\s+exactly|\s+actually|\s+really|\s+originally|\s+first)*\s+(?:owns?|made|makes|created|creates|built|builds|developed|develops|founded|wrote|programmed|designed|coded|runs|is\s+behind|are\s+behind|was\s+behind|behind)\s+` + subj + `\b(?:\s+(\w+))?`)
	// "which company makes Blaze", "what team built you"
	askWhichRe = regexp.MustCompile(`(?i)\b(?:which|what)\s+(?:company|team|person|people|studio|business|developer|organi[sz]ation)\s+(?:\w+\s+)?(?:owns?|made|makes|created|built|developed|is\s+behind|are\s+behind)\s+` + subj + `\b`)
	// "were you made by", "is Blaze owned by"
	askByRe = regexp.MustCompile(`(?i)\b(?:(?:were|are|was|is)\s+` + subj + `\s+(?:made|created|built|developed|owned|designed|programmed)|` + subj + `\s+(?:was|were|is|are)\s+(?:made|created|built|developed|owned|designed|programmed))\s+by\b[^.!]*\?`)
	// "Blaze's owner", "your creator", "the owner of Blaze", "the person who owns Blaze" (asked)
	ownerNounRe = regexp.MustCompile(`(?i)\b(?:blaze(?:'s|’s)?|your)\s+(?:real\s+|actual\s+)?(?:owners?|makers?|creators?|developers?|founders?|authors?)\b|\b(?:owners?|makers?|creators?|developers?|founders?|authors?|people|person|company|team)\s+(?:of|behind|who\s+(?:owns?|made|created|built|develops?|developed|runs?))\s+` + subj + `\b`)
	questionRe  = regexp.MustCompile(`(?i)\?|^\s*(?:who|what|whats|which|tell|give|name|do\s+you\s+know|can\s+you\s+tell)\b|\bname\b`)
	notOwnerRe  = map[string]bool{"a": true, "an": true, "some": true, "this": true, "that": true, "my": true, "feel": true, "do": true, "laugh": true, "cry": true, "think": true, "want": true, "say": true, "so": true, "angry": true, "sad": true, "happy": true}
	dupMakerRe  = regexp.MustCompile(`(?i)\bblazeinchue(?:[\s,.-]+blazeinchue)+\b`)
)

// AsksOwner reports whether the user's message asks who owns or made Blaze, or the owner's name.
func AsksOwner(q string) bool {
	if m := askWhoRe.FindStringSubmatch(q); m != nil {
		if !notOwnerRe[strings.ToLower(m[1])] {
			return true
		}
	}
	if askWhichRe.MatchString(q) || askByRe.MatchString(q) {
		return true
	}
	return ownerNounRe.MatchString(q) && questionRe.MatchString(q)
}

// OwnerNames puts Blazeinchue where a reply names the owner (Luke, Lackson, Kapapa).
func OwnerNames(s string) string {
	if !ownerRe.MatchString(s) {
		return s
	}
	return dupMakerRe.ReplaceAllString(ownerRe.ReplaceAllString(s, Maker), Maker)
}

// OwnerReply is the guard on a final reply: for an owner question it names no person and always
// says Blazeinchue; any other reply is left alone.
func OwnerReply(question, reply string) string {
	if !AsksOwner(question) {
		return reply
	}
	reply = OwnerNames(reply)
	if strings.Contains(strings.ToLower(reply), strings.ToLower(Maker)) {
		return reply
	}
	return "Blaze is made by " + Maker + ". " + strings.TrimLeft(reply, " \t\r\n")
}
