// Package ui shows the mirrored iPhone screen in a plain Win32 window that can
// be captured by OBS ("Window Capture").
package ui

import (
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	gdi32  = windows.NewLazySystemDLL("gdi32.dll")

	pRegisterClassExW              = user32.NewProc("RegisterClassExW")
	pCreateWindowExW               = user32.NewProc("CreateWindowExW")
	pDefWindowProcW                = user32.NewProc("DefWindowProcW")
	pGetMessageW                   = user32.NewProc("GetMessageW")
	pTranslateMessage              = user32.NewProc("TranslateMessage")
	pDispatchMessageW              = user32.NewProc("DispatchMessageW")
	pPostQuitMessage               = user32.NewProc("PostQuitMessage")
	pPostMessageW                  = user32.NewProc("PostMessageW")
	pBeginPaint                    = user32.NewProc("BeginPaint")
	pEndPaint                      = user32.NewProc("EndPaint")
	pGetClientRect                 = user32.NewProc("GetClientRect")
	pGetWindowRect                 = user32.NewProc("GetWindowRect")
	pInvalidateRect                = user32.NewProc("InvalidateRect")
	pLoadCursorW                   = user32.NewProc("LoadCursorW")
	pShowWindow                    = user32.NewProc("ShowWindow")
	pFillRect                      = user32.NewProc("FillRect")
	pDrawTextW                     = user32.NewProc("DrawTextW")
	pSetWindowTextW                = user32.NewProc("SetWindowTextW")
	pSetWindowPos                  = user32.NewProc("SetWindowPos")
	pAdjustWindowRectExForDpi      = user32.NewProc("AdjustWindowRectExForDpi")
	pMonitorFromWindow             = user32.NewProc("MonitorFromWindow")
	pGetMonitorInfoW               = user32.NewProc("GetMonitorInfoW")
	pGetDpiForWindow               = user32.NewProc("GetDpiForWindow")
	pSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	pStretchDIBits                 = gdi32.NewProc("StretchDIBits")
	pSetStretchBltMode             = gdi32.NewProc("SetStretchBltMode")
	pSetBrushOrgEx                 = gdi32.NewProc("SetBrushOrgEx")
	pGetStockObject                = gdi32.NewProc("GetStockObject")
	pSetTextColor                  = gdi32.NewProc("SetTextColor")
	pSetBkMode                     = gdi32.NewProc("SetBkMode")
	pCreateFontW                   = gdi32.NewProc("CreateFontW")
	pSelectObject                  = gdi32.NewProc("SelectObject")
	pDeleteObject                  = gdi32.NewProc("DeleteObject")
)

const (
	wsOverlappedWindow = 0x00CF0000
	wsVisible          = 0x10000000
	cwUseDefault       = 0x80000000
	csHRedraw          = 0x0002
	csVRedraw          = 0x0001
	csDblClks          = 0x0008
	wmDestroy          = 0x0002
	wmSize             = 0x0005
	wmPaint            = 0x000F
	wmClose            = 0x0010
	wmEraseBkgnd       = 0x0014
	wmDpiChanged       = 0x02E0
	wmLButtonDblClk    = 0x0203
	wmUser             = 0x0400
	wmSetTitle         = wmUser + 1
	idcArrow           = 32512
	blackBrush         = 4
	halftone           = 4
	dibRGBColors       = 0
	srcCopy            = 0x00CC0020
	dtCenter           = 0x0001
	dtVCenter          = 0x0004
	dtWordBreak        = 0x0010
	dtCalcRect         = 0x0400
	dtNoPrefix         = 0x0800
	swpNoZOrder        = 0x0004
)

type wndClassEx struct {
	size       uint32
	style      uint32
	wndProc    uintptr
	clsExtra   int32
	wndExtra   int32
	instance   windows.Handle
	icon       windows.Handle
	cursor     windows.Handle
	background windows.Handle
	menuName   *uint16
	className  *uint16
	iconSm     windows.Handle
}

type point struct{ x, y int32 }

type msg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
	private uint32
}

type rect struct{ left, top, right, bottom int32 }

type paintStruct struct {
	hdc       windows.Handle
	erase     int32
	rcPaint   rect
	restore   int32
	incUpdate int32
	reserved  [32]byte
}

type bitmapInfoHeader struct {
	size          uint32
	width         int32
	height        int32
	planes        uint16
	bitCount      uint16
	compression   uint32
	sizeImage     uint32
	xPelsPerMeter int32
	yPelsPerMeter int32
	clrUsed       uint32
	clrImportant  uint32
}

// Window is the mirror output window.
type Window struct {
	title string
	hwnd  windows.HWND

	mu      sync.Mutex
	frame   []byte
	fw, fh  int
	status  string
	pending string

	hooks              SettingsHooks
	dlg                windows.HWND
	dlgFont            uintptr
	dlgClassRegistered bool
	startOnTop         bool
}

