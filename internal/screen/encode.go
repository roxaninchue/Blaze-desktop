package screen

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// Compact encoding of an accessibility tree. One line per useful node:
//
//	<indent><code> "name" =value *
//
// Codes are short role names (btn, in, a, txt...). Wrapper nodes with nothing to
// say are dropped (their children move up), long runs of similar siblings are
// collapsed, repeated lines are deduped, password values never appear, and the
// whole thing is capped.

var roleCodes = map[string]string{
	// buttons and inputs
	"button": "btn", "push button": "btn", "toggle button": "btn", "split button": "btn", "menu button": "btn", "axbutton": "btn",
	"axpopupbutton": "btn", "axmenubutton": "btn", "axdisclosuretriangle": "btn",
	"checkbox": "cb", "check box": "cb", "axcheckbox": "cb", "radio button": "rb", "radiobutton": "rb", "axradiobutton": "rb",
	"edit": "in", "entry": "in", "text field": "in", "axtextfield": "in", "axtextarea": "in", "axsearchfield": "in", "password text": "in",
	"spin button": "in", "spinner": "in", "axincrementor": "in", "combo box": "sel", "combobox": "sel", "axcombobox": "sel",
	"slider": "sl", "axslider": "sl", "scroll bar": "", "scrollbar": "", "axscrollbar": "",
	// text
	"text": "txt", "static": "txt", "label": "txt", "axstatictext": "txt", "paragraph": "txt", "static text": "txt",
	"heading": "h", "header": "h", "axheading": "h", "link": "a", "hyperlink": "a", "axlink": "a",
	"document": "doc", "document web": "doc", "document frame": "doc", "axwebarea": "doc", "terminal": "term",
	"image": "img", "icon": "img", "aximage": "img", "graphic": "img",
	// structure
	"list": "ul", "axlist": "ul", "list box": "ul", "listbox": "ul", "list item": "li", "listitem": "li", "axrow": "li",
	"tree": "tree", "axoutline": "tree", "tree item": "ti", "treeitem": "ti",
	"table": "tbl", "axtable": "tbl", "data grid": "tbl", "datagrid": "tbl", "row": "tr", "table row": "tr", "dataitem": "td", "table cell": "td", "cell": "td", "axcell": "td",
	"tab": "tabs", "tab list": "tabs", "page tab list": "tabs", "axtabgroup": "tabs", "tabitem": "tab", "page tab": "tab",
	"menu": "menu", "menu bar": "menu", "menubar": "menu", "axmenubar": "menu", "axmenu": "menu", "menu item": "mi", "menuitem": "mi", "axmenuitem": "mi", "axmenubaritem": "mi",
	"check menu item": "mi", "radio menu item": "mi",
	"tool bar": "bar", "toolbar": "bar", "axtoolbar": "bar", "status bar": "bar", "statusbar": "bar", "titlebar": "bar",
	"dialog": "dlg", "alert": "dlg", "axsheet": "dlg", "axdialog": "dlg", "file chooser": "dlg",
	"window": "win", "frame": "win", "axwindow": "win",
	"progress bar": "prog", "progressbar": "prog", "axprogressindicator": "prog",
	"tooltip": "tip", "tool tip": "tip", "notification": "dlg", "form": "form", "article": "art", "section": "",
	// wrappers (dropped unless they carry a name)
	"group": "", "axgroup": "", "pane": "", "panel": "", "filler": "", "generic": "", "custom": "", "unknown": "", "axunknown": "",
	"layered pane": "", "scroll pane": "", "axscrollarea": "", "axsplitgroup": "", "split pane": "", "redundant object": "",
	"viewport": "", "axlayoutarea": "", "axlayoutitem": "", "embedded": "", "block quote": "", "axsplitter": "", "separator": "",
	"thumb": "", "section header": "h",
}

// code is the short role code; "" means a wrapper.
func code(role string) (string, bool) {
	r := strings.ToLower(strings.TrimSpace(role))
	if c, ok := roleCodes[r]; ok {
		return c, true
	}
	r = strings.TrimPrefix(r, "ax")
	if c, ok := roleCodes[r]; ok {
		return c, true
	}
	if r == "" {
		return "", true
	}
	if len(r) > 10 {
		r = r[:10]
	}
	return strings.ReplaceAll(r, " ", "-"), false
}

