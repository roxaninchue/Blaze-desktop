//go:build linux && cgo

package screen

/*
#cgo LDFLAGS: -lX11
#include <stdlib.h>
#include <string.h>
#include <X11/Xlib.h>
#include <X11/Xatom.h>
#include <X11/Xutil.h>

static int blaze_x_err(Display *d, XErrorEvent *e) { return 0; }

static Display *blaze_dpy = NULL;

static Display* blaze_open(void) {
	if (!blaze_dpy) {
		XSetErrorHandler(blaze_x_err); // BadWindow from a closing window must not kill Blaze
		blaze_dpy = XOpenDisplay(NULL);
	}
	return blaze_dpy;
}

static unsigned long blaze_prop_window(Display *d, Window w, const char *name) {
	Atom a = XInternAtom(d, name, True), type; int fmt; unsigned long n = 0, after; unsigned char *data = NULL;
	if (a == None) return 0;
	if (XGetWindowProperty(d, w, a, 0, 1, False, AnyPropertyType, &type, &fmt, &n, &after, &data) != Success || !data) return 0;
	unsigned long v = n ? ((unsigned long*)data)[0] : 0;
	XFree(data);
	return v;
}

// blaze_stack returns the client list bottom-to-top; caller frees with XFree.
static unsigned long* blaze_stack(Display *d, unsigned long *count) {
	Atom a = XInternAtom(d, "_NET_CLIENT_LIST_STACKING", True), type; int fmt; unsigned long after; unsigned char *data = NULL;
	*count = 0;
	if (a == None) return NULL;
	if (XGetWindowProperty(d, DefaultRootWindow(d), a, 0, 4096, False, XA_WINDOW, &type, &fmt, count, &after, &data) != Success) return NULL;
	return (unsigned long*)data;
}

static char* blaze_title(Display *d, Window w) {
	Atom net = XInternAtom(d, "_NET_WM_NAME", True), utf8 = XInternAtom(d, "UTF8_STRING", True), type; int fmt; unsigned long n = 0, after; unsigned char *data = NULL;
	if (net != None && XGetWindowProperty(d, w, net, 0, 1024, False, utf8, &type, &fmt, &n, &after, &data) == Success && data) {
		char *s = strndup((char*)data, n); XFree(data); return s;
	}
	char *name = NULL;
	if (XFetchName(d, w, &name) && name) { char *s = strdup(name); XFree(name); return s; }
	return strdup("");
}

static char* blaze_class(Display *d, Window w) {
	XClassHint h; memset(&h, 0, sizeof h);
	if (XGetClassHint(d, w, &h)) {
		char *s = strdup(h.res_class ? h.res_class : (h.res_name ? h.res_name : ""));
		if (h.res_name) XFree(h.res_name);
		if (h.res_class) XFree(h.res_class);
		return s;
	}
	return strdup("");
}

static int blaze_geom(Display *d, Window w, int *x, int *y, int *wd, int *ht) {
	XWindowAttributes at;
	if (!XGetWindowAttributes(d, w, &at)) return 0;
	Window child;
	XTranslateCoordinates(d, w, DefaultRootWindow(d), 0, 0, x, y, &child);
	*wd = at.width; *ht = at.height;
	return at.map_state == IsViewable;
}

static int blaze_fullscreen(Display *d, Window w) {
	Atom st = XInternAtom(d, "_NET_WM_STATE", True), fs = XInternAtom(d, "_NET_WM_STATE_FULLSCREEN", True), type; int fmt; unsigned long n = 0, after; unsigned char *data = NULL;
	if (st == None || fs == None) return 0;
	if (XGetWindowProperty(d, w, st, 0, 64, False, XA_ATOM, &type, &fmt, &n, &after, &data) != Success || !data) return 0;
	int r = 0;
	for (unsigned long i = 0; i < n; i++) if (((Atom*)data)[i] == fs) r = 1;
	XFree(data);
	return r;
}

static void blaze_screen_size(Display *d, int *w, int *h) {
	*w = DisplayWidth(d, DefaultScreen(d)); *h = DisplayHeight(d, DefaultScreen(d));
}

// blaze_grab copies a root-window area as 32-bit BGRX rows into out (w*h*4 bytes).
static int blaze_grab(Display *d, int x, int y, int w, int h, unsigned char *out) {
	XImage *img = XGetImage(d, DefaultRootWindow(d), x, y, w, h, AllPlanes, ZPixmap);
	if (!img) return 0;
	for (int j = 0; j < h; j++) {
		for (int i = 0; i < w; i++) {
			unsigned long p = (img->bits_per_pixel == 32) ? ((unsigned int*)(img->data + j * img->bytes_per_line))[i] : XGetPixel(img, i, j);
			unsigned char *o = out + (j * w + i) * 4;
			o[0] = (p & img->red_mask) >> 16; o[1] = (p & img->green_mask) >> 8; o[2] = (p & img->blue_mask); o[3] = 255;
		}
	}
	XDestroyImage(img);
	return 1;
}
*/
import "C"

