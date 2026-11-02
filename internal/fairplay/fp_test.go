package fairplay

import (
	"encoding/hex"
	"testing"
)

func TestDecryptDeterministic(t *testing.T) {
	var s Session
	fp1 := []byte{0x46, 0x50, 0x4c, 0x59, 0x03, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x04, 0x02, 0x00, 0x02, 0xbb}
	s.Setup(fp1)
	m := make([]byte, 164)
	for i := range m {
		m[i] = byte(i * 7)
	}
	m[4] = 3
	m[12] = 2 // FairPlay mode, must match fp1[14]
	s.Handshake(m)
	ek := make([]byte, 72)
	for i := range ek {
		ek[i] = byte(i * 13)
	}
	a, _ := s.Decrypt(ek)
	b, _ := s.Decrypt(ek)
	// Pinned value: identical at -O0 and -O2, and across repeated calls.
	const want = "8474d42ea2f1423c092d8759b6c51903"
	if hex.EncodeToString(a) != want || hex.EncodeToString(b) != want {
		t.Fatalf("got %x and %x, want %s", a, b, want)
	}
}
