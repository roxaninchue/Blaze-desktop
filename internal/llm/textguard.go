package llm

import "strings"

// TextGuard sits between the stream and the screen so tool syntax a model
// writes as text is never shown (v0.14.14). Prose passes through as it arrives;
// a line that could start a text-form call ("about_blaze", "dispatch_agent:",
// "{", "<tool_call>", a ```json fence) is held until it is clear. Once it is a
// call, everything after it is held too, and the caller decides with Held()
// once the step ends: run the recovered calls, or release the text.
type TextGuard struct {
	Known []string
	Out   func(string)

	line    strings.Builder // the current line, not yet passed or held
	passing bool            // the current line was judged prose: pass it on
	fence   bool            // holding a ``` block until it closes
	fenceB  strings.Builder
	rest    bool // holding everything from here on
	held    strings.Builder
}

var toolTags = []string{"<tool_call", "<toolcall", "<tool_use", "<function=", "<function_call", "<|tool", "<|python_tag|>", "<invoke"}

// Write takes a streamed chunk.
func (g *TextGuard) Write(s string) {
	for _, r := range s {
		g.put(r)
	}
}

func (g *TextGuard) emit(s string) {
	if s != "" && g.Out != nil {
		g.Out(s)
	}
}

func (g *TextGuard) put(r rune) {
	if g.rest {
		g.held.WriteRune(r)
		return
	}
	if g.fence {
		g.fenceB.WriteRune(r)
		if r == '\n' {
			lines := strings.Split(strings.TrimRight(g.fenceB.String(), "\n"), "\n")
			if len(lines) > 1 && strings.HasPrefix(strings.TrimSpace(lines[len(lines)-1]), "```") {
				g.fence = false
				block := g.fenceB.String()
				g.fenceB.Reset()
				if _, calls := ExtractTextCalls(block, g.Known); len(calls) > 0 {
					g.rest = true
					g.held.WriteString(block)
					return
				}
				g.emit(block)
			}
		}
		return
	}
	if g.passing {
		g.emit(string(r))
		if r == '\n' {
			g.passing = false
		}
		return
	}
	g.line.WriteRune(r)
	switch g.judge(r == '\n') {
	case vPass:
		g.passing = r != '\n'
		s := g.line.String()
		g.line.Reset()
		g.emit(s)
	case vHoldRest:
		g.rest = true
		g.held.WriteString(g.line.String())
		g.line.Reset()
	case vFence:
		g.fence = true
		g.fenceB.WriteString(g.line.String())
		g.line.Reset()
	}
}

type verdict int

const (
	vWait verdict = iota
	vPass
	vHoldRest
	vFence
)

// judge decides the current line from what has arrived of it.
func (g *TextGuard) judge(eol bool) verdict {
	l := g.line.String()
	t := strings.TrimLeft(l, " \t")
	if strings.TrimSpace(t) == "" {
		if eol {
			return vPass
		}
		return vWait
	}
	switch c := t[0]; {
	case c == '{':
		return vHoldRest
	case c == '<':
		low := strings.ToLower(t)
		for _, tag := range toolTags {
			if strings.HasPrefix(low, tag) || (strings.HasPrefix(tag, strings.TrimSpace(low)) && !eol) {
				if strings.HasPrefix(low, tag) {
					return vHoldRest
				}
				return vWait
			}
		}
		return vPass
	case c == '`':
		if strings.HasPrefix(t, "```") {
			if !eol {
				return vWait
			}
			lang := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(t), "```")))
			switch lang {
			case "", "json", "jsonc", "tool_call", "tool", "xml", "javascript", "js":
				return vFence
			}
			return vPass
		}
		if len(t) >= 3 || eol {
			return vPass
		}
		return vWait
	case isWordStart(c):
		end := strings.IndexFunc(t, func(r rune) bool { return !isWordRune(r) })
		if end < 0 && !eol {
			return vWait // the first word is still arriving
		}
		tok := t
		if end >= 0 {
			tok = t[:end]
		}
		if !strings.Contains(tok, "_") && ResolveName(tok, g.Known) == "" {
			return vPass
		}
		// "about_blaze …" / "dispatch_agent: about_blaze": wait for the line.
		if i := strings.IndexByte(t, '{'); i >= 0 || eol {
			head := strings.TrimRight(t, "\r\n")
			if n, _ := splitNameLine(head, g.Known); n != "" {
				return vHoldRest
			}
			if i >= 0 {
				if n, _ := splitNameLine(head[:i], g.Known); n != "" {
					return vHoldRest
				}
			}
			return vPass
		}
		if len(t) > 200 {
			return vPass
		}
		return vWait
	}
	return vPass
}

func isWordStart(c byte) bool { return c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' }

func isWordRune(r rune) bool {
	return r == '_' || r == '.' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
}

// Held is the text not shown yet (an unfinished line, an open fence, or
// everything after a suspected call). Calling it ends the guard's hold.
func (g *TextGuard) Held() string {
	s := g.held.String() + g.fenceB.String() + g.line.String()
	g.held.Reset()
	g.fenceB.Reset()
	g.line.Reset()
	g.rest, g.fence, g.passing = false, false, false
	return s
}

// Holding reports whether a suspected call is being held.
func (g *TextGuard) Holding() bool { return g.rest }
