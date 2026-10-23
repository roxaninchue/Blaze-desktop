package main

// v0.19 one infinite chat (like Blaze Mobile): the Chat tab is the one main conversation, opened on its
// last turns; older turns page in as the user scrolls up, then the archived chats from before one mind,
// newest first. Only the UI side changes: OpenMainPage still opens the whole conversation for the model
// (OpenConversation), it just sends the UI the tail. Nothing in ~/.blaze is moved or deleted.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/chatpage"
	"blaze-desktop/internal/llm"
)

// ChatPage is one page of the main conversation: Messages are msgs[Start:Start+len(Messages)].
type ChatPage struct {
	ID          string        `json:"id"`
	Title       string        `json:"title"`
	Folder      string        `json:"folder"`
	Messages    []llm.Message `json:"messages"`
	Start       int           `json:"start"`
	Total       int           `json:"total"`
	Agents      []agents.View `json:"agents,omitempty"`
	Running     bool          `json:"running,omitempty"`
	ContextFrom int           `json:"context_from,omitempty"`
}

// OpenMainPage opens the main conversation (it becomes the current chat, as OpenMain did) and returns
// its last turns (chatpage.Turns when turns <= 0).
func (a *App) OpenMainPage(turns int) ChatPage {
	c := a.OpenConversation(mainConv)
	start := chatpage.Window(c.Messages, len(c.Messages), turns, chatpage.MaxMsgs)
	ctx := 0
	a.mu.Lock()
	if a.cur != nil && a.cur.id == mainConv {
		ctx = a.cur.ctxFrom
	}
	a.mu.Unlock()
	return ChatPage{ID: mainConv, Title: c.Title, Folder: c.Folder, Messages: c.Messages[start:], Start: start, Total: len(c.Messages),
		Agents: c.Agents, Running: c.Running, ContextFrom: ctx}
}

// mainHist is the main conversation as it is now: the live session's history when it is open or
// running, else the saved file.
func (a *App) mainHist() []llm.Message {
	a.mu.Lock()
	if s := a.live[mainConv]; s != nil {
		h := append([]llm.Message(nil), s.hist...)
		a.mu.Unlock()
		return h
	}
	if a.cur != nil && a.cur.id == mainConv {
		h := append([]llm.Message(nil), a.cur.hist...)
		a.mu.Unlock()
		return h
	}
	a.mu.Unlock()
	var c Conv
	if b, err := os.ReadFile(filepath.Join(convDir(), mainConv+".json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c.Messages
}

// MainHistory returns the page of older turns that ends just before message index before. It doesn't
// change the open chat.
func (a *App) MainHistory(before, turns int) ChatPage {
	h := a.mainHist()
	if before > len(h) || before < 0 {
		before = len(h)
	}
	start := chatpage.Window(h, before, turns, chatpage.MaxMsgs)
	return ChatPage{ID: mainConv, Messages: h[start:before], Start: start, Total: len(h)}
}

// HistoryHit is one search result across the main conversation and the archived chats.
type HistoryHit struct {
	Conv    string `json:"conv"`
	Title   string `json:"title"`
	When    string `json:"when"`
	Index   int    `json:"index"`
	Role    string `json:"role"`
	Snippet string `json:"snippet"`
}

// SearchHistory finds words in the one conversation (newest first) and then in the archived chats.
func (a *App) SearchHistory(q string) []HistoryHit {
	out := []HistoryHit{}
	if strings.TrimSpace(q) == "" {
		return out
	}
	for _, h := range chatpage.Search(a.mainHist(), q, 40) {
		out = append(out, HistoryHit{Conv: mainConv, Title: "Chat", Index: h.Index, Role: h.Role, Snippet: h.Snippet})
	}
	for _, c := range a.allConvs() {
		if c.Project != "" || c.ID == mainConv || len(out) >= 80 {
			continue
		}
		for _, h := range chatpage.Search(c.Messages, q, 3) {
			out = append(out, HistoryHit{Conv: c.ID, Title: c.Title, When: c.Updated.Format("2 Jan 2006"), Index: h.Index, Role: h.Role, Snippet: h.Snippet})
		}
	}
	return out
}
