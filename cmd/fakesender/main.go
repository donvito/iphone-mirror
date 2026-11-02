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

type segment struct {
	paramSets [][]byte // H.264: SPS, PPS; HEVC: VPS, SPS, PPS
	frames    [][][]byte
}

func encode(codec, size string, fps, seconds int) segment {
	encoder := map[string]string{"h264": "libx264", "hevc": "libx265"}[codec]
	stream, err := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "error",
		"-f", "lavfi", "-i", "testsrc2=size="+size+":rate="+strconv.Itoa(fps),
		"-t", strconv.Itoa(seconds), "-c:v", encoder, "-bf", "0", "-g", "60", "-x265-params", "log-level=none",
		"-pix_fmt", "yuv420p", "-f", codec, "-").Output()
	if err != nil {
		log.Fatalf("ffmpeg: %v", err)
	}
	nals := sender.SplitAnnexB(stream)
	if codec == "hevc" {
		vps, sps, pps, frames := sender.HEVCAccessUnits(nals)
		return segment{[][]byte{vps, sps, pps}, frames}
	}
	sps, pps, frames := sender.AccessUnits(nals)
	return segment{[][]byte{sps, pps}, frames}
}

func main() {
	addr := flag.String("addr", "127.0.0.1:7000", "receiver address")
	size := flag.String("size", "886x1920", "video size")
	seconds := flag.Int("seconds", 15, "duration")
	fps := flag.Int("fps", 30, "frame rate")
	rotate := flag.Bool("rotate", false, "switch to the rotated size half-way through (like turning the phone)")
	codec := flag.String("codec", "h264", "h264 or hevc")
	flag.Parse()

	var segs []segment
	if *rotate {
		w, h, _ := strings.Cut(*size, "x")
		for _, sz := range []string{*size, h + "x" + w} {
			segs = append(segs, encode(*codec, sz, *fps, *seconds/2))
		}
	} else {
		segs = append(segs, encode(*codec, *size, *fps, *seconds))
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
		var err error
		if *codec == "hevc" {
			err = m.SendCodecHEVC(s.paramSets[0], s.paramSets[1], s.paramSets[2])
		} else {
			err = m.SendCodec(s.paramSets[0], s.paramSets[1])
		}
		if err != nil {
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
