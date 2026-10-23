package llm

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Some free models (nemotron, some qwen and llama builds) answer a tool call as
// plain text instead of through tool_calls, for example
//
//	dispatch_agent: about_blaze
//	{"topic": "crew"}
//
// or <tool_call>{"name":…,"arguments":…}</tool_call>, or a fenced JSON object.
// Shown as is, the user saw raw tool syntax and nothing ran (v0.14.14 fix).
// ExtractTextCalls recovers such calls so they run like native ones; whatever
// prose is left stays the answer text.

var (
	reTagCall  = regexp.MustCompile(`(?s)<(tool_call|toolcall|tool_use|function_call)>\s*(.*?)\s*(?:</(?:tool_call|toolcall|tool_use|function_call)>|$)`)
	reFuncTag  = regexp.MustCompile(`(?s)<function=([A-Za-z0-9_.:\-]+)>\s*(.*?)\s*(?:</function>|$)`)
	reParamTag = regexp.MustCompile(`(?s)<parameter=([A-Za-z0-9_\-]+)>\s*(.*?)\s*</parameter>`)
	reFence    = regexp.MustCompile("(?s)```[a-zA-Z_]*\\s*\\n(.*?)\\n?\\s*```")
	// A line that names a tool: "about_blaze", "dispatch_agent: about_blaze",
	// "functions.about_blaze:", "call about_blaze", "about_blaze(" …
	reNameLine = regexp.MustCompile(`^\s*(?:[A-Za-z_][A-Za-z0-9_.\-]*\s*[:>]\s*)?(?:call\s+)?([A-Za-z_][A-Za-z0-9_.:\-]*?)\s*[:(]?\s*$`)
)

// ExtractTextCalls finds tool calls written into text. known are the tool names
// offered on this step; a call to anything else is not a call. It returns the
// text with the calls removed and the calls (with ids text_call_N).
func ExtractTextCalls(text string, known []string) (string, []ToolCall) {
	if len(known) == 0 || strings.TrimSpace(text) == "" {
		return text, nil
	}
	var calls []ToolCall
	add := func(name, args string) bool {
		n := ResolveName(name, known)
		if n == "" {
			return false
		}
		obj, ok := RepairJSONObject(args)
		if !ok {
			// {"tool":"memory_search","input":"sister"}: a bare string for the tool's main argument.
			a := strings.TrimSpace(args)
			if a == "" || strings.HasPrefix(a, "{") || PrimaryArg == nil {
				return false
			}
			key := PrimaryArg(n)
			if key == "" {
				return false
			}
			obj = map[string]any{key: a}
		}
		b, _ := json.Marshal(obj)
		calls = append(calls, ToolCall{ID: fmt.Sprintf("text_call_%d", len(calls)), Type: "function",
			Function: struct {
				Name      string `json:"name"`
				Arguments string `json:"arguments"`
			}{n, string(b)}})
		return true
	}
	rest := text

	// 1. <tool_call>{json}</tool_call> and friends.
	rest = reTagCall.ReplaceAllStringFunc(rest, func(m string) string {
		g := reTagCall.FindStringSubmatch(m)
		if name, args, ok := envelope(g[2]); ok && add(name, args) {
			return ""
		}
		if f := reFuncTag.FindStringSubmatch(g[2]); f != nil && add(f[1], funcArgs(f[2])) {
			return ""
		}
		return m
	})
	// 2. <function=name>…</function> (qwen3-coder / nemotron XML form).
	rest = reFuncTag.ReplaceAllStringFunc(rest, func(m string) string {
		g := reFuncTag.FindStringSubmatch(m)
		if add(g[1], funcArgs(g[2])) {
			return ""
		}
		return m
	})
	// 3. A fenced block that is exactly a call envelope.
	rest = reFence.ReplaceAllStringFunc(rest, func(m string) string {
		g := reFence.FindStringSubmatch(m)
		if name, args, ok := envelope(g[1]); ok && add(name, args) {
			return ""
		}
		return m
	})
	// 4. The whole text is one envelope (or a JSON list of them).
	if t := strings.TrimSpace(rest); strings.HasPrefix(t, "{") || strings.HasPrefix(t, "[") {
		if name, args, ok := envelope(t); ok && add(name, args) {
			rest = ""
		} else if strings.HasPrefix(t, "[") {
			var list []json.RawMessage
			if json.Unmarshal([]byte(t), &list) == nil && len(list) > 0 {
				n0 := len(calls)
				all := true
				for _, it := range list {
					name, args, ok := envelope(string(it))
					if !ok || !add(name, args) {
						all = false
						break
					}
				}
				if all {
					rest = ""
				} else {
					calls = calls[:n0]
				}
			}
		}
	}
	// 5. A line naming a known tool, followed by its JSON arguments.
	rest = nameThenJSON(rest, known, add)
	// 6. An envelope written after prose on the same line: "Let's call x.{"tool":"x",...}".
	rest = inlineEnvelopes(rest, add)

	if len(calls) == 0 {
		return text, nil
	}
	return strings.TrimSpace(rest), calls
}

