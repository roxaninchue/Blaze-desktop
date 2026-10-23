//go:build windows

package screen

// UI Automation over raw COM vtables (no cgo). Runs only in the helper process,
// so a misbehaving provider in another app can't take Blaze down with it.

import (
	"errors"
	"fmt"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	ole "github.com/go-ole/go-ole"
)

var (
	clsidCUIAutomation = ole.NewGUID("{FF48DBA4-60EF-4201-AA87-54103EEF594E}")
	iidIUIAutomation   = ole.NewGUID("{30CBE57D-D9D0-452A-AB13-7AC5AC4825EE}")
	iidTextPattern     = ole.NewGUID("{32EBA289-3583-42C9-9C59-3B6D9A1E9B6A}")
)

// vtable slots (UIAutomationClient.h)
const (
	uiaGetControlViewWalker  = 14 // IUIAutomation::get_ControlViewWalker
	uiaElementFromHandleBC   = 10 // IUIAutomation::ElementFromHandleBuildCache
	uiaCreateCacheRequest    = 20 // IUIAutomation::CreateCacheRequest
	crAddProperty            = 3  // IUIAutomationCacheRequest::AddProperty
	twFirstChildBuildCache   = 10 // IUIAutomationTreeWalker::GetFirstChildElementBuildCache
	twNextSiblingBuildCache  = 12 // IUIAutomationTreeWalker::GetNextSiblingElementBuildCache
	elGetCachedPropertyValue = 12 // IUIAutomationElement::GetCachedPropertyValue
	elGetCurrentPatternAs    = 14 // IUIAutomationElement::GetCurrentPatternAs
	tpGetVisibleRanges       = 6  // IUIAutomationTextPattern::GetVisibleRanges
	arrLength                = 3  // IUIAutomation*Array::get_Length
	arrGetElement            = 4  // IUIAutomation*Array::GetElement
	trGetText                = 12 // IUIAutomationTextRange::GetText
)

const (
	propBounds     = 30001
	propControl    = 30003
	propName       = 30005
	propFocus      = 30008
	propPassword   = 30019
	propOffscreen  = 30022
	propValue      = 30045
	textPatternID  = 10014
	ctEdit         = 50004
	ctDocument     = 50030
	vtArrayR8      = 0x2005
	vtBstr         = 8
	vtBool         = 11
	vtI4           = 3
	treeTimeBudget = 3500 * time.Millisecond
)

var controlNames = map[int]string{
	50000: "button", 50001: "calendar", 50002: "checkbox", 50003: "combobox", 50004: "edit", 50005: "hyperlink",
	50006: "image", 50007: "listitem", 50008: "list", 50009: "menu", 50010: "menubar", 50011: "menuitem",
	50012: "progressbar", 50013: "radiobutton", 50014: "scrollbar", 50015: "slider", 50016: "spinner",
	50017: "statusbar", 50018: "tab", 50019: "tabitem", 50020: "text", 50021: "toolbar", 50022: "tooltip",
	50023: "tree", 50024: "treeitem", 50025: "custom", 50026: "group", 50027: "thumb", 50028: "datagrid",
	50029: "dataitem", 50030: "document", 50031: "split button", 50032: "window", 50033: "pane", 50034: "header",
	50035: "header", 50036: "table", 50037: "titlebar", 50038: "separator", 50039: "group", 50040: "toolbar",
}

func vcall(obj uintptr, slot int, args ...uintptr) uintptr {
	vtbl := *(*uintptr)(unsafe.Pointer(obj))
	fn := *(*uintptr)(unsafe.Pointer(vtbl + uintptr(slot)*unsafe.Sizeof(uintptr(0))))
	r, _, _ := syscall.SyscallN(fn, append([]uintptr{obj}, args...)...)
	return r
}

func release(obj uintptr) {
	if obj != 0 {
		vcall(obj, 2)
	}
}

func failed(hr uintptr) bool { return int32(hr) < 0 }

func newUIA() (uintptr, error) {
	_ = ole.CoInitializeEx(0, ole.COINIT_MULTITHREADED)
	unk, err := ole.CreateInstance(clsidCUIAutomation, iidIUIAutomation)
	if err != nil {
		return 0, err
	}
	return uintptr(unsafe.Pointer(unk)), nil
}

func uiaAvailable() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ua, err := newUIA()
	if err != nil {
		return err
	}
	release(ua)
	return nil
}

func cachedVariant(el uintptr, prop int) (ole.VARIANT, bool) {
	var v ole.VARIANT
	if failed(vcall(el, elGetCachedPropertyValue, uintptr(prop), uintptr(unsafe.Pointer(&v)))) {
		return v, false
	}
	return v, true
}

func cachedString(el uintptr, prop int) string {
	v, ok := cachedVariant(el, prop)
	if !ok {
		return ""
	}
	defer ole.VariantClear(&v)
	if v.VT != vtBstr || v.Val == 0 {
		return ""
	}
	return ole.BstrToString((*uint16)(unsafe.Pointer(uintptr(v.Val))))
}

func cachedInt(el uintptr, prop int) int {
	v, ok := cachedVariant(el, prop)
	if !ok {
		return 0
	}
	defer ole.VariantClear(&v)
	if v.VT == vtI4 {
		return int(int32(v.Val))
	}
	return 0
}

