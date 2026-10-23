package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"blaze-desktop/internal/persona"
	"blaze-desktop/internal/privacy"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"blaze-desktop/internal/router"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/cloudai"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/connect"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/longchat"
	"blaze-desktop/internal/sched"
	"blaze-desktop/internal/sysinfo"
	"blaze-desktop/internal/tools"
)

// Prefs are desktop-only settings; models and keys are shared with the CLI in ~/.blaze.
type Prefs struct {
	Theme   string   `json:"theme"`  // system, light, dark
	Folder  string   `json:"folder"` // current project folder
	Recent  []string `json:"recent"`
	Welcome bool     `json:"welcome_done"`
	Project string   `json:"project"`     // current project id ("" = General)
	Design  string   `json:"design_mode"` // ask, always, never

	NoUpdateCheck bool   `json:"no_update_check"` // don't look for updates on launch
	SkipUpdate    string `json:"skip_update"`     // a release the user chose to skip, e.g. v0.3.0

	CloudDailyCap int `json:"cloud_daily_cap"` // v0.19.9: most Blaze Cloud messages a day the user allows (0 = none)

	Background *bool  `json:"background,omitempty"` // closing the window keeps Blaze in the tray (default on)
	Autostart  bool   `json:"autostart"`            // start Blaze (hidden) when the user signs in
	Hotkey     string `json:"hotkey"`               // global shortcut preset; "" = default, "off"
	ShowSteps  bool   `json:"show_steps"`           // show every tool step instead of one status line
	FoldCards  *bool  `json:"fold_cards,omitempty"` // fold long answers into expandable cards (default on)

	AutoTitles *bool `json:"auto_titles,omitempty"` // AI chat titles from the chat's dominant theme (default on, titles.go)

	Personality       string `json:"personality"`        // "" / default, plain (no block), custom
	PersonalityCustom string `json:"personality_custom"` // the user's own personality text (custom)

	OnboardStep    int  `json:"onboard_step"`    // first-run onboarding: the step to resume at
	OnboardSkipped bool `json:"onboard_skipped"` // onboarding was skipped or finished; don't open it by itself

	Perms PermPrefs `json:"perms"` // v0.19.14 just-in-time permission answers (permissions.go)

	MemoryAISort *bool `json:"memory_ai_sort,omitempty"` // let a model sort new memories into categories (default on)

	Composer   ComposerPrefs `json:"composer"`              // work mode, approval mode, voice (modes.go)
	Notify     NotifyPrefs   `json:"notify"`                // notifications and proactive suggestions (notify_app.go)
	Vision     VisionPrefs   `json:"vision"`                // screen reading (vision_app.go)
	MaxAgents  int           `json:"max_agents,omitempty"`  // background agents at once (0 = default, bgagents.go)
	Learn      LearnPrefs    `json:"learn"`                 // learn automatically + chat marks (learn_app.go)
	Studio     StudioPrefs   `json:"studio"`                // Design studio: critic, taste, shared comments (studio_app.go)
	MindReview *bool         `json:"mind_review,omitempty"` // review past turns into memory when idle (mind_app.go; default on)
	LowData    bool          `json:"low_data,omitempty"`    // Low-data mode: text first, no screenshots, MB per task (student_app.go)
}

type App struct {
	conv  atomic.Value // the current chat id, for convKey (read without a.mu)
	ctx   context.Context
	cfg   *config.Config
	info  sysinfo.Info
	prefs Prefs
	env   *tools.Env

	mu      sync.Mutex
	busy    bool                // a turn is running in some chat (open or in the background)
	live    map[string]*session // chats whose turn runs, by id (sessions.go)
	cur     *session            // the chat on screen
	pending map[string]chan int // confirm cards: 0 no, 1 yes, 2 always
	conn    *connect.Manager
	cloud   *cloudState // Blaze account + one-click connectors (account.go)
	convID  string      // the chat on screen (a.cur.id once it has one)
	pv      *preview
	ready   chan struct{} // closed when startup has finished
	tab     string        // work or design

	sh       shellState // window, orb and tray state (shell.go)
	quitting bool       // a real quit is under way; don't hide on close
	flags    startFlags // command-line flags (--background, --orb, --relaunch)

	// Background agents (bgagents.go).
	agents    *agents.Manager
	envs      map[string]*tools.Env // each sub-agent's own shell and approval gate
	questions map[string]chan string

	router *router.Router // BlazeRouter, the local AI gateway (router_app.go)
	pair   *pairHub       // Blaze Link: phone pairing server (pair.go)
}

func NewApp() *App {
	return &App{pending: map[string]chan int{}, ready: make(chan struct{}), live: map[string]*session{}, cur: &session{}}
}

// waitReady blocks until startup is done. Wails runs OnStartup in a goroutine while
// the page loads, so the UI's first calls can arrive before cfg/env exist.
func (a *App) waitReady() {
	select {
	case <-a.ready:
	case <-time.After(30 * time.Second):
	}
}

func prefsFile() string { return filepath.Join(config.Dir(), "desktop.json") }

func (a *App) startup(ctx context.Context) {
	defer close(a.ready)
	a.ctx = ctx
	cleanupOldBinary()
	a.cfg = config.Load()
	a.info = sysinfo.Detect()
	b, _ := os.ReadFile(prefsFile())
	_ = json.Unmarshal(b, &a.prefs)
	if a.prefs.Theme == "" {
		a.prefs.Theme = "system"
	}
	if a.prefs.Design == "" {
		a.prefs.Design = "ask"
	}
	if st, err := os.Stat(a.prefs.Folder); err != nil || !st.IsDir() {
		a.prefs.Folder = a.info.Home
	}
	a.migrateComposer()
	a.pv = &preview{}
	a.pv.start()
	a.pv.load(a.prefs.Folder)
	a.startStudio()
	a.pair = newPairHub(a) // Blaze Link: starts its server only when a phone is paired
	a.newEnv()
	a.startConnectors()
	a.startCloud()
	a.startCloudModels() // v0.19: Blaze Cloud by default, or Custom (cloudmodels_app.go)
	a.startBgRuns()      // v0.19.14: ack now, work in background, report when done (runs_app.go)
	a.startComposer()
	a.startAgents()
	a.startShell()
	a.startNotify()
	a.startRouter()      // BlazeRouter: local OpenAI-compatible gateway + dashboard (router_app.go)
	a.startLearning()    // bundled skills + proactive memory (learn_app.go)
	a.startMind()        // one mind: journal, memories table, idle review (mind_app.go)
	go tools.SyncIndex() // every tool is registered by now (init); index them for find_tools
}

func (a *App) shutdown(ctx context.Context) {
	if a.agents != nil {
		a.agents.StopAll() // saved as running, so the next launch offers Resume
	}
	if a.env != nil {
		a.env.Jobs.StopAll()
	}
	if a.conn != nil {
		a.conn.Close()
	}
	a.stopRouter()
	if a.pair != nil {
		a.pair.close()
	}
	stopTray()
}

