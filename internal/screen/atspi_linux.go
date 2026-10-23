//go:build linux

package screen

// AT-SPI over D-Bus (pure Go, godbus). The accessibility bus is separate from the
// session bus; org.a11y.Bus tells us where it is.

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/godbus/dbus/v5"
)

type aref struct {
	Name string
	Path dbus.ObjectPath
}

const (
	stActive  = 1
	stFocused = 12
	stShowing = 25
)

var rootRef = aref{"org.a11y.atspi.Registry", "/org/a11y/atspi/accessible/root"}

func a11yBus() (*dbus.Conn, error) {
	sess, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	obj := sess.Object("org.a11y.Bus", "/org/a11y/bus")
	// Screen reading is on: make sure apps expose their trees (affects apps opened from now on).
	if v, err := obj.GetProperty("org.a11y.Status.IsEnabled"); err == nil {
		if b, _ := v.Value().(bool); !b {
			_ = obj.SetProperty("org.a11y.Status.IsEnabled", dbus.MakeVariant(true))
		}
	}
	var addr string
	if err := obj.Call("org.a11y.Bus.GetAddress", 0).Store(&addr); err != nil {
		return nil, err
	}
	return dbus.Connect(addr)
}

func children(ctx context.Context, c *dbus.Conn, r aref) []aref {
	var out []aref
	_ = c.Object(r.Name, r.Path).CallWithContext(ctx, "org.a11y.atspi.Accessible.GetChildren", 0).Store(&out)
	return out
}

func states(ctx context.Context, c *dbus.Conn, r aref) uint64 {
	var s []uint32
	if c.Object(r.Name, r.Path).CallWithContext(ctx, "org.a11y.atspi.Accessible.GetState", 0).Store(&s) != nil || len(s) == 0 {
		return 0
	}
	v := uint64(s[0])
	if len(s) > 1 {
		v |= uint64(s[1]) << 32
	}
	return v
}

func has(st uint64, bit uint) bool { return st&(1<<bit) != 0 }

func propString(ctx context.Context, c *dbus.Conn, r aref, prop string) string {
	var v dbus.Variant
	if c.Object(r.Name, r.Path).CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, "org.a11y.atspi.Accessible", prop).Store(&v) != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

func roleName(ctx context.Context, c *dbus.Conn, r aref) string {
	var s string
	_ = c.Object(r.Name, r.Path).CallWithContext(ctx, "org.a11y.atspi.Accessible.GetRoleName", 0).Store(&s)
	return s
}

func appPID(ctx context.Context, c *dbus.Conn, bus string) int {
	var pid uint32
	if c.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.GetConnectionUnixProcessID", 0, bus).Store(&pid) != nil {
		return 0
	}
	return int(pid)
}

func extents(ctx context.Context, c *dbus.Conn, r aref) *Rect {
	var e struct{ X, Y, W, H int32 }
	if c.Object(r.Name, r.Path).CallWithContext(ctx, "org.a11y.atspi.Component.GetExtents", 0, uint32(0)).Store(&e) != nil {
		return nil
	}
	return &Rect{int(e.X), int(e.Y), int(e.W), int(e.H)}
}

func textOf(ctx context.Context, c *dbus.Conn, r aref) string {
	var s string
	_ = c.Object(r.Name, r.Path).CallWithContext(ctx, "org.a11y.atspi.Text.GetText", 0, int32(0), int32(-1)).Store(&s)
	return s
}

