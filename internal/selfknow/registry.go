package selfknow

// Blaze Desktop's views and slash commands live in its frontend (frontend/dist, embedded in
// the binary): the BlazeViews LIST in views.js and the COMMANDS array in app.js, which other
// files extend with COMMANDS.splice/push. Reading them from the embedded files keeps the
// report true to this build without a second hand-kept list.

import (
	"io/fs"
	"regexp"
	"sort"
	"strings"
)

var (
	viewRe   = regexp.MustCompile(`\{\s*id:\s*"([^"]+)",\s*name:\s*"([^"]+)",[^}]*?desc:\s*"([^"]*)"`)
	cmdRe    = regexp.MustCompile(`\{\s*name:\s*"([^"]+)",\s*desc:\s*"([^"]*)"`)
	cmdAddRe = regexp.MustCompile(`COMMANDS\.(?:splice|push|unshift)\([^{]*(\{\s*name:\s*"[^"]+",\s*desc:\s*"[^"]*")`)
)

// Views reads the /views registry (views.js) from the frontend files.
func Views(fsys fs.FS, dir string) []Entry {
	b, err := fs.ReadFile(fsys, dir+"/views.js")
	if err != nil {
		return nil
	}
	s := string(b)
	if i := strings.Index(s, "const LIST"); i >= 0 {
		s = s[i:]
		if j := strings.Index(s, "];"); j >= 0 {
			s = s[:j]
		}
	}
	var out []Entry
	for _, m := range viewRe.FindAllStringSubmatch(s, -1) {
		out = append(out, Entry{Name: m[2], Desc: m[3]})
	}
	return out
}

// Commands reads the slash commands: the COMMANDS array in app.js plus every
// COMMANDS.splice/push in the other frontend files. Sorted by name, no duplicates.
func Commands(fsys fs.FS, dir string) []Entry {
	seen := map[string]bool{}
	var out []Entry
	add := func(name, desc string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, Entry{Name: name, Desc: desc})
		}
	}
	if b, err := fs.ReadFile(fsys, dir+"/app.js"); err == nil {
		s := string(b)
		if i := strings.Index(s, "const COMMANDS = ["); i >= 0 {
			s = s[i:]
			if j := strings.Index(s, "\n];"); j >= 0 {
				s = s[:j]
			}
			for _, line := range strings.Split(s, "\n") {
				// only the array's own rows (two-space indent), not nested sub-menus
				if !strings.HasPrefix(line, "  { name:") {
					continue
				}
				if m := cmdRe.FindStringSubmatch(line); m != nil {
					add(m[1], m[2])
				}
			}
		}
	}
	es, _ := fs.ReadDir(fsys, dir)
	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".js") {
			continue
		}
		b, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			continue
		}
		for _, m := range cmdAddRe.FindAllStringSubmatch(string(b), -1) {
			if c := cmdRe.FindStringSubmatch(m[1]); c != nil {
				add(c[1], c[2])
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