func (a *App) savePrefs() {
	_ = os.MkdirAll(config.Dir(), 0o700)
	b, _ := json.MarshalIndent(a.prefs, "", "  ")
	_ = os.WriteFile(prefsFile(), b, 0o600)
}

func (a *App) newEnv() {
	var jobs *tools.Jobs
	if a.env != nil {
		jobs = a.env.Jobs
	} else {
		jobs = &tools.Jobs{}
	}
	a.env = &tools.Env{
		Info:  &a.info,
		Root:  a.prefs.Folder,
		Yolo:  false, // the desktop's Approval mode decides (gate in modes.go); auto_approve stays a CLI setting
		Shell: tools.NewShell(a.info.Shell, a.info.ShellPath, a.prefs.Folder),
		Jobs:  jobs,
	}
	a.env.Conv = a.convKey
	a.env.Confirm = a.gate
	a.env.Status = func(s string) { wr.EventsEmit(a.ctx, "status", privacy.Event("status", s)) }
	if a.pair != nil {
		a.env.Phone = a.pair.askPhone
	}
	a.env.Approve = a.confirm // a real yes on screen, even in Autopilot (messages sent through the phone)
}

// confirm shows the in-app confirm card and blocks the agent until the user answers.
func (a *App) confirm(title, detail string) bool { return a.ask(title, detail, false) > 0 }

// Answer is called by a confirm card's Yes / No.
func (a *App) Answer(id string, yes bool) {
	if yes {
		a.AnswerChoice(id, "once")
	} else {
		a.AnswerChoice(id, "no")
	}
}

func newID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------------- state for the UI ----------------

type State struct {
	Version     string            `json:"version"`
	SetupDone   bool              `json:"setup_done"`
	Info        sysinfo.Info      `json:"info"`
	Missing     []string          `json:"missing"`
	Installs    map[string]string `json:"installs"`
	Providers   []ProviderView    `json:"providers"`
	Chain       []config.Entry    `json:"chain"`
	Prefs       Prefs             `json:"prefs"`
	Yolo        bool              `json:"yolo"`
	Modes       ModeView          `json:"modes"`
	VisionModel string            `json:"vision_model"` // the first model that can see images ("" = none; composer)
	Persona     string            `json:"persona"`
	Keyless     bool              `json:"keyless"`
	Skills      []string          `json:"skills"`
	Memory      string            `json:"memory"`
	Project     *Project          `json:"project"`
	Model       string            `json:"model"`
	Selection   Selection         `json:"selection"`
	Routing     RoutingView       `json:"routing"`
	Desktop     DesktopView       `json:"desktop"`
	Connected   int               `json:"connected"` // connectors + MCP servers ready
	Notify      NotifyView        `json:"notify"`
	Vision      VisionPrefs       `json:"vision"`
}

func (a *App) State() State {
	a.waitReady()
	prefs := a.prefs
	fold := a.foldCards()
	prefs.FoldCards = &fold // always explicit for the UI
	autoT := a.autoTitlesOn()
	prefs.AutoTitles = &autoT
	if prefs.Personality == "" {
		prefs.Personality = "default"
	}
	aiSort := a.memAISort()
	prefs.MemoryAISort = &aiSort
	s := State{Version: version, SetupDone: a.cfg.SetupDone, Info: a.info, Chain: a.cfg.RouteChain(), Prefs: prefs,
		Yolo: a.cfg.AutoApprove, Persona: a.cfg.Persona, Keyless: a.cfg.AllowKeyless(), Missing: a.info.Missing(),
		Installs: map[string]string{}, Memory: tools.Memory(), Project: a.project(), Model: a.modelName(),
		Selection: a.selection(), Routing: a.routing(), Providers: a.providerViews(), Desktop: a.desktopView(), Connected: a.conn.ConnectedCount(),
		Notify: a.notifyView(false), Vision: a.prefs.Vision,
		Modes: a.modeView(), VisionModel: llm.VisionModel(llm.Targets(a.cfg, false))}
	for _, m := range s.Missing {
		s.Installs[m] = a.info.InstallCommand(m)
	}
	for _, sk := range tools.Skills() {
		s.Skills = append(s.Skills, sk.Name+": "+sk.Desc)
	}
	return s
}

// ---------------- setup & settings ----------------

func (a *App) FinishSetup() {
	a.cfg.SetupDone = true
	a.prefs.Welcome = true
	_ = a.cfg.Save()
	a.savePrefs()
}

func (a *App) SetPersona(p string)    { a.cfg.Persona = strings.TrimSpace(p); _ = a.cfg.Save() }
func (a *App) SetYolo(v bool)         { a.cfg.AutoApprove = v; _ = a.cfg.Save() } // the CLI's --yolo default; the desktop uses Approval mode
func (a *App) SetTheme(t string)      { a.prefs.Theme = t; a.savePrefs() }
func (a *App) SetDesignMode(m string) { a.prefs.Design = m; a.savePrefs() }

// SetMemory replaces the saved memories with the lines of the Settings textarea.
func (a *App) SetMemory(text string) { tools.SetMemoryText(text) }

// OpenURL opens a link in the user's browser.
func (a *App) OpenURL(u string) { wr.BrowserOpenURL(a.ctx, u) }

// InstallTool runs the package-manager install for a missing tool (the UI asked first).
func (a *App) InstallTool(name string) string {
	cmd := a.info.InstallCommand(name)
	if cmd == "" {
		return "No package manager can install " + name + " here."
	}
	sh := tools.NewShell(a.info.Shell, a.info.ShellPath, a.info.Home)
	r := sh.Run(cmd, 20*time.Minute)
	a.info = sysinfo.Detect()
	return r
}

func (a *App) RefreshPC() sysinfo.Info { a.info = sysinfo.Detect(); return a.info }

// ---------------- folder ----------------

func (a *App) PickFolder() string {
	d, err := wr.OpenDirectoryDialog(a.ctx, wr.OpenDialogOptions{Title: "Open a project folder", DefaultDirectory: a.prefs.Folder})
	if err != nil || d == "" {
		return a.prefs.Folder
	}
	a.SetFolder(d)
	return d
}

func (a *App) SetFolder(d string) {
	if st, err := os.Stat(d); err != nil || !st.IsDir() {
		return
	}
	a.prefs.Folder = d
	rec := []string{d}
	for _, r := range a.prefs.Recent {
		if r != d && len(rec) < 8 {
			rec = append(rec, r)
		}
	}
	a.prefs.Recent = rec
	a.savePrefs()
	a.newEnv()
	a.pv.load(d)
}

// ---------------- conversations ----------------

type Conv struct {
	ID       string        `json:"id"`
	Project  string        `json:"project"`
	Title    string        `json:"title"`
	Folder   string        `json:"folder"`
	Updated  time.Time     `json:"updated"`
	Messages []llm.Message `json:"messages"`
	Agents   []agents.View `json:"agents,omitempty"`  // this chat's background agents (filled when opened)
	Running  bool          `json:"running,omitempty"` // its turn is still going (opened mid-turn, sessions.go)

	TitleSource string `json:"title_source,omitempty"` // auto (AI, titles.go) or manual (renamed); "" = first prompt
	TitleTurns  int    `json:"title_turns,omitempty"`  // user turns when the auto title was last evaluated
	ContextFrom int    `json:"context_from,omitempty"` // /clear: the model sees messages from here on (one mind)
}