func clean(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > n {
		r := []rune(s)
		s = string(r[:n-1]) + "…"
	}
	return s
}

// tail keeps the end of long text (a terminal's last lines matter most).
func tail(s string, n int) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r", ""), "\n")
	var keep []string
	total := 0
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], " \t")
		if l == "" && len(keep) == 0 {
			continue
		}
		total += len(l) + 1
		if total > n {
			break
		}
		keep = append([]string{l}, keep...)
	}
	return strings.Join(keep, " ⏎ ")
}

type line struct {
	depth int
	code  string
	text  string // the full line without indent
}

// Encode turns the tree into the compact text the model reads (max chars).
func Encode(t *Tree, max int) string {
	if max <= 0 {
		max = 8000
	}
	var b strings.Builder
	fmt.Fprintf(&b, "window: %s — %q", orStr(t.Win.App, "?"), clean(t.Win.Title, 120))
	if t.Win.W > 0 {
		fmt.Fprintf(&b, " at %d,%d %dx%d", t.Win.X, t.Win.Y, t.Win.W, t.Win.H)
	}
	b.WriteString("\n")
	lines := encodeLines(t.Nodes)
	lines = collapse(lines)
	seen := map[string]int{}
	n := 0
	for i, l := range lines {
		key := l.text
		if len(key) > 3 && !strings.HasPrefix(key, "…") {
			seen[key]++
			if seen[key] > 2 {
				continue
			}
		}
		d := l.depth
		if d > 12 {
			d = 12
		}
		s := strings.Repeat(" ", d) + l.text + "\n"
		if b.Len()+len(s) > max {
			fmt.Fprintf(&b, "… (cut: %d more lines)\n", len(lines)-i)
			break
		}
		b.WriteString(s)
		n++
	}
	if n == 0 {
		b.WriteString("(no readable text in this window)\n")
	}
	return b.String()
}

func orStr(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func encodeLines(nodes []Flat) []line {
	var out []line
	// stack of emitted depths: maps raw depth -> output depth
	type frame struct{ raw, out int }
	var stack []frame
	for _, n := range nodes {
		for len(stack) > 0 && stack[len(stack)-1].raw >= n.D {
			stack = stack[:len(stack)-1]
		}
		od := 0
		if len(stack) > 0 {
			od = stack[len(stack)-1].out + 1
		}
		c, _ := code(n.Role)
		name := clean(n.Name, 160)
		val := ""
		if n.Secret {
			val = "[password hidden]"
		} else if c == "term" || c == "doc" && len(n.Value) > 200 {
			val = tail(n.Value, 1800)
		} else {
			val = clean(n.Value, 200)
		}
		if val == name {
			val = ""
		}
		if c == "" && name == "" && val == "" {
			continue // a wrapper: its children move up
		}
		if c == "" {
			c = "grp"
		}
		if c == "win" && len(out) == 0 && name == "" {
			continue
		}
		if (c == "img" || c == "grp") && name == "" && val == "" {
			continue
		}
		var s strings.Builder
		s.WriteString(c)
		if name != "" {
			if c == "txt" {
				s.WriteString(" " + name)
			} else {
				s.WriteString(" " + quote(name))
			}
		}
		if val != "" {
			s.WriteString(" =" + val)
		}
		if n.Focus {
			s.WriteString(" *")
		}
		out = append(out, line{depth: od, code: c, text: s.String()})
		stack = append(stack, frame{raw: n.D, out: od})
	}
	return out
}

func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `'`) + `"` }

// collapse keeps the first few of a long run of siblings with the same code.
func collapse(in []line) []line {
	const keep, limit = 6, 9
	var out []line
	for i := 0; i < len(in); {
		l := in[i]
		// find the run of same-depth same-code siblings (their subtrees included)
		j, count := i, 0
		var subtreeEnds []int
		for j < len(in) && in[j].depth == l.depth && in[j].code == l.code {
			k := j + 1
			for k < len(in) && in[k].depth > l.depth {
				k++
			}
			subtreeEnds = append(subtreeEnds, k)
			count++
			j = k
		}
		if count >= limit && l.code != "txt" {
			out = append(out, in[i:subtreeEnds[keep-1]]...)
			out = append(out, line{depth: l.depth, code: "…", text: fmt.Sprintf("… +%d more %s", count-keep, l.code)})
			i = j
			continue
		}
		out = append(out, in[i])
		i++
	}
	return out
}
