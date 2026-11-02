package ui

import (
	"strconv"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	pCreatePopupMenu     = user32.NewProc("CreatePopupMenu")
	pAppendMenuW         = user32.NewProc("AppendMenuW")
	pTrackPopupMenu      = user32.NewProc("TrackPopupMenu")
	pDestroyMenu         = user32.NewProc("DestroyMenu")
	pSetForegroundWindow = user32.NewProc("SetForegroundWindow")
	pDestroyWindow       = user32.NewProc("DestroyWindow")
	pEnableWindow        = user32.NewProc("EnableWindow")
	pSendMessageW        = user32.NewProc("SendMessageW")
	pGetWindowTextW      = user32.NewProc("GetWindowTextW")
	pGetWindowTextLength = user32.NewProc("GetWindowTextLengthW")
	pIsDialogMessageW    = user32.NewProc("IsDialogMessageW")
	pSetFocus            = user32.NewProc("SetFocus")
	pGetSysColorBrush    = user32.NewProc("GetSysColorBrush")
	pShellExecuteW       = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteW")
)

const (
	wmCommand     = 0x0111
	wmContextMenu = 0x007B
	wmSetFont     = 0x0030
	wsChild       = 0x40000000
	wsTabStop     = 0x00010000
	wsBorder      = 0x00800000
	wsCaption     = 0x00C00000
	wsSysMenu     = 0x00080000
	wsVScroll     = 0x00200000
	wsExClientEdg = 0x00000200
	wsExDlgModal  = 0x00000001
	esAutoHScroll = 0x0080
	cbsDropDown   = 0x0003 // CBS_DROPDOWNLIST
	bsAutoCheck   = 0x0003
	bsDefPush     = 0x0001
	cbAddString   = 0x0143
	cbSetCurSel   = 0x014E
	cbGetCurSel   = 0x0147
	bmGetCheck    = 0x00F0
	bmSetCheck    = 0x00F1
	mfString      = 0x0000
	mfSeparator   = 0x0800
	mfChecked     = 0x0008
	tpmReturnCmd  = 0x0100
	tpmRightBtn   = 0x0002
	idOK          = 1
	idCancel      = 2
	colorBtnFace  = 15

	menuSettings = 100
	menuFit      = 101
	menuOnTop    = 102
	menuLogs     = 103
	menuExit     = 104

	ctlName    = 200
	ctlQuality = 201
	ctlFPS     = 202
	ctlStatus  = 203
	ctlOnTop   = 204
)

var qualityChoices = []string{
	"High: HEVC, up to 2160p (recommended)",
	"Compatible: H.264, up to 1080p",
}

var fpsChoices = []int{60, 30}

func (w *Window) showContextMenu(x, y int32) {
	if w.hooks.Get == nil {
		return
	}
	cur := w.hooks.Get()
	m, _, _ := pCreatePopupMenu.Call()
	defer pDestroyMenu.Call(m)
	add := func(id int, text string, flags uintptr) {
		p, _ := windows.UTF16PtrFromString(text)
		pAppendMenuW.Call(m, flags, uintptr(id), uintptr(unsafe.Pointer(p)))
	}
	add(menuSettings, "Settings…", mfString)
	add(menuFit, "Fit window to phone\tDouble-click", mfString)
	onTop := uintptr(mfString)
	if cur.AlwaysOnTop {
		onTop |= mfChecked
	}
	add(menuOnTop, "Keep window on top", onTop)
	pAppendMenuW.Call(m, mfSeparator, 0, 0)
	add(menuLogs, "Open log folder", mfString)
	add(menuExit, "Exit", mfString)

	if x == -1 && y == -1 { // opened from the keyboard
		var wr rect
		pGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&wr)))
		x, y = wr.left+40, wr.top+60
	}
	pSetForegroundWindow.Call(uintptr(w.hwnd))
	cmd, _, _ := pTrackPopupMenu.Call(m, tpmReturnCmd|tpmRightBtn, uintptr(x), uintptr(y), 0, uintptr(w.hwnd), 0)
	switch cmd {
	case menuSettings:
		w.openSettings()
	case menuFit:
		w.fitToVideo()
	case menuOnTop:
		cur.AlwaysOnTop = !cur.AlwaysOnTop
		w.hooks.Apply(cur)
	case menuLogs:
		dir, _ := windows.UTF16PtrFromString(w.hooks.LogsDir)
		open, _ := windows.UTF16PtrFromString("open")
		pShellExecuteW.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(open)), uintptr(unsafe.Pointer(dir)), 0, 0, 1)
	case menuExit:
		pDestroyWindow.Call(uintptr(w.hwnd))
	}
}

