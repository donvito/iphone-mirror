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
	"github.com/melvi/iphone-win-obs/internal/ui"
	"github.com/melvi/iphone-win-obs/internal/video"
)

const windowTitle = "iPhone Mirror"

var (
	cleanupMu sync.Mutex
	cleanup   func()
)

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

// glue connects the AirPlay receiver to the decoder and the window.
type glue struct {
	win     *ui.Window
	dec     *video.Decoder
	log     *log.Logger
	waiting string
}

func (g *glue) MirrorStarted(client string) {
	g.log.Printf("mirroring started from %s", client)
	if g.waiting != "" {
		g.win.SetStatus("iPhone connected (" + client + ")\n\nStarting video…")
	}
}

func (g *glue) CodecConfig(sps, pps []byte) {
	if err := g.dec.Configure(sps, pps); err != nil {
		g.log.Printf("decoder: %v", err)
	}
}

func (g *glue) VideoData(annexB []byte) {
	g.dec.Write(annexB)
}

func (g *glue) MirrorStopped() {
	g.dec.Stop()
	g.win.ClearFrame()
	g.win.SetStatus(g.waiting)
}

func main() {
	host, _ := os.Hostname()
	name := flag.String("name", host, "name shown in the iPhone's Screen Mirroring list")
	res := flag.String("res", "1920x1080", "requested resolution WxH (the height decides the stream size)")
	fps := flag.Int("fps", 60, "maximum frame rate requested from the iPhone")
	port := flag.Int("port", 7000, "AirPlay TCP port")
	debug := flag.Bool("debug", false, "log every mDNS packet received (for troubleshooting discovery)")
	hideStatus := flag.Bool("hide-status", false, "show a plain black window instead of instructions while no iPhone is connected")
	flag.Parse()

	dir := appDir()
	logFile, _ := os.Create(filepath.Join(dir, "log.txt"))
	var out io.Writer = os.Stderr
	if logFile != nil {
		out = io.MultiWriter(logFile, os.Stderr)
	}
	logger := log.New(out, "", log.LstdFlags|log.Lmicroseconds)

	win := ui.New(windowTitle)
	waiting := fmt.Sprintf("Waiting for iPhone…\n\nOn your iPhone, open Control Center, tap Screen Mirroring and choose “%s”.\n\n"+
		"The iPhone and this PC must be on the same network.", *name)
	if *hideStatus {
		waiting = ""
	}
	win.SetStatus(waiting)

	go func() {
		if err := start(*name, *res, *fps, *port, *debug, dir, logger, win, waiting); err != nil {
			logger.Printf("fatal: %v", err)
			win.SetStatus("Error: " + err.Error() + "\n\nSee log: " + filepath.Join(dir, "log.txt"))
		}
	}()

	if err := win.Run(); err != nil {
		logger.Printf("window: %v", err)
	}
	cleanupMu.Lock()
	if cleanup != nil {
		cleanup()
	}
	cleanupMu.Unlock()
}

func start(name, res string, fps, port int, debug bool, dir string, logger *log.Logger, win *ui.Window, waiting string) error {
	var width, height int
	if _, err := fmt.Sscanf(strings.ToLower(res), "%dx%d", &width, &height); err != nil || width <= 0 || height <= 0 {
		return fmt.Errorf("invalid -res %q, expected e.g. 1920x1080", res)
	}
	key, hw, err := loadIdentity(dir)
	if err != nil {
		return fmt.Errorf("identity: %w", err)
	}
	ffmpeg, err := video.FindFFmpeg()
	if err != nil {
		return err
	}
	logger.Printf("using ffmpeg at %s", ffmpeg)

	g := &glue{win: win, log: logger, waiting: waiting}
	g.dec = video.NewDecoder(ffmpeg, win.SetFrame, logger)

	srv := airplay.NewServer(airplay.Config{
		Name: name, HWAddr: hw, Identity: key,
		Width: width, Height: height, FPS: fps, Port: port, Log: logger,
	}, g)

	if err := srv.Listen(); err != nil {
		return fmt.Errorf("cannot listen on port %d (is another AirPlay receiver running?): %w", port, err)
	}
	responder, err := mdns.Start("iPhoneMirror-"+hex.EncodeToString(hw[3:]), []mdns.Service{
		{Instance: name, Type: "_airplay._tcp", Port: port, TXT: srv.AirPlayTXT()},
		{Instance: srv.RAOPInstanceName(), Type: "_raop._tcp", Port: port, TXT: srv.RAOPTXT()},
	}, logger, debug)
	if err != nil {
		return fmt.Errorf("mDNS: %w", err)
	}
	cleanupMu.Lock()
	cleanup = func() { responder.Close(); srv.Close(); g.dec.Stop() }
	cleanupMu.Unlock()
	logger.Printf("AirPlay receiver %q ready on port %d (device id %s)", name, port, srv.DeviceID())
	if err := srv.Serve(); err != nil {
		return fmt.Errorf("AirPlay server: %w", err)
	}
	return nil
}
