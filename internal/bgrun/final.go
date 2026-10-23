package bgrun

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Kinds of final.
const (
	KindDone   = "done"
	KindFailed = "failed"
)

// Final is how a run (Cloud or local) ended: exactly one of done or failed.
type Final struct {
	RunID   string
	Kind    string // done | failed
	Action  bool   // the request was an action (it had an ack): a done shows "Done ✓ …"
	Reply   string
	Receipt *Receipt
	// Device tools: DeviceRan is true when this computer ran a tool for the run, DeviceOK when
	// every one of them came back without an error. "Done ✓" needs DeviceOK (or no device tool).
	DeviceRan bool
	DeviceOK  bool
	// NoProof: a local action whose reply claims work no tool backed (the reply already says so).
	NoProof bool
	Intent  string // the ack's (or local check's) intent, when the receipt has none
	// LocalIntent: Intent is the app's own read of an action the gateway sent no ack for; it
	// outranks the receipt's intent (live: a "read, find, and create" run's receipt said search).
	LocalIntent bool
	Code        string // failed: the gateway's code (models_busy, expired, …)
	Reason      string // failed: honest, human
	Try         string // failed: what to do next
}

// intent is the receipt's intent, else the one the ack (or the local check) gave.
func (f Final) intent() string {
	if f.LocalIntent && f.Intent != "" {
		return f.Intent
	}
	if f.Receipt != nil && f.Receipt.Intent != "" {
		return f.Receipt.Intent
	}
	return f.Intent
}

// InfoIntent is an action that finishes with an answer, not a change on this computer (search,
// summarize): its done is the answer itself, never a "Done ✓".
func InfoIntent(intent string) bool {
	switch intent {
	case "", "chat", "search", "summarize", "lookup":
		return true
	}
	return false
}

// Confirmed is true when the chat may show "Done ✓": a done action that needed this computer,
// and whose tools on it ran and came back OK. A reply that only talks (asks a question, says it
// can't) is never confirmed, even with a receipt: live smoke 2026-10-23 had a reminder run end in
// "done" with the model asking for the date and no tool run.
func (f Final) Confirmed() bool {
	return f.Kind == KindDone && f.Action && !f.NoProof && !InfoIntent(f.intent()) && f.DeviceRan && f.DeviceOK &&
		!replyFails.MatchString(StripFollowups(f.Reply))
}

// replyFails is a reply saying the work didn't happen. Tools ran fine but the model still
// couldn't finish (live 0.19.14 CSV run 5: "I couldn't make that tool call. Please try again."
// was shown as "Done ✓").
var replyFails = regexp.MustCompile(`(?i)(\bi (couldn'?t|could not|can'?t|cannot|was unable to|wasn'?t able to|am unable to|was not able to)\b|\bunable to (complete|finish|make|run|create|do)\b|\bfailed to\b|\bplease try again\b|\b(it|that) (didn'?t|did not) work\b)`)

// followupsRe is the model's "<<follow-ups: a | b | c>>" line (the chat turns it into chips). It
// never belongs in a receipt summary or a notification. An unclosed one at the end counts too.
var followupsRe = regexp.MustCompile(`(?is)<<\s*follow-?ups\s*:[^>]*(>>|>|$)`)

// SplitFollowups splits a reply into its text and its follow-ups line ("" when there is none).
func SplitFollowups(s string) (body, followups string) {
	locs := followupsRe.FindAllStringIndex(s, -1)
	if len(locs) == 0 {
		return strings.TrimSpace(s), ""
	}
	l := locs[len(locs)-1]
	followups = strings.TrimSpace(s[l[0]:l[1]])
	body = strings.TrimSpace(followupsRe.ReplaceAllString(s, ""))
	return body, followups
}

// StripFollowups is s without any follow-ups line.
func StripFollowups(s string) string { b, _ := SplitFollowups(s); return b }

