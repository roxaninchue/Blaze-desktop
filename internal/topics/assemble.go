package topics

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"blaze-desktop/internal/llm"
)

// Rule goes in the system prompt whenever the topic view is used.
const Rule = `TOPICS
- This chat never ends, so it is split into topics. Only the ACTIVE TOPIC's state note and recent turns are below; turns about other topics are left out on purpose.
- Answer about the active topic as if the other topics never happened. Don't mention them unless the user does, or a carried-over line matters right now (a deadline, a decision, a preference, a promise).
- If the user refers to something you don't see here, it is in another topic or older: call search_chat (then read_turn) before answering. Never ask the user to repeat it.`

// NotePrompt keeps one topic's state note (updated, never redone).
const NotePrompt = `You keep the state note of ONE topic of a long chat between a user and Blaze, an assistant on their computer. Update the note with the new turns of this topic below. Keep exactly these sections: Goal, Decisions (with why), Facts (names, numbers, paths and commands exactly), Open threads. At most 600 tokens. Keep what is still true from the current note; drop chit-chat and tool noise. Never invent anything. Reply with the note only.`

// JudgePrompt is the small model call for an ambiguous message.
const JudgePrompt = `You sort a chat into topics. Given the topics so far and the user's new message, answer which topic the message continues, or "new" if it starts a different subject. A follow-up question about the same thing (a detail, a variation, a next step) continues that topic. Answer ONLY JSON: {"topic": <id number or "new">, "title": "<2-4 word title if new>"}`

// CarryPrompt is the optional model pass over a topic being left.
const CarryPrompt = `From these turns of one topic, list ONLY what must still be remembered when the chat moves on to other subjects: deadlines and dates, decisions and why, standing user preferences or rules, promises Blaze made that aren't done. Skip everything specific to the topic's details. Answer ONLY a JSON array, [] when nothing: [{"kind":"deadline|decision|preference|promise|important","text":"one line"}]`

// NoteMax caps a topic note (~700 tokens).
const NoteMax = 2800

// Opts tunes Assemble.
type Opts struct {
	Budget   int // tokens for the selected turns plus the topic text (0 = 12000)
	From     int // /clear: messages before this index are never sent
	MaxIndex int // other-topic lines (0 = 12)
	MaxCarry int // carried-over lines (0 = 8)
	Preamble int // characters of a pre-first-question message kept (0 = 1200)
}

func (o *Opts) defaults() {
	if o.Budget <= 0 {
		o.Budget = 12000
	}
	if o.MaxIndex <= 0 {
		o.MaxIndex = 12
	}
	if o.MaxCarry <= 0 {
		o.MaxCarry = 8
	}
	if o.Preamble <= 0 {
		o.Preamble = 1200
	}
}

// Tokens estimates prompt tokens (4 characters a token plus calls), like longchat.Tokens.
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

// Included says which user turns go to the model: the active topic's turns the note
// doesn't cover, small talk that follows one of them, and always the current turn.
func (s *State) Included(hist []llm.Message, from int) []int {
	sp := Spans(hist)
	act := s.Get(s.Active)
	upto := from
	if act != nil && act.Upto > upto {
		upto = act.Upto
	}
	var out []int
	lastReal := 0 // topic of the latest non-small-talk turn seen
	for k, p := range sp {
		i := p[0]
		id, known := s.TurnTopic[i]
		switch {
		case k == len(sp)-1:
			out = append(out, i) // the current turn, whatever it is
		case !known || i < upto:
		case id != 0:
			if act != nil && id == act.ID {
				out = append(out, i)
			}
		default: // small talk: kept inside the active topic's flow only
			if act != nil && lastReal == act.ID {
				out = append(out, i)
			}
		}
		if known && id != 0 {
			lastReal = id
		}
	}
	return out
}

