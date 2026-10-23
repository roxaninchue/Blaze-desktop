package tools

// Memory and skills, backed by the local database (internal/store). A turn no
// longer pastes every saved fact and every skill into the prompt: it gets the
// pinned memories plus the few that match the conversation, and the skills that
// match, and the model can recall / load_skill for more.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"blaze-desktop/internal/store"
)

// Prompt budget for memories: up to MemMatches matches, within MemBudget tokens.
const (
	MemMatches = 8
	MemBudget  = 600
	SkillLimit = 8
)

// DB is the open database, or nil when it can't be opened (Blaze still runs, without memory).
func DB() *store.DB {
	d, err := store.Default()
	if err != nil {
		return nil
	}
	return d
}

// UpdateNotice is the "please update Blaze" note when blaze.db was written by a
// newer Blaze (the file is left untouched and Blaze runs without memory), else "".
func UpdateNotice() string {
	if _, err := store.Default(); errors.Is(err, store.ErrNewerSchema) {
		return "Your memory was saved by a newer version of Blaze. Please update Blaze to use it; nothing was changed."
	}
	return ""
}

// noDBMsg is what a memory tool says when there is no database.
func noDBMsg() string {
	if n := UpdateNotice(); n != "" {
		return "Error: " + n + " Tell the user."
	}
	return "Error: Blaze's memory database can't be opened."
}

// Memory returns every saved fact as Markdown lines (for the Settings textarea).
func Memory() string {
	d := DB()
	if d == nil {
		return ""
	}
	var b strings.Builder
	for _, m := range d.Memories() {
		b.WriteString("- " + m.Text + "\n")
	}
	return strings.TrimSpace(b.String())
}

// SetMemoryText replaces the memories with the lines of a Markdown text.
func SetMemoryText(text string) {
	d := DB()
	if d == nil {
		return
	}
	var lines []string
	for _, l := range strings.Split(text, "\n") {
		if t, _, ok := store.ParseLine(l); ok {
			lines = append(lines, t)
		}
	}
	d.ReplaceAll(lines)
}

// MemoryFor is the memory section of the system prompt for a turn about query.
func MemoryFor(query string) string {
	d := DB()
	if d == nil {
		return ""
	}
	total := d.CountMemories()
	if total == 0 {
		return ""
	}
	ms := d.ForPrompt(query, MemMatches, MemBudget)
	var b strings.Builder
	fmt.Fprintf(&b, "\nWHAT YOU KNOW ABOUT THE USER (%d of %d saved memories, picked for this conversation", len(ms), total)
	if total > len(ms) {
		b.WriteString("; recall(query) searches the rest")
	}
	b.WriteString(")\n")
	for _, m := range ms {
		pin := ""
		if m.Pinned {
			pin = " pinned"
		}
		fmt.Fprintf(&b, "- %s [#%d%s]\n", m.Text, m.ID, pin)
	}
	return b.String()
}

// ---------------- skills ----------------

func skillsDir() string { return dataDir("skills") }

type Skill struct {
	Name, Desc, Path string
	Off              bool // turned off in Settings › Skills: kept on disk, left out of prompts
}

func parseSkill(path, txt string) Skill {
	s := Skill{Name: filepath.Base(filepath.Dir(path)), Path: path}
	if strings.HasPrefix(txt, "---") {
		if end := strings.Index(txt[3:], "---"); end > 0 {
			for _, l := range strings.Split(txt[3:3+end], "\n") {
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "name:"); ok {
					s.Name = strings.Trim(strings.TrimSpace(v), `"'`)
				}
				if v, ok := strings.CutPrefix(strings.TrimSpace(l), "description:"); ok {
					s.Desc = strings.Trim(strings.TrimSpace(v), `"'`)
				}
			}
		}
	}
	return s
}

type skillFile struct {
	path  string
	mtime int64
}

func skillFiles() []skillFile {
	var out []skillFile
	ents, _ := os.ReadDir(skillsDir())
	for _, en := range ents {
		if !en.IsDir() || strings.HasPrefix(en.Name(), ".") {
			continue
		}
		f := filepath.Join(skillsDir(), en.Name(), "SKILL.md")
		if st, err := os.Stat(f); err == nil && !st.IsDir() {
			out = append(out, skillFile{f, st.ModTime().UnixNano()})
		}
	}
	return out
}

// scanSkills reads ~/.blaze/skills/*/SKILL.md (same format as the Android app).
func scanSkills() []store.Skill {
	var out []store.Skill
	for _, f := range skillFiles() {
		b, err := os.ReadFile(f.path)
		if err != nil {
			continue
		}
		s := parseSkill(f.path, string(b))
		out = append(out, store.Skill{Name: s.Name, Desc: s.Desc, Path: f.path, Body: string(b), Mtime: f.mtime})
	}
	return out
}

