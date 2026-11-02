// Package sender is a fake AirPlay screen mirroring client used for testing
// the receiver without an iPhone. It speaks the same RTSP/pairing/FairPlay
// handshake and sends an H.264 elementary stream as mirror packets.
package sender

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha512"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"time"

	"github.com/melvi/iphone-win-obs/internal/fairplay"
	"howett.net/plist"
)

type Client struct {
	conn net.Conn
	br   *bufio.Reader
	cseq int
	host string

	secret []byte
	fp     fairplay.Session
	aesKey []byte
}

func Dial(addr string) (*Client, error) {
	c, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return nil, err
	}
	host, _, _ := net.SplitHostPort(addr)
	return &Client{conn: c, br: bufio.NewReader(c), host: host}, nil
}

func (c *Client) Close() error { return c.conn.Close() }

type Response struct {
	Status int
	Header textproto.MIMEHeader
	Body   []byte
}

func (c *Client) Do(method, url, contentType string, body []byte) (*Response, error) {
	c.cseq++
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s RTSP/1.0\r\nCSeq: %d\r\nUser-Agent: AirPlay/800.0\r\n", method, url, c.cseq)
	if contentType != "" {
		fmt.Fprintf(&b, "Content-Type: %s\r\n", contentType)
	}
	fmt.Fprintf(&b, "Content-Length: %d\r\n\r\n", len(body))
	b.Write(body)
	if _, err := c.conn.Write(b.Bytes()); err != nil {
		return nil, err
	}
	tp := textproto.NewReader(c.br)
	line, err := tp.ReadLine()
	if err != nil {
		return nil, err
	}
	var proto string
	var status int
	fmt.Sscanf(line, "%s %d", &proto, &status)
	hdr, err := tp.ReadMIMEHeader()
	if err != nil {
		return nil, err
	}
	res := &Response{Status: status, Header: hdr}
	if n, _ := strconv.Atoi(hdr.Get("Content-Length")); n > 0 {
		res.Body = make([]byte, n)
		if _, err := io.ReadFull(c.br, res.Body); err != nil {
			return nil, err
		}
	}
	if status != 200 {
		return res, fmt.Errorf("%s %s: status %d", method, url, status)
	}
	return res, nil
}

