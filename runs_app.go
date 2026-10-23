package main

// v0.19.14: "ack now, work in background, report when done" (Site RUNS_ACK_CONTRACT, gateway 0.8.38).
//
// Blaze Cloud (work chats, not private, not Design): every turn is a Cloud run (POST /v1/runs with
// the user's IANA tz). The 202's ack line shows at once as a plain Blaze bubble and the turn ends
// there, so the composer is free; the run keeps going on the gateway (also when the window
// closes), this computer answers its device tool calls, and the final lands later as one plain
// message. No ack (greetings, thanks, chat, questions): the normal typing state until the reply.
//
// Local / own-key models: an action request gets a local ack and runs in a background copy of
// the chat (App.run on a shadow session whose events never reach the screen); the same "Done ✓"
// or "Couldn't finish" message lands when it ends. Anything else runs as before.
//
// No "working" notices. A final that lands while the window isn't focused sends exactly one
// desktop notification, worded like a text from Blaze; nothing while it is focused.

import (
	"context"
	"blaze-desktop/internal/runevents"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/bgrun"
	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/notify"
	"blaze-desktop/internal/persona"
	"blaze-desktop/internal/privacy"
	"blaze-desktop/internal/tools"
)

// bgRuns follows Cloud runs and delivers every final (runs and local) exactly once.
var bgRuns *bgrun.Manager

// Test ports: the gateway's runs API, "is Blaze Cloud on", the device tool runner and the clock
// of polls. nil means the real thing.
var (
	runsAPIOverride bgrun.API
	runsOnOverride  func() bool
	deviceToolTap   func(ctx context.Context, conv, name, args string) string
	runsPoll        time.Duration
	runsSleep       func(ctx context.Context, d time.Duration) error
	runsCreateSleep func(ctx context.Context, d time.Duration) error // tests: the offline retry's waits
)

// winFocused is the window's focus as the frontend reports it (focus / blur / visibility).
var winFocused atomic.Bool

// frontTZ is the IANA zone the frontend read from the OS (Intl), used when Go can't find one.
var frontTZ atomic.Value

func init() { winFocused.Store(true) }

// SetFocused is called by the frontend on window focus, blur and visibility changes. Coming back
// to the front picks up runs that finished meanwhile (at most every 30 s).
func (a *App) SetFocused(on bool) {
	was := winFocused.Swap(on)
	if on && !was {
		a.resumeRuns(30 * time.Second)
	}
}

// SetTimeZone is the frontend's Intl time zone (the OS setting), for platforms where Go can't
// read an IANA name (Windows).
func (a *App) SetTimeZone(tz string) {
	if validTZ(tz) {
		frontTZ.Store(tz)
	}
}

// windowFocused: Blaze's window is on screen and focused.
func (a *App) windowFocused() bool {
	a.sh.mu.Lock()
	v := a.sh.visible
	a.sh.mu.Unlock()
	return v && winFocused.Load()
}

var tzRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)

func validTZ(tz string) bool {
	tz = strings.TrimSpace(tz)
	return tz != "" && tz != "Local" && len(tz) < 64 && tzRe.MatchString(tz)
}

// osTimeZone is the user's IANA zone, from the OS: $TZ, /etc/localtime's link, /etc/timezone,
// then what the frontend read (Intl). "" when none is known (the gateway then marks tz_missing
// and never assumes a zone).
func osTimeZone() string {
	if tz := strings.TrimPrefix(os.Getenv("TZ"), ":"); validTZ(tz) {
		return tz
	}
	if runtime.GOOS != "windows" {
		if p, err := filepath.EvalSymlinks("/etc/localtime"); err == nil {
			if i := strings.Index(p, "zoneinfo/"); i >= 0 && validTZ(p[i+9:]) {
				return p[i+9:]
			}
		}
		if b, err := os.ReadFile("/etc/timezone"); err == nil && validTZ(strings.TrimSpace(string(b))) {
			return strings.TrimSpace(string(b))
		}
	}
	if s, _ := frontTZ.Load().(string); validTZ(s) {
		return s
	}
	if n := time.Local.String(); validTZ(n) {
		return n
	}
	return ""
}

func (a *App) runsAPI() bgrun.API {
	if runsAPIOverride != nil {
		return runsAPIOverride
	}
	if cloudModels == nil || cloudModels.GW == nil {
		return nil
	}
	return &bgrun.HTTP{Base: cloudModels.GW.Base, Token: a.blazeToken, HC: cloudModels.GW.HC}
}

