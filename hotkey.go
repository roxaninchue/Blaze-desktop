package main

import (
	"runtime"
	"strings"
	"sync"
	"time"

	"golang.design/x/hotkey"
)

// HotkeyPreset is one choice in Settings › Behaviour.
type HotkeyPreset struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// The shortcut presets. "alt" means Option on a Mac.
var presetIDs = []string{"ctrl+shift+space", "alt+space", "ctrl+alt+space", "ctrl+shift+b"}

func presetLabel(id string) string {
	mac := runtime.GOOS == "darwin"
	parts := strings.Split(id, "+")
	for i, p := range parts {
		switch p {
		case "ctrl":
			parts[i] = map[bool]string{true: "⌃", false: "Ctrl"}[mac]
		case "shift":
			parts[i] = map[bool]string{true: "⇧", false: "Shift"}[mac]
		case "alt":
			parts[i] = map[bool]string{true: "⌥", false: "Alt"}[mac]
		case "space":
			parts[i] = "Space"
		default:
			parts[i] = strings.ToUpper(p)
		}
	}
	if mac {
		return strings.Join(parts[:len(parts)-1], "") + " " + parts[len(parts)-1]
	}
	return strings.Join(parts, "+")
}

func presets() []HotkeyPreset {
	out := make([]HotkeyPreset, 0, len(presetIDs))
	for _, id := range presetIDs {
		out = append(out, HotkeyPreset{id, presetLabel(id)})
	}
	return out
}

func presetByID(id string) (HotkeyPreset, bool) {
	for _, p := range presets() {
		if p.ID == id {
			return p, true
		}
	}
	return HotkeyPreset{}, false
}

// defaultHotkey: Ctrl+Shift+Space on Windows and Linux, Option+Space on a Mac.
func defaultHotkey() string {
	if runtime.GOOS == "darwin" {
		return "alt+space"
	}
	return "ctrl+shift+space"
}

func (a *App) hotkeyID() string {
	if a.prefs.Hotkey == "" {
		return defaultHotkey()
	}
	return a.prefs.Hotkey
}

func hotkeyCombo(id string) ([]hotkey.Modifier, hotkey.Key) {
	var mods []hotkey.Modifier
	key := hotkey.KeySpace
	for _, p := range strings.Split(id, "+") {
		switch p {
		case "ctrl":
			mods = append(mods, hotkey.ModCtrl)
		case "shift":
			mods = append(mods, hotkey.ModShift)
		case "alt":
			mods = append(mods, modAlt)
		case "b":
			key = hotkey.KeyB
		}
	}
	return mods, key
}

type hkStatus struct {
	ok          bool
	note        string
	needsAccess bool
}

var (
	hkMu   sync.Mutex
	hkCur  *hotkey.Hotkey
	hkStop chan struct{}
	hkLast hkStatus
)

func (a *App) hotkeyStatus() hkStatus {
	hkMu.Lock()
	defer hkMu.Unlock()
	return hkLast
}

// applyHotkey (re)registers the shortcut from prefs.
func (a *App) applyHotkey() {
	hkMu.Lock()
	defer hkMu.Unlock()
	if hkCur != nil {
		close(hkStop)
		_ = hkCur.Unregister()
		hkCur, hkStop = nil, nil
	}
	id := a.hotkeyID()
	if id == "off" {
		hkLast = hkStatus{note: "Off"}
		return
	}
	if isWayland() {
		hkLast = hkStatus{note: "Wayland doesn't let apps grab a global shortcut. Bind one in your desktop's keyboard settings to run: blaze-desktop --orb"}
		return
	}
	mods, key := hotkeyCombo(id)
	hk := hotkey.New(mods, key)
	if err := registerHotkey(hk); err != nil {
		st := hkStatus{note: "Couldn't use " + presetLabel(id) + ": " + err.Error() + ". Another app may own it; pick another."}
		if runtime.GOOS == "darwin" {
			st = hkStatus{needsAccess: true, note: "macOS needs Accessibility permission for the shortcut. Grant access, then press Try again."}
		}
		hkLast = st
		return
	}
	stop := make(chan struct{})
	hkCur, hkStop = hk, stop
	hkLast = hkStatus{ok: true, note: "Press " + presetLabel(id) + " anywhere for quick ask"}
	go func() {
		var last time.Time
		for {
			select {
			case <-stop:
				return
			case _, ok := <-hk.Keydown():
				if !ok {
					return
				}
				if time.Since(last) < 300*time.Millisecond {
					continue
				}
				last = time.Now()
				go a.ToggleOrb()
			}
		}
	}()
}

// registerHotkey turns a panic in a native layer into an error.
func registerHotkey(hk *hotkey.Hotkey) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errorString("the shortcut library failed")
		}
	}()
	return hk.Register()
}

type errorString string

func (e errorString) Error() string { return string(e) }
