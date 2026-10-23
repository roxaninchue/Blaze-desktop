package main

// Composer modes (v0.5): Work mode Plan / Build and Approval mode Auto / Autopilot.
//
//   Plan       read + scout only: the tool list is filtered and any other call is refused.
//   Build      every tool (the old behaviour).
//   Auto       dangerous actions still show a confirm card (danger classification in tools).
//   Autopilot  no confirm cards; a hard blocklist of catastrophic actions stays blocked;
//              questions to the user get an automatic "decide yourself" reply; the run is
//              bounded by a step and time budget and the daily spend cap.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/llm"
)

// ComposerPrefs live in desktop.json under "composer".
type ComposerPrefs struct {
	WorkMode  string `json:"work_mode"`         // build (default) | plan
	Approval  string `json:"approval"`          // auto (default) | autopilot
	Migrated  bool   `json:"migrated"`          // the old auto_approve switch was read once
	KeyWork   string `json:"key_work"`          // default ctrl+tab (cycles Plan/Build while typing)
	KeyAppr   string `json:"key_approval"`      // default shift+tab (toggles Auto/Autopilot while typing)
	KeyMic    string `json:"key_mic"`           // default ctrl+shift+m (voice input)
	APSteps   int    `json:"autopilot_steps"`   // default 150
	APMinutes int    `json:"autopilot_minutes"` // default 45
	STTEngine string `json:"stt_engine"`        // local (default) | groq | openai
	STTModel  string `json:"stt_model"`         // base (default) | small
	STTLang   string `json:"stt_lang"`          // auto (default) or a language code
}

// AutoReply is what Autopilot answers when the agent asks the user something.
const AutoReply = "The user is away and can't answer. Make the decision yourself pragmatically, based on your understanding and best practice, note the assumption, and continue. If the task is already done and verified, don't start new work: finish with your summary and the assumptions you made."

// emit sends an event to the UI (a variable so tests can run without a window).
var emit = func(ctx context.Context, ev string, data ...any) {
	if ctx != nil && ctx.Value("frontend") != nil {
		wr.EventsEmit(ctx, ev, data...)
	}
}

const autoPrefix = "[Autopilot] "

func (c *ComposerPrefs) defaults() {
	if c.WorkMode != "plan" {
		c.WorkMode = "build"
	}
	if c.Approval != "autopilot" {
		c.Approval = "auto"
	}
	if c.KeyWork == "" {
		c.KeyWork = "ctrl+tab"
	}
	if c.KeyAppr == "" {
		c.KeyAppr = "shift+tab"
	}
	if c.KeyMic == "" {
		c.KeyMic = "ctrl+shift+m"
	}
	if c.APSteps <= 0 {
		c.APSteps = 150
	}
	if c.APMinutes <= 0 {
		c.APMinutes = 45
	}
	if c.STTEngine == "" {
		c.STTEngine = "local"
	}
	if c.STTModel == "" {
		c.STTModel = "base"
	}
	if c.STTLang == "" {
		c.STTLang = "auto"
	}
}

// migrateComposer runs once at startup. The old AUTO-APPROVE switch (config auto_approve,
// shared with the CLI) maps to Auto either way: true keeps its old meaning only in the
// CLI; the desktop starts safe and Autopilot is always an explicit choice.
func (a *App) migrateComposer() {
	c := &a.prefs.Composer
	if !c.Migrated {
		c.Approval = "auto"
		c.WorkMode = "build"
		c.Migrated = true
		c.defaults()
		a.savePrefs()
		return
	}
	c.defaults()
}

func (a *App) workMode() string { a.mu.Lock(); defer a.mu.Unlock(); return a.prefs.Composer.WorkMode }
func (a *App) approval() string { a.mu.Lock(); defer a.mu.Unlock(); return a.prefs.Composer.Approval }

// ModeView is what the composer shows.
type ModeView struct {
	WorkMode string `json:"work_mode"`
	Approval string `json:"approval"`
}

func (a *App) modeView() ModeView { return ModeView{a.workMode(), a.approval()} }

// Modes is what the composer shows on start.
func (a *App) Modes() ModeView { a.waitReady(); return a.modeView() }

func (a *App) emitModes() {
	if a.ctx != nil {
		emit(a.ctx, "modes", a.modeView())
	}
}

// SetWorkMode switches Plan / Build (takes effect from the next model step).
func (a *App) SetWorkMode(m string) ModeView {
	if m != "plan" {
		m = "build"
	}
	a.mu.Lock()
	a.prefs.Composer.WorkMode = m
	a.mu.Unlock()
	a.savePrefs()
	a.emitModes()
	return ModeView{a.workMode(), a.approval()}
}