// SetAlwaysOnTop pins the window above others (safe to call from any goroutine).
func (w *Window) SetAlwaysOnTop(on bool) {
	if w.hwnd == 0 {
		w.startOnTop = on
		return
	}
	after := ^uintptr(1) // HWND_NOTOPMOST (-2)
	if on {
		after = ^uintptr(0) // HWND_TOPMOST (-1)
	}
	pSetWindowPos.Call(uintptr(w.hwnd), after, 0, 0, 0, 0, 0x0001|0x0002|0x0010) // NOSIZE|NOMOVE|NOACTIVATE
}

func (w *Window) openSettings() {
	if w.dlg != 0 {
		pSetForegroundWindow.Call(uintptr(w.dlg))
		return
	}
	className, _ := windows.UTF16PtrFromString("IPhoneMirrorSettings")
	if !w.dlgClassRegistered {
		var inst windows.Handle
		windows.GetModuleHandleEx(0, nil, &inst)
		cursor, _, _ := pLoadCursorW.Call(0, idcArrow)
		brush, _, _ := pGetSysColorBrush.Call(colorBtnFace)
		wc := wndClassEx{wndProc: windows.NewCallback(w.dlgProc), instance: inst, cursor: windows.Handle(cursor),
			background: windows.Handle(brush), className: className}
		wc.size = uint32(unsafe.Sizeof(wc))
		pRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc)))
		w.dlgClassRegistered = true
	}

	dpi := w.dpi()
	sc := func(v int32) int32 { return v * dpi / 96 }
	cw, ch := sc(440), sc(330)
	r := rect{0, 0, cw, ch}
	pAdjustWindowRectExForDpi.Call(uintptr(unsafe.Pointer(&r)), wsCaption|wsSysMenu, 0, wsExDlgModal, uintptr(dpi))
	var wr rect
	pGetWindowRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&wr)))
	x := wr.left + (wr.right-wr.left-(r.right-r.left))/2
	y := wr.top + sc(80)
	title, _ := windows.UTF16PtrFromString("iPhone Mirror Settings")
	hwnd, _, _ := pCreateWindowExW.Call(wsExDlgModal, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(title)),
		wsCaption|wsSysMenu, uintptr(x), uintptr(y), uintptr(r.right-r.left), uintptr(r.bottom-r.top),
		uintptr(w.hwnd), 0, 0, 0)
	if hwnd == 0 {
		return
	}
	w.dlg = windows.HWND(hwnd)

	face, _ := windows.UTF16PtrFromString("Segoe UI")
	w.dlgFont, _, _ = pCreateFontW.Call(uintptr(-sc(15)), 0, 0, 0, 400, 0, 0, 0, 1, 0, 0, 5, 0, uintptr(unsafe.Pointer(face)))
	ctl := func(class, text string, style uintptr, exStyle uintptr, id int, x, y, cw, ch int32) uintptr {
		cls, _ := windows.UTF16PtrFromString(class)
		txt, _ := windows.UTF16PtrFromString(text)
		h, _, _ := pCreateWindowExW.Call(exStyle, uintptr(unsafe.Pointer(cls)), uintptr(unsafe.Pointer(txt)),
			wsChild|wsVisible|style, uintptr(sc(x)), uintptr(sc(y)), uintptr(sc(cw)), uintptr(sc(ch)), hwnd, uintptr(id), 0, 0)
		pSendMessageW.Call(h, wmSetFont, w.dlgFont, 1)
		return h
	}
	cur := w.hooks.Get()

	ctl("STATIC", "Name shown on the iPhone", 0, 0, 0, 20, 16, 400, 20)
	name := ctl("EDIT", cur.Name, wsTabStop|esAutoHScroll, wsExClientEdg, ctlName, 20, 38, 400, 26)

	ctl("STATIC", "Quality", 0, 0, 0, 20, 78, 400, 20)
	q := ctl("COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDown, 0, ctlQuality, 20, 100, 400, 200)
	for _, s := range qualityChoices {
		p, _ := windows.UTF16PtrFromString(s)
		pSendMessageW.Call(q, cbAddString, 0, uintptr(unsafe.Pointer(p)))
	}
	sel := uintptr(0)
	if !cur.HEVC {
		sel = 1
	}
	pSendMessageW.Call(q, cbSetCurSel, sel, 0)

	ctl("STATIC", "Maximum frame rate", 0, 0, 0, 20, 140, 400, 20)
	f := ctl("COMBOBOX", "", wsTabStop|wsVScroll|cbsDropDown, 0, ctlFPS, 20, 162, 400, 200)
	for i, v := range fpsChoices {
		p, _ := windows.UTF16PtrFromString(strconv.Itoa(v) + " fps")
		pSendMessageW.Call(f, cbAddString, 0, uintptr(unsafe.Pointer(p)))
		if v == cur.FPS {
			pSendMessageW.Call(f, cbSetCurSel, uintptr(i), 0)
		}
	}

	st := ctl("BUTTON", "Show instructions while no iPhone is connected", wsTabStop|bsAutoCheck, 0, ctlStatus, 20, 204, 400, 24)
	ot := ctl("BUTTON", "Keep window on top of other windows", wsTabStop|bsAutoCheck, 0, ctlOnTop, 20, 232, 400, 24)
	check := func(h uintptr, on bool) {
		if on {
			pSendMessageW.Call(h, bmSetCheck, 1, 0)
		}
	}
	check(st, cur.ShowStatus)
	check(ot, cur.AlwaysOnTop)

	ctl("STATIC", "Changing the name or quality briefly disconnects the iPhone.", 0, 0, 0, 20, 266, 400, 20)
	ctl("BUTTON", "Save", wsTabStop|bsDefPush, 0, idOK, 230, 290, 90, 30)
	ctl("BUTTON", "Cancel", wsTabStop, 0, idCancel, 330, 290, 90, 30)

	pEnableWindow.Call(uintptr(w.hwnd), 0)
	pShowWindow.Call(hwnd, 5)
	pSetFocus.Call(name)
	pSendMessageW.Call(name, 0x00B1, 0, ^uintptr(0)) // EM_SETSEL: select all
}

