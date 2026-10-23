package screen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// HelperMain is the "__screen" mode of the Blaze binary. It prints one JSON
// object (a Tree or a Shot) on stdout and exits.
//
//	__screen tree --skip <pid> [--max 600]
//	__screen shot --skip <pid> [--full] [--region x,y,w,h] [--px 1280] [--q 70] [--blur x,y,w,h;...]
//	__screen caps
func HelperMain(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: __screen tree|shot|caps")
		return 2
	}
	mode := args[0]
	fs := flag.NewFlagSet("screen", flag.ContinueOnError)
	skip := fs.Int("skip", 0, "pid whose windows are skipped (Blaze itself)")
	max := fs.Int("max", 600, "most nodes to read")
	full := fs.Bool("full", false, "the whole screen instead of the window")
	region := fs.String("region", "", "x,y,w,h inside the window (or screen with --full)")
	px := fs.Int("px", 1280, "longest side of the screenshot")
	q := fs.Int("q", 70, "JPEG quality")
	blur := fs.String("blur", "", "screen rects to blur, x,y,w,h;x,y,w,h")
	win := fs.Uint64("win", 0, "window id to read (0 = frontmost that isn't --skip)")
	fs.BoolVar(&shallow, "shallow", false, "skip text patterns (after a helper crash)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	helperInit()
	enc := json.NewEncoder(os.Stdout)
	start := time.Now()
	switch mode {
	case "caps":
		_ = enc.Encode(Capabilities())
	case "tree":
		t := Tree{}
		w, err := probe(*skip, *win)
		t.Win = w
		if err != nil {
			t.Err = err.Error()
		} else {
			nodes, src, err := tree(w, *max)
			t.Nodes, t.Source = nodes, src
			t.Capped = len(nodes) >= *max
			if err != nil {
				t.Err = err.Error()
			}
		}
		t.Ms = int(time.Since(start).Milliseconds())
		_ = enc.Encode(t)
	case "shot":
		s := Shot{}
		w, err := probe(*skip, *win)
		s.Win = w
		if err != nil && !*full {
			s.Err = err.Error()
			_ = enc.Encode(s)
			return 0
		}
		img, origin, src, err := capture(w, *full)
		s.Source = src
		if err != nil {
			s.Err = err.Error()
			_ = enc.Encode(s)
			return 0
		}
		var rects []Rect
		for _, p := range strings.Split(*blur, ";") {
			if r, ok := ParseRect(p); ok {
				// screen coordinates -> image coordinates
				r.X = int(float64(r.X-origin.X) * pixelScale)
				r.Y = int(float64(r.Y-origin.Y) * pixelScale)
				r.W = int(float64(r.W) * pixelScale)
				r.H = int(float64(r.H) * pixelScale)
				rects = append(rects, r)
			}
		}
		img = Blur(img, rects)
		if r, ok := ParseRect(*region); ok {
			r = Rect{int(float64(r.X) * pixelScale), int(float64(r.Y) * pixelScale), int(float64(r.W) * pixelScale), int(float64(r.H) * pixelScale)}
			img = Crop(img, r)
		}
		img = Fit(img, *px)
		b, err := JPEG(img, *q)
		if err != nil {
			s.Err = err.Error()
		}
		s.JPEG = b
		s.W, s.H = img.Bounds().Dx(), img.Bounds().Dy()
		s.Ms = int(time.Since(start).Milliseconds())
		_ = enc.Encode(s)
	default:
		return 2
	}
	return 0
}

// shallow skips the riskier deep text reads (terminal / editor contents).
var shallow bool

// helper runs the Blaze binary in "__screen" mode and decodes its answer into out.
func helper(ctx context.Context, exe string, timeout time.Duration, out any, args ...string) error {
	if exe == "" {
		e, err := os.Executable()
		if err != nil {
			return err
		}
		exe = e
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, append([]string{"__screen"}, args...)...)
	hideConsole(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return errors.New("the window took too long to answer (over " + timeout.String() + ")")
	}
	if err != nil && stdout.Len() == 0 {
		msg := strings.TrimSpace(stderr.String())
		if len(msg) > 300 {
			msg = msg[len(msg)-300:]
		}
		if msg == "" {
			msg = err.Error()
		}
		return errors.New("screen helper failed: " + msg)
	}
	return json.Unmarshal(stdout.Bytes(), out)
}
