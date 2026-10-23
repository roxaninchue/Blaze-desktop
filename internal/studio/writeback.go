// Package studio holds the source-level logic behind the Design tab's v0.8.0 tools:
// edits that write back to the real file, brand kits, taste memory, the critic's
// score card, stack detection and site shipping. It has no UI and no Wails, so it
// is tested on its own.
package studio

// Write-back (v0.8.0): an edit made in the preview (retyped text, a colour, a drag or
// a resize) becomes a small change in the element's real source: HTML, Vue and Svelte
// templates (style="…"), JSX/TSX (style={{…}} and className). The element is found
// from what the picker saw in the page: its tag, id, classes, a bit of its text and its
// position among look-alikes.

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Target identifies an element as the preview saw it.
type Target struct {
	Tag     string   `json:"tag"`
	ID      string   `json:"id,omitempty"`
	Classes []string `json:"classes,omitempty"`
	Text    string   `json:"text,omitempty"`  // its visible text (trimmed, may be cut)
	Index   int      `json:"index,omitempty"` // 0-based position among elements with the same tag+classes
}

// Edit is one change to make in the source.
type Edit struct {
	Kind    string            `json:"kind"` // text | style | attr | remove | part (a 3D part, parts.go)
	Target  Target            `json:"target"`
	OldText string            `json:"old_text,omitempty"`
	NewText string            `json:"new_text,omitempty"`
	Props   map[string]string `json:"props,omitempty"` // style: CSS property -> value ("" removes); attr: name -> value
}

// Lang is the template flavour a file is edited as.
type Lang int

const (
	LangHTML Lang = iota // .html .htm .vue .svelte .astro (style="…", class="…")
	LangJSX              // .jsx .tsx .js .ts (style={{…}}, className)
)

// LangFor picks the flavour from a file name; ok is false for files write-back can't edit.
func LangFor(path string) (Lang, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".html", ".htm", ".vue", ".svelte", ".astro", ".svg":
		return LangHTML, true
	case ".jsx", ".tsx", ".js", ".ts":
		return LangJSX, true
	}
	return 0, false
}

// ErrNotFound means no element in the file matched the target.
var ErrNotFound = errors.New("couldn't find that element in the source")

// ErrTooComplex means the element was found but the change can't be made safely by
// hand (e.g. a JSX style built from variables, or text spread across several tags).
var ErrTooComplex = errors.New("that change needs the model")

// node is an element found in the source.
type node struct {
	tag        string
	start, end int // the whole element: src[start:end]
	openEnd    int // end of the opening tag (index after '>')
	inner      [2]int
	attrs      []attr
	selfClose  bool
}

type attr struct {
	name       string
	val        string // without quotes/braces
	raw        string // as written, e.g. "\"x\"" or "{{ a: 1 }}"
	start, end int    // the whole attribute (name through value)
	vstart     int    // start of raw value (-1 = boolean attribute)
}

var voidTags = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true, "link": true, "meta": true, "source": true, "track": true, "wbr": true}

