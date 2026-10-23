package main

// One mind (v0.16+): one continuous main conversation, a journal of every turn and a
// memories table as the source of truth (internal/mind). This file wires it into the
// app: the one-time migration, journaling and the cheap save-as-you-go pass after each
// turn, the idle review, the memory tools and the prompt block. Shared text and schema:
// docs/plans/plan-one-mind.md.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/mind"
	"blaze-desktop/internal/store"
	"blaze-desktop/internal/tools"
)

// mainConv is the id of the one continuous main conversation (Chat mode, no project).
const mainConv = "main"

const (
	migratedKey   = "migrated_v1"    // chats imported into the journal
	importedKey   = "imported_v1"    // blaze.db memories imported into the memories table
	idleAfter     = 10 * time.Minute // no turn for this long = idle
	reviewEvery   = 10 * time.Minute // at most one review chunk per this
	reviewChunk   = 12000            // characters of journal per review call
	coreChars     = 1600             // pinned core budget in the prompt
	indexPerKind  = 14
	extractTurnCh = 6000 // characters of the finished turn the after-turn pass reads
)

var mindState struct {
	sync.Mutex
	lastTurn   time.Time
	lastReview time.Time
	reviewing  bool
}

func mindDB() *mind.DB {
	d, err := mind.Default()
	if err != nil {
		return nil
	}
	return d
}

// mindReviewOn: Settings › Behaviour › Learning › Review past chats when idle (default on).
func (a *App) mindReviewOn() bool { return a.prefs.MindReview == nil || *a.prefs.MindReview }

func (a *App) SetMindReview(on bool) { a.prefs.MindReview = &on; a.savePrefs() }

// startMind runs the migration in the background and starts the idle-review clock.
func (a *App) startMind() {
	mindState.Lock()
	mindState.lastTurn = time.Now()
	mindState.Unlock()
	go func() {
		a.migrateMind()
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			a.maybeIdleReview(context.Background(), false)
		}
	}()
}

// migrateMind imports every saved chat into the journal (queued for review) and the
// blaze.db memories into the memories table, once. Both steps are idempotent: journal
// rows are unique per (conv, turn), imported memories carry their blaze.db id, and a
// flag records each finished step. Chat files are never changed or deleted.
func (a *App) migrateMind() (journaled, imported int) {
	d := mindDB()
	if d == nil {
		return 0, 0
	}
	if d.Meta(migratedKey) == "" {
		for _, c := range a.allConvs() {
			var ms []mind.Msg
			for i, m := range c.Messages {
				ms = append(ms, mind.Msg{Turn: i, Role: m.Role, Content: m.Content, At: c.Updated})
			}
			n, err := d.Journal(c.ID, ms)
			if err != nil {
				return journaled, imported // try again next launch; nothing is lost
			}
			journaled += n
		}
		_ = d.SetMeta(migratedKey, time.Now().Format(time.RFC3339))
	}
	if d.Meta(importedKey) == "" {
		have := map[int64]bool{}
		for _, m := range d.All() {
			if m.Mirror != 0 {
				have[m.Mirror] = true
			}
		}
		for _, f := range tools.Facts() {
			id, _ := strconv.ParseInt(strings.TrimPrefix(f.ID, "m"), 10, 64)
			if id == 0 || have[id] {
				continue
			}
			kind := mind.NormKind(f.Category)
			if _, err := d.Save(mind.Memory{Kind: kind, Subject: subjectOf(f.Text), Text: f.Text, Mirror: id, Created: f.Date.Unix()}); err == nil {
				imported++
			}
		}
		_ = d.SetMeta(importedKey, time.Now().Format(time.RFC3339))
	}
	return journaled, imported
}

// subjectOf guesses a subject for an imported line: its first capitalised name, else "general".
func subjectOf(text string) string {
	for i, w := range strings.Fields(text) {
		w = strings.Trim(w, ".,:;!?'\"()")
		if i > 0 && len(w) > 1 && w[0] >= 'A' && w[0] <= 'Z' {
			return w
		}
	}
	if f := strings.Fields(text); len(f) > 0 && f[0] == "User" {
		return f[0]
	}
	return "general"
}

// journalConv appends a conversation's new messages to the journal.
func journalConv(id string, hist []llm.Message) []int64 {
	d := mindDB()
	if d == nil || id == "" {
		return nil
	}
	from := d.Journaled(id) + 1
	if from > len(hist) { // cleared or edited: keep what's there, add nothing twice
		return nil
	}
	var ms []mind.Msg
	for i := from; i < len(hist); i++ {
		ms = append(ms, mind.Msg{Turn: i, Role: hist[i].Role, Content: hist[i].Content})
	}
	_, _ = d.Journal(id, ms)
	return nil
}

