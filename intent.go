package main

import (
	"regexp"
	"strings"

	"blaze-desktop/internal/bgrun"
)

// v0.19.13 (Bug Diver review 2026-10-22, holes 3 and 4): a turn whose user plainly asked
// for something only a tool can do. Its first step is sent with tool_choice "required"
// (like CLI 0.14.15), the keyless plain fallback is off for it, and a reply that claims
// the work is done while no tool ran this turn is sent back once (like Mobile ActionProof).

var smallTalkWords = map[string]bool{
	"hi": true, "hello": true, "hey": true, "yo": true, "hiya": true, "howdy": true, "sup": true,
	"thanks": true, "thank": true, "thx": true, "ty": true, "cheers": true, "ok": true, "okay": true,
	"cool": true, "nice": true, "great": true, "good": true, "morning": true, "evening": true,
	"afternoon": true, "night": true, "bye": true, "goodbye": true, "you": true, "so": true,
	"much": true, "very": true, "awesome": true, "perfect": true, "lol": true, "there": true,
	"blaze": true, "gm": true, "gn": true, "np": true,
}

func smallTalk(s string) bool {
	f := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return r == ' ' || r == ',' || r == '.' || r == '!' || r == '\'' })
	if len(f) == 0 || len(f) > 5 {
		return false
	}
	for _, w := range f {
		if !smallTalkWords[w] {
			return false
		}
	}
	return true
}

var actionCue = regexp.MustCompile(`(?i)^\s*(please\s+|pls\s+|can you\s+|could you\s+|would you\s+|now\s+|then\s+|go ahead and\s+)*(run|execute|exec|create|add (a |an |the )?(file|line|section|function|test)|add .{1,60} to [\w./-]+\.\w+|make (a |an |me )?(file|folder|dir)|(make|build|create|generate|give me|produce) (a |an |me |the )?(\w+ ){0,2}(report|chart|graph|plot|pdf|spreadsheet)\b|(chart|plot|graph|analy[sz]e|clean up) [\w./-]+\.(csv|tsv|xlsx)|write (a |the |to )?(file|to)|edit|replace|rename|delete|remove|move|copy the file|read|open|cat|list|ls|show me (the )?(file|contents|readme)|search|look (for|up)|find|fetch|download|install|commit|push|remember|save|check (my |the )?(memory|weather)|recall|google|set (a |an |me )?(reminder|alarm|timer)|remind me|schedule|post (it |this |that )?(on|to)|send|email|text|take a screenshot|screenshot|update blaze|fix (the |this |my |a |that )?(failing |broken )?(tests?|build|bug|error|crash|script|code in|file)|build (and run|the project|the app|it)|compile|deploy|publish|upload|tweet|uninstall|restart)\b|\b(weather|forecast)\b.*\b(in|at|for|today|now|tomorrow)\b|\b(latest|today'?s|this week'?s|breaking)\b.*\bnews\b|\bnews\b.*\b(latest|today|this week)\b|\b(what (have|did) we (work|worked|do|done)|worked on (together|before|last)|last (session|time we|chat)|earlier (chat|conversation|session)|what do you remember)\b`)

// actionIntent is true when the user plainly asked for an action.
func actionIntent(s string) bool {
	s = strings.TrimSpace(stripAttached(s))
	s = strings.TrimPrefix(s, "[Design edit] ")
	if s == "" || len(s) > 2000 || smallTalk(s) {
		return false
	}
	return actionCue.MatchString(s)
}

// claimCue is a reply saying the work happened ("Done.", "I've created…", "Posted!").
var claimCue = regexp.MustCompile(`(?i)(^\s*(done|all set|posted|sent|saved|created|finished|completed|fixed|published|deployed|uploaded|scheduled|deleted)\b|\b(i'?ve|i have|i just|successfully)\s+(now\s+)?(run|ran|created|written|wrote|saved|edited|updated|deleted|removed|moved|renamed|installed|posted|sent|emailed|scheduled|set|added|committed|pushed|downloaded|fixed|executed|remembered|searched|fetched|opened|published|deployed|uploaded|tweeted|compiled|launched|restarted|uninstalled)\b|\b(has|have) been (successfully )?(created|saved|written|updated|deleted|removed|posted|sent|scheduled|set|added|committed|pushed|installed|fixed|published|deployed|uploaded|run|executed)\b|\breminder (is )?set\b|\ball tests pass(ed)?\b)`)

// claimsDone is true for a reply that says it did the work.
func claimsDone(text string) bool { return claimCue.MatchString(text) }

// noProofNote is sent back to a model that claimed an action while no tool ran.
const noProofNote = "Your last reply said the work was done, but no tool ran this turn, so nothing actually happened. Do it now with the tools, or tell the user plainly that you could not do it and why. Never say something was done without a tool result that shows it."

// noProofLine ends a reply that still claims an action no tool backed.
const noProofLine = "\n\n_Blaze didn't run any tool for this, so nothing was actually done._"

// questionCue is a turn that asks something ("what is…", "how do I…", "do you remember…")
// rather than telling Blaze to do it. "can you / could you / please" turns are requests.
var questionCue = regexp.MustCompile(`(?i)^\s*(what|what's|whats|how|why|who|whom|whose|when|where|which|is|are|am|do|does|did|was|were|should|shall|will|has|have|isn't|aren't|don't|doesn't)\b`)

// bgAction decides whether a user turn goes the ack-and-background way: only a request to DO
// something (a change on this computer or out in the world). Greetings, chat, questions,
// look-ups (read / list / recall), searches and summaries stay the classic streaming turn.
func bgAction(text string) (intent string, ok bool) {
	t := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(stripAttached(text)), "[Design edit] "))
	if !actionIntent(text) || questionCue.MatchString(t) {
		return "", false
	}
	intent, _ = bgrun.LocalAck(t, true)
	if bgrun.InfoIntent(intent) {
		return intent, false
	}
	return intent, true
}
