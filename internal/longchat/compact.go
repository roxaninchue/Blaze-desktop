package longchat

import (
	"strconv"
	"strings"

	"blaze-desktop/internal/llm"
)

// KeepTail is how many recent messages always go to the model word for word.
const KeepTail = 12

// FreeBudget is the compaction trigger on free models: past this many prompt tokens
// the older turns fold into the summary (or 60% of the context window, if smaller).
const FreeBudget = 12000

// SummaryMax caps the state note (~800 tokens).
const SummaryMax = 3200

const SummarisePrompt = `You keep the state note of a long chat between a user and Blaze, an assistant that works on their computer. Update the note with the new turns below. Keep exactly these sections: Goal, Decisions (with why), Facts (names, numbers, paths and commands exactly), Open threads, Files touched. At most 800 tokens. Drop chit-chat and tool noise. Never invent anything. Reply with the note only.`

// Tokens estimates a message list's prompt size (4 characters a token, plus calls).
func Tokens(ms []llm.Message) int {
	n := 0
	for _, m := range ms {
		n += len(m.Content)/4 + 4
		for _, c := range m.ToolCalls {
			n += (len(c.Function.Name) + len(c.Function.Arguments)) / 4
		}
	}
	return n
}

// Budget is when compaction starts for a model with this context window (0 = unknown).
func Budget(contextWindow int) int {
	b := FreeBudget
	if contextWindow > 0 && contextWindow*6/10 < b {
		b = contextWindow * 6 / 10
	}
	return b
}

// tailStart is where the word-for-word tail begins: KeepTail from the end, moved back
// to a user message so a tool call is never separated from its result.
func tailStart(h []llm.Message) int {
	i := len(h) - KeepTail
	if i <= 0 {
		return 0
	}
	for i > 0 && h[i].Role != "user" {
		i--
	}
	return i
}

// Summarise folds a chunk of turns into the previous note and returns the new one.
type Summarise func(prev string, chunk []llm.Message) (string, error)

// Context picks what goes to the model: when the chat is over budget, everything
// before the tail folds into st.Summary (updated, never redone from scratch). It
// returns the messages to send after the system prompt, the system text to add
// (the summary) and whether st changed.
func Context(h []llm.Message, st *ChatState, budget int, sum Summarise) ([]llm.Message, string, bool, error) {
	if st.Upto > len(h) {
		st.Upto = 0 // the chat was cleared or edited: start over
		st.Summary = ""
	}
	changed := false
	ts := tailStart(h)
	if ts > st.Upto && Tokens(h[st.Upto:]) > budget && sum != nil {
		s, err := sum(st.Summary, h[st.Upto:ts])
		if err != nil {
			return nil, "", false, err
		}
		if r := []rune(strings.TrimSpace(s)); len(r) > SummaryMax {
			s = string(r[:SummaryMax])
		}
		st.Summary, st.Upto, changed = strings.TrimSpace(s), ts, true
	}
	from := st.Upto
	// Never start on a tool result or an assistant tool call cut from its user turn.
	for from < len(h) && from > 0 && h[from].Role != "user" {
		from++
	}
	extra := ""
	if st.Summary != "" {
		extra = "\nEARLIER IN THIS CHAT (summary of turns 0-" + itoa(st.Upto-1) + "; use search_chat or read_turn for exact text, never ask the user to repeat)\n" + st.Summary + "\n"
	}
	return append([]llm.Message(nil), h[from:]...), extra, changed, nil
}

// Transcript renders a chunk for the summariser (tool output clipped).
func Transcript(chunk []llm.Message, startTurn int) string {
	var b strings.Builder
	for i, m := range chunk {
		c := m.Content
		if m.Role == "tool" && len(c) > 600 {
			c = c[:400] + " … " + c[len(c)-150:]
		}
		for _, tc := range m.ToolCalls {
			c += "\n[calls " + tc.Function.Name + " " + clip(tc.Function.Arguments, 200) + "]"
		}
		if strings.TrimSpace(c) == "" {
			continue
		}
		b.WriteString("[" + itoa(startTurn+i) + " " + m.Role + "] " + c + "\n")
	}
	return b.String()
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func itoa(n int) string {
	if n < 0 {
		n = 0
	}
	return strconv.Itoa(n)
}
