package airplay

import (
	"encoding/binary"
	"log"
	"net"
	"sync"
	"time"
)

const ntpEpochOffset = 2208988800 // seconds between 1900 and 1970

func ntpNow() uint64 {
	now := time.Now()
	sec := uint64(now.Unix()) + ntpEpochOffset
	frac := (uint64(now.Nanosecond()) << 32) / 1e9
	return sec<<32 | frac
}

// ntpClient implements the AirPlay timing channel: it periodically sends timing
// requests to the client and answers any timing requests the client sends us.
type ntpClient struct {
	conn   *net.UDPConn
	remote *net.UDPAddr
	log    *log.Logger
	once   sync.Once
	quit   chan struct{}
}

func startNTP(remoteIP net.IP, zone string, remotePort int, logger *log.Logger) (*ntpClient, error) {
	conn, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	n := &ntpClient{conn: conn, log: logger, quit: make(chan struct{})}
	if remotePort > 0 {
		n.remote = &net.UDPAddr{IP: remoteIP, Port: remotePort, Zone: zone}
		go n.sendLoop()
	}
	go n.recvLoop()
	return n, nil
}

func (n *ntpClient) localPort() int { return n.conn.LocalAddr().(*net.UDPAddr).Port }

func (n *ntpClient) stop() {
	n.once.Do(func() {
		close(n.quit)
		n.conn.Close()
	})
}

func (n *ntpClient) sendLoop() {
	req := make([]byte, 32)
	req[0], req[1], req[3] = 0x80, 0xd2, 0x07
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		binary.BigEndian.PutUint64(req[24:], ntpNow())
		n.conn.WriteToUDP(req, n.remote)
		select {
		case <-n.quit:
			return
		case <-t.C:
		}
	}
}

func (n *ntpClient) recvLoop() {
	buf := make([]byte, 128)
	for {
		sz, addr, err := n.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		recv := ntpNow()
		if sz >= 32 && buf[1]&0x7f == 0x52 { // timing request: answer it
			res := make([]byte, 32)
			res[0], res[1], res[3] = 0x80, 0xd3, 0x07
			copy(res[8:16], buf[24:32])
			binary.BigEndian.PutUint64(res[16:], recv)
			binary.BigEndian.PutUint64(res[24:], ntpNow())
			n.conn.WriteToUDP(res, addr)
		}
	}
}

// audioSink accepts the AirPlay audio stream so that mirroring can start.
// Audio packets are currently discarded.
type audioSink struct {
	data, control *net.UDPConn
}

func startAudioSink() (*audioSink, error) {
	data, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	control, err := net.ListenUDP("udp", &net.UDPAddr{})
	if err != nil {
		data.Close()
		return nil, err
	}
	a := &audioSink{data: data, control: control}
	go drain(data)
	go drain(control)
	return a, nil
}

func drain(c *net.UDPConn) {
	buf := make([]byte, 2048)
	for {
		if _, _, err := c.ReadFromUDP(buf); err != nil {
			return
		}
	}
}

func (a *audioSink) dataPort() int    { return a.data.LocalAddr().(*net.UDPAddr).Port }
func (a *audioSink) controlPort() int { return a.control.LocalAddr().(*net.UDPAddr).Port }

func (a *audioSink) stop() {
	a.data.Close()
	a.control.Close()
}