// PrimaryArg names a tool's main argument (its first required parameter), so a call
// whose arguments are a bare string can still run. Set by the app from its tool schemas.
var PrimaryArg func(tool string) string

// inlineEnvelopes finds call envelopes embedded in prose and turns them into calls.
func inlineEnvelopes(text string, add func(name, args string) bool) string {
	out := text
	for from := 0; from < len(out); {
		i := strings.Index(out[from:], "{\"")
		if i < 0 {
			break
		}
		i += from
		end := matchBrace(out, i)
		if end < 0 {
			break
		}
		if name, args, ok := envelope(out[i : end+1]); ok && add(name, args) {
			out = out[:i] + out[end+1:]
			from = i
			continue
		}
		from = i + 1
	}
	return out
}

// matchBrace returns the index of the brace closing the one at i, honoring JSON strings.
func matchBrace(s string, i int) int {
	depth, inStr, esc := 0, false, false
	for j := i; j < len(s); j++ {
		c := s[j]
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return j
			}
		}
	}
	return -1
}

// envelope reads {"name":…,"arguments":…} in its common spellings.
func envelope(s string) (name, args string, ok bool) {
	obj, good := RepairJSONObject(s)
	if !good {
		return "", "", false
	}
	if fn, isMap := obj["function"].(map[string]any); isMap {
		obj = fn
	}
	for _, k := range []string{"name", "tool", "tool_name", "function", "recipient_name"} {
		if v, isStr := obj[k].(string); isStr && v != "" {
			name = v
			break
		}
	}
	if name == "" {
		return "", "", false
	}
	for _, k := range []string{"arguments", "parameters", "args", "input", "params"} {
		switch v := obj[k].(type) {
		case string:
			return name, v, true
		case map[string]any:
			b, _ := json.Marshal(v)
			return name, string(b), true
		}
	}
	// {"name": "x"} with no arguments is a call with none, but only when the
	// object holds nothing else (otherwise it is likely just data).
	if len(obj) == 1 {
		return name, "{}", true
	}
	return "", "", false
}

// funcArgs turns <parameter=k>v</parameter>… (or a JSON body) into JSON.
func funcArgs(body string) string {
	ps := reParamTag.FindAllStringSubmatch(body, -1)
	if len(ps) == 0 {
		if strings.TrimSpace(body) == "" {
			return "{}"
		}
		return body
	}
	m := map[string]any{}
	for _, p := range ps {
		v := strings.TrimSpace(p[2])
		var j any
		if json.Unmarshal([]byte(v), &j) == nil {
			m[p[1]] = j
		} else {
			m[p[1]] = v
		}
	}
	b, _ := json.Marshal(m)
	return string(b)
}

// nameThenJSON finds "<tool line>\n{json}" or "<tool> {json}" blocks.
func nameThenJSON(text string, known []string, add func(name, args string) bool) string {
	lines := strings.Split(text, "\n")
	var out []string
	for i := 0; i < len(lines); i++ {
		line := lines[i]
		name, inline := splitNameLine(line, known)
		if name == "" {
			out = append(out, line)
			continue
		}
		// The arguments: on the same line after the name, or the JSON object
		// that starts on the next non-empty line and ends where its braces close.
		js, j := inline, i
		if strings.TrimSpace(js) == "" {
			k := i + 1
			for k < len(lines) && strings.TrimSpace(lines[k]) == "" {
				k++
			}
			if k < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[k]), "{") {
				var b strings.Builder
				for j = k; j < len(lines); j++ {
					b.WriteString(lines[j])
					b.WriteByte('\n')
					if balanced(b.String()) {
						break
					}
				}
				if j == len(lines) {
					j--
				}
				js = b.String()
			} else {
				js = ""
			}
		}
		js = strings.TrimSuffix(strings.TrimSpace(js), ")")
		if js != "" && add(name, js) {
			i = j
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}