// runsOn: Blaze Cloud answers and Cloud runs aren't switched off (BLAZE_RUNS=0).
func (a *App) runsOn() bool {
	if runsOnOverride != nil {
		return runsOnOverride()
	}
	if os.Getenv("BLAZE_RUNS") == "0" {
		return false
	}
	return cloudModelsOn() && a.blazeToken() != ""
}

// startBgRuns sets up the follower and picks up runs left from the last session.
func (a *App) startBgRuns() {
	a.ensureBgRuns()
	go func() {
		time.Sleep(3 * time.Second)
		a.resumeRuns(0)
	}()
}

var bgOnce sync.Mutex

func (a *App) ensureBgRuns() *bgrun.Manager {
	bgOnce.Lock()
	defer bgOnce.Unlock()
	if bgRuns != nil {
		return bgRuns
	}
	bgRuns = &bgrun.Manager{
		API:      a.runsAPI(),
		Store:    &bgrun.Store{Path: filepath.Join(config.Dir(), "runs-pending.json")},
		Follower: a.followerFor,
		Deliver:  a.deliverFinal,
		Notifier: &bgrun.Notifier{Focused: a.windowFocused, Send: a.notifyFinal},
	}
	return bgRuns
}

func (a *App) follower(conv string) *bgrun.Follower {
	return &bgrun.Follower{API: a.runsAPI(), Device: a.deviceFor(conv), Poll: runsPoll, Sleep: runsSleep}
}

// followerFor follows run e and tells the chat what it is doing (working pill, delivery status)
// through the one adapter, internal/runevents. Steps are never notifications.
func (a *App) followerFor(e bgrun.Entry) *bgrun.Follower {
	f := a.follower(e.Conv)
	tr := &runevents.Tracker{RunID: e.RunID, ClientMsgID: e.ClientMsgID, Request: e.Ask}
	var mu sync.Mutex
	f.OnEvent = func(ev bgrun.Event) {
		mu.Lock()
		out := tr.FromRun(ev)
		mu.Unlock()
		a.emitRun(e.Conv, out...)
	}
	f.OnCall = func(c bgrun.ToolCall) {
		mu.Lock()
		out := tr.FromToolCall(c.Function.Name, friendlyTool(c.Function.Name, c.Function.Arguments))
		mu.Unlock()
		a.emitRun(e.Conv, out...)
	}
	return f
}

// clipAsk keeps the first line of a request, short, for Entry.Ask.
func clipAsk(s string) string {
	s = strings.TrimSpace(strings.SplitN(s, "\n", 2)[0])
	if r := []rune(s); len(r) > 200 {
		s = string(r[:200])
	}
	return s
}

// emitRun sends run events to the chat view ("run_event"; the frontend's runEvents.js reads them).
func (a *App) emitRun(conv string, evs ...runevents.Ev) {
	for _, ev := range evs {
		ev.Conv = conv
		if eventTap != nil {
			eventTap("run_event", ev)
		}
		a.emit("run_event", ev)
	}
}

var lastResume atomic.Int64

// resumeRuns: GET /v1/runs and pick up our unfinished runs (finals that landed while Blaze was
// away, device tool calls waiting for this computer). every throttles repeated calls.
func (a *App) resumeRuns(every time.Duration) {
	m := a.ensureBgRuns()
	if m.API == nil || len(m.Store.All()) == 0 {
		return
	}
	now := time.Now().UnixNano()
	if every > 0 && now-lastResume.Load() < int64(every) {
		return
	}
	lastResume.Store(now)
	if a.runsAPIOK() {
		_, _ = m.Resume(a.bgCtx()) // the follows it starts live as long as the app
	}
}

func (a *App) runsAPIOK() bool {
	return runsAPIOverride != nil || a.blazeToken() != ""
}

// bgCtx lives as long as the app (background follows).
func (a *App) bgCtx() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

// ---------------- the turn ----------------

