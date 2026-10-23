package main

// v0.7: the bundled skill library and proactive memory & skills.
//
//   - On start the embedded skills/ pack is seeded into ~/.blaze/skills
//     (internal/skillpack: idempotent, version-aware, never overwrites edits).
//   - The system prompt carries learn.Rules: save durable facts silently, replace
//     stale ones, write or improve skills when a workflow repeats or is taught.
//   - After a turn, a cheap local check (learn.Decide, no model call) decides
//     whether a tiny reflection call on the cheapest model is worth it; it is
//     skipped for trivial turns, when the turn already saved, past the spend cap,
//     and more than once a minute. What it saves passes learn.Accept first.
//   - Every memory/skill change is emitted as a "learned" event: the chat shows a
//     small mark and a toast (Settings › Behaviour › Learning).

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/learn"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/skillpack"
	"blaze-desktop/internal/tools"
)

//go:embed all:skills
var skillsFS embed.FS

// LearnPrefs live in desktop.json (Prefs.Learn).
type LearnPrefs struct {
	Off     bool `json:"off,omitempty"`      // "Learn automatically" (default on)
	NoMarks bool `json:"no_marks,omitempty"` // hide the chat mark and toast
}

func (a *App) learnOn() bool { return !a.prefs.Learn.Off }

// learnRules is the MEMORY & SKILLS block of the system prompt.
func (a *App) learnRules() string { return learn.Rules(a.learnOn()) }

// SetLearn turns automatic learning on or off (Settings › Behaviour).
func (a *App) SetLearn(on bool) { a.prefs.Learn.Off = !on; a.savePrefs() }

// SetLearnMarks shows or hides the "learned" mark in chat.
func (a *App) SetLearnMarks(on bool) { a.prefs.Learn.NoMarks = !on; a.savePrefs() }

// startLearning seeds the bundled skills and forwards memory/skill changes to the UI.
func (a *App) startLearning() {
	sub, err := fs.Sub(skillsFS, "skills")
	if err == nil {
		r, err := tools.SeedSkills(sub)
		if err != nil {
			fmt.Println("blaze: skills:", err)
		} else if r.Changed() {
			fmt.Printf("blaze: skills: %d added, %d upgraded\n", len(r.Added), len(r.Upgraded))
		}
	}
	tools.SetOnLearn(func(ev tools.LearnEvent) {
		if a.ctx != nil {
			wr.EventsEmit(a.ctx, "learned", ev)
		}
	})
}

// ---------------- Settings › Skills ----------------

// SkillList is every skill with its state (bundled, edited, own, removed) and switch.
func (a *App) SkillList() []skillpack.View { return tools.SkillStatus() }

func errStr(err error) string {
	if err != nil {
		return err.Error()
	}
	return ""
}

// SetSkillEnabled turns a skill on or off for prompts.
func (a *App) SetSkillEnabled(name string, on bool) string {
	return errStr(tools.SetSkillEnabled(name, on))
}

// DeleteSkill removes a skill; a bundled one can be restored later.
func (a *App) DeleteSkill(name string) string { return errStr(tools.DeleteSkill(name)) }

// RestoreSkill brings a removed bundled skill back, or resets an edited one.
func (a *App) RestoreSkill(name string) string { return errStr(tools.RestoreSkill(name)) }

// SkillText is a skill's SKILL.md (for the preview in Settings).
func (a *App) SkillText(name string) string {
	for _, v := range tools.SkillStatus() {
		if v.Name == name && v.Path != "" {
			return tools.Run(a.env, "load_skill", `{"name":`+jsonStr(name)+`}`)
		}
	}
	return ""
}

func jsonStr(s string) string { b, _ := json.Marshal(s); return string(b) }

// ---------------- end-of-turn reflection ----------------

var reflectState struct {
	sync.Mutex
	last     time.Time
	inflight bool
}

const reflectEvery = time.Minute

