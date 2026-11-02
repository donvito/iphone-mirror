// Package ui shows the mirrored iPhone screen in a native window that OBS can
// capture: Win32 + GDI on Windows (*_windows.go), Cocoa + VideoToolbox on
// macOS (*_darwin.*). The window title must stay the same so OBS finds it.
package ui

import "github.com/donvito/iphone-mirror/internal/settings"

// SettingsHooks connect the window to the rest of the app.
type SettingsHooks struct {
	Get     func() settings.Settings
	Apply   func(settings.Settings) // called on the UI thread; must not block
	LogsDir string
	OnQuit  func() // macOS: called on the main thread right before the app exits (Run never returns there)
}