// bgTurn handles a user turn the ack-and-background way. false: run it the classic way.
func (a *App) bgTurn(ctx context.Context, s *session, in agents.Input) bool {
	if in.Kind != "" && in.Kind != "user" {
		return false
	}
	a.mu.Lock()
	last := ""
	for i := len(s.hist) - 1; i >= 0; i-- {
		if s.hist[i].Role == "user" {
			last = s.hist[i].Content
			break
		}
	}
	design := s.tab == "design" || strings.HasPrefix(last, "[Design edit]")
	private := isPrivate(s.id)
	s.detach = false
	a.mu.Unlock()
	if design || private || last == "" || strings.HasPrefix(last, continueTag) {
		return false
	}
	// Only a request to DO something (a change on this computer or out in the world) goes the
	// ack-and-background way. Greetings, chat, questions, look-ups and summaries stay the classic
	// streaming turn: the gateway's runs return a step's text whole (no token stream), so routing
	// them through a run made the first word wait for the whole reply (3-20 s in the 0.19.14
	// strict run), and they get no ack anyway.
	intent, ok := bgAction(last)
	if !ok {
		return false
	}
	if a.runsOn() {
		return a.cloudRun(ctx, s, last, intent)
	}
	return a.localBg(s, last)
}

// runRequest is the run's messages and tools: the same system prompt and history as a classic
// turn, Blaze's own tools (they run on this computer when the gateway asks).
func (a *App) runRequest(ctx context.Context, s *session) ([]llm.Message, []llm.Tool) {
	a.mu.Lock()
	full := append([]llm.Message(nil), s.hist...)
	id := s.id
	a.mu.Unlock()
	hist, longExtra := a.longContext(withSkipFold(ctx), s, full)
	hist = dropAcks(hist)
	conv := "chat:" + id
	tools.ActivateFor(conv, lastUserText(hist)) // a run's tools are fixed at start: find_tools inside it can't add them
	q := recentQuery(hist)
	sys := promptOrder(a.systemStable(q), longChatLine, a.systemLive(q), tools.ExtraLine(conv), longExtra, "") + weatherLine
	msgs := append([]llm.Message{{Role: "system", Content: persona.System()}, {Role: "system", Content: sys + a.firstNameLine()}}, hist...)
	var all []llm.Tool
	all = append(all, tools.Select(conv)...)
	if a.conn != nil {
		all = append(all, a.conn.Tools()...)
	}
	all = append(all, updateBlazeTool(), weatherTool())
	return msgs, a.modeTools(all)
}

// cloudRun is one Blaze Cloud turn as a Cloud run. false: the gateway couldn't take it (an older
// gateway, too large, unreachable), so the classic loop answers instead.
// intent is the app's own read of the request (bgAction): it is an action, so when the gateway
// sends no ack line (live 0.19.14: "Run this command and tell me its output" came back without
// one and the chat sat on the typing state for 9-15 s) the local ack line is shown instead.
func (a *App) cloudRun(ctx context.Context, s *session, last, intent string) bool {
	api := a.runsAPI()
	m := a.ensureBgRuns()
	if api == nil {
		return false
	}
	msgs, sent := a.runRequest(ctx, s)
	req := bgrun.CreateReq{
		ClientMsgID: bgrun.NewClientMsgID(),
		TZ:          osTimeZone(),
		Messages:    llm.Wire(llm.Sanitize(msgs), true, false),
		Task:        cloudai.Task(msgs, sent, false),
	}
	if len(sent) > 0 {
		req.Tools = llm.AdaptTools(sent, "")
	}
	// Offline: the message stays "Sending" and is tried again with the same client_msg_id (the
	// gateway is idempotent on it: no second run, no duplicate message).
	cr, err := runevents.Create(ctx, api, req, func(ev runevents.Ev) { a.emitRun(s.id, ev) }, runsCreateSleep)
	if err != nil {
		code := bgrun.StatusOf(err)
		if code == 401 || code == 403 {
			line := cloudai.Friendly(code, err.Error())
			if code == 401 {
				line = cloudai.Expired
			}
			a.temit(s, "error", map[string]any{"message": line, "kind": "model"})
			a.finishTurn(s)
			return true
		}
		return false
	}
	ack := bgrun.AckLine(cr.Ack)
	e := bgrun.Entry{RunID: cr.RunID, ClientMsgID: req.ClientMsgID, Conv: s.id, Action: ack != "" || cr.Ack != nil, Created: time.Now(), Ask: clipAsk(stripAttached(last))}
	if cr.Ack != nil {
		e.Intent = cr.Ack.Intent
	}
	if ack == "" && intent != "" && cloudLocalAck {
		if li, line := bgrun.LocalAck(stripAttached(last), true); line != "" {
			ack, e.Action = line, true
			if e.Intent == "" {
				e.Intent, e.LocalIntent = li, true
			}
		}
	}
	// Held from the start, so a resume (start-up, refocus) can't take the run between Track and
	// its follow (a foreground turn would then never end).
	m.Hold(e.RunID)
	m.Track(e)
	if ack != "" {
		a.emitRun(s.id, runevents.For(cr, e.ClientMsgID).Ack()...)
		// The promise, at once, as a plain Blaze bubble. The turn ends here: the composer is free
		// while the run works, and the final lands as its own message.
		a.showAck(s, ack)
		a.finishTurn(s)
		m.Unhold(e.RunID)
		m.Follow(a.bgCtx(), e, 0)
		return true
	}
	// No ack: the typing state until the reply (the composer still takes a new message, which
	// moves this run to the background).
	// The run stays held while this turn watches it: the reply lands in this turn's bubble and the
	// turn always ends.
	f := a.followerFor(e)
	fin, ferr := f.Follow(ctx, e.RunID, 0, e.Action)
	if fin.Intent == "" {
		fin.Intent = e.Intent
	}
	if ferr == nil {
		m.FinishHeld(e, fin)
		a.showFinal(s, fin)
	} else {
		m.Unhold(e.RunID)
	}
	switch {
	case ferr == nil:
	case ctx.Err() != nil:
		a.mu.Lock()
		detach := s.detach
		a.mu.Unlock()
		if detach {
			// A new message came in: this run keeps going and lands as its own message.
			m.Follow(a.bgCtx(), e, 0)
		} else {
			a.temit(s, "stopped", map[string]any{"resumable": false})
		}
	default:
		// Lost touch with the gateway: the run stays tracked and the next resume shows it.
		a.temit(s, "error", map[string]any{"message": cloudai.Unreachable, "kind": "model"})
	}
	a.finishTurn(s)
	return true
}