// SetApprovalMode switches Auto / Autopilot (takes effect immediately, also mid-run).
func (a *App) SetApprovalMode(m string) ModeView {
	if m != "autopilot" {
		m = "auto"
	}
	a.mu.Lock()
	a.prefs.Composer.Approval = m
	a.mu.Unlock()
	a.savePrefs()
	a.emitModes()
	return ModeView{a.workMode(), a.approval()}
}

// SetComposerPrefs saves the remappable keys, the Autopilot budget and voice settings.
func (a *App) SetComposerPrefs(c ComposerPrefs) ComposerPrefs {
	a.mu.Lock()
	old := a.prefs.Composer
	c.Migrated = true
	if c.WorkMode == "" {
		c.WorkMode = old.WorkMode
	}
	if c.Approval == "" {
		c.Approval = old.Approval
	}
	c.KeyWork, c.KeyAppr, c.KeyMic = normKey(c.KeyWork), normKey(c.KeyAppr), normKey(c.KeyMic)
	c.defaults()
	a.prefs.Composer = c
	a.mu.Unlock()
	a.savePrefs()
	a.emitModes()
	return c
}

func normKey(k string) string {
	k = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(k), " ", ""))
	if k == "alt+tab" { // the OS window switcher on Windows and most Linux desktops: never capturable
		return "ctrl+tab"
	}
	return k
}

// ---------------- Plan mode ----------------

// planTools are the read + scout tools. see_screen / screen_read come from the screen-vision
// work and are allowed when they exist.
var planTools = map[string]bool{
	"file_read": true, "file_list": true, "file_search": true,
	"web_search": true, "fetch_page": true, "system_info": true,
	"git":     true, // read-only subcommands only (planGit)
	"job_log": true, "use_skill": true, "load_skill": true,
	"recall": true, "find_tools": true, // searching memory / turning tools on changes nothing (Plan still filters what find_tools turns on)
	"see_screen": true, "screen_read": true, "read_screen": true, "screen_info": true,
	"notify":      true, // a notification changes nothing on disk
	"list_agents": true, // background agents: Plan may look, not start or steer them
	// Specialists: a Plan lead may brief Plan specialists (they only scout) and steer
	// them; apply_specialist changes files, so it waits for Build.
	"dispatch_specialist": true, "check_specialist": true, "message_specialist": true, "cancel_specialist": true,
}

var gitReadOnly = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "blame": true, "ls-files": true,
	"rev-parse": true, "describe": true, "shortlog": true, "grep": true, "whatchanged": true,
	"ls-tree": true, "cat-file": true, "rev-list": true, "show-ref": true, "for-each-ref": true,
	"branch": true, "tag": true, "remote": true, "reflog": true, "stash": true, "config": true,
}

// planGit is "" when a git argument string only reads.
func planGit(raw string) string {
	f := strings.Fields(raw)
	for len(f) > 0 && (f[0] == "git" || strings.HasPrefix(f[0], "-C") || strings.HasPrefix(f[0], "--no-pager")) {
		if f[0] == "-C" && len(f) > 1 {
			f = f[1:]
		}
		f = f[1:]
	}
	if len(f) == 0 {
		return ""
	}
	sub, rest := f[0], f[1:]
	if !gitReadOnly[sub] {
		return "git " + sub + " changes the repository"
	}
	has := func(xs ...string) bool {
		for _, a := range rest {
			for _, x := range xs {
				if a == x || strings.HasPrefix(a, x+"=") {
					return true
				}
			}
		}
		return false
	}
	switch sub {
	case "branch":
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") || has("-d", "-D", "-m", "-M", "-c", "-C", "--delete", "--move", "--copy", "--set-upstream-to", "-u", "--unset-upstream", "--edit-description", "-f", "--force") {
				return "git branch with those arguments changes branches"
			}
		}
	case "tag":
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") || has("-d", "--delete", "-a", "-s", "-f", "-m") {
				if !has("-l", "--list") {
					return "git tag with those arguments changes tags"
				}
			}
		}
	case "remote":
		if len(rest) > 0 && rest[0] != "-v" && rest[0] != "show" && rest[0] != "get-url" {
			return "git remote " + rest[0] + " changes remotes"
		}
	case "reflog":
		if len(rest) > 0 && (rest[0] == "expire" || rest[0] == "delete") {
			return "git reflog " + rest[0] + " changes history"
		}
	case "stash":
		if len(rest) == 0 || (rest[0] != "list" && rest[0] != "show") {
			return "git stash changes the working tree"
		}
	case "config":
		if !has("--get", "--get-all", "--list", "-l", "--get-regexp") {
			return "git config without --get/--list changes settings"
		}
	}
	if has("--output", "-o") && (sub == "diff" || sub == "log" || sub == "show") {
		return "writing git output to a file"
	}
	return ""
}

