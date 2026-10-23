package main

import (
	"context"
	"encoding/json"
	"fmt"
	"blaze-desktop/internal/privacy"
	"os"
	"path/filepath"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/agents"
	"blaze-desktop/internal/config"
	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/tools"
)

// Background agents (v0.4.7): the main chat hands long work to sub-agents and keeps
// chatting. The engine lives in internal/agents; this file wires it to the app: the
// model router, the tools and approval gate, the UI events and the main turn queue.

func agentsDir() string { return filepath.Join(config.Dir(), "desktop", "agents") }

// worktreesDir is where specialists get their own git worktree or folder copy (v0.6.0).
func worktreesDir() string { return filepath.Join(config.Dir(), "desktop", "worktrees") }

// agentGuard stops an agent at the lead's limits: the daily spend cap for every agent,
// and Autopilot's time budget for a specialist (it inherits the lead's budgets).
func (a *App) agentGuard(ag *agents.Agent) string {
	if c := a.cfg.DailyCap; c > 0 && llm.TodayCost() >= c {
		return fmt.Sprintf("stopped: today's spend cap ($%.2f) is reached", c)
	}
	if ag.Kind == agents.Specialist && a.approval() == "autopilot" && !ag.Started.IsZero() {
		a.mu.Lock()
		mins := a.prefs.Composer.APMinutes
		a.mu.Unlock()
		if mins > 0 && time.Since(ag.Started) > time.Duration(mins)*time.Minute {
			return fmt.Sprintf("stopped at Autopilot's time budget (%d min)", mins)
		}
	}
	return ""
}

// agentPlan is true when an agent may only read and scout: a Plan specialist, or any
// agent while the lead (the main chat) is in Plan mode. An agent never has more
// permissions than the lead.
func (a *App) agentPlan(ag *agents.Agent) bool { return ag.Mode == "plan" || a.workMode() == "plan" }

// agentBlock is why an agent's tool call is refused ("" = allowed): Plan limits, and a
// Build specialist with its own copy writing into the original folder.
func (a *App) agentBlock(ag *agents.Agent, name, rawArgs string) string {
	if a.agentPlan(ag) {
		if why := a.planBlock(name, rawArgs); why != "" {
			return "Refused: you are in PLAN mode (read and scout only) and " + why + ". Don't retry it. Finish investigating with read-only tools, then report the plan: the exact changes and commands."
		}
	}
	if ag.Iso != nil && writesFiles[name] {
		var args map[string]any
		_ = json.Unmarshal([]byte(rawArgs), &args)
		p, _ := args["path"].(string)
		if p != "" && filepath.IsAbs(p) && insideDir(ag.Iso.OriginalWorkdir(), p) && !insideDir(ag.Iso.Path, p) {
			return "Refused: " + p + " is in the original folder. Work only in your own copy, " + ag.Iso.Workdir() + " (the same relative path there); the lead applies your changes after review."
		}
	}
	return ""
}

// writesFiles are the file tools a specialist could aim at the original folder.
var writesFiles = map[string]bool{"file_write": true, "file_edit": true, "file_delete": true}

func insideDir(dir, p string) bool {
	rel, err := filepath.Rel(filepath.Clean(dir), filepath.Clean(p))
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// agentCap is how many sub-agents may run at once (Settings › Behaviour).
func (a *App) agentCap() int {
	if a.prefs.MaxAgents > 0 {
		return a.prefs.MaxAgents
	}
	n := agents.DefaultCap
	if a.info.Cores > 0 && a.info.Cores <= 2 {
		n = 1 // a small machine: one agent's builds and installs at a time
	}
	return n
}

func (a *App) startAgents() {
	a.envs = map[string]*tools.Env{}
	a.questions = map[string]chan string{}
	steps := a.cfg.MaxSteps
	if steps <= 0 {
		steps = 40
	}
	a.agents = agents.New(agents.Runtime{
		Complete: func(ctx context.Context, msgs []llm.Message, ts []llm.Tool) (*llm.Result, error) {
			return llm.CompleteWith(ctx, a.cfg, msgs, ts, llm.Opts{})
		},
		Tools: a.agentTools,
		Known: func() []string { return append(append(tools.Names(), a.conn.Known()...), "ask_user") },
		Exec:  a.agentExec,
		System: func(ag *agents.Agent) string {
			return a.system(ag.Task) + tools.ExtraLine(agentConv(ag.Handle)) + agents.AgentSystem(ag)
		},
		Describe: friendlyTool,
		MaxSteps: steps,
		Guard:    a.agentGuard,
		Parallel: func(name string) bool { return tools.ParallelSafe(name) },
	}, agentsDir(), a.agentCap())
	a.agents.SetIsolationDir(worktreesDir())
	a.agents.OnChange = func(v agents.View) {
		if agents.Active(v.Status) {
			v.Steps = nil // steps stream as agent_step; the whole log comes with the final view
		}
		a.emit("agent", v)
	}
	a.agents.OnStep = func(h string, s agents.Step) {
		if len(s.Result) > 2000 {
			s.Result = s.Result[:2000] + "\n…"
		}
		a.emit("agent_step", map[string]any{"handle": h, "step": s})
	}
	a.agents.OnRemove = func(h string) { a.emit("agent_removed", h) }
	a.agents.OnResult = func(v agents.View) {
		a.mu.Lock()
		delete(a.envs, v.Handle)
		a.mu.Unlock()
		a.deliverResult(agents.Input{Kind: "agent", Text: agents.ResultMessage(v), Handle: v.Handle, Conv: v.Conv})
	}
	a.agents.Load()
	a.agents.Prune(7 * 24 * time.Hour)
	a.resumeAfterUpdate()
}

func (a *App) emit(ev string, d any) {
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, ev, privacy.Event(ev, d))
	}
}

