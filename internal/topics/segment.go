// Package topics makes one endless chat topic-aware (v0.19): every user turn gets a topic,
// each topic keeps its own rolling state note, and the model is sent only the ACTIVE
// topic (its note and its recent turns word for word), a short index of the other
// topics and whatever was important enough to carry over. Turns from unrelated topics
// stay out of the prompt but never leave the search index (search_chat / read_turn).
//
// "hi" → pizza → rocket → "back to the pizza": the last prompt carries the pizza turns
// and the pizza note, not the rocket turns, unless something about the rocket was marked
// important (a deadline, a decision, a preference), which is carried over as one line.
//
// Segmentation is cheap first: keywords + recency + explicit cues ("back to the pizza",
// "new topic"). Only a message with no overlap with any topic and no anaphora goes to
// an optional Judge (one small model call). Pure logic, no database: the state is a
// plain struct that internal/longchat persists.
package topics

import (
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Topic is one subject of the chat.
type Topic struct {
	ID      int                `json:"id"`
	Title   string             `json:"title"`
	Note    string             `json:"note,omitempty"`    // rolling state note: Goal, Decisions, Facts, Open threads
	Upto    int                `json:"upto,omitempty"`    // first message index the note does not cover
	Terms   map[string]float64 `json:"terms,omitempty"`   // keyword bag: user mention 1, assistant mention 0.4
	First   int                `json:"first"`             // message index of its first user turn
	Last    int                `json:"last"`              // message index of its latest user turn
	Turns   int                `json:"turns"`             // user turns
	Carried int                `json:"carried,omitempty"` // first message index not yet scanned for carry-over
	Titled  bool               `json:"titled,omitempty"`  // the title came from a model or the user (keep it)
}

// Carry is something from one topic that matters in every topic: a deadline, a
// decision, a standing preference, an unfinished promise.
type Carry struct {
	Topic int    `json:"topic"`
	Turn  int    `json:"turn"`
	Kind  string `json:"kind"` // deadline | important | preference | decision | promise
	Text  string `json:"text"`
}

// State is everything topic-related about one chat.
type State struct {
	Active    int         `json:"active"`   // current topic (0 = none yet / only small talk)
	Assigned  int         `json:"assigned"` // messages [0, Assigned) have been read
	NextID    int         `json:"next_id"`
	Topics    []*Topic    `json:"topics"`
	TurnTopic map[int]int `json:"turn_topic"` // user message index → topic id (0 = small talk)
	LastUser  int         `json:"last_user"`  // index of the latest user message read (-1 = none)
	Carry     []Carry     `json:"carry,omitempty"`
}

// New is an empty state.
func New() *State { return &State{NextID: 1, TurnTopic: map[int]int{}, LastUser: -1} }

// Get returns a topic by id (nil if unknown).
func (s *State) Get(id int) *Topic {
	for _, t := range s.Topics {
		if t.ID == id {
			return t
		}
	}
	return nil
}

// Kinds of decision.
const (
	Same      = "same"
	Return    = "return"
	NewTopic  = "new"
	SmallTalk = "smalltalk"
)

// Decision is where a user message goes.
type Decision struct {
	Topic int     // 0 for small talk
	Kind  string  // same | return | new | smalltalk
	Score float64 // best lexical score
	Why   string  // cue | lexical | followup | judge | fallback | smalltalk
	Title string  // for a new topic
}

// Judge is the optional small model call for an ambiguous message: it returns the id
// of the topic the message belongs to, or 0 for a new topic (and a short title).
type Judge func(text string, active *Topic, topics []*Topic) (id int, title string, err error)

// ---------- words ----------

var wordRe = regexp.MustCompile(`[\p{L}\p{N}][\p{L}\p{N}'’-]*`)

var stop = set(`a an the and or but if then so of to in on at by for with from into onto about over under as is are was were be been being am do does did doing done have has had having i me my mine we us our you your yours he him his she her it its they them their this that these those there here what which who whom whose when where why how not no yes can could would should will shall may might must just also too very really quite much many more most less some any all each every other another such own same than only still even ever again now then once up down out off s t d ll re ve m don doesn didn isn aren wasn weren won wouldn couldn shouldn let lets let's i'm i've i'll i'd you're you've it's that's what's there's here's
make made making build building built create creating write writing help helping want wants wanted need needs needed use using used get getting got give gave show tell told know think thought like try tried trying go going went come put keep see look looking find work works please thanks thank ok okay sure yeah yep hey hi hello
today tonight tomorrow yesterday later soon morning evening night week time times day days
good best better great nice cool new old quick quickly simple easy right wrong real thing things stuff way ways lot lots bit kind sort maybe probably actually basically something anything everything nothing someone one two first last next`)

var smallTalkWords = set(`hi hello hey hiya yo howdy greetings morning afternoon evening night good thanks thank thx ty cheers ok okay k kk cool great nice awesome perfect lovely sweet fine alright yes yeah yep yup no nope nah sure bye goodbye cya later see soon lol lmao haha hehe wow oh ah hmm how are you doing going it's its is that this me much very so all for well up what sup gn gm np welcome appreciate appreciated got understood`)

var anaphora = set(`it its that this those these them they there more also too instead again another same then why how one ones`)

func set(s string) map[string]bool {
	m := map[string]bool{}
	for _, w := range strings.Fields(s) {
		m[w] = true
	}
	return m
}

func lowerWords(text string) []string {
	ws := wordRe.FindAllString(strings.ToLower(text), -1)
	for i, w := range ws {
		ws[i] = strings.Trim(strings.ReplaceAll(w, "’", "'"), "'-")
	}
	return ws
}

// Stem is a tiny English stemmer (plurals, possessives, -ing/-ed), enough to match
// "pizzas" with "pizza" and "fins" with "fin".
func Stem(w string) string {
	w = strings.TrimSuffix(w, "'s")
	n := len(w)
	switch {
	case n > 4 && strings.HasSuffix(w, "ies"):
		return w[:n-3] + "y"
	case n > 4 && (strings.HasSuffix(w, "ches") || strings.HasSuffix(w, "shes") || strings.HasSuffix(w, "sses") || strings.HasSuffix(w, "xes")):
		return w[:n-2]
	case n > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss") && !strings.HasSuffix(w, "us") && !strings.HasSuffix(w, "is"):
		return w[:n-1]
	case n > 6 && strings.HasSuffix(w, "ing"):
		return w[:n-3]
	case n > 5 && strings.HasSuffix(w, "ed"):
		return w[:n-2]
	}
	return w
}

// contentWord: not a stopword, 2+ letters; numbers only with 3+ digits (500g yes, "2" no).
func contentWord(w string) bool {
	if len([]rune(w)) < 2 || stop[w] {
		return false
	}
	for _, r := range w {
		if !unicode.IsDigit(r) {
			return true
		}
	}
	return len(w) >= 3
}

// Terms are the stemmed content words of text, in order, de-duplicated.
func Terms(text string) []string {
	seen := map[string]bool{}
	var out []string
	for _, w := range lowerWords(text) {
		if !contentWord(w) {
			continue
		}
		s := Stem(w)
		if stop[s] || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

// IsSmallTalk is a greeting, thanks, ok, bye: nothing to attach to a topic.
func IsSmallTalk(text string) bool {
	ws := lowerWords(text)
	if len(ws) == 0 || len(ws) > 8 {
		return len(ws) == 0
	}
	hit := false
	for _, w := range ws {
		switch {
		case smallTalkWords[w]:
			hit = true
		case stop[w]:
		default:
			return false
		}
	}
	return hit
}

func anaphoric(text string) bool {
	ws := lowerWords(text)
	if len(ws) > 12 {
		return false
	}
	for _, w := range ws {
		if anaphora[w] {
			return true
		}
	}
	return false
}

// ---------- cues ----------

var (
	returnCue = regexp.MustCompile(`(?i)\b(?:back to|return(?:ing)? to|go(?:ing)? back to|get(?:ting)? back to|regarding|re:|as for|what about|how about|about (?:that|the|those|our|my)|remember (?:the|that|our|my)|earlier|the other day|you (?:said|mentioned|suggested) (?:about|for)?|we (?:talked|discussed|spoke) about)\s+(?:the|that|our|my|those|this|your)?\s*([^.?!,;:\n]{2,60})`)
	newCue    = regexp.MustCompile(`(?i)\b(?:new topic|different (?:topic|question|subject)|unrelated(?: question)?|change of subject|chang(?:e|ing) the subject|switch(?:ing)? (?:gears|topics?)|on (?:a|an) (?:different|unrelated|another|separate) (?:note|topic)|something (?:else|different|completely different)|another (?:question|topic|thing)|separate question|totally different)\b`)
)

// scoreOn rates how well a topic matches terms (0..1). Words no topic has used weigh 1;
// words several topics share weigh less than words only one topic uses, and a word
// every topic uses (3+ topics) says almost nothing.
func (s *State) scoreOn(t *Topic, terms []string) float64 {
	if len(terms) == 0 || t == nil {
		return 0
	}
	n := float64(len(s.Topics))
	var tot, hit float64
	for _, w := range terms {
		df := 0.0
		for _, o := range s.Topics {
			if o.Terms[w] > 0 {
				df++
			}
		}
		wt := 1.0
		switch {
		case df > 0 && df == n && n >= 3:
			wt = 0.3
		case df > 0:
			wt = 1 + math.Log(n/df)
		}
		tot += wt
		hit += wt * math.Min(1, t.Terms[w])
	}
	if tot == 0 {
		return 0
	}
	return hit / tot
}

// best is the best topic for terms other than skip, and its score (ties: more recent).
func (s *State) best(terms []string, skip int) (*Topic, float64) {
	var bt *Topic
	bs := 0.0
	for _, t := range s.Topics {
		if t.ID == skip {
			continue
		}
		sc := s.scoreOn(t, terms)
		if sc > bs || (sc == bs && sc > 0 && bt != nil && t.Last > bt.Last) {
			bt, bs = t, sc
		}
	}
	return bt, bs
}

func (s *State) lastTouched() *Topic {
	var bt *Topic
	for _, t := range s.Topics {
		if bt == nil || t.Last > bt.Last {
			bt = t
		}
	}
	return bt
}

// Thresholds of the lexical decision.
const (
	ReturnScore   = 0.4  // an older topic this well matched…
	ReturnMargin  = 0.15 // …and this much better than the active one wins
	SameScore     = 0.15 // the active topic keeps a message that overlaps it this much
	OverlapReturn = 0.25 // no overlap with the active topic: an older topic this well matched
	CueScore      = 0.5  // "back to the X": X must match a topic this well
)

// Decide places one user message. judge may be nil (lexical only).
func (s *State) Decide(text string, judge Judge) Decision {
	if IsSmallTalk(text) {
		return Decision{Kind: SmallTalk, Why: "smalltalk"}
	}
	terms := Terms(text)
	active := s.Get(s.Active)

	// Explicit return cue: "back to the pizza", "about that rocket" (the title counts too).
	if m := returnCue.FindStringSubmatch(text); m != nil && len(s.Topics) > 0 {
		ct := Terms(m[1])
		bt, bs := s.best(ct, -1)
		for _, t := range s.Topics {
			if ts := titleScore(t.Title, ct); ts > bs {
				bt, bs = t, ts
			}
		}
		if bt != nil && bs >= CueScore {
			k := Return
			if active != nil && bt.ID == active.ID {
				k = Same
			}
			return Decision{Topic: bt.ID, Kind: k, Score: bs, Why: "cue"}
		}
	}
	if newCue.MatchString(text) {
		return Decision{Kind: NewTopic, Why: "cue", Title: Title(text)}
	}
	if len(terms) == 0 {
		// "make it pop", "why?": a follow-up of whatever we were on.
		if active == nil {
			active = s.lastTouched()
		}
		if active == nil {
			return Decision{Kind: SmallTalk, Why: "followup"}
		}
		return Decision{Topic: active.ID, Kind: Same, Why: "followup"}
	}
	if len(s.Topics) == 0 {
		return Decision{Kind: NewTopic, Why: "lexical", Title: Title(text)}
	}
	cur := s.scoreOn(active, terms)
	other, os := s.best(terms, s.Active)
	if active != nil && other != nil && os >= ReturnScore && os >= cur+ReturnMargin {
		return Decision{Topic: other.ID, Kind: Return, Score: os, Why: "lexical"}
	}
	if active != nil && cur >= SameScore {
		return Decision{Topic: active.ID, Kind: Same, Score: cur, Why: "lexical"}
	}
	if other != nil && os >= OverlapReturn {
		return Decision{Topic: other.ID, Kind: Return, Score: os, Why: "lexical"}
	}
	// No real overlap anywhere.
	if active == nil {
		return Decision{Kind: NewTopic, Why: "lexical", Title: Title(text)}
	}
	if anaphoric(text) {
		return Decision{Topic: active.ID, Kind: Same, Why: "followup"}
	}
	if judge != nil {
		if id, title, err := judge(text, active, s.recent(8)); err == nil {
			if id == 0 {
				return Decision{Kind: NewTopic, Why: "judge", Title: firstNonEmpty(cleanTitle(title), Title(text))}
			}
			if t := s.Get(id); t != nil {
				k := Return
				if t.ID == active.ID {
					k = Same
				}
				return Decision{Topic: t.ID, Kind: k, Why: "judge"}
			}
		}
	}
	return Decision{Kind: NewTopic, Why: "fallback", Title: Title(text)}
}

func titleScore(title string, terms []string) float64 {
	tt := Terms(title)
	if len(tt) == 0 || len(terms) == 0 {
		return 0
	}
	in := map[string]bool{}
	for _, w := range tt {
		in[w] = true
	}
	hit := 0
	for _, w := range terms {
		if in[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(terms))
}

// recent is the n most recently touched topics, newest first.
func (s *State) recent(n int) []*Topic {
	ts := append([]*Topic(nil), s.Topics...)
	sort.SliceStable(ts, func(i, j int) bool { return ts[i].Last > ts[j].Last })
	if len(ts) > n {
		ts = ts[:n]
	}
	return ts
}

// ---------- titles ----------

var determiners = set(`a an the my our some your this that his her their`)

// Title is a short heuristic name: the noun phrase after the first determiner ("make a
// pizza" → "Pizza", "build a model rocket" → "Model rocket"), or the first content words.
func Title(text string) string {
	ws := lowerWords(text)
	var words []string
	for i, w := range ws {
		if !determiners[w] {
			continue
		}
		var ph []string
		for _, x := range ws[i+1:] {
			if !contentWord(x) || determiners[x] {
				break
			}
			ph = append(ph, x)
			if len(ph) == 3 {
				break
			}
		}
		if len(ph) > 0 {
			words = ph
			break
		}
	}
	if len(words) == 0 {
		for _, w := range ws {
			if contentWord(w) {
				words = append(words, w)
				if len(words) == 3 {
					break
				}
			}
		}
	}
	if len(words) == 0 {
		return "Chat"
	}
	return cleanTitle(strings.Join(words, " "))
}

func cleanTitle(t string) string {
	t = strings.TrimSpace(strings.Trim(strings.TrimSpace(t), `"'.`))
	if r := []rune(t); len(r) > 40 {
		t = string(r[:40])
	}
	if t == "" {
		return ""
	}
	r := []rune(t)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}