func (w *Window) closeSettings() {
	if w.dlg == 0 {
		return
	}
	pEnableWindow.Call(uintptr(w.hwnd), 1)
	pDestroyWindow.Call(uintptr(w.dlg))
	w.dlg = 0
	if w.dlgFont != 0 {
		pDeleteObject.Call(w.dlgFont)
		w.dlgFont = 0
	}
	pSetForegroundWindow.Call(uintptr(w.hwnd))
}

func (w *Window) child(id int) uintptr {
	h, _, _ := user32.NewProc("GetDlgItem").Call(uintptr(w.dlg), uintptr(id))
	return h
}

func (w *Window) saveSettings() {
	s := w.hooks.Get()
	nameCtl := w.child(ctlName)
	n, _, _ := pGetWindowTextLength.Call(nameCtl)
	buf := make([]uint16, n+1)
	pGetWindowTextW.Call(nameCtl, uintptr(unsafe.Pointer(&buf[0])), n+1)
	if name := strings.TrimSpace(windows.UTF16ToString(buf)); name != "" {
		s.Name = name
	}
	q, _, _ := pSendMessageW.Call(w.child(ctlQuality), cbGetCurSel, 0, 0)
	s.HEVC = q != 1
	if i, _, _ := pSendMessageW.Call(w.child(ctlFPS), cbGetCurSel, 0, 0); int(i) >= 0 && int(i) < len(fpsChoices) {
		s.FPS = fpsChoices[i]
	}
	st, _, _ := pSendMessageW.Call(w.child(ctlStatus), bmGetCheck, 0, 0)
	ot, _, _ := pSendMessageW.Call(w.child(ctlOnTop), bmGetCheck, 0, 0)
	s.ShowStatus, s.AlwaysOnTop = st == 1, ot == 1
	w.closeSettings()
	w.hooks.Apply(s)
}

func (w *Window) dlgProc(hwnd windows.HWND, message uint32, wParam, lParam uintptr) uintptr {
	switch message {
	case wmCommand:
		switch wParam & 0xffff {
		case idOK:
			w.saveSettings()
			return 0
		case idCancel:
			w.closeSettings()
			return 0
		}
	case wmClose:
		w.closeSettings()
		return 0
	}
	r, _, _ := pDefWindowProcW.Call(uintptr(hwnd), uintptr(message), wParam, lParam)
	return r
}
