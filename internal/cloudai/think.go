package cloudai

// Port of Blaze Android's ThinkBudget.kt (v0.33.1): a tiny classifier from cheap features
// (length, hard / easy cue words, maths, code, questions in a row). A QUICK turn goes to the
// gateway's fast "quick" lineup; no model call, a few microseconds.

import (
	"regexp"
	"strings"
)

// Budget is how hard to think about one message.
type Budget int

const (
	Quick Budget = iota
	Normal
	Deep
)

var (
	deepCue = regexp.MustCompile(`(?i)\b(why|explain|prove|proof|derive|calculate|compute|solve|integrate|differentiate|probability|optimi[sz]e|compare|trade-?offs?|pros and cons|plan|strategy|design|architect\w*|debug|analy[sz]e|evaluate|critique|review|step[- ]by[- ]step|think (hard|carefully|it through)|reason|figure out|what if|should i|best way|recommend|diagnos\w*|algorithm|complexity|essay|research)\b`)
	easyCue = regexp.MustCompile(`(?i)^\s*(hi|hello|hey|yo|thanks|thank you|ok|okay|cool|nice|good (morning|night|evening|afternoon)|bye|lol|haha|yes|no|sure)\b|\b(fix (the )?typos|translate|summari[sz]e|tl;?dr|shorten|rephrase|rewrite|make it (shorter|longer|formal|casual)|open|turn (on|off)|call|text|play|pause|stop|set (an? )?(alarm|timer|reminder)|remind me|what time|what's the time|weather)\b`)
	maths   = regexp.MustCompile(`(\d\s*[-+*/^=<>]\s*\d)|[∫∑√π≤≥≠]|\b(sin|cos|tan|log|ln|lim|dx|dy)\b`)
	codeKw  = regexp.MustCompile("```|\\b(fun|def|class|function|return|import|const|SELECT|stack ?trace|traceback|exception)\\b")
)

// Level is the think budget for a message with n attachments.
func Level(text string, attachments int) Budget {
	t := strings.TrimSpace(text)
	if t == "" {
		if attachments > 0 {
			return Normal
		}
		return Quick
	}
	if deepCue.MatchString(t) || maths.MatchString(t) || codeKw.MatchString(t) || strings.Count(t, "?") >= 3 {
		return Deep
	}
	switch {
	case attachments > 0:
		return Normal
	case easyCue.MatchString(t):
		return Quick
	case len([]rune(t)) < 80:
		return Quick
	}
	return Normal
}
