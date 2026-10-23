package topics

import (
	"regexp"
	"sort"
	"strings"

	"blaze-desktop/internal/llm"
)

// SegOpts tunes Segment.
type SegOpts struct {
	// Judge is asked only about ambiguous messages at or after JudgeFrom (normally the
	// latest user message): old history is re-read lexically, never with model calls.
	Judge     Judge
	JudgeFrom int
	// Clean returns the text to segment for a user message and whether it is a
	// continuation that always stays in the active topic (a [Continue], an agent's
	// result). nil = the content as is.
	Clean func(m llm.Message) (text string, followup bool)
}

// Event is what Segment did with one user message.
type Event struct {
	Turn     int
	Decision Decision
	Closed   int // topic left by this message (0 = none)
}

const (
	userWeight      = 1.0
	assistantWeight = 0.4
	maxTerms        = 300
)

// Segment reads the messages of hist not read yet: each user message gets a topic,
// assistant replies feed the keywords of their turn's topic. When the active topic
// changes, the topic left behind is scanned for carry-over. It returns one event per
// user message read.
func (s *State) Segment(hist []llm.Message, o SegOpts) []Event {
	if s.TurnTopic == nil {
		s.TurnTopic = map[int]int{}
	}
	if s.NextID < 1 {
		s.NextID = 1
	}
	if s.Assigned > len(hist) {
		s.Truncate(len(hist))
	}
	var evs []Event
	for i := s.Assigned; i < len(hist); i++ {
		m := hist[i]
		switch m.Role {
		case "user":
			text, follow := m.Content, false
			if o.Clean != nil {
				text, follow = o.Clean(m)
			}
			var d Decision
			if follow {
				if t := s.Get(s.Active); t != nil {
					d = Decision{Topic: t.ID, Kind: Same, Why: "followup"}
				} else {
					d = Decision{Kind: SmallTalk, Why: "followup"}
				}
			} else {
				var j Judge
				if i >= o.JudgeFrom {
					j = o.Judge
				}
				d = s.Decide(text, j)
			}
			if d.Kind == NewTopic {
				t := &Topic{ID: s.NextID, Title: firstNonEmpty(d.Title, Title(text)), Titled: d.Why == "judge" && d.Title != "", Terms: map[string]float64{}, First: i, Upto: i, Carried: i}
				s.NextID++
				s.Topics = append(s.Topics, t)
				d.Topic = t.ID
			}
			ev := Event{Turn: i, Decision: d}
			prev := s.Active
			s.TurnTopic[i] = d.Topic
			s.LastUser = i
			if t := s.Get(d.Topic); t != nil {
				t.Last = i
				t.Turns++
				addTerms(t, text, userWeight)
				if !t.Titled {
					t.Title = retitle(t)
				}
				s.Active = t.ID
			}
			if prev != 0 && s.Active != prev {
				ev.Closed = prev
				s.CarryFrom(hist[:i], prev)
			}
			evs = append(evs, ev)
		case "assistant":
			if s.LastUser >= 0 {
				if t := s.Get(s.TurnTopic[s.LastUser]); t != nil {
					addTerms(t, m.Content, assistantWeight)
				}
			}
		}
	}
	s.Assigned = len(hist)
	return evs
}

func addTerms(t *Topic, text string, w float64) {
	if t.Terms == nil {
		t.Terms = map[string]float64{}
	}
	if len(text) > 4000 {
		text = text[:4000]
	}
	for _, x := range Terms(text) {
		t.Terms[x] += w
	}
	if len(t.Terms) > maxTerms {
		type kv struct {
			k string
			v float64
		}
		var all []kv
		for k, v := range t.Terms {
			all = append(all, kv{k, v})
		}
		sort.Slice(all, func(i, j int) bool { return all[i].v > all[j].v || (all[i].v == all[j].v && all[i].k < all[j].k) })
		t.Terms = map[string]float64{}
		for _, e := range all[:maxTerms] {
			t.Terms[e.k] = e.v
		}
	}
}

// retitle keeps the first heuristic title (a chip that keeps renaming itself is noise);
// only a topic that started without a usable title takes its top keyword.
func retitle(t *Topic) string {
	if t.Title != "Chat" {
		return t.Title
	}
	top, tv := "", 0.0
	for k, v := range t.Terms {
		if v > tv || (v == tv && k < top) {
			top, tv = k, v
		}
	}
	if tv >= 2*userWeight {
		return cleanTitle(top)
	}
	return t.Title
}

// Truncate forgets what Segment read at or past message n (the chat was retried or edited).
func (s *State) Truncate(n int) {
	for k := range s.TurnTopic {
		if k >= n {
			delete(s.TurnTopic, k)
		}
	}
	var keep []*Topic
	for _, t := range s.Topics {
		if t.First >= n {
			continue
		}
		if t.Upto > n {
			t.Upto, t.Note = t.First, ""
		}
		if t.Carried > n {
			t.Carried = n
		}
		t.Turns, t.Last = 0, t.First
		for k, id := range s.TurnTopic {
			if id == t.ID {
				t.Turns++
				if k > t.Last {
					t.Last = k
				}
			}
		}
		keep = append(keep, t)
	}
	s.Topics = keep
	var cs []Carry
	for _, c := range s.Carry {
		if c.Turn < n {
			cs = append(cs, c)
		}
	}
	s.Carry = cs
	s.Assigned, s.LastUser, s.Active = n, -1, 0
	best := -1
	for k, id := range s.TurnTopic {
		if k > s.LastUser {
			s.LastUser = k
		}
		if id != 0 && k > best { // the active topic is the latest non-small-talk turn's
			best, s.Active = k, id
		}
	}
}