// dropAcks leaves the ack bubbles out of what the model sees: they stay in the chat for the user,
// but a history full of "On it. I'll confirm when it's done." taught the model to answer a new
// request with the same line and no tool call (live 0.19.14, files run with prior history).
//
// A task whose final hasn't landed yet (its ack is followed by a newer user message or ends the
// chat) is left out too, request and ack: it is being done in the background, and a model that
// sees the request unanswered does it again or answers it inside the next reply (live 0.19.14 N3:
// "51\n\nI'll create bg-1.txt…").
func dropAcks(h []llm.Message) []llm.Message {
	out := h[:0:0]
	for i, m := range h {
		if m.Role == "assistant" && len(m.ToolCalls) == 0 && bgrun.IsAckLine(m.Content) {
			pending := i+1 >= len(h) || h[i+1].Role == "user"
			if pending && len(out) > 0 && out[len(out)-1].Role == "user" {
				out = out[:len(out)-1]
			}
			continue
		}
		if m.Role == "assistant" && strings.HasPrefix(m.Content, "Done ✓") {
			// "Done ✓" is the chat's mark for a confirmed result, not the model's words: a model
			// that sees it starts its own replies with it, tool or not (live 0.19.14 memsave run
			// with prior history: "Done ✓ Got it…" and nothing saved).
			m.Content = strings.TrimSpace(strings.TrimPrefix(m.Content, "Done ✓"))
			if m.Content == "" {
				m.Content = "Done."
			}
		}
		out = append(out, m)
	}
	return out
}

// cloudLocalAck: a Cloud action run the gateway sent no ack line for gets the local one. Tests of
// the no-ack foreground follow (kept for a run with no ack at all) switch it off.
var cloudLocalAck = true

// showAck shows the ack as Blaze's bubble and keeps it in the chat.
func (a *App) showAck(s *session, line string) {
	a.temit(s, "delta", line)
	a.mu.Lock()
	s.hist = append(s.hist, llm.Message{Role: "assistant", Content: line})
	a.mu.Unlock()
	a.temit(s, "done", map[string]any{"ack": true, "route": cloudai.Name})
}

// showFinal shows a final in the turn's own bubble (foreground).
func (a *App) showFinal(s *session, f bgrun.Final) {
	a.mu.Lock()
	ask := stripAttached(lastUserText(s.hist))
	a.mu.Unlock()
	text := privacy.OwnerReply(ask, f.Message()) // v0.19.16 owner guard, as in run()
	a.temit(s, "delta", text)
	a.mu.Lock()
	s.hist = append(s.hist, llm.Message{Role: "assistant", Content: text})
	a.mu.Unlock()
	d := map[string]any{"route": cloudai.Name, "final": f.Kind}
	a.temit(s, "done", d)
}

