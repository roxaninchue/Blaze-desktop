//go:build darwin

package screen

/*
#cgo CFLAGS: -Wno-deprecated-declarations -Wno-unguarded-availability-new
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <stdio.h>
#include <CoreGraphics/CoreGraphics.h>
#include <ApplicationServices/ApplicationServices.h>
#include <CoreFoundation/CoreFoundation.h>

typedef struct { unsigned int id; int pid; int x, y, w, h; int full; char app[256]; char title[512]; } blaze_win;

static void blaze_cfstr(CFTypeRef s, char *out, int n) {
	out[0] = 0;
	if (s && CFGetTypeID(s) == CFStringGetTypeID()) CFStringGetCString((CFStringRef)s, out, n, kCFStringEncodingUTF8);
}

// blaze_front fills w with the frontmost normal window not owned by skip (or window id want). 1 = found.
static int blaze_front(int skip, unsigned int want, blaze_win *w) {
	CFArrayRef list = CGWindowListCopyWindowInfo(kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (!list) return 0;
	CGRect main = CGDisplayBounds(CGMainDisplayID());
	int found = 0;
	for (int pass = 0; pass < 2 && !found; pass++) {
		if (pass == 0 && want == 0) continue;
		for (CFIndex i = 0; i < CFArrayGetCount(list); i++) {
			CFDictionaryRef d = (CFDictionaryRef)CFArrayGetValueAtIndex(list, i);
			int layer = 0, pid = 0; unsigned int wid = 0; double alpha = 1;
			CFNumberRef n;
			if ((n = CFDictionaryGetValue(d, kCGWindowLayer))) CFNumberGetValue(n, kCFNumberIntType, &layer);
			if ((n = CFDictionaryGetValue(d, kCGWindowOwnerPID))) CFNumberGetValue(n, kCFNumberIntType, &pid);
			if ((n = CFDictionaryGetValue(d, kCGWindowNumber))) CFNumberGetValue(n, kCFNumberIntType, &wid);
			if ((n = CFDictionaryGetValue(d, kCGWindowAlpha))) CFNumberGetValue(n, kCFNumberDoubleType, &alpha);
			if (pass == 0 && wid != want) continue;
			if (layer != 0 || alpha <= 0 || (skip && pid == skip)) continue;
			CGRect r = CGRectZero;
			CFDictionaryRef b = CFDictionaryGetValue(d, kCGWindowBounds);
			if (b) CGRectMakeWithDictionaryRepresentation(b, &r);
			if (r.size.width < 60 || r.size.height < 40) continue;
			w->id = wid; w->pid = pid;
			w->x = (int)r.origin.x; w->y = (int)r.origin.y; w->w = (int)r.size.width; w->h = (int)r.size.height;
			w->full = CGRectContainsRect(r, main) ? 1 : 0;
			blaze_cfstr(CFDictionaryGetValue(d, kCGWindowOwnerName), w->app, sizeof w->app);
			blaze_cfstr(CFDictionaryGetValue(d, kCGWindowName), w->title, sizeof w->title);
			found = 1;
			break;
		}
	}
	CFRelease(list);
	return found;
}

static double blaze_idle(void) {
	return CGEventSourceSecondsSinceLastEventType(kCGEventSourceStateHIDSystemState, kCGAnyInputEventType);
}

static int blaze_ax_trusted(void) { return AXIsProcessTrusted() ? 1 : 0; }

static int blaze_screen_ok(void) {
	if (__builtin_available(macOS 10.15, *)) return CGPreflightScreenCaptureAccess() ? 1 : 0;
	return 1;
}

static void blaze_screen_ask(void) {
	if (__builtin_available(macOS 10.15, *)) CGRequestScreenCaptureAccess();
}

// ---------- AX tree ----------

typedef struct { char *buf; size_t len, cap; int count, max; } blaze_out;

static void blaze_put(blaze_out *o, const char *s) {
	size_t n = strlen(s);
	if (o->len + n + 1 > o->cap) {
		size_t c = (o->cap ? o->cap * 2 : 65536);
		while (c < o->len + n + 1) c *= 2;
		char *nb = realloc(o->buf, c);
		if (!nb) return;
		o->buf = nb; o->cap = c;
	}
	memcpy(o->buf + o->len, s, n); o->len += n; o->buf[o->len] = 0;
}

// blaze_attr copies an attribute as text: tabs and newlines become spaces (newlines
// become \x1f when keepLines, for terminals). Long text keeps its end when keepLines.
static void blaze_attr(AXUIElementRef el, CFStringRef attr, char *out, int n, int keepLines) {
	out[0] = 0;
	CFTypeRef v = NULL;
	if (AXUIElementCopyAttributeValue(el, attr, &v) != kAXErrorSuccess || !v) return;
	if (CFGetTypeID(v) == CFStringGetTypeID()) {
		CFStringRef str = (CFStringRef)v;
		CFIndex len = CFStringGetLength(str), maxc = (n - 1) / 4;
		if (len > maxc) {
			CFStringRef sub = CFStringCreateWithSubstring(NULL, str, CFRangeMake(keepLines ? len - maxc : 0, maxc));
			if (sub) { CFStringGetCString(sub, out, n, kCFStringEncodingUTF8); CFRelease(sub); }
		} else {
			CFStringGetCString(str, out, n, kCFStringEncodingUTF8);
		}
		for (char *p = out; *p; p++) {
			if (*p == '\t' || *p == '\r') *p = ' ';
			else if (*p == '\n') *p = keepLines ? '\x1f' : ' ';
		}
	} else if (CFGetTypeID(v) == CFNumberGetTypeID()) {
		double d = 0; CFNumberGetValue((CFNumberRef)v, kCFNumberDoubleType, &d);
		snprintf(out, n, "%g", d);
	}
	CFRelease(v);
}

static void blaze_walk(AXUIElementRef el, int depth, blaze_out *o) {
	if (o->count >= o->max || depth > 40) return;
	o->count++;
	// static: each is used before recursing into the children
	static char role[128], sub[128], title[1024], val[8192], line[12000];
	blaze_attr(el, kAXRoleAttribute, role, sizeof role, 0);
	blaze_attr(el, kAXSubroleAttribute, sub, sizeof sub, 0);
	blaze_attr(el, kAXTitleAttribute, title, sizeof title, 0);
	if (!title[0]) blaze_attr(el, kAXDescriptionAttribute, title, sizeof title, 0);
	int secret = strcmp(sub, "AXSecureTextField") == 0;
	val[0] = 0;
	char box[96] = "";
	if (secret) {
		CFTypeRef p = NULL, s = NULL; CGPoint pt = {0, 0}; CGSize sz = {0, 0};
		if (AXUIElementCopyAttributeValue(el, kAXPositionAttribute, &p) == kAXErrorSuccess && p) { AXValueGetValue((AXValueRef)p, kAXValueCGPointType, &pt); CFRelease(p); }
		if (AXUIElementCopyAttributeValue(el, kAXSizeAttribute, &s) == kAXErrorSuccess && s) { AXValueGetValue((AXValueRef)s, kAXValueCGSizeType, &sz); CFRelease(s); }
		snprintf(box, sizeof box, "%d,%d,%d,%d", (int)pt.x, (int)pt.y, (int)sz.width, (int)sz.height);
	} else {
		int big = strcmp(role, "AXTextArea") == 0;
		blaze_attr(el, kAXValueAttribute, val, big ? (int)sizeof val : 512, big);
	}
	CFTypeRef foc = NULL; int focused = 0;
	if (AXUIElementCopyAttributeValue(el, kAXFocusedAttribute, &foc) == kAXErrorSuccess && foc) {
		if (CFGetTypeID(foc) == CFBooleanGetTypeID()) focused = CFBooleanGetValue((CFBooleanRef)foc) ? 1 : 0;
		CFRelease(foc);
	}
	snprintf(line, sizeof line, "%d\t%s\t%s\t%s\t%d%d\t%s\n", depth, role, title, val, secret, focused, box);
	blaze_put(o, line);
	CFTypeRef kids = NULL;
	if (AXUIElementCopyAttributeValue(el, kAXChildrenAttribute, &kids) != kAXErrorSuccess || !kids) return;
	if (CFGetTypeID(kids) == CFArrayGetTypeID()) {
		CFIndex n = CFArrayGetCount((CFArrayRef)kids);
		if (n > 300) n = 300;
		for (CFIndex i = 0; i < n && o->count < o->max; i++)
			blaze_walk((AXUIElementRef)CFArrayGetValueAtIndex((CFArrayRef)kids, i), depth + 1, o);
	}
	CFRelease(kids);
}

// blaze_ax_tree returns the window of pid (titled title, else the focused one) as
// lines "depth\trole\tname\tvalue\tSF\tbox"; free() it. err: 1 no permission, 2/3 no window.
static char* blaze_ax_tree(int pid, const char *title, int max, int *err) {
	*err = 0;
	if (!AXIsProcessTrusted()) { *err = 1; return NULL; }
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	if (!app) { *err = 2; return NULL; }
	AXUIElementSetMessagingTimeout(app, 0.8);
	AXUIElementRef win = NULL;
	CFTypeRef wins = NULL;
	if (title && title[0] && AXUIElementCopyAttributeValue(app, kAXWindowsAttribute, &wins) == kAXErrorSuccess && wins) {
		if (CFGetTypeID(wins) == CFArrayGetTypeID()) {
			char t[1024];
			for (CFIndex i = 0; i < CFArrayGetCount((CFArrayRef)wins) && !win; i++) {
				AXUIElementRef w = (AXUIElementRef)CFArrayGetValueAtIndex((CFArrayRef)wins, i);
				blaze_attr(w, kAXTitleAttribute, t, sizeof t, 0);
				if (strcmp(t, title) == 0) { win = w; CFRetain(win); }
			}
		}
		CFRelease(wins);
	}
	if (!win) AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, (CFTypeRef*)&win);
	if (!win) AXUIElementCopyAttributeValue(app, kAXMainWindowAttribute, (CFTypeRef*)&win);
	if (!win) { CFRelease(app); *err = 3; return NULL; }
	blaze_out o = {0};
	o.max = max;
	blaze_walk(win, 0, &o);
	CFRelease(win);
	CFRelease(app);
	return o.buf;
}

static char* blaze_win_title(int pid) {
	AXUIElementRef app = AXUIElementCreateApplication(pid);
	if (!app) return NULL;
	AXUIElementSetMessagingTimeout(app, 0.5);
	AXUIElementRef win = NULL;
	char *out = NULL;
	if (AXUIElementCopyAttributeValue(app, kAXFocusedWindowAttribute, (CFTypeRef*)&win) == kAXErrorSuccess && win) {
		out = malloc(1024);
		if (out) blaze_attr(win, kAXTitleAttribute, out, 1024, 0);
		CFRelease(win);
	}
	CFRelease(app);
	return out;
}
*/
import "C"