// summary is the receipt's summary, else the reply's first sentence (never the follow-ups line).
func (f Final) summary() string {
	if f.Receipt != nil {
		if s := StripFollowups(f.Receipt.Summary); s != "" {
			return s
		}
	}
	return FirstSentence(StripFollowups(f.Reply))
}

// NoToolLine ends an action reply that claims work no tool on this computer did.
const NoToolLine = "_Blaze didn't run any tool for this, so nothing was actually done._"

// replyClaim is a reply saying the work happened (the app's claim check, intent.go claimCue).
var replyClaim = regexp.MustCompile(`(?i)(^\s*(done|all set|posted|sent|saved|created|finished|completed|fixed|published|deployed|uploaded|scheduled|deleted)\b|\b(i'?ve|i have|i just|successfully)\s+(now\s+)?(run|ran|created|written|wrote|saved|edited|updated|deleted|removed|moved|renamed|installed|posted|sent|emailed|scheduled|set|added|committed|pushed|downloaded|fixed|executed|remembered|noted|stored)\b|\b(has|have) been (successfully )?(created|saved|written|updated|deleted|removed|posted|sent|scheduled|set|added|committed|pushed|installed|fixed|published|deployed|uploaded|run|executed|noted|stored)\b|\bis (now )?saved\b|\bsaved\b.*\b(memory|for later|for next time)\b|\breminder (is )?set\b)`)

// DeviceFailedLine ends a done reply whose tool on this computer didn't work.
const DeviceFailedLine = "_A step on this computer didn't work, so this may not be finished._"

// Message is what the chat shows for the final, as one plain Blaze bubble.
func (f Final) Message() string {
	if f.Kind != KindDone {
		reason := strings.TrimSpace(f.Reason)
		if reason == "" {
			reason = "Something went wrong before this could finish."
		}
		out := "Couldn't finish that. " + ensureStop(reason)
		if t := strings.TrimSpace(f.Try); t != "" {
			out += "\n\n" + ensureStop(t)
		}
		return out
	}
	reply := strings.TrimSpace(f.Reply)
	if !f.Action {
		return reply
	}
	if f.NoProof || InfoIntent(f.intent()) {
		return reply
	}
	if f.DeviceRan && !f.DeviceOK {
		if reply == "" {
			reply = "I couldn't confirm this finished."
		}
		return reply + "\n\n" + DeviceFailedLine
	}
	if !f.Confirmed() {
		// Nothing ran on this computer: the reply says what happened (often a question). It never
		// wears the chat's "Done ✓" mark, and a claim no tool backed says so.
		reply = strings.TrimSpace(strings.TrimPrefix(reply, "Done ✓"))
		if !f.DeviceRan && replyClaim.MatchString(StripFollowups(reply)) {
			body, fu := SplitFollowups(reply)
			reply = body + "\n\n" + NoToolLine
			if fu != "" {
				reply += "\n\n" + fu
			}
		}
		return reply
	}
	// The follow-ups line stays at the end of the bubble (chips) but never shapes the "Done ✓" text.
	body, fu := SplitFollowups(reply)
	withFU := func(s string) string {
		s = strings.TrimSpace(s)
		if fu == "" {
			return s
		}
		return s + "\n\n" + fu
	}
	sum := f.summary()
	if sum == "" {
		if body == "" {
			return withFU("Done ✓")
		}
		return withFU(body)
	}
	if body == "" || strings.EqualFold(strings.TrimSpace(FirstSentence(body)), sum) || strings.HasPrefix(body, sum) {
		if body == "" {
			body = sum
		}
		d := dropDone(body)
		if d == "" {
			return withFU("Done ✓")
		}
		return withFU("Done ✓ " + d)
	}
	if d := dropDone(sum); d != "" {
		return withFU("Done ✓ " + ensureStop(d) + "\n\n" + body)
	}
	return withFU("Done ✓ " + body)
}

// leadDone is a reply that itself opens with "Done." / "Done —" / "Done ✓".
var leadDone = regexp.MustCompile(`(?i)^done(\s*✓|\s*[.!,:;\x{2014}\x{2013}-](\s|$)|\s+|$)\s*`)