// Spans maps each user turn to its message range [start, end).
func Spans(hist []llm.Message) [][2]int {
	var out [][2]int
	for i, m := range hist {
		if m.Role != "user" {
			continue
		}
		if len(out) > 0 {
			out[len(out)-1][1] = i
		}
		out = append(out, [2]int{i, len(hist)})
	}
	return out
}

// ---------- carry-over ----------

var (
	sentRe     = regexp.MustCompile(`[^.!?\n]+[.!?]*`)
	weekday    = `(?:mon|tues|wednes|thurs|fri|satur|sun)day`
	month      = `(?:jan(?:uary)?|feb(?:ruary)?|mar(?:ch)?|apr(?:il)?|may|june?|july?|aug(?:ust)?|sep(?:t(?:ember)?)?|oct(?:ober)?|nov(?:ember)?|dec(?:ember)?)`
	deadlineRe = regexp.MustCompile(`(?i)\b(?:deadline|due (?:on|by|date)?|no later than|(?:by|before|until) (?:` + weekday + `|tomorrow|tonight|next \w+|end of (?:the )?\w+|the \d{1,2}(?:st|nd|rd|th)?|\d{1,2}(?:st|nd|rd|th)? ` + month + `|` + month + ` \d{1,2}|\d{1,2}[:/]\d{1,2}|noon|midnight))\b`)
	importRe   = regexp.MustCompile(`(?i)\b(?:important|don'?t forget|do not forget|never forget|remember (?:that|to)|keep in mind|crucial|urgent|whatever you do)\b`)
	prefRe     = regexp.MustCompile(`(?i)\b(?:i (?:always|never|prefer|hate|love|can'?t stand|am allergic)|i'?m (?:allergic|vegetarian|vegan)|my name is|call me|from now on|in every (?:chat|topic|answer))\b`)
	decideRe   = regexp.MustCompile(`(?i)\b(?:let'?s go with|we(?:'ll| will) go with|i(?:'ll| will) go with|(?:we|i) decided|decided to|settled on|final (?:answer|decision|choice)|we chose|i chose)\b`)
	promiseRe  = regexp.MustCompile(`(?i)\b(?:i(?:'ll| will) (?:remind you|follow up|check back|get back to you|come back to)|remind me)\b`)
)

// ExtractCarry finds the lines of one message that matter beyond its topic. User lines
// count for every kind; assistant lines only for promises it made.
func ExtractCarry(role, text string) []Carry {
	var out []Carry
	if len(text) > 6000 {
		text = text[:6000]
	}
	for _, sn := range sentRe.FindAllString(text, -1) {
		sn = strings.Join(strings.Fields(sn), " ")
		if len(sn) < 8 {
			continue
		}
		kind := ""
		switch {
		case role == "assistant":
			if promiseRe.MatchString(sn) {
				kind = "promise"
			}
		case deadlineRe.MatchString(sn):
			kind = "deadline"
		case decideRe.MatchString(sn):
			kind = "decision"
		case prefRe.MatchString(sn):
			kind = "preference"
		case importRe.MatchString(sn):
			kind = "important"
		case promiseRe.MatchString(sn):
			kind = "promise"
		}
		if kind == "" {
			continue
		}
		out = append(out, Carry{Kind: kind, Text: clip(sn, 220)})
	}
	return out
}

// MaxCarry caps the carry-over list (oldest dropped first).
const MaxCarry = 40

// CarryFrom scans topic id's turns not scanned yet (in hist) and adds their carry-over.
// It returns what was new.
func (s *State) CarryFrom(hist []llm.Message, id int) []Carry {
	t := s.Get(id)
	if t == nil {
		return nil
	}
	var found []Carry
	for _, sp := range Spans(hist) {
		if sp[0] < t.Carried || s.TurnTopic[sp[0]] != id {
			continue
		}
		for j := sp[0]; j < sp[1] && j < len(hist); j++ {
			m := hist[j]
			if m.Role != "user" && m.Role != "assistant" {
				continue
			}
			for _, c := range ExtractCarry(m.Role, m.Content) {
				c.Topic, c.Turn = id, j
				found = append(found, c)
			}
		}
	}
	if len(hist) > t.Carried {
		t.Carried = len(hist)
	}
	return s.AddCarry(found)
}

// AddCarry adds lines (de-duplicated) and returns the new ones.
func (s *State) AddCarry(cs []Carry) []Carry {
	seen := map[string]bool{}
	for _, c := range s.Carry {
		seen[strings.ToLower(c.Text)] = true
	}
	var added []Carry
	for _, c := range cs {
		c.Text = strings.TrimSpace(c.Text)
		if c.Text == "" || seen[strings.ToLower(c.Text)] {
			continue
		}
		seen[strings.ToLower(c.Text)] = true
		s.Carry = append(s.Carry, c)
		added = append(added, c)
	}
	if len(s.Carry) > MaxCarry {
		s.Carry = s.Carry[len(s.Carry)-MaxCarry:]
	}
	return added
}