func atspiApps() (int, error) {
	c, err := a11yBus()
	if err != nil {
		return 0, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var out []aref
	if err := c.Object(rootRef.Name, rootRef.Path).CallWithContext(ctx, "org.a11y.atspi.Accessible.GetChildren", 0).Store(&out); err != nil {
		return 0, err
	}
	return len(out), nil
}

// findFrame picks the frame of window w (by pid and title), or the active one.
func findFrame(ctx context.Context, c *dbus.Conn, w Win, skip int) (aref, string, int, error) {
	apps := children(ctx, c, rootRef)
	type cand struct {
		app, frame aref
		appName    string
		pid        int
		score      int
	}
	var best *cand
	for _, app := range apps {
		pid := appPID(ctx, c, app.Name)
		if skip != 0 && pid == skip {
			continue
		}
		pidMatch := w.PID > 0 && pid == w.PID
		if w.PID > 0 && !pidMatch && best != nil && best.score >= 2 {
			continue
		}
		for _, f := range children(ctx, c, app) {
			st := states(ctx, c, f)
			name := ""
			score := 0
			if pidMatch {
				score += 2
				name = propString(ctx, c, f, "Name")
				if w.Title != "" && name == w.Title {
					score += 2
				}
			}
			if has(st, stActive) {
				score++
			}
			if !has(st, stShowing) && score < 2 {
				continue
			}
			if best == nil || score > best.score {
				best = &cand{app: app, frame: f, pid: pid, score: score}
			}
		}
		if ctx.Err() != nil {
			break
		}
	}
	if best == nil || best.score == 0 {
		return aref{}, "", 0, errors.New("no readable window: the app may not support accessibility (Chrome/Electron need it turned on)")
	}
	return best.frame, propString(ctx, c, best.app, "Name"), best.pid, nil
}

func atspiActive(skip int) (Win, error) {
	c, err := a11yBus()
	if err != nil {
		return Win{}, err
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	f, app, pid, err := findFrame(ctx, c, Win{}, skip)
	if err != nil {
		return Win{}, err
	}
	w := Win{PID: pid, App: app, Title: propString(ctx, c, f, "Name")}
	if e := extents(ctx, c, f); e != nil {
		w.X, w.Y, w.W, w.H = e.X, e.Y, e.W, e.H
	}
	return w, nil
}

type anode struct {
	f    Flat
	kids []*anode
}

func tree(w Win, max int) ([]Flat, string, error) {
	src := "AT-SPI"
	c, err := a11yBus()
	if err != nil {
		return nil, src, errors.New("AT-SPI isn't reachable: " + err.Error())
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3500*time.Millisecond)
	defer cancel()
	frame, _, _, err := findFrame(ctx, c, w, 0)
	if err != nil {
		return nil, src, err
	}
	var count int64
	sem := make(chan struct{}, 12)
	deepLeft := int64(3)
	var visit func(r aref, depth int) *anode
	visit = func(r aref, depth int) *anode {
		if atomic.AddInt64(&count, 1) > int64(max) || ctx.Err() != nil {
			return nil
		}
		sem <- struct{}{}
		role := roleName(ctx, c, r)
		st := states(ctx, c, r)
		n := &anode{f: Flat{D: depth, Role: role, Name: propString(ctx, c, r, "Name"), Focus: has(st, stFocused)}}
		switch {
		case role == "password text":
			n.f.Secret = true
			n.f.Box = extents(ctx, c, r)
		case role == "terminal" && !shallow && atomic.AddInt64(&deepLeft, -1) >= 0:
			n.f.Value = textOf(ctx, c, r)
		case role == "entry" || role == "text" || role == "spin button" || role == "combo box":
			n.f.Value = clean(textOf(ctx, c, r), 300)
		}
		var kids []aref
		if depth < maxDepth && (depth == 0 || has(st, stShowing)) && role != "terminal" {
			kids = children(ctx, c, r)
		}
		<-sem
		if len(kids) > 200 {
			kids = kids[:200]
		}
		n.kids = make([]*anode, len(kids))
		var wg sync.WaitGroup
		for i, k := range kids {
			wg.Add(1)
			go func(i int, k aref) {
				defer wg.Done()
				n.kids[i] = visit(k, depth+1)
			}(i, k)
		}
		wg.Wait()
		return n
	}
	root := visit(frame, 0)
	var out []Flat
	var flat func(n *anode)
	flat = func(n *anode) {
		if n == nil || len(out) >= max {
			return
		}
		out = append(out, n.f)
		for _, k := range n.kids {
			flat(k)
		}
	}
	flat(root)
	if len(out) == 0 {
		return nil, src, errors.New("the window has no readable text")
	}
	return out, src, nil
}
