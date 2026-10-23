package main

// Screen vision: read_screen (text first, from the accessibility tree) and
// see_screen (a downscaled screenshot for a vision model). A small cache makes
// repeat reads instant, and a slow warm loop keeps it fresh while Blaze is
// working or the orb is open.

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	"runtime"
	"strings"
	"sync"
	"time"

	wr "github.com/wailsapp/wails/v2/pkg/runtime"

	"blaze-desktop/internal/ocr"
	"blaze-desktop/internal/screen"
	"blaze-desktop/internal/tools"
)

// VisionPrefs live in desktop.json under "vision".
type VisionPrefs struct {
	Off      bool `json:"off,omitempty"`       // screen reading disabled entirely
	NoPixels bool `json:"no_pixels,omitempty"` // text only: never take screenshots
}

// VisionView is what Settings and Doctor show.
type VisionView struct {
	Prefs VisionPrefs `json:"prefs"`
	Caps  screen.Caps `json:"caps"`
	OS    string      `json:"os"`
	Last  string      `json:"last"` // "Firefox — Pull request #12 · 3s ago · AT-SPI 140 ms"
	Warm  bool        `json:"warm"`
}

type visionSvc struct {
	r       *screen.Reader
	mu      sync.Mutex
	caps    *screen.Caps
	capsAt  time.Time
	looked  screen.Answers // see_screen answers by window + picture hash + question
	warm    bool
	reading int
}

var sv = &visionSvc{}

func (a *App) startVision() {
	sv.r = screen.NewReader()
	sv.r.OnRead = func(on bool, how string) {
		sv.mu.Lock()
		if on {
			sv.reading++
		} else if sv.reading > 0 {
			sv.reading--
		}
		n := sv.reading
		sv.mu.Unlock()
		if a.ctx != nil {
			wr.EventsEmit(a.ctx, "screen", map[string]any{"on": n > 0, "how": how})
		}
		if on {
			setTrayStatus("Reading your screen…")
		} else if n == 0 {
			a.mu.Lock()
			busy := a.busy
			a.mu.Unlock()
			setTrayStatus(map[bool]string{true: "Working…", false: "Idle"}[busy])
		}
	}
	go a.warmLoop()
}

// warmLoop pre-reads the front window once when the orb opens or a turn starts, so
// the model's first read_screen is instant. It reads on the rising edge only (never
// on a timer while Blaze stays busy) and never when screen reading is off.
func (a *App) warmLoop() {
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	was := false
	for range t.C {
		if a.ctx == nil || sv.r == nil {
			continue
		}
		a.mu.Lock()
		busy := a.busy
		a.mu.Unlock()
		a.sh.mu.Lock()
		orb := a.sh.orb && a.sh.visible
		a.sh.mu.Unlock()
		now := (busy || orb) && !a.prefs.Vision.Off
		sv.mu.Lock()
		sv.warm = now
		sv.mu.Unlock()
		if now && !was {
			ctx, cancel := context.WithTimeout(a.ctx, 8*time.Second)
			_, _, _ = sv.r.Read(ctx, 20*time.Second)
			cancel()
		}
		was = now
	}
}

func visionOff() string {
	return "Screen reading is turned off (Settings › Notifications › Screen vision). Ask the user to describe what they see, or to turn it on."
}