// SetHooks enables the right-click menu and settings dialog. Call before Run.
func (w *Window) SetHooks(h SettingsHooks) { w.hooks = h }

func New(title string) *Window {
	return &Window{title: title}
}

// SetFrame stores the latest BGRA frame and returns the previous buffer for reuse.
func (w *Window) SetFrame(width, height int, bgra []byte) []byte {
	w.mu.Lock()
	old := w.frame
	w.frame, w.fw, w.fh = bgra, width, height
	w.mu.Unlock()
	w.invalidate()
	return old
}

// ClearFrame removes the current frame so the status text is shown again.
func (w *Window) ClearFrame() {
	w.mu.Lock()
	w.frame = nil
	w.mu.Unlock()
	w.invalidate()
}

// SetStatus sets the text shown while no video is being displayed.
func (w *Window) SetStatus(s string) {
	w.mu.Lock()
	w.status = s
	w.mu.Unlock()
	w.invalidate()
}

// SetTitle changes the window title (safe to call from any goroutine).
func (w *Window) SetTitle(s string) {
	w.mu.Lock()
	w.pending = s
	w.mu.Unlock()
	if w.hwnd != 0 {
		pPostMessageW.Call(uintptr(w.hwnd), wmSetTitle, 0, 0)
	}
}

func (w *Window) invalidate() {
	if w.hwnd != 0 {
		pInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
	}
}

// Run creates the window and runs the message loop until the window is closed.
// It must be called from the main goroutine.
func (w *Window) Run() error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	pSetProcessDpiAwarenessContext.Call(^uintptr(3)) // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)

	className, _ := windows.UTF16PtrFromString("IPhoneMirrorWindow")
	titlePtr, _ := windows.UTF16PtrFromString(w.title)
	var inst windows.Handle
	windows.GetModuleHandleEx(0, nil, &inst)
	cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
	brush, _, _ := pGetStockObject.Call(blackBrush)
	wc := wndClassEx{
		style:      csHRedraw | csVRedraw | csDblClks,
		wndProc:    windows.NewCallback(w.wndProc),
		instance:   inst,
		cursor:     windows.Handle(cursor),
		background: windows.Handle(brush),
		className:  className,
	}
	wc.size = uint32(unsafe.Sizeof(wc))
	if r, _, err := pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); r == 0 {
		return err
	}
	hwnd, _, err := pCreateWindowExW.Call(0,
		uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(titlePtr)),
		wsOverlappedWindow|wsVisible,
		cwUseDefault, cwUseDefault, 0, 0, 0, 0, uintptr(inst), 0)
	if hwnd == 0 {
		return err
	}
	w.hwnd = windows.HWND(hwnd)
	if w.startOnTop {
		w.SetAlwaysOnTop(true)
	}
	w.fitTo(1290, 2796) // iPhone 15 Pro Max portrait aspect ratio
	pShowWindow.Call(hwnd, 5)

	var m msg
	for {
		r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
		if int32(r) <= 0 {
			return nil
		}
		if w.dlg != 0 {
			if r, _, _ := pIsDialogMessageW.Call(uintptr(w.dlg), uintptr(unsafe.Pointer(&m))); r != 0 {
				continue // Tab / Enter / Esc handling for the settings dialog
			}
		}
		pTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}

func (w *Window) dpi() int32 {
	d, _, _ := pGetDpiForWindow.Call(uintptr(w.hwnd))
	if d == 0 {
		return 96
	}
	return int32(d)
}

type monitorInfo struct {
	size    uint32
	monitor rect
	work    rect
	flags   uint32
}

// fitTo makes the window as tall as the monitor's work area allows, with a
// client area of the given aspect ratio. A tall window matters for OBS: Window
// Capture records the window at its on-screen size.
func (w *Window) fitTo(aspectW, aspectH int) {
	if aspectW <= 0 || aspectH <= 0 {
		return
	}
	mon, _, _ := pMonitorFromWindow.Call(uintptr(w.hwnd), 2) // MONITOR_DEFAULTTONEAREST
	mi := monitorInfo{}
	mi.size = uint32(unsafe.Sizeof(mi))
	if r, _, _ := pGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
		return
	}
	dpi := w.dpi()
	var frame rect // non-client border sizes
	pAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&frame)), wsOverlappedWindow, 0, 0, uintptr(dpi))
	frameW, frameH := frame.right-frame.left, frame.bottom-frame.top
	workW, workH := mi.work.right-mi.work.left, mi.work.bottom-mi.work.top

	ch := workH - frameH
	cw := int32(int64(ch) * int64(aspectW) / int64(aspectH))
	if cw > workW-frameW {
		cw = workW - frameW
		ch = int32(int64(cw) * int64(aspectH) / int64(aspectW))
	}
	var wr rect
	pGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&wr)))
	x := wr.left
	if x < mi.work.left || x+cw+frameW > mi.work.right {
		x = mi.work.left + (workW-cw-frameW)/2
	}
	pSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(x), uintptr(mi.work.top), uintptr(cw+frameW), uintptr(ch+frameH), swpNoZOrder)
}