// turnSlice is this turn: from the last real user message to the end.
func turnSlice(h []llm.Message) learn.Turn {
	start := -1
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role == "user" && !isAgentResult(h[i]) {
			start = i
			break
		}
	}
	var t learn.Turn
	if start < 0 {
		return t
	}
	t.User = stripAttached(h[start].Content)
	for _, m := range h[start+1:] {
		for _, tc := range m.ToolCalls {
			n := tc.Function.Name
			t.Tools = append(t.Tools, n)
			switch n {
			case "remember", "forget":
				t.SavedMemory = true
			case "write_skill":
				t.SavedSkill = true
			}
		}
		if m.Role == "assistant" && strings.TrimSpace(m.Content) != "" {
			t.Reply = m.Content
		}
	}
	return t
}

// maybeReflect runs after a finished turn. It never blocks and fails silently.
func (a *App) maybeReflect(conv string, hist []llm.Message) {
	if !a.learnOn() || a.cfg == nil {
		return
	}
	t := turnSlice(hist)
	if t.User == "" || t.Reply == "" {
		return
	}
	d := tools.DB()
	sig := learn.Signature(t.User, t.Tools)
	var earlier [][]string
	if d != nil {
		_ = json.Unmarshal([]byte(d.Meta("learn_workflows")), &earlier)
	}
	steps, _ := learn.Steps(t.Tools)
	dec := learn.Decide(t, learn.Repeats(sig, earlier))
	if d != nil && steps >= 3 { // remember this workflow so a repeat can be spotted
		earlier = append(earlier, sig)
		if len(earlier) > 40 {
			earlier = earlier[len(earlier)-40:]
		}
		b, _ := json.Marshal(earlier)
		_ = d.SetMeta("learn_workflows", string(b))
	}
	if !dec.Reflect || d == nil {
		return
	}
	if c := a.cfg.DailyCap; c > 0 && llm.TodayCost() >= c {
		return // the spend cap is reached: no background calls
	}
	reflectState.Lock()
	if reflectState.inflight || time.Since(reflectState.last) < reflectEvery {
		reflectState.Unlock()
		return
	}
	reflectState.inflight, reflectState.last = true, time.Now()
	reflectState.Unlock()
	go func() {
		defer func() {
			recover()
			reflectState.Lock()
			reflectState.inflight = false
			reflectState.Unlock()
		}()
		a.reflect(conv, t, dec)
	}()
}

func (a *App) reflect(conv string, t learn.Turn, dec learn.Decision) {
	d := tools.DB()
	if d == nil {
		return
	}
	var known []string
	for _, m := range d.Search(t.User, 6) {
		known = append(known, fmt.Sprintf("#%d %s", m.ID, m.Text))
	}
	var skills []string
	if dec.Skill {
		for _, s := range tools.Skills() {
			skills = append(skills, s.Name)
		}
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	out, err := llm.Quick(ctx, a.cfg, []llm.Message{{Role: "user", Content: learn.Prompt(t, dec, known, skills)}}, 25*time.Second)
	if err != nil {
		return
	}
	r, err := learn.Parse(out)
	if err != nil {
		return
	}
	project := ""
	if p := a.project(); p != nil {
		project = p.ID
	}
	for _, m := range r.Memories {
		if id, ok := parseRef(m.Replaces); ok {
			if d.Edit(id, m.Text) == nil {
				tools.Learned(tools.LearnEvent{Kind: "memory", Action: "replaced", Text: m.Text, Ref: fmt.Sprintf("#%d", id), Auto: true})
				continue
			}
		}
		id, st, err := d.Add(m.Text, project, conv, time.Now())
		if err != nil || st == "exists" {
			continue
		}
		act := "saved"
		if st == "updated" {
			act = "updated"
		}
		tools.Learned(tools.LearnEvent{Kind: "memory", Action: act, Text: m.Text, Ref: fmt.Sprintf("#%d", id), Auto: true})
	}
	if s := r.Skill; s != nil && dec.Skill {
		for _, have := range tools.Skills() {
			if have.Name == s.Name {
				return // never replace a skill from a background guess
			}
		}
		if created, err := tools.WriteSkill(s.Name, s.Description, s.Tags, s.Body); err == nil && created {
			tools.Learned(tools.LearnEvent{Kind: "skill", Action: "created", Text: s.Name, Ref: s.Name, Auto: true})
		}
	}
}

func parseRef(s string) (int64, bool) {
	var n int64
	_, err := fmt.Sscanf(strings.TrimPrefix(strings.TrimSpace(s), "#"), "%d", &n)
	return n, err == nil && n > 0
}
