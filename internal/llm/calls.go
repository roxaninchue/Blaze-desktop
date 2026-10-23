package llm

// The tool-call gatekeeper (ported from the Android app's ToolCalls.kt). Models,
// free ones especially, send calls with glued names, broken JSON, made-up names or
// none at all. Everything here runs locally between the model and the tools, so a
// sloppy call is repaired or dropped instead of crashing the turn or being echoed
// back to the provider, which then answers 400 INVALID_ARGUMENT on every later turn.

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// completeJSON is true when s is exactly one complete JSON value.
func completeJSON(s string) bool {
	t := strings.TrimSpace(s)
	if t == "" {
		return false
	}
	dec := json.NewDecoder(strings.NewReader(t))
	var v any
	if dec.Decode(&v) != nil {
		return false
	}
	return !dec.More() && dec.InputOffset() == int64(len(t))
}

// splitObjects finds top-level JSON objects written back to back ("{}{\"q\":1}").
func splitObjects(s string) []string {
	var out []string
	depth, start := 0, -1
	inStr, esc := false, false
	for i, c := range s {
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			if depth == 0 {
				start = i
			}
			depth++
		case '}':
			if depth > 0 {
				depth--
				if depth == 0 && start >= 0 {
					out = append(out, s[start:i+1])
					start = -1
				}
			}
		}
	}
	return out
}

func tryObj(s string) map[string]any {
	var m map[string]any
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	if dec.Decode(&m) != nil || m == nil || dec.More() {
		return nil
	}
	return m
}

var (
	trailingComma = regexp.MustCompile(`,\s*([}\]])`)
	pyTrue        = regexp.MustCompile(`([:\[,\s])True\b`)
	pyFalse       = regexp.MustCompile(`([:\[,\s])False\b`)
	pyNone        = regexp.MustCompile(`([:\[,\s])None\b`)
	bareKey       = regexp.MustCompile(`([{,]\s*)([A-Za-z_][A-Za-z0-9_]*)\s*:`)
)

// ParseArgs is a best-effort parse of model-written arguments. ok is false only when
// nothing usable is there (the call must not be run or saved as-is).
func ParseArgs(raw string) (map[string]any, bool) {
	s := strings.TrimSpace(raw)
	if s == "" || s == "null" || s == "{}" {
		return map[string]any{}, true
	}
	s = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(s, "```json"), "```"), "```"))
	if m := tryObj(s); m != nil {
		return m, true
	}
	// A JSON string that itself holds JSON.
	if strings.HasPrefix(s, `"`) {
		var inner string
		if json.Unmarshal([]byte(s), &inner) == nil {
			if m := tryObj(inner); m != nil {
				return m, true
			}
		}
	}
	r := trailingComma.ReplaceAllString(s, "$1")
	if !strings.Contains(r, `"`) && strings.Contains(r, "'") {
		r = strings.ReplaceAll(r, "'", `"`)
	}
	r = pyTrue.ReplaceAllString(r, "${1}true")
	r = pyFalse.ReplaceAllString(r, "${1}false")
	r = pyNone.ReplaceAllString(r, "${1}null")
	r = bareKey.ReplaceAllString(r, `$1"$2":`)
	if m := tryObj(r); m != nil {
		return m, true
	}
	// Several objects glued together: keep the first.
	if objs := splitObjects(r); len(objs) > 1 {
		if m := tryObj(objs[0]); m != nil {
			return m, true
		}
	}
	// A cut-off stream: close the open brackets when the cut fell between values.
	if m := closeUp(r); m != nil {
		return m, true
	}
	return nil, false
}