// syncSkills re-indexes the skills folder when a SKILL.md was added, changed or removed.
func syncSkills(d *store.DB) {
	files := skillFiles()
	have := d.SkillMtimes()
	changed := len(files) != len(have)
	for _, f := range files {
		if have[f.path] != f.mtime {
			changed = true
			break
		}
	}
	if changed {
		_ = d.ReplaceSkills(scanSkills())
	}
}

func toSkills(in []store.Skill) []Skill {
	out := make([]Skill, 0, len(in))
	off := disabledSkills()
	for _, s := range in {
		out = append(out, Skill{Name: s.Name, Desc: s.Desc, Path: s.Path, Off: off[filepath.Base(filepath.Dir(s.Path))] || off[s.Name]})
	}
	return out
}

// Skills lists every skill (the folder is the source of truth; the index follows it).
func Skills() []Skill {
	d := DB()
	if d == nil {
		return toSkills(scanSkills())
	}
	syncSkills(d)
	return toSkills(d.Skills())
}

// SkillsFor is the skills section of the system prompt: the best matches for query
// (every skill when there are only a few), names and one-line descriptions only.
func SkillsFor(query string) string {
	var all []Skill
	for _, s := range Skills() {
		if !s.Off {
			all = append(all, s)
		}
	}
	if len(all) == 0 {
		return ""
	}
	pick := all
	if len(all) > SkillLimit {
		pick = nil
		if d := DB(); d != nil {
			for _, s := range toSkills(d.SearchSkills(query, SkillLimit+4)) {
				if !s.Off && len(pick) < SkillLimit {
					pick = append(pick, s)
				}
			}
		}
	}
	var b strings.Builder
	if len(pick) < len(all) {
		fmt.Fprintf(&b, "\nSKILLS (%d of %d shown, matched to this conversation; load_skill(name) reads one before you do that kind of task, several when a task spans them; find_tools or load_skill with a guess finds others)\n", len(pick), len(all))
	} else {
		b.WriteString("\nSKILLS (load_skill(name) reads one before you do that kind of task, several when a task spans them)\n")
	}
	for _, s := range pick {
		b.WriteString("- " + s.Name + ": " + s.Desc + "\n")
	}
	return b.String()
}

func loadSkill(name string) string {
	name = strings.TrimSpace(name)
	all := Skills()
	for _, s := range all {
		if strings.EqualFold(s.Name, name) || strings.EqualFold(filepath.Base(filepath.Dir(s.Path)), name) {
			b, _ := os.ReadFile(s.Path)
			return "Skill folder: " + filepath.Dir(s.Path) + " (scripts inside can be run with run_shell)\n\n" + string(b)
		}
	}
	msg := "No skill called " + name + "."
	if d := DB(); d != nil {
		if m := d.SearchSkills(name, 5); len(m) > 0 {
			var ns []string
			for _, s := range m {
				ns = append(ns, s.Name)
			}
			msg += " Closest: " + strings.Join(ns, ", ") + "."
		}
	}
	return msg
}

// ---------------- tools ----------------

func memID(s string) (int64, bool) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