func cachedBool(el uintptr, prop int) bool {
	v, ok := cachedVariant(el, prop)
	if !ok {
		return false
	}
	defer ole.VariantClear(&v)
	return v.VT == vtBool && int16(v.Val) != 0
}

func cachedRect(el uintptr) *Rect {
	v, ok := cachedVariant(el, propBounds)
	if !ok {
		return nil
	}
	defer ole.VariantClear(&v)
	if v.VT != vtArrayR8 || v.Val == 0 {
		return nil
	}
	sac := ole.SafeArrayConversion{Array: (*ole.SafeArray)(unsafe.Pointer(uintptr(v.Val)))}
	vals := sac.ToValueArray()
	if len(vals) < 4 {
		return nil
	}
	f := func(i int) int {
		if x, ok := vals[i].(float64); ok {
			return int(x)
		}
		return 0
	}
	return &Rect{f(0), f(1), f(2), f(3)}
}

// visibleText reads the on-screen text of a document or edit (terminals, editors).
func visibleText(el uintptr) string {
	var tp uintptr
	if failed(vcall(el, elGetCurrentPatternAs, textPatternID, uintptr(unsafe.Pointer(iidTextPattern)), uintptr(unsafe.Pointer(&tp)))) || tp == 0 {
		return ""
	}
	defer release(tp)
	var arr uintptr
	if failed(vcall(tp, tpGetVisibleRanges, uintptr(unsafe.Pointer(&arr)))) || arr == 0 {
		return ""
	}
	defer release(arr)
	var n int32
	vcall(arr, arrLength, uintptr(unsafe.Pointer(&n)))
	out := ""
	for i := int32(0); i < n && i < 8 && len(out) < 6000; i++ {
		var rg uintptr
		if failed(vcall(arr, arrGetElement, uintptr(i), uintptr(unsafe.Pointer(&rg)))) || rg == 0 {
			continue
		}
		var bs *uint16
		if !failed(vcall(rg, trGetText, uintptr(6000), uintptr(unsafe.Pointer(&bs)))) && bs != nil {
			out += ole.BstrToString(bs) + "\n"
			ole.SysFreeString((*int16)(unsafe.Pointer(bs)))
		}
		release(rg)
	}
	return out
}

func tree(w Win, max int) ([]Flat, string, error) {
	return uiaTree(w, max, !shallow)
}

func uiaTree(w Win, max int, deep bool) (nodes []Flat, src string, err error) {
	src = "UI Automation"
	if w.ID == 0 {
		return nil, src, errors.New("no window")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	ua, err := newUIA()
	if err != nil {
		return nil, src, fmt.Errorf("UI Automation isn't available: %w", err)
	}
	defer release(ua)
	var cr uintptr
	if failed(vcall(ua, uiaCreateCacheRequest, uintptr(unsafe.Pointer(&cr)))) || cr == 0 {
		return nil, src, errors.New("UI Automation: no cache request")
	}
	defer release(cr)
	for _, p := range []int{propControl, propName, propFocus, propPassword, propOffscreen, propValue, propBounds} {
		vcall(cr, crAddProperty, uintptr(p))
	}
	var walker uintptr
	if failed(vcall(ua, uiaGetControlViewWalker, uintptr(unsafe.Pointer(&walker)))) || walker == 0 {
		return nil, src, errors.New("UI Automation: no tree walker")
	}
	defer release(walker)
	var root uintptr
	if hr := vcall(ua, uiaElementFromHandleBC, uintptr(w.ID), cr, uintptr(unsafe.Pointer(&root))); failed(hr) || root == 0 {
		return nil, src, fmt.Errorf("UI Automation couldn't open the window (0x%x)", uint32(hr))
	}
	defer release(root)
	start := time.Now()
	deepLeft := 3
	var visit func(el uintptr, depth int)
	visit = func(el uintptr, depth int) {
		if len(nodes) >= max || time.Since(start) > treeTimeBudget {
			return
		}
		ct := cachedInt(el, propControl)
		n := Flat{D: depth, Role: controlNames[ct], Name: cachedString(el, propName), Focus: cachedBool(el, propFocus)}
		if n.Role == "" {
			n.Role = "custom"
		}
		if cachedBool(el, propPassword) {
			n.Secret = true
			n.Box = cachedRect(el)
		} else {
			n.Value = cachedString(el, propValue)
			if deep && deepLeft > 0 && n.Value == "" && (ct == ctDocument || ct == ctEdit) {
				deepLeft--
				n.Value = visibleText(el)
			}
		}
		offscreen := cachedBool(el, propOffscreen)
		nodes = append(nodes, n)
		if depth >= maxDepth || (offscreen && depth > 0) {
			return
		}
		var child uintptr
		if failed(vcall(walker, twFirstChildBuildCache, el, cr, uintptr(unsafe.Pointer(&child)))) {
			return
		}
		for child != 0 {
			visit(child, depth+1)
			var next uintptr
			hr := vcall(walker, twNextSiblingBuildCache, child, cr, uintptr(unsafe.Pointer(&next)))
			release(child)
			if failed(hr) || len(nodes) >= max || time.Since(start) > treeTimeBudget {
				release(next)
				return
			}
			child = next
		}
	}
	visit(root, 0)
	return nodes, src, nil
}
