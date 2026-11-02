// Package settings stores the user's preferences in %APPDATA%\iPhoneMirror\settings.json.
package settings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

type Settings struct {
	Name        string `json:"name"`        // shown in the iPhone's Screen Mirroring list
	HEVC        bool   `json:"hevc"`        // allow H.265, needed for streams taller than 1080
	FPS         int    `json:"fps"`         // maximum frame rate requested from the phone
	ShowStatus  bool   `json:"show_status"` // show instructions while no phone is connected
	AlwaysOnTop bool   `json:"always_on_top"`
}

// Defaults returns the settings used before the user changes anything.
func Defaults() Settings {
	host, _ := os.Hostname()
	if host == "" {
		host = "iPhone Mirror"
	}
	return Settings{Name: host, HEVC: true, FPS: 60, ShowStatus: true}
}

// Resolution is what the receiver advertises; the phone fits its screen to the height.
func (s Settings) Resolution() (w, h int) {
	if s.HEVC {
		return 3840, 2160
	}
	return 1920, 1080
}

// Normalize fixes up missing or invalid values.
func (s Settings) Normalize() Settings {
	d := Defaults()
	s.Name = strings.TrimSpace(s.Name)
	if s.Name == "" {
		s.Name = d.Name
	}
	if s.FPS != 30 && s.FPS != 60 {
		s.FPS = d.FPS
	}
	return s
}

func path(dir string) string { return filepath.Join(dir, "settings.json") }

// Load reads the settings from dir, falling back to defaults for anything missing.
func Load(dir string) Settings {
	s := Defaults()
	if b, err := os.ReadFile(path(dir)); err == nil {
		json.Unmarshal(b, &s)
	}
	return s.Normalize()
}

func Save(dir string, s Settings) error {
	b, err := json.MarshalIndent(s.Normalize(), "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(dir), b, 0o644)
}
