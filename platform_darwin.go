package main

import (
	"log"
	"runtime"
	"sync"

	"github.com/donvito/iphone-mirror/internal/ui"
	"github.com/donvito/iphone-mirror/internal/video"
)

const (
	// macOS's own AirPlay Receiver (ControlCenter) already listens on 7000 and 5000.
	defaultPort  = 7100
	computerNoun = "Mac"
	settingsHint = "Right-click this window or press ⌘, for settings."
)

// Cocoa must run on the main thread; keep the main goroutine there.
func init() { runtime.LockOSThread() }

// vtVideo decodes with VideoToolbox inside the Cocoa window.
type vtVideo struct {
	win *ui.Window
	log *log.Logger

	mu   sync.Mutex
	size [2]int // current video size, for logging changes
}

func (v *vtVideo) Configure(codec string, paramSets [][]byte) error {
	w, h, err := video.FrameSize(codec, paramSets)
	if err != nil {
		return err
	}
	v.mu.Lock()
	changed := v.size != [2]int{w, h}
	v.size = [2]int{w, h}
	v.mu.Unlock()
	if changed {
		v.log.Printf("decoder: %s %dx%d", codec, w, h)
	}
	return v.win.VideoConfig(codec, paramSets, w, h)
}

func (v *vtVideo) Write(annexB []byte) { v.win.VideoFrame(annexB) }

func (v *vtVideo) Stop() {
	v.win.VideoStop()
	v.mu.Lock()
	v.size = [2]int{}
	v.mu.Unlock()
}

func newVideo(win *ui.Window, logger *log.Logger) (videoOutput, error) {
	return &vtVideo{win: win, log: logger}, nil
}