// Assemble builds what goes to the model after the system prompt: the active topic's
// turns (whole turns, newest first until the budget is used, the current turn always),
// and the system text to add: the topic rule, the active topic's note, carried-over
// lines from other topics and a one-line index of the other topics.
func (s *State) Assemble(hist []llm.Message, o Opts) ([]llm.Message, string) {
	o.defaults()
	extra := s.Text(hist, o)
	// The topic text may take at most half the budget: trim the index, then the carry list.
	for len(extra)/4 > o.Budget/2 && (o.MaxIndex > 1 || o.MaxCarry > 1) {
		if o.MaxIndex > 1 {
			o.MaxIndex /= 2
		} else {
			o.MaxCarry /= 2
		}
		extra = s.Text(hist, o)
	}
	left := o.Budget - len(extra)/4
	inc := s.Included(hist, o.From)
	end := map[int]int{}
	for _, p := range Spans(hist) {
		end[p[0]] = p[1]
	}
	var chosen [][2]int
	used := 0
	for k := len(inc) - 1; k >= 0; k-- {
		a, b := inc[k], end[inc[k]]
		t := Tokens(hist[a:b])
		if k < len(inc)-1 && used+t > left {
			break // older turns are in the note or a search away
		}
		used += t
		chosen = append(chosen, [2]int{a, b})
	}
	var out []llm.Message
	for k := len(chosen) - 1; k >= 0; k-- {
		out = append(out, hist[chosen[k][0]:chosen[k][1]]...)
	}
	return out, extra
}

// Text is the topic system text (see Assemble).
func (s *State) Text(hist []llm.Message, o Opts) string {
	o.defaults()
	var b strings.Builder
	b.WriteString("\n" + Rule + "\n")
	// A message before the first question (FreshChat's hand-over) belongs to every topic.
	if len(hist) > 0 && hist[0].Role == "assistant" && o.From == 0 && strings.TrimSpace(hist[0].Content) != "" {
		b.WriteString("\nCHAT OPENING (applies to every topic)\n" + clip(hist[0].Content, o.Preamble) + "\n")
	}
	act := s.Get(s.Active)
	if act != nil {
		fmt.Fprintf(&b, "\nACTIVE TOPIC: %s (#%d, %d turns)\n", act.Title, act.ID, act.Turns)
		// /clear: a note that only covers turns before the fresh window belongs to the old window
		// (longchat drops its pre-clear summary the same way). v0.19.13.
		if act.Note != "" && (o.From == 0 || act.Upto > o.From) {
			fmt.Fprintf(&b, "STATE NOTE of this topic (its turns before message %d; search_chat for exact words)\n%s\n", act.Upto, act.Note)
		}
	}
	var cs []Carry
	for i := len(s.Carry) - 1; i >= 0 && len(cs) < o.MaxCarry; i-- {
		if act == nil || s.Carry[i].Topic != act.ID {
			cs = append(cs, s.Carry[i])
		}
	}
	if len(cs) > 0 {
		b.WriteString("\nCARRIED OVER FROM OTHER TOPICS (still matters everywhere)\n")
		for _, c := range cs {
			title := ""
			if t := s.Get(c.Topic); t != nil {
				title = t.Title
			}
			fmt.Fprintf(&b, "- [%s · %s · turn %d] %s\n", c.Kind, title, c.Turn, c.Text)
		}
	}
	var others []*Topic
	for _, t := range s.recent(len(s.Topics)) {
		if act == nil || t.ID != act.ID {
			others = append(others, t)
		}
	}
	if len(others) > 0 {
		b.WriteString("\nOTHER TOPICS IN THIS CHAT (not shown; only if the user brings one up: search_chat / read_turn)\n")
		for k, t := range others {
			if k == o.MaxIndex {
				fmt.Fprintf(&b, "- … %d older topics\n", len(others)-k)
				break
			}
			fmt.Fprintf(&b, "- #%d %s · %d turns · messages %d-%d · %s\n", t.ID, t.Title, t.Turns, t.First, t.Last, strings.Join(topTerms(t, 4), ", "))
		}
	}
	return b.String()
}

