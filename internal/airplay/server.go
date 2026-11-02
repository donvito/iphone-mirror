// Package airplay implements a minimal AirPlay (v1-style) screen mirroring
// receiver, modelled on the open-source UxPlay/RPiPlay implementations.
package airplay

import (
	"bufio"
	"crypto/ed25519"
	"crypto/sha512"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"

	"github.com/melvi/iphone-win-obs/internal/fairplay"
	"howett.net/plist"
)

const (
	model         = "AppleTV3,2"
	sourceVersion = "220.68"
	featuresLow   = 0x5A7FFEE6 // includes screen mirroring + legacy pairing (bit 27)
	featuresHigh  = 0x0
	pi            = "2e388006-13ba-4041-9a67-25dd4a43d536"
)

// Sink receives the decoded-from-network (but still compressed) video stream.
type Sink interface {
	MirrorStarted(client string)
	CodecConfig(sps, pps []byte) // raw H.264 NAL units, without start codes
	VideoData(annexB []byte)     // one access unit in Annex-B format
	MirrorStopped()
}

type Config struct {
	Name     string
	HWAddr   net.HardwareAddr // 6 bytes, used as the AirPlay device ID
	Identity ed25519.PrivateKey
	Width    int
	Height   int
	FPS      int
	Port     int
	Log      *log.Logger
}

type Server struct {
	cfg  Config
	sink Sink
	ln   net.Listener

	mu           sync.Mutex
	activeMirror *mirrorStream
}

func NewServer(cfg Config, sink Sink) *Server {
	if cfg.Port == 0 {
		cfg.Port = 7000
	}
	if cfg.Log == nil {
		cfg.Log = log.Default()
	}
	return &Server{cfg: cfg, sink: sink}
}

func (s *Server) Port() int { return s.cfg.Port }

func (s *Server) DeviceID() string {
	return strings.ToUpper(s.cfg.HWAddr.String())
}

func (s *Server) publicKey() []byte {
	return s.cfg.Identity.Public().(ed25519.PublicKey)
}

func (s *Server) features() string {
	return fmt.Sprintf("0x%X,0x%X", featuresLow, featuresHigh)
}

// AirPlayTXT returns the TXT record entries for the _airplay._tcp service.
func (s *Server) AirPlayTXT() []string {
	return []string{
		"deviceid=" + s.DeviceID(),
		"features=" + s.features(),
		"pw=false",
		"flags=0x4",
		"model=" + model,
		"pk=" + hex.EncodeToString(s.publicKey()),
		"pi=" + pi,
		"srcvers=" + sourceVersion,
		"vv=2",
	}
}

// RAOPTXT returns the TXT record entries for the _raop._tcp service.
func (s *Server) RAOPTXT() []string {
	return []string{
		"ch=2", "cn=0,1,2,3", "da=true", "et=0,3,5", "vv=2",
		"ft=" + s.features(),
		"am=" + model, "md=0,1,2", "rhd=5.6.0.0", "pw=false", "sf=0x4",
		"sr=44100", "ss=16", "sv=false", "tp=UDP", "txtvers=1",
		"vs=" + sourceVersion, "vn=65537",
		"pk=" + hex.EncodeToString(s.publicKey()),
	}
}

// RAOPInstanceName is the "<hwaddr>@<name>" instance name used for _raop._tcp.
func (s *Server) RAOPInstanceName() string {
	return strings.ToUpper(hex.EncodeToString(s.cfg.HWAddr)) + "@" + s.cfg.Name
}

func txtBytes(entries []string) []byte {
	var b []byte
	for _, e := range entries {
		b = append(b, byte(len(e)))
		b = append(b, e...)
	}
	return b
}

func (s *Server) Listen() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", s.cfg.Port))
	if err != nil {
		return err
	}
	s.ln = ln
	return nil
}

func (s *Server) Serve() error {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveConn(c)
	}
}

func (s *Server) Close() error {
	if s.ln != nil {
		return s.ln.Close()
	}
	return nil
}

