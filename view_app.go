package main

// open_view: Blaze switches the window into one of its views (Design, Memory, Jobs…) by
// itself. Visual work opens Design without asking; other views ask first unless the user
// asked for them. The top bar then shows "DESIGN · Exit", which returns to the chat.

import (
	"strings"

	"blaze-desktop/internal/tools"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Views open_view can switch to (the frontend's /views registry has the same ids).
var openableViews = map[string]string{
	"chat": "Chat", "design": "Design", "memory": "Memory", "jobs": "Jobs", "schedule": "Schedule",
	"accounts": "Accounts", "doctor": "Doctor", "settings": "Settings", "router": "BlazeRouter", "phone": "Phone",
	"music": "Music", "video": "Video", // v0.19 media_app.go
}

const openViewLine = "\nVIEWS\n- When a task is visual (a website, a page, a design, 3D, a poster), call open_view with view=design and ask=false before you build. For any other view, set ask=true unless the user asked for that view. open_view chat goes back to the plain chat.\n"

// openView validates the request, asks when needed, and tells the UI. It returns the
// tool result for the model.
func openView(view, reason string, ask bool, confirm func(title, detail string) bool, emit func(string)) string {
	view = strings.ToLower(strings.TrimSpace(view))
	name, ok := openableViews[view]
	if !ok {
		return "Error: unknown view " + view + ". Use one of: chat, design, memory, jobs, schedule, accounts, doctor, settings, router, phone, music, video."
	}
	if emit == nil {
		return "Error: there is no window to switch here."
	}
	if ask && confirm != nil && view != "chat" {
		detail := strings.TrimSpace(reason)
		if !confirm("Switch to "+name+"?", detail) {
			return "The user stayed in the chat."
		}
	}
	emit(view)
	if view == "chat" {
		return "Back in the chat."
	}
	return "Opened " + name + ". The user can press Exit to return to the chat."
}

func init() {
	tools.Register("open_view", "Switch Blaze's window to one of its views: design (live preview for anything visual), memory, jobs, schedule, accounts, doctor, settings, router (BlazeRouter: models and keys), phone (Blaze Link: pair the Android phone), music, video, or chat to go back. Visual work: ask=false. Other views: ask=true unless the user asked for that view.",
		[][3]string{{"view", "string", "design, memory, jobs, schedule, accounts, doctor, settings, router, phone, music, video or chat"}, {"ask", "boolean", "Ask the user before switching"}, {"reason", "string", "One short line shown when asking"}},
		[]string{"view"}, func(e *tools.Env, args map[string]any) string {
			a := theApp
			var emit func(string)
			if a != nil && a.ctx != nil {
				emit = func(v string) { wr.EventsEmit(a.ctx, "view:open", v) }
			}
			var confirm func(string, string) bool
			if e != nil && !e.Yolo {
				confirm = e.Confirm
			}
			return openView(tools.ArgS(args, "view"), tools.ArgS(args, "reason"), tools.ArgB(args, "ask"), confirm, emit)
		})
}
