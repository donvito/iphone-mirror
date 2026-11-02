# iPhone Mirror (Windows & macOS)

Mirror your iPhone's screen to a window on your PC or Mac over Wi‑Fi, then add that window to OBS.

It is an **AirPlay screen mirroring receiver**: nothing to install on the iPhone. The phone uses
its built‑in *Screen Mirroring* and the computer shows up in the list next to any Apple TVs.

The AirPlay/FairPlay protocol code is shared. Each OS has native pieces:

| | Windows | macOS |
|---|---|---|
| Discovery | built‑in mDNS responder (no Bonjour needed) | system Bonjour |
| Video decoding | `ffmpeg.exe` child process | VideoToolbox (hardware, ~4 ms per frame) |
| Window | Win32 + GDI | Cocoa |
| Default port | 7000 | 7100 (macOS's own AirPlay Receiver owns 7000) |
| Default name | computer name | "<Computer Name> Mirror" |

## Requirements

- iPhone and computer on the **same network** (the computer can be wired to the router, as long as
  it's the same LAN/subnet as the phone's Wi‑Fi and the router doesn't isolate Wi‑Fi clients).
- **Windows:** Windows 10/11 and `ffmpeg.exe`, used to decode the video. Either put it next to
  `iPhoneMirror.exe` or have it on PATH (`winget install Gyan.FFmpeg`).
- **macOS:** macOS 14 (Sonoma) or later, Apple silicon or Intel. No ffmpeg needed.
- To build: Go 1.26+ and a C compiler for cgo (FairPlay code; on macOS also the Cocoa code):
  64‑bit MinGW `gcc` on PATH on Windows, Xcode or the Xcode Command Line Tools on macOS.

## Build & first run

### Windows

```powershell
.\build.ps1            # -> dist\iPhoneMirror.exe
.\setup-firewall.ps1   # once: lets the iPhone reach the app (asks for admin)
.\dist\iPhoneMirror.exe
```

The firewall step matters: if your network is set to **Public** in Windows, the normal "Allow access"
prompt doesn't cover it, and the iPhone won't see the PC.

### macOS

```sh
./build.sh                      # -> dist/iPhone Mirror.app (universal)
open "dist/iPhone Mirror.app"
```

On first launch macOS asks for permission to **find devices on your local network**. Click
**Allow**, otherwise the iPhone can't connect. If the macOS firewall is on, also allow incoming
connections. You can change both later in System Settings → Privacy & Security → Local Network,
and Network → Firewall.

macOS also has its own AirPlay Receiver, listed under the plain computer name. This app uses a
different name and port so the two don't clash. Pick the entry ending in "Mirror".

### On the iPhone

**Control Center → Screen Mirroring → pick your computer's name** (as shown in the window).

## Using it in OBS

- **Windows:** add a **Window Capture** source, pick `[iPhoneMirror.exe]: iPhone Mirror` and set
  *Capture Method* to **Windows 10 (1903 and up)**.
- **macOS:** add a **macOS Screen Capture** source, set *Method* to **Window Capture** and pick
  `[iPhone Mirror] iPhone Mirror`.

This also works when the window is behind other windows (but not when it's minimized). The window
opens as tall as your screen allows. Double‑click it (⌘0 on macOS) to match the phone's aspect ratio
exactly (no black bars). OBS captures the window at its on‑screen size, so keep it large for sharp text.

The window title stays `iPhone Mirror`, so OBS finds it again after a restart.

## Settings

Right‑click the mirror window (or press Shift+F10 on Windows; on macOS the menu bar has the same
items):

- **Settings…** (⌘, on macOS)
  - **Name shown on the iPhone**
  - **Quality:** *High* (HEVC, up to 2160p; an iPhone 15 Pro Max sends about 998×2160 at ~50 fps) or
    *Compatible* (H.264, up to 1080p; about 498×1080, less CPU).
  - **Maximum frame rate:** 60 or 30 fps.
  - **Show instructions while no iPhone is connected:** turn off for a plain black window.
  - **Keep window on top of other windows**
- **Fit window to phone:** the same as double‑clicking the window.
- **Keep window on top** (⌘T on macOS)
- **Open log folder**
- **Exit / Quit**

Settings are saved to `settings.json` in `%APPDATA%\iPhoneMirror` (Windows) or
`~/Library/Application Support/iPhoneMirror` (macOS). Changing the name, quality or frame rate
restarts the receiver, which briefly disconnects the iPhone.

Command‑line flags override the saved settings for one run (they're useful for scripts):
`-name`, `-h264`, `-fps`, `-hide-status`, `-debug` (Windows: logs mDNS traffic), `-port` (if the
default port is busy a free one is used, unless you set `-port` yourself), and the advanced `-res WxH`.

```sh
"dist/iPhone Mirror.app/Contents/MacOS/iPhoneMirror" -name "Studio Mac" -fps 30   # macOS
```

Logs are written to `log.txt` in the same folder as the settings. On macOS the log shows the frame
rate, picture size and decode time every 10 seconds while mirroring. If it feels laggy while the
decode time stays low, the delay is on the Wi‑Fi: a wired computer, a 5 GHz network or (on a Mac)
turning off AirDrop/Handoff can help.

## Limitations

- Video only: the iPhone's audio is received but thrown away. While you're mirroring, the phone sends
  its sound to the receiver, so you won't hear it on the phone either.
- One phone at a time. A new phone takes over from the current one.

## Testing without an iPhone

`cmd/fakesender` speaks the same protocol (pairing, FairPlay, encrypted mirror stream) and streams a
test pattern to the default port of the OS it runs on. It needs `ffmpeg` with libx264/libx265:

```sh
go run ./cmd/fakesender -size 886x1920 -seconds 15 -rotate
go run ./cmd/fakesender -codec hevc -size 998x2160 -seconds 10
go test ./...
```

## Project layout

- `main.go`: shared app logic; `platform_windows.go` / `platform_darwin.go`: per‑OS video output and defaults
- `internal/airplay`, `internal/fairplay`, `internal/sender`, `internal/settings`: shared
- `internal/mdns`, `internal/ui`, `internal/video`: shared types/helpers plus `*_windows.go` and `*_darwin.*` files

## Credits / license

The protocol handling follows the open‑source [UxPlay](https://github.com/FDH2/UxPlay) / RPiPlay
receivers. `internal/fairplay/*.c` (playfair) comes from UxPlay and is GPLv3, so this project is
GPLv3 as well.