import (
	"errors"
	"image"
	"sync"
	"unsafe"
)

const x11Built = true

var xmu sync.Mutex

func x11Win(skip int, id uint64) (Win, error) {
	xmu.Lock()
	defer xmu.Unlock()
	d := C.blaze_open()
	if d == nil {
		return Win{}, errors.New("can't open the X display")
	}
	pidName := C.CString("_NET_WM_PID")
	defer C.free(unsafe.Pointer(pidName))
	pick := func(w C.Window) (Win, bool) {
		var x, y, wd, ht C.int
		if C.blaze_geom(d, w, &x, &y, &wd, &ht) == 0 || wd < 40 || ht < 40 {
			return Win{}, false
		}
		pid := int(C.blaze_prop_window(d, w, pidName))
		if skip != 0 && pid == skip {
			return Win{}, false
		}
		t := C.blaze_title(d, w)
		cl := C.blaze_class(d, w)
		defer C.free(unsafe.Pointer(t))
		defer C.free(unsafe.Pointer(cl))
		win := Win{ID: uint64(w), PID: pid, App: C.GoString(cl), Title: C.GoString(t), X: int(x), Y: int(y), W: int(wd), H: int(ht)}
		var sw, sh C.int
		C.blaze_screen_size(d, &sw, &sh)
		win.Fullscreen = C.blaze_fullscreen(d, w) != 0 || (int(x) <= 0 && int(y) <= 0 && int(wd) >= int(sw) && int(ht) >= int(sh))
		return win, true
	}
	if id != 0 {
		if w, ok := pick(C.Window(id)); ok {
			return w, nil
		}
	}
	root := C.XDefaultRootWindow(d)
	cname := C.CString("_NET_ACTIVE_WINDOW")
	defer C.free(unsafe.Pointer(cname))
	if act := C.blaze_prop_window(d, root, cname); act != 0 {
		if w, ok := pick(C.Window(act)); ok {
			return w, nil
		}
	}
	var n C.ulong
	list := C.blaze_stack(d, &n)
	if list != nil {
		defer C.XFree(unsafe.Pointer(list))
		wins := unsafe.Slice((*C.ulong)(unsafe.Pointer(list)), int(n))
		for i := len(wins) - 1; i >= 0; i-- {
			if w, ok := pick(C.Window(wins[i])); ok {
				return w, nil
			}
		}
	}
	return Win{}, errors.New("no window is open")
}

func x11Capture(w Win, full bool) (image.Image, image.Point, error) {
	xmu.Lock()
	defer xmu.Unlock()
	d := C.blaze_open()
	if d == nil {
		return nil, image.Point{}, errors.New("can't open the X display")
	}
	var sw, sh C.int
	C.blaze_screen_size(d, &sw, &sh)
	x, y, wd, ht := 0, 0, int(sw), int(sh)
	if !full && w.W > 0 {
		x, y, wd, ht = w.X, w.Y, w.W, w.H
		// clip to the screen
		if x < 0 {
			wd += x
			x = 0
		}
		if y < 0 {
			ht += y
			y = 0
		}
		if x+wd > int(sw) {
			wd = int(sw) - x
		}
		if y+ht > int(sh) {
			ht = int(sh) - y
		}
	}
	if wd <= 0 || ht <= 0 {
		return nil, image.Point{}, errors.New("the window is off screen")
	}
	img := image.NewRGBA(image.Rect(0, 0, wd, ht))
	if C.blaze_grab(d, C.int(x), C.int(y), C.int(wd), C.int(ht), (*C.uchar)(unsafe.Pointer(&img.Pix[0]))) == 0 {
		return nil, image.Point{}, errors.New("XGetImage failed")
	}
	return img, image.Pt(x, y), nil
}
