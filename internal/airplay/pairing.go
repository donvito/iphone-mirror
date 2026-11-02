package airplay

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"errors"
)

// pairing implements AirPlay "legacy" pair-verify (curve25519 + ed25519),
// which yields a shared secret that is mixed into the stream AES key.
type pairing struct {
	identity ed25519.PrivateKey

	ours        *ecdh.PrivateKey
	theirsX     []byte
	theirsEd    ed25519.PublicKey
	secret      []byte
	verifyStage int // 0 = none, 1 = handshake done, 2 = verified
}

func pairKeyIV(secret []byte) (key, iv []byte) {
	k := sha512.Sum512(append([]byte("Pair-Verify-AES-Key"), secret...))
	v := sha512.Sum512(append([]byte("Pair-Verify-AES-IV"), secret...))
	return k[:16], v[:16]
}

func ctr(key, iv []byte) cipher.Stream {
	block, err := aes.NewCipher(key)
	if err != nil {
		panic(err)
	}
	return cipher.NewCTR(block, iv)
}

// verifyStep1 takes the client's curve25519 and ed25519 public keys and returns
// our curve25519 public key followed by our encrypted signature.
func (p *pairing) verifyStep1(clientX, clientEd []byte) ([]byte, error) {
	theirs, err := ecdh.X25519().NewPublicKey(clientX)
	if err != nil {
		return nil, err
	}
	if p.ours, err = ecdh.X25519().GenerateKey(rand.Reader); err != nil {
		return nil, err
	}
	if p.secret, err = p.ours.ECDH(theirs); err != nil {
		return nil, err
	}
	p.theirsX = append([]byte(nil), clientX...)
	p.theirsEd = append(ed25519.PublicKey(nil), clientEd...)
	ourX := p.ours.PublicKey().Bytes()

	sig := ed25519.Sign(p.identity, append(append([]byte(nil), ourX...), clientX...))
	key, iv := pairKeyIV(p.secret)
	ctr(key, iv).XORKeyStream(sig, sig)
	p.verifyStage = 1
	return append(ourX, sig...), nil
}

// verifyStep2 checks the client's encrypted signature.
func (p *pairing) verifyStep2(encSig []byte) error {
	if p.verifyStage != 1 || len(encSig) != 64 {
		return errors.New("pair-verify out of order")
	}
	key, iv := pairKeyIV(p.secret)
	s := ctr(key, iv)
	skip := make([]byte, 64) // the first 64 keystream bytes were used for our own signature
	s.XORKeyStream(skip, skip)
	sig := make([]byte, 64)
	s.XORKeyStream(sig, encSig)
	msg := append(append([]byte(nil), p.theirsX...), p.ours.PublicKey().Bytes()...)
	if !ed25519.Verify(p.theirsEd, msg, sig) {
		return errors.New("pair-verify signature mismatch")
	}
	p.verifyStage = 2
	return nil
}

// sharedSecret returns the ECDH secret if a pair-verify handshake happened.
func (p *pairing) sharedSecret() []byte {
	if p.verifyStage == 0 {
		return nil
	}
	return p.secret
}