type ConvItem struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Updated string `json:"updated"`
	Source  string `json:"source,omitempty"` // title_source
}

func convDir() string {
	d := filepath.Join(config.Dir(), "desktop", "chats")
	_ = os.MkdirAll(d, 0o700)
	return d
}

// Conversations lists the chats of one project. General ("") is the one main
// conversation (one mind); its old separate chats are in ArchivedConversations.
func (a *App) Conversations(project string) []ConvItem {
	var out []ConvItem
	for _, c := range a.allConvs() {
		if c.Project == project && (project != "" || c.ID == mainConv) {
			out = append(out, ConvItem{ID: c.ID, Title: c.Title, Updated: c.Updated.Format("Jan 2"), Source: c.TitleSource})
		}
	}
	return out
}

func (a *App) allConvs() []Conv {
	ents, _ := os.ReadDir(convDir())
	var convs []Conv
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(convDir(), e.Name()))
		if err != nil {
			continue
		}
		var c Conv
		if json.Unmarshal(b, &c) == nil {
			convs = append(convs, c)
		}
	}
	sort.Slice(convs, func(i, j int) bool { return convs[i].Updated.After(convs[j].Updated) })
	return convs
}

func (a *App) OpenConversation(id string) Conv {
	var c Conv
	b, err := os.ReadFile(filepath.Join(convDir(), id+".json"))
	if err == nil {
		_ = json.Unmarshal(b, &c)
	}
	rememberTitle(c)
	a.mu.Lock()
	s := a.live[c.ID]
	if s != nil && c.ID != "" { // its turn is still running: show it as it is now
		c.Messages, c.Running = append([]llm.Message(nil), s.hist...), s.running()
	} else {
		s = &session{id: c.ID, hist: c.Messages, folder: c.Folder, project: c.Project, started: c.ID != "", ctxFrom: c.ContextFrom}
	}
	a.cur, a.convID = s, c.ID
	a.mu.Unlock()
	a.noteHistory(c.Messages)
	if a.agents != nil && c.ID != "" {
		c.Agents = a.agents.List(c.ID)
	}
	if c.Project != a.prefs.Project {
		a.prefs.Project = c.Project
		a.savePrefs()
	}
	if c.Folder != "" && c.Folder != a.prefs.Folder {
		a.SetFolder(c.Folder)
	}
	return c
}

// NewConversation starts a new chat in a project. In Chat mode there is one continuous
// conversation (one mind), so it reopens it instead; /clear starts a fresh context.
func (a *App) NewConversation() {
	if a.prefs.Project == "" {
		a.OpenConversation(mainConv)
		if a.conn != nil {
			a.conn.ResetLoaded()
		}
		return
	}
	a.mu.Lock()
	a.convID, a.cur = "", &session{}
	a.mu.Unlock()
	a.conn.ResetLoaded()
}

// OpenMain opens the one continuous main conversation.
func (a *App) OpenMain() Conv { return a.OpenConversation(mainConv) }

// ClearContext (/clear) starts a fresh context window in the current conversation:
// the model stops seeing earlier turns, which stay in the transcript, the journal and
// memory. Returns the message index the new window starts at.
func (a *App) ClearContext() int {
	a.mu.Lock()
	s := a.cur
	if s == nil {
		a.mu.Unlock()
		return 0
	}
	s.ctxFrom = len(s.hist)
	n := s.ctxFrom
	a.mu.Unlock()
	if s.id != "" || n > 0 {
		a.saveConv(s)
	}
	if a.conn != nil {
		a.conn.ResetLoaded()
	}
	return n
}

// ArchivedConversations are the separate chats from before one mind (read-only).
func (a *App) ArchivedConversations() []ConvItem {
	out := []ConvItem{}
	for _, c := range a.allConvs() {
		if c.Project == "" && c.ID != mainConv {
			out = append(out, ConvItem{ID: c.ID, Title: c.Title, Updated: c.Updated.Format("2 Jan 2006"), Source: c.TitleSource})
		}
	}
	return out
}

