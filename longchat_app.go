package main

// The long-chat engine in the app (plan part B): each turn sends the system prompt,
// the project brief, a rolling summary of older turns and only the last ~12 messages.
// Everything is indexed for search_chat/read_turn; big tool outputs become artifacts.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/longchat"
	"blaze-desktop/internal/mind"
	"blaze-desktop/internal/tools"
)

const longChatLine = "\nLONG CHATS\n- Older turns of this chat may be folded into a summary. If you need something older than the summary, call search_chat (then read_turn for the full message). Never ask the user to repeat something they already said.\n- A tool result that ends with artifact:<id> was set aside because it was long: read_artifact reads the lines you need.\n- update_brief keeps this project's pinned brief (purpose, rules, key files, in progress; under ~600 tokens). Update it when something lasting changes.\n"

// keepWhole are tools whose output the model needs whole (never set aside).
var keepWhole = map[string]bool{"load_skill": true, "read_artifact": true, "read_turn": true, "search_chat": true, "use_skill": true}

// asideResult is what goes into the history for a tool result.
func asideResult(name, res string) string {
	if keepWhole[name] {
		return res
	}
	return longchat.Aside(res)
}

// contextWindow is the current model's context window (0 when unknown).
func (a *App) contextWindow() int {
	if a.cfg == nil || a.cfg.Default == nil {
		return 0
	}
	return llm.Info(a.cfg.Default.Provider, a.cfg.Default.Model).Context
}

// longContext indexes the chat, folds older turns into its summary when it's over
// budget, and returns the messages to send plus the system text to add (brief +
// summary). Any failure falls back to the old 60-message window.
func (a *App) longContext(ctx context.Context, s *session, hist []llm.Message) ([]llm.Message, string) {
	d, err := longchat.Default()
	if err != nil || s.id == "" || isPrivate(s.id) { // a private chat is never indexed
		return trim(hist), ""
	}
	n := d.Indexed(s.id)
	if n > len(hist) { // the chat got shorter (cleared or retried): index it again
		d.Forget(s.id)
		n = 0
	}
	if n < len(hist) {
		var ms []longchat.Msg
		for i := n; i < len(hist); i++ {
			c := hist[i].Content
			if len(c) > 6000 {
				c = c[:6000]
			}
			ms = append(ms, longchat.Msg{Turn: i, Role: hist[i].Role, Content: c})
		}
		_ = d.Index(s.id, len(hist), ms)
	}
	extra := ""
	if b := d.Brief(s.project); b != "" && s.project != "" {
		extra = "\nPROJECT BRIEF (pinned; update_brief edits it)\n" + b + "\n"
	}
	all := hist // the topic view works on absolute turn ids
	// /clear: a fresh context window in the same conversation (the transcript keeps everything).
	a.mu.Lock()
	from := s.ctxFrom
	a.mu.Unlock()
	if from > 0 && from <= len(hist) {
		hist = hist[from:]
		extra += "\nFRESH CONTEXT: the user cleared the context. Earlier turns of this conversation aren't in this prompt; if something from before matters, look it up (memory_search, search_chat) instead of guessing.\n"
	}
	// v0.19: only the active topic goes to the model (topics_app.go); else the v0.18 path.
	if out, tx, ok := a.topicContext(ctx, s, all, from); ok {
		return out, extra + tx
	}
	st := d.State(s.id)
	if from > 0 && st.Upto < from { // a summary from before /clear doesn't belong to this window
		st = longchat.ChatState{}
	}
	sum := func(prev string, chunk []llm.Message) (string, error) {
		a.temit(s, "status", "Summarising older turns")
		msgs := []llm.Message{{Role: "system", Content: longchat.SummarisePrompt},
			{Role: "user", Content: "CURRENT NOTE:\n" + firstNonEmpty(prev, "(none yet)") + "\n\nNEW TURNS:\n" + longchat.Transcript(chunk, st.Upto)}}
		r, err := llm.CompleteWith(ctx, a.cfg, msgs, nil, llm.Opts{})
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(r.Text) == "" {
			return "", fmt.Errorf("empty summary")
		}
		return r.Text, nil
	}
	window := a.contextWindow()
	budget := longchat.Budget(window)
	out, sumText, changed, err := longchat.Context(hist, &st, budget, sum)
	if err == nil && changed {
		_ = d.SetState(s.id, st)
	}
	// The context ladder (one mind): recent turns → rolling summary → lookup.
	lvl := mind.Ladder(longchat.Tokens(hist), len(sumText)/4, longchat.Tokens(out), budget, window, err != nil)
	switch {
	case lvl == mind.Lookup:
		a.temit(s, "status", "Context reset: looking things up from memory")
		return lookupTail(hist), extra + "\n" + mind.LookupRule + "\n"
	case st.Summary == "":
		out = trim(out)
	}
	return out, extra + sumText
}

