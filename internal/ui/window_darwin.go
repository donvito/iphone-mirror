package ui

// On macOS the mirrored iPhone screen is shown in a native Cocoa window that
// OBS can capture ("macOS Screen Capture" → Window). Video is decoded by
// VideoToolbox and shown as soon as each frame is decoded.

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=14.0
#cgo LDFLAGS: -framework Cocoa -framework CoreMedia -framework CoreVideo -framework VideoToolbox -framework QuartzCore -framework IOSurface -mmacosx-version-min=14.0
#include <stdlib.h>
#include "ui_darwin.h"
*/
import "C"

import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/donvito/iphone-mirror/internal/settings"
	"github.com/donvito/iphone-mirror/internal/video"
)

// Window is the mirror output window. There is only one per process.
type Window struct {
	hooks SettingsHooks
	log   *log.Logger

	mu     sync.Mutex // serializes video calls and guards the fields below
	buf    []byte
	codec  string
	frames int
	since  time.Time
}

var current *Window

// New creates the application and its window. It must be called on the main
// thread (lock it with runtime.LockOSThread in an init function).
func New(title string, logger *log.Logger) *Window {
	if current != nil {
		panic("ui: only one window is supported")
	}
	current = &Window{log: logger}
	cs := C.CString(title)
	defer C.free(unsafe.Pointer(cs))
	C.ui_init(cs)
	return current
}

// SetHooks enables the settings window, log folder and quit handling.
func (w *Window) SetHooks(h SettingsHooks) {
	w.hooks = h
	cs := C.CString(h.LogsDir)
	defer C.free(unsafe.Pointer(cs))
	C.ui_set_logs_dir(cs)
}

// Run runs the Cocoa event loop on the main thread. It does not return; the
// process exits after OnQuit when the user quits the app or closes the window.
func (w *Window) Run() error {
	C.ui_run()
	return nil
}

// SetStatus sets the text shown while no video is being displayed.
func (w *Window) SetStatus(s string) {
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	C.ui_set_status(cs)
}

func (w *Window) SetAlwaysOnTop(on bool) { C.ui_set_on_top(cbool(on)) }

func (w *Window) showSettings(s settings.Settings) {
	cs := C.CString(s.Name)
	defer C.free(unsafe.Pointer(cs))
	C.ui_show_settings(cs, cbool(s.HEVC), C.int(s.FPS), cbool(s.ShowStatus), cbool(s.AlwaysOnTop))
}

// isParamSet reports whether nal is a parameter set that belongs in the
// format description (the hvcC record can also carry SEI NAL units).
func isParamSet(codec string, nal []byte) bool {
	if len(nal) == 0 {
		return false
	}
	if codec == video.HEVC {
		t := (nal[0] >> 1) & 0x3f
		return t == 32 || t == 33 || t == 34 // VPS, SPS, PPS
	}
	t := nal[0] & 0x1f
	return t == 7 || t == 8 // SPS, PPS
}

// VideoConfig sets up the decoder for a new stream (also after the phone is
// rotated). paramSets are raw NAL units without start codes.
func (w *Window) VideoConfig(codec string, paramSets [][]byte, width, height int) error {
	var buf []byte
	var sizes []C.size_t
	for _, ps := range paramSets {
		if isParamSet(codec, ps) {
			buf = append(buf, ps...)
			sizes = append(sizes, C.size_t(len(ps)))
		}
	}
	if len(sizes) == 0 {
		return fmt.Errorf("no %s parameter sets", codec)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	st := C.ui_video_config(cbool(codec == video.HEVC), (*C.uint8_t)(unsafe.Pointer(&buf[0])), &sizes[0], C.int(len(sizes)), C.int(width), C.int(height))
	if st != 0 {
		return fmt.Errorf("create %s format description: OSStatus %d", codec, int(st))
	}
	w.codec = codec
	return nil
}

// VideoFrame decodes and shows one Annex-B access unit (4-byte start codes).
func (w *Window) VideoFrame(annexB []byte) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = video.AnnexBToAVCC(append(w.buf[:0], annexB...))
	if len(w.buf) == 0 {
		return
	}
	C.ui_video_frame((*C.uint8_t)(unsafe.Pointer(&w.buf[0])), C.size_t(len(w.buf)))
	if w.frames++; w.since.IsZero() {
		w.since = time.Now()
	} else if d := time.Since(w.since); d >= 10*time.Second {
		var sw, sh C.int
		var avg, max C.double
		shown := C.ui_video_stats(&sw, &sh, &avg, &max)
		w.log.Printf("video: %s, received %.1f fps, shown %.1f fps at %dx%d (last %.0fs), decode %.1f ms avg, %.1f ms max",
			w.codec, float64(w.frames)/d.Seconds(), float64(shown)/d.Seconds(), int(sw), int(sh), d.Seconds(), float64(avg), float64(max))
		w.frames, w.since = 0, time.Now()
	}
}

// VideoStop removes the current picture so the status text is shown again.
func (w *Window) VideoStop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	C.ui_video_clear()
	w.frames, w.since = 0, time.Time{}
}

func cbool(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
