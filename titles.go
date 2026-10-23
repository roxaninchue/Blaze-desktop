package main

// Chat titles from the dominant theme of the conversation, not its first prompt.
// After the first full exchange, and again when the chat moves on (every 4 user turns,
// or sooner when a long new message lands), a tiny call on the cheapest healthy model
// names the chat in at most 6 words. It never blocks a turn and fails silently.
// Chats the user renamed by hand (title_source "manual") are never retitled.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/llm"
)

const (
	titleAuto    = "auto"
	titleManual  = "manual"
	retitleEvery = 4 // user turns between re-evaluations
)

type titleMeta struct {
	Title  string
	Source string // "", auto, manual
	Turns  int    // user turns when it was last evaluated
}

var titles = struct {
	sync.Mutex
	m        map[string]titleMeta
	inflight map[string]bool
}{m: map[string]titleMeta{}, inflight: map[string]bool{}}

// convWriteMu serialises writes of chat files (saveConv and title patches).
var convWriteMu sync.Mutex

func getTitle(id string) (titleMeta, bool) {
	titles.Lock()
	defer titles.Unlock()
	m, ok := titles.m[id]
	return m, ok
}

func setTitle(id string, m titleMeta) {
	titles.Lock()
	titles.m[id] = m
	titles.Unlock()
}

// rememberTitle loads a saved chat's title state.
func rememberTitle(c Conv) {
	if c.ID == "" || c.TitleSource == "" {
		return
	}
	setTitle(c.ID, titleMeta{Title: c.Title, Source: c.TitleSource, Turns: c.TitleTurns})
}

// promptTitle is the old fallback: the first line of the first prompt.
func promptTitle(h []llm.Message) string {
	for _, m := range h {
		if m.Role == "user" {
			t := strings.TrimSpace(strings.SplitN(stripAttached(m.Content), "\n", 2)[0])
			t = strings.TrimPrefix(t, "[Design edit] ")
			if len(t) > 60 {
				t = t[:60] + "…"
			}
			if t != "" {
				return t
			}
		}
	}
	return "New chat"
}

func (a *App) autoTitlesOn() bool { return a.prefs.AutoTitles == nil || *a.prefs.AutoTitles }

// SetAutoTitles turns AI chat titles on or off (Settings › Behaviour).
func (a *App) SetAutoTitles(on bool) { a.prefs.AutoTitles = &on; a.savePrefs() }

// RenameConversation sets a title by hand; Blaze won't retitle it again.
// An empty title hands the chat back to automatic titles.
func (a *App) RenameConversation(id, title string) string {
	title = strings.Join(strings.Fields(title), " ")
	if len([]rune(title)) > 80 {
		title = string([]rune(title)[:80])
	}
	m := titleMeta{Title: title, Source: titleManual}
	if title == "" {
		m = titleMeta{}
	}
	setTitle(id, m)
	return patchConvTitle(id, m)
}

// patchConvTitle rewrites only the title fields of a saved chat (keeps its date).
func patchConvTitle(id string, m titleMeta) string {
	convWriteMu.Lock()
	defer convWriteMu.Unlock()
	p := filepath.Join(convDir(), id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return m.Title
	}
	var c Conv
	if json.Unmarshal(b, &c) != nil {
		return m.Title
	}
	c.Title, c.TitleSource, c.TitleTurns = m.Title, m.Source, m.Turns
	if c.Title == "" {
		c.Title = promptTitle(c.Messages)
	}
	if out, err := json.Marshal(c); err == nil {
		_ = os.WriteFile(p, out, 0o600)
	}
	return c.Title
}

func userTurns(h []llm.Message) (n int, last string) {
	for _, m := range h {
		if m.Role == "user" && !isAgentResult(m) {
			n++
			last = stripAttached(m.Content)
		}
	}
	return
}

// needsTitle decides whether this point in the chat is worth a title call.
func needsTitle(m titleMeta, h []llm.Message) bool {
	if m.Source == titleManual || len(h) == 0 {
		return false
	}
	end := h[len(h)-1]
	if end.Role != "assistant" || strings.TrimSpace(end.Content) == "" {
		return false // only after a full exchange
	}
	n, last := userTurns(h)
	if n == 0 {
		return false
	}
	if m.Source != titleAuto {
		return true
	}
	since := n - m.Turns
	return since >= retitleEvery || (since >= 2 && len(last) > 600)
}