// dropDone keeps "Done ✓ Done. The file…" from saying done twice.
func dropDone(s string) string {
	if !leadDone.MatchString(s) {
		return s // nothing dropped: the reply stays exactly as written ("blaze-43-ok")
	}
	t := leadDone.ReplaceAllString(s, "")
	if strings.TrimSpace(t) == "" {
		return ""
	}
	// Capitalize a plain word ("the file…" → "The file…"), never a name or path ("out-1.txt").
	w := t
	if i := strings.IndexAny(w, " \n"); i >= 0 {
		w = w[:i]
	}
	for _, c := range w {
		if !unicode.IsLetter(c) {
			return t
		}
	}
	r := []rune(t)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// Notification is the one desktop notification for the final, worded like a text from Blaze
// ("Done, reminder set for Fri 9:00").
func (f Final) Notification() string {
	if f.Kind != KindDone {
		r := strings.TrimSpace(f.Reason)
		if r == "" {
			r = "something went wrong"
		}
		return clipRunes("Couldn't finish that: "+lowerFirst(strings.TrimSuffix(r, ".")), 180)
	}
	s := strings.TrimSuffix(f.summary(), ".")
	if s == "" {
		return "Done."
	}
	if f.Action && f.Confirmed() {
		if strings.HasPrefix(strings.ToLower(s), "done") {
			return clipRunes(s, 180)
		}
		return clipRunes("Done, "+lowerFirst(s), 180)
	}
	return clipRunes(s, 180)
}

var sentenceEnd = regexp.MustCompile(`[.!?](\s|$)`)

// FirstSentence is the first sentence of s (markdown marks dropped), at most 160 characters.
func FirstSentence(s string) string {
	s = strings.TrimSpace(strings.NewReplacer("**", "", "__", "", "`", "", "#", "").Replace(s))
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if loc := sentenceEnd.FindStringIndex(s); loc != nil {
		s = s[:loc[0]+1]
	}
	return clipRunes(strings.TrimSpace(s), 160)
}

func ensureStop(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r, _ := utf8.DecodeLastRuneInString(s)
	if r == '.' || r == '!' || r == '?' {
		return s
	}
	return s + "."
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	// Keep acronyms and names that start with two capitals ("PDF", "GitLab" stays as is when the
	// next letter is upper too).
	if len(s) > n {
		r2, _ := utf8.DecodeRuneInString(s[n:])
		if unicode.IsUpper(r2) {
			return s
		}
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func clipRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return strings.TrimSpace(string(r[:n-1])) + "…"
}

// ---------------- the ack line ----------------

// claimDone is an ack line that says the work already happened. The gateway's lines are
// templates that never do this; the guard is here so no ack can ever read as finished.
var claimDone = regexp.MustCompile(`(?i)(^\s*(done|all set|finished|completed|sent|saved|set)\b|✓|\b(is|are|has been|have been|was|were) (now )?(done|set|sent|saved|created|finished|completed|scheduled)\b|\bi'?ve (set|sent|saved|created|scheduled|finished|done)\b)`)

// AckLine is the line to show for a gateway ack: "" when there is no ack (greeting, thanks,
// chat, a question) or when the line would claim the work is done.
func AckLine(a *Ack) string {
	if a == nil {
		return ""
	}
	l := strings.TrimSpace(a.Line)
	if l == "" || ClaimsDone(l) {
		return ""
	}
	return l
}

// ackRe is an ack line (the gateway's templates and the local ones): a one-line promise.
var ackRe = regexp.MustCompile(`(?i)^\s*(got it|on it|okay|ok|sure)\b[^\n]{0,200}\bi'?ll (confirm|let you know|tell you|report back|send|update you|ping you)\b[^\n]{0,80}$`)

// IsAckLine is true for an ack bubble kept in the chat ("On it. I'll confirm when it's done.").
// Acks are for the user; a model that sees a history full of them starts answering with one
// instead of doing the work (live 0.19.14 files run with prior history).
func IsAckLine(s string) bool {
	s = strings.TrimSpace(s)
	return s != "" && len(s) < 300 && !strings.Contains(s, "\n") && ackRe.MatchString(s)
}

// ClaimsDone is true for a line that says something already happened.
func ClaimsDone(s string) bool { return claimDone.MatchString(s) }

// imperative is where a verb tells Blaze to do something: the start of the request (after
// "please", "can you"…) or a later clause ("…, and create out.txt", "then commit"). A verb inside
// a search phrase ("search for how to run Go tests") is not a request to run anything.
const imperative = `(^\s*(please\s+|pls\s+|can you\s+|could you\s+|would you\s+|now\s+|then\s+|go ahead and\s+)*|\b(and|then|also)\s+(also\s+)?(please\s+)?|[,;:]\s*(and\s+|then\s+)?(please\s+)?)`

// Local intents (local / own-key mode), in the gateway's words.
var localKinds = []struct {
	re     *regexp.Regexp
	intent string
	line   string
}{
	{regexp.MustCompile(`(?i)\b(remind me|set (a |an |me )?reminder|reminder)\b`), "reminder", "Got it, setting that reminder. I'll confirm when it's set."},
	{regexp.MustCompile(`(?i)\b(alarm|wake me)\b`), "alarm", "Got it, setting that alarm. I'll confirm when it's set."},
	{regexp.MustCompile(`(?i)\btimer\b`), "timer", "Got it, starting that timer. I'll confirm when it's running."},
	{regexp.MustCompile(`(?i)\b(schedule|calendar|meeting|event)\b`), "calendar", "Got it, putting that on the calendar. I'll confirm when it's there."},
	{regexp.MustCompile(`(?i)^\s*(please\s+|can you\s+|could you\s+)?(send|email|text|post|tweet|publish|upload)\b`), "send", "Got it, sending that. I'll confirm when it's gone out."},
	{regexp.MustCompile(`(?i)` + imperative + `(fix|build|compile|test|refactor|deploy|commit|push|run|execute|install)\b`), "code", "On it. I'll tell you how it went when it's finished."},
	{regexp.MustCompile(`(?i)` + imperative + `(write|create|make|edit|rename|delete|remove|move|save|change|replace|append|add)\b|^\s*(please\s+|can you\s+|could you\s+)?remember\b|\bremember (that|this|my|i)\b`), "write", "On it. I'll confirm when it's done."},
	// Answers (search, summarize, look-ups) come after every kind that changes something, so a
	// mixed request ("read notes.txt, find the word, and create out.txt") is the action it ends in.
	{regexp.MustCompile(`(?i)\b(search|look (for|up)|find|google|fetch|news|weather|forecast)\b`), "search", "On it, looking that up. I'll report back with what I find."},
	{regexp.MustCompile(`(?i)\b(summari[sz]e|tl;?dr|sum up)\b`), "summarize", "On it, reading that through. I'll send the summary when it's ready."},
	// look-ups on this computer or in memory: read / open / list / show / recall. They end in an
	// answer, so they stay a normal streaming turn (InfoIntent).
	{regexp.MustCompile(`(?i)\b(read|open|cat|list|ls|show me|recall|remember|what (have|did) we|worked on|last (session|time)|earlier (chat|conversation|session))\b`), "lookup", "On it, looking that up. I'll report back with what I find."},
}

// LocalAck is the ack for a local / own-key turn: only for an action request (isAction comes
// from the app's action-intent check, which says no for greetings, thanks and chat), never a
// claim that it already happened. "" means no ack: the chat shows the normal typing state.
func LocalAck(text string, isAction bool) (intent, line string) {
	if !isAction {
		return "", ""
	}
	for _, k := range localKinds {
		if k.re.MatchString(text) {
			return k.intent, k.line
		}
	}
	return "task", "On it. I'll tell you when it's done."
}