// mindAfterTurn journals the turn and runs the cheap save-as-you-go pass (background).
func (a *App) mindAfterTurn(id string, hist []llm.Message) {
	mindState.Lock()
	mindState.lastTurn = time.Now()
	mindState.Unlock()
	journalConv(id, hist)
	if !a.learnOn() || a.cfg == nil {
		return
	}
	go func() {
		d := mindDB()
		if d == nil {
			return
		}
		// The finished turn: from the last user message on, user + assistant text only.
		start := 0
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				start = i
				break
			}
		}
		var b strings.Builder
		for _, m := range hist[start:] {
			if (m.Role == "user" || m.Role == "assistant") && strings.TrimSpace(m.Content) != "" {
				b.WriteString(m.Role + ": " + m.Content + "\n")
			}
		}
		text := b.String()
		if len(text) > extractTurnCh {
			text = text[:extractTurnCh]
		}
		if len(strings.TrimSpace(text)) < 20 {
			return
		}
		ms := a.extract(d, text)
		ids := []int64{}
		for _, t := range d.Unreviewed(1 << 30) {
			if t.Conv == id {
				ids = append(ids, t.ID)
			}
		}
		for _, m := range ms {
			a.mindSave(d, m)
		}
		if ms != nil {
			_ = d.MarkReviewed(ids) // the turn was read; the idle review can skip it
		}
	}()
}

// extract asks the cheapest model what to remember from text. nil = the call failed.
func (a *App) extract(d *mind.DB, text string) []mind.Memory {
	var existing strings.Builder
	for _, m := range d.Core(coreChars) {
		existing.WriteString("- " + m.Subject + ": " + m.Text + "\n")
	}
	msgs := []llm.Message{{Role: "system", Content: mind.ExtractPrompt},
		{Role: "user", Content: "EXISTING:\n" + firstNonEmpty(existing.String(), "(none)\n") + "\nCONVERSATION:\n" + text}}
	out, err := llmQuick(context.Background(), a, msgs)
	if err != nil {
		return nil
	}
	ms := mind.ParseExtract(out)
	if ms == nil {
		ms = []mind.Memory{}
	}
	return ms
}

// mindModel is the BlazeRouter preset the memory passes use: free tiers only, cheapest
// and healthiest first, so save-as-you-go and idle review never spend paid quota.
const mindModel = "auto/best-free"

// llmQuick is the cheap model call (swappable in tests). While BlazeRouter runs it goes
// through auto/best-free; otherwise (or if that fails) it falls back to llm.Quick, which
// also tries free models first.
var llmQuick = func(ctx context.Context, a *App, msgs []llm.Message) (string, error) {
	if a.router != nil && a.router.Running() {
		if p, ok := config.ProviderByID(config.RouterID); ok {
			c, cancel := context.WithTimeout(ctx, 60*time.Second)
			r, err := llm.Chat(c, p, a.router.APIKey(), mindModel, msgs, nil, nil)
			cancel()
			if err == nil && r != nil && strings.TrimSpace(r.Text) != "" {
				return r.Text, nil
			}
		}
	}
	return llm.Quick(ctx, a.cfg, msgs, 60*time.Second)
}

// mindSave writes one memory and keeps a blaze.db copy in step so Blaze CLI sees it.
func (a *App) mindSave(d *mind.DB, m mind.Memory) mind.Saved {
	r, err := d.Save(m)
	if err != nil {
		return r
	}
	if db, err := store.Default(); err == nil {
		cur, _ := d.Get(r.ID)
		if cur.Mirror != 0 {
			_ = db.Edit(cur.Mirror, cur.Text)
		} else if id, _, err := db.Add(cur.Text, "", "", time.Now()); err == nil && id > 0 {
			d.SetMirror(r.ID, id)
		}
	}
	if r.Action != "same" {
		a.emit("learned", map[string]any{"kind": "memory", "action": map[string]string{"added": "saved", "updated": "updated"}[r.Action], "text": m.Text, "auto": true})
	}
	return r
}

// ---------- idle review ----------

// idleOK: no turn for idleAfter, nothing running, on mains power and a quiet CPU.
func (a *App) idleOK() bool {
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	mindState.Lock()
	idle := time.Since(mindState.lastTurn) >= idleAfter
	mindState.Unlock()
	return !busy && idle && onMains() && cpuQuiet()
}

// maybeIdleReview reviews one chunk of unreviewed journal turns when Blaze is idle
// (force skips the idle and throttle checks; tests). Returns how many turns it marked.
func (a *App) maybeIdleReview(ctx context.Context, force bool) int {
	if !a.mindReviewOn() || a.cfg == nil {
		return 0
	}
	mindState.Lock()
	if mindState.reviewing || (!force && time.Since(mindState.lastReview) < reviewEvery) {
		mindState.Unlock()
		return 0
	}
	mindState.Unlock()
	if !force && !a.idleOK() {
		return 0
	}
	d := mindDB()
	if d == nil {
		return 0
	}
	ts := d.Unreviewed(reviewChunk)
	if len(ts) == 0 {
		return 0
	}
	mindState.Lock()
	mindState.reviewing, mindState.lastReview = true, time.Now()
	mindState.Unlock()
	defer func() { mindState.Lock(); mindState.reviewing = false; mindState.Unlock() }()
	ms := a.extract(d, mind.Transcript(ts))
	if ms == nil {
		return 0 // the model call failed: keep them queued
	}
	for _, m := range ms {
		if m.SourceTurn == 0 {
			m.SourceTurn = ts[0].ID
		}
		a.mindSave(d, m)
	}
	ids := make([]int64, len(ts))
	for i, t := range ts {
		ids[i] = t.ID
	}
	_ = d.MarkReviewed(ids)
	return len(ids)
}

