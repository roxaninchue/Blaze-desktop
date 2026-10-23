package main

// Proactive suggestions: a slow, frugal watcher. At most every ~10 minutes while
// the user is at the computer (never full screen, never in quiet hours, never past
// the hourly limit) it reads the cached screen text and asks a model one strict
// question: is there something specific and genuinely useful to offer? The answer
// is almost always NONE. Ignored suggestions make it back off.

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"blaze-desktop/internal/llm"
	"blaze-desktop/internal/notify"
	"blaze-desktop/internal/screen"
)

var ws struct {
	mu        sync.Mutex
	state     string
	lastCheck time.Time
	lastHash  uint64
	lastText  string
}

func setWatch(s string) {
	ws.mu.Lock()
	ws.state = s
	ws.mu.Unlock()
}

func watchState() string {
	ws.mu.Lock()
	defer ws.mu.Unlock()
	return ws.state
}

const gatePrompt = `You decide whether Blaze, an assistant on the user's computer, should interrupt with a notification. You get the text of the window the user is looking at.

Reply with exactly NONE unless the screen shows something specific where Blaze can genuinely help right now, for example:
- an error dialog, a crash, or a stack trace
- a failing build, test or command in a terminal or editor
- a long or confusing form the user is filling in
- important information the user is likely to miss (a deadline, a warning, a failed payment)

Never greet. Never comment on ordinary browsing, reading, chatting, music, videos or games. Never give generic tips. Never mention private content (messages, passwords, money details). When in doubt: NONE.

If you do suggest something, reply with one JSON object and nothing else:
{"title": "<specific, max 50 chars>", "body": "<what you noticed and how Blaze can help, one sentence, max 140 chars>", "ask": "<the request Blaze should run if the user accepts, written as the user would ask>"}`

var jsonObj = regexp.MustCompile(`(?s)\{.*\}`)

// watchKick wakes the watcher early (Settings changed); it never polls.
var watchKick = make(chan struct{}, 1)

func kickWatch() {
	select {
	case watchKick <- struct{}{}:
	default:
	}
}

// watchLoop sleeps until the policy says a check is due. While suggestions are off
// it sleeps until Settings turns them on: no timer at all.
func (a *App) watchLoop() {
	select { // let startup settle
	case <-time.After(90 * time.Second):
	case <-watchKick:
	}
	for {
		var wait time.Duration
		func() {
			defer func() {
				if r := recover(); r != nil {
					wait = 10 * time.Minute
				}
			}()
			wait = a.watchTick(false)
		}()
		if wait <= 0 {
			<-watchKick
			continue
		}
		t := time.NewTimer(wait)
		select {
		case <-t.C:
		case <-watchKick:
			t.Stop()
		}
	}
}

func (p NotifyPrefs) policy() notify.Policy {
	f, t := p.quiet()
	return notify.Policy{Every: p.every(), MaxHour: p.maxHour(), QuietFrom: f, QuietTo: t}
}

// watchTick runs one check if it's due and returns how long to sleep (0 = until
// Settings changes). force skips the interval and the caps ("Check now").
func (a *App) watchTick(force bool) time.Duration {
	p := a.prefs.Notify
	if p.Off || !p.Proactive {
		setWatch("Off")
		return 0
	}
	if a.prefs.Vision.Off || sv.r == nil {
		setWatch("Off: screen reading is turned off")
		return 0
	}
	now := time.Now()
	h := notify.History()
	ws.mu.Lock()
	last := ws.lastCheck
	ws.mu.Unlock()
	v := p.policy().Check(now, last, h, screen.Idle())
	if !v.Run && !force {
		if v.Why != "" {
			setWatch(v.Why)
		}
		return v.Wait
	}
	a.mu.Lock()
	busy := a.busy
	a.mu.Unlock()
	if !force && (busy || sv.r.BlazeInFront()) {
		return time.Minute // the user is already talking to Blaze
	}
	ws.mu.Lock()
	ws.lastCheck = now
	ws.mu.Unlock()
	next := v.Wait
	if next <= 0 {
		next = p.every()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	snap, _, err := sv.r.Read(ctx, 30*time.Second)
	if err != nil || snap == nil {
		setWatch("Couldn't read the screen")
		return next
	}
	at := "Checked " + now.Format("15:04") + " · "
	if snap.Win.Fullscreen {
		setWatch(at + "paused: something is full screen")
		return next
	}
	ws.mu.Lock()
	same := snap.Hash == ws.lastHash
	prev := ws.lastText
	ws.lastHash, ws.lastText = snap.Hash, snap.Text
	ws.mu.Unlock()
	if same && !force {
		setWatch(at + "nothing new on screen")
		return next
	}
	if d, ok := screen.Diff(prev, snap.Text); ok && prev != "" && strings.Count(d, "\n") < 3 && !force {
		setWatch(at + "barely changed")
		return next
	}
	if snap.Nodes < 5 || len(snap.Text) < 120 {
		setWatch(at + "too little text")
		return next
	}
	text := snap.Text
	if len(text) > 3000 {
		text = text[:3000]
	}
	msgs := []llm.Message{{Role: "system", Content: gatePrompt}, {Role: "user", Content: text}}
	r, err := llm.CompleteWith(ctx, a.cfg, msgs, nil, llm.Opts{})
	if err != nil {
		setWatch(at + "no model answered")
		return next
	}
	s, ok := parseSuggestion(r.Text)
	if !ok {
		setWatch(at + "nothing worth interrupting")
		return next
	}
	if notify.Similar(h, s.Title, s.Body) {
		setWatch(at + "already suggested that")
		return next
	}
	e, err := a.send(notify.Note{Title: s.Title, Body: s.Body, Prompt: s.Ask, Kind: "suggest", Urgency: "low"})
	if err == nil {
		setWatch("Suggested at " + now.Format("15:04") + ": " + e.Title)
	}
	return next
}

type suggestion struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Ask   string `json:"ask"`
}

// parseSuggestion reads the gate's answer: NONE (or anything malformed) is no.
func parseSuggestion(out string) (suggestion, bool) {
	out = strings.TrimSpace(out)
	if out == "" || strings.HasPrefix(strings.ToUpper(out), "NONE") {
		return suggestion{}, false
	}
	m := jsonObj.FindString(out)
	if m == "" {
		return suggestion{}, false
	}
	var s suggestion
	if json.Unmarshal([]byte(m), &s) != nil {
		return suggestion{}, false
	}
	s.Title, s.Body, s.Ask = strings.TrimSpace(s.Title), strings.TrimSpace(s.Body), strings.TrimSpace(s.Ask)
	low := strings.ToLower(s.Title + " " + s.Body)
	if s.Title == "" || s.Body == "" || s.Ask == "" || len(s.Title) > 90 || len(s.Body) > 260 {
		return suggestion{}, false
	}
	for _, w := range []string{"hello", "hi there", "hey there", "how can i help", "let me know if"} {
		if strings.Contains(low, w) {
			return suggestion{}, false
		}
	}
	return s, true
}

// CheckSuggestionNow runs the watcher once (Settings' "Check now"); it says what happened.
func (a *App) CheckSuggestionNow() string {
	_ = a.watchTick(true)
	return fmt.Sprint(watchState())
}