// conn is the state of one RTSP control connection from an AirPlay client.
type conn struct {
	s      *Server
	nc     net.Conn
	remote net.IP
	fp     fairplay.Session
	pair   pairing

	aesKey []byte
	aesIV  []byte
	ntp    *ntpClient
	mirror *mirrorStream
	audio  *audioSink
}

func (s *Server) serveConn(nc net.Conn) {
	c := &conn{s: s, nc: nc, pair: pairing{identity: s.cfg.Identity}}
	if ta, ok := nc.RemoteAddr().(*net.TCPAddr); ok {
		c.remote = ta.IP
	}
	defer c.close()
	br := bufio.NewReader(nc)
	bw := bufio.NewWriter(nc)
	for {
		req, err := readRequest(br)
		if err != nil {
			return
		}
		res := c.handle(req)
		if err := writeResponse(bw, req.Protocol, res); err != nil || res.Disconnect {
			return
		}
	}
}

func (c *conn) close() {
	c.nc.Close()
	c.stopMirror()
	c.stopAudio()
	if c.ntp != nil {
		c.ntp.stop()
		c.ntp = nil
	}
}

func (c *conn) logf(format string, args ...any) {
	c.s.cfg.Log.Printf("[%s] "+format, append([]any{c.remote}, args...)...)
}

func (c *conn) handle(req *request) *response {
	res := newResponse()
	cseq := req.Header.Get("CSeq")
	if cseq != "" && req.Method != "RECORD" {
		res.addHeader("Audio-Jack-Status", "connected; type=digital")
	}
	if req.URL != "/feedback" {
		c.logf("%s %s", req.Method, req.URL)
	}

	var err error
	switch {
	case req.Method == "GET" && (req.URL == "/info" || strings.HasPrefix(req.URL, "/info?")):
		err = c.handleInfo(req, res)
	case req.Method == "POST" && req.URL == "/pair-setup":
		err = c.handlePairSetup(req, res)
	case req.Method == "POST" && req.URL == "/pair-verify":
		err = c.handlePairVerify(req, res)
	case req.Method == "POST" && req.URL == "/fp-setup":
		err = c.handleFPSetup(req, res)
	case req.Method == "POST" && (req.URL == "/feedback" || req.URL == "/audioMode"):
		// heartbeat / audio mode: nothing to do
	case req.Method == "OPTIONS":
		res.addHeader("Public", "SETUP, RECORD, FLUSH, TEARDOWN, OPTIONS, GET_PARAMETER, SET_PARAMETER")
	case req.Method == "SETUP":
		err = c.handleSetup(req, res)
	case req.Method == "RECORD":
		res.addHeader("Audio-Latency", "0")
		res.addHeader("Audio-Jack-Status", "connected; type=analog")
	case req.Method == "GET_PARAMETER":
		if strings.Contains(string(req.Body), "volume") {
			res.addHeader("Content-Type", "text/parameters")
			res.Body = []byte("volume: 0.000000\r\n")
		}
	case req.Method == "SET_PARAMETER", req.Method == "FLUSH":
		// volume / metadata / progress: ignored
	case req.Method == "TEARDOWN":
		err = c.handleTeardown(req, res)
	default:
		c.logf("unhandled request %s %s", req.Method, req.URL)
		res.setStatus(404, "Not Found")
	}
	if err != nil {
		c.logf("error handling %s %s: %v", req.Method, req.URL, err)
		if res.Status == 200 {
			res.setStatus(400, "Bad Request")
		}
	}
	res.addHeader("Server", "AirTunes/"+sourceVersion)
	if cseq != "" {
		res.addHeader("CSeq", cseq)
	}
	return res
}

func setPlist(res *response, v any) error {
	b, err := plist.Marshal(v, plist.BinaryFormat)
	if err != nil {
		return err
	}
	res.addHeader("Content-Type", "application/x-apple-binary-plist")
	res.Body = b
	return nil
}

