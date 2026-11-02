# iPhone Mirror (Windows)

Mirror your iPhone's screen to a Windows window over Wi‑Fi, then add that window to OBS.

It is an **AirPlay screen mirroring receiver**: nothing to install on the iPhone. The phone uses
its built‑in *Screen Mirroring* and the PC shows up in the list next to any Apple TVs.

## Requirements

- Windows 10/11, iPhone and PC on the **same network** (the PC can be wired to the router, as long
  as it's the same LAN/subnet as the phone's Wi‑Fi and the router doesn't isolate Wi‑Fi clients).
- `ffmpeg.exe`, used to decode the video. Either put it next to `iPhoneMirror.exe` or have it on PATH
  (`winget install Gyan.FFmpeg`).
- To build: Go 1.22+ and a 64‑bit MinGW `gcc` on PATH (cgo compiles the FairPlay code).

## Build & first run

```powershell
.\build.ps1            # -> dist\iPhoneMirror.exe
.\setup-firewall.ps1   # once: lets the iPhone reach the app (asks for admin)
.\dist\iPhoneMirror.exe
```

The firewall step matters: if your network is set to **Public** in Windows, the normal "Allow access"
prompt doesn't cover it, and the iPhone won't see the PC.

On the iPhone: **Control Center → Screen Mirroring → pick your PC's name**.

## Using it in OBS

1. Add a **Window Capture** source and pick `[iPhoneMirror.exe]: iPhone Mirror`.
2. Set *Capture Method* to **Windows 10 (1903 and up)**. This also works when the window is behind other windows (but not when it's minimized).
3. The window opens as tall as your screen allows. Double‑click it to match the phone's aspect ratio
   exactly (no black bars). OBS captures the window at its on‑screen size, so keep it large for sharp text.

The window title stays `iPhone Mirror`, so OBS finds it again after a restart.

## Settings

Right‑click the mirror window (or press Shift+F10) for the menu:

- **Settings…**
  - **Name shown on the iPhone:** defaults to the computer name.
  - **Quality:** *High* (HEVC, up to 2160p; an iPhone 15 Pro Max sends about 998×2160 at ~50 fps) or
    *Compatible* (H.264, up to 1080p; about 498×1080, less CPU).
  - **Maximum frame rate:** 60 or 30 fps.
  - **Show instructions while no iPhone is connected:** turn off for a plain black window.
  - **Keep window on top of other windows**
- **Fit window to phone:** the same as double‑clicking the window.
- **Keep window on top**
- **Open log folder**
- **Exit**

Settings are saved to `%APPDATA%\iPhoneMirror\settings.json`. Changing the name, quality or frame rate
restarts the receiver, which briefly disconnects the iPhone.

Command‑line flags override the saved settings for one run (they're useful for scripts):
`-name`, `-h264`, `-fps`, `-hide-status`, `-debug` (logs mDNS traffic), `-port`, and the advanced
`-res WxH`.

Logs are written to `%APPDATA%\iPhoneMirror\log.txt`.

## Limitations

- Video only: the iPhone's audio is received but thrown away. While you're mirroring, the phone sends
  its sound to the receiver, so you won't hear it on the phone either.
- One phone at a time. A new phone takes over from the current one.

## Testing without an iPhone

`cmd/fakesender` speaks the same protocol (pairing, FairPlay, encrypted mirror stream) and streams a
test pattern:

```powershell
go run ./cmd/fakesender -addr 127.0.0.1:7000 -size 886x1920 -seconds 15 -rotate
go run ./cmd/fakesender -codec hevc -size 998x2160 -seconds 10
go test ./...
```

## Credits / license

The protocol handling follows the open‑source [UxPlay](https://github.com/FDH2/UxPlay) / RPiPlay
receivers. `internal/fairplay/*.c` (playfair) comes from UxPlay and is GPLv3, so this project is
GPLv3 as well.
