package main

// Native notifications: probe once, cache the working method in ~/.blaze/notify.json,
// then every notification is a template fill. Clicks open Blaze on the related chat
// (or the orb with a suggestion).

import (
	"errors"
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/notify"
	"blaze-desktop/internal/tools"
)

// NotifyPrefs live in desktop.json under "notify". Zero values are the defaults.
type NotifyPrefs struct {
	Off       bool   `json:"off,omitempty"`        // no notifications at all
	NoTasks   bool   `json:"no_tasks,omitempty"`   // don't tell me when a chat or background job finishes
	NoJobs    bool   `json:"no_jobs,omitempty"`    // don't tell me when a scheduled job runs
	Proactive bool   `json:"proactive,omitempty"`  // screen-aware suggestions (opt-in)
	Every     int    `json:"every,omitempty"`      // minutes between suggestion checks (default 10)
	MaxHour   int    `json:"max_hour,omitempty"`   // suggestions per hour at most (default 3)
	QuietFrom string `json:"quiet_from,omitempty"` // quiet hours start, default 22:00
	QuietTo   string `json:"quiet_to,omitempty"`   // quiet hours end, default 08:00
	NoQuiet   bool   `json:"no_quiet,omitempty"`   // no quiet hours
}

func (p NotifyPrefs) every() time.Duration {
	if p.Every <= 0 {
		return 10 * time.Minute
	}
	if p.Every < 5 {
		return 5 * time.Minute
	}
	return time.Duration(p.Every) * time.Minute
}

func (p NotifyPrefs) maxHour() int {
	if p.MaxHour <= 0 {
		return 3
	}
	return p.MaxHour
}

func (p NotifyPrefs) quiet() (string, string) {
	if p.NoQuiet {
		return "", ""
	}
	f, t := p.QuietFrom, p.QuietTo
	if f == "" {
		f = "22:00"
	}
	if t == "" {
		t = "08:00"
	}
	return f, t
}

// NotifyView is what Settings and Doctor show.
type NotifyView struct {
	Prefs    NotifyPrefs    `json:"prefs"`
	Quiet    [2]string      `json:"quiet"`
	Method   string         `json:"method"`   // e.g. "Windows toast"
	Kind     string         `json:"kind"`     // native | exec | ""
	Clicks   bool           `json:"clicks"`   // a click opens Blaze
	Fallback string         `json:"fallback"` // the command method scheduled jobs use
	Probed   string         `json:"probed"`
	Missing  []string       `json:"missing,omitempty"`
	LastErr  string         `json:"last_error,omitempty"`
	History  []notify.Entry `json:"history"`
	Watch    string         `json:"watch"` // what the suggestion watcher is doing
}

type notifier struct {
	mu        sync.Mutex
	cache     *notify.Cache
	nativeOK  bool
	nativeErr string
	inited    bool
	tool      []time.Time // recent notify-tool sends (rate limit)
}

var nt = &notifier{}

// theApp is the running app, for tool handlers and callbacks.
var theApp *App

// startNotify runs at the end of startup.
func (a *App) startNotify() {
	theApp = a
	notify.WriteIcon(icon)
	tools.OnJobDone = a.jobDone
	go func() {
		a.initNative()
		nt.mu.Lock()
		c := notify.Load()
		nt.cache = c
		nt.mu.Unlock()
		if c == nil && probeAtStartup(runtime.GOOS) {
			a.probeNotify() // first run: work out the method once and cache it (macOS waits: its probe prompts)
		}
	}()
	a.startVision()
	go a.watchLoop()
}

func (a *App) initNative() {
	nt.mu.Lock()
	defer nt.mu.Unlock()
	if nt.inited || a.ctx == nil {
		return
	}
	nt.inited = true
	defer func() {
		if r := recover(); r != nil {
			nt.nativeOK, nt.nativeErr = false, fmt.Sprint("native notifications crashed: ", r)
		}
	}()
	if err := wr.InitializeNotifications(a.ctx); err != nil {
		nt.nativeErr = err.Error()
		return
	}
	wr.OnNotificationResponse(a.ctx, a.notificationClicked)
	nt.nativeOK = true
}

