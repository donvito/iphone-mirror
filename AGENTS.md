# AGENTS.md

AirPlay screen-mirroring receiver for Windows and macOS (Go + cgo), shows the iPhone in a native window for OBS.
One module; OS-specific code is selected by `_windows` / `_darwin` filename suffixes.

## Commands
- Windows release exe: `powershell -File build.ps1` (-> `dist\iPhoneMirror.exe`, `-H windowsgui`)
- macOS app: `./build.sh` (-> `dist/iPhone Mirror.app`, universal; `ARCHS=arm64 ./build.sh` for a faster local build)
- macOS env for plain `go` commands (a mismatched Command Line Tools SDK may otherwise fail to link / spam ld warnings):
  `export SDKROOT=$(xcrun --sdk macosx --show-sdk-path) CGO_CFLAGS="-O2 -mmacosx-version-min=14.0" CGO_LDFLAGS="-mmacosx-version-min=14.0"`
- Release: push a tag `vX.Y.Z`; `.github/workflows/release.yml` builds on windows-2025 + macos-15 and publishes a GitHub release
  (notes from `.github/release-notes.md` + generated). `gh workflow run release.yml` builds artifacts only (dry run).
- Tests: `go test ./...` (needs `ffmpeg` with libx264/libx265 on PATH; `-short` skips the Windows latency test)
- Lint: `gofmt -l .` and `go vet ./...`
- Type-check the Windows build from a Mac (no MinGW): stub out the cgo FairPlay file with an overlay, e.g.
  `GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go vet -overlay overlay.json ./...` where overlay.json replaces
  `internal/fairplay/fairplay.go` with a file declaring the same `Session` methods without `import "C"`.
- Manual end-to-end without an iPhone: run the app, then `go run ./cmd/fakesender -rotate` (and `-codec hevc -size 998x2160`).

## Layout
- `main.go`: shared app (settings, receiver restarts, status text); `platform_*.go`: `newVideo`, default port, wording
- `internal/airplay`: RTSP server, pair-verify, SETUP, mirror stream decryption (AES-CTR)
- `internal/fairplay`: cgo wrapper around UxPlay's playfair C code (GPLv3)
- `internal/mdns`: `service.go` shared type; Windows built-in responder (`mdns_windows.go`), macOS Bonjour via dns_sd (`mdns_darwin.*`)
- `internal/video`: SPS size parsers, Annex-B -> AVCC (shared); ffmpeg subprocess decoder (`decoder_windows.go`)
- `internal/ui`: `hooks.go` shared; Win32 window (GDI StretchDIBits) / Cocoa window with VTDecompressionSession -> IOSurface -> CALayer. Title must stay "iPhone Mirror" for OBS
- `internal/sender`: fake AirPlay client used by tests and `cmd/fakesender`

## Gotchas
- Both `ui` implementations must keep the same API used by main: `New(title, logger)`, `SetHooks`, `SetStatus`, `SetAlwaysOnTop`, `Run`.
- Windows: never pass `-fflags nobuffer` to ffmpeg: with ffmpeg 8 it stalls pipe output. Latency is about 40ms with `-flags low_delay`.
- macOS: don't use AVSampleBufferDisplayLayer for video: it queues 4-5 frames (~73-86 ms measured). The VTDecompressionSession path shows a frame ~4 ms after it arrives. Decoded buffers are held for a few frames (`heldFrames`) so surfaces aren't reused while on screen.
- macOS's built-in AirPlay Receiver owns TCP 7000/5000 and registers `_airplay._tcp` under the computer name, hence port 7100 and "<ComputerName> Mirror".
- macOS: `main` is locked to the main thread (`init` in platform_darwin.go); `Run` never returns, cleanup runs via `SettingsHooks.OnQuit`.
- macOS deployment target is 14. Guard newer APIs with `@available`; the build must stay warning-free.
- iPhones often connect over link-local IPv6 (seen on macOS); keep the IPv6 zone when dialing back (NTP timing channel).
- The FairPlay handshake message byte 12 (mode) must be 0..3, otherwise playfair reads out of bounds (UB).
- HEVC is on by default (features bit 42). Without it, iOS caps mirroring at 1080 pixels high. A real iPhone 15 Pro Max on iOS 27 sends HEVC at 998x2160 and ~50 fps.
- The fake sender takes over from a connected iPhone. Check the log for an active session before running it (or run the app with `-name Test -port 7200`).
- Python edits on Windows write CRLF. Run `gofmt -w .` afterwards.