// modeTools filters the tool list for the current work mode.
func (a *App) modeTools(all []llm.Tool) []llm.Tool {
	all = a.lowDataTools(all)
	if a.workMode() != "plan" {
		return all
	}
	var out []llm.Tool
	for _, t := range all {
		if a.planAllows(t.Function.Name) {
			out = append(out, t)
		}
	}
	return out
}

// planAllows: Blaze's read/scout tools, plus connector tools that only read.
func (a *App) planAllows(name string) bool {
	return planTools[name] || (a.conn != nil && a.conn.ReadOnly(name))
}

// modeBlock is the reason a call is refused in the current mode ("" = allowed).
func (a *App) modeBlock(name, rawArgs string) string {
	if a.workMode() != "plan" {
		return ""
	}
	why := a.planBlock(name, rawArgs)
	if why == "" {
		return ""
	}
	return "Refused: Blaze is in PLAN mode (read and scout only) and " + why + ". Don't retry it. Finish investigating with read-only tools, then present the plan: the exact changes and commands you would run. The user switches to Build (Ctrl+Tab) to let you carry it out."
}

// planBlock is why Plan refuses a call ("" = allowed), for the lead and its agents.
func (a *App) planBlock(name, rawArgs string) string {
	why := ""
	if !a.planAllows(name) {
		why = name + " can change things"
	} else if name == "git" {
		var args map[string]any
		_ = json.Unmarshal([]byte(rawArgs), &args)
		s, _ := args["args"].(string)
		if s == "" {
			s, _ = args["command"].(string)
		}
		why = planGit(s)
	}
	return why
}

// ---------------- Autopilot ----------------

// catastrophic matches actions Autopilot never takes, even though it confirms nothing else.
var catastrophic = regexp.MustCompile(`(?i)(\bmkfs(\.\w+)?\b|\bdd\s+[^|;&]*of=/dev/|\bformat(\.com)?\s+[a-z]:|\bdiskpart\b|\bClear-Disk\b|\bInitialize-Disk\b|\bFormat-Volume\b|\bwipefs\b|\bsgdisk\s+(-Z|--zap)|\bfdisk\b|\bparted\b|>\s*/dev/(sd|nvme|disk|hd)|:\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}|\bchmod\s+-R\s+0?777\s+/(\s|$)|\bchown\s+-R\s+\S+\s+/(\s|$)|\brm\s+(-[a-z]*\s+)*-[a-z]*r[a-z]*\s+(-[a-z]+\s+)*(/|/\*|~|~/|~/\*|\$HOME|\$HOME/\*|/home|/Users|/etc|/usr|/boot|/var|C:\\?|C:\\Windows|C:\\Users)(\s|$|")|Remove-Item\b.*-Recurse.*\s([A-Z]:\\?|~|\$HOME|\$env:USERPROFILE|C:\\Users|C:\\Windows)(\s|$|")|\brd\s+/s\s+.*[A-Z]:\\(\s|$)|\bdel\s+/[sfq].*[A-Z]:\\\*|\bbcdedit\b|\bvssadmin\s+delete\b|\breg\s+delete\s+HK(LM|EY_LOCAL_MACHINE)\b)`)

