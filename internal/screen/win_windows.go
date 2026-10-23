//go:build windows

package screen

import (
	"errors"
	"image"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32   = windows.NewLazySystemDLL("user32.dll")
	gdi32    = windows.NewLazySystemDLL("gdi32.dll")
	shell32  = windows.NewLazySystemDLL("shell32.dll")
	dwmapi   = windows.NewLazySystemDLL("dwmapi.dll")
	kernel32 = windows.NewLazySystemDLL("kernel32.dll")

	pGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	pGetWindow                = user32.NewProc("GetWindow")
	pGetTopWindow             = user32.NewProc("GetTopWindow")
	pIsWindow                 = user32.NewProc("IsWindow")
	pIsWindowVisible          = user32.NewProc("IsWindowVisible")
	pIsIconic                 = user32.NewProc("IsIconic")
	pGetWindowTextW           = user32.NewProc("GetWindowTextW")
	pGetClassNameW            = user32.NewProc("GetClassNameW")
	pGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")
	pGetWindowRect            = user32.NewProc("GetWindowRect")
	pGetWindowLongPtrW        = user32.NewProc("GetWindowLongPtrW")
	pPrintWindow              = user32.NewProc("PrintWindow")
	pGetDC                    = user32.NewProc("GetDC")
	pReleaseDC                = user32.NewProc("ReleaseDC")
	pGetLastInputInfo         = user32.NewProc("GetLastInputInfo")
	pGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	pSetDpiAwarenessContext   = user32.NewProc("SetProcessDpiAwarenessContext")
	pMonitorFromWindow        = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")

	pCreateCompatibleDC     = gdi32.NewProc("CreateCompatibleDC")
	pCreateCompatibleBitmap = gdi32.NewProc("CreateCompatibleBitmap")
	pSelectObject           = gdi32.NewProc("SelectObject")
	pBitBlt                 = gdi32.NewProc("BitBlt")
	pStretchBlt             = gdi32.NewProc("StretchBlt")
	pSetStretchBltMode      = gdi32.NewProc("SetStretchBltMode")
	pGetDIBits              = gdi32.NewProc("GetDIBits")
	pDeleteObject           = gdi32.NewProc("DeleteObject")
	pDeleteDC               = gdi32.NewProc("DeleteDC")

	pSHQueryUserNotificationState = shell32.NewProc("SHQueryUserNotificationState")
	pDwmGetWindowAttribute        = dwmapi.NewProc("DwmGetWindowAttribute")
	pGetTickCount                 = kernel32.NewProc("GetTickCount")
)

type rect32 struct{ L, T, R, B int32 }

type monitorInfo struct {
	Size    uint32
	Monitor rect32
	Work    rect32
	Flags   uint32
}

type bmiHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

const (
	gwHwndNext       = 2
	gwlExStyle       = ^uintptr(19) // -20
	wsExToolWindow   = 0x80
	dwmwaExtFrame    = 9
	dwmwaCloaked     = 14
	srcCopy          = 0x00CC0020
	captureBlt       = 0x40000000
	pwRenderFull     = 2
	halftone         = 4
	monitorNearest   = 2
	quNsBusy         = 2
	quNsD3DFull      = 3
	quNsPresentation = 4
)

func helperInit() {
	if pSetDpiAwarenessContext.Find() == nil {
		pSetDpiAwarenessContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	}
}

func hideConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}

func winText(h uintptr) string {
	buf := make([]uint16, 512)
	n, _, _ := pGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 512)
	return windows.UTF16ToString(buf[:n])
}

func winClass(h uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := pGetClassNameW.Call(h, uintptr(unsafe.Pointer(&buf[0])), 256)
	return windows.UTF16ToString(buf[:n])
}

func winPID(h uintptr) int {
	var pid uint32
	pGetWindowThreadProcessId.Call(h, uintptr(unsafe.Pointer(&pid)))
	return int(pid)
}

func cloaked(h uintptr) bool {
	if pDwmGetWindowAttribute.Find() != nil {
		return false
	}
	var v uint32
	r, _, _ := pDwmGetWindowAttribute.Call(h, dwmwaCloaked, uintptr(unsafe.Pointer(&v)), 4)
	return r == 0 && v != 0
}