// finishTurn ends a turn that bgTurn handled: the chat is idle and saved.
func (a *App) finishTurn(s *session) {
	a.endSession(s)
	a.saveConv(s)
	a.mu.Lock()
	id, hist := s.id, append([]llm.Message(nil), s.hist...)
	a.mu.Unlock()
	if a.ctx != nil && !isPrivate(id) {
		a.maybeRetitle(id, hist)
	}
}

// deliverFinal puts a background final in its chat: the open session (on screen or working) or
// the saved chat, and tells the chat view.
func (a *App) deliverFinal(e bgrun.Entry, f bgrun.Final) {
	msg := llm.Message{Role: "assistant", Content: privacy.OwnerReply(e.Ask, f.Message())} // v0.19.16 owner guard
	a.mu.Lock()
	s := a.live[e.Conv]
	if s == nil && a.cur != nil && a.cur.id == e.Conv {
		s = a.cur
	}
	if s != nil {
		s.hist = insertFinal(s.hist, msg, s.running())
	}
	a.mu.Unlock()
	if s != nil {
		a.saveConv(s)
	} else {
		appendToConv(e.Conv, msg)
	}
	ev := map[string]any{"conv": e.Conv, "text": msg.Content, "kind": f.Kind, "run_id": e.RunID}
	if eventTap != nil {
		eventTap("blaze_msg", ev)
	}
	a.emit("blaze_msg", ev)
}

// insertFinal adds a background final to a chat's history. While a newer turn is running in that
// chat, the final goes in before that turn's user message: appended after it, the running turn
// (and any retry it builds from the history) would see the chat end on Blaze's "Done ✓" instead of
// the user's question, and could answer the old task again (live 0.19.14 N3 run 1).
func insertFinal(hist []llm.Message, msg llm.Message, running bool) []llm.Message {
	if running {
		for i := len(hist) - 1; i >= 0; i-- {
			if hist[i].Role == "user" {
				out := make([]llm.Message, 0, len(hist)+1)
				out = append(out, hist[:i]...)
				out = append(out, msg)
				return append(out, hist[i:]...)
			}
		}
	}
	return append(hist, msg)
}

// notifyFinal is the one desktop notification for a final (the window isn't focused).
func (a *App) notifyFinal(conv, text string) {
	if notifyTap != nil {
		notifyTap(conv, text)
		return
	}
	if a.ctx == nil || a.prefs.Notify.Off || a.prefs.Notify.NoTasks {
		return
	}
	if isPrivate(conv) {
		text = "Blaze answered in your private chat."
	}
	_, _ = a.send(notify.Note{Title: "Blaze", Body: text, Kind: "task", Chat: conv})
}

// notifyTap lets tests see the desktop notifications.
var notifyTap func(conv, text string)

// ---------------- device tools ----------------

var badResult = regexp.MustCompile(`^(Error|exit [1-9]|Refused|Blaze couldn't|Tool error)|did NOT approve`)

// toolFailed is a tool result that says it didn't work (same rule as the chat's step rows).
func toolFailed(res string) bool { return badResult.MatchString(strings.TrimSpace(res)) }

// deviceFor runs the gateway's tool calls for a run of chat conv on this computer.
func (a *App) deviceFor(conv string) bgrun.Device {
	return func(ctx context.Context, c bgrun.ToolCall) (string, bool) {
		name, args := c.Function.Name, c.Function.Arguments
		if strings.TrimSpace(args) == "" {
			args = "{}"
		}
		var out string
		switch {
		case deviceToolTap != nil:
			out = deviceToolTap(ctx, conv, name, args)
		case a.modeBlock(name, args) != "":
			out = a.modeBlock(name, args)
		case agents.IsMainTool(name):
			out = "Error: background agents aren't available in Cloud runs."
		case name == "update_blaze":
			out = a.selfUpdate()
		case name == "get_weather":
			out = a.runWeather(ctx, args)
		case a.conn != nil && a.conn.Handles(name):
			out = a.conn.Run(ctx, name, args)
		default:
			out = tools.RunCtx(ctx, a.envFor(conv), name, args)
		}
		return out, !toolFailed(out)
	}
}

