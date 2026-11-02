package video

import (
	"bytes"
	"testing"
)

func TestAnnexBToAVCC(t *testing.T) {
	in := []byte{0, 0, 0, 1, 0x65, 0xAA, 0, 0, 3, 0, 0, 0, 0, 1, 0x41, 0xBB}
	want := []byte{0, 0, 0, 6, 0x65, 0xAA, 0, 0, 3, 0, 0, 0, 0, 2, 0x41, 0xBB}
	if got := AnnexBToAVCC(append([]byte(nil), in...)); !bytes.Equal(got, want) {
		t.Fatalf("got % x, want % x", got, want)
	}
	if got := AnnexBToAVCC([]byte{0x65, 0, 0, 0, 1, 0x41}); got != nil {
		t.Fatalf("expected nil for data not starting with a start code, got % x", got)
	}
}