// closeUp tries to finish JSON that was cut off mid-way.
func closeUp(s string) map[string]any {
	var stack []byte
	inStr, esc := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch {
			case esc:
				esc = false
			case c == '\\':
				esc = true
			case c == '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{':
			stack = append(stack, '}')
		case '[':
			stack = append(stack, ']')
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	// Cut off inside a string: the value is incomplete (a half-written file, say), so
	// it must not be run. Only a cut between values is safe to close.
	if len(stack) == 0 || inStr {
		return nil
	}
	t := strings.TrimRight(s, " \n\t,")
	if strings.HasSuffix(t, ":") {
		return nil
	}
	for i := len(stack) - 1; i >= 0; i-- {
		t += string(stack[i])
	}
	return tryObj(t)
}

func normKey(n string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(n) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// ResolveName maps "functions.file_read", "default_api:file_read", "FileRead" or a
// small typo to a known tool name. "" when there is no confident match.
func ResolveName(raw string, known []string) string {
	for _, k := range known {
		if k == raw {
			return k
		}
	}
	s := strings.TrimSpace(raw)
	for _, sep := range []string{".", ":", "/"} {
		if i := strings.LastIndex(s, sep); i >= 0 {
			s = s[i+1:]
		}
	}
	s = strings.Trim(s, "\"'` ")
	for _, k := range known {
		if k == s {
			return k
		}
	}
	nk := normKey(s)
	if nk == "" {
		return ""
	}
	for _, k := range known {
		if normKey(k) == nk {
			return k
		}
	}
	limit := 1
	if len(nk) > 8 {
		limit = 2
	}
	best, bestD, count := "", 99, 0
	for _, k := range known {
		d := lev(normKey(k), nk)
		if d <= limit {
			if d < bestD {
				best, bestD, count = k, d, 1
			} else if d == bestD {
				count++
			}
		}
	}
	if count == 1 {
		return best
	}
	return ""
}

// SplitGlued splits a name made of several known tool names glued together.
func SplitGlued(raw string, known []string) []string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil
	}
	byLen := append([]string(nil), known...)
	sort.Slice(byLen, func(i, j int) bool { return len(byLen[i]) > len(byLen[j]) })
	best := make([][]string, len(s)+1)
	best[0] = []string{}
	for i := 0; i < len(s); i++ {
		if best[i] == nil {
			continue
		}
		for _, n := range byLen {
			if strings.HasPrefix(s[i:], n) && best[i+len(n)] == nil {
				best[i+len(n)] = append(append([]string{}, best[i]...), n)
			}
		}
	}
	if r := best[len(s)]; len(r) > 1 {
		return r
	}
	return nil
}

func lev(a, b string) int {
	if a == b {
		return 0
	}
	d := make([]int, len(b)+1)
	for j := range d {
		d[j] = j
	}
	for i := 1; i <= len(a); i++ {
		prev := d[0]
		d[0] = i
		for j := 1; j <= len(b); j++ {
			tmp := d[j]
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			d[j] = min(d[j]+1, d[j-1]+1, prev+cost)
			prev = tmp
		}
	}
	return d[len(b)]
}

