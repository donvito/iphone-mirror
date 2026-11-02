package video

import (
	"os/exec"
	"testing"
)

func TestParseSPSSize(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	for _, tc := range []struct {
		size, profile string
		w, h          int
	}{
		{"886x1920", "high", 886, 1920},
		{"1920x886", "high", 1920, 886},
		{"498x1080", "main", 498, 1080},
		{"1170x2532", "high", 1170, 2532},
	} {
		out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size="+tc.size,
			"-frames:v", "1", "-c:v", "libx264", "-profile:v", tc.profile, "-pix_fmt", "yuv420p", "-f", "h264", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		var sps []byte
		for i := 0; i+4 < len(out); i++ {
			if out[i] == 0 && out[i+1] == 0 && out[i+2] == 1 && out[i+3]&0x1f == 7 {
				end := i + 4
				for end+3 < len(out) && !(out[end] == 0 && out[end+1] == 0 && (out[end+2] == 1 || out[end+2] == 0)) {
					end++
				}
				sps = out[i+3 : end]
				break
			}
		}
		w, h, err := ParseSPSSize(sps)
		if err != nil || w != tc.w || h != tc.h {
			t.Errorf("%s: got %dx%d, %v", tc.size, w, h, err)
		}
	}
}

func TestParseHEVCSPSSize(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not available")
	}
	for _, tc := range []struct {
		size string
		w, h int
	}{
		{"998x2160", 998, 2160},
		{"2160x998", 2160, 998},
		{"886x1920", 886, 1920},
	} {
		out, err := exec.Command(ffmpeg, "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "testsrc2=size="+tc.size,
			"-frames:v", "1", "-c:v", "libx265", "-x265-params", "log-level=none", "-pix_fmt", "yuv420p", "-f", "hevc", "-").Output()
		if err != nil {
			t.Fatal(err)
		}
		var sps []byte
		for i := 0; i+5 < len(out); i++ {
			if out[i] == 0 && out[i+1] == 0 && out[i+2] == 1 && (out[i+3]>>1)&0x3f == 33 {
				end := i + 5
				for end+3 < len(out) && !(out[end] == 0 && out[end+1] == 0 && (out[end+2] == 1 || out[end+2] == 0)) {
					end++
				}
				sps = out[i+3 : end]
				break
			}
		}
		w, h, err := ParseHEVCSPSSize(sps)
		if err != nil || w != tc.w || h != tc.h {
			t.Errorf("%s: got %dx%d, %v", tc.size, w, h, err)
		}
	}
}