// ReadArchived returns an archived chat for reading; it doesn't become the open chat.
func (a *App) ReadArchived(id string) Conv {
	var c Conv
	if strings.ContainsAny(id, `/\`) {
		return c
	}
	if b, err := os.ReadFile(filepath.Join(convDir(), id+".json")); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	return c
}

func (a *App) DeleteConversation(id string) {
	titles.Lock()
	delete(titles.m, id)
	titles.Unlock()
	_ = os.Remove(filepath.Join(convDir(), id+".json"))
	if d, err := longchat.Default(); err == nil {
		d.Forget(id)
		d.ForgetTopics(id) // v0.19 topic state (topics_app.go)
	}
	a.mu.Lock()
	if s := a.live[id]; s != nil && s.cancel != nil {
		s.cancel() // deleting a chat stops its turn
	}
	delete(a.live, id)
	if a.convID == id {
		a.convID, a.cur = "", &session{}
	}
	a.mu.Unlock()
}

func (a *App) saveConv(s *session) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if isPrivate(s.id) {
		return // private chats are never written (private_app.go)
	}
	a.nameSession(s)
	title, src, turns := promptTitle(s.hist), "", 0
	if m, ok := getTitle(s.id); ok && m.Title != "" {
		title, src, turns = m.Title, m.Source, m.Turns
	}
	folder := s.folder
	if folder == "" {
		folder = a.prefs.Folder
	}
	project := a.prefs.Project
	if s.started {
		project = s.project
	}
	c := Conv{ID: s.id, Project: project, Title: title, Folder: folder, Updated: time.Now(), Messages: s.hist,
		TitleSource: src, TitleTurns: turns, ContextFrom: s.ctxFrom}
	convWriteMu.Lock()
	defer convWriteMu.Unlock()
	b, _ := json.Marshal(c)
	_ = os.WriteFile(filepath.Join(convDir(), s.id+".json"), b, 0o600)
}

// ---------------- chat ----------------

// Send starts a turn. Progress arrives as events: delta, tool, tool_done, status, confirm, done, error.
// files are project-relative paths the user attached with @.
func (a *App) Send(text string, files []string) string { return a.SendWith(text, files, nil) }

// SendWith is Send plus what the composer attached with +, drag-and-drop or paste
// (images, files and folders anywhere on disk; see attach.go).
func (a *App) SendWith(text string, files []string, att []llm.Attach) string {
	var b strings.Builder
	b.WriteString(text)
	for _, f := range files {
		full := filepath.Join(a.prefs.Folder, filepath.FromSlash(f))
		data, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if len(data) > 120_000 {
			data = append(data[:120_000], []byte("\n…(cut)")...)
		}
		fmt.Fprintf(&b, "\n\n<attached path=\"%s\">\n%s\n</attached>", f, data)
	}
	att = inlineAttach(&b, att)
	// No queue: a message sent mid-answer stops that chat's answer (its text so far is
	// kept) and is answered at once. Turns in other chats keep running in the background.
	s := a.screen()
	if s.main().Busy() {
		a.mu.Lock()
		s.detach = true // a Cloud run being waited on moves to the background (runs_app.go)
		a.mu.Unlock()
		a.Stop()
	}
	if s.main().Submit(agents.Input{Kind: "user", Text: b.String(), Attach: att}) {
		return "queued"
	}
	return "ok"
}

// Retry drops the last answer and asks again.
func (a *App) Retry() string {
	s := a.screen()
	if s.main().Busy() {
		return "busy"
	}
	a.mu.Lock()
	has := false
	for _, m := range s.hist {
		has = has || m.Role == "user"
	}
	a.mu.Unlock()
	if !has {
		return "nothing"
	}
	s.main().Submit(agents.Input{Kind: "retry"})
	return "ok"
}

var attachedRe = regexp.MustCompile(`(?s)\n*<attached path="[^"]*"[^>]*>.*?</attached>`)

func stripAttached(s string) string { return attachedRe.ReplaceAllString(s, "") }

// Stop ends the answer in the chat on screen (turns in other chats keep going).
func (a *App) Stop() {
	a.mu.Lock()
	if s := a.cur; s != nil && s.cancel != nil {
		s.cancel()
	}
	pend := a.pending
	a.mu.Unlock()
	for _, ch := range pend {
		select {
		case ch <- 0:
		default:
		}
	}
}

// panicTap lets tests see a recovered run panic with its stack.
var panicTap func(any)

func (a *App) run(ctx context.Context, s *session) {
	started := time.Now()
	// v0.19.1: every gateway request of this turn carries X-Blaze-Turn / X-Blaze-Prefer.
	turn := cloudai.NewTurn()
	ctx = cloudai.WithTurn(ctx, turn)
	tr := &turnTrace{Turn: turn.ID}
	defer a.emitTrace(s, tr, turn, started) // v0.19.7: the reply's "What happened" view
	var toolOrder []string                  // v0.19.1: tools keep their place across the turn's steps
	setTrayStatus("Working…")
	defer setTrayStatus("Idle")
	var changed, urls []string
	prompt, lastUser := "", ""
	a.mu.Lock()
	for _, m := range s.hist {
		if m.Role == "user" && !isAgentResult(m) && !strings.HasPrefix(m.Content, continueTag) {
			prompt = strings.TrimPrefix(firstLine(stripAttached(m.Content)), "[Design edit] ")
			lastUser = m.Content
		}
	}
	a.mu.Unlock()
	// v0.19.13: an action request must be done with tools (intent.go).
	intent := actionIntent(lastUser)
	// v0.19.16: an owner question's reply is held until it is complete, so the owner guard
	// (privacy.OwnerReply: always Blazeinchue, never a person's name) runs before anything shows.
	ownerAsk := stripAttached(lastUser)
	ownerQ := privacy.AsksOwner(ownerAsk)
	forceTool, toolsRan, textRetries, proofRetries := intent, 0, 0, 0
	defer func() {
		if ok, st := a.pv.noteTurn(changed, urls, prompt); ok {
			a.temit(s, "design", map[string]any{"target": st.Target, "versions": len(st.Versions), "three": a.pv.threeD(st)})
		}
	}()
	defer func() {
		if r := recover(); r != nil {
			if panicTap != nil {
				panicTap(r)
			}
			a.temit(s, "error", fmt.Sprint("Blaze hit an internal error: ", r))
		}
		if s.bg != nil {
			return // a background shadow turn (runs_app.go): its final is delivered by localBg
		}
		a.endSession(s)
		a.saveConv(s)
		a.mu.Lock()
		id, hist := s.id, append([]llm.Message(nil), s.hist...)
		a.mu.Unlock()
		if isPrivate(id) {
			a.afterTurn(s, started) // no title, learning or journal for a private chat
			return
		}
		a.maybeRetitle(id, hist)  // background, never blocks the turn
		a.maybeReflect(id, hist)  // end-of-turn learning check (learn_app.go)
		a.mindAfterTurn(id, hist) // journal + save-as-you-go (mind_app.go)
		a.afterTurn(s, started)
	}()
	steps := a.cfg.MaxSteps
	if steps <= 0 {
		steps = 40
	}
	a.mu.Lock()
	design := s.tab == "design"
	a.mu.Unlock()
	a.mu.Lock()
	for i := len(s.hist) - 1; i >= 0; i-- {
		if s.hist[i].Role == "user" {
			design = design || strings.HasPrefix(s.hist[i].Content, "[Design edit]")
			break
		}
	}
	a.mu.Unlock()
	note, retries := "", 0
	ap, steps := a.startAutopilot(steps)
	for step := 0; step < steps; step++ {
		if why := a.capReached(); why != "" { // v0.19.9: the user's own daily cap
			a.temit(s, "error", map[string]any{"message": why, "kind": "cap"})
			return
		}
		if why := ap.stop(a.cfg.DailyCap, llm.TodayCost()); why != "" {
			a.temit(s, "error", map[string]any{"message": why, "kind": "steps"})
			return
		}
		a.mu.Lock()
		full := append([]llm.Message(nil), s.hist...)
		a.mu.Unlock()
		hist, longExtra := a.longContext(ctx, s, full) // brief + rolling summary + recent turns (longchat_app.go)
		hist = dropAcks(hist)                          // ack bubbles are for the user, never the model (runs_app.go)
		conv := "chat:" + s.id
		tools.ActivateFor(conv, lastUserText(hist)) // e.g. csv_report for "report on x.csv"
		q := recentQuery(hist)
		sys := promptOrder(a.systemStable(q), agents.Delegation+agents.Orchestration+longChatLine, a.systemLive(q), tools.ExtraLine(conv), longExtra, note)
		private := isPrivate(s.id)
		sys += weatherLine // v0.19.7: weather only from get_weather
		if private {
			sys += privateLine
		}
		// v0.19.8: the shared Blaze persona is the FIRST system message, byte-identical every turn
		// (same voice in every Blaze app, and provider prompt caches hit); Desktop's own
		// instructions, the user's first name and the local time follow in a second one.
		msgs := append([]llm.Message{{Role: "system", Content: persona.System()}, {Role: "system", Content: sys + a.firstNameLine()}}, hist...)
		// Connector tools join only when connected and enabled, and a group's tools
		// only after the model loads it, so the list stays small.
		// Blaze's own tools: the core set plus what find_tools turned on in this chat.
		all := append(append(append(tools.Select(conv), a.conn.Tools()...), agents.MainTools()...), updateBlazeTool(), weatherTool())
		if private {
			// No specialists (they keep files) and nothing that saves memories or skills.
			all = privateTools(append(append(tools.Select(conv), a.conn.Tools()...), weatherTool()))
		}
		known := append(append(append(tools.Names(), a.conn.Known()...), agents.MainToolNames...), "update_blaze", "get_weather")
		llm.PrimaryArg = primaryArgs(all)
		a.temit(s, "thinking", step)
		var partial strings.Builder // what streamed, kept if the reply breaks off
		var sent []llm.Tool
		sent, toolOrder = keepToolOrder(toolOrder, a.modeTools(all))
		sentNames := make([]string, 0, len(sent))
		for _, t := range sent {
			sentNames = append(sentNames, t.Function.Name)
		}
		// v0.19.13: tool syntax a model writes as text is held back, never shown (CLI TextGuard).
		tg := &llm.TextGuard{Known: sentNames, Out: func(t string) {
			partial.WriteString(t)
			if !ownerQ {
				a.temit(s, "delta", t)
			}
		}}
		// shown: the text a broken-off reply leaves; an owner question's held text shows now, guarded.
		shown := func() string {
			t := partial.String()
			if ownerQ && strings.TrimSpace(t) != "" {
				t = privacy.OwnerNames(t)
				a.temit(s, "delta", t)
			}
			return t
		}
		cctx := ctx
		if step == 0 && forceTool && len(sent) > 0 {
			cctx = llm.WithRequireTool(ctx) // tool_choice "required" for a plain action request
		}
		stepStart, ttft := time.Now(), time.Duration(0)
		r, err := completeTurn(cctx, a.cfg, msgs, sent, llm.Opts{
			OnText: func(t string) {
				if ttft == 0 {
					ttft = time.Since(stepStart)
				}
				tg.Write(t)
			},
			OnStatus: func(t string) { a.temit(s, "status", t) },
			// Keyless may answer plain chat in Work when nothing that can use tools is set up.
			// Never in Design (a design turn without tools can't change anything), and never
			// for an action request, which it could only answer with a made-up "Done" (v0.19.13).
			PlainFallback: !design && !intent,
		})
		tr.step(step+1, r, err, time.Since(stepStart), ttft, len(sent), len(msgs))
		if err != nil && cctx != ctx && ctx.Err() == nil && partial.Len() == 0 {
			// A route that refuses tool_choice "required": ask again without it, once.
			forceTool = false
			step--
			continue
		}
		if err == nil {
			held := tg.Held()
			r2, recovered, junk := recoverTextCalls(r, sentNames)
			switch {
			case recovered:
				r = r2
				a.temit(s, "repair", map[string]any{"text_calls": len(r.ToolCalls)})
			case junk && textRetries < 1 && ctx.Err() == nil:
				textRetries++
				note = "Your last reply wrote a tool call as plain text, so nothing ran. Call the tool through the tools interface with its exact name and a JSON object for the arguments."
				a.temit(s, "status", "Retrying a tool call the model wrote as text")
				continue
			case junk:
				a.keepPartial(s, shown())
				a.temit(s, "error", map[string]any{"message": errTextCalls, "kind": "model", "partial": step > 0})
				return
			default:
				if held != "" {
					partial.WriteString(held)
					if !ownerQ {
						a.temit(s, "delta", held) // it was prose after all
					}
				}
			}
		}
		if err != nil {
			// Keep what the user already saw, so Continue picks up from there.
			kept := a.keepPartial(s, shown())
			if ctx.Err() != nil {
				a.temit(s, "stopped", map[string]any{"resumable": kept || step > 0})
			} else {
				e := turnError(err)
				e["partial"] = kept || step > 0
				a.temit(s, "error", e)
			}
			return
		}
		a.temit(s, "route", map[string]any{"route": r.Route, "note": r.Note, "keyless": r.Keyless})
		a.emitTokens(s, r, msgs)
		// The gatekeeper: repair what can be repaired; never save a call with no name or
		// unreadable arguments (the provider would refuse every later request with a 400).
		n := llm.NormalizeCalls(r.ToolCalls, known)
		if ownerQ {
			if len(n.Calls) == 0 {
				r.Text = privacy.OwnerReply(ownerAsk, r.Text) // the final reply (local or cloud)
			} else {
				r.Text = privacy.OwnerNames(r.Text)
			}
			if strings.TrimSpace(r.Text) != "" {
				a.temit(s, "delta", r.Text)
			}
		}
		if strings.TrimSpace(r.Text) != "" || len(n.Calls) > 0 {
			a.mu.Lock()
			s.hist = append(s.hist, llm.Message{Role: "assistant", Content: r.Text, ToolCalls: n.Calls})
			a.mu.Unlock()
		}
		note = ""
		if len(n.Dropped) > 0 {
			note = "Blaze couldn't run " + strings.Join(n.Dropped, "; ") + ". Call the tool again with its exact name and a complete JSON object for the arguments."
			a.temit(s, "repair", map[string]any{"dropped": n.Dropped, "repairs": n.Repairs})
		}
		if len(n.Calls) == 0 {
			if len(n.Dropped) > 0 && retries < 2 {
				retries++
				continue
			}
			unverified := false
			if strings.TrimSpace(r.Text) == "" && !r.Truncated() {
				// v0.19.13: an empty reply is an error with Retry, never an empty "done" bubble.
				a.temit(s, "error", map[string]any{"message": "The model sent back an empty reply.", "kind": "model", "empty": true, "partial": step > 0})
				return
			}
			if intent && toolsRan == 0 && claimsDone(r.Text) {
				// v0.19.13: it says the work is done, but no tool ran this turn.
				if proofRetries < 1 && ctx.Err() == nil {
					proofRetries++
					note = noProofNote
					a.temit(s, "status", "Checking the work was really done")
					continue
				}
				unverified = true
				a.temit(s, "delta", noProofLine)
				a.mu.Lock()
				if k := len(s.hist) - 1; k >= 0 && s.hist[k].Role == "assistant" {
					s.hist[k].Content += noProofLine
				}
				a.mu.Unlock()
			}
			if a.autoReply(s, &ap, r.Text) {
				continue // Autopilot: the user is away, so the agent decides and keeps going
			}
			done := map[string]any{"route": r.Route, "note": r.Note, "keyless": r.Keyless, "seconds": time.Since(started).Seconds()}
			if r.Truncated() {
				done["cut"] = true // ran out of output tokens or the stream broke: offer Continue
			}
			if unverified {
				done["unverified"] = true
			}
			a.temit(s, "done", done)
			return
		}
		dispatched, onlyAgentTools := false, true
		// Reads from one reply (files, searches, fetches, skills) run at the same time;
		// anything that changes things runs one by one, in the order the model asked.
		calls := n.Calls
		toolsRan += len(calls)
		blocked := make([]string, len(calls))
		t0 := make([]time.Time, len(calls))
		completed := llm.RunBatch(ctx, len(calls),
			func(i int) bool {
				tc := calls[i]
				return tools.ParallelSafe(tc.Function.Name) && a.modeBlock(tc.Function.Name, tc.Function.Arguments) == ""
			},
			func(i int) bool {
				if ctx.Err() != nil {
					return false
				}
				tc := calls[i]
				t0[i] = time.Now()
				a.temit(s, "tool", map[string]string{"id": tc.ID, "name": tc.Function.Name, "args": tc.Function.Arguments})
				setTrayStatus("Working · " + friendlyTool(tc.Function.Name, tc.Function.Arguments))
				blocked[i] = a.modeBlock(tc.Function.Name, tc.Function.Arguments) // Plan mode refuses what can change things
				return true
			},
			func(ctx context.Context, i int) string { // ctx: the turn's, with a per-call deadline for reads
				tc := calls[i]
				switch {
				case blocked[i] != "":
					return blocked[i]
				case private && (privateBlocked[tc.Function.Name] || agents.IsMainTool(tc.Function.Name)):
					return privateRefusal
				case agents.IsMainTool(tc.Function.Name):
					a.mu.Lock()
					conv := s.id
					a.mu.Unlock()
					// Specialists inherit the lead's mode and step budget (never more).
					out := a.agents.HandleLeadTool(tc.Function.Name, tc.Function.Arguments, agents.Lead{Conv: conv, Folder: s.dir(a.prefs.Folder), Mode: a.workMode(), MaxSteps: steps})
					dispatched = dispatched || out.Dispatched != "" // agent tools never run in parallel
					return out.Result
				case tc.Function.Name == "update_blaze":
					return a.selfUpdate()
				case tc.Function.Name == "get_weather":
					return a.runWeather(ctx, tc.Function.Arguments)
				case a.conn.Handles(tc.Function.Name):
					return a.conn.Run(ctx, tc.Function.Name, tc.Function.Arguments)
				default:
					return tools.RunCtx(ctx, a.tenv(s), tc.Function.Name, tc.Function.Arguments)
				}
			},
			func(i int, res string) {
				tc := calls[i]
				if blocked[i] != "" || !agents.IsMainTool(tc.Function.Name) {
					onlyAgentTools = false
				}
				tr.tool(step+1, tc.Function.Name, t0[i], res, blocked[i] != "")
				changed, urls = a.designTouch(tc.Function.Name, tc.Function.Arguments, res, changed, urls)
				a.temit(s, "tool_done", map[string]string{"id": tc.ID, "result": res})
				a.mu.Lock()
				s.hist = append(s.hist, llm.Message{Role: "tool", ToolCallID: tc.ID, Name: tc.Function.Name, Content: asideResult(tc.Function.Name, res)})
				a.mu.Unlock()
			})
		if !completed {
			a.temit(s, "stopped", map[string]any{"resumable": true})
			return
		}
		// Handing work off ends the turn at once: the user gets a short line and the
		// composer is free while the agent works.
		if dispatched && onlyAgentTools {
			line := strings.TrimSpace(r.Text)
			if line == "" {
				line = "On it. I'll report back when it's done."
				a.temit(s, "delta", line)
				a.mu.Lock()
				s.hist = append(s.hist, llm.Message{Role: "assistant", Content: line})
				a.mu.Unlock()
			}
			a.temit(s, "done", map[string]any{"route": r.Route, "note": r.Note, "keyless": r.Keyless, "seconds": time.Since(started).Seconds(), "handoff": true})
			return
		}
	}
	a.temit(s, "error", map[string]any{"message": fmt.Sprintf("Stopped after %d steps without finishing. Continue gives it another %d.", steps, steps), "kind": "steps"})
}

// continueTag starts the message the Continue button sends.
const continueTag = "[Continue]"

// keepPartial saves the text a reply streamed before it broke off (Stop, an error),
// so the model can see where it stopped when the user presses Continue.
func (a *App) keepPartial(s *session, text string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	a.mu.Lock()
	s.hist = append(s.hist, llm.Message{Role: "assistant", Content: text + "\n\n(cut off here)"})
	a.mu.Unlock()
	return true
}

// turnError is what the chat's error card shows: a plain sentence, the raw details,
// and whether switching model would help.
func turnError(err error) map[string]any {
	out := map[string]any{"message": llm.Plain(err), "detail": err.Error(), "kind": "model"}
	var re *llm.RouteError
	if errors.As(err, &re) {
		out["failures"] = re.Failures
		out["resting"] = re.Resting
		if re.NoModels || re.NoTools {
			out["kind"] = "setup"
		}
	}
	return out
}

// SetTab tells the backend which tab the chat is in (work or design).
func (a *App) SetTab(t string) {
	a.mu.Lock()
	a.tab = t
	a.mu.Unlock()
}

func trim(h []llm.Message) []llm.Message {
	const max = 60
	if len(h) <= max {
		return append([]llm.Message(nil), h...)
	}
	cut := len(h) - max
	for cut < len(h) && h[cut].Role != "user" {
		cut++
	}
	return append([]llm.Message(nil), h[cut:]...)
}

func (a *App) foldCards() bool { return a.prefs.FoldCards == nil || *a.prefs.FoldCards }

// answerShape tells the model how to shape a reply. With cards on, long material goes
// into :::card blocks that the app folds away under a short answer.
func answerShape(cards bool) string {
	if !cards {
		return ""
	}
	return `
ANSWER SHAPE
- First the short answer: 1-4 plain sentences the user can read at a glance. Never put it inside a card.
- Long material (tables, step lists, long explanations, code over ~15 lines, reports, comparisons) goes in a card the app folds away:
:::card Short title
...Markdown...
:::
- Several cards are fine. Short replies need no card.
`
}

// replyExtras is the static RICH REPLIES and FOLLOW-UPS block (v0.17.1, the same rules as
// Blaze Android v0.24.0 / v0.24.9): the app draws these as step-flow cards, pictures and diagrams,
// and the follow-up line as pills under the answer (frontend/dist/md.js, bubbles.js).
func replyExtras() string {
	return `
RICH REPLIES (only when they help)
- Steps: a numbered list whose items start with a **bold title** shows as a step-flow card.
- Pictures: ![alt](https://…) on its own line, only with real image URLs; one right before a paragraph sits beside it, 2-3 in a row sit side by side, 4+ become a swipeable deck.
- Diagrams: a ` + "```mermaid" + ` block is drawn inline.
FOLLOW-UPS
- At the very end of a final answer (never when calling tools), add one line exactly like <<follow-ups: first | second | third>> with three short next requests (max 6 words each) written as the user would say them.
`
}

// defaultPersonality is Blaze's voice in chat turns (Settings › Behaviour › Personality).
const defaultPersonality = `PERSONALITY
You are Blaze. Be warm, sharp, and one step ahead: an expert who happens to be easy to talk to.
- Answer first. Lead with the result or the most useful true thing; skip preamble and filler ("Great question", "I'd be happy to").
- Take a side. When there are options, recommend one and say why in a sentence; give one backup only if it earns it.
- Be specific. Real names, numbers, file paths and commands beat adjectives. Never invent facts; say plainly when you're unsure.
- Read the room. Match the user's energy: brief when they're brief, steady and kind when something's gone wrong. Light humour is fine; forced cheer isn't.
- Talk like a person texting a friend who's good at this: contractions, short sentences, plain words.
- Bad news first, with a small cushion, then the way forward. If you made a mistake, fix it first, then own it in a few words.
- Do the work instead of describing it. Finish with the natural next step only when it's theirs to take.
- Be honest about what you are: a capable assistant on their computer, not a person.
`

// personality is the PERSONALITY block for chat turns (scheduled jobs don't use system()).
// Default: Blaze's voice. Plain: none. Custom: the user's text (empty falls back to none).
func (a *App) personality() string {
	switch a.prefs.Personality {
	case "plain":
		return ""
	case "custom":
		if t := strings.TrimSpace(a.prefs.PersonalityCustom); t != "" {
			return "\nPERSONALITY\n" + t + "\n"
		}
		return ""
	}
	return "\n" + defaultPersonality
}

// SetPersonality saves the personality mode (default, plain, custom) and the custom text.
func (a *App) SetPersonality(mode, custom string) {
	if mode != "plain" && mode != "custom" {
		mode = "default"
	}
	a.prefs.Personality, a.prefs.PersonalityCustom = mode, strings.TrimSpace(custom)
	a.savePrefs()
}

// DefaultPersonality is shown in Settings so the user can start a custom one from it.
func (a *App) DefaultPersonality() string { return defaultPersonality }

// recentQuery is what the memory and skill search looks for this turn: the last
// few user messages and the last reply, oldest first (the search favours the newest words).
// lastUserText is the newest user message's text ("" when there is none).
func lastUserText(h []llm.Message) string {
	for i := len(h) - 1; i >= 0; i-- {
		if h[i].Role == "user" {
			return h[i].Content
		}
	}
	return ""
}

func recentQuery(h []llm.Message) string {
	var parts []string
	users, replies := 0, 0
	for i := len(h) - 1; i >= 0 && users < 3; i-- {
		m := h[i]
		c := m.Content
		switch {
		case m.Role == "user":
			users++
		case m.Role == "assistant" && replies == 0 && c != "":
			replies++
			if len(c) > 400 {
				c = c[:400]
			}
		default:
			continue
		}
		if len(c) > 800 {
			c = c[len(c)-800:]
		}
		parts = append([]string{c}, parts...)
	}
	return strings.Join(parts, "\n")
}

// system is the system prompt for a turn whose conversation is about query: it
// carries only the memories and skills that match (v0.5.1), not all of them.
// system is the whole system prompt: the stable part, then the live part (v0.19.1 keeps every
// part's wording; only the order changed, stable first, so prompt caching reuses the longest start).
func (a *App) system(query string) string { return a.systemStable(query) + a.systemLive(query) }

// systemStable is the part that stays the same across the steps of a turn and across turns:
// rules, about Blaze, personality, design and studio rules, project, PC profile, connected
// accounts, the skills picked for this request and the learning rules.
func (a *App) systemStable(query string) string {
	var b strings.Builder
	b.WriteString(`You are Blaze, a capable assistant in a desktop app on the user's own computer. You act, not just advise: use your tools to run commands, read and edit files, write and run code, use git, search the web and install what's missing.

RULES
- Know before you ask: the PC profile below tells you the OS, shell, package managers and installed tools. Never ask what OS or shell they use.
- Write commands for THIS shell (PowerShell syntax on Windows, POSIX elsewhere).
- Look before you change: file_list / file_read / git status first. Make the smallest correct change, then run it or its tests to check.
- Servers, watchers and long builds go in run_shell background=true; then job_log. open_url shows a local server or file to the user.
- Missing program? install_tool (the user approves it).
- Only your core tools are loaded each turn. MORE TOOLS below lists the rest by name: call find_tools(query) to turn them on (or call one by its exact name). Memories and skills are also picked per turn: recall(query) and load_skill(name) reach the rest. Never use sudo or admin rights without asking.
- Work in parallel: when you need several things that don't depend on each other (read three files, search two queries, fetch a few pages, load two skills), ask for all of them in ONE reply. Blaze runs read-only calls at the same time, so a batch costs one wait instead of several. Calls that change things still run one by one, in your order.
- Skills combine. When a task falls between skills (a hand-drawn poster, a game with pixel sprites and music, a 3D walk-through of a floor plan), load every skill it touches and use each one for its own part. No single skill covers it and it is likely to come up again: after it works, save the combination with write_skill as a new skill that names the skills it builds on and holds only the glue (which part comes from which skill, in what order, and what was learned). Tell the user in one line.
- Be honest about failures. Never claim something worked without checking.
- Answers: short, direct, Markdown. Lead with the result.
`)
	b.WriteString(a.aboutYou())
	if a.pair != nil {
		if names := a.pair.deviceNames(); names != "" {
			b.WriteString("\nPAIRED PHONE: " + names + " (Blaze for Android, over Blaze Link). For anything that lives on the phone (texts, WhatsApp, calls, contacts, notifications, alarms, its location), use phone_ask with the request in plain words. Sending a message always needs the user's approval on the phone.\n")
		}
	}
	b.WriteString(a.personality())
	b.WriteString(answerShape(a.foldCards()))
	b.WriteString(replyExtras())
	b.WriteString(a.modeLine())
	b.WriteString(a.lowDataLine())
	if a.cfg.Persona != "" {
		b.WriteString("\nSTYLE\n" + a.cfg.Persona + "\n")
	}
	if m := a.modelName(); m != "" {
		b.WriteString("\nYOU RUN ON " + m + " through Blaze's model router (it may fall back to another model when one is busy). If asked what model you are, say that. Never claim to be GPT-4 or any model you are not.\n")
	}
	b.WriteString(`
DESIGN TAB
- Build anything visual (pages, screens, components, logos) as real files in the project: plain HTML/CSS/SVG, or the project's own framework. The app previews them live in its Design tab and saves a version after each turn.
- For a framework app, start its dev server with run_shell background=true; the app finds the localhost URL. Don't open_url it.
- A message starting with [Continue] means your last reply was cut off (output limit, the user pressed Stop, an error, or the step limit). Carry on exactly where it stopped: no recap and no apology, don't repeat text already written, and if it broke mid-sentence or mid-code-block, resume there (reopen the code fence first). If tools were mid-way, check what already happened before redoing it.
- A message starting with [Design edit] names one element (CSS selector + its HTML), or a 3D part ("Part: <id>" + its blaze-parts JSON). Change ONLY that element or part in the source file (for a part: its entry in the blaze-parts block, plus its build code only if the shape itself changes), keep everything else identical, then say in one line what changed.
- 3D: any request for a 3D model or 3D design (a product, furniture, a printable part, a building, a character, a scene), including one to be made in 3D or CAD software, starts with a Three.js mockup in Design, built with the three-d-design skill (load it first). The mockup is the blueprint: the user picks and edits its parts in Design. If they named a program (Blender, FreeCAD, Fusion 360, OpenSCAD, SketchUp, Unity, …), build it there only after the mockup, from the same parts.
- When asked for directions, make 2-3 clearly different options as separate files (e.g. design/option-a.html, option-b.html, option-c.html), each self-contained. (The Directions button runs them as live branches instead.)
`)
	b.WriteString(openViewLine)
	b.WriteString(a.studioPrompt())
	if p := a.project(); p != nil {
		b.WriteString("\nPROJECT: " + p.Name + "\n")
		if p.Instructions != "" {
			b.WriteString("Project instructions (always follow):\n" + p.Instructions + "\n")
		}
	}
	b.WriteString("\nPC PROFILE\n" + a.info.Summary() + "\n")
	b.WriteString("\nProject folder: " + a.env.Root + "\n")
	if _, err := os.Stat(filepath.Join(a.env.Root, ".git")); err == nil {
		b.WriteString("The project folder is a git repository.\n")
	}
	b.WriteString(a.conn.PromptBlock())
	b.WriteString(tools.SkillsFor(query))
	b.WriteString(a.learnRules())
	return b.String()
}

// systemLive is the part that changes while the user works: the current folder, the memory
// block (it changes when Blaze saves a memory) and the clock. It goes last.
func (a *App) systemLive(query string) string {
	var b strings.Builder
	b.WriteString("\nCurrent folder: " + a.env.Shell.Cwd() + "\n")
	if mb := a.mindBlock(); mb != "" {
		b.WriteString(mb) // pinned core + memory index; the rest is looked up (mind_app.go)
	} else {
		b.WriteString(tools.MemoryFor(query))
	}
	b.WriteString("\nNow (local time): " + time.Now().Format("Monday, 2 January 2006, 3:04 PM") + "\n")
	return b.String()
}

// ---------------- jobs & schedules ----------------

type JobView struct {
	ID    int    `json:"id"`
	Cmd   string `json:"cmd"`
	State string `json:"state"`
	Log   string `json:"log"`
}

func (a *App) Jobs() string { return tools.Run(a.env, "job_log", "{}") }
func (a *App) JobLog(id int) string {
	return tools.Run(a.env, "job_log", fmt.Sprintf(`{"id":%d,"lines":300}`, id))
}
func (a *App) JobStop(id int) string {
	return tools.Run(a.env, "job_stop", fmt.Sprintf(`{"id":%d}`, id))
}
func (a *App) Schedules() []sched.Job         { return sched.Load() }
func (a *App) RemoveSchedule(n string) string { return sched.Remove(n) }

func (a *App) ScheduleLog(name string) string {
	b, err := os.ReadFile(filepath.Join(config.Dir(), "logs", name+".log"))
	if err != nil {
		return "No runs yet."
	}
	s := string(b)
	if len(s) > 20000 {
		s = s[len(s)-20000:]
	}
	return s
}

// ---------------- doctor ----------------

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"` // a link that fixes it (e.g. a macOS privacy pane)
}

