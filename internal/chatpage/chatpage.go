// Package chatpage pages the one continuous conversation for the UI (v0.19, like Blaze Mobile's endless
// Chat): the chat opens on its last few turns and older turns load as you scroll up. Pages always start at
// a user message, so a turn's assistant replies, tool calls and tool results stay together.
//
// This is only about what the UI renders. What the model sees (context window, summaries, /clear) is
// assembled elsewhere and doesn't change here; nothing is removed from the saved conversation.
package chatpage

import (
	"strings"
	"unicode/utf8"

	"blaze-desktop/internal/llm"
)

// Turns is the default page size in user turns; MaxMsgs caps a page's messages (tool-heavy turns).
const (
	Turns   = 12
	MaxMsgs = 400
)

// Window returns where the page that ends at before starts: at most turns user turns and (when more than
// one turn fits) at most maxMsgs messages, always at a user message, or 0 when the older part is short.
func Window(msgs []llm.Message, before, turns, maxMsgs int) int {
	if before > len(msgs) {
		before = len(msgs)
	}
	if before <= 0 {
		return 0
	}
	if turns <= 0 {
		turns = Turns
	}
	if maxMsgs <= 0 {
		maxMsgs = MaxMsgs
	}
	n, last := 0, -1
	for i := before - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		if n >= 1 && before-i > maxMsgs {
			return last
		}
		n++
		last = i
		if n == turns {
			return i
		}
	}
	if n >= 1 && before > maxMsgs && last > 0 {
		return last
	}
	return 0
}

// Hit is one search match in a conversation.
type Hit struct {
	Index   int    `json:"index"`
	Role    string `json:"role"`
	Snippet string `json:"snippet"`
}

// Search finds q (case-insensitive, every word) in user and assistant messages, newest first.
func Search(msgs []llm.Message, q string, limit int) []Hit {
	ws := strings.Fields(strings.ToLower(q))
	if len(ws) == 0 {
		return nil
	}
	var out []Hit
	for i := len(msgs) - 1; i >= 0 && (limit <= 0 || len(out) < limit); i-- {
		m := msgs[i]
		if (m.Role != "user" && m.Role != "assistant") || strings.TrimSpace(m.Content) == "" {
			continue
		}
		low := strings.ToLower(m.Content)
		ok := true
		for _, w := range ws {
			if !strings.Contains(low, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, Hit{Index: i, Role: m.Role, Snippet: Snippet(m.Content, ws[0], 70)})
		}
	}
	return out
}

// Snippet is the text around the first match of word, about width runes either side, on one line.
func Snippet(text, word string, width int) string {
	t := strings.Join(strings.Fields(text), " ")
	i := strings.Index(strings.ToLower(t), strings.ToLower(word))
	if i < 0 {
		i = 0
	}
	from, to := i, i+len(word)
	if to > len(t) {
		to = len(t)
	}
	for k := 0; k < width && from > 0; k++ {
		_, sz := utf8.DecodeLastRuneInString(t[:from])
		from -= sz
	}
	for k := 0; k < width && to < len(t); k++ {
		_, sz := utf8.DecodeRuneInString(t[to:])
		to += sz
	}
	s := t[from:to]
	if from > 0 {
		s = "…" + s
	}
	if to < len(t) {
		s += "…"
	}
	return s
}
