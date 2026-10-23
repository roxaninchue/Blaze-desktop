package main

// The desktop shell around the chat: background mode, the orb (quick ask),
// the global shortcut and start-at-login. The tray itself is in tray*.go.

import (
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"
)

// shellState is the window and tray bookkeeping, guarded by its own lock so the
// tray, the hotkey and the UI can call in from any goroutine.
type shellState struct {
	mu        sync.Mutex
	visible   bool // our own idea of whether the window is on screen
	orb       bool // the window is in compact orb mode
	saved     bool // geometry below was saved before the orb
	x, y      int
	w, h      int
	maximised bool

	trayOK  bool   // the tray / menu-bar icon is up
	trayErr string // why it isn't
	lastKey time.Time
}

// DesktopView is what Settings, Doctor and the hero show about the shell.
type DesktopView struct {
	OS          string         `json:"os"`
	Tray        bool           `json:"tray"`
	TrayNote    string         `json:"tray_note"`
	Background  bool           `json:"background"`
	Autostart   bool           `json:"autostart"`
	AutostartOK bool           `json:"autostart_ok"` // the OS entry really exists
	Hotkey      string         `json:"hotkey"`       // preset id, or "off"
	HotkeyLabel string         `json:"hotkey_label"` // e.g. Ctrl+Shift+Space
	HotkeyOK    bool           `json:"hotkey_ok"`    // registered and listening
	HotkeyNote  string         `json:"hotkey_note"`  // status or what to do
	NeedsAccess bool           `json:"needs_access"` // macOS Accessibility permission missing
	Wayland     bool           `json:"wayland"`
	Frameless   bool           `json:"frameless"`
	Orb         bool           `json:"orb"`
	Presets     []HotkeyPreset `json:"presets"`
}

// startFlags come from the command line (and from a second launch).
type startFlags struct {
	background bool // --background: start hidden (login items use this)
	orb        bool // --orb: open the quick-ask orb
	relaunch   bool // --relaunch: an update restarted us
}

func parseFlags(args []string) startFlags {
	var f startFlags
	for _, a := range args {
		switch strings.TrimLeft(a, "-") {
		case "background", "hidden":
			f.background = true
		case "orb":
			f.orb = true
		case "relaunch":
			f.relaunch = true
		}
	}
	return f
}

func isWayland() bool {
	return runtime.GOOS == "linux" && (strings.EqualFold(os.Getenv("XDG_SESSION_TYPE"), "wayland") || (os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == ""))
}

// backgroundOn: closing the window keeps Blaze running (default on).
func (a *App) backgroundOn() bool { return a.prefs.Background == nil || *a.prefs.Background }

// quit really quits: tray Quit, the updater, and close when background mode is off.
func (a *App) quit() {
	a.sh.mu.Lock()
	a.quitting = true
	a.sh.mu.Unlock()
	if a.ctx != nil {
		wr.Quit(a.ctx)
	}
}

// beforeClose hides the window instead of quitting when Blaze lives in the tray.
func (a *App) beforeClose(ctx interface{}) bool {
	a.sh.mu.Lock()
	quitting, tray := a.quitting, a.sh.trayOK
	a.sh.mu.Unlock()
	if quitting || !tray || !a.backgroundOn() {
		return false
	}
	a.HideWindow()
	return true
}

// secondInstance runs when Blaze is launched again while it's already running.
func (a *App) secondInstance(args []string) {
	a.waitReady()
	// A blaze:// sign-in link (Windows and Linux hand it over as an argument).
	for _, arg := range args {
		if strings.HasPrefix(strings.ToLower(arg), "blaze://") {
			a.routeURL(arg)
			a.ShowMain()
			return
		}
	}
	if parseFlags(args).orb {
		a.ShowOrb()
	} else {
		a.ShowMain()
	}
}

// ---------------- window ----------------

// ShowMain brings the full window back (leaving orb mode if needed).
func (a *App) ShowMain() {
	if a.ctx == nil {
		return
	}
	a.sh.mu.Lock()
	orb := a.sh.orb
	a.sh.mu.Unlock()
	if orb {
		a.ExpandOrb()
		return
	}
	wr.WindowShow(a.ctx)
	wr.WindowUnminimise(a.ctx)
	a.sh.mu.Lock()
	a.sh.visible = true
	a.sh.mu.Unlock()
}