// agentConv is the find_tools key of a background agent (each agent turns on its own tools).
func agentConv(handle string) string { return "agent:" + handle }

// agentTools is a sub-agent's tool list, built the same way as the main agent's:
// the core set plus what it turned on with find_tools, plus connectors and
// ask_user, without the agent-control tools. A dispatch's scope narrows it to
// exactly those tools (searched in every tool, not just the core).
func (a *App) agentTools(ag *agents.Agent) []llm.Tool {
	scope := ag.Scope
	all := append(tools.Select(agentConv(ag.Handle)), a.conn.Tools()...)
	if len(scope) > 0 {
		every := append(tools.All(), a.conn.Tools()...)
		want := map[string]bool{}
		for _, s := range scope {
			want[strings.TrimSpace(s)] = true
		}
		var out []llm.Tool
		for _, t := range every {
			if want[t.Function.Name] {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			all = out
		}
	}
	if a.agentPlan(ag) {
		var ro []llm.Tool
		for _, t := range all {
			if a.planAllows(t.Function.Name) {
				ro = append(ro, t)
			}
		}
		all = ro
	}
	return append(all, agents.AskTool())
}

// agentEnv is a sub-agent's own tool environment: its own shell (and working
// folder), the shared jobs, and an approval gate that shows on its card.
func (a *App) agentEnv(ag *agents.Agent) *tools.Env {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.envs[ag.Handle]; e != nil {
		e.Yolo = a.env.Yolo
		return e
	}
	root := ag.Folder
	if st, err := os.Stat(root); root == "" || err != nil || !st.IsDir() {
		root = a.prefs.Folder
	}
	h := ag.Handle
	e := &tools.Env{Info: &a.info, Root: root, Yolo: a.env.Yolo, Shell: tools.NewShell(a.info.Shell, a.info.ShellPath, root), Jobs: a.env.Jobs}
	e.Conv = func() string { return agentConv(h) }
	e.Confirm = func(title, detail string) bool { return a.agentGate(h, title, detail) }
	e.Status = func(s string) { a.agents.Status(h, s) }
	e.Phone = a.env.Phone
	e.Approve = func(title, detail string) bool { return a.confirm(title, detail) }
	a.envs[h] = e
	return e
}

func (a *App) agentExec(ctx context.Context, ag *agents.Agent, name, args string) string {
	if name == "ask_user" {
		return a.agentQuestion(ctx, ag.Handle, args)
	}
	if agents.IsMainTool(name) {
		return "Error: background agents and specialists can't start or steer other agents. Do the work yourself."
	}
	if why := a.agentBlock(ag, name, args); why != "" {
		return why
	}
	if a.conn.Handles(name) {
		return a.conn.Run(ctx, name, args)
	}
	return tools.RunCtx(ctx, a.agentEnv(ag), name, args)
}

// agentGate is a sub-agent's approval: Autopilot approves (catastrophic commands stay
// blocked) like the main chat's gate in modes.go; Auto asks on the agent's card.
func (a *App) agentGate(handle, title, detail string) bool {
	if a.approval() == "autopilot" {
		if why := Catastrophic(title, detail); why != "" {
			a.agents.Status(handle, "Blocked: "+why)
			return false
		}
		return true
	}
	return a.askAgent(handle, title, detail)
}

// askAgent is the approval card for a sub-agent: it shows on the agent's card and
// blocks only that agent, never the main chat.
func (a *App) askAgent(handle, title, detail string) bool {
	id := newID()
	ch := make(chan int, 1)
	a.mu.Lock()
	a.pending[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.pending, id); a.mu.Unlock() }()
	a.agents.SetWaiting(handle, true, "Waiting for your approval")
	defer a.agents.SetWaiting(handle, false, "Working on it")
	a.emit("confirm", map[string]any{"id": id, "title": title, "detail": detail, "agent": handle})
	select {
	case v := <-ch:
		return v > 0
	case <-a.ctx.Done():
		return false
	}
}

// agentAway is the Autopilot hook: in Autopilot (modes.go) the user is away, so a
// sub-agent's question gets the same away-reply as the main agent's.
var agentAway = func(a *App) (string, bool) {
	if a.approval() == "autopilot" {
		return AutoReply, true
	}
	return "", false
}