// catastrophicPath is true for deleting a disk root, the home folder or one of its parents.
func catastrophicPath(p string) bool {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsAny(p, "\n") {
		return false
	}
	home, _ := os.UserHomeDir()
	if strings.HasPrefix(p, "~") {
		p = filepath.Join(home, strings.TrimLeft(p[1:], `/\`))
	}
	if !filepath.IsAbs(p) {
		return false
	}
	c := filepath.Clean(p)
	if c == filepath.VolumeName(c)+string(filepath.Separator) || c == "/" {
		return true
	}
	if home != "" {
		h := filepath.Clean(home)
		if eqPath(c, h) {
			return true
		}
		if rel, err := filepath.Rel(c, h); err == nil && !strings.HasPrefix(rel, "..") {
			return true // a parent of the home folder
		}
		for _, d := range []string{".blaze", ".ssh", "Desktop", "Documents"} {
			if eqPath(c, filepath.Join(h, d)) {
				return true
			}
		}
	}
	for _, d := range []string{"/etc", "/usr", "/bin", "/boot", "/var", "/System", "/Applications", `C:\Windows`, `C:\Program Files`} {
		if eqPath(c, filepath.Clean(d)) {
			return true
		}
	}
	return false
}

func eqPath(a, b string) bool {
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// Catastrophic is the hard-blocklist reason for an action ("" = not on it).
func Catastrophic(title, detail string) string {
	if catastrophic.MatchString(detail) {
		return "it could wipe a disk, the system or your home folder"
	}
	if strings.HasPrefix(title, "Delete") && catastrophicPath(detail) {
		return "it would delete a disk root, your home folder or a system folder"
	}
	return ""
}

// gate is the tools' confirm hook. Auto shows the confirm card; Autopilot approves
// everything except the hard blocklist, which is refused without asking.
func (a *App) gate(title, detail string) bool {
	if why := Catastrophic(title, detail); why != "" && a.approval() == "autopilot" {
		emit(a.ctx, "ap_blocked", map[string]string{"title": title, "detail": detail, "why": why})
		return false
	}
	if a.approval() == "autopilot" {
		emit(a.ctx, "ap_approved", map[string]string{"title": title, "detail": detail})
		return true
	}
	return a.confirm(title, detail)
}

// autopilotRun holds one run's budget.
type autopilotRun struct {
	on       bool
	started  time.Time
	deadline time.Time
	replies  int
}

func (a *App) startAutopilot(steps int) (autopilotRun, int) {
	r := autopilotRun{on: a.approval() == "autopilot", started: time.Now()}
	if !r.on {
		return r, steps
	}
	a.mu.Lock()
	c := a.prefs.Composer
	a.mu.Unlock()
	r.deadline = r.started.Add(time.Duration(c.APMinutes) * time.Minute)
	emit(a.ctx, "autopilot", map[string]any{"on": true, "steps": c.APSteps, "minutes": c.APMinutes})
	return r, c.APSteps
}

// stop is why Autopilot must end now ("" = keep going).
func (r *autopilotRun) stop(capUSD, spent float64) string {
	if !r.on {
		return ""
	}
	if !r.deadline.IsZero() && time.Now().After(r.deadline) {
		return fmt.Sprintf("Autopilot stopped at its time budget (%s). Say \"continue\" to keep going.", r.deadline.Sub(r.started).Round(time.Minute))
	}
	if capUSD > 0 && spent >= capUSD {
		return fmt.Sprintf("Autopilot stopped: today's spend cap ($%.2f) is reached. Raise it in Settings › Models, or continue tomorrow.", capUSD)
	}
	return ""
}

var questionRe = regexp.MustCompile(`(?i)(\?\s*(\*\*)?\s*$|\b(would you like|do you want|should i|shall i|let me know|which (one|option)|can you (confirm|tell|provide)|could you (confirm|tell|provide)|please (confirm|choose|provide|tell))\b)`)

// asksUser is true when a reply that ran no tools ends by asking the user something.
func asksUser(text string) bool {
	t := strings.TrimSpace(text)
	if t == "" {
		return false
	}
	lines := strings.Split(t, "\n")
	tail := ""
	for i := len(lines) - 1; i >= 0 && len(tail) < 400; i-- {
		tail = lines[i] + "\n" + tail
		if strings.TrimSpace(lines[i]) != "" && len(tail) > 80 {
			break
		}
	}
	return questionRe.MatchString(strings.TrimSpace(tail))
}

// autoReply answers the agent's question for the away user (at most 8 times per run).
func (a *App) autoReply(s *session, r *autopilotRun, text string) bool {
	if !r.on || r.replies >= 8 || a.approval() != "autopilot" || !asksUser(text) {
		return false
	}
	r.replies++
	a.mu.Lock()
	s.hist = append(s.hist, llm.Message{Role: "user", Content: autoPrefix + AutoReply})
	a.mu.Unlock()
	a.temit(s, "autoreply", AutoReply)
	return true
}

// modeLine tells the model which modes are on.
func (a *App) modeLine() string {
	var b strings.Builder
	b.WriteString("\nMODE\n")
	if a.workMode() == "plan" {
		b.WriteString("- PLAN (read + scout only). You can read files, list and search folders, search and read the web, run read-only git and check the system. You cannot write, edit or delete files, run shell commands, run code or install anything: such calls are refused. Investigate, then give a clear plan: what you found, the exact changes (files and edits) and the commands you would run, and the risks. The user switches to Build to let you do it.\n")
	} else {
		b.WriteString("- BUILD: full access (read, write code, shell, installs).\n")
	}
	if a.approval() == "autopilot" {
		b.WriteString("- AUTOPILOT: the user is away. Don't stop to ask questions or wait for approval; make reasonable decisions using best practice, note each assumption, and keep going until the task is fully done and verified. Actions that could wipe a disk, the system or the home folder are blocked: never attempt them. Finish with a short summary and the assumptions you made.\n")
	} else {
		b.WriteString("- AUTO approval: risky actions show the user a confirm card first.\n")
	}
	return b.String()
}
