//go:build linux

package main

// Tiling Wayland compositors ignore a window's own size and position requests, so
// the orb (a compact mode of the one window) would fill the whole tile. On Hyprland
// and sway Blaze asks the compositor itself: float the window, size it 400x560 and
// move it bottom-right; Expand tiles it again. Other tiling WMs need a window rule
// (Doctor says so).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type tilingWM struct {
	mu      sync.Mutex
	floated bool   // we floated the window (so Expand tiles it again)
	addr    string // Hyprland window address
}

var tiling tilingWM

// tilingName names the tiling compositor Blaze can drive, or "".
func tilingName() string {
	switch {
	case os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "":
		return "hyprland"
	case os.Getenv("SWAYSOCK") != "":
		return "sway"
	}
	return ""
}

// otherTiling spots tiling compositors Blaze can't drive yet (for Doctor).
func otherTiling() string {
	d := strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP") + " " + os.Getenv("XDG_SESSION_DESKTOP") + " " + os.Getenv("DESKTOP_SESSION"))
	for _, n := range []string{"river", "niri", "qtile", "dwl", "i3", "bspwm", "awesome", "xmonad", "herbstluftwm", "leftwm", "wayfire", "labwc"} {
		if strings.Contains(d, n) {
			return n
		}
	}
	return ""
}

func wmRun(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return exec.CommandContext(ctx, name, args...).Output()
}

type hyprClient struct {
	Address  string `json:"address"`
	Pid      int    `json:"pid"`
	Class    string `json:"class"`
	Title    string `json:"title"`
	Floating bool   `json:"floating"`
	Mapped   bool   `json:"mapped"`
	Hidden   bool   `json:"hidden"`
}

type hyprMonitor struct {
	X, Y          int
	Width, Height int
	Scale         float64
	Transform     int
	Focused       bool
	Reserved      []int `json:"reserved"`
}

// hyprWindow finds Blaze's window: by our pid first, then by its class.
func hyprWindow() (hyprClient, bool) {
	b, err := wmRun("hyprctl", "clients", "-j")
	if err != nil {
		return hyprClient{}, false
	}
	var cs []hyprClient
	if json.Unmarshal(b, &cs) != nil {
		return hyprClient{}, false
	}
	pid := os.Getpid()
	for _, c := range cs {
		if c.Pid == pid && c.Mapped && !c.Hidden {
			return c, true
		}
	}
	for _, c := range cs {
		if strings.Contains(strings.ToLower(c.Class), "blaze") && c.Mapped && !c.Hidden {
			return c, true
		}
	}
	return hyprClient{}, false
}

// bottomRight is where a w x h window goes on a monitor (logical px), clear of bars.
func bottomRight(mx, my, mw, mh int, reserved []int, w, h int) (int, int) {
	r, bt := 0, 0
	if len(reserved) == 4 {
		r, bt = reserved[2], reserved[3]
	}
	x, y := mx+mw-w-24-r, my+mh-h-24-bt
	if x < mx {
		x = mx
	}
	if y < my {
		y = my
	}
	return x, y
}

func hyprFloat(w, h int) bool {
	c, ok := hyprWindow()
	if !ok {
		return false
	}
	addr := "address:" + c.Address
	cmds := []string{}
	if !c.Floating {
		cmds = append(cmds, "dispatch setfloating "+addr)
	}
	cmds = append(cmds, fmt.Sprintf("dispatch resizewindowpixel exact %d %d,%s", w, h, addr))
	if b, err := wmRun("hyprctl", "monitors", "-j"); err == nil {
		var ms []hyprMonitor
		if json.Unmarshal(b, &ms) == nil {
			for _, m := range ms {
				if !m.Focused {
					continue
				}
				sc := m.Scale
				if sc <= 0 {
					sc = 1
				}
				mw, mh := int(float64(m.Width)/sc), int(float64(m.Height)/sc)
				if m.Transform%2 == 1 { // rotated 90/270
					mw, mh = mh, mw
				}
				x, y := bottomRight(m.X, m.Y, mw, mh, m.Reserved, w, h)
				cmds = append(cmds, fmt.Sprintf("dispatch movewindowpixel exact %d %d,%s", x, y, addr))
			}
		}
	}
	if _, err := wmRun("hyprctl", "--batch", strings.Join(cmds, " ; ")); err != nil {
		return false
	}
	tiling.mu.Lock()
	if !c.Floating {
		tiling.floated = true
	}
	tiling.addr = c.Address
	tiling.mu.Unlock()
	return true
}

