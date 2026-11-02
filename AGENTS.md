# AGENTS.md

AirPlay screen-mirroring receiver for Windows (Go + cgo), shows the iPhone in a Win32 window for OBS.

## Commands
- Build release exe: `powershell -File build.ps1` (-> `dist\iPhoneMirror.exe`, `-H windowsgui`)
- Tests: `go test ./...` (needs `ffmpeg` with libx264 on PATH; `-short` skips the latency test)
- Lint: `gofmt -l .` and `go vet ./...`
- Manual end-to-end without an iPhone: run the exe, then `go run ./cmd/fakesender -rotate`

## Layout
- `internal/airplay`: RTSP server, pair-verify, SETUP, mirror stream decryption (AES-CTR)
- `internal/fairplay`: cgo wrapper around UxPlay's playfair C code (GPLv3)
- `internal/mdns`: built-in mDNS/DNS-SD responder (no Bonjour needed)
- `internal/video`: ffmpeg subprocess decoder (H.264 -> BGRA), SPS size parser
- `internal/ui`: raw Win32 window (GDI StretchDIBits), title must stay "iPhone Mirror" for OBS
- `internal/sender`: fake AirPlay client used by tests and `cmd/fakesender`

## Gotchas
- Never pass `-fflags nobuffer` to ffmpeg: with ffmpeg 8 it stalls pipe output. Latency is about 40ms with `-flags low_delay`.
- The FairPlay handshake message byte 12 (mode) must be 0..3, otherwise playfair reads out of bounds (UB).
- Python edits on Windows write CRLF. Run `gofmt -w .` afterwards.
