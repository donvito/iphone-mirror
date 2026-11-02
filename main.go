// iPhone Mirror: an AirPlay screen mirroring receiver for Windows.
//
// Run it, then on the iPhone open Control Center -> Screen Mirroring and pick
// this PC. The phone screen appears in a normal window that OBS can capture
// with a "Window Capture" source.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/melvi/iphone-win-obs/internal/airplay"
	"github.com/melvi/iphone-win-obs/internal/mdns"
	"github.com/melvi/iphone-win-obs/internal/settings"
	"github.com/melvi/iphone-win-obs/internal/ui"
	"github.com/melvi/iphone-win-obs/internal/video"
)

const windowTitle = "iPhone Mirror"

type identity struct {
	Seed   string `json:"seed"`
	HWAddr string `json:"hwaddr"`
}

func appDir() string {
	base, err := os.UserConfigDir()
	if err != nil {
		base = "."
	}
	dir := filepath.Join(base, "iPhoneMirror")
	os.MkdirAll(dir, 0o755)
	return dir
}

// loadIdentity returns a persistent signing key and device ID so the iPhone
// sees the same receiver across restarts.
func loadIdentity(dir string) (ed25519.PrivateKey, net.HardwareAddr, error) {
	path := filepath.Join(dir, "identity.json")
	var id identity
	if b, err := os.ReadFile(path); err == nil && json.Unmarshal(b, &id) == nil {
		seed, err1 := hex.DecodeString(id.Seed)
		hw, err2 := hex.DecodeString(id.HWAddr)
		if err1 == nil && err2 == nil && len(seed) == ed25519.SeedSize && len(hw) == 6 {
			return ed25519.NewKeyFromSeed(seed), hw, nil
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	hw := make([]byte, 6)
	if _, err := rand.Read(seed); err != nil {
		return nil, nil, err
	}
	rand.Read(hw)
	hw[0] = (hw[0] | 0x02) &^ 0x01 // locally administered, unicast
	b, _ := json.MarshalIndent(identity{Seed: hex.EncodeToString(seed), HWAddr: hex.EncodeToString(hw)}, "", "  ")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return nil, nil, err
	}
	return ed25519.NewKeyFromSeed(seed), hw, nil
}

// app ties the AirPlay receiver, decoder, window and settings together.
type app struct {
	dir   string
	log   *log.Logger
	win   *ui.Window
	dec   *video.Decoder
	key   ed25519.PrivateKey
	hw    net.HardwareAddr
	port  int
	debug bool
	resW  int // -res override (0 = derive from settings)
	resH  int

	mu  sync.Mutex
	cur settings.Settings

	restartMu sync.Mutex // serializes receiver restarts
	srv       *airplay.Server
	resp      *mdns.Responder
}

func (a *app) settings() settings.Settings {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cur
}

func (a *app) waitingText() string {
	s := a.settings()
	if !s.ShowStatus {
		return ""
	}
	return fmt.Sprintf("Waiting for iPhone…\n\nOn your iPhone, open Control Center, tap Screen Mirroring and choose “%s”.\n\n"+
		"The iPhone and this PC must be on the same network.\n\nRight-click this window for settings.", s.Name)
}

func (a *app) MirrorStarted(client string) {
	a.log.Printf("mirroring started from %s", client)
	a.dec.Stop() // fresh decoder per session, even if the size matches the previous one
	if a.settings().ShowStatus {
		a.win.SetStatus("iPhone connected (" + client + ")\n\nStarting video…")
	}
}

func (a *app) CodecConfig(codec string, paramSets [][]byte) {
	if err := a.dec.Configure(codec, paramSets); err != nil {
		a.log.Printf("decoder: %v", err)
	}
}

func (a *app) VideoData(annexB []byte) {
	a.dec.Write(annexB)
}

func (a *app) MirrorStopped() {
	a.dec.Stop()
	a.win.ClearFrame()
	a.win.SetStatus(a.waitingText())
}

// apply is called from the settings UI (on the UI thread).
func (a *app) apply(s settings.Settings) {
	s = s.Normalize()
	a.mu.Lock()
	old := a.cur
	a.cur = s
	a.mu.Unlock()
	if err := settings.Save(a.dir, s); err != nil {
		a.log.Printf("settings: save failed: %v", err)
	}
	a.log.Printf("settings: %+v", s)
	if s.AlwaysOnTop != old.AlwaysOnTop {
		a.win.SetAlwaysOnTop(s.AlwaysOnTop)
	}
	if s.Name != old.Name || s.HEVC != old.HEVC || s.FPS != old.FPS {
		go func() {
			if err := a.restartReceiver(); err != nil {
				a.fail(err)
			}
		}()
	} else if s.ShowStatus != old.ShowStatus {
		a.win.SetStatus(a.waitingText())
	}
}

func (a *app) fail(err error) {
	a.log.Printf("error: %v", err)
	a.win.SetStatus("Error: " + err.Error() + "\n\nSee log: " + filepath.Join(a.dir, "log.txt"))
}

// restartReceiver stops the AirPlay service (disconnecting any phone) and
// starts it again with the current settings.
func (a *app) restartReceiver() error {
	a.restartMu.Lock()
	defer a.restartMu.Unlock()
	a.stopLocked()
	return a.startLocked()
}

func (a *app) stopLocked() {
	if a.resp != nil {
		a.resp.Close()
		a.resp = nil
	}
	if a.srv != nil {
		a.srv.Close()
		a.srv = nil
	}
	a.dec.Stop()
}

func (a *app) startLocked() error {
	s := a.settings()
	w, h := s.Resolution()
	if a.resW > 0 {
		w, h = a.resW, a.resH
	}
	srv := airplay.NewServer(airplay.Config{
		Name: s.Name, HWAddr: a.hw, Identity: a.key, HEVC: s.HEVC,
		Width: w, Height: h, FPS: s.FPS, Port: a.port, Log: a.log,
	}, a)
	if err := srv.Listen(); err != nil {
		return fmt.Errorf("cannot listen on port %d (is another AirPlay receiver running?): %w", a.port, err)
	}
	resp, err := mdns.Start("iPhoneMirror-"+hex.EncodeToString(a.hw[3:]), []mdns.Service{
		{Instance: s.Name, Type: "_airplay._tcp", Port: a.port, TXT: srv.AirPlayTXT()},
		{Instance: srv.RAOPInstanceName(), Type: "_raop._tcp", Port: a.port, TXT: srv.RAOPTXT()},
	}, a.log, a.debug)
	if err != nil {
		srv.Close()
		return fmt.Errorf("mDNS: %w", err)
	}
	a.srv, a.resp = srv, resp
	go func() {
		if err := srv.Serve(); err != nil {
			a.fail(fmt.Errorf("AirPlay server: %w", err))
		}
	}()
	a.win.ClearFrame()
	a.win.SetStatus(a.waitingText())
	a.log.Printf("AirPlay receiver %q ready on port %d (device id %s, requesting %dx%d@%d, hevc=%v)",
		s.Name, a.port, srv.DeviceID(), w, h, s.FPS, s.HEVC)
	return nil
}

func main() {
	name := flag.String("name", "", "name shown in the iPhone's Screen Mirroring list (overrides settings)")
	res := flag.String("res", "", "advanced: requested resolution WxH; the height decides the stream size")
	h264Only := flag.Bool("h264", false, "only accept H.264, up to 1080p (overrides settings)")
	fps := flag.Int("fps", 0, "maximum frame rate requested from the iPhone, 30 or 60 (overrides settings)")
	port := flag.Int("port", 7000, "AirPlay TCP port")
	debug := flag.Bool("debug", false, "log every mDNS packet received (for troubleshooting discovery)")
	hideStatus := flag.Bool("hide-status", false, "plain black window while no iPhone is connected (overrides settings)")
	flag.Parse()

	dir := appDir()
	logFile, _ := os.Create(filepath.Join(dir, "log.txt"))
	var out io.Writer = os.Stderr
	if logFile != nil {
		out = io.MultiWriter(logFile, os.Stderr)
	}
	logger := log.New(out, "", log.LstdFlags|log.Lmicroseconds)

	// Command-line flags override the saved settings for this run only.
	cur := settings.Load(dir)
	flag.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "name":
			cur.Name = *name
		case "h264":
			cur.HEVC = !*h264Only
		case "fps":
			cur.FPS = *fps
		case "hide-status":
			cur.ShowStatus = !*hideStatus
		}
	})
	a := &app{dir: dir, log: logger, win: ui.New(windowTitle), port: *port, debug: *debug, cur: cur.Normalize()}
	if *res != "" {
		if _, err := fmt.Sscanf(strings.ToLower(*res), "%dx%d", &a.resW, &a.resH); err != nil || a.resW <= 0 || a.resH <= 0 {
			logger.Fatalf("invalid -res %q, expected e.g. 1920x1080", *res)
		}
	}
	a.win.SetHooks(ui.SettingsHooks{Get: a.settings, Apply: a.apply, LogsDir: dir})
	a.win.SetAlwaysOnTop(a.cur.AlwaysOnTop)
	a.win.SetStatus(a.waitingText())

	go func() {
		var err error
		if a.key, a.hw, err = loadIdentity(dir); err != nil {
			a.fail(fmt.Errorf("identity: %w", err))
			return
		}
		ffmpeg, err := video.FindFFmpeg()
		if err != nil {
			a.fail(err)
			return
		}
		logger.Printf("using ffmpeg at %s", ffmpeg)
		a.restartMu.Lock()
		a.dec = video.NewDecoder(ffmpeg, a.win.SetFrame, logger)
		err = a.startLocked()
		a.restartMu.Unlock()
		if err != nil {
			a.fail(err)
		}
	}()

	if err := a.win.Run(); err != nil {
		logger.Printf("window: %v", err)
	}
	a.restartMu.Lock()
	if a.dec != nil {
		a.stopLocked()
	}
	a.restartMu.Unlock()
}
