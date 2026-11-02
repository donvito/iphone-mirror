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
3. Double‑click the mirror window to resize it to the phone's aspect ratio and remove the black bars.
   You can also leave it as is and crop in OBS.

The window title stays `iPhone Mirror`, so OBS finds it again after a restart.

## Options

```
iPhoneMirror.exe [-name "My PC"] [-res 1920x1080] [-fps 60] [-hide-status] [-port 7000]
```

- `-name`: the name shown on the iPhone (defaults to the computer name).
- `-res`: the resolution requested from the phone. The **height** decides the stream size: with
  1080, a portrait iPhone sends about 498×1080, which fills a 1080p OBS canvas.
- `-hide-status`: shows a plain black window instead of the instructions while no phone is connected.

Logs are written to `%APPDATA%\iPhoneMirror\log.txt`.

## Limitations

- Video only: the iPhone's audio is received but thrown away. While you're mirroring, the phone sends
  its sound to the receiver, so you won't hear it on the phone either.
- H.264 only, up to 1080p‑class streams (no 4K/HEVC).
- One phone at a time. A new phone takes over from the current one.

## Testing without an iPhone

`cmd/fakesender` speaks the same protocol (pairing, FairPlay, encrypted mirror stream) and streams a
test pattern:

```powershell
go run ./cmd/fakesender -addr 127.0.0.1:7000 -size 886x1920 -seconds 15 -rotate
go test ./...
```

## Credits / license

The protocol handling follows the open‑source [UxPlay](https://github.com/FDH2/UxPlay) / RPiPlay
receivers. `internal/fairplay/*.c` (playfair) comes from UxPlay and is GPLv3, so this project is
GPLv3 as well.