// parseOpen parses the opening tag that starts at src[i] ('<'). It returns false when
// src[i:] isn't an element start.
func parseOpen(src string, i int, lang Lang) (node, bool) {
	n := node{start: i}
	j := i + 1
	k := j
	for k < len(src) && (isNameChar(src[k])) {
		k++
	}
	if k == j || !isLetter(src[j]) {
		return n, false
	}
	n.tag = src[j:k]
	p := k
	for p < len(src) {
		for p < len(src) && isSpace(src[p]) {
			p++
		}
		if p >= len(src) {
			return n, false
		}
		if src[p] == '>' {
			n.openEnd = p + 1
			break
		}
		if src[p] == '/' && p+1 < len(src) && src[p+1] == '>' {
			n.openEnd = p + 2
			n.selfClose = true
			break
		}
		if src[p] == '{' { // JSX spread {...props}
			e := matchBrace(src, p)
			if e < 0 {
				return n, false
			}
			p = e + 1
			continue
		}
		as := p
		for p < len(src) && !isSpace(src[p]) && src[p] != '=' && src[p] != '>' && !(src[p] == '/' && p+1 < len(src) && src[p+1] == '>') {
			p++
		}
		a := attr{name: src[as:p], start: as, vstart: -1}
		q := p
		for q < len(src) && isSpace(src[q]) {
			q++
		}
		if q < len(src) && src[q] == '=' {
			q++
			for q < len(src) && isSpace(src[q]) {
				q++
			}
			if q >= len(src) {
				return n, false
			}
			a.vstart = q
			switch src[q] {
			case '"', '\'':
				e := strings.IndexByte(src[q+1:], src[q])
				if e < 0 {
					return n, false
				}
				a.raw = src[q : q+e+2]
				a.val = src[q+1 : q+1+e]
				p = q + e + 2
			case '{':
				e := matchBrace(src, q)
				if e < 0 {
					return n, false
				}
				a.raw = src[q : e+1]
				a.val = strings.TrimSpace(src[q+1 : e])
				p = e + 1
			default:
				e := q
				for e < len(src) && !isSpace(src[e]) && src[e] != '>' {
					e++
				}
				a.raw = src[q:e]
				a.val = a.raw
				p = e
			}
		}
		a.end = p
		if a.name != "" {
			n.attrs = append(n.attrs, a)
		}
	}
	if n.openEnd == 0 {
		return n, false
	}
	low := strings.ToLower(n.tag)
	if n.selfClose || (lang == LangHTML && voidTags[low]) {
		n.end = n.openEnd
		n.inner = [2]int{n.openEnd, n.openEnd}
		return n, true
	}
	// Find the matching close tag, counting nested elements with the same name.
	depth := 1
	s := n.openEnd
	for s < len(src) {
		o := strings.Index(src[s:], "<")
		if o < 0 {
			break
		}
		o += s
		if strings.HasPrefix(src[o:], "<!--") {
			e := strings.Index(src[o:], "-->")
			if e < 0 {
				break
			}
			s = o + e + 3
			continue
		}
		if strings.HasPrefix(src[o:], "</") {
			e := o + 2
			for e < len(src) && isNameChar(src[e]) {
				e++
			}
			if strings.EqualFold(src[o+2:e], n.tag) {
				depth--
				if depth == 0 {
					c := strings.IndexByte(src[e:], '>')
					if c < 0 {
						return n, false
					}
					n.inner = [2]int{n.openEnd, o}
					n.end = e + c + 1
					return n, true
				}
			}
			s = e
			continue
		}
		if c, ok := parseOpen(src, o, lang); ok && strings.EqualFold(c.tag, n.tag) {
			if !c.selfClose && !(lang == LangHTML && voidTags[low]) {
				depth++
			}
			s = c.openEnd
			continue
		}
		s = o + 1
	}
	return n, false
}

func matchBrace(src string, i int) int {
	depth := 0
	var quote byte
	for k := i; k < len(src); k++ {
		c := src[k]
		if quote != 0 {
			if c == '\\' {
				k++
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			quote = c
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return k
			}
		}
	}
	return -1
}

func isSpace(c byte) bool  { return c == ' ' || c == '\n' || c == '\t' || c == '\r' }
func isLetter(c byte) bool { return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') }
func isNameChar(c byte) bool {
	return isLetter(c) || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == ':' || c == '.'
}

func (n node) get(name string) (attr, bool) {
	for _, a := range n.attrs {
		if strings.EqualFold(a.name, name) {
			return a, true
		}
	}
	return attr{}, false
}

func (n node) classes() []string {
	for _, nm := range []string{"class", "className"} {
		if a, ok := n.get(nm); ok {
			v := a.val
			if strings.HasPrefix(a.raw, "{") { // className={"a b"} or {`a b`}
				v = strings.Trim(v, "\"'`")
			}
			return strings.Fields(v)
		}
	}
	return nil
}

var tagRe = regexp.MustCompile(`<[^>]*>`)
var wsRe = regexp.MustCompile(`\s+`)

func plain(s string) string {
	s = tagRe.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", "\"", "&#39;", "'", "&nbsp;", " ").Replace(s)
	return strings.TrimSpace(wsRe.ReplaceAllString(s, " "))
}