import (
	"errors"
	"image"
	_ "image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unsafe"
)

func helperInit()                {}
func hideConsole(cmd *exec.Cmd)  {}
func thumb(w Win) (uint64, bool) { return 0, false }

func probe(skip int, id uint64) (Win, error) {
	var cw C.blaze_win
	if C.blaze_front(C.int(skip), C.uint(id), &cw) == 0 {
		return Win{}, errors.New("no window is open")
	}
	w := Win{ID: uint64(cw.id), PID: int(cw.pid), App: C.GoString(&cw.app[0]), Title: C.GoString(&cw.title[0]),
		X: int(cw.x), Y: int(cw.y), W: int(cw.w), H: int(cw.h), Fullscreen: cw.full != 0}
	if w.Title == "" && C.blaze_ax_trusted() != 0 {
		// Without Screen Recording, macOS hides window titles; Accessibility still has them.
		if t := C.blaze_win_title(C.int(w.PID)); t != nil {
			w.Title = C.GoString(t)
			C.free(unsafe.Pointer(t))
		}
	}
	return w, nil
}

func tree(w Win, max int) ([]Flat, string, error) {
	src := "Accessibility (AX)"
	ct := C.CString(w.Title)
	defer C.free(unsafe.Pointer(ct))
	var cerr C.int
	buf := C.blaze_ax_tree(C.int(w.PID), ct, C.int(max), &cerr)
	switch cerr {
	case 1:
		return nil, src, errors.New("Blaze needs the Accessibility permission to read windows (System Settings › Privacy & Security › Accessibility)")
	case 2, 3:
		return nil, src, errors.New("the window didn't answer accessibility requests")
	}
	if buf == nil {
		return nil, src, errors.New("the window has no readable text")
	}
	text := C.GoString(buf)
	C.free(unsafe.Pointer(buf))
	var out []Flat
	for _, l := range strings.Split(text, "\n") {
		p := strings.SplitN(l, "\t", 6)
		if len(p) < 5 {
			continue
		}
		d, _ := strconv.Atoi(p[0])
		f := Flat{D: d, Role: p[1], Name: p[2], Value: strings.ReplaceAll(p[3], "\x1f", "\n")}
		if len(p[4]) == 2 {
			f.Secret, f.Focus = p[4][0] == '1', p[4][1] == '1'
		}
		if f.Secret {
			f.Value = ""
			if len(p) > 5 {
				if r, ok := ParseRect(p[5]); ok {
					f.Box = &r
				}
			}
		}
		out = append(out, f)
	}
	return out, src, nil
}

