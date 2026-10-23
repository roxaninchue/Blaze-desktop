package main

import (
	"context"
	"blaze-desktop/internal/privacy"
	"time"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/tools"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Chats run on their own (v0.9.3): a turn belongs to its chat, not to the screen. The
// user can open another chat, or start a new one, while a turn runs; it keeps going in
// the background, saves into its own chat, and the sidebar shows it working. Opening
// that chat again shows it live. Each chat has its own Mainline, so chats never wait
// on each other.
type session struct {
	id      string
	hist    []llm.Message
	folder  string     // the folder the chat works in (set when its turn starts)
	project string     // its project (set when its turn starts)
	started bool       // project has been set
	tab     string     // work or design, when its turn started
	env     *tools.Env // the tools' shell and folder for this chat's turn (kept if the user switches folders)
	cancel  context.CancelFunc
	ml      *agents.Mainline
	app     *App
	ctxFrom int     // /clear: the model's context starts at this message (the transcript keeps everything)
	detach  bool    // v0.19.14: a new message came in mid-turn: a Cloud run moves to the background
	bg      *bgSink // v0.19.14: a background shadow of a chat (runs_app.go): events go here, never to the screen
}

// tenv is the env this chat's tools run in.
func (a *App) tenv(s *session) *tools.Env {
	if s.env != nil {
		return s.env
	}
	return a.env
}

// main is the chat's own turn runner (created on first use).
func (s *session) main() *agents.Mainline { return s.ml }

func (s *session) running() bool { return s.ml != nil && s.ml.Busy() }

// dir is the chat's folder, or def before its first turn.
func (s *session) dir(def string) string {
	if s.folder != "" {
		return s.folder
	}
	return def
}

// screen is the chat on screen, ready to take a turn.
func (a *App) screen() *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cur == nil {
		a.cur = &session{}
	}
	a.arm(a.cur)
	return a.cur
}

// arm gives s its Mainline. Call with a.mu held.
func (a *App) arm(s *session) {
	if s.ml == nil {
		s.app = a
		s.ml = &agents.Mainline{Run: func(in agents.Input) { a.turn(s, in) }}
	}
}

// nameSession gives a new chat its id. Call with a.mu held.
func (a *App) nameSession(s *session) {
	if s.id == "" {
		proj := s.project
		if !s.started {
			proj = a.prefs.Project
		}
		if proj == "" {
			s.id = mainConv // one mind: Chat mode has one continuous conversation
		} else {
			s.id = time.Now().Format("20060102-150405") + "-" + newID()[:4]
		}
	}
	if s == a.cur {
		a.convID = s.id
	}
}

// startSession marks s's turn as running. Call with a.mu held.
func (a *App) startSession(s *session, cancel context.CancelFunc) {
	a.nameSession(s)
	s.cancel = cancel
	if s == a.cur || !s.started {
		// On screen, the chat works where the screen is: its folder, project and tab.
		s.folder, s.project, s.started, s.tab = a.prefs.Folder, a.prefs.Project, true, a.tab
		if a.env != nil && (s.env == nil || s.env.Root != a.env.Root) {
			e := *a.env
			e.Status = func(st string) { a.temit(s, "status", st) }
			id := s.id
			e.Conv = func() string { return "chat:" + id } // this chat's tools, search and brief (not the one on screen)
			s.env = &e
		}
	}
	if isPrivate(s.id) {
		s.project = "" // a private chat belongs to no project
	}
	if s.tab == "" {
		s.tab = "work"
	}
	a.live[s.id] = s
	a.busy = true
	a.emitLive()
}

// endSession marks s's turn as finished.
func (a *App) endSession(s *session) {
	a.mu.Lock()
	s.cancel = nil
	// The Mainline flips to idle just after this returns; a chat with nothing queued is done.
	if s.ml == nil || s.ml.Pending() == 0 {
		delete(a.live, s.id)
	}
	a.busy = len(a.live) > 0
	a.emitLive()
	a.mu.Unlock()
}

// emitLive tells the sidebar which chats are working. Call with a.mu held.
func (a *App) emitLive() {
	ids := make([]string, 0, len(a.live))
	for id := range a.live {
		ids = append(ids, id)
	}
	if a.ctx != nil {
		go wr.EventsEmit(a.ctx, "live", ids)
	}
}

// Live lists the chats whose turn is running.
func (a *App) Live() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	ids := make([]string, 0, len(a.live))
	for id := range a.live {
		ids = append(ids, id)
	}
	return ids
}

// temit sends a turn's event to the chat view only when that chat is on screen. A
// chat in the background still saves everything; the user sees it when they open it.
func (a *App) temit(s *session, ev string, d any) {
	if s != nil && s.bg != nil {
		s.bg.event(ev, d) // a background shadow turn: nothing reaches the screen (runs_app.go)
		return
	}
	if eventTap != nil {
		eventTap(ev, d) // tests: App.run's events port (run_script_test.go)
	}
	if a.ctx == nil {
		return
	}
	a.mu.Lock()
	on := a.cur == s
	a.mu.Unlock()
	if on {
		wr.EventsEmit(a.ctx, ev, privacy.Event(ev, d)) // never a provider or model in the chrome (0.19.16)
	}
}

// deliverResult wakes the chat a background agent belongs to: the chat on screen, a
// chat whose turn is running, or a closed chat (filed there, and the user is told).
func (a *App) deliverResult(in agents.Input) {
	a.mu.Lock()
	s := a.live[in.Conv]
	if s == nil && a.cur != nil && (a.cur.id == in.Conv || in.Conv == "") {
		s = a.cur
	}
	if s != nil {
		a.arm(s)
	}
	a.mu.Unlock()
	if s == nil {
		appendToConv(in.Conv, llm.Message{Role: "user", Content: in.Text})
		a.emit("toast", map[string]string{"text": "Background agent " + in.Handle + " finished in another chat", "kind": "ok"})
		a.agentElsewhere(in.Handle, in.Conv) // native notification when Blaze is hidden (notify_app.go)
		return
	}
	s.main().Submit(in)
}