// all lists every element in the source, in order.
func all(src string, lang Lang) []node {
	var out []node
	for i := 0; i < len(src); i++ {
		if src[i] != '<' {
			continue
		}
		if strings.HasPrefix(src[i:], "<!--") {
			if e := strings.Index(src[i:], "-->"); e >= 0 {
				i += e + 2
			}
			continue
		}
		if n, ok := parseOpen(src, i, lang); ok {
			out = append(out, n)
		}
	}
	return out
}

// Locate finds the element the target describes. It returns its start and end offsets.
func Locate(src string, lang Lang, t Target) (start, end int, err error) {
	n, err := locate(src, lang, t)
	if err != nil {
		return 0, 0, err
	}
	return n.start, n.end, nil
}

func locate(src string, lang Lang, t Target) (node, error) {
	tag := strings.ToLower(t.Tag)
	nodes := all(src, lang)
	if t.ID != "" {
		for _, n := range nodes {
			if a, ok := n.get("id"); ok && a.val == t.ID && strings.ToLower(n.tag) == tag {
				return n, nil
			}
		}
		// The id may be set by script or an expression: fall back to classes and text.
	}
	var same []node // same tag and all classes: the look-alikes the index counts
	for _, n := range nodes {
		if strings.ToLower(n.tag) != tag {
			continue
		}
		have := n.classes()
		ok := true
		for _, c := range t.Classes {
			if !containsStr(have, c) {
				ok = false
				break
			}
		}
		if ok {
			same = append(same, n)
		}
	}
	if len(same) == 0 {
		return node{}, ErrNotFound
	}
	want := plain(t.Text)
	if want != "" {
		var hits []node
		for _, n := range same {
			got := plain(src[n.inner[0]:n.inner[1]])
			if got == want || (len(want) > 12 && strings.HasPrefix(got, strings.TrimSuffix(want, "…"))) {
				hits = append(hits, n)
			}
		}
		if len(hits) == 1 {
			return hits[0], nil
		}
		if len(hits) > 1 {
			same = hits
		}
	}
	if t.Index >= 0 && t.Index < len(same) {
		return same[t.Index], nil
	}
	return same[0], nil
}

func containsStr(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// Apply makes one edit in src and returns the new source.
func Apply(src string, lang Lang, e Edit) (string, error) {
	if e.Kind == "part" { // a 3D mockup part lives in the page's blaze-parts JSON, not in the markup
		return ApplyPart(src, e.Target.ID, e.Props)
	}
	n, err := locate(src, lang, e.Target)
	if err != nil {
		return src, err
	}
	switch e.Kind {
	case "text":
		return applyText(src, n, e)
	case "style":
		return applyStyle(src, lang, n, e.Props)
	case "attr":
		out := src
		// Apply right to left so offsets stay valid: re-locate after each change.
		keys := sortedKeys(e.Props)
		for _, k := range keys {
			nn, err := locate(out, lang, e.Target)
			if err != nil {
				return src, err
			}
			out = setAttr(out, lang, nn, k, e.Props[k])
		}
		return out, nil
	case "remove":
		s, en := n.start, n.end
		// Take the line with it when the element sits alone on its line.
		ls := strings.LastIndexByte(src[:s], '\n') + 1
		le := strings.IndexByte(src[en:], '\n')
		if strings.TrimSpace(src[ls:s]) == "" && le >= 0 && strings.TrimSpace(src[en:en+le]) == "" {
			s, en = ls, en+le+1
		}
		return src[:s] + src[en:], nil
	}
	return src, fmt.Errorf("unknown edit %q", e.Kind)
}

func sortedKeys(m map[string]string) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

func applyText(src string, n node, e Edit) (string, error) {
	inner := src[n.inner[0]:n.inner[1]]
	old := strings.TrimSpace(e.OldText)
	nw := e.NewText
	if old != "" {
		if i := strings.Index(inner, old); i >= 0 && strings.Count(inner, old) == 1 {
			return src[:n.inner[0]+i] + escText(nw, old, inner) + src[n.inner[0]+i+len(old):], nil
		}
		if esc := htmlEscape(old); esc != old {
			if i := strings.Index(inner, esc); i >= 0 && strings.Count(inner, esc) == 1 {
				return src[:n.inner[0]+i] + htmlEscape(nw) + src[n.inner[0]+i+len(esc):], nil
			}
		}
	}
	// Pure text inside: replace it whole, keeping the surrounding whitespace.
	if !strings.ContainsAny(inner, "<{") {
		lead := inner[:len(inner)-len(strings.TrimLeft(inner, " \t\r\n"))]
		trail := inner[len(strings.TrimRight(inner, " \t\r\n")):]
		return src[:n.inner[0]] + lead + htmlEscape(nw) + trail + src[n.inner[1]:], nil
	}
	return src, ErrTooComplex
}

// escText escapes the new text like the old one was written.
func escText(nw, old, inner string) string {
	if strings.ContainsAny(nw, "<>&") {
		return htmlEscape(nw)
	}
	return nw
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// ---------- styles ----------

type decl struct{ k, v string }

func parseCSSDecls(s string) []decl {
	var out []decl
	for _, part := range splitTop(s, ';') {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		i := strings.IndexByte(part, ':')
		if i < 0 {
			continue
		}
		out = append(out, decl{strings.TrimSpace(part[:i]), strings.TrimSpace(part[i+1:])})
	}
	return out
}

// splitTop splits on sep outside parentheses and quotes.
func splitTop(s string, sep byte) []string {
	var out []string
	depth, last := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if c == q {
				q = 0
			}
			continue
		}
		switch c {
		case '"', '\'', '`':
			q = c
		case '(', '{', '[':
			depth++
		case ')', '}', ']':
			depth--
		default:
			if c == sep && depth == 0 {
				out = append(out, s[last:i])
				last = i + 1
			}
		}
	}
	return append(out, s[last:])
}

