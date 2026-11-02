package airplay_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/melvi/iphone-win-obs/internal/airplay"
	"github.com/melvi/iphone-win-obs/internal/sender"
)

type recSink struct {
	mu       sync.Mutex
	started  bool
	stopped  bool
	sps, pps []byte
	frames   [][]byte
}

func (r *recSink) MirrorStarted(string) { r.mu.Lock(); r.started = true; r.mu.Unlock() }
func (r *recSink) MirrorStopped()       { r.mu.Lock(); r.stopped = true; r.mu.Unlock() }
func (r *recSink) CodecConfig(sps, pps []byte) {
	r.mu.Lock()
	r.sps, r.pps = sps, pps
	r.mu.Unlock()
}
func (r *recSink) VideoData(b []byte) {
	r.mu.Lock()
	r.frames = append(r.frames, append([]byte(nil), b...))
	r.mu.Unlock()
}

func testStream(t *testing.T) []byte {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	out := filepath.Join(t.TempDir(), "test.h264")
	cmd := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=886x1920:rate=30",
		"-frames:v", "20", "-c:v", "libx264", "-profile:v", "high", "-bf", "0", "-pix_fmt", "yuv420p", "-f", "h264", out)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, b)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestMirrorEndToEnd(t *testing.T) {
	stream := testStream(t)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	sink := &recSink{}
	srv := airplay.NewServer(airplay.Config{
		Name: "Test", HWAddr: net.HardwareAddr{2, 1, 2, 3, 4, 5}, Identity: priv,
		Width: 1920, Height: 1080, FPS: 60, Port: port, Log: log.New(io.Discard, "", 0),
	}, sink)
	if err := srv.Listen(); err != nil {
		t.Fatal(err)
	}
	go srv.Serve()
	defer srv.Close()

	c, err := sender.Dial(net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := c.Handshake(); err != nil {
		t.Fatal(err)
	}
	m, err := c.StartMirror(uint64(time.Now().UnixNano())) // > MaxInt64 to exercise unsigned handling
	if err != nil {
		t.Fatal(err)
	}
	sps, pps, frames := sender.AccessUnits(sender.SplitAnnexB(stream))
	if err := m.SendCodec(sps, pps); err != nil {
		t.Fatal(err)
	}
	for _, f := range frames {
		if err := m.SendFrame(f); err != nil {
			t.Fatal(err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		sink.mu.Lock()
		n := len(sink.frames)
		sink.mu.Unlock()
		if n == len(frames) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := c.Teardown(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)

	sink.mu.Lock()
	defer sink.mu.Unlock()
	if !sink.started || !sink.stopped {
		t.Errorf("started=%v stopped=%v", sink.started, sink.stopped)
	}
	if !bytes.Equal(sink.sps, sps) || !bytes.Equal(sink.pps, pps) {
		t.Errorf("codec config mismatch")
	}
	if len(sink.frames) != len(frames) {
		t.Fatalf("got %d frames, want %d", len(sink.frames), len(frames))
	}
	for i, f := range frames {
		var want []byte
		for _, n := range f {
			want = append(append(want, 0, 0, 0, 1), n...)
		}
		if !bytes.Equal(sink.frames[i], want) {
			t.Fatalf("frame %d differs after decryption", i)
		}
	}
}