// agentQuestion is ask_user for a sub-agent: a question on its card.
func (a *App) agentQuestion(ctx context.Context, handle, raw string) string {
	var q struct {
		Question string   `json:"question"`
		Choices  []string `json:"choices"`
	}
	_ = json.Unmarshal([]byte(raw), &q)
	if strings.TrimSpace(q.Question) == "" {
		return "Error in ask_user: write the question."
	}
	if reply, ok := agentAway(a); ok {
		return reply
	}
	id := newID()
	ch := make(chan string, 1)
	a.mu.Lock()
	a.questions[id] = ch
	a.mu.Unlock()
	defer func() { a.mu.Lock(); delete(a.questions, id); a.mu.Unlock() }()
	a.agents.SetWaiting(handle, true, "Waiting for your answer")
	defer a.agents.SetWaiting(handle, false, "Working on it")
	a.emit("agent_question", map[string]any{"id": id, "agent": handle, "question": q.Question, "choices": q.Choices})
	select {
	case ans := <-ch:
		return "The user answered: " + ans
	case <-ctx.Done():
		return "The agent was stopped before the user answered."
	case <-a.ctx.Done():
		return "Blaze closed before the user answered."
	}
}

// ---------------- bound methods for the UI ----------------

// Agents lists background agents for a chat ("" = all chats).
func (a *App) Agents(conv string) []agents.View {
	a.waitReady()
	return a.agents.List(conv)
}

// AgentStop is the Stop button on an agent card.
func (a *App) AgentStop(handle string) string { return errText(a.agents.Cancel(handle)) }

// AgentResume restarts an interrupted (or stopped) agent.
func (a *App) AgentResume(handle string) string { return errText(a.agents.Resume(handle)) }

// AgentDiscard forgets an agent that isn't running.
func (a *App) AgentDiscard(handle string) string { return errText(a.agents.Discard(handle)) }

// SpecialistReview is the Apply / Discard button on a finished specialist's row: the
// same as the lead's apply_specialist, for when the user decides themselves.
func (a *App) SpecialistReview(handle, action string) string {
	if action == "apply" && a.workMode() == "plan" {
		return "Blaze is in Plan mode: switch to Build to apply changes."
	}
	msg, err := a.agents.ApplySpecialist(handle, action)
	if err != nil {
		return err.Error()
	}
	a.emit("toast", map[string]string{"text": msg, "kind": "ok"})
	return ""
}

// AnswerAgent answers a sub-agent's question.
func (a *App) AnswerAgent(id, text string) {
	a.mu.Lock()
	ch := a.questions[id]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- strings.TrimSpace(text):
		default:
		}
	}
}

// SetMaxAgents sets how many background agents run at once (1-6).
func (a *App) SetMaxAgents(n int) int {
	if n < 1 {
		n = 1
	}
	if n > agents.MaxCap {
		n = agents.MaxCap
	}
	a.prefs.MaxAgents = n
	a.savePrefs()
	a.agents.SetCap(n)
	return n
}

// ---------------- the main turn queue ----------------

// turn runs one main-agent turn in chat s for an input from s's Mainline: a user
// message, a background agent's result, or a retry. The chat may be on screen or not:
// a turn keeps going when the user switches to another chat (sessions.go).
func (a *App) turn(s *session, in agents.Input) {
	a.mu.Lock()
	switch in.Kind {
	case "agent":
		s.hist = append(s.hist, llm.Message{Role: "user", Content: in.Text})
	case "retry":
		last := -1
		for i, m := range s.hist {
			if m.Role == "user" {
				last = i
			}
		}
		if last < 0 {
			a.mu.Unlock()
			return
		}
		s.hist = s.hist[:last+1]
	default:
		s.hist = append(s.hist, llm.Message{Role: "user", Content: in.Text, Attach: in.Attach})
	}
	ctx, cancel := context.WithCancel(a.ctx)
	a.startSession(s, cancel)
	a.tagMemoryTurn(s)
	conv := s.id
	a.mu.Unlock()
	a.temit(s, "turn", map[string]any{"kind": in.Kind, "handle": in.Handle, "conv": conv})
	if a.bgTurn(ctx, s, in) { // v0.19.14: ack now, work in background (runs_app.go)
		return
	}
	a.run(ctx, s)
}

// appendToConv adds a message to a saved chat that isn't open.
func appendToConv(id string, m llm.Message) {
	convWriteMu.Lock()
	defer convWriteMu.Unlock()
	p := filepath.Join(convDir(), id+".json")
	b, err := os.ReadFile(p)
	if err != nil {
		return
	}
	var c Conv
	if json.Unmarshal(b, &c) != nil {
		return
	}
	c.Messages = append(c.Messages, m)
	c.Updated = time.Now()
	out, _ := json.Marshal(c)
	_ = os.WriteFile(p, out, 0o600)
}

func isAgentResult(m llm.Message) bool {
	return m.Role == "user" && strings.HasPrefix(m.Content, agents.ResultPrefix)
}
