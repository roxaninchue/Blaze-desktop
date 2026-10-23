package studio

import (
	"fmt"
	"strings"
)

// Diff is a unified diff of one file's change. Edits from the preview touch one spot,
// so it trims the common head and tail and shows the middle with 3 lines of context.
func Diff(path, old, nw string) string {
	if old == nw {
		return ""
	}
	a, b := strings.Split(old, "\n"), strings.Split(nw, "\n")
	pre := 0
	for pre < len(a) && pre < len(b) && a[pre] == b[pre] {
		pre++
	}
	suf := 0
	for suf < len(a)-pre && suf < len(b)-pre && a[len(a)-1-suf] == b[len(b)-1-suf] {
		suf++
	}
	ctx := 3
	s := pre - ctx
	if s < 0 {
		s = 0
	}
	ea := len(a) - suf + ctx
	if ea > len(a) {
		ea = len(a)
	}
	eb := len(b) - suf + ctx
	if eb > len(b) {
		eb = len(b)
	}
	var w strings.Builder
	fmt.Fprintf(&w, "--- a/%s\n+++ b/%s\n", path, path)
	fmt.Fprintf(&w, "@@ -%d,%d +%d,%d @@\n", s+1, ea-s, s+1, eb-s)
	for i := s; i < pre; i++ {
		w.WriteString(" " + a[i] + "\n")
	}
	for i := pre; i < len(a)-suf; i++ {
		w.WriteString("-" + a[i] + "\n")
	}
	for i := pre; i < len(b)-suf; i++ {
		w.WriteString("+" + b[i] + "\n")
	}
	for i := len(a) - suf; i < ea; i++ {
		w.WriteString(" " + a[i] + "\n")
	}
	return w.String()
}

// DiffStat counts changed lines (added, removed) in a Diff.
func DiffStat(d string) (add, del int) {
	for _, l := range strings.Split(d, "\n") {
		if strings.HasPrefix(l, "+++") || strings.HasPrefix(l, "---") {
			continue
		}
		if strings.HasPrefix(l, "+") {
			add++
		} else if strings.HasPrefix(l, "-") {
			del++
		}
	}
	return
}