// fitToVideo resizes the window to the video's aspect ratio (no black bars).
func (w *Window) fitToVideo() {
	w.mu.Lock()
	fw, fh := w.fw, w.fh
	w.mu.Unlock()
	if fw == 0 || fh == 0 {
		fw, fh = 1290, 2796 // no video yet: iPhone 15 Pro Max portrait
	}
	w.fitTo(fw, fh)
}

func (w *Window) wndProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmEraseBkgnd:
		return 1
	case wmPaint:
		w.paint(hwnd)
		return 0
	case wmLButtonDblClk:
		w.fitToVideo()
		return 0
	case wmCommand:
		if wParam&0xffff == menuSettings {
			w.openSettings()
			return 0
		}
	case wmContextMenu:
		w.showContextMenu(int32(int16(lParam&0xffff)), int32(int16(lParam>>16)))
		return 0
	case wmSetTitle:
		w.mu.Lock()
		t := w.pending
		w.mu.Unlock()
		p, _ := windows.UTF16PtrFromString(t)
		pSetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(p)))
		return 0
	case wmDpiChanged:
		r := *(**rect)(unsafe.Pointer(&lParam)) // lParam points to the suggested window rect
		pSetWindowPos.Call(uintptr(hwnd), 0, uintptr(r.left), uintptr(r.top), uintptr(r.right-r.left), uintptr(r.bottom-r.top), swpNoZOrder)
		return 0
	case wmDestroy:
		pPostQuitMessage.Call(0)
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}

func (w *Window) paint(hwnd windows.HWND) {
	var ps paintStruct
	hdc, _, _ := pBeginPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))
	defer pEndPaint.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&ps)))

	var cr rect
	pGetClientRect.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&cr)))
	cw, ch := cr.right, cr.bottom
	black, _, _ := pGetStockObject.Call(blackBrush)

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.frame == nil || w.fw == 0 || w.fh == 0 {
		pFillRect.Call(hdc, uintptr(unsafe.Pointer(&cr)), black)
		w.drawStatus(hdc, cr)
		return
	}

	// Letterbox: scale to fit while preserving the aspect ratio.
	dw, dh := cw, int32(int64(cw)*int64(w.fh)/int64(w.fw))
	if dh > ch {
		dh, dw = ch, int32(int64(ch)*int64(w.fw)/int64(w.fh))
	}
	dx, dy := (cw-dw)/2, (ch-dh)/2
	for _, bar := range []rect{{0, 0, cw, dy}, {0, dy + dh, cw, ch}, {0, dy, dx, dy + dh}, {dx + dw, dy, cw, dy + dh}} {
		if bar.right > bar.left && bar.bottom > bar.top {
			pFillRect.Call(hdc, uintptr(unsafe.Pointer(&bar)), black)
		}
	}

	bih := bitmapInfoHeader{
		width:    int32(w.fw),
		height:   -int32(w.fh), // top-down
		planes:   1,
		bitCount: 32,
	}
	bih.size = uint32(unsafe.Sizeof(bih))
	pSetStretchBltMode.Call(hdc, halftone)
	pSetBrushOrgEx.Call(hdc, 0, 0, 0)
	pStretchDIBits.Call(hdc,
		uintptr(dx), uintptr(dy), uintptr(dw), uintptr(dh),
		0, 0, uintptr(w.fw), uintptr(w.fh),
		uintptr(unsafe.Pointer(&w.frame[0])), uintptr(unsafe.Pointer(&bih)),
		dibRGBColors, srcCopy)
}

func (w *Window) drawStatus(hdc uintptr, cr rect) {
	if w.status == "" {
		return
	}
	dpi := w.dpi()
	face, _ := windows.UTF16PtrFromString("Segoe UI")
	font, _, _ := pCreateFontW.Call(uintptr(-20*dpi/96), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	old, _, _ := pSelectObject.Call(hdc, font)
	defer func() {
		pSelectObject.Call(hdc, old)
		pDeleteObject.Call(font)
	}()
	pSetTextColor.Call(hdc, 0x00D0D0D0)
	pSetBkMode.Call(hdc, 1) // TRANSPARENT

	text, _ := syscall.UTF16FromString(w.status)
	margin := 24 * dpi / 96
	box := rect{cr.left + margin, cr.top, cr.right - margin, cr.bottom}
	measure := box
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&text[0])), ^uintptr(0), uintptr(unsafe.Pointer(&measure)), dtCenter|dtWordBreak|dtNoPrefix|dtCalcRect)
	h := measure.bottom - measure.top
	box.top = cr.top + (cr.bottom-cr.top-h)/2
	box.bottom = box.top + h
	pDrawTextW.Call(hdc, uintptr(unsafe.Pointer(&text[0])), ^uintptr(0), uintptr(unsafe.Pointer(&box)), dtCenter|dtWordBreak|dtNoPrefix)
}
