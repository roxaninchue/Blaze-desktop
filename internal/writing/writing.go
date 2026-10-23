// Package writing is Desktop's select-and-rewrite / translate helper (v0.19.10): the prompt, the
// splitting of long text, and the checks that make the result safe to drop back into a document
// (no "Here's the rewritten text:" wrapper, the same links, placeholders and code, the same
// leading/trailing whitespace).
package writing

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Tones the rewrite menu offers (anything else is passed through as a free description).
var Tones = []string{"Clearer", "Shorter", "Friendlier", "More formal", "More casual", "Fix spelling and grammar", "More confident"}

// Languages the translate menu offers first.
var Languages = []string{"English", "French", "Spanish", "Portuguese", "German", "Swahili", "Bemba", "Nyanja", "Arabic", "Chinese (Simplified)", "Hindi"}

// MaxChunk is the most text sent in one request; longer text is split on paragraph breaks.
const MaxChunk = 3500

// MaxText is the most a selection may be (about 30 pages).
const MaxText = 60000

// System is the instruction for one request. mode is "rewrite" (arg = tone) or "translate"
// (arg = target language).
func System(mode, arg string) (string, error) {
	arg = strings.TrimSpace(arg)
	if len(arg) > 80 {
		arg = arg[:80]
	}
	rules := "Return ONLY the resulting text: no introduction, no explanation, no quotes around it, no code fences. " +
		"Keep the meaning, the paragraph and line breaks, list markers and Markdown formatting. " +
		"Keep every URL, email address, number, name, @mention, `code`, {placeholder}, {{placeholder}}, %s-style and $VARIABLE exactly as written. " +
		"If the text is already right, return it unchanged."
	switch mode {
	case "rewrite":
		if arg == "" {
			arg = "Clearer"
		}
		return "You rewrite the user's text in this style: " + arg + ". Write in the same language as the text. " + rules, nil
	case "translate":
		if arg == "" {
			return "", errors.New("pick a language")
		}
		return "You translate the user's text into " + arg + ". Translate everything except the items listed below; keep a natural, native tone. " + rules, nil
	}
	return "", fmt.Errorf("unknown mode %q", mode)
}

// Split cuts text into chunks of at most max bytes on paragraph breaks (then line breaks, then
// sentence ends), keeping every separator so the chunks join back to the original exactly.
func Split(text string, max int) []string {
	if len(text) <= max {
		return []string{text}
	}
	var out []string
	for len(text) > max {
		cut := -1
		for _, sep := range []string{"\n\n", "\n", ". ", " "} {
			if i := strings.LastIndex(text[:max], sep); i > 0 {
				cut = i + len(sep)
				break
			}
		}
		if cut <= 0 {
			cut = max
		}
		out = append(out, text[:cut])
		text = text[cut:]
	}
	if text != "" {
		out = append(out, text)
	}
	return out
}

var (
	preamble = regexp.MustCompile(`(?i)^\s*(sure[,!.]?\s*)?(here(?:'s| is| are)\b[^\n]{0,80}?:|(?:rewritten|translated|revised|translation|rewrite)[^\n]{0,40}:)\s*\n+`)
	fence    = regexp.MustCompile("(?s)^\\s*```[a-zA-Z0-9_-]*\\n(.*?)\\n?```\\s*$")
	protect  = regexp.MustCompile("https?://[^\\s)\\]>\"']+|[\\w.+-]+@[\\w-]+\\.[\\w.]+|\\{\\{[^{}]{1,40}\\}\\}|\\{[A-Za-z_][\\w.]{0,40}\\}|`[^`\\n]{1,80}`|%[sd]|\\$[A-Z_][A-Z0-9_]{1,30}")
)

// Clean strips what models wrap around an answer and gives the result the input's own leading
// and trailing whitespace, so it drops back into the document seamlessly.
func Clean(out, in string) string {
	o := strings.TrimSpace(out)
	if m := fence.FindStringSubmatch(o); m != nil && !strings.Contains(in, "```") {
		o = strings.TrimSpace(m[1])
	}
	o = strings.TrimSpace(preamble.ReplaceAllString(o, ""))
	core := strings.TrimSpace(in)
	for _, q := range [][2]string{{`"`, `"`}, {"“", "”"}, {"'", "'"}, {"«", "»"}} {
		if strings.HasPrefix(o, q[0]) && strings.HasSuffix(o, q[1]) && len(o) > 2 && !(strings.HasPrefix(core, q[0]) && strings.HasSuffix(core, q[1])) {
			o = strings.TrimSpace(o[len(q[0]) : len(o)-len(q[1])])
		}
	}
	lead := in[:len(in)-len(strings.TrimLeft(in, " \t\r\n"))]
	trail := in[len(strings.TrimRight(in, " \t\r\n")):]
	return lead + o + trail
}

// Missing lists the protected items (links, emails, placeholders, `code`, %s, $VARS) that the
// input has and the output lost.
func Missing(in, out string) []string {
	var miss []string
	seen := map[string]bool{}
	for _, t := range protect.FindAllString(in, -1) {
		t = strings.TrimRight(t, ".,;:!?")
		if seen[t] {
			continue
		}
		seen[t] = true
		if !strings.Contains(out, t) {
			miss = append(miss, t)
		}
	}
	return miss
}

// Check says whether a chunk's result can be used: not empty, keeps the protected items, and for a
// translation into another language it isn't just the input echoed back.
func Check(mode, in, out string) error {
	if strings.TrimSpace(out) == "" {
		return errors.New("the answer was empty")
	}
	if m := Missing(in, out); len(m) > 0 {
		return fmt.Errorf("it dropped %s", strings.Join(m, ", "))
	}
	if mode == "translate" && strings.TrimSpace(out) == strings.TrimSpace(in) && len(strings.Fields(in)) > 3 {
		return errors.New("it came back untranslated")
	}
	return nil
}