// nativeName is the in-process method on this OS.
func nativeName() string {
	switch runtime.GOOS {
	case "windows":
		return "Windows toast"
	case "darwin":
		return "macOS Notification Center"
	}
	return "D-Bus notifications"
}

// probeNotify finds the best working method and caches it with its call template.
func (a *App) probeNotify() *notify.Cache {
	a.initNative()
	c := notify.NewCache(time.Now())
	nt.mu.Lock()
	nativeOK, nativeErr := nt.nativeOK, nt.nativeErr
	nt.mu.Unlock()
	name := nativeName()
	if nativeOK {
		switch runtime.GOOS {
		case "linux":
			// Something must own org.freedesktop.Notifications (mako, dunst, swaync, GNOME Shell, KDE…).
			if server, err := notifyServer(); err != nil {
				nativeOK = false
				nativeErr = "no notification daemon is running (GNOME and KDE have one built in; on Hyprland or sway start mako, dunst or swaync)"
			} else if server != "" {
				name += " · " + server
			}
		case "darwin":
			ok, err := wr.CheckNotificationAuthorization(a.ctx)
			if err == nil && !ok {
				ok, err = wr.RequestNotificationAuthorization(a.ctx) // macOS asks once
			}
			if err != nil || !ok {
				nativeOK = false
				nativeErr = "Notifications are off for Blaze in System Settings › Notifications"
				if err != nil {
					nativeErr = err.Error()
				}
			}
		}
	}
	execs, missing := notify.ExecCandidates()
	var native *notify.Method
	if nativeOK {
		native = &notify.Method{Kind: "native", Name: name, Clicks: true}
	} else {
		nativeErr = nativeName() + ": " + orStr(nativeErr, "not available")
	}
	c.Choose(native, nativeErr, execs, missing)
	c.Save()
	nt.mu.Lock()
	nt.cache = c
	nt.mu.Unlock()
	return c
}

func notifyServer() (string, error) {
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var name, vendor, version, spec string
	err = conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications").
		Call("org.freedesktop.Notifications.GetServerInformation", 0).Store(&name, &vendor, &version, &spec)
	if err != nil {
		return "", err
	}
	return name, nil
}

func (a *App) notifyCache() *notify.Cache {
	nt.mu.Lock()
	c := nt.cache
	nt.mu.Unlock()
	if c == nil {
		c = a.probeNotify()
	}
	return c
}

// send shows a notification. It's skipped (nil error) when notifications are off.
func (a *App) send(n notify.Note) (notify.Entry, error) {
	e := notify.Entry{At: time.Now()}
	if a.prefs.Notify.Off {
		return e, errors.New("notifications are off in Settings › Notifications")
	}
	if notifyDenied(a.prefs, n.Kind) {
		return e, errors.New("you chose no desktop notifications; it's waiting in Chat")
	}
	n.ID = newID()
	n.Clean()
	e.Note = n
	c := a.notifyCache()
	err := a.sendWith(c.Primary, n)
	if err != nil {
		// The cached method stopped working: probe again once, then try the command fallback.
		c = a.probeNotify()
		if err = a.sendWith(c.Primary, n); err != nil && c.Fallback != nil && c.Fallback != c.Primary {
			err = a.sendWith(c.Fallback, n)
			e.Method = c.Fallback.Name
		}
		if err != nil {
			c.LastErr = err.Error()
			c.Save()
		}
	}
	if e.Method == "" && c.Primary != nil {
		e.Method = c.Primary.Name
	}
	if err != nil {
		e.Err = err.Error()
	}
	notify.Record(e)
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "notified", e)
	}
	return e, err
}

func (a *App) sendWith(m *notify.Method, n notify.Note) error {
	if m == nil {
		return errors.New("no way to show notifications was found on this computer")
	}
	if m.Kind == "native" {
		if a.ctx == nil {
			return errors.New("the app isn't ready")
		}
		data := map[string]any{"kind": n.Kind, "note": n.ID}
		if n.Chat != "" {
			data["chat"] = n.Chat
		}
		return wr.SendNotification(a.ctx, wr.NotificationOptions{ID: n.ID, Title: n.Title, Body: n.Body, Data: data})
	}
	return notify.RunExec(*m, n)
}

