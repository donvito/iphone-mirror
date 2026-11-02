package airplay_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log"
	"net"
	"os/exec"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/donvito/iphone-mirror/internal/airplay"
	"github.com/donvito/iphone-mirror/internal/sender"
)

type recSink struct {
	mu        sync.Mutex
	started   bool
	stopped   bool
	codec     string
	paramSets [][]byte
	frames    [][]byte
}

func (r *recSink) MirrorStarted(string) { r.mu.Lock(); r.started = true; r.mu.Unlock() }
func (r *recSink) MirrorStopped()       { r.mu.Lock(); r.stopped = true; r.mu.Unlock() }
func (r *recSink) CodecConfig(codec string, ps [][]byte) {
	r.mu.Lock()
	r.codec, r.paramSets = codec, ps
	r.mu.Unlock()
}
func (r *recSink) VideoData(b []byte) {
	r.mu.Lock()
	r.frames = append(r.frames, append([]byte(nil), b...))
	r.mu.Unlock()
}

func encode(t *testing.T, encoder string) []byte {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	format := map[string]string{"libx264": "h264", "libx265": "hevc"}[encoder]
	out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=886x1920:rate=30",
		"-frames:v", "20", "-c:v", encoder, "-bf", "0", "-pix_fmt", "yuv420p", "-x265-params", "log-level=none",
		"-f", format, "-").Output()
	if err != nil {
		t.Fatalf("ffmpeg %s: %v", encoder, err)
	}
	return out
}

func TestMirrorEndToEnd(t *testing.T) {
	for _, tc := range []struct{ name, encoder, codec string }{
		{"h264", "libx264", airplay.CodecH264},
		{"hevc", "libx265", airplay.CodecHEVC},
	} {
		t.Run(tc.name, func(t *testing.T) { testMirror(t, tc.encoder, tc.codec) })
	}
}

func testMirror(t *testing.T, encoder, codec string) {
	stream := encode(t, encoder)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	sink := &recSink{}
	srv := airplay.NewServer(airplay.Config{
		Name: "Test", HWAddr: net.HardwareAddr{2, 1, 2, 3, 4, 5}, Identity: priv, HEVC: true,
		Width: 3840, Height: 2160, FPS: 60, Port: port, Log: log.New(io.Discard, "", 0),
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
	m, err := c.StartMirror(0xF234567890ABCDEF) // > MaxInt64 to exercise unsigned handling
	if err != nil {
		t.Fatal(err)
	}
	nals := sender.SplitAnnexB(stream)
	var wantPS [][]byte
	var frames [][][]byte
	if codec == airplay.CodecHEVC {
		vps, sps, pps, f := sender.HEVCAccessUnits(nals)
		wantPS, frames = [][]byte{vps, sps, pps}, f
		err = m.SendCodecHEVC(vps, sps, pps)
	} else {
		sps, pps, f := sender.AccessUnits(nals)
		wantPS, frames = [][]byte{sps, pps}, f
		err = m.SendCodec(sps, pps)
	}
	if err != nil {
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
	if sink.codec != codec || len(sink.paramSets) != len(wantPS) {
		t.Fatalf("codec config: got %s with %d parameter sets", sink.codec, len(sink.paramSets))
	}
	for i := range wantPS {
		if !bytes.Equal(sink.paramSets[i], wantPS[i]) {
			t.Errorf("parameter set %d mismatch", i)
		}
	}
	if len(frames) == 0 || len(sink.frames) != len(frames) {
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