func init() {
	tools.Register("read_screen", "Read what's on the user's screen right now as compact text: the frontmost window (not Blaze) from the accessibility tree, roles abbreviated (btn button, in input, a link, txt text, h heading, li list item, dlg dialog, term terminal; * = focused). Instant when cached. After the first read it returns only what changed. Use it before helping with an app, an error dialog, a form or a terminal. Password fields are never included.",
		[][3]string{{"full", "boolean", "Return the whole window text even if you read it before"}, {"fresh", "boolean", "Skip the cache (the screen just changed)"}},
		nil, func(e *tools.Env, args map[string]any) string {
			a := theApp
			if a == nil || sv.r == nil {
				return "Error: screen reading isn't available here."
			}
			if a.prefs.Vision.Off {
				return visionOff()
			}
			ttl := 2 * time.Second
			if tools.ArgB(args, "fresh") {
				ttl = 0
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			s, _, err := sv.r.Read(ctx, ttl)
			if err != nil {
				msg := "Error: couldn't read the screen: " + err.Error()
				if !a.prefs.Vision.NoPixels {
					msg += "\nTry see_screen to look at it instead."
				}
				return msg
			}
			a.mu.Lock()
			conv := a.convID
			a.mu.Unlock()
			out := sv.r.ForModel(conv, s, tools.ArgB(args, "full"))
			if s.Nodes < 4 && !a.prefs.Vision.NoPixels {
				out += "\nLittle text was readable in this window; call see_screen to look at it."
			}
			if s.Err != "" {
				out += "\n(note: " + s.Err + ")"
			}
			return out
		})

	tools.Register("see_screen", "Look at the user's screen with a vision model: a downscaled screenshot of the frontmost window (not Blaze), or the whole screen. Use it when read_screen has too little text (images, canvases, games, remote desktops) or the question is visual (layout, colours, charts). Ask a specific question. Password fields are blurred.",
		[][3]string{{"question", "string", "What you want to know about the screen"}, {"whole_screen", "boolean", "The whole screen instead of the front window"}, {"region", "string", "Optional crop x,y,w,h inside the window (in window coordinates)"}},
		nil, func(e *tools.Env, args map[string]any) string {
			a := theApp
			if a == nil || sv.r == nil {
				return "Error: screen vision isn't available here."
			}
			if a.prefs.Vision.Off {
				return visionOff()
			}
			if a.prefs.Vision.NoPixels {
				return "Screenshots are turned off (Settings › Notifications › Screen vision: text only). Use read_screen."
			}
			q := strings.TrimSpace(tools.ArgS(args, "question"))
			if q == "" {
				q = "What is on the screen, and what is the user probably doing?"
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			// The text read first: it finds password fields to blur and gives the model context.
			s, _, _ := sv.r.Read(ctx, 3*time.Second)
			shot, err := sv.r.Look(ctx, screen.LookOpts{Full: tools.ArgB(args, "whole_screen"), Region: tools.ArgS(args, "region")})
			if err != nil {
				msg := "Error: couldn't take a screenshot: " + err.Error()
				if s != nil && s.Text != "" {
					msg += "\nHere is the window's text instead:\n" + s.Text
				}
				return msg
			}
			var hash uint64
			if img, _, err := image.Decode(bytes.NewReader(shot.JPEG)); err == nil {
				hash = screen.DHash(img)
			}
			key := fmt.Sprintf("%s|%v|%s|%s", shot.Win.Key(), tools.ArgB(args, "whole_screen"), tools.ArgS(args, "region"), q)
			head := fmt.Sprintf("[%s — %q · screenshot %dx%d via %s]\n", shot.Win.App, shot.Win.Title, shot.W, shot.H, shot.Source)
			if ans, age, ok := sv.looked.Get(key, hash, time.Now()); ok {
				return head + "(same picture as " + fmt.Sprint(int(age.Seconds())) + "s ago)\n" + ans
			}
			ans, model, err := screen.Describe(ctx, a.cfg, shot.JPEG, q)
			if err != nil {
				msg := "Error: " + err.Error()
				if s != nil && s.Text != "" {
					msg += "\nHere is the window's text instead:\n" + s.Text
				}
				return msg
			}
			sv.looked.Put(key, hash, ans, time.Now())
			return head + "[seen by " + model + "]\n" + ans
		})
}

// ---------------- UI ----------------

func (a *App) visionCaps(refresh bool) screen.Caps {
	sv.mu.Lock()
	c, at := sv.caps, sv.capsAt
	sv.mu.Unlock()
	if c != nil && !refresh && time.Since(at) < 2*time.Minute {
		return *c
	}
	if sv.r == nil {
		return screen.Caps{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nc := sv.r.Caps(ctx)
	sv.mu.Lock()
	sv.caps, sv.capsAt = &nc, time.Now()
	sv.mu.Unlock()
	return nc
}

func (a *App) visionView(refresh bool) VisionView {
	v := VisionView{Prefs: a.prefs.Vision, Caps: a.visionCaps(refresh), OS: runtime.GOOS}
	if sv.r != nil {
		if s := sv.r.Last(); s != nil {
			v.Last = fmt.Sprintf("%s — %s · %s ago · %s %d ms", orStr(s.Win.App, "?"), clipStr(s.Win.Title, 50), time.Since(s.At).Round(time.Second), orStr(s.Source, "?"), s.Ms)
		}
	}
	sv.mu.Lock()
	v.Warm = sv.warm
	sv.mu.Unlock()
	return v
}

// VisionStatus is Settings › Notifications › Screen vision.
func (a *App) VisionStatus() VisionView { a.waitReady(); return a.visionView(true) }

// SetVisionPrefs saves the screen vision switches.
func (a *App) SetVisionPrefs(p VisionPrefs) VisionView {
	a.prefs.Vision = p
	a.savePrefs()
	kickWatch()
	if a.ctx != nil {
		wr.EventsEmit(a.ctx, "vision", p)
	}
	return a.visionView(false)
}

// PrivateVisionView is Settings › Screen vision › Private vision.
type PrivateVisionView struct {
	On     bool   `json:"on"`
	Engine string `json:"engine"` // the on-device OCR ("" = none)
	Note   string `json:"note"`
}

func (a *App) privateVisionView() PrivateVisionView {
	name, note := ocr.Engine()
	return PrivateVisionView{On: a.cfg.PrivateVision, Engine: name, Note: note}
}

// PrivateVision reports the Private vision switch and the OCR it would use.
func (a *App) PrivateVision() PrivateVisionView { a.waitReady(); return a.privateVisionView() }

// SetPrivateVision turns Private vision on or off (shared with Blaze CLI in config.json).
func (a *App) SetPrivateVision(on bool) PrivateVisionView {
	a.cfg.PrivateVision = on
	_ = a.cfg.Save()
	return a.privateVisionView()
}

// GrantScreen opens the OS permission for screenshots (macOS Screen Recording).
func (a *App) GrantScreen() {
	screen.AskScreenRecording()
	if c := a.visionCaps(false); c.Permission != "" {
		wr.BrowserOpenURL(a.ctx, c.Permission)
	}
}

// GrantScreenText opens the OS permission for reading windows (macOS Accessibility).
func (a *App) GrantScreenText() {
	if c := a.visionCaps(false); c.TextPerm != "" {
		askAccessibility()
		wr.BrowserOpenURL(a.ctx, c.TextPerm)
	}
}

// TryReadScreen reads the front window now (Settings' "Test" button).
func (a *App) TryReadScreen() string {
	if a.prefs.Vision.Off || sv.r == nil {
		return "Screen reading is off."
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s, _, err := sv.r.Read(ctx, 0)
	if err != nil {
		return "Couldn't read the screen: " + err.Error()
	}
	return fmt.Sprintf("Read %s — %q: %d items in %d ms via %s.", orStr(s.Win.App, "?"), s.Win.Title, s.Nodes, s.Ms, s.Source)
}

// visionChecks are Doctor's lines.
func (a *App) visionChecks() []Check {
	if a.prefs.Vision.Off {
		return []Check{{Name: "Screen reading", OK: true, Detail: "Off (Settings › Notifications)"}, a.watchCheck()}
	}
	c := a.visionCaps(true)
	if a.cfg.PrivateVision {
		pv := a.privateVisionView()
		chk := Check{Name: "Private vision", OK: pv.Engine != "", Detail: "On: images are read on this computer with " + orStr(pv.Engine, "no OCR") + "; only text goes to models"}
		if pv.Engine == "" {
			chk.Detail = "On, but " + pv.Note
		}
		return append(a.visionChecksBase(c), chk)
	}
	return a.visionChecksBase(c)
}

func (a *App) visionChecksBase(c screen.Caps) []Check {
	text := Check{Name: "Screen reading", OK: c.Text, Detail: c.TextVia}
	if c.TextNote != "" {
		text.Detail += " · " + c.TextNote
	}
	if !c.Text {
		text.Fix = c.TextPerm
	}
	out := []Check{text}
	if a.prefs.Vision.NoPixels {
		return append(out, Check{Name: "Screenshots", OK: true, Detail: "Off: text only (Settings › Notifications)"}, a.watchCheck())
	}
	px := Check{Name: "Screenshots", OK: c.Pixels, Detail: orStr(c.PixelsVia, "none")}
	if c.PixelsNote != "" {
		px.Detail += " · " + c.PixelsNote
	}
	if !c.Pixels {
		px.Fix = c.Permission
	}
	return append(out, px, a.watchCheck())
}

// watchCheck is Doctor's line for proactive suggestions.
func (a *App) watchCheck() Check {
	p := a.prefs.Notify
	switch {
	case p.Off || !p.Proactive:
		return Check{Name: "Proactive suggestions", OK: true, Detail: "Off (Settings › Notifications)"}
	case a.prefs.Vision.Off:
		return Check{Name: "Proactive suggestions", OK: false, Detail: "On, but screen reading is off, so Blaze can't see what to help with"}
	}
	f, t := p.quiet()
	d := fmt.Sprintf("Every %d min at most, %d an hour", int(p.every().Minutes()), p.maxHour())
	if f != "" {
		d += ", quiet " + f + "–" + t
	}
	if s := watchState(); s != "" {
		d += " · " + s
	}
	return Check{Name: "Proactive suggestions", OK: true, Detail: d}
}
