// Package video holds codec helpers for the mirrored H.264/H.265 stream and,
// on Windows, the ffmpeg-based decoder.
package video

import (
	"encoding/binary"
	"fmt"
)

// Codec names double as ffmpeg input format names.
const (
	H264 = "h264"
	HEVC = "hevc"
)

// FrameSize returns the displayed frame size from the SPS among paramSets.
func FrameSize(codec string, paramSets [][]byte) (int, int, error) {
	for _, ps := range paramSets {
		switch {
		case codec == H264 && len(ps) > 0 && ps[0]&0x1f == 7:
			return ParseSPSSize(ps)
		case codec == HEVC && len(ps) > 0 && (ps[0]>>1)&0x3f == 33:
			return ParseHEVCSPSSize(ps)
		}
	}
	return 0, 0, fmt.Errorf("no %s SPS in codec configuration", codec)
}

// AnnexBToAVCC rewrites an Annex-B access unit that uses 4-byte start codes
// (as produced by the airplay package) into 4-byte length-prefixed NAL units,
// in place. Emulation prevention guarantees 00 00 01 never occurs inside a NAL unit.
func AnnexBToAVCC(b []byte) []byte {
	var starts []int
	for i := 0; i+4 <= len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 0 && b[i+3] == 1 {
			starts = append(starts, i)
			i += 3
		}
	}
	if len(starts) == 0 || starts[0] != 0 {
		return nil
	}
	for k, s := range starts {
		end := len(b)
		if k+1 < len(starts) {
			end = starts[k+1]
		}
		binary.BigEndian.PutUint32(b[s:], uint32(end-s-4))
	}
	return b
}
