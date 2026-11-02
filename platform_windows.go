package main

import (
	"log"

	"github.com/donvito/iphone-mirror/internal/ui"
	"github.com/donvito/iphone-mirror/internal/video"
)

const (
	defaultPort  = 7000
	computerNoun = "PC"
	settingsHint = "Right-click this window for settings."
)

// ffmpegVideo decodes with an ffmpeg child process and draws BGRA frames in the Win32 window.
type ffmpegVideo struct {
	dec *video.Decoder
	win *ui.Window
}

func (v ffmpegVideo) Configure(codec string, paramSets [][]byte) error {
	return v.dec.Configure(codec, paramSets)
}

func (v ffmpegVideo) Write(annexB []byte) { v.dec.Write(annexB) }

func (v ffmpegVideo) Stop() {
	v.dec.Stop()
	v.win.ClearFrame()
}

func newVideo(win *ui.Window, logger *log.Logger) (videoOutput, error) {
	ffmpeg, err := video.FindFFmpeg()
	if err != nil {
		return nil, err
	}
	logger.Printf("using ffmpeg at %s", ffmpeg)
	return ffmpegVideo{dec: video.NewDecoder(ffmpeg, win.SetFrame, logger), win: win}, nil
}
