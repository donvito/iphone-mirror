package ui

/*
#include "ui_darwin.h"
*/
import "C"

//export goOpenSettings
func goOpenSettings() {
	if w := current; w != nil && w.hooks.Get != nil {
		w.showSettings(w.hooks.Get())
	}
}

// goApplySettings is called by the settings window (all values) and by the
// "Keep Window on Top" menu item (name == nil, other values -1).
//
//export goApplySettings
func goApplySettings(name *C.char, hevc, fps, showStatus, onTop C.int) {
	w := current
	if w == nil || w.hooks.Get == nil || w.hooks.Apply == nil {
		return
	}
	s := w.hooks.Get()
	if name != nil {
		s.Name = C.GoString(name)
	}
	if hevc >= 0 {
		s.HEVC = hevc != 0
	}
	if fps > 0 {
		s.FPS = int(fps)
	}
	if showStatus >= 0 {
		s.ShowStatus = showStatus != 0
	}
	if onTop >= 0 {
		s.AlwaysOnTop = onTop != 0
	}
	w.hooks.Apply(s)
}

//export goWillTerminate
func goWillTerminate() {
	if w := current; w != nil && w.hooks.OnQuit != nil {
		w.hooks.OnQuit()
	}
}

//export goLog
func goLog(msg *C.char) {
	if w := current; w != nil && w.log != nil {
		w.log.Print(C.GoString(msg))
	}
}