func init() {
	register("remember", "Save a lasting fact about the user or their setup (preferences, project paths, how they like things), with its context. Not for one-off task details or secrets. Near-duplicates update the existing memory; pass replaces=#id when the fact supersedes a saved one.",
		[]prop{p("fact", "string", "One short sentence"), p("replaces", "string", "Optional: the id (#12) of a saved memory this one replaces because it changed")}, []string{"fact"},
		func(e *Env, a args) string {
			d := DB()
			if d == nil {
				return noDBMsg()
			}
			fact := strings.Join(strings.Fields(a.s("fact")), " ")
			if fact == "" {
				return "Error in remember: write the fact."
			}
			if id, ok := memID(a.s("replaces")); ok {
				if err := d.Edit(id, fact); err == nil {
					Learned(LearnEvent{Kind: "memory", Action: "replaced", Text: fact, Ref: fmt.Sprintf("#%d", id)})
					return fmt.Sprintf("Replaced memory #%d with the new fact.", id)
				}
			}
			memCtx.Lock()
			pr, ch := memCtx.project, memCtx.chat
			memCtx.Unlock()
			id, st, err := d.Add(fact, pr, ch, time.Now())
			switch {
			case err != nil:
				return "Error: " + err.Error()
			case st == store.Exists:
				return fmt.Sprintf("Already remembered (#%d).", id)
			case st == store.Updated:
				Learned(LearnEvent{Kind: "memory", Action: "updated", Text: fact, Ref: fmt.Sprintf("#%d", id)})
				return fmt.Sprintf("Updated memory #%d with the new wording.", id)
			}
			Learned(LearnEvent{Kind: "memory", Action: "saved", Text: fact, Ref: fmt.Sprintf("#%d", id)})
			out := fmt.Sprintf("Remembered (#%d).", id)
			// A light nudge against stale duplicates: show what looks like the same subject.
			if rel := d.Related(fact, 0.34, 3, id); len(rel) > 0 {
				out += " Similar saved memories:"
				for _, m := range rel {
					out += fmt.Sprintf(" #%d %q;", m.ID, m.Text)
				}
				out += " If one is now wrong, forget it (or next time use remember with replaces=#id)."
			}
			return out
		})

	register("recall", "Search everything you've saved about the user (your prompt only shows the memories that match this conversation). Use before asking the user something you may already know.",
		[]prop{p("query", "string", "Words to look for, e.g. 'deploy homeglow' or 'coffee'"), p("limit", "integer", "Max results (default 10)")}, []string{"query"},
		func(e *Env, a args) string {
			d := DB()
			if d == nil {
				return noDBMsg()
			}
			ms := d.Search(a.s("query"), max(1, min(a.i("limit", 10), 50)))
			if len(ms) == 0 {
				return "No saved memory matches " + a.s("query") + "."
			}
			var b strings.Builder
			ids := []int64{}
			for _, m := range ms {
				fmt.Fprintf(&b, "#%d %s (%s)\n", m.ID, m.Text, m.Created.Format("2006-01-02"))
				ids = append(ids, m.ID)
			}
			d.Touch(ids)
			return b.String()
		})

	register("forget", "Remove saved memories: by id (#12 from your prompt or recall) or every memory containing some text.",
		[]prop{p("text", "string", "A memory id like #12, or text to match")}, []string{"text"},
		func(e *Env, a args) string {
			d := DB()
			if d == nil {
				return noDBMsg()
			}
			if id, ok := memID(a.s("text")); ok {
				m, _ := d.Memory(id)
				if err := d.Delete(id); err != nil {
					return fmt.Sprintf("No memory #%d.", id)
				}
				Learned(LearnEvent{Kind: "memory", Action: "forgot", Text: m.Text, Ref: fmt.Sprintf("#%d", id)})
				return fmt.Sprintf("Forgot memory #%d.", id)
			}
			gone := d.ForgetMatching(a.s("text"))
			for _, m := range gone {
				Learned(LearnEvent{Kind: "memory", Action: "forgot", Text: m.Text, Ref: fmt.Sprintf("#%d", m.ID)})
			}
			return fmt.Sprintf("Forgot %d fact(s).", len(gone))
		})

	register("load_skill", "Load a skill's full instructions (listed in your prompt) before doing that kind of task.",
		[]prop{p("name", "string", "Skill name")}, []string{"name"},
		func(e *Env, a args) string { return loadSkill(a.s("name")) })
	// The pre-0.5.1 name, kept so old chats and the CLI's habits still work (hidden from the model).
	register("use_skill", "Same as load_skill.", []prop{p("name", "string", "Skill name")}, []string{"name"},
		func(e *Env, a args) string { return loadSkill(a.s("name")) })

	register("write_skill", "Create or rewrite a reusable skill (~/.blaze/skills/<name>/SKILL.md) so the next task of that kind goes faster. Use when a multi-step workflow repeats, the user teaches a method, or you found a better way for an existing skill (load_skill it first, then write the whole improved body under the same name).",
		[]prop{p("name", "string", "kebab-case name, e.g. deploy-homeglow"), p("description", "string", "When to use it, one line"), p("body", "string", "Markdown: numbered steps, exact commands, checks, pitfalls"), p("tags", "string", "Optional comma-separated tags")},
		[]string{"name", "description", "body"},
		func(e *Env, a args) string {
			name := strings.ToLower(strings.TrimSpace(a.s("name")))
			created, err := WriteSkill(name, a.s("description"), splitTags(a.s("tags")), a.s("body"))
			if err != nil {
				return "Error: " + err.Error()
			}
			if created {
				Learned(LearnEvent{Kind: "skill", Action: "created", Text: name, Ref: name})
				return "Saved skill " + name + ". Tell the user in one short line."
			}
			Learned(LearnEvent{Kind: "skill", Action: "edited", Text: name, Ref: name})
			return "Updated skill " + name + ". Tell the user in one short line what changed."
		})
}
