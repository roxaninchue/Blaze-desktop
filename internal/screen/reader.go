package screen

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot is the last text read of the frontmost window.
type Snapshot struct {
	Win     Win       `json:"win"`
	Text    string    `json:"text"` // compact encoding
	Nodes   int       `json:"nodes"`
	Source  string    `json:"source"`
	Secrets []Rect    `json:"-"` // password fields (screen coordinates), blurred in screenshots
	At      time.Time `json:"at"`
	Ms      int       `json:"ms"`
	Hash    uint64    `json:"hash"`  // of Text
	Thumb   uint64    `json:"thumb"` // perceptual hash of the window, where it's cheap to take
	Err     string    `json:"err,omitempty"`
	key     string    // the probed window's Key (the cache key with Thumb)
}

// Age of the snapshot.
func (s *Snapshot) Age() time.Duration { return time.Since(s.At) }

// Reader reads the screen with a small cache in front, so repeat calls are instant.
type Reader struct {
	Exe     string                    // the Blaze binary (helper); "" = os.Executable
	Skip    int                       // our own pid: Blaze's windows are never read
	OnRead  func(on bool, how string) // the privacy indicator: on while the screen is read
	MaxText int                       // cap of the encoding (default 8000)

	mu      sync.Mutex
	last    *Snapshot
	sent    map[string]uint64 // per conversation: hash of the text the model last saw
	sentTx  map[string]string
	busy    sync.Mutex // one helper at a time
	shallow bool       // the deep text read crashed the helper once: skip it from now on
}

func NewReader() *Reader {
	return &Reader{Skip: os.Getpid(), sent: map[string]uint64{}, sentTx: map[string]string{}}
}

func (r *Reader) indicate(on bool, how string) {
	if r.OnRead != nil {
		r.OnRead(on, how)
	}
}

// Last is the cached snapshot, if any (never reads).
func (r *Reader) Last() *Snapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.last
}

// Front is the frontmost window that isn't Blaze (cheap, in-process).
func (r *Reader) Front() (Win, error) { return probe(r.Skip, 0) }

// Read returns the frontmost window as compact text. A cached snapshot of the same
// window (same id and title, same thumbnail where one is cheap) younger than ttl is
// returned as is.
func (r *Reader) Read(ctx context.Context, ttl time.Duration) (*Snapshot, bool, error) {
	w, err := probe(r.Skip, 0)
	if err != nil {
		return nil, false, err
	}
	th, hasThumb := thumb(w)
	r.mu.Lock()
	last := r.last
	r.mu.Unlock()
	if Fresh(last, w, th, hasThumb, ttl, time.Now()) {
		return last, true, nil
	}
	r.busy.Lock()
	defer r.busy.Unlock()
	r.indicate(true, "text")
	defer r.indicate(false, "text")
	var t Tree
	args := []string{"tree", "--skip", strconv.Itoa(r.Skip), "--max", "700", "--win", strconv.FormatUint(w.ID, 10)}
	if r.shallow {
		args = append(args, "--shallow")
	}
	err = helper(ctx, r.Exe, 6*time.Second, &t, args...)
	if err != nil && !r.shallow && ctx.Err() == nil {
		// A provider crashed or hung the helper: read again without the deep text reads.
		r.shallow = true
		t = Tree{}
		err = helper(ctx, r.Exe, 6*time.Second, &t, append(args, "--shallow")...)
	}
	s := &Snapshot{Win: w, At: time.Now(), Thumb: th, key: w.Key()}
	if err != nil {
		s.Err = err.Error()
	} else {
		if t.Win.Title != "" || t.Win.App != "" {
			s.Win = t.Win
		}
		s.Win.Fullscreen = s.Win.Fullscreen || w.Fullscreen
		s.Source, s.Ms, s.Nodes = t.Source, t.Ms, len(t.Nodes)
		s.Text = Encode(&t, r.MaxText)
		if t.Capped {
			s.Text += "(the window is large; only the first part was read)\n"
		}
		for _, n := range t.Nodes {
			if n.Secret && n.Box != nil {
				s.Secrets = append(s.Secrets, *n.Box)
			}
		}
		if t.Err != "" {
			s.Err = t.Err
		}
	}
	h := fnv.New64a()
	h.Write([]byte(s.Text))
	s.Hash = h.Sum64()
	r.mu.Lock()
	r.last = s
	r.mu.Unlock()
	if s.Err != "" && s.Text == "" {
		return s, false, errors.New(s.Err)
	}
	return s, false, nil
}

