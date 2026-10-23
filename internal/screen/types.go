// Package screen lets Blaze look at the user's screen, text first: the focused
// window's accessibility tree (UI Automation on Windows, AX on macOS, AT-SPI on
// Linux) in a compact encoding, with a downscaled screenshot as the fallback.
//
// The tree and the screenshot are taken in a short-lived helper process (the
// Blaze binary run with "__screen"), so a hung or crashing accessibility
// provider in another app can never freeze or crash Blaze itself. Cheap probes
// (which window is in front, idle time, full screen) run in-process.
package screen

import (
	"fmt"
	"strconv"
	"strings"
)

// Win is the window Blaze is looking at: the frontmost window that isn't Blaze's.
type Win struct {
	ID         uint64 `json:"id"`
	PID        int    `json:"pid"`
	App        string `json:"app"`
	Title      string `json:"title"`
	X          int    `json:"x"`
	Y          int    `json:"y"`
	W          int    `json:"w"`
	H          int    `json:"h"`
	Fullscreen bool   `json:"fullscreen"` // full screen or presenting: never interrupt
}

func (w Win) Key() string { return fmt.Sprintf("%d|%d|%s", w.ID, w.PID, w.Title) }

// Rect is a screen rectangle (password fields to blur, a region to crop).
type Rect struct{ X, Y, W, H int }

func (r Rect) Empty() bool { return r.W <= 0 || r.H <= 0 }

// ParseRect reads "x,y,w,h".
func ParseRect(s string) (Rect, bool) {
	p := strings.Split(strings.TrimSpace(s), ",")
	if len(p) != 4 {
		return Rect{}, false
	}
	var v [4]int
	for i, x := range p {
		n, err := strconv.Atoi(strings.TrimSpace(x))
		if err != nil {
			return Rect{}, false
		}
		v[i] = n
	}
	r := Rect{v[0], v[1], v[2], v[3]}
	return r, !r.Empty()
}

func (r Rect) String() string { return fmt.Sprintf("%d,%d,%d,%d", r.X, r.Y, r.W, r.H) }

// Flat is one accessibility node in document order; D is its depth.
type Flat struct {
	D      int    `json:"d"`
	Role   string `json:"r"`
	Name   string `json:"n,omitempty"`
	Value  string `json:"v,omitempty"`
	Secret bool   `json:"s,omitempty"` // a password field: its value is never read
	Focus  bool   `json:"f,omitempty"`
	Box    *Rect  `json:"b,omitempty"` // only kept for secret fields (to blur them)
}

// Tree is what the helper returns for "tree".
type Tree struct {
	Win    Win    `json:"win"`
	Nodes  []Flat `json:"nodes"`
	Source string `json:"source"` // "UI Automation", "AX", "AT-SPI"
	Err    string `json:"err,omitempty"`
	Ms     int    `json:"ms"`
	Capped bool   `json:"capped,omitempty"`
}

// Shot is what the helper returns for "shot".
type Shot struct {
	Win    Win    `json:"win"`
	JPEG   []byte `json:"jpeg"`
	W      int    `json:"w"`
	H      int    `json:"h"`
	Source string `json:"source"`
	Err    string `json:"err,omitempty"`
	Ms     int    `json:"ms"`
}

// Caps says what this computer can do, for Doctor and Settings.
type Caps struct {
	Text       bool   `json:"text"`        // an accessibility tree can be read
	TextVia    string `json:"text_via"`    // "UI Automation", "AX", "AT-SPI"
	TextNote   string `json:"text_note"`   // what's missing / what to do
	Pixels     bool   `json:"pixels"`      // a screenshot can be taken
	PixelsVia  string `json:"pixels_via"`  // "GDI", "screencapture", "grim", "portal", "X11"
	PixelsNote string `json:"pixels_note"` // what's missing / what to do
	Permission string `json:"permission"`  // a deep link that grants what's missing (macOS)
	TextPerm   string `json:"text_perm"`   // a deep link for the accessibility permission (macOS)
}

// maxDepth is the deepest accessibility level read.
const maxDepth = 40

// pixelScale is image pixels per screen unit of the last capture (2 on a Retina Mac).
var pixelScale = 1.0
