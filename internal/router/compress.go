package router

// Prompt compression: a small, safe port of OmniRoute's RTK (tool-output trimming)
// and "Caveman" (shorter history) ideas. Nothing here changes what a line of code
// says: indentation is never touched, and long text is cut with a visible note.
//
//   off        nothing
//   light      trailing spaces and runs of blank lines (default; lossless for meaning)
//   standard   + tool output: colour codes stripped, repeated lines folded, and very
//              long outputs cut to their head and tail
//   aggressive + older turns (all but the last six messages) cut to their first
//              part, and tool output cut shorter

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

var (
	ansiRe   = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)
	blanksRe = regexp.MustCompile(`\n{3,}`)
)

// Compress shrinks the messages of a chat request in place and returns how many
// characters it removed.
func Compress(body map[string]any, level string) int {
	if level == CompressOff || level == "" {
		return 0
	}
	msgs, ok := body["messages"].([]any)
	if !ok {
		return 0
	}
	toolMax, oldMax := 0, 0
	switch level {
	case CompressStandard:
		toolMax = 8000
	case CompressAggressive:
		toolMax, oldMax = 3000, 2000
	}
	saved := 0
	for i, mm := range msgs {
		m, ok := mm.(map[string]any)
		if !ok {
			continue
		}
		role, _ := m["role"].(string)
		old := oldMax > 0 && i < len(msgs)-6 && role != "system"
		fix := func(s string) string {
			n := len(s)
			s = light(s)
			if role == "tool" && toolMax > 0 {
				s = toolOutput(s, toolMax)
			}
			if old && len(s) > oldMax {
				s = cut(s, oldMax*3/4, oldMax/8, "of an earlier message")
			}
			saved += n - len(s)
			return s
		}
		switch c := m["content"].(type) {
		case string:
			m["content"] = fix(c)
		case []any:
			for _, part := range c {
				if p, ok := part.(map[string]any); ok {
					if t, ok := p["text"].(string); ok {
						p["text"] = fix(t)
					}
				}
			}
		}
	}
	return saved
}

// light removes trailing spaces on each line and collapses runs of blank lines.
func light(s string) string {
	if !strings.ContainsAny(s, " \t\n") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return blanksRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n")
}

// toolOutput strips colour codes, folds repeated lines and cuts long output.
func toolOutput(s string, max int) string {
	s = ansiRe.ReplaceAllString(s, "")
	lines := strings.Split(s, "\n")
	var out []string
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && lines[j] == lines[i] {
			j++
		}
		out = append(out, lines[i])
		if n := j - i; n > 2 {
			out = append(out, fmt.Sprintf("[… the line above repeated %d more times]", n-1))
		} else if n == 2 {
			out = append(out, lines[i])
		}
		i = j
	}
	s = strings.Join(out, "\n")
	if len(s) > max {
		s = cut(s, max*2/3, max/3, "of tool output")
	}
	return s
}

// cut keeps head and tail characters (on line boundaries where it can) with a note.
func cut(s string, head, tail int, what string) string {
	if len(s) <= head+tail+80 {
		return s
	}
	for head > 0 && !utf8.RuneStart(s[head]) {
		head--
	}
	ts := len(s) - tail
	for ts < len(s) && !utf8.RuneStart(s[ts]) {
		ts++
	}
	h := s[:head]
	if i := strings.LastIndex(h, "\n"); i > head/2 {
		h = h[:i]
	}
	t := s[ts:]
	if i := strings.Index(t, "\n"); i >= 0 && i < tail/2 {
		t = t[i+1:]
	}
	removed := len(s) - len(h) - len(t)
	return h + fmt.Sprintf("\n[… BlazeRouter left out %d characters %s …]\n", removed, what) + t
}
