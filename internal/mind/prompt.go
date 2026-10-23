package mind

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ---------- shared prompt text (mirrored by Blaze Android; see plan-one-mind.md) ----------

// MindRule goes in every system prompt.
const MindRule = `ONE MIND
- This is one continuous conversation with the user; there are no separate chats. What you know lives in the memory table and the journal, not in this prompt.
- WHAT YOU KNOW (below) is only the pinned core and an index of subjects. Before answering anything about the user, their people, projects, plans or past conversations, call memory_search (and search_chat for exact earlier words). Never ask the user to repeat something you could look up.
- Save as you go: memory_save for anything lasting (profile, preference, person, project state, decision, commitment). One line each, the user's own conditions kept exactly ("on weekdays", "for work trips"); write "(inferred)" when you inferred it. memory_update rewrites a line that changed; never keep two versions.
- Never save procedure, one-off values (prices, counts, codes) or secrets.`

// LookupRule is added when the context ladder dropped the history (level Lookup).
const LookupRule = `CONTEXT RESET: earlier turns are NOT in this prompt. You don't remember them; look them up. Use memory_search for facts, preferences and decisions, and search_chat / read_turn for exact earlier words, before you answer. Don't guess and don't ask the user to repeat.`

// ExtractPrompt is the cheap after-turn pass and the idle review.
const ExtractPrompt = `You maintain the user's memory for Blaze. From the conversation below, list what is worth remembering for months: profile facts (name, school, work, where they live), standing preferences (with every condition the user attached), people in their life and who they are to them, project state, decisions and why, commitments and deadlines.
Skip: procedure, how a tool works, one-off values (prices, counts, order numbers, codes), small talk, secrets and passwords, anything already in EXISTING unless it changed.
Write lines no broader or firmer than the user said. Mark your inferences "(inferred)".
Answer ONLY a JSON array, [] when nothing: [{"kind":"profile|preference|person|project|decision|fact","subject":"who or what it is about","text":"one line","related":"other subjects, comma-separated"}]`

// ---------- the context ladder ----------

// Level is how much history goes into the prompt.
type Level int

const (
	Recent  Level = iota // the recent turns word for word
	Summary              // rolling summary + recent turns (long-chat engine), at 60% of context
	Lookup               // pinned core + memory index + the last few turns; the agent looks up the rest
)

func (l Level) String() string { return [...]string{"recent", "summary", "lookup"}[l] }

// LookupTail is how many messages stay word for word at level Lookup.
const LookupTail = 6

// Ladder picks the level: history under budget → Recent; over it → Summary; when the
// summary plus the recent tail would itself pass the hard limit (90% of the context
// window), or summarising failed → Lookup.
func Ladder(historyTokens, summaryTokens, tailTokens, budget, window int, summaryErr bool) Level {
	if summaryErr {
		return Lookup
	}
	if historyTokens <= budget {
		return Recent
	}
	hard := window * 9 / 10
	if window <= 0 || hard < budget {
		hard = budget * 3 / 2
	}
	if summaryTokens+tailTokens > hard {
		return Lookup
	}
	return Summary
}

// ---------- prompt block ----------

// Block is the WHAT YOU KNOW section: the pinned core and the subject index.
func Block(core []Memory, index map[string][]string) string {
	var b strings.Builder
	b.WriteString("\nWHAT YOU KNOW (pinned core; memory_search for the rest)\n")
	if len(core) == 0 {
		b.WriteString("- (nothing pinned yet)\n")
	}
	for _, m := range core {
		fmt.Fprintf(&b, "- [%s] %s: %s\n", m.Kind, m.Subject, m.Text)
	}
	if len(index) > 0 {
		b.WriteString("MEMORY INDEX (subjects you have notes on; look them up)\n")
		for _, k := range Kinds {
			if l := index[k]; len(l) > 0 {
				fmt.Fprintf(&b, "- %s: %s\n", k, strings.Join(l, ", "))
			}
		}
	}
	return b.String()
}

// ParseExtract reads the model's JSON array (tolerating code fences and prose around it).
func ParseExtract(s string) []Memory {
	i, j := strings.Index(s, "["), strings.LastIndex(s, "]")
	if i < 0 || j <= i {
		return nil
	}
	var raw []struct{ Kind, Subject, Text, Related string }
	if json.Unmarshal([]byte(s[i:j+1]), &raw) != nil {
		return nil
	}
	var out []Memory
	for _, r := range raw {
		if strings.TrimSpace(r.Text) == "" {
			continue
		}
		out = append(out, Memory{Kind: NormKind(r.Kind), Subject: r.Subject, Text: r.Text, Related: r.Related})
	}
	return out
}

// Transcript renders journal turns for the review prompt.
func Transcript(ts []Turn) string {
	var b strings.Builder
	for _, t := range ts {
		c := t.Content
		if len(c) > 2000 {
			c = c[:2000] + " …"
		}
		fmt.Fprintf(&b, "[%d %s] %s\n", t.ID, t.Role, c)
	}
	return b.String()
}