func (c *conn) handleInfo(req *request, res *response) error {
	s := c.s
	out := map[string]any{}
	if strings.Contains(req.Header.Get("Content-Type"), "apple-binary-plist") {
		var body map[string]any
		if _, err := plist.Unmarshal(req.Body, &body); err == nil {
			if q, ok := body["qualifier"].([]any); ok && len(q) > 0 {
				switch q[0] {
				case "txtAirPlay":
					out["txtAirPlay"] = txtBytes(s.AirPlayTXT())
				case "txtRAOP":
					out["txtRAOP"] = txtBytes(s.RAOPTXT())
				}
			}
		}
		return setPlist(res, out)
	}
	if req.Header.Get("CSeq") == "" { // Bluetooth LE discovery: GET /info?txtAirPlay&txtRAOP
		if strings.Contains(req.URL, "txtAirPlay") {
			out["txtAirPlay"] = txtBytes(s.AirPlayTXT())
		}
		if strings.Contains(req.URL, "txtRAOP") {
			out["txtRAOP"] = txtBytes(s.RAOPTXT())
		}
	}
	out["deviceID"] = s.DeviceID()
	out["macAddress"] = s.DeviceID()
	out["pk"] = s.publicKey()
	out["features"] = uint64(featuresHigh)<<32 | featuresLow
	out["name"] = s.cfg.Name
	out["pi"] = pi
	out["vv"] = uint64(2)
	out["statusFlags"] = uint64(68)
	out["keepAliveLowPower"] = true
	out["sourceVersion"] = sourceVersion
	out["keepAliveSendStatsAsBody"] = true
	out["model"] = model
	if req.Header.Get("CSeq") == "" {
		return setPlist(res, out)
	}
	out["initialVolume"] = 0.0
	latency := func(t uint64) map[string]any {
		return map[string]any{"type": t, "audioType": "default", "inputLatencyMicros": uint64(0), "outputLatencyMicros": false}
	}
	out["audioLatencies"] = []any{latency(100), latency(101)}
	format := func(t uint64) map[string]any {
		return map[string]any{"type": t, "audioInputFormats": uint64(0x3fffffc), "audioOutputFormats": uint64(0x3fffffc)}
	}
	out["audioFormats"] = []any{format(100), format(101)}
	out["displays"] = []any{map[string]any{
		"uuid":           "e0ff8a27-6738-3d56-8a16-cc53aacee925",
		"widthPhysical":  uint64(0),
		"heightPhysical": uint64(0),
		"width":          uint64(s.cfg.Width),
		"height":         uint64(s.cfg.Height),
		"widthPixels":    uint64(s.cfg.Width),
		"heightPixels":   uint64(s.cfg.Height),
		"rotation":       false,
		"refreshRate":    1.0 / float64(s.cfg.FPS),
		"maxFPS":         uint64(s.cfg.FPS),
		"overscanned":    false,
		"features":       uint64(14),
	}}
	return setPlist(res, out)
}

func (c *conn) handlePairSetup(req *request, res *response) error {
	if len(req.Body) != 32 {
		return fmt.Errorf("invalid pair-setup length %d", len(req.Body))
	}
	res.addHeader("Content-Type", "application/octet-stream")
	res.Body = append([]byte(nil), c.s.publicKey()...)
	return nil
}

func (c *conn) handlePairVerify(req *request, res *response) error {
	d := req.Body
	if len(d) < 4 {
		return errors.New("short pair-verify")
	}
	res.addHeader("Content-Type", "application/octet-stream")
	switch d[0] {
	case 1:
		if len(d) != 4+32+32 {
			return errors.New("invalid pair-verify step 1 length")
		}
		out, err := c.pair.verifyStep1(d[4:36], d[36:68])
		if err != nil {
			return err
		}
		res.Body = out
	case 0:
		if len(d) != 4+64 {
			return errors.New("invalid pair-verify step 2 length")
		}
		if err := c.pair.verifyStep2(d[4:]); err != nil {
			res.Disconnect = true
			return err
		}
	}
	return nil
}

func (c *conn) handleFPSetup(req *request, res *response) error {
	var out []byte
	var err error
	switch len(req.Body) {
	case 16:
		out, err = c.fp.Setup(req.Body)
	case 164:
		out, err = c.fp.Handshake(req.Body)
	default:
		err = fmt.Errorf("invalid fp-setup length %d", len(req.Body))
	}
	if err != nil {
		return err
	}
	res.addHeader("Content-Type", "application/octet-stream")
	res.Body = out
	return nil
}