func capture(w Win, full bool) (image.Image, image.Point, string, error) {
	if C.blaze_screen_ok() == 0 {
		return nil, image.Point{}, "screencapture", errors.New("Blaze needs the Screen Recording permission to take screenshots (System Settings › Privacy & Security › Screen Recording)")
	}
	tmp := filepath.Join(os.TempDir(), "blaze-shot-"+strconv.Itoa(os.Getpid())+".jpg")
	defer os.Remove(tmp)
	args := []string{"-x", "-t", "jpg"}
	origin := image.Pt(0, 0)
	if !full && w.ID != 0 {
		args = append(args, "-o", "-l"+strconv.FormatUint(w.ID, 10))
		origin = image.Pt(w.X, w.Y)
	} else {
		args = append(args, "-m")
	}
	cmd := exec.Command("/usr/sbin/screencapture", append(args, tmp)...)
	if err := cmd.Start(); err != nil {
		return nil, image.Point{}, "screencapture", err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return nil, image.Point{}, "screencapture", err
		}
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		return nil, image.Point{}, "screencapture", errors.New("screencapture took too long")
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nil, image.Point{}, "screencapture", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, image.Point{}, "screencapture", err
	}
	// Retina: the picture is in pixels, the window in points.
	if !full && w.W > 0 && img.Bounds().Dx() > w.W*3/2 {
		pixelScale = float64(img.Bounds().Dx()) / float64(w.W)
	}
	return img, origin, "screencapture", nil
}

func IdleSeconds() int { return int(C.blaze_idle()) }

// AskScreenRecording shows macOS's own Screen Recording prompt (first time only).
func AskScreenRecording() { C.blaze_screen_ask() }

func Capabilities() Caps {
	c := Caps{TextVia: "Accessibility (AX)", PixelsVia: "screencapture",
		Permission: "x-apple.systempreferences:com.apple.preference.security?Privacy_ScreenCapture",
		TextPerm:   "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"}
	if C.blaze_ax_trusted() != 0 {
		c.Text = true
	} else {
		c.TextNote = "Allow Blaze in System Settings › Privacy & Security › Accessibility"
	}
	if C.blaze_screen_ok() != 0 {
		c.Pixels = true
	} else {
		c.PixelsNote = "Allow Blaze in System Settings › Privacy & Security › Screen Recording, then reopen Blaze"
	}
	return c
}
