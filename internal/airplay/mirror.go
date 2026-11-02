package airplay

import (
	"crypto/cipher"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// mirrorStream receives the screen mirroring video over its own TCP connection.
// Each packet has a 128-byte header followed by the payload:
//
//	header[0:4]  payload size (little endian)
//	header[4:6]  type: 0 = encrypted video (AVCC NAL units), 1 = codec config (avcC), 2/5 = heartbeat/report
//	header[8:16] NTP timestamp
type mirrorStream struct {
	s      *Server
	ln     net.Listener
	stream cipher.Stream
	client string

	mu      sync.Mutex
	conn    net.Conn
	stopped bool
	done    chan struct{}
}

func streamKeys(aesKey []byte, streamConnectionID uint64) (key, iv []byte) {
	id := fmt.Sprintf("%d", streamConnectionID)
	k := sha512.Sum512(append([]byte("AirPlayStreamKey"+id), aesKey...))
	v := sha512.Sum512(append([]byte("AirPlayStreamIV"+id), aesKey...))
	return k[:16], v[:16]
}

func startMirror(s *Server, aesKey []byte, streamConnectionID uint64, client string) (*mirrorStream, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, err
	}
	key, iv := streamKeys(aesKey, streamConnectionID)
	m := &mirrorStream{s: s, ln: ln, stream: ctr(key, iv), client: client, done: make(chan struct{})}
	go m.run()
	return m, nil
}

func (m *mirrorStream) port() int { return m.ln.Addr().(*net.TCPAddr).Port }

func (m *mirrorStream) stop() {
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return
	}
	m.stopped = true
	m.ln.Close()
	if m.conn != nil {
		m.conn.Close()
	}
	m.mu.Unlock()
	<-m.done
}

func (m *mirrorStream) active() bool {
	m.s.mu.Lock()
	defer m.s.mu.Unlock()
	return m.s.activeMirror == m
}

func (m *mirrorStream) run() {
	defer close(m.done)
	log := m.s.cfg.Log
	c, err := m.ln.Accept()
	m.ln.Close()
	if err != nil {
		return
	}
	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		c.Close()
		return
	}
	m.conn = c
	m.mu.Unlock()
	if tc, ok := c.(*net.TCPConn); ok {
		tc.SetKeepAlive(true)
		tc.SetKeepAlivePeriod(10 * time.Second)
		tc.SetReadBuffer(4 << 20)
	}

	log.Printf("mirror stream connected from %s", m.client)
	sink := m.s.sink
	if m.active() {
		sink.MirrorStarted(m.client)
	}
	defer func() {
		c.Close()
		m.s.mu.Lock()
		emit := m.s.activeMirror == m || m.s.activeMirror == nil
		m.s.mu.Unlock()
		if emit {
			sink.MirrorStopped()
		}
		log.Printf("mirror stream from %s ended", m.client)
	}()

	header := make([]byte, 128)
	var payload, out []byte
	for {
		if _, err := io.ReadFull(c, header); err != nil {
			return
		}
		size := int(binary.LittleEndian.Uint32(header[0:4]))
		if size < 0 || size > 32<<20 {
			log.Printf("mirror: invalid payload size %d", size)
			return
		}
		if cap(payload) < size {
			payload = make([]byte, size)
		}
		payload = payload[:size]
		if _, err := io.ReadFull(c, payload); err != nil {
			return
		}

		switch header[4] {
		case 0: // encrypted video
			m.stream.XORKeyStream(payload, payload)
			var err error
			out, err = avccToAnnexB(out[:0], payload)
			if err != nil {
				log.Printf("mirror: %v", err)
				continue
			}
			if m.active() {
				sink.VideoData(out)
			}
		case 1: // codec configuration (avcC)
			if size == 0 {
				log.Printf("mirror: client sent a non-H.264 stream, which is not supported")
				continue
			}
			sps, pps, err := parseAVCC(payload)
			if err != nil {
				log.Printf("mirror: %v", err)
				continue
			}
			if m.active() {
				sink.CodecConfig(sps, pps)
			}
		}
	}
}

var startCode = []byte{0, 0, 0, 1}

// avccToAnnexB converts 4-byte length prefixed NAL units to start-code prefixed ones.
func avccToAnnexB(dst, src []byte) ([]byte, error) {
	for len(src) > 0 {
		if len(src) < 4 {
			return dst, errors.New("truncated NAL length")
		}
		n := int(binary.BigEndian.Uint32(src))
		src = src[4:]
		if n > len(src) {
			return dst, fmt.Errorf("NAL length %d exceeds payload %d", n, len(src))
		}
		dst = append(dst, startCode...)
		dst = append(dst, src[:n]...)
		src = src[n:]
	}
	return dst, nil
}

// parseAVCC extracts the first SPS and PPS from an AVCDecoderConfigurationRecord.
func parseAVCC(b []byte) (sps, pps []byte, err error) {
	if len(b) < 8 || b[0] != 1 {
		return nil, nil, errors.New("invalid avcC record")
	}
	spsLen := int(binary.BigEndian.Uint16(b[6:8]))
	if len(b) < 8+spsLen+3 {
		return nil, nil, errors.New("truncated avcC SPS")
	}
	sps = b[8 : 8+spsLen]
	p := b[8+spsLen:]
	ppsLen := int(binary.BigEndian.Uint16(p[1:3]))
	if len(p) < 3+ppsLen {
		return nil, nil, errors.New("truncated avcC PPS")
	}
	pps = p[3 : 3+ppsLen]
	return append([]byte(nil), sps...), append([]byte(nil), pps...), nil
}
