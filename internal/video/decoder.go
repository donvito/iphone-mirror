// Package video decodes the mirrored H.264 stream into BGRA frames using an
// ffmpeg child process.
package video

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"sync/atomic"
	"syscall"
)

// FrameSink receives decoded frames. It returns a buffer that the decoder may
// reuse for a later frame (typically the previously displayed frame), or nil.
type FrameSink func(width, height int, bgra []byte) (recycled []byte)

type Decoder struct {
	ffmpeg string
	sink   FrameSink
	log    *log.Logger

	mu     sync.Mutex
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	width  int
	height int
	gen    atomic.Int64 // bumped whenever ffmpeg is restarted, so stale frames are dropped
}

// FindFFmpeg looks for ffmpeg.exe next to the executable, then on PATH.
func FindFFmpeg() (string, error) {
	if exe, err := os.Executable(); err == nil {
		for _, p := range []string{
			filepath.Join(filepath.Dir(exe), "ffmpeg.exe"),
			filepath.Join(filepath.Dir(exe), "ffmpeg", "ffmpeg.exe"),
			filepath.Join(filepath.Dir(exe), "ffmpeg", "bin", "ffmpeg.exe"),
		} {
			if _, err := os.Stat(p); err == nil {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath("ffmpeg"); err == nil {
		return p, nil
	}
	return "", errors.New("ffmpeg.exe not found (place it next to the app or install it, e.g. `winget install Gyan.FFmpeg`)")
}

func NewDecoder(ffmpegPath string, sink FrameSink, logger *log.Logger) *Decoder {
	return &Decoder{ffmpeg: ffmpegPath, sink: sink, log: logger}
}

// Configure is called with each new SPS/PPS. It (re)starts ffmpeg when the
// frame size changes (e.g. the phone is rotated) and feeds the parameter sets.
func (d *Decoder) Configure(sps, pps []byte) error {
	w, h, err := ParseSPSSize(sps)
	if err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cmd == nil || w != d.width || h != d.height {
		d.stopLocked()
		if err := d.startLocked(w, h); err != nil {
			return err
		}
		d.log.Printf("decoder: started for %dx%d", w, h)
	}
	buf := append(append(append([]byte{0, 0, 0, 1}, sps...), 0, 0, 0, 1), pps...)
	return d.writeLocked(buf)
}

// Write feeds one Annex-B access unit to the decoder.
func (d *Decoder) Write(annexB []byte) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.writeLocked(annexB)
}

func (d *Decoder) writeLocked(b []byte) error {
	if d.stdin == nil {
		return nil
	}
	if _, err := d.stdin.Write(b); err != nil {
		d.log.Printf("decoder: write failed: %v", err)
		d.stopLocked()
		return err
	}
	return nil
}

func (d *Decoder) Stop() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopLocked()
}

func (d *Decoder) stopLocked() {
	if d.cmd == nil {
		return
	}
	d.gen.Add(1)
	d.stdin.Close()
	d.cmd.Process.Kill()
	d.cmd.Wait()
	d.cmd, d.stdin = nil, nil
}

// Note: "-fflags nobuffer" must NOT be used; with ffmpeg 7/8 it stalls output
// from a pipe. "-flags low_delay" disables frame threading (~40ms latency total).
var ffmpegArgs = []string{
	"-hide_banner", "-loglevel", "error",
	"-flags", "low_delay",
	"-probesize", "32", "-analyzeduration", "0",
	"-f", "h264", "-i", "pipe:0",
	"-fps_mode", "passthrough",
	"-f", "rawvideo", "-pix_fmt", "bgra", "pipe:1",
}

func (d *Decoder) startLocked(w, h int) error {
	cmd := exec.Command(d.ffmpeg, ffmpegArgs...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start ffmpeg: %w", err)
	}
	d.cmd, d.stdin, d.width, d.height = cmd, stdin, w, h
	gen := d.gen.Add(1)
	go func() {
		sc := bufio.NewScanner(stderr)
		for sc.Scan() {
			d.log.Printf("ffmpeg: %s", sc.Text())
		}
	}()
	go d.readFrames(bufio.NewReaderSize(stdout, 1<<20), w, h, gen)
	return nil
}

func (d *Decoder) readFrames(r io.Reader, w, h int, gen int64) {
	size := w * h * 4
	buf := make([]byte, size)
	for {
		if _, err := io.ReadFull(r, buf); err != nil {
			return
		}
		if d.gen.Load() != gen {
			return
		}
		recycled := d.sink(w, h, buf)
		if cap(recycled) >= size {
			buf = recycled[:size]
		} else {
			buf = make([]byte, size)
		}
	}
}