func (c *Client) doPlist(method, url string, v any) (map[string]any, error) {
	body, err := plist.Marshal(v, plist.BinaryFormat)
	if err != nil {
		return nil, err
	}
	res, err := c.Do(method, url, "application/x-apple-binary-plist", body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if len(res.Body) > 0 {
		if _, err := plist.Unmarshal(res.Body, &out); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func ctr(key, iv []byte) cipher.Stream {
	b, _ := aes.NewCipher(key)
	return cipher.NewCTR(b, iv)
}

// Handshake performs /info, pair-setup, pair-verify and fp-setup.
func (c *Client) Handshake() error {
	if _, err := c.Do("GET", "/info", "", nil); err != nil {
		return err
	}
	edPub, edPriv, _ := ed25519.GenerateKey(rand.Reader)
	res, err := c.Do("POST", "/pair-setup", "application/octet-stream", edPub)
	if err != nil {
		return err
	}
	serverEd := ed25519.PublicKey(res.Body)

	x, _ := ecdh.X25519().GenerateKey(rand.Reader)
	msg := append(append([]byte{1, 0, 0, 0}, x.PublicKey().Bytes()...), edPub...)
	res, err = c.Do("POST", "/pair-verify", "application/octet-stream", msg)
	if err != nil {
		return err
	}
	if len(res.Body) != 96 {
		return fmt.Errorf("pair-verify: unexpected length %d", len(res.Body))
	}
	serverX, err := ecdh.X25519().NewPublicKey(res.Body[:32])
	if err != nil {
		return err
	}
	c.secret, _ = x.ECDH(serverX)
	k := sha512.Sum512(append([]byte("Pair-Verify-AES-Key"), c.secret...))
	v := sha512.Sum512(append([]byte("Pair-Verify-AES-IV"), c.secret...))
	s := ctr(k[:16], v[:16])
	sig := make([]byte, 64)
	s.XORKeyStream(sig, res.Body[32:])
	if !ed25519.Verify(serverEd, append(res.Body[:32:32], x.PublicKey().Bytes()...), sig) {
		return fmt.Errorf("pair-verify: bad server signature")
	}
	mySig := ed25519.Sign(edPriv, append(x.PublicKey().Bytes(), res.Body[:32]...))
	s.XORKeyStream(mySig, mySig)
	if _, err := c.Do("POST", "/pair-verify", "application/octet-stream", append([]byte{0, 0, 0, 0}, mySig...)); err != nil {
		return err
	}

	fp1 := []byte{0x46, 0x50, 0x4c, 0x59, 0x03, 0x01, 0x01, 0x00, 0x00, 0x00, 0x00, 0x04, 0x02, 0x00, 0x02, 0xbb}
	if res, err = c.Do("POST", "/fp-setup", "application/octet-stream", fp1); err != nil {
		return err
	}
	if len(res.Body) != 142 {
		return fmt.Errorf("fp-setup 1: unexpected length %d", len(res.Body))
	}
	fp2 := make([]byte, 164)
	rand.Read(fp2)
	copy(fp2, []byte{0x46, 0x50, 0x4c, 0x59, 0x03, 0x01, 0x03, 0x00, 0x00, 0x00, 0x00, 0x98, fp1[14]})
	if res, err = c.Do("POST", "/fp-setup", "application/octet-stream", fp2); err != nil {
		return err
	}
	if len(res.Body) != 32 || !bytes.Equal(res.Body[12:], fp2[144:]) {
		return fmt.Errorf("fp-setup 2: unexpected reply")
	}
	c.fp.Setup(fp1)
	c.fp.Handshake(fp2)
	return nil
}

// StartMirror sends the SETUP requests and returns the mirror data connection.
func (c *Client) StartMirror(streamConnectionID uint64) (*Mirror, error) {
	ekey := make([]byte, 72)
	eiv := make([]byte, 16)
	rand.Read(ekey)
	rand.Read(eiv)
	// A real sender encrypts its key with FairPlay; here we just use whatever the
	// receiver will decrypt it to, which exercises exactly the same code path.
	key, err := c.fp.Decrypt(ekey)
	if err != nil {
		return nil, err
	}
	h := sha512.Sum512(append(key, c.secret...))
	c.aesKey = h[:16]

	if _, err := c.doPlist("SETUP", "rtsp://"+c.host+"/1", map[string]any{
		"ekey": ekey, "eiv": eiv, "timingProtocol": "NTP", "timingPort": uint64(7011),
		"deviceID": "AA:BB:CC:DD:EE:FF", "name": "Fake iPhone", "model": "iPhone16,2",
	}); err != nil {
		return nil, err
	}
	out, err := c.doPlist("SETUP", "rtsp://"+c.host+"/1", map[string]any{
		"streams": []any{map[string]any{"type": uint64(110), "streamConnectionID": streamConnectionID}},
	})
	if err != nil {
		return nil, err
	}
	streams, _ := out["streams"].([]any)
	if len(streams) != 1 {
		return nil, fmt.Errorf("SETUP: unexpected streams %v", out)
	}
	port := streams[0].(map[string]any)["dataPort"].(uint64)
	if _, err := c.Do("RECORD", "rtsp://"+c.host+"/1", "", nil); err != nil {
		return nil, err
	}
	dc, err := net.Dial("tcp", net.JoinHostPort(c.host, strconv.Itoa(int(port))))
	if err != nil {
		return nil, err
	}
	id := strconv.FormatUint(streamConnectionID, 10)
	k := sha512.Sum512(append([]byte("AirPlayStreamKey"+id), c.aesKey...))
	v := sha512.Sum512(append([]byte("AirPlayStreamIV"+id), c.aesKey...))
	return &Mirror{conn: dc, stream: ctr(k[:16], v[:16])}, nil
}

func (c *Client) Teardown() error {
	_, err := c.doPlist("TEARDOWN", "rtsp://"+c.host+"/1", map[string]any{
		"streams": []any{map[string]any{"type": uint64(110)}},
	})
	return err
}

type Mirror struct {
	conn   net.Conn
	stream cipher.Stream
}

func (m *Mirror) Close() error { return m.conn.Close() }

func (m *Mirror) send(typ byte, payload []byte) error {
	hdr := make([]byte, 128)
	binary.LittleEndian.PutUint32(hdr, uint32(len(payload)))
	hdr[4] = typ
	binary.LittleEndian.PutUint64(hdr[8:], uint64(time.Now().UnixNano()))
	_, err := m.conn.Write(append(hdr, payload...))
	return err
}

// SendCodec sends the SPS/PPS as an avcC record (unencrypted, type 1).
func (m *Mirror) SendCodec(sps, pps []byte) error {
	b := []byte{1, sps[1], sps[2], sps[3], 0xff, 0xe1, byte(len(sps) >> 8), byte(len(sps))}
	b = append(b, sps...)
	b = append(b, 1, byte(len(pps)>>8), byte(len(pps)))
	b = append(b, pps...)
	return m.send(1, b)
}

// SendCodecHEVC sends VPS/SPS/PPS the way an iPhone does: an 'hvc1' sample
// entry box containing an 'hvcC' (HEVCDecoderConfigurationRecord) box.
func (m *Mirror) SendCodecHEVC(vps, sps, pps []byte) error {
	rec := make([]byte, 22)
	rec[0] = 1 // configurationVersion
	rec = append(rec, 3)
	for i, n := range [][]byte{vps, sps, pps} {
		rec = append(rec, 0x80|byte(32+i), 0, 1, byte(len(n)>>8), byte(len(n)))
		rec = append(rec, n...)
	}
	hvcC := binary.BigEndian.AppendUint32(nil, uint32(8+len(rec)))
	hvcC = append(append(hvcC, "hvcC"...), rec...)
	entry := binary.BigEndian.AppendUint32(nil, uint32(8+78+len(hvcC)))
	entry = append(append(append(entry, "hvc1"...), make([]byte, 78)...), hvcC...)
	return m.send(1, entry)
}

// SendFrame sends NAL units as an encrypted, length-prefixed type 0 packet.
func (m *Mirror) SendFrame(nals [][]byte) error {
	var b []byte
	for _, n := range nals {
		b = binary.BigEndian.AppendUint32(b, uint32(len(n)))
		b = append(b, n...)
	}
	m.stream.XORKeyStream(b, b)
	return m.send(0, b)
}

// SplitAnnexB splits an H.264/H.265 elementary stream into NAL units.
func SplitAnnexB(b []byte) [][]byte {
	var nals [][]byte
	start := -1
	for i := 0; i+2 < len(b); i++ {
		if b[i] == 0 && b[i+1] == 0 && b[i+2] == 1 {
			if start >= 0 {
				end := i
				if end > start && b[end-1] == 0 {
					end--
				}
				nals = append(nals, b[start:end])
			}
			start = i + 3
			i += 2
		}
	}
	if start >= 0 && start < len(b) {
		nals = append(nals, b[start:])
	}
	return nals
}

// AccessUnits groups NAL units into frames: each frame ends with a VCL NAL unit.
// SPS/PPS are returned separately.
func AccessUnits(nals [][]byte) (sps, pps []byte, frames [][][]byte) {
	var cur [][]byte
	for _, n := range nals {
		switch n[0] & 0x1f {
		case 7:
			sps = n
		case 8:
			pps = n
		case 9: // access unit delimiter
		case 1, 5:
			cur = append(cur, n)
			frames = append(frames, cur)
			cur = nil
		default:
			cur = append(cur, n)
		}
	}
	return
}

// HEVCAccessUnits is AccessUnits for H.265: parameter sets are returned
// separately and each frame ends with a VCL NAL unit.
func HEVCAccessUnits(nals [][]byte) (vps, sps, pps []byte, frames [][][]byte) {
	var cur [][]byte
	for _, n := range nals {
		switch t := (n[0] >> 1) & 0x3f; {
		case t == 32:
			vps = n
		case t == 33:
			sps = n
		case t == 34:
			pps = n
		case t == 35: // access unit delimiter
		case t < 32:
			cur = append(cur, n)
			frames = append(frames, cur)
			cur = nil
		default:
			cur = append(cur, n)
		}
	}
	return
}
