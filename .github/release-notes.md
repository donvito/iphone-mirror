Mirror your iPhone's screen to a window on your PC or Mac over Wi‑Fi (AirPlay screen mirroring), then capture it in OBS. Nothing to install on the iPhone.

## Downloads

| File | For |
|---|---|
| `iPhoneMirror-*-windows-x64.zip` | Windows 10/11 (64-bit) |
| `iPhoneMirror-*-macos.zip` | macOS 14 or later (Apple silicon and Intel) |

### Windows

1. Unzip the folder anywhere.
2. Install ffmpeg (used to decode the video): `winget install Gyan.FFmpeg`, or put `ffmpeg.exe` next to `iPhoneMirror.exe`.
3. Once: right-click `setup-firewall.ps1` → **Run with PowerShell** (asks for admin), so the iPhone can reach the PC.
4. Run `iPhoneMirror.exe`. The app isn't code-signed yet, so SmartScreen may warn: click **More info → Run anyway**.

### macOS

1. Unzip and move **iPhone Mirror** to Applications.
2. The app isn't notarized yet, so macOS blocks the first launch. Open it once, then go to **System Settings → Privacy & Security** and click **Open Anyway**.
3. When asked, **Allow** finding devices on your local network (and incoming connections, if the firewall is on).

### On the iPhone

**Control Center → Screen Mirroring** and pick the name shown in the app's window (on a Mac it ends in "Mirror").

See the [README](https://github.com/donvito/iphone-mirror#readme) for OBS setup, settings and troubleshooting. `SHA256SUMS.txt` has checksums for the downloads.