// maybeRetitle runs after a turn is saved. It returns at once; the call is in the background.
func (a *App) maybeRetitle(id string, hist []llm.Message) {
	if id == "" || !a.autoTitlesOn() || a.ctx == nil {
		return
	}
	m, _ := getTitle(id)
	if !needsTitle(m, hist) {
		return
	}
	titles.Lock()
	if titles.inflight[id] {
		titles.Unlock()
		return
	}
	titles.inflight[id] = true
	titles.Unlock()
	go func() {
		defer func() {
			recover()
			titles.Lock()
			delete(titles.inflight, id)
			titles.Unlock()
		}()
		cur := m.Title
		if cur == "" {
			cur = promptTitle(hist)
		}
		out, err := llm.Quick(a.ctx, a.cfg, titlePrompt(hist, cur), 20*time.Second)
		if err != nil {
			return
		}
		t := cleanTitle(out)
		if t == "" {
			return
		}
		n, _ := userTurns(hist)
		if now, _ := getTitle(id); now.Source == titleManual {
			return // renamed by hand while we were asking
		}
		nm := titleMeta{Title: t, Source: titleAuto, Turns: n}
		setTitle(id, nm)
		patchConvTitle(id, nm)
		if t != cur || m.Source != titleAuto {
			wr.EventsEmit(a.ctx, "title", map[string]string{"id": id, "title": t, "source": titleAuto})
		}
	}()
}

// titlePrompt is short on purpose: the last messages, each trimmed, no tool output.
func titlePrompt(h []llm.Message, current string) []llm.Message {
	const lastN, each, total = 12, 400, 3200
	var lines []string
	size := 0
	for i := len(h) - 1; i >= 0 && len(lines) < lastN; i-- {
		m := h[i]
		if (m.Role != "user" && m.Role != "assistant") || strings.TrimSpace(m.Content) == "" {
			continue
		}
		t := strings.Join(strings.Fields(strings.TrimPrefix(stripAttached(m.Content), "[Design edit] ")), " ")
		if len(t) > each {
			t = t[:each] + "…"
		}
		who := "User"
		if m.Role == "assistant" {
			who = "Assistant"
		}
		l := who + ": " + t
		if size+len(l) > total && len(lines) > 0 {
			break
		}
		size += len(l)
		lines = append([]string{l}, lines...)
	}
	sys := "You name chat conversations for a sidebar. Reply with ONLY a title of 2 to 6 words, in Title Case, " +
		"naming the dominant theme of the whole conversation (what most of it is about), not just the first message. " +
		"No quotes, no emoji, no trailing punctuation, no words like Chat or Conversation. " +
		"If the current title still fits the dominant theme, reply with it unchanged."
	user := "Current title: " + current + "\n\nConversation (latest last):\n" + strings.Join(lines, "\n")
	return []llm.Message{{Role: "system", Content: sys}, {Role: "user", Content: user}}
}

// cleanTitle turns a model reply into a sidebar title: one line, no quotes, emoji or
// markdown, at most 6 words and 60 characters. "" when nothing usable is left.
func cleanTitle(s string) string {
	s = strings.TrimSpace(s)
	// Thinking models sometimes leak <think>…</think>.
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			s = l
			break
		}
	}
	low := strings.ToLower(s)
	for _, p := range []string{"title:", "new title:", "current title:"} {
		if strings.HasPrefix(low, p) {
			s = strings.TrimSpace(s[len(p):])
			low = strings.ToLower(s)
		}
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ':
			b.WriteRune(r)
		case strings.ContainsRune("-&+/.,:'’#@", r):
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		}
	}
	w := strings.Fields(b.String())
	if len(w) > 6 {
		w = w[:6]
	}
	s = strings.Join(w, " ")
	s = strings.Trim(s, " .,:;-'’")
	if r := []rune(s); len(r) > 60 {
		s = strings.TrimSpace(string(r[:60]))
	}
	if strings.EqualFold(s, "new chat") {
		return ""
	}
	return s
}