func candidate(h uintptr, skip int) bool {
	if h == 0 {
		return false
	}
	if v, _, _ := pIsWindowVisible.Call(h); v == 0 {
		return false
	}
	if v, _, _ := pIsIconic.Call(h); v != 0 {
		return false
	}
	ex, _, _ := pGetWindowLongPtrW.Call(h, gwlExStyle)
	if ex&wsExToolWindow != 0 || cloaked(h) {
		return false
	}
	if skip != 0 && winPID(h) == skip {
		return false
	}
	switch winClass(h) {
	case "Progman", "WorkerW", "Shell_TrayWnd", "Shell_SecondaryTrayWnd", "Windows.UI.Core.CoreWindow":
		return false
	}
	return strings.TrimSpace(winText(h)) != ""
}

func procName(pid int) string {
	hp, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(hp)
	buf := make([]uint16, 1024)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(hp, 0, &buf[0], &n) != nil {
		return ""
	}
	base := filepath.Base(windows.UTF16ToString(buf[:n]))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func winRect(h uintptr) rect32 {
	var r rect32
	if pDwmGetWindowAttribute.Find() == nil {
		if ok, _, _ := pDwmGetWindowAttribute.Call(h, dwmwaExtFrame, uintptr(unsafe.Pointer(&r)), unsafe.Sizeof(r)); ok == 0 && r.R > r.L {
			return r
		}
	}
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

func probe(skip int, id uint64) (Win, error) {
	h := uintptr(0)
	if id != 0 {
		if ok, _, _ := pIsWindow.Call(uintptr(id)); ok != 0 && candidate(uintptr(id), skip) {
			h = uintptr(id)
		}
	}
	fg, _, _ := pGetForegroundWindow.Call()
	if h == 0 && candidate(fg, skip) {
		h = fg
	}
	if h == 0 {
		w, _, _ := pGetTopWindow.Call(0)
		for i := 0; w != 0 && i < 2000; i++ {
			if candidate(w, skip) {
				h = w
				break
			}
			w, _, _ = pGetWindow.Call(w, gwHwndNext)
		}
	}
	if h == 0 {
		return Win{}, errors.New("no window is open")
	}
	r := winRect(h)
	pid := winPID(h)
	w := Win{ID: uint64(h), PID: pid, App: procName(pid), Title: winText(h), X: int(r.L), Y: int(r.T), W: int(r.R - r.L), H: int(r.B - r.T)}
	mon, _, _ := pMonitorFromWindow.Call(h, monitorNearest)
	if mon != 0 {
		mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
		if ok, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok != 0 {
			w.Fullscreen = r.L <= mi.Monitor.L && r.T <= mi.Monitor.T && r.R >= mi.Monitor.R && r.B >= mi.Monitor.B
		}
	}
	if h == fg && pSHQueryUserNotificationState.Find() == nil {
		var st int32
		if res, _, _ := pSHQueryUserNotificationState.Call(uintptr(unsafe.Pointer(&st))); res == 0 {
			if st == quNsBusy || st == quNsD3DFull || st == quNsPresentation {
				w.Fullscreen = true
			}
		}
	}
	return w, nil
}

// dibImage copies a bitmap into an RGBA image (the bitmap must not be selected into a DC).
func dibImage(dc, bmp uintptr, w, h int) (*image.RGBA, error) {
	bi := bmiHeader{Width: int32(w), Height: -int32(h), Planes: 1, BitCount: 32}
	bi.Size = uint32(unsafe.Sizeof(bi))
	buf := make([]byte, w*h*4)
	// BITMAPINFO = header + colour table (unused at 32bpp); pad generously.
	var info [64]byte
	*(*bmiHeader)(unsafe.Pointer(&info[0])) = bi
	n, _, _ := pGetDIBits.Call(dc, bmp, 0, uintptr(h), uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&info[0])), 0)
	if n == 0 {
		return nil, errors.New("GetDIBits failed")
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(buf); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = buf[i+2], buf[i+1], buf[i], 255
	}
	return img, nil
}

// grab copies a screen rectangle (BitBlt from the screen DC).
func grab(x, y, w, h int) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty area")
	}
	sdc, _, _ := pGetDC.Call(0)
	if sdc == 0 {
		return nil, errors.New("GetDC failed")
	}
	defer pReleaseDC.Call(0, sdc)
	mdc, _, _ := pCreateCompatibleDC.Call(sdc)
	defer pDeleteDC.Call(mdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(sdc, uintptr(w), uintptr(h))
	if bmp == 0 {
		return nil, errors.New("CreateCompatibleBitmap failed")
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mdc, bmp)
	pBitBlt.Call(mdc, 0, 0, uintptr(w), uintptr(h), sdc, uintptr(int32(x)), uintptr(int32(y)), srcCopy|captureBlt)
	pSelectObject.Call(mdc, old)
	return dibImage(mdc, bmp, w, h)
}