func encodeArgs(m map[string]any) string {
	if m == nil {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// Normalized is what the gatekeeper made of one reply's tool calls.
type Normalized struct {
	Calls   []ToolCall // safe to run and to save: known or unknown name, valid JSON arguments
	Unknown []string   // names that match no tool (answered with an error, never run)
	Dropped []string   // calls that could not be saved at all (no name / unreadable arguments)
	Repairs []string   // what was fixed, for the log
}

// NormalizeCalls repairs the calls of one assistant reply against the known tools.
func NormalizeCalls(calls []ToolCall, known []string) Normalized {
	var n Normalized
	ids := map[string]bool{}
	add := func(src ToolCall, name string, args map[string]any, i int, extra json.RawMessage) {
		id := strings.TrimSpace(src.ID)
		if id == "" {
			id = fmt.Sprintf("call_%d", i)
		}
		for ids[id] {
			id += "x"
		}
		ids[id] = true
		var c ToolCall
		c.ID, c.Type = id, "function"
		c.Function.Name = name
		c.Function.Arguments = encodeArgs(args)
		c.ExtraContent = extra
		n.Calls = append(n.Calls, c)
	}
	for i, c := range calls {
		raw := strings.TrimSpace(c.Function.Name)
		if raw == "" {
			n.Dropped = append(n.Dropped, "a call with no tool name")
			continue
		}
		if name := ResolveName(raw, known); name != "" {
			args, ok := ParseArgs(c.Function.Arguments)
			if !ok {
				n.Dropped = append(n.Dropped, name+" (its arguments weren't valid JSON)")
				continue
			}
			if name != raw {
				n.Repairs = append(n.Repairs, raw+" → "+name)
			}
			add(c, name, args, i, c.ExtraContent)
			continue
		}
		if parts := SplitGlued(raw, known); parts != nil {
			objs := splitObjects(c.Function.Arguments)
			n.Repairs = append(n.Repairs, "split "+raw+" into "+strings.Join(parts, ", "))
			for j, p := range parts {
				var args map[string]any
				if len(objs) == len(parts) {
					args, _ = ParseArgs(objs[j])
				} else if j == 0 && len(objs) > 0 {
					args, _ = ParseArgs(objs[0])
				}
				sub := c
				if c.ID != "" {
					sub.ID = fmt.Sprintf("%s_%d", c.ID, j)
				}
				var extra json.RawMessage
				if j == 0 {
					extra = c.ExtraContent
				}
				add(sub, p, args, i*10+j, extra)
			}
			continue
		}
		args, ok := ParseArgs(c.Function.Arguments)
		if !ok {
			n.Dropped = append(n.Dropped, raw+" (unknown tool, unreadable arguments)")
			continue
		}
		n.Unknown = append(n.Unknown, raw)
		add(c, raw, args, i, c.ExtraContent)
	}
	return n
}

// Sanitize returns a copy of msgs every provider accepts: tool calls have a name and
// valid JSON arguments, every call is answered by exactly one tool message right
// after it (in call order), stray tool messages become plain notes, and no assistant
// turn is empty. Old chats saved before v0.3 are cleaned the same way.
func Sanitize(msgs []Message) []Message {
	var out []Message
	for i := 0; i < len(msgs); i++ {
		m := msgs[i]
		if m.Role == "tool" {
			out = append(out, Message{Role: "user", Content: toolNote(m)})
			continue
		}
		if m.Role != "assistant" || len(m.ToolCalls) == 0 {
			if m.Role == "assistant" {
				m.ToolCalls = nil
				if strings.TrimSpace(m.Content) == "" {
					m.Content = "…"
				}
			}
			out = append(out, m)
			continue
		}
		var fixed []ToolCall
		seen := map[string]bool{}
		for j, c := range m.ToolCalls {
			name := strings.TrimSpace(c.Function.Name)
			if name == "" {
				continue
			}
			args, ok := ParseArgs(c.Function.Arguments)
			if !ok {
				continue
			}
			c.Function.Name = name
			c.Function.Arguments = encodeArgs(args)
			if c.ID == "" {
				c.ID = fmt.Sprintf("call_%d_%d", i, j)
			}
			for seen[c.ID] {
				c.ID += "x"
			}
			seen[c.ID] = true
			c.Type = "function"
			fixed = append(fixed, c)
		}
		// Collect the tool replies that follow.
		k := i + 1
		replies := map[string]Message{}
		var loose []Message
		for k < len(msgs) && msgs[k].Role == "tool" {
			t := msgs[k]
			if seen[t.ToolCallID] {
				if _, dup := replies[t.ToolCallID]; !dup {
					replies[t.ToolCallID] = t
					k++
					continue
				}
			}
			loose = append(loose, t)
			k++
		}
		if len(fixed) == 0 {
			m.ToolCalls = nil
			if strings.TrimSpace(m.Content) == "" {
				m.Content = "…"
			}
			out = append(out, m)
			for _, t := range loose {
				out = append(out, Message{Role: "user", Content: toolNote(t)})
			}
			i = k - 1
			continue
		}
		m.ToolCalls = fixed
		out = append(out, m)
		for _, c := range fixed {
			t, ok := replies[c.ID]
			if !ok && len(loose) > 0 && loose[0].ToolCallID == "" {
				t, ok = loose[0], true
				loose = loose[1:]
			}
			if !ok {
				t = Message{Role: "tool", Content: "(no result)"}
			}
			t.Role, t.ToolCallID = "tool", c.ID
			if t.Name == "" {
				t.Name = c.Function.Name
			}
			if strings.TrimSpace(t.Content) == "" {
				t.Content = "(empty)"
			}
			out = append(out, t)
		}
		for _, t := range loose {
			out = append(out, Message{Role: "user", Content: toolNote(t)})
		}
		i = k - 1
	}
	return out
}

func toolNote(t Message) string {
	n := ""
	if t.Name != "" {
		n = " from " + t.Name
	}
	return "[Tool result" + n + "]\n" + t.Content
}

// Flatten is the last resort when a provider still refuses the conversation: every
// tool turn becomes plain text. No ids, no signatures, nothing a strict API rejects.
func Flatten(msgs []Message) []Message {
	src := Sanitize(msgs)
	var out []Message
	for _, m := range src {
		switch m.Role {
		case "assistant":
			if len(m.ToolCalls) == 0 {
				out = append(out, m)
				continue
			}
			var parts []string
			for _, c := range m.ToolCalls {
				a := c.Function.Arguments
				if len(a) > 300 {
					a = a[:300] + "…"
				}
				parts = append(parts, c.Function.Name+"("+a+")")
			}
			said := strings.TrimSpace(m.Content)
			if said == "…" {
				said = ""
			}
			txt := "[I called: " + strings.Join(parts, "; ") + "]"
			if said != "" {
				txt = said + "\n" + txt
			}
			out = append(out, Message{Role: "assistant", Content: txt})
		case "tool":
			c := m.Content
			if len(c) > 8000 {
				c = c[:8000] + "…"
			}
			out = append(out, Message{Role: "user", Content: "[Result of " + m.Name + "]\n" + c})
		default:
			out = append(out, m)
		}
	}
	// Merge back-to-back user notes so strict providers that want alternating roles accept it.
	var merged []Message
	for _, m := range out {
		if n := len(merged); n > 0 && merged[n-1].Role == "user" && m.Role == "user" {
			merged[n-1].Content += "\n\n" + m.Content
			continue
		}
		merged = append(merged, m)
	}
	return merged
}

// IsShapeError is true for a provider refusing the request's shape (worth one flattened retry).
func IsShapeError(err error) bool {
	he, ok := err.(*HTTPError)
	if !ok {
		return false
	}
	low := strings.ToLower(he.Body)
	if he.Code == 400 || he.Code == 422 {
		// Key and model problems also come back as 400 on some providers; those aren't shape errors.
		for _, s := range []string{"api key", "api_key", "not found", "does not exist", "quota", "rate limit", "context", "too long", "too large"} {
			if strings.Contains(low, s) {
				return false
			}
		}
		return true
	}
	for _, s := range []string{"invalid_argument", "invalid argument", "tool_call", "function call", "thought_signature", "tool_use", "must be followed"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// TooLarge is true when the request was too big for the model or the plan.
func TooLarge(err error) bool {
	he, ok := err.(*HTTPError)
	if !ok {
		return false
	}
	low := strings.ToLower(he.Body)
	if he.Code == 413 {
		return true
	}
	for _, s := range []string{"too large", "context length", "context_length", "maximum context", "too many tokens", "reduce the length", "request too large"} {
		if strings.Contains(low, s) {
			return true
		}
	}
	return false
}

// NoToolSupport is true when the provider said this model can't use tools.
func NoToolSupport(err error) bool {
	he, ok := err.(*HTTPError)
	if !ok || (he.Code != 400 && he.Code != 404 && he.Code != 422) {
		return false
	}
	low := strings.ToLower(he.Body)
	return (strings.Contains(low, "tool") || strings.Contains(low, "function")) &&
		(strings.Contains(low, "not support") || strings.Contains(low, "unsupported") || strings.Contains(low, "does not support") || strings.Contains(low, "no endpoints"))
}

// Shrink keeps the system prompt and the recent turns, and cuts very long messages.
func Shrink(msgs []Message, keep int) []Message {
	var sys, rest []Message
	for i, m := range msgs {
		if m.Role == "system" && i == len(sys) {
			sys = append(sys, m)
		} else {
			rest = append(rest, m)
		}
	}
	if len(rest) > keep {
		rest = rest[len(rest)-keep:]
	}
	for len(rest) > 0 && rest[0].Role == "tool" {
		rest = rest[1:]
	}
	var out []Message
	for _, m := range append(sys, rest...) {
		if len(m.Content) > 6000 {
			m.Content = m.Content[:2500] + "\n…[trimmed]…\n" + m.Content[len(m.Content)-2500:]
		}
		out = append(out, m)
	}
	return Sanitize(out)
}

// ---------- provider adapters ----------

var geminiDrop = map[string]bool{"additionalProperties": true, "$schema": true, "examples": true, "default": true,
	"$ref": true, "$defs": true, "definitions": true, "pattern": true, "format": true, "minLength": true, "maxLength": true, "const": true}

// AdaptTools returns the tool list the way this provider likes it. Gemini rejects some
// JSON-schema keywords with INVALID_ARGUMENT and wants enum values as strings.
func AdaptTools(tools []Tool, pid string) []Tool {
	if pid != "gemini" {
		return tools
	}
	out := make([]Tool, len(tools))
	for i, t := range tools {
		c := t
		c.Function.Parameters = scrub(deepCopy(t.Function.Parameters), false)
		out[i] = c
	}
	return out
}

func deepCopy(m map[string]any) map[string]any {
	b, _ := json.Marshal(m)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func scrub(o map[string]any, props bool) map[string]any {
	for k, v := range o {
		if !props && geminiDrop[k] {
			delete(o, k)
			continue
		}
		switch vv := v.(type) {
		case map[string]any:
			o[k] = scrub(vv, !props && k == "properties")
		case []any:
			if k == "enum" && !props {
				for i := range vv {
					vv[i] = fmt.Sprint(vv[i])
				}
				o["type"] = "string"
			} else {
				for i := range vv {
					if m, ok := vv[i].(map[string]any); ok {
						vv[i] = scrub(m, false)
					}
				}
			}
		}
	}
	return o
}
