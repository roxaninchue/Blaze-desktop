package runevents

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Specialty pills say what the help is FOR, in plain words taken from the event's own detail
// (its task, brief or step text), never which specialty, model or provider it is and never a
// fixed label. The general line (SpecialtyLoadedText / SpecialtyUpdatedText) is only the last
// resort when neither the event nor the user's request says anything usable (user, 2026-10-23).

var (
	genericHelp = regexp.MustCompile(`(?i)\b(using|updating|updated|loading|loaded|bringing in|brought in|briefing|applying)\s+(an?|the)?\s*(specialty|specialist)s?\b`)
	helpWord    = regexp.MustCompile(`(?i)\b(specialty|specialties|specialist|specialists|playbook|playbooks)\b`)
	slugName    = regexp.MustCompile(`\b[a-z0-9]+(?:-[a-z0-9]+)+\b`) // data-analyst, csv-pro (names, not words)
	parens      = regexp.MustCompile(`\([^)]*\)|\[[^\]]*\]`)
	viaWho      = regexp.MustCompile(`(?i)\b(via|on|using|with|through)\s+[A-Z][\w.-]*(\s+[\w.-]+)?\s*$`)
	leadAsk     = regexp.MustCompile(`(?i)^(please|pls|hey blaze|blaze|ok|okay|so|from now on|now|can you|could you|would you|will you|i need you to|i want you to|help me|go ahead and)[\s,]+`)
	nonVerb     = regexp.MustCompile(`(?i)^(what|who|whom|whose|why|how|when|where|which|is|are|was|were|am|do|does|did|can|could|would|will|should|shall|may|might|i|i'm|im|my|we|our|you|your|it|its|the|a|an|this|that|these|those|there|here|hi|hello|hey|thanks|thank)\b`)
	alreadyHelp = regexp.MustCompile(`^(Getting help|Saving what I learned|Saving your rule)\b`)
	trailPrep   = regexp.MustCompile(`(?i)(\s+(with|via|on|using|through|by|for|to|from|and))+$`)
	sentenceEnd = regexp.MustCompile(`[.!?;:](\s|$)`)
)

// cleanDetail is the detail with every name and label taken out; "" when nothing readable is left.
func cleanDetail(s string, names ...string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if loc := sentenceEnd.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]] // the first sentence is the goal
	}
	for _, n := range names {
		if n = strings.TrimSpace(n); len(n) >= 2 {
			s = regexp.MustCompile(`(?i)\b`+regexp.QuoteMeta(n)+`\b`).ReplaceAllString(s, "")
		}
	}
	s = parens.ReplaceAllString(s, "")
	s = genericHelp.ReplaceAllString(s, "")
	s = helpWord.ReplaceAllString(s, "")
	s = slugNameOutsideFiles(s)
	s = viaWho.ReplaceAllString(s, "")
	s = trailPrep.ReplaceAllString(strings.TrimSpace(s), "")
	s = Sanitize(s)
	s = strings.TrimSpace(strings.TrimRight(s, "."))
	for {
		t := leadAsk.ReplaceAllString(s, "")
		if t == s {
			break
		}
		s = t
	}
	s = strings.TrimLeft(s, " ,:;-")
	if utf8.RuneCountInString(s) < 4 || len(strings.Fields(s)) < 2 && !strings.ContainsAny(s, ".") {
		return "" // "loaded data-analyst" -> "": a bare name or one word says nothing
	}
	return s
}

func slugNameOutsideFiles(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range slugName.FindAllStringIndex(s, -1) {
		if m[1]+1 < len(s) && s[m[1]] == '.' && isWordByte(s[m[1]+1]) {
			continue // a file name (cafe-sales-sept.csv) is the real object: keep it
		}
		b.WriteString(s[last:m[0]])
		last = m[1]
	}
	b.WriteString(s[last:])
	return b.String()
}

func lowerFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	// keep acronyms and names that start with two capitals (CSV, API, PDF)
	if r2, _ := utf8.DecodeRuneInString(s[n:]); unicode.IsUpper(r2) {
		return s
	}
	return string(unicode.ToLower(r)) + s[n:]
}

func isGerund(s string) bool {
	f := strings.Fields(s)
	return len(f) > 0 && len(f[0]) > 4 && strings.HasSuffix(strings.ToLower(f[0]), "ing")
}

// HelpLine is the pill line for a specialty / specialist event. loaded: help is coming in for
// detail; updated: what was learned is being saved. request is the user's own message, used
// when the event carries no detail. names are the specialty's own names, never shown.
func HelpLine(updated bool, detail, request string, names ...string) string {
	if alreadyHelp.MatchString(detail) {
		return Sanitize(detail) // already a help line (built once upstream)
	}
	if d := cleanDetail(detail, names...); d != "" {
		if updated {
			return Sanitize("Saving what I learned: " + lowerFirst(d))
		}
		if isGerund(d) {
			return Sanitize("Getting help with " + lowerFirst(d))
		}
		if nonVerb.MatchString(d) {
			return Sanitize("Getting help with " + lowerFirst(d))
		}
		return Sanitize("Getting help to " + lowerFirst(d))
	}
	if r := cleanDetail(request, names...); r != "" {
		if updated {
			return Sanitize("Saving your rule: " + lowerFirst(r))
		}
		if nonVerb.MatchString(r) || isGerund(r) {
			return Sanitize("Getting help with: " + lowerFirst(r))
		}
		return Sanitize("Getting help to " + lowerFirst(r))
	}
	if updated {
		return SpecialtyUpdatedText
	}
	return SpecialtyLoadedText
}
