package llm

// Plain-English errors (ported from the Android app's PlainError.kt): raw provider
// errors (JSON blobs, URLs, HTTP codes) become one or two sentences for the chat.
// The raw text is kept separately so the UI can show it behind "Details".

import (
	"errors"
	"regexp"
	"strings"
)

// Failure is one model that couldn't answer during a routed turn.
type Failure struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	Name     string `json:"name"`
	Code     int    `json:"code"`
	Message  string `json:"message"` // plain English
	Raw      string `json:"raw"`
}

// RouteError is returned when no model in the route could answer.
type RouteError struct {
	Failures []Failure
	Resting  []string // "Groq back in 40s (rate limit)"
	NoTools  bool     // nothing set up can use tools
	NoModels bool     // nothing set up at all
}

func (e *RouteError) Error() string {
	if e.NoModels {
		return "No AI model is set up yet. Add a free Gemini or Groq key in Settings › Models (it takes a minute)."
	}
	if e.NoTools {
		return "None of your models can use tools, so Blaze can't act. Add a free Gemini, Groq or Cerebras key in Settings › Models."
	}
	var b strings.Builder
	if len(e.Failures) == 0 && len(e.Resting) > 0 {
		b.WriteString("All your models are resting right now:")
	} else {
		b.WriteString("No model could answer:")
	}
	for _, r := range e.Resting {
		b.WriteString("\n• " + r)
	}
	for _, f := range e.Failures {
		b.WriteString("\n• " + f.Name + ": " + f.Raw)
	}
	return b.String()
}

// Plain is the short message the chat shows for an error.
func Plain(err error) string {
	if err == nil {
		return ""
	}
	var pl Plainer // v0.19: Blaze Cloud / Custom errors already carry their line (backend.go)
	if errors.As(err, &pl) {
		return pl.PlainLine()
	}
	var re *RouteError
	if errors.As(err, &re) {
		if re.NoModels || re.NoTools {
			return re.Error()
		}
		if len(re.Failures) == 0 && len(re.Resting) > 0 {
			return "All your models are resting after hitting their limits (" + strings.Join(re.Resting, "; ") + "). Try again shortly, or add another free key so Blaze can switch."
		}
		if len(re.Failures) == 1 && len(re.Resting) == 0 {
			return re.Failures[0].Message
		}
		var parts []string
		for _, f := range re.Failures {
			parts = append(parts, f.Message)
		}
		parts = append(parts, re.Resting...)
		return "None of your models could answer. " + strings.Join(uniq(parts), " ")
	}
	return PlainText("The AI", 0, err.Error())
}

var codeRe = regexp.MustCompile(`(?:HTTP |\()(\d{3})\b`)

// PlainText turns one raw error from who (a provider name) into a sentence.
func PlainText(who string, code int, raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return "Something went wrong. Try again."
	}
	if code == 0 {
		if m := codeRe.FindStringSubmatch(t); m != nil {
			for _, c := range m[1] {
				code = code*10 + int(c-'0')
			}
		}
	}
	low := strings.ToLower(t)
	switch {
	case code == 429 || strings.Contains(low, "quota") || strings.Contains(low, "rate limit") || strings.Contains(low, "resource_exhausted"):
		return who + " hit its free limit for now. Blaze will use another model; limits reset by themselves (usually within a minute, or by tomorrow)."
	case code == 401 || code == 403 || (strings.Contains(low, "api key") && (strings.Contains(low, "invalid") || strings.Contains(low, "not valid"))) || strings.Contains(low, "is a ") && strings.Contains(low, "key"):
		return who + " didn't accept its key. Check it in Settings › Models."
	case code == 404 || strings.Contains(low, "not found") || strings.Contains(low, "does not exist") || strings.Contains(low, "no model chosen"):
		return who + " doesn't offer that model to your key. Pick another one in Settings › Models."
	case NoToolSupport(&HTTPError{Code: code, Body: t}):
		return who + " can't use tools with this model. Pick a model marked Tools."
	case code == 400 && (strings.Contains(low, "invalid argument") || strings.Contains(low, "invalid_argument") || strings.Contains(low, "thought_signature") || strings.Contains(low, "function")):
		return who + " refused the conversation's format (" + short(t) + "). Blaze repaired the history; try again, or switch model."
	case code >= 500 || strings.Contains(low, "overloaded") || strings.Contains(low, "unavailable"):
		return who + " is having trouble on its side right now. Try again in a moment."
	case strings.Contains(low, "timeout") || strings.Contains(low, "timed out") || strings.Contains(low, "stopped sending") || strings.Contains(low, "deadline"):
		return who + " took too long to answer. Try again."
	case strings.Contains(low, "no such host") || strings.Contains(low, "connection refused") || strings.Contains(low, "network") || strings.Contains(low, "dial tcp"):
		if strings.Contains(low, "127.0.0.1") || strings.Contains(low, "localhost") {
			return who + " isn't running on this computer. Start it, then try again."
		}
		return "Blaze can't reach " + who + ". Check your internet connection, then try again."
	case strings.Contains(low, "context") && (strings.Contains(low, "length") || strings.Contains(low, "too long") || strings.Contains(low, "token")):
		return "This chat got too long for " + who + ". Start a new chat, or try again and Blaze will trim it."
	}
	if len(t) < 160 && !strings.Contains(t, "{") {
		return who + ": " + t
	}
	return who + " couldn't finish that one. Try again, or switch model."
}

func short(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if len(s) > 80 {
		s = s[:80] + "…"
	}
	return s
}

func uniq(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