// notificationClicked: open Blaze on the related chat, or the orb with the suggestion.
func (a *App) notificationClicked(res wr.NotificationResult) {
	if res.Error != nil {
		return
	}
	r := res.Response
	id := r.ID
	if s, _ := r.UserInfo["note"].(string); s != "" {
		id = s
	}
	e, ok := notify.Mark(id, true)
	chat, _ := r.UserInfo["chat"].(string)
	kind, _ := r.UserInfo["kind"].(string)
	if ok && chat == "" {
		chat = e.Chat
	}
	if kind == "suggest" || (ok && e.Kind == "suggest") {
		a.ShowOrb()
		if ok {
			wr.EventsEmit(a.ctx, "suggest", e)
		}
		return
	}
	a.ShowMain()
	if chat != "" {
		wr.EventsEmit(a.ctx, "open_chat", chat)
	}
}

// ---------------- events that notify ----------------

var mdRe = regexp.MustCompile("(?s)```.*?```|:::card[^\n]*|:::|[*_`#>]+")

// afterTurn tells the user an answer is ready when Blaze's window is hidden.
func (a *App) afterTurn(s *session, started time.Time) {
	if a.prefs.Notify.Off || a.prefs.Notify.NoTasks || a.ctx == nil {
		return
	}
	a.sh.mu.Lock()
	visible := a.sh.visible
	a.sh.mu.Unlock()
	a.mu.Lock()
	away := a.cur != s // finished in a chat the user left
	a.mu.Unlock()
	if (visible && !away) || time.Since(started) < 3*time.Second {
		return
	}
	a.mu.Lock()
	conv, text := s.id, ""
	for i := len(s.hist) - 1; i >= 0; i-- {
		if s.hist[i].Role == "assistant" && strings.TrimSpace(s.hist[i].Content) != "" {
			text = s.hist[i].Content
			break
		}
	}
	a.mu.Unlock()
	body := strings.Join(strings.Fields(mdRe.ReplaceAllString(text, " ")), " ")
	if isPrivate(conv) {
		body = "Blaze answered in your private chat." // never the words themselves
	}
	if body == "" {
		body = "Your answer is ready."
	}
	_, _ = a.send(notify.Note{Title: "Blaze finished", Body: body, Kind: "task", Chat: conv})
}

// agentElsewhere: a background agent finished in a chat that isn't open. The toast
// covers a visible window; a hidden one gets a native notification that opens that chat.
func (a *App) agentElsewhere(handle, conv string) {
	if a.prefs.Notify.Off || a.prefs.Notify.NoTasks || a.ctx == nil {
		return
	}
	a.sh.mu.Lock()
	visible := a.sh.visible
	a.sh.mu.Unlock()
	if visible {
		return
	}
	_, _ = a.send(notify.Note{Title: "Background agent finished", Body: "Agent " + handle + " finished its task. Open the chat for its report.", Kind: "task", Chat: conv})
}

// jobDone: a background job (server, build, watcher) ended after a while.
func (a *App) jobDone(id int, cmd string, code int, took float64) {
	if a.prefs.Notify.Off || a.prefs.Notify.NoTasks || took < 30 {
		return
	}
	a.mu.Lock()
	conv := a.convID
	a.mu.Unlock()
	title := "Background job finished"
	if code != 0 {
		title = fmt.Sprintf("Background job failed (exit %d)", code)
	}
	cmd = strings.Join(strings.Fields(cmd), " ")
	_, _ = a.send(notify.Note{Title: title, Body: fmt.Sprintf("%s · %s", clipStr(cmd, 120), (time.Duration(took) * time.Second).String()), Kind: "task", Chat: conv, Urgency: map[bool]string{true: "normal", false: "critical"}[code == 0]})
}

func clipStr(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}

// ---------------- the notify tool ----------------