func hyprRestore() {
	tiling.mu.Lock()
	floated := tiling.floated
	tiling.floated = false
	tiling.mu.Unlock()
	if !floated {
		return
	}
	if c, ok := hyprWindow(); ok && c.Floating {
		_, _ = wmRun("hyprctl", "dispatch", "settiled", "address:"+c.Address)
	}
}

type swayNode struct {
	Pid     int        `json:"pid"`
	Type    string     `json:"type"`
	Focused bool       `json:"focused"`
	Nodes   []swayNode `json:"nodes"`
	Float   []swayNode `json:"floating_nodes"`
	Rect    struct{ X, Y, Width, Height int }
}

func swayFloat(w, h int) bool {
	pid := os.Getpid()
	b, err := wmRun("swaymsg", "-t", "get_tree")
	if err != nil {
		return false
	}
	var root swayNode
	if json.Unmarshal(b, &root) != nil {
		return false
	}
	found, floating := false, false
	var walk func(n swayNode, fl bool)
	walk = func(n swayNode, fl bool) {
		if n.Pid == pid {
			found, floating = true, fl
		}
		for _, c := range n.Nodes {
			walk(c, fl)
		}
		for _, c := range n.Float {
			walk(c, true)
		}
	}
	walk(root, false)
	if !found {
		return false
	}
	cmd := fmt.Sprintf("[pid=%d] floating enable, resize set %d %d", pid, w, h)
	if ob, err := wmRun("swaymsg", "-t", "get_outputs"); err == nil {
		var outs []struct {
			Focused bool `json:"focused"`
			Rect    struct{ X, Y, Width, Height int }
		}
		if json.Unmarshal(ob, &outs) == nil {
			for _, o := range outs {
				if o.Focused {
					x, y := bottomRight(o.Rect.X, o.Rect.Y, o.Rect.Width, o.Rect.Height, nil, w, h)
					cmd += fmt.Sprintf(", move absolute position %d %d", x, y)
				}
			}
		}
	}
	if _, err := wmRun("swaymsg", cmd); err != nil {
		return false
	}
	tiling.mu.Lock()
	if !floating {
		tiling.floated = true
	}
	tiling.mu.Unlock()
	return true
}

func swayRestore() {
	tiling.mu.Lock()
	floated := tiling.floated
	tiling.floated = false
	tiling.mu.Unlock()
	if floated {
		_, _ = wmRun("swaymsg", fmt.Sprintf("[pid=%d] floating disable", os.Getpid()))
	}
}

// tilingFloat makes the orb a floating w x h window bottom-right. The window may only
// just have been shown (mapped), so it retries briefly. Runs in the background.
func tilingFloat(w, h int) {
	wm := tilingName()
	if wm == "" {
		return
	}
	go func() {
		for _, d := range []time.Duration{60, 200, 450, 900} {
			time.Sleep(d * time.Millisecond)
			ok := false
			if wm == "hyprland" {
				ok = hyprFloat(w, h)
			} else {
				ok = swayFloat(w, h)
			}
			if ok {
				return
			}
		}
	}()
}

// tilingRestore tiles the window again on Expand (only if Blaze floated it).
func tilingRestore() {
	switch tilingName() {
	case "hyprland":
		hyprRestore()
	case "sway":
		swayRestore()
	}
}

// tilingCheck is Doctor's "Window manager" line ("" when not a tiling WM).
func tilingCheck() (bool, string) {
	switch tilingName() {
	case "hyprland":
		if _, err := exec.LookPath("hyprctl"); err != nil {
			return false, "Hyprland, but hyprctl isn't on PATH, so the orb can't float. Add: windowrulev2 = float, class:^(blaze)$"
		}
		cls := "blaze"
		if c, ok := hyprWindow(); ok && c.Class != "" {
			cls = c.Class
		}
		return true, "Hyprland · the orb floats bottom-right via hyprctl (window class " + cls + ")"
	case "sway":
		if _, err := exec.LookPath("swaymsg"); err != nil {
			return false, "sway, but swaymsg isn't on PATH, so the orb can't float. Add: for_window [app_id=\"blaze\"] floating enable"
		}
		return true, "sway · the orb floats bottom-right via swaymsg"
	}
	if n := otherTiling(); n != "" {
		return false, n + " is a tiling window manager Blaze can't resize yet: the orb may fill the screen. Add a rule to float app id/class \"blaze\" (it floats the main window too)."
	}
	return true, ""
}