// printWin asks the window to draw itself (works when it's covered, e.g. by the orb).
func printWin(h uintptr, w, ht int) (*image.RGBA, error) {
	sdc, _, _ := pGetDC.Call(0)
	defer pReleaseDC.Call(0, sdc)
	mdc, _, _ := pCreateCompatibleDC.Call(sdc)
	defer pDeleteDC.Call(mdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(sdc, uintptr(w), uintptr(ht))
	if bmp == 0 {
		return nil, errors.New("CreateCompatibleBitmap failed")
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mdc, bmp)
	ok, _, _ := pPrintWindow.Call(h, mdc, pwRenderFull)
	pSelectObject.Call(mdc, old)
	if ok == 0 {
		return nil, errors.New("PrintWindow failed")
	}
	return dibImage(mdc, bmp, w, ht)
}

func capture(w Win, full bool) (image.Image, image.Point, string, error) {
	if full || w.ID == 0 {
		vx, _, _ := pGetSystemMetrics.Call(76)
		vy, _, _ := pGetSystemMetrics.Call(77)
		vw, _, _ := pGetSystemMetrics.Call(78)
		vh, _, _ := pGetSystemMetrics.Call(79)
		x, y := int(int32(vx)), int(int32(vy))
		img, err := grab(x, y, int(vw), int(vh))
		return img, image.Pt(x, y), "GDI", err
	}
	h := uintptr(w.ID)
	var wr rect32
	pGetWindowRect.Call(h, uintptr(unsafe.Pointer(&wr)))
	ww, wh := int(wr.R-wr.L), int(wr.B-wr.T)
	if ww > 0 && wh > 0 {
		if img, err := printWin(h, ww, wh); err == nil && !blank(img) {
			// Crop the invisible resize borders down to the visible frame.
			cr := Rect{w.X - int(wr.L), w.Y - int(wr.T), w.W, w.H}
			if cr.X >= 0 && cr.Y >= 0 && cr.W > 0 && cr.H > 0 {
				return Crop(img, cr), image.Pt(w.X, w.Y), "PrintWindow", nil
			}
			return img, image.Pt(int(wr.L), int(wr.T)), "PrintWindow", nil
		}
	}
	img, err := grab(w.X, w.Y, w.W, w.H)
	return img, image.Pt(w.X, w.Y), "GDI", err
}

// thumb is a 16x16 HALFTONE copy of the window's area of the screen: a few ms.
func thumb(w Win) (uint64, bool) {
	if w.W <= 0 || w.H <= 0 {
		return 0, false
	}
	sdc, _, _ := pGetDC.Call(0)
	if sdc == 0 {
		return 0, false
	}
	defer pReleaseDC.Call(0, sdc)
	mdc, _, _ := pCreateCompatibleDC.Call(sdc)
	defer pDeleteDC.Call(mdc)
	bmp, _, _ := pCreateCompatibleBitmap.Call(sdc, 16, 16)
	if bmp == 0 {
		return 0, false
	}
	defer pDeleteObject.Call(bmp)
	old, _, _ := pSelectObject.Call(mdc, bmp)
	pSetStretchBltMode.Call(mdc, halftone)
	pStretchBlt.Call(mdc, 0, 0, 16, 16, sdc, uintptr(int32(w.X)), uintptr(int32(w.Y)), uintptr(w.W), uintptr(w.H), srcCopy)
	pSelectObject.Call(mdc, old)
	img, err := dibImage(mdc, bmp, 16, 16)
	if err != nil {
		return 0, false
	}
	return DHash(img), true
}

func IdleSeconds() int {
	var li struct{ Size, Time uint32 }
	li.Size = 8
	if ok, _, _ := pGetLastInputInfo.Call(uintptr(unsafe.Pointer(&li))); ok == 0 {
		return -1
	}
	now, _, _ := pGetTickCount.Call()
	return int((uint32(now) - li.Time) / 1000)
}

func Capabilities() Caps {
	c := Caps{Pixels: true, PixelsVia: "Windows GDI (PrintWindow)", TextVia: "UI Automation"}
	if err := uiaAvailable(); err != nil {
		c.TextNote = "UI Automation isn't available: " + err.Error()
	} else {
		c.Text = true
	}
	return c
}

// AskScreenRecording is macOS only.
func AskScreenRecording() {}