// lookupTail is the last few messages, starting at a user message so a tool call is
// never cut from its result (level Lookup of the context ladder).
func lookupTail(h []llm.Message) []llm.Message {
	i := len(h) - mind.LookupTail
	if i <= 0 {
		return h
	}
	for i > 0 && h[i].Role != "user" {
		i--
	}
	return h[i:]
}

func firstNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// emitTokens feeds the live card's token meter, and past 80% of the context window
// offers a fresh chat that starts from a summary.
func (a *App) emitTokens(s *session, r *llm.Result, sent []llm.Message) {
	in, out := longchat.Tokens(sent), 0
	if r != nil && r.Usage != nil {
		if r.Usage.Prompt > 0 {
			in = r.Usage.Prompt
		}
		out = r.Usage.Completion
	} else if r != nil {
		out = len(r.Text) / 4
	}
	limit := a.contextWindow()
	if limit <= 0 {
		limit = 32768
	}
	a.temit(s, "tokens", map[string]any{"in": in, "out": out, "limit": limit, "high": in*10 >= limit*8, "bytes": callBytes(sent, r)})
}

// FreshChat starts a new chat that opens with a summary of the current one (offered
// when a chat nears its model's context limit). Returns the new chat's id.
func (a *App) FreshChat() string {
	a.mu.Lock()
	cur := a.cur
	a.mu.Unlock()
	if cur == nil || cur.id == "" {
		return ""
	}
	a.mu.Lock()
	hist := append([]llm.Message(nil), cur.hist...)
	a.mu.Unlock()
	summary := ""
	if d, err := longchat.Default(); err == nil {
		st := d.State(cur.id)
		summary = st.Summary
		if st.Upto < len(hist) {
			msgs := []llm.Message{{Role: "system", Content: longchat.SummarisePrompt},
				{Role: "user", Content: "CURRENT NOTE:\n" + firstNonEmpty(summary, "(none yet)") + "\n\nNEW TURNS:\n" + longchat.Transcript(hist[st.Upto:], st.Upto)}}
			if r, err := llm.CompleteWith(context.Background(), a.cfg, msgs, nil, llm.Opts{}); err == nil && strings.TrimSpace(r.Text) != "" {
				summary = strings.TrimSpace(r.Text)
			}
		}
	}
	if summary == "" {
		summary = longchat.Transcript(hist[max(0, len(hist)-6):], max(0, len(hist)-6))
	}
	ns := &session{hist: []llm.Message{{Role: "assistant", Content: "Picking up from your previous chat. Here's where we are:\n\n" + summary}}}
	a.mu.Lock()
	a.cur = ns
	a.mu.Unlock()
	a.saveConv(ns)
	return ns.id
}