func mergeDecls(ds []decl, props map[string]string) []decl {
	for _, k := range sortedKeys(props) {
		v := props[k]
		k = strings.ToLower(strings.TrimSpace(k))
		found := false
		for i := 0; i < len(ds); i++ {
			if strings.EqualFold(ds[i].k, k) {
				if v == "" {
					ds = append(ds[:i], ds[i+1:]...)
					i--
				} else {
					ds[i].v = v
				}
				found = true
			}
		}
		if !found && v != "" {
			ds = append(ds, decl{k, v})
		}
	}
	return ds
}

func applyStyle(src string, lang Lang, n node, props map[string]string) (string, error) {
	if len(props) == 0 {
		return src, nil
	}
	if lang == LangJSX {
		return applyJSXStyle(src, n, props)
	}
	a, ok := n.get("style")
	var ds []decl
	if ok {
		if strings.HasPrefix(a.raw, "{") { // Svelte/Astro style={expr}
			return src, ErrTooComplex
		}
		ds = parseCSSDecls(a.val)
	}
	ds = mergeDecls(ds, props)
	var parts []string
	for _, d := range ds {
		parts = append(parts, d.k+": "+d.v)
	}
	val := strings.Join(parts, "; ")
	if ok {
		if val == "" {
			s := a.start
			for s > 0 && isSpace(src[s-1]) {
				s--
			}
			return src[:s] + src[a.end:], nil
		}
		q := "\""
		if strings.HasPrefix(a.raw, "'") {
			q = "'"
		} else {
			val = strings.ReplaceAll(val, "\"", "'")
		}
		return src[:a.vstart] + q + val + q + src[a.end:], nil
	}
	if val == "" {
		return src, nil
	}
	return insertAttr(src, n, `style="`+strings.ReplaceAll(val, "\"", "'")+`"`), nil
}

func insertAttr(src string, n node, text string) string {
	at := n.openEnd - 1
	if n.selfClose {
		at = n.openEnd - 2
		for at > n.start && isSpace(src[at-1]) {
			at--
		}
		return src[:at] + " " + text + " " + src[at:]
	}
	return src[:at] + " " + text + src[at:]
}