func (a *App) Doctor() []Check {
	var out []Check
	add := func(n string, ok bool, d string) { out = append(out, Check{Name: n, OK: ok, Detail: d}) }
	c := &http.Client{Timeout: 8 * time.Second}
	resp, err := c.Get("https://www.google.com/generate_204")
	online := err == nil && resp.StatusCode < 400
	if resp != nil {
		resp.Body.Close()
	}
	add("Internet", online, map[bool]string{true: "Online", false: "Can't reach the internet"}[online])
	targets := llm.Targets(a.cfg, false)
	if len(targets) == 0 {
		add("Models", false, "No model is set up. Add a free Gemini or Groq key in Settings › Models.")
	}
	for _, t := range targets {
		c := a.TestModel(t.Provider, t.Model)
		if t.Fixed {
			c.Detail = "Default · " + c.Detail
		}
		out = append(out, c)
	}
	if len(targets) > 0 && len(llm.Targets(a.cfg, true)) == 0 {
		add("Tools", false, "None of your models can use tools, so Blaze can't act. Add a Gemini, Groq or Cerebras key.")
	}
	sh := tools.NewShell(a.info.Shell, a.info.ShellPath, a.info.Home)
	add("Shell", strings.Contains(sh.Run("echo blaze-ok", 20*time.Second), "blaze-ok"), a.info.Shell+" · "+a.info.ShellPath)
	for _, t := range []string{"git", "python", "node"} {
		v, ok := a.info.Tools[t]
		if t == "python" && !ok {
			v, ok = a.info.Tools["python3"]
		}
		if !ok {
			v = "Not installed"
		}
		add(strings.ToUpper(t[:1])+t[1:], ok, v)
	}
	add("Disk", a.info.DiskFreeGB > 1, fmt.Sprintf("%.0f GB free", a.info.DiskFreeGB))
	add("Package manager", len(a.info.PkgManagers) > 0, strings.Join(a.info.PkgManagers, ", "))
	if runtime.GOOS != "windows" {
		_, err := exec.LookPath("crontab")
		add("Scheduler", err == nil, map[bool]string{true: "cron", false: "crontab missing: scheduled jobs won't run"}[err == nil])
	} else {
		add("Scheduler", true, "Task Scheduler")
	}
	if ok, note := tilingCheck(); note != "" {
		add("Window manager", ok, note)
	}
	d := a.desktopView()
	add("Tray", d.Tray, map[bool]string{true: d.TrayNote, false: "Not running: " + orStr(d.TrayNote, "the tray didn't start") + ". Closing the window quits Blaze."}[d.Tray])
	if d.Hotkey == "off" {
		add("Shortcut", true, "Off (Settings › Behaviour)")
	} else {
		add("Shortcut", d.HotkeyOK, orStr(d.HotkeyNote, d.HotkeyLabel))
	}
	switch {
	case !d.Autostart:
		add("Start at login", true, "Off (Settings › Behaviour)")
	case d.AutostartOK:
		add("Start at login", true, "On · starts hidden in the background")
	default:
		add("Start at login", false, "On, but the login entry is missing. Turn it off and on again.")
	}
	out = append(out, a.notifyCheck())
	out = append(out, a.visionChecks()...)
	return out
}