// longChatTools: search_chat, read_turn, read_artifact, update_brief.
func init() {
	chatOf := func(e *tools.Env) string {
		if e == nil || e.Conv == nil {
			return ""
		}
		return strings.TrimPrefix(e.Conv(), "chat:")
	}
	projectOf := func(chat string) string {
		a := theApp
		if a == nil {
			return ""
		}
		a.mu.Lock()
		defer a.mu.Unlock()
		if s := a.live[chat]; s != nil && s.started {
			return s.project
		}
		return a.prefs.Project
	}
	tools.Register("search_chat", "Search earlier conversation for exact words: this conversation (including turns folded into the summary or dropped by a context reset) and, with everywhere=true or when nothing matches here, the whole journal (every past chat, imported or archived). Use it before asking the user to repeat anything.",
		[][3]string{{"query", "string", "Words to look for"}, {"limit", "integer", "Max results (default 5)"}, {"everywhere", "boolean", "Search every past conversation too (the journal)"}}, []string{"query"},
		func(e *tools.Env, args map[string]any) string {
			q, lim := tools.ArgS(args, "query"), tools.ArgI(args, "limit", 5)
			var b strings.Builder
			if d, err := longchat.Default(); err == nil {
				for _, h := range d.Search(chatOf(e), q, lim) {
					fmt.Fprintf(&b, "turn %d (%s): %s\n", h.Turn, h.Role, h.Snippet)
				}
			}
			if md := mindDB(); md != nil && (b.Len() == 0 || tools.ArgS(args, "everywhere") == "true") {
				chat := chatOf(e)
				for _, t := range md.SearchJournal(q, "", lim) {
					if t.Conv == chat && b.Len() > 0 {
						continue
					}
					fmt.Fprintf(&b, "journal %d (%s, %s, %s): %s\n", t.ID, t.Conv, t.Role, time.Unix(t.At, 0).Format("2 Jan 2006"), t.Content)
				}
			}
			if b.Len() == 0 {
				return "No earlier message matches. Try other words, or memory_search."
			}
			b.WriteString("read_turn {id} (or {journal_id}) returns a whole message.")
			return b.String()
		})
	tools.Register("read_turn", "Read one earlier message in full: by the turn id search_chat gave for this conversation, or by journal_id for a journal hit.",
		[][3]string{{"id", "integer", "Turn id in this conversation"}, {"journal_id", "integer", "Or: a journal id from search_chat"}}, nil,
		func(e *tools.Env, args map[string]any) string {
			if jid := tools.ArgI(args, "journal_id", 0); jid > 0 {
				if md := mindDB(); md != nil {
					if t, ok := md.JournalTurn(int64(jid)); ok {
						return fmt.Sprintf("journal %d (%s turn %d, %s):\n%s", t.ID, t.Conv, t.Turn, t.Role, t.Content)
					}
				}
				return "Error: no journal entry with that id."
			}
			d, err := longchat.Default()
			if err != nil {
				return "Error: " + err.Error()
			}
			m, ok := d.Turn(chatOf(e), tools.ArgI(args, "id", -1))
			if !ok {
				return "Error: no message with that turn id in this chat."
			}
			return fmt.Sprintf("turn %d (%s):\n%s", m.Turn, m.Role, m.Content)
		})
	tools.Register("read_artifact", "Read part of a long tool output that was set aside (artifact:<id>), with line numbers.",
		[][3]string{{"id", "string", "The artifact id"}, {"from", "integer", "First line, 1-based (default 1)"}, {"lines", "integer", "How many lines (default 200, max 400)"}}, []string{"id"},
		func(e *tools.Env, args map[string]any) string {
			out, err := longchat.ReadArtifact(tools.ArgS(args, "id"), tools.ArgI(args, "from", 1), tools.ArgI(args, "lines", 200))
			if err != nil {
				return "Error: " + err.Error()
			}
			return out
		})
	tools.Register("update_brief", "Replace this project's pinned brief, sent with every turn: purpose, rules, key files, what's in progress. Under ~600 tokens; write the whole brief each time.",
		[][3]string{{"brief", "string", "The full new brief"}}, []string{"brief"},
		func(e *tools.Env, args map[string]any) string {
			p := projectOf(chatOf(e))
			if p == "" {
				return "This chat isn't in a project, so there's no brief to keep. Use remember for lasting facts."
			}
			d, err := longchat.Default()
			if err != nil {
				return "Error: " + err.Error()
			}
			saved, err := d.SetBrief(p, tools.ArgS(args, "brief"))
			if err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Brief saved (%d characters).", len(saved))
		})
}

// ProjectBrief and SetProjectBrief let the project page show and edit the brief.
func (a *App) ProjectBrief(project string) string {
	d, err := longchat.Default()
	if err != nil {
		return ""
	}
	return d.Brief(project)
}

func (a *App) SetProjectBrief(project, brief string) string {
	d, err := longchat.Default()
	if err != nil {
		return err.Error()
	}
	_, err = d.SetBrief(project, brief)
	if err != nil {
		return err.Error()
	}
	return ""
}