func topTerms(t *Topic, n int) []string {
	type kv struct {
		k string
		v float64
	}
	var all []kv
	for k, v := range t.Terms {
		all = append(all, kv{k, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
	var out []string
	for _, e := range all {
		if len(out) == n {
			break
		}
		out = append(out, e.k)
	}
	return out
}

// ---------- folding a topic into its note ----------

// KeepTail is how many messages of the active topic always go word for word.
const KeepTail = 12

// FoldSlack: the note is updated only when this many messages past the tail piled up
// (or the topic is over budget), so it is updated in batches, never every turn.
const FoldSlack = 6

// FoldChunk returns the messages of topic id that should fold into its note now (whole
// turns before its tail of KeepTail messages; never the current turn), where the chunk
// starts and the new Upto. ok=false: nothing to do.
func (s *State) FoldChunk(hist []llm.Message, id, budget int) (chunk []llm.Message, start, newUpto int, ok bool) {
	t := s.Get(id)
	if t == nil {
		return nil, 0, 0, false
	}
	var turns [][2]int
	for _, p := range Spans(hist) {
		if p[0] >= t.Upto && s.TurnTopic[p[0]] == id && p[0] != s.LastUser {
			turns = append(turns, p)
		}
	}
	n, tok := 0, 0
	for _, p := range turns {
		n += p[1] - p[0]
		tok += Tokens(hist[p[0]:p[1]])
	}
	if len(turns) < 2 || (n <= KeepTail+FoldSlack && (budget <= 0 || tok <= budget*6/10)) {
		return nil, 0, 0, false
	}
	kept, cut := 0, len(turns)
	for cut > 1 && kept < KeepTail {
		cut--
		kept += turns[cut][1] - turns[cut][0]
	}
	for _, p := range turns[:cut] {
		chunk = append(chunk, hist[p[0]:p[1]]...)
	}
	return chunk, turns[0][0], turns[cut][0], true
}

// SetNote stores a folded note (cut to NoteMax) and moves Upto.
func (s *State) SetNote(id int, note string, upto int) {
	t := s.Get(id)
	if t == nil {
		return
	}
	note = strings.TrimSpace(note)
	if r := []rune(note); len(r) > NoteMax {
		note = string(r[:NoteMax])
	}
	t.Note, t.Upto = note, upto
}

// HeuristicNote updates a note without a model: the goal is the topic's first ask, and
// each folded user message adds a line under Open threads (oldest dropped past NoteMax).
// It appends, never redoes.
func HeuristicNote(prev string, chunk []llm.Message, start int) string {
	prev = strings.TrimSpace(prev)
	var lines []string
	goal := ""
	for i, m := range chunk {
		if m.Role != "user" || strings.TrimSpace(m.Content) == "" {
			continue
		}
		c := strings.Join(strings.Fields(m.Content), " ")
		if goal == "" {
			goal = clip(c, 200)
		}
		lines = append(lines, "- turn "+strconv.Itoa(start+i)+": "+clip(c, 160))
	}
	if prev == "" {
		prev = "Goal: " + firstNonEmpty(goal, "(not stated)") + "\nDecisions:\nFacts:\nOpen threads:"
	}
	note := prev
	if len(lines) > 0 {
		note += "\n" + strings.Join(lines, "\n")
	}
	for len([]rune(note)) > NoteMax {
		i := strings.Index(note, "\n- turn ") // drop the oldest line, keep the header
		if i < 0 {
			note = string([]rune(note)[:NoteMax])
			break
		}
		j := strings.Index(note[i+1:], "\n")
		if j < 0 {
			note = note[:i]
			break
		}
		note = note[:i] + note[i+1+j:]
	}
	return note
}

// Transcript renders topic turns for the note or carry prompt (tool output clipped).
func Transcript(chunk []llm.Message, start int) string {
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
		b.WriteString("[" + strconv.Itoa(start+i) + " " + m.Role + "] " + c + "\n")
	}
	return b.String()
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// ---------- the frontend view ----------

// Info is one topic for the frontend chip.
type Info struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Turns  int    `json:"turns"`
	Active bool   `json:"active"`
}

// View is the chip's data: the current topic and every topic, newest first.
type View struct {
	Current string `json:"current"`
	ID      int    `json:"id"`
	Topics  []Info `json:"topics"`
}

func (s *State) View() View {
	v := View{Topics: []Info{}}
	if t := s.Get(s.Active); t != nil {
		v.Current, v.ID = t.Title, t.ID
	}
	for _, t := range s.recent(len(s.Topics)) {
		v.Topics = append(v.Topics, Info{ID: t.ID, Title: t.Title, Turns: t.Turns, Active: t.ID == s.Active})
	}
	return v
}