func orStr(s, alt string) string {
	if strings.TrimSpace(s) == "" {
		return alt
	}
	return s
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// designTouch notes design files written and local servers seen during a turn.
func (a *App) designTouch(name, rawArgs, res string, changed, urls []string) ([]string, []string) {
	var args map[string]any
	_ = json.Unmarshal([]byte(rawArgs), &args)
	if name == "file_write" || name == "file_edit" {
		if p, _ := args["path"].(string); p != "" && designExt.MatchString(p) && !strings.HasPrefix(res, "Error") {
			full := p
			if !filepath.IsAbs(full) {
				full = filepath.Join(a.env.Shell.Cwd(), p)
			}
			if rel, err := filepath.Rel(a.prefs.Folder, full); err == nil && !strings.HasPrefix(rel, "..") {
				rel = filepath.ToSlash(rel)
				if !containsStr(changed, rel) {
					changed = append(changed, rel)
				}
			}
		}
	}
	if name == "run_shell" || name == "job_log" || name == "open_url" {
		src := res
		if name == "run_shell" && !strings.Contains(res, "[server on") {
			src = ""
		}
		if name == "open_url" {
			src, _ = args["target"].(string)
			if src == "" {
				src, _ = args["url"].(string)
			}
		}
		for _, m := range localURL.FindAllString(src, -1) {
			m = strings.Replace(m, "0.0.0.0", "localhost", 1)
			if m != a.pv.base() && !containsStr(urls, m) {
				urls = append(urls, m)
			}
		}
	}
	return changed, urls
}