// splitNameLine reports the tool a line names and any JSON after it on the line.
func splitNameLine(line string, known []string) (string, string) {
	t := strings.TrimSpace(line)
	if t == "" || len(t) > 4000 {
		return "", ""
	}
	head, tail := t, ""
	if i := strings.IndexAny(t, "{("); i >= 0 {
		head, tail = t[:i], t[i:]
		if strings.HasPrefix(tail, "(") {
			tail = tail[1:]
		}
		if !strings.HasPrefix(strings.TrimSpace(tail), "{") {
			return "", ""
		}
	}
	g := reNameLine.FindStringSubmatch(head)
	if g == nil {
		return "", ""
	}
	n := ResolveName(g[1], known)
	if n == "" {
		// "dispatch_agent: about_blaze" names the tool second; with a mangled
		// first word ("ispatch_agent") the regexp still puts the tool in g[1].
		return "", ""
	}
	// A bare English word on its own line ("Search") is prose, not a call.
	if !strings.Contains(n, "_") && !strings.ContainsAny(head, ":(>") && tail == "" {
		return "", ""
	}
	return n, tail
}

func balanced(s string) bool {
	depth, inStr, esc, seen := 0, false, false, false
	for _, r := range s {
		switch {
		case esc:
			esc = false
		case inStr && r == '\\':
			esc = true
		case r == '"':
			inStr = !inStr
		case inStr:
		case r == '{':
			depth++
			seen = true
		case r == '}':
			depth--
		}
	}
	return seen && depth <= 0
}

// RepairJSONObject parses s as a JSON object, repairing what weak models get
// wrong: code fences, single quotes, unquoted keys, trailing commas, Python
// literals and missing closing braces.
func RepairJSONObject(s string) (map[string]any, bool) {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(strings.TrimSuffix(s, "```"), "```json")
	s = strings.TrimSpace(strings.TrimPrefix(s, "```"))
	try := func(x string) (map[string]any, bool) {
		var m map[string]any
		if json.Unmarshal([]byte(x), &m) == nil && m != nil {
			return m, true
		}
		return nil, false
	}
	if m, ok := try(s); ok {
		return m, true
	}
	if !strings.HasPrefix(s, "{") {
		return nil, false
	}
	r := s
	// close what was left open
	depth, inStr, esc := 0, false, false
	for _, c := range r {
		switch {
		case esc:
			esc = false
		case inStr && c == '\\':
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
		}
	}
	if inStr {
		r += `"`
	}
	if depth > 0 {
		r += strings.Repeat("}", depth)
	}
	if m, ok := try(r); ok {
		return m, true
	}
	r = reTrailingComma.ReplaceAllString(r, "$1")
	r = reBareKey.ReplaceAllString(r, `$1"$2":`)
	if !strings.Contains(r, `"`) || strings.Count(r, "'") >= 2 && strings.Count(r, `"`) < 2 {
		r = strings.ReplaceAll(r, "'", `"`)
	}
	r = rePyTrue.ReplaceAllString(r, "${1}true")
	r = rePyFalse.ReplaceAllString(r, "${1}false")
	r = rePyNone.ReplaceAllString(r, "${1}null")
	return try(r)
}

var (
	reTrailingComma = regexp.MustCompile(`,\s*([}\]])`)
	reBareKey       = regexp.MustCompile(`([{,]\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:`)
	rePyTrue        = regexp.MustCompile(`([:\[,]\s*)True\b`)
	rePyFalse       = regexp.MustCompile(`([:\[,]\s*)False\b`)
	rePyNone        = regexp.MustCompile(`([:\[,]\s*)None\b`)
)

// LooksLikeToolText reports text that is tool syntax the extractor could not
// turn into a call (an unknown tool, broken arguments). It is never shown.
func LooksLikeToolText(text string, known []string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	low := strings.ToLower(t)
	for _, tag := range []string{"<tool_call", "<function=", "<tool_use", "<function_call", "<|tool", "<|python_tag|>"} {
		if strings.Contains(low, tag) {
			return true
		}
	}
	first := strings.SplitN(t, "\n", 2)[0]
	if n, _ := splitNameLine(first, known); n != "" {
		return true
	}
	if strings.HasPrefix(t, "{") {
		if _, _, ok := envelope(t); ok {
			return true
		}
		if strings.Contains(t, `"name"`) && (strings.Contains(t, `"arguments"`) || strings.Contains(t, `"parameters"`)) {
			return true
		}
	}
	return false
}
