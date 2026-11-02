package video

import (
	"io"
	"log"
	"os/exec"
	"sync"
	"testing"
	"time"

	"github.com/donvito/iphone-mirror/internal/sender"
)

// TestDecoderLatency feeds a 60fps stream in real time and checks that decoded
// frames come out promptly (guards against ffmpeg flags that add buffering).
func TestDecoderLatency(t *testing.T) {
	if testing.Short() {
		t.Skip("slow")
	}
	ff, err := FindFFmpeg()
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	out, err := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size=886x1920:rate=60",
		"-frames:v", "90", "-c:v", "libx264", "-profile:v", "high", "-bf", "0", "-pix_fmt", "yuv420p", "-f", "h264", "-").Output()
	if err != nil {
		t.Fatal(err)
	}
	sps, pps, frames := sender.AccessUnits(sender.SplitAnnexB(out))
	var mu sync.Mutex
	var arrivals []time.Time
	d := NewDecoder(ff, func(w, h int, b []byte) []byte {
		mu.Lock()
		arrivals = append(arrivals, time.Now())
		mu.Unlock()
		return b
	}, log.New(io.Discard, "", 0))
	if err := d.Configure(H264, [][]byte{sps, pps}); err != nil {
		t.Fatal(err)
	}
	var sent []time.Time
	tk := time.NewTicker(time.Second / 60)
	defer tk.Stop()
	for _, f := range frames {
		<-tk.C
		var b []byte
		for _, n := range f {
			b = append(append(b, 0, 0, 0, 1), n...)
		}
		sent = append(sent, time.Now())
		d.Write(b)
	}
	time.Sleep(300 * time.Millisecond)
	d.Stop()
	mu.Lock()
	defer mu.Unlock()
	if len(arrivals) < len(sent)-3 {
		t.Fatalf("only %d of %d frames decoded", len(arrivals), len(sent))
	}
	for i := 5; i < len(arrivals); i += 20 {
		lat := arrivals[i].Sub(sent[i])
		t.Logf("frame %d latency %v", i, lat)
		if lat > 250*time.Millisecond {
			t.Errorf("frame %d latency %v is too high", i, lat)
		}
	}
}