// HideWindow hides Blaze; it keeps running in the tray.
func (a *App) HideWindow() {
	if a.ctx == nil {
		return
	}
	wr.WindowHide(a.ctx)
	a.sh.mu.Lock()
	a.sh.visible = false
	a.sh.mu.Unlock()
}

// ShowOrb turns the window into the compact always-on-top quick ask, bottom-right.
func (a *App) ShowOrb() {
	if a.ctx == nil {
		return
	}
	a.sh.mu.Lock()
	wasOrb, visible := a.sh.orb, a.sh.visible
	a.sh.mu.Unlock()
	if !wasOrb {
		max := visible && wr.WindowIsMaximised(a.ctx)
		var x, y, w, h int
		if visible {
			if max {
				wr.WindowUnmaximise(a.ctx)
			}
			x, y = wr.WindowGetPosition(a.ctx)
			w, h = wr.WindowGetSize(a.ctx)
		}
		a.sh.mu.Lock()
		if visible && w > 0 {
			a.sh.x, a.sh.y, a.sh.w, a.sh.h, a.sh.maximised, a.sh.saved = x, y, w, h, max, true
		}
		a.sh.orb = true
		a.sh.mu.Unlock()
	}
	const ow, oh = 400, 560
	wr.WindowSetMinSize(a.ctx, 340, 420)
	wr.WindowSetSize(a.ctx, ow, oh)
	if sw, shh := a.screenSize(); sw > 0 {
		x, y := sw-ow-24, shh-oh-64
		if runtime.GOOS == "darwin" {
			y = shh - oh - 96 // leave room for the Dock
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		wr.WindowSetPosition(a.ctx, x, y)
	}
	wr.WindowSetAlwaysOnTop(a.ctx, true)
	wr.WindowShow(a.ctx)
	wr.WindowUnminimise(a.ctx)
	a.sh.mu.Lock()
	a.sh.visible = true
	a.sh.mu.Unlock()
	tilingFloat(ow, oh) // Hyprland / sway ignore WindowSetSize (tiling_linux.go)
	wr.EventsEmit(a.ctx, "orb", true)
}

// ExpandOrb goes back to the full window where it was.
func (a *App) ExpandOrb() {
	if a.ctx == nil {
		return
	}
	a.sh.mu.Lock()
	saved, x, y, w, h, max := a.sh.saved, a.sh.x, a.sh.y, a.sh.w, a.sh.h, a.sh.maximised
	a.sh.orb, a.sh.saved = false, false
	a.sh.mu.Unlock()
	wr.WindowSetAlwaysOnTop(a.ctx, false)
	tilingRestore()
	wr.WindowSetMinSize(a.ctx, 760, 520)
	if saved {
		wr.WindowSetSize(a.ctx, w, h)
		wr.WindowSetPosition(a.ctx, x, y)
	} else {
		wr.WindowSetSize(a.ctx, 1180, 780)
		wr.WindowCenter(a.ctx)
	}
	if max {
		wr.WindowMaximise(a.ctx)
	}
	wr.WindowShow(a.ctx)
	wr.WindowUnminimise(a.ctx)
	a.sh.mu.Lock()
	a.sh.visible = true
	a.sh.mu.Unlock()
	wr.EventsEmit(a.ctx, "orb", false)
}

// ToggleOrb is the global shortcut: a visible orb hides, otherwise the orb shows.
func (a *App) ToggleOrb() {
	a.sh.mu.Lock()
	orbUp := a.sh.orb && a.sh.visible
	a.sh.mu.Unlock()
	if orbUp {
		a.HideWindow()
	} else {
		a.ShowOrb()
	}
}

func (a *App) screenSize() (int, int) {
	screens, err := wr.ScreenGetAll(a.ctx)
	if err != nil || len(screens) == 0 {
		return 0, 0
	}
	pick := screens[0]
	for _, s := range screens {
		if s.IsPrimary {
			pick = s
		}
	}
	for _, s := range screens {
		if s.IsCurrent {
			pick = s
		}
	}
	w, h := pick.Size.Width, pick.Size.Height
	if w == 0 {
		w, h = pick.Width, pick.Height
	}
	return w, h
}

// Window buttons for the frameless Windows title bar.
func (a *App) Minimise()       { wr.WindowMinimise(a.ctx) }
func (a *App) ToggleMaximise() { wr.WindowToggleMaximise(a.ctx) }

// CloseWindow is the custom close button: it hides in background mode, else quits.
func (a *App) CloseWindow() {
	if !a.beforeClose(nil) {
		a.quit()
	}
}

// ---------------- settings ----------------

func (a *App) SetBackground(on bool) {
	a.prefs.Background = &on
	a.savePrefs()
}

func (a *App) SetShowSteps(on bool) { a.prefs.ShowSteps = on; a.savePrefs() }

// SetFoldCards turns answer cards (short answer + expandable detail) on or off.
func (a *App) SetFoldCards(on bool) { a.prefs.FoldCards = &on; a.savePrefs() }

// SetAutostart adds or removes the start-at-login entry. It returns "" or the error.
func (a *App) SetAutostart(on bool) string {
	if err := setAutostart(on); err != nil {
		return err.Error()
	}
	a.prefs.Autostart = on
	a.savePrefs()
	return ""
}

// SetHotkey switches the global shortcut preset ("off" turns it off) and reports how it went.
func (a *App) SetHotkey(id string) DesktopView {
	if _, ok := presetByID(id); !ok && id != "off" {
		id = defaultHotkey()
	}
	a.prefs.Hotkey = id
	a.savePrefs()
	a.applyHotkey()
	return a.desktopView()
}

// GrantAccess opens macOS's Accessibility settings (the shortcut needs it).
func (a *App) GrantAccess() {
	askAccessibility()
	wr.BrowserOpenURL(a.ctx, "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility")
}

// RetryHotkey registers the shortcut again (after granting access).
func (a *App) RetryHotkey() DesktopView { a.applyHotkey(); return a.desktopView() }

func (a *App) desktopView() DesktopView {
	a.sh.mu.Lock()
	tray, trayErr, orb := a.sh.trayOK, a.sh.trayErr, a.sh.orb
	a.sh.mu.Unlock()
	hk := a.hotkeyStatus()
	d := DesktopView{OS: runtime.GOOS, Tray: tray, TrayNote: trayErr, Background: a.backgroundOn(), Autostart: a.prefs.Autostart,
		AutostartOK: autostartEnabled(), Hotkey: a.hotkeyID(), HotkeyOK: hk.ok, HotkeyNote: hk.note, NeedsAccess: hk.needsAccess,
		Wayland: isWayland(), Frameless: runtime.GOOS == "windows", Orb: orb, Presets: presets()}
	if p, ok := presetByID(d.Hotkey); ok {
		d.HotkeyLabel = p.Label
	}
	if tray && d.TrayNote == "" {
		d.TrayNote = map[string]string{"darwin": "In the menu bar", "windows": "In the system tray"}[runtime.GOOS]
		if d.TrayNote == "" {
			d.TrayNote = "In the system tray"
		}
	}
	return d
}

// startShell runs after startup: tray, shortcut, and the orb if asked for.
func (a *App) startShell() {
	a.sh.mu.Lock()
	a.sh.visible = !a.flags.background
	a.sh.mu.Unlock()
	a.startTray()
	a.applyHotkey()
	// Keep the login item pointing at this copy of Blaze (it may have moved or updated).
	if a.prefs.Autostart && !autostartEnabled() {
		_ = setAutostart(true)
	}
	go func() {
		time.Sleep(900 * time.Millisecond)
		if a.flags.orb {
			a.ShowOrb()
			return
		}
		// Started hidden but there's no tray and no shortcut to get back: show the window.
		time.Sleep(2 * time.Second)
		a.sh.mu.Lock()
		tray, visible := a.sh.trayOK, a.sh.visible
		a.sh.mu.Unlock()
		if !visible && !tray && !a.hotkeyStatus().ok {
			a.ShowMain()
		}
	}()
}