func init() {
	tools.Register("notify", "Show a native notification on this computer (instant; the method is cached). Only for something the user needs to know when they're not looking at Blaze: a long task finished, something failed, a reminder they asked for. Never for greetings or small talk.",
		[][3]string{{"title", "string", "Short title (max ~60 chars)"}, {"body", "string", "One or two sentences"}},
		[]string{"title", "body"}, func(e *tools.Env, args map[string]any) string {
			a := theApp
			if a == nil {
				return "Error: notifications aren't available here."
			}
			nt.mu.Lock()
			var recent []time.Time
			for _, t := range nt.tool {
				if time.Since(t) < time.Hour {
					recent = append(recent, t)
				}
			}
			if len(recent) >= 10 {
				nt.tool = recent
				nt.mu.Unlock()
				return "Not sent: already 10 notifications this hour. Tell the user in the chat instead."
			}
			nt.tool = append(recent, time.Now())
			nt.mu.Unlock()
			a.mu.Lock()
			conv := a.convID
			a.mu.Unlock()
			en, err := a.send(notify.Note{Title: tools.ArgS(args, "title"), Body: tools.ArgS(args, "body"), Kind: "tool", Chat: conv})
			if err != nil {
				return "Error: the notification couldn't be shown: " + err.Error()
			}
			return "Notification shown via " + en.Method + "."
		})
}

// ---------------- UI ----------------

func (a *App) notifyView(withHistory bool) NotifyView {
	v := NotifyView{Prefs: a.prefs.Notify}
	f, t := a.prefs.Notify.quiet()
	v.Quiet = [2]string{f, t}
	nt.mu.Lock()
	c := nt.cache
	nt.mu.Unlock()
	if c != nil {
		if c.Primary != nil {
			v.Method, v.Kind, v.Clicks = c.Primary.Name, c.Primary.Kind, c.Primary.Clicks
		}
		if c.Fallback != nil {
			v.Fallback = c.Fallback.Name
		}
		v.Probed = c.Probed.Format("Jan 2 15:04")
		v.Missing = c.Tried
		v.LastErr = c.LastErr
	}
	if withHistory {
		v.History = notify.History()
		if len(v.History) > 40 {
			v.History = v.History[:40]
		}
	}
	v.Watch = watchState()
	return v
}

// NotifyStatus is Settings › Notifications.
func (a *App) NotifyStatus() NotifyView { a.waitReady(); return a.notifyView(true) }

// ProbeNotify works out the notification method again (Detect again / onboarding).
func (a *App) ProbeNotify() NotifyView { a.probeNotify(); return a.notifyView(true) }

// TestNotify sends a test notification; "" or the error.
func (a *App) TestNotify() string {
	_, err := a.send(notify.Note{Title: "Blaze notifications work", Body: "This is how Blaze will tell you when something needs you.", Kind: "test"})
	if err != nil {
		return err.Error()
	}
	return ""
}

// SetNotifyPrefs saves Settings › Notifications.
func (a *App) SetNotifyPrefs(p NotifyPrefs) NotifyView {
	if p.MaxHour > 10 {
		p.MaxHour = 10
	}
	if p.Every > 120 {
		p.Every = 120
	}
	a.prefs.Notify = p
	a.savePrefs()
	kickWatch()
	return a.notifyView(true)
}

func (a *App) NotifyHistory() []notify.Entry { return notify.History() }
func (a *App) ClearNotifyHistory()           { notify.ClearHistory() }

// SuggestionAction: the user accepted (true) or dismissed a suggestion. Dismissals back off.
func (a *App) SuggestionAction(id string, accept bool) {
	notify.Mark(id, accept)
}

// notifyCheck is Doctor's line.
func (a *App) notifyCheck() Check {
	if a.prefs.Notify.Off {
		return Check{Name: "Notifications", OK: true, Detail: "Off (Settings › Notifications)"}
	}
	c := a.probeNotify()
	if c.Primary == nil {
		return Check{Name: "Notifications", OK: false, Detail: "No way to show notifications was found: " + strings.Join(c.Tried, "; ")}
	}
	d := c.Primary.Name + " · cached in ~/.blaze/notify.json"
	if !c.Primary.Clicks {
		d += " · clicks don't open Blaze"
	}
	if c.Primary.Kind != "native" && len(c.Tried) > 0 {
		d += " (" + c.Tried[0] + ")"
	}
	return Check{Name: "Notifications", OK: true, Detail: d}
}