// envFor is the tools' env of chat conv (its own folder and shell when it is open).
func (a *App) envFor(conv string) *tools.Env {
	a.mu.Lock()
	defer a.mu.Unlock()
	if s := a.live[conv]; s != nil && s.env != nil {
		return s.env
	}
	if a.cur != nil && a.cur.id == conv && a.cur.env != nil {
		return a.cur.env
	}
	return a.env
}

// ---------------- local / own-key ----------------

// bgSink collects a shadow turn's events (they never reach the screen).
type bgSink struct {
	mu       sync.Mutex
	kind     string // done | error | stopped
	errLine  string
	noProof  bool
	tools    int
	lastBad  bool
	anyTools bool
	// step sees each tool call of the shadow turn (the working pill's line, from local intent).
	step func(name, args string)
}

func (b *bgSink) event(ev string, d any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch ev {
	case "done":
		b.kind = "done"
		if m, ok := d.(map[string]any); ok {
			if u, _ := m["unverified"].(bool); u {
				b.noProof = true
			}
		}
	case "error":
		b.kind = "error"
		switch v := d.(type) {
		case string:
			b.errLine = v
		case map[string]any:
			b.errLine, _ = v["message"].(string)
		}
	case "stopped":
		b.kind = "stopped"
	case "tool":
		if m, ok := d.(map[string]string); ok && b.step != nil {
			go b.step(m["name"], m["args"])
		}
	case "tool_done":
		if m, ok := d.(map[string]string); ok {
			b.tools++
			b.anyTools = true
			b.lastBad = toolFailed(m["result"])
		}
	}
}

// final is the shadow turn's final; base is how long its history was when it started.
func (b *bgSink) final(s *session, base int, id, intent string) bgrun.Final {
	b.mu.Lock()
	defer b.mu.Unlock()
	f := bgrun.Final{RunID: id, Action: true, DeviceOK: true, Intent: intent}
	reply := ""
	for i := len(s.hist) - 1; i >= base && i >= 0; i-- {
		if s.hist[i].Role == "assistant" && strings.TrimSpace(s.hist[i].Content) != "" {
			reply = s.hist[i].Content
			break
		}
	}
	switch b.kind {
	case "done":
		f.Kind, f.Reply = bgrun.KindDone, reply
		f.DeviceRan = b.anyTools
		f.DeviceOK = !b.lastBad
		f.NoProof = b.noProof
	case "stopped":
		f.Kind, f.Reason, f.Try = bgrun.KindFailed, "It was stopped before it finished.", "Ask again when you're ready."
	default:
		line := strings.TrimSpace(b.errLine)
		if line == "" {
			line = "Something went wrong before it finished."
		}
		f.Kind, f.Reason, f.Try = bgrun.KindFailed, line, "Retry and I'll have another go."
	}
	return f
}

// localBg: an action request on a local / own-key model. The ack shows at once, the composer is
// free, and the work runs on a shadow copy of the chat in the background.
func (a *App) localBg(s *session, last string) bool {
	intent, line := bgrun.LocalAck(stripAttached(last), true)
	if line == "" {
		return false
	}
	m := a.ensureBgRuns()
	a.mu.Lock()
	base := append([]llm.Message(nil), s.hist...)
	id := "local-" + newID()
	tr := &runevents.Tracker{RunID: id, Request: stripAttached(last)}
	var trMu sync.Mutex
	sink := &bgSink{step: func(name, args string) {
		trMu.Lock()
		out := tr.FromToolCall(name, friendlyTool(name, args))
		trMu.Unlock()
		a.emitRun(s.id, out...)
	}}
	sh := &session{id: s.id, hist: base, folder: s.folder, project: s.project, started: true, tab: s.tab,
		app: a, ctxFrom: s.ctxFrom, bg: sink}
	if s.env != nil {
		e := *s.env
		e.Status = func(string) {} // no "working" lines
		sh.env = &e
	}
	a.mu.Unlock()
	// A local run: this computer has the message at once, and the ack is Blaze reading it.
	a.emitRun(s.id, runevents.Accepted(&bgrun.Created{RunID: id}, ""))
	trMu.Lock()
	a.emitRun(s.id, tr.Ack()...)
	trMu.Unlock()
	a.showAck(s, line)
	a.finishTurn(s)
	e := bgrun.Entry{RunID: id, Conv: s.id, Action: true, Intent: intent, Created: time.Now(), Ask: clipAsk(stripAttached(last))}
	go func() {
		a.run(a.bgCtx(), sh)
		m.Land(e, sh.bg.final(sh, len(base), id, intent))
	}()
	return true
}