func toUint(v any) uint64 {
	switch n := v.(type) {
	case uint64:
		return n
	case int64:
		return uint64(n)
	case int:
		return uint64(n)
	case float64:
		return uint64(n)
	}
	return 0
}

func (c *conn) handleSetup(req *request, res *response) error {
	var body map[string]any
	if _, err := plist.Unmarshal(req.Body, &body); err != nil {
		return fmt.Errorf("parse SETUP plist: %w", err)
	}
	out := map[string]any{}

	ekey, hasKey := body["ekey"].([]byte)
	eiv, hasIV := body["eiv"].([]byte)
	if hasKey && hasIV {
		if len(ekey) != 72 || len(eiv) != 16 {
			return errors.New("invalid ekey/eiv size")
		}
		key, err := c.fp.Decrypt(ekey)
		if err != nil {
			return err
		}
		if secret := c.pair.sharedSecret(); secret != nil {
			h := sha512.Sum512(append(append([]byte(nil), key...), secret...))
			key = h[:16]
		}
		c.aesKey, c.aesIV = key, eiv
		c.logf("client %v (%v) starting session", body["name"], body["model"])

		if c.ntp != nil {
			c.ntp.stop()
		}
		c.ntp, err = startNTP(c.remote, int(toUint(body["timingPort"])), c.s.cfg.Log)
		if err != nil {
			return err
		}
		out["timingPort"] = uint64(c.ntp.localPort())
		out["eventPort"] = uint64(0)
	}

	if streams, ok := body["streams"].([]any); ok {
		var resStreams []any
		for _, st := range streams {
			sm, _ := st.(map[string]any)
			switch toUint(sm["type"]) {
			case 110:
				if c.aesKey == nil {
					return errors.New("mirror SETUP before key exchange")
				}
				c.stopMirror()
				m, err := startMirror(c.s, c.aesKey, toUint(sm["streamConnectionID"]), c.remote.String())
				if err != nil {
					return err
				}
				c.mirror = m
				c.s.setActiveMirror(m)
				resStreams = append(resStreams, map[string]any{"type": uint64(110), "dataPort": uint64(m.port())})
			case 96:
				c.stopAudio()
				a, err := startAudioSink()
				if err != nil {
					return err
				}
				c.audio = a
				resStreams = append(resStreams, map[string]any{
					"type": uint64(96), "dataPort": uint64(a.dataPort()), "controlPort": uint64(a.controlPort()),
				})
			default:
				c.logf("ignoring unknown stream type %v", sm["type"])
			}
		}
		out["streams"] = resStreams
	}
	return setPlist(res, out)
}

func (c *conn) handleTeardown(req *request, res *response) error {
	var body map[string]any
	plist.Unmarshal(req.Body, &body)
	streams, _ := body["streams"].([]any)
	if len(streams) == 0 {
		c.stopMirror()
		c.stopAudio()
		res.Disconnect = true
		return nil
	}
	for _, st := range streams {
		sm, _ := st.(map[string]any)
		switch toUint(sm["type"]) {
		case 110:
			c.stopMirror()
		case 96:
			c.stopAudio()
		}
	}
	return nil
}

func (c *conn) stopMirror() {
	if c.mirror != nil {
		c.mirror.stop()
		c.s.clearActiveMirror(c.mirror)
		c.mirror = nil
	}
}

func (c *conn) stopAudio() {
	if c.audio != nil {
		c.audio.stop()
		c.audio = nil
	}
}

// setActiveMirror makes m the only mirror stream; a new phone takes over from an old one.
func (s *Server) setActiveMirror(m *mirrorStream) {
	s.mu.Lock()
	old := s.activeMirror
	s.activeMirror = m
	s.mu.Unlock()
	if old != nil && old != m {
		old.stop()
	}
}

func (s *Server) clearActiveMirror(m *mirrorStream) {
	s.mu.Lock()
	if s.activeMirror == m {
		s.activeMirror = nil
	}
	s.mu.Unlock()
}
