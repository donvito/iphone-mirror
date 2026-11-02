package video

import "errors"

type bitReader struct {
	b   []byte
	pos int
}

func (r *bitReader) bit() (uint, error) {
	if r.pos >= len(r.b)*8 {
		return 0, errors.New("sps: out of data")
	}
	v := (r.b[r.pos/8] >> (7 - uint(r.pos%8))) & 1
	r.pos++
	return uint(v), nil
}

func (r *bitReader) bits(n int) (uint, error) {
	var v uint
	for i := 0; i < n; i++ {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		v = v<<1 | b
	}
	return v, nil
}

func (r *bitReader) ue() (uint, error) {
	zeros := 0
	for {
		b, err := r.bit()
		if err != nil {
			return 0, err
		}
		if b == 1 {
			break
		}
		zeros++
		if zeros > 31 {
			return 0, errors.New("sps: invalid exp-golomb")
		}
	}
	v, err := r.bits(zeros)
	return (1 << zeros) - 1 + v, err
}

func (r *bitReader) se() (int, error) {
	v, err := r.ue()
	if v&1 == 1 {
		return int(v+1) / 2, err
	}
	return -int(v / 2), err
}

// unescapeRBSP removes emulation prevention bytes (00 00 03 -> 00 00).
func unescapeRBSP(b []byte) []byte {
	out := make([]byte, 0, len(b))
	zeros := 0
	for _, c := range b {
		if zeros >= 2 && c == 3 {
			zeros = 0
			continue
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

// ParseSPSSize returns the displayed (cropped) frame size from an H.264 SPS NAL unit.
func ParseSPSSize(nal []byte) (width, height int, err error) {
	if len(nal) < 4 || nal[0]&0x1f != 7 {
		return 0, 0, errors.New("not an SPS NAL unit")
	}
	r := &bitReader{b: unescapeRBSP(nal[1:])}
	must := func(v uint, e error) uint {
		if e != nil && err == nil {
			err = e
		}
		return v
	}
	profile := must(r.bits(8))
	must(r.bits(16)) // constraint flags + level
	must(r.ue())     // sps id
	chromaFormat := uint(1)
	separateColourPlane := uint(0)
	switch profile {
	case 100, 110, 122, 244, 44, 83, 86, 118, 128, 138, 139, 134, 135:
		chromaFormat = must(r.ue())
		if chromaFormat == 3 {
			separateColourPlane = must(r.bit())
		}
		must(r.ue())            // bit depth luma
		must(r.ue())            // bit depth chroma
		must(r.bit())           // qpprime_y_zero_transform_bypass
		if must(r.bit()) == 1 { // seq_scaling_matrix_present
			n := 8
			if chromaFormat == 3 {
				n = 12
			}
			for i := 0; i < n; i++ {
				if must(r.bit()) == 0 {
					continue
				}
				size := 16
				if i >= 6 {
					size = 64
				}
				last, next := 8, 8
				for j := 0; j < size; j++ {
					if next != 0 {
						d, e := r.se()
						if e != nil {
							return 0, 0, e
						}
						next = (last + d + 256) % 256
					}
					if next != 0 {
						last = next
					}
				}
			}
		}
	}
	must(r.ue()) // log2_max_frame_num
	pocType := must(r.ue())
	switch pocType {
	case 0:
		must(r.ue())
	case 1:
		must(r.bit())
		r.se()
		r.se()
		n := must(r.ue())
		for i := uint(0); i < n && err == nil; i++ {
			r.se()
		}
	}
	must(r.ue())  // max_num_ref_frames
	must(r.bit()) // gaps_in_frame_num_allowed
	mbW := must(r.ue()) + 1
	mapH := must(r.ue()) + 1
	frameMbsOnly := must(r.bit())
	if frameMbsOnly == 0 {
		must(r.bit()) // mb_adaptive_frame_field
	}
	must(r.bit()) // direct_8x8_inference
	var cl, cr, ct, cb uint
	if must(r.bit()) == 1 {
		cl, cr, ct, cb = must(r.ue()), must(r.ue()), must(r.ue()), must(r.ue())
	}
	if err != nil {
		return 0, 0, err
	}
	cropX, cropY := uint(1), 2-frameMbsOnly
	if separateColourPlane == 0 && chromaFormat != 0 {
		subW, subH := uint(2), uint(2)
		if chromaFormat == 2 {
			subH = 1
		} else if chromaFormat == 3 {
			subW, subH = 1, 1
		}
		cropX, cropY = subW, subH*(2-frameMbsOnly)
	}
	width = int(mbW*16 - cropX*(cl+cr))
	height = int((2-frameMbsOnly)*mapH*16 - cropY*(ct+cb))
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 {
		return 0, 0, errors.New("sps: implausible frame size")
	}
	return width, height, nil
}