// ForModel returns what the model should see for conversation conv: the full text the
// first time, "unchanged" or a diff after that (full again when most of it changed).
func (r *Reader) ForModel(conv string, s *Snapshot, full bool) string {
	r.mu.Lock()
	prevHash, seen := r.sent[conv]
	prev := r.sentTx[conv]
	r.sent[conv] = s.Hash
	r.sentTx[conv] = s.Text
	if len(r.sent) > 50 {
		for k := range r.sent {
			if k != conv {
				delete(r.sent, k)
				delete(r.sentTx, k)
			}
		}
	}
	r.mu.Unlock()
	head := fmt.Sprintf("[read via %s in %dms", orStr(s.Source, "accessibility"), s.Ms)
	if age := s.Age(); age > time.Second {
		head += fmt.Sprintf(", cached %ds ago", int(age.Seconds()))
	}
	head += "]\n"
	if !full && seen && prevHash == s.Hash {
		return head + "Nothing changed on screen since you last read it (" + firstLine(s.Text) + ")."
	}
	if !full && seen && sameWindow(prev, s.Text) {
		if d, ok := Diff(prev, s.Text); ok && d != "" {
			return head + firstLine(s.Text) + "\nOnly part of the window changed since your last read. " + d
		}
	}
	return head + s.Text
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func sameWindow(a, b string) bool { return a != "" && firstLine(a) == firstLine(b) }

// LookOpts says what to photograph.
type LookOpts struct {
	Full   bool   // the whole screen, not just the window
	Region string // "x,y,w,h" inside the window (or the screen with Full)
	MaxPx  int    // longest side, default 1280
	Q      int    // JPEG quality, default 70
}

// Look takes a downscaled screenshot of the frontmost window (or the screen), with
// password fields blurred where the accessibility tree found them.
func (r *Reader) Look(ctx context.Context, o LookOpts) (*Shot, error) {
	if o.MaxPx <= 0 {
		o.MaxPx = 1280
	}
	if o.Q <= 0 {
		o.Q = 70
	}
	var blur []string
	if s := r.Last(); s != nil && time.Since(s.At) < 30*time.Second {
		for _, b := range s.Secrets {
			blur = append(blur, b.String())
		}
	}
	r.busy.Lock()
	defer r.busy.Unlock()
	r.indicate(true, "pixels")
	defer r.indicate(false, "pixels")
	args := []string{"shot", "--skip", strconv.Itoa(r.Skip), "--px", strconv.Itoa(o.MaxPx), "--q", strconv.Itoa(o.Q)}
	if o.Full {
		args = append(args, "--full")
	}
	if o.Region != "" {
		args = append(args, "--region", o.Region)
	}
	if len(blur) > 0 {
		args = append(args, "--blur", strings.Join(blur, ";"))
	}
	var s Shot
	// A desktop portal may show a permission prompt the first time: give it time.
	if err := helper(ctx, r.Exe, 40*time.Second, &s, args...); err != nil {
		return nil, err
	}
	if s.Err != "" {
		return &s, errors.New(s.Err)
	}
	return &s, nil
}

// Idle is how long the user hasn't touched keyboard or mouse; -1 when unknown.
func Idle() time.Duration {
	s := IdleSeconds()
	if s < 0 {
		return -1
	}
	return time.Duration(s) * time.Second
}

// Caps runs the capability check in the helper (it may load accessibility libraries).
func (r *Reader) Caps(ctx context.Context) Caps {
	var c Caps
	if err := helper(ctx, r.Exe, 8*time.Second, &c, "caps"); err != nil {
		c.TextNote = err.Error()
		c.PixelsNote = err.Error()
	}
	return c
}

// BlazeInFront is true when Blaze's own window is the one in front.
func (r *Reader) BlazeInFront() bool {
	w, err := probe(0, 0)
	return err == nil && w.PID == r.Skip
}
