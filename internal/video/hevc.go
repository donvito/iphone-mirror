package video

import "errors"

// ParseHEVCSPSSize returns the displayed (conformance-cropped) frame size from
// an H.265 SPS NAL unit (including its 2-byte NAL header).
func ParseHEVCSPSSize(nal []byte) (width, height int, err error) {
	if len(nal) < 4 || (nal[0]>>1)&0x3f != 33 {
		return 0, 0, errors.New("not an HEVC SPS NAL unit")
	}
	r := &bitReader{b: unescapeRBSP(nal[2:])}
	must := func(v uint, e error) uint {
		if e != nil && err == nil {
			err = e
		}
		return v
	}
	must(r.bits(4)) // sps_video_parameter_set_id
	maxSubLayersMinus1 := int(must(r.bits(3)))
	must(r.bit()) // temporal_id_nesting

	// profile_tier_level(1, maxSubLayersMinus1)
	must(r.bits(32))
	must(r.bits(32))
	must(r.bits(24)) // general profile (88 bits) ...
	must(r.bits(8))  // ... + general_level_idc
	subProfile := make([]uint, maxSubLayersMinus1)
	subLevel := make([]uint, maxSubLayersMinus1)
	for i := 0; i < maxSubLayersMinus1; i++ {
		subProfile[i] = must(r.bit())
		subLevel[i] = must(r.bit())
	}
	if maxSubLayersMinus1 > 0 {
		for i := maxSubLayersMinus1; i < 8; i++ {
			must(r.bits(2))
		}
	}
	for i := 0; i < maxSubLayersMinus1; i++ {
		if subProfile[i] == 1 {
			must(r.bits(32))
			must(r.bits(32))
			must(r.bits(24))
		}
		if subLevel[i] == 1 {
			must(r.bits(8))
		}
	}

	must(r.ue()) // sps_seq_parameter_set_id
	chroma := must(r.ue())
	separate := uint(0)
	if chroma == 3 {
		separate = must(r.bit())
	}
	w := must(r.ue())
	h := must(r.ue())
	var cl, cr, ct, cb uint
	if must(r.bit()) == 1 {
		cl, cr, ct, cb = must(r.ue()), must(r.ue()), must(r.ue()), must(r.ue())
	}
	if err != nil {
		return 0, 0, err
	}
	subW, subH := uint(1), uint(1)
	if separate == 0 {
		switch chroma {
		case 1:
			subW, subH = 2, 2
		case 2:
			subW = 2
		}
	}
	width = int(w - subW*(cl+cr))
	height = int(h - subH*(ct+cb))
	if width <= 0 || height <= 0 || width > 8192 || height > 8192 {
		return 0, 0, errors.New("hevc sps: implausible frame size")
	}
	return width, height, nil
}