func setAttr(src string, lang Lang, n node, name, val string) string {
	if lang == LangJSX && strings.EqualFold(name, "class") {
		name = "className"
	}
	a, ok := n.get(name)
	if ok {
		if val == "" && name != "alt" {
			s := a.start
			for s > 0 && isSpace(src[s-1]) {
				s--
			}
			return src[:s] + src[a.end:]
		}
		return src[:a.start] + name + `="` + strings.ReplaceAll(val, "\"", "&quot;") + `"` + src[a.end:]
	}
	if val == "" && name != "alt" {
		return src
	}
	return insertAttr(src, n, name+`="`+strings.ReplaceAll(val, "\"", "&quot;")+`"`)
}

// ---------- JSX style={{ … }} ----------

var camelRe = regexp.MustCompile(`-([a-z])`)

func camel(k string) string {
	if strings.HasPrefix(k, "--") {
		return `"` + k + `"`
	}
	return camelRe.ReplaceAllStringFunc(strings.ToLower(k), func(m string) string { return strings.ToUpper(m[1:]) })
}

func applyJSXStyle(src string, n node, props map[string]string) (string, error) {
	a, ok := n.get("style")
	type kv struct{ k, v string } // v is the literal as written
	var items []kv
	if ok {
		in := strings.TrimSpace(a.val) // "{ a: 1 }" (outer braces of style={...} removed)
		if !strings.HasPrefix(in, "{") || !strings.HasSuffix(in, "}") {
			return src, ErrTooComplex // style={styles.card}
		}
		body := strings.TrimSpace(in[1 : len(in)-1])
		for _, part := range splitTop(body, ',') {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if strings.HasPrefix(part, "...") {
				return src, ErrTooComplex
			}
			i := strings.IndexByte(part, ':')
			if i < 0 {
				return src, ErrTooComplex
			}
			v := strings.TrimSpace(part[i+1:])
			if !isJSLiteral(v) {
				return src, ErrTooComplex
			}
			items = append(items, kv{strings.Trim(strings.TrimSpace(part[:i]), "\"'"), v})
		}
	}
	for _, k := range sortedKeys(props) {
		v := props[k]
		ck := strings.Trim(camel(k), "\"")
		found := false
		for i := 0; i < len(items); i++ {
			if items[i].k == ck {
				found = true
				if v == "" {
					items = append(items[:i], items[i+1:]...)
					i--
				} else {
					items[i].v = strconv.Quote(v)
				}
			}
		}
		if !found && v != "" {
			items = append(items, kv{ck, strconv.Quote(v)})
		}
	}
	var parts []string
	for _, it := range items {
		k := it.k
		if strings.HasPrefix(k, "--") {
			k = `"` + k + `"`
		}
		parts = append(parts, k+": "+it.v)
	}
	if len(parts) == 0 {
		if ok {
			s := a.start
			for s > 0 && isSpace(src[s-1]) {
				s--
			}
			return src[:s] + src[a.end:], nil
		}
		return src, nil
	}
	lit := "style={{ " + strings.Join(parts, ", ") + " }}"
	if ok {
		return src[:a.start] + lit + src[a.end:], nil
	}
	return insertAttr(src, n, lit), nil
}

var numRe = regexp.MustCompile(`^-?\d+(\.\d+)?$`)

func isJSLiteral(v string) bool {
	if numRe.MatchString(v) {
		return true
	}
	if len(v) >= 2 && (v[0] == '"' || v[0] == '\'' || v[0] == '`') && v[len(v)-1] == v[0] && !strings.Contains(v, "${") {
		return true
	}
	return false
}

// ---------- element extraction (restore one element) ----------

// Extract returns the element's source text.
func Extract(src string, lang Lang, t Target) (string, error) {
	s, e, err := Locate(src, lang, t)
	if err != nil {
		return "", err
	}
	return src[s:e], nil
}

// ReplaceElement swaps the element the target finds in cur for the given source text.
func ReplaceElement(cur string, lang Lang, t Target, with string) (string, error) {
	s, e, err := Locate(cur, lang, t)
	if err != nil {
		return cur, err
	}
	return cur[:s] + with + cur[e:], nil
}
