package main

// v0.19.14 just-in-time permissions. Setup asks for nothing up front: Blaze asks for a folder,
// a risky command or notifications only when a task first needs one, with a one-line reason,
// and a "no" gets an honest fallback instead of a silent failure. The asks themselves are
// drawn by frontend/dist/perms.js; this file decides when to ask and remembers the answers.

import (
	"regexp"
	"strings"
)

// PermPrefs live in desktop.json under "perms". "" means not asked yet.
type PermPrefs struct {
	Notify string `json:"notify,omitempty"` // "", "allow" or "deny": desktop notifications
	Folder string `json:"folder,omitempty"` // "" or "deny": the user said "not now" to picking a folder
}

// PermPrompt is one ask: shown only when Ask is true.
type PermPrompt struct {
	Ask      bool   `json:"ask"`
	Kind     string `json:"kind"`     // folder | notify
	Reason   string `json:"reason"`   // one line: why Blaze needs it for this task
	Allow    string `json:"allow"`    // the yes button
	Deny     string `json:"deny"`     // the no button
	Fallback string `json:"fallback"` // what happens on "no", said plainly
}

// PermResult is what an answer did.
type PermResult struct {
	Allowed bool   `json:"allowed"`
	Folder  string `json:"folder,omitempty"` // the folder now in use (folder asks)
	Line    string `json:"line"`             // one line to show the user
}

// PermView is the current state, for Settings and the Home folder chip.
type PermView struct {
	Folder       string `json:"folder"`
	FolderChosen bool   `json:"folder_chosen"` // a real project folder, not the home-folder default
	Notify       string `json:"notify"`
	Approval     string `json:"approval"` // auto (asks before risky steps) | autopilot
}

const (
	folderReason   = "To work on your files, Blaze needs to know which folder. It reads and edits only there."
	folderFallback = "No folder for now. Blaze starts in your home folder and asks before changing anything outside it. Pick one any time from the folder chip."
	notifyReason   = "This may take a while. Blaze can tell you when it's done, even if the window is closed."
	notifyFallback = "No desktop notifications. Finished tasks wait for you in Chat."
)

// folderWords: a message that is about files in a folder or a codebase.
var folderWords = regexp.MustCompile(`(?i)\b(this|that|the|my|current|our)\s+(folder|directory|dir|project|repo|repository|codebase|code\s?base|workspace)\b|\b(in|from)\s+here\b|\bthese\s+files\b|\bthe\s+tests?\b|\brun\s+(the\s+)?tests?\b|\bgit\s+(status|log|diff|commit|push|pull)\b|\b(my|the)\s+code\b`)

// folderNeed reports whether a message needs a project folder.
func folderNeed(text string) bool { return folderWords.MatchString(text) }

// folderChosen: a real folder, not the home-folder default startup falls back to.
func (a *App) folderChosen() bool {
	f := strings.TrimSpace(a.prefs.Folder)
	return f != "" && !eqPath(f, a.info.Home)
}

// PermState is the current state of every ask.
func (a *App) PermState() PermView {
	a.waitReady()
	a.mu.Lock()
	defer a.mu.Unlock()
	return PermView{Folder: a.prefs.Folder, FolderChosen: a.folderChosen(), Notify: a.prefs.Perms.Notify, Approval: a.prefs.Composer.Approval}
}

// PermAsk says whether to ask for kind before the task in text goes ahead.
func (a *App) PermAsk(kind, text string) PermPrompt {
	a.mu.Lock()
	defer a.mu.Unlock()
	switch kind {
	case "folder":
		p := PermPrompt{Kind: kind, Reason: folderReason, Allow: "Choose a folder", Deny: "Not now", Fallback: folderFallback}
		p.Ask = !a.folderChosen() && a.prefs.Perms.Folder != "deny" && a.prefs.Project == "" && folderNeed(text)
		return p
	case "notify":
		p := PermPrompt{Kind: kind, Reason: notifyReason, Allow: "Notify me", Deny: "Not now", Fallback: notifyFallback}
		p.Ask = a.prefs.Perms.Notify == "" && !a.prefs.Notify.Off
		return p
	}
	return PermPrompt{Kind: kind}
}

// PermAnswer records the user's answer to an ask and does what it needs (the folder picker,
// the OS notification permission).
func (a *App) PermAnswer(kind string, allow bool) PermResult {
	switch kind {
	case "folder":
		if allow && a.ctx != nil {
			before := a.prefs.Folder
			if d := a.PickFolder(); d != "" && d != before {
				a.mu.Lock()
				a.prefs.Perms.Folder = ""
				a.mu.Unlock()
				a.savePrefs()
				return PermResult{Allowed: true, Folder: d, Line: "Working in " + d + "."}
			}
		}
		a.mu.Lock()
		a.prefs.Perms.Folder = "deny"
		f := a.prefs.Folder
		a.mu.Unlock()
		a.savePrefs()
		return PermResult{Folder: f, Line: folderFallback}
	case "notify":
		a.mu.Lock()
		if allow {
			a.prefs.Perms.Notify = "allow"
		} else {
			a.prefs.Perms.Notify = "deny"
		}
		a.mu.Unlock()
		a.savePrefs()
		if !allow {
			return PermResult{Line: notifyFallback}
		}
		if a.ctx != nil {
			// The first real notification: macOS shows its own permission prompt here, not at launch.
			if err := a.OnboardNotifyTest(); err != "" {
				return PermResult{Allowed: true, Line: "Blaze couldn't show a notification: " + err + ". Finished tasks still wait for you in Chat."}
			}
		}
		return PermResult{Allowed: true, Line: "Notifications on. Blaze only uses them when something finishes or needs you."}
	}
	return PermResult{}
}

// notifyDenied: the user said no to desktop notifications (the test note is still allowed,
// it's how they turn them back on from Settings).
func notifyDenied(p Prefs, kind string) bool { return p.Perms.Notify == "deny" && kind != "test" }

// probeAtStartup: whether startup may probe notifications. On macOS the probe asks the OS for
// permission, so it waits for the first real notification instead of prompting at launch.
func probeAtStartup(goos string) bool { return goos != "darwin" }

// permReason is the one-line "why" on a confirm card, from its title.
func permReason(title, detail string) string {
	t := strings.ToLower(title)
	switch {
	case strings.Contains(t, "risky command"), strings.Contains(t, "risky code"):
		return "This could change or delete things on your computer, so Blaze runs it only with your OK."
	case strings.Contains(t, "risky git"):
		return "This rewrites or publishes git history, which is hard to undo."
	case strings.HasPrefix(t, "delete"):
		return "Deleting can't be undone from inside Blaze."
	case strings.Contains(t, "outside this project"):
		return "It's outside the folder you picked, so Blaze checks with you first."
	case strings.HasPrefix(t, "install"):
		return "Installing adds software to this computer."
	case strings.Contains(t, "schedul"):
		return "A scheduled job runs by itself later, even when Blaze is closed."
	case strings.Contains(t, "phone"):
		return "This sends something from your phone in your name."
	}
	return ""
}

// permDeclined is the line a declined confirm card shows.
func permDeclined(title string) string {
	if permReason(title, "") == "" {
		return ""
	}
	return "Not done. Blaze will suggest another way or ask you."
}
