package tools

// Tool selection (v0.5.1). Sending every tool's JSON schema on every turn costs
// thousands of tokens, so a turn gets a small core set and the rest are indexed
// in blaze.db: find_tools(query) searches that index and turns the matches on for
// the rest of the conversation. Calling an extra tool by its exact name also
// works and turns it on.

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/store"
)

// core tools are sent every turn.
var core = map[string]bool{
	"run_shell": true, "file_read": true, "file_write": true, "file_edit": true, "file_list": true, "file_search": true,
	"web_search": true, "fetch_page": true, "git": true, "job_log": true,
	"remember": true, "recall": true, "forget": true, "load_skill": true, "find_tools": true,
	"notify": true, "open_view": true, "about_blaze": true, "memory_search": true, "memory_save": true, "memory_update": true, "search_chat": true, "read_turn": true, "read_artifact": true, "update_brief": true,
}

// hidden tools still run when called but are never offered (old aliases).
var hidden = map[string]bool{"use_skill": true}

// groups make find_tools match on what a tool is for, not only its wording.
var groups = map[string]string{
	"file_delete": "files", "http_request": "web api network", "download_file": "web files download",
	"open_url": "web browser screen", "run_code": "code python node script", "install_tool": "install packages setup",
	"system_info": "pc hardware system", "schedule": "schedule cron timer later recurring", "job_input": "jobs background",
	"job_stop": "jobs background kill", "write_skill": "skills", "read_screen": "screen vision", "see_screen": "screen vision image",
	"csv_report":  "csv tsv spreadsheet excel data table clean chart plot graph bar line report pdf markdown",
	"csv_summary": "csv report written summary pdf markdown",
}

// IsCore says whether a tool is always sent.
func IsCore(name string) bool { return core[name] }

// Extra lists the tools that are only sent on demand, sorted.
func Extra() []string {
	var out []string
	for n := range registry {
		if !core[n] && !hidden[n] {
			out = append(out, n)
		}
	}
	sort.Strings(out)
	return out
}

// SyncIndex writes the tool registry into the database's tools table (cheap when
// nothing changed). Call it after every Register.
func SyncIndex() {
	d := DB()
	if d == nil {
		return
	}
	var rows []store.ToolRow
	for n, t := range registry {
		if hidden[n] {
			continue
		}
		rows = append(rows, store.ToolRow{Name: n, Desc: t.tool.Function.Description, Group: groups[n]})
	}
	_ = d.SyncTools(rows)
}

// Select is the tool list for one step of a conversation: the core set plus the
// tools that conversation turned on. With no database it falls back to All().
func Select(conv string) []llm.Tool {
	d := DB()
	if d == nil {
		return All()
	}
	on := map[string]bool{}
	for _, n := range d.Active(conv) {
		on[n] = true
	}
	var out []llm.Tool
	for _, t := range All() {
		n := t.Function.Name
		if (core[n] || on[n]) && !hidden[n] {
			out = append(out, t)
		}
	}
	return out
}

// Activate turns tools on for a conversation.
func Activate(conv string, names ...string) {
	if d := DB(); d != nil && conv != "" {
		_ = d.Activate(conv, names...)
	}
}

// autoOn turns on-demand tools on before a turn when the user's own words clearly need them.
// A Cloud run's tool list is fixed when the run starts, so a find_tools call inside the run can't
// add them (live 0.19.14: every "Make a CSV report of x.csv" run skipped csv_report and wrote
// pandas scripts; one called find_tools, then couldn't call csv_report).
var autoOn = []struct {
	re    *regexp.Regexp
	names []string
}{
	{regexp.MustCompile(`(?i)(\.(csv|tsv)\b|\bcsv\b|\btsv\b)`), []string{"csv_report", "csv_summary"}},
}

// ActivateFor turns on the tools the user's message needs (see autoOn) and returns their names.
func ActivateFor(conv, text string) []string {
	var names []string
	for _, r := range autoOn {
		if r.re.MatchString(text) {
			for _, n := range r.names {
				if _, ok := registry[n]; ok {
					names = append(names, n)
				}
			}
		}
	}
	if len(names) > 0 {
		Activate(conv, names...)
	}
	return names
}

// ExtraLine is the one-line hint for the system prompt naming the on-demand tools.
func ExtraLine(conv string) string {
	ex := Extra()
	if len(ex) == 0 {
		return ""
	}
	on := map[string]bool{}
	if d := DB(); d != nil {
		for _, n := range d.Active(conv) {
			on[n] = true
		}
	}
	var off []string
	for _, n := range ex {
		if !on[n] {
			off = append(off, n)
		}
	}
	if len(off) == 0 {
		return ""
	}
	return "\nMORE TOOLS (not loaded; find_tools(query) turns them on): " + strings.Join(off, ", ") + "\n"
}

func (e *Env) conv() string {
	if e == nil || e.Conv == nil {
		return ""
	}
	return e.Conv()
}

func init() {
	register("find_tools", "Find and turn on more tools for this conversation (downloads, HTTP APIs, running code, installing programs, schedules, the screen, background job control, deleting files…). They're usable from your next step.",
		[]prop{p("query", "string", "What you need to do, e.g. 'download a file' or 'schedule a daily task'. Empty lists every extra tool.")}, []string{"query"},
		func(e *Env, a args) string {
			q := strings.TrimSpace(a.s("query"))
			var names []string
			if d := DB(); d != nil && q != "" {
				for _, n := range d.SearchTools(q, 12) {
					if !core[n] && !hidden[n] && registry[n].run != nil {
						names = append(names, n)
					}
					if len(names) == 5 {
						break
					}
				}
			}
			if len(names) == 0 {
				var b strings.Builder
				if q != "" {
					b.WriteString("No extra tool matches \"" + q + "\". ")
				}
				b.WriteString("Extra tools (call find_tools with one of these names to turn it on):\n")
				for _, n := range Extra() {
					fmt.Fprintf(&b, "- %s: %s\n", n, firstSentence(registry[n].tool.Function.Description))
				}
				return b.String()
			}
			Activate(e.conv(), names...)
			var b strings.Builder
			b.WriteString("Turned on for this conversation (use them from your next step):\n")
			for _, n := range names {
				fmt.Fprintf(&b, "- %s: %s\n", n, registry[n].tool.Function.Description)
			}
			return b.String()
		})
}

func firstSentence(s string) string {
	if i := strings.Index(s, ". "); i > 0 {
		return s[:i+1]
	}
	return s
}
