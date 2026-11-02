// fakesender streams a test pattern to a running iPhone Mirror receiver using
// the AirPlay mirroring protocol, so the app can be tested without an iPhone.
//
//	go run ./cmd/fakesender -addr 127.0.0.1:7000 -size 886x1920 -seconds 20 -rotate
package main

import (
	"flag"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/melvi/iphone-win-obs/internal/sender"
)

func encode(size string, fps, seconds int) (sps, pps []byte, frames [][][]byte) {
	stream, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size="+size+":rate="+strconv.Itoa(fps),
		"-t", strconv.Itoa(seconds), "-c:v", "libx264", "-profile:v", "high", "-bf", "0", "-g", "60",
		"-pix_fmt", "yuv420p", "-f", "h264", "-").Output()
	if err != nil {
		log.Fatalf("ffmpeg: %v", err)
	}
	return sender.AccessUnits(sender.SplitAnnexB(stream))
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7000", "receiver address")
	size := flag.String("size", "886x1920", "video size")
	seconds := flag.Int("seconds", 15, "duration")
	fps := flag.Int("fps", 30, "frame rate")
	rotate := flag.Bool("rotate", false, "switch to the rotated size half-way through (like turning the phone)")
	flag.Parse()

	type segment struct {
		sps, pps []byte
		frames   [][][]byte
	}
	var segs []segment
	if *rotate {
		w, h, _ := strings.Cut(*size, "x")
		for _, sz := range []string{*size, h + "x" + w} {
			sps, pps, frames := encode(sz, *fps, *seconds/2)
			segs = append(segs, segment{sps, pps, frames})
		}
	} else {
		sps, pps, frames := encode(*size, *fps, *seconds)
		segs = append(segs, segment{sps, pps, frames})
	}

	c, err := sender.Dial(*addr)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if err := c.Handshake(); err != nil {
		log.Fatalf("handshake: %v", err)
	}
	m, err := c.StartMirror(uint64(time.Now().UnixNano()))
	if err != nil {
		log.Fatalf("setup: %v", err)
	}
	defer m.Close()
	tick := time.NewTicker(time.Second / time.Duration(*fps))
	for _, s := range segs {
		if err := m.SendCodec(s.sps, s.pps); err != nil {
			log.Fatal(err)
		}
		log.Printf("streaming %d frames", len(s.frames))
		for _, f := range s.frames {
			<-tick.C
			if err := m.SendFrame(f); err != nil {
				log.Fatal(err)
			}
		}
	}
	c.Teardown()
	log.Printf("done")
}