// onMains: false only when we can tell the PC runs on battery (Linux sysfs).
func onMains() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	ms, _ := filepath.Glob("/sys/class/power_supply/*/online")
	if len(ms) == 0 {
		return true
	}
	for _, p := range ms {
		if b, err := os.ReadFile(p); err == nil && strings.TrimSpace(string(b)) == "1" {
			return true
		}
	}
	return false
}

// cpuQuiet: the 1-minute load is under half the cores (Linux/macOS /proc); true elsewhere.
func cpuQuiet() bool {
	b, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return true
	}
	f := strings.Fields(string(b))
	if len(f) == 0 {
		return true
	}
	l, err := strconv.ParseFloat(f[0], 64)
	return err != nil || l < float64(runtime.NumCPU())/2
}

// ---------- prompt ----------

// mindBlock is ONE MIND + WHAT YOU KNOW (pinned core + memory index).
func (a *App) mindBlock() string {
	d := mindDB()
	if d == nil {
		return ""
	}
	return "\n" + mind.MindRule + "\n" + mind.Block(d.Core(coreChars), d.Index(indexPerKind))
}

// ---------- tools ----------

func init() {
	tools.Register("memory_search", "Search what you know about the user (the memory table): profile, preferences, people, projects, decisions, facts. Use it before answering anything personal or about past plans; the prompt only carries a pinned core and an index of subjects.",
		[][3]string{{"query", "string", "Words or a subject from the MEMORY INDEX"}, {"kind", "string", "Optional: profile, preference, person, project, decision or fact"}, {"limit", "integer", "Max results (default 10)"}}, []string{"query"},
		func(e *tools.Env, args map[string]any) string {
			d := mindDB()
			if d == nil {
				return "Error: memory isn't available."
			}
			ms := d.Search(tools.ArgS(args, "query"), tools.ArgS(args, "kind"), tools.ArgI(args, "limit", 10))
			if len(ms) == 0 {
				return "Nothing in memory matches. Try other words, or search_chat for earlier conversations."
			}
			var b strings.Builder
			for _, m := range ms {
				fmt.Fprintf(&b, "[%d] %s · %s: %s (updated %s)\n", m.ID, m.Kind, m.Subject, m.Text, time.Unix(m.Updated, 0).Format("2 Jan 2006"))
			}
			return b.String()
		})
	tools.Register("memory_save", "Save one lasting thing about the user: a profile fact, standing preference, person, project state, decision or commitment. One line with the user's conditions kept exactly; add '(inferred)' when you inferred it. A line that repeats or supersedes one on the same subject replaces it (newest wins).",
		[][3]string{{"kind", "string", "profile, preference, person, project, decision or fact"}, {"subject", "string", "Who or what it's about, e.g. 'Gloria Gozza', 'HomeGlow', 'food'"}, {"text", "string", "The line to remember"}, {"related", "string", "Optional other subjects, comma-separated"}}, []string{"kind", "subject", "text"},
		func(e *tools.Env, args map[string]any) string {
			d := mindDB()
			if d == nil || theApp == nil {
				return "Error: memory isn't available."
			}
			r := theApp.mindSave(d, mind.Memory{Kind: tools.ArgS(args, "kind"), Subject: tools.ArgS(args, "subject"), Text: tools.ArgS(args, "text"), Related: tools.ArgS(args, "related")})
			switch r.Action {
			case "added":
				return fmt.Sprintf("Saved [%d].", r.ID)
			case "updated":
				return fmt.Sprintf("Updated [%d] (was: %s).", r.ID, r.Old)
			case "same":
				return fmt.Sprintf("Already known [%d].", r.ID)
			}
			return "Error: nothing saved (empty line?)."
		})
	tools.Register("memory_update", "Rewrite one memory line by id (from memory_search) when it changed, so there's never an old and a new version. Optional new kind or subject.",
		[][3]string{{"id", "integer", "Memory id"}, {"text", "string", "The new line"}, {"kind", "string", "Optional new kind"}, {"subject", "string", "Optional new subject"}}, []string{"id", "text"},
		func(e *tools.Env, args map[string]any) string {
			d := mindDB()
			if d == nil {
				return "Error: memory isn't available."
			}
			id := int64(tools.ArgI(args, "id", 0))
			if err := d.Update(id, tools.ArgS(args, "text"), tools.ArgS(args, "kind"), tools.ArgS(args, "subject")); err != nil {
				return "Error: " + err.Error()
			}
			if m, ok := d.Get(id); ok && m.Mirror != 0 {
				if db, err := store.Default(); err == nil {
					_ = db.Edit(m.Mirror, m.Text)
				}
			}
			return fmt.Sprintf("Updated [%d].", id)
		})
}
