// Package mdns advertises the receiver's DNS-SD services so it shows up in the
// iPhone's Screen Mirroring list: through the system's Bonjour daemon on macOS
// (mdns_darwin.go) and with a built-in responder on Windows (mdns_windows.go).
//
// Both implement:
//
//	func Start(hostLabel string, services []Service, logger *log.Logger, debug bool) (*Responder, error)
//	func (r *Responder) Close()
package mdns

type Service struct {
	Instance string // e.g. "My PC"
	Type     string // e.g. "_airplay._tcp"
	Port     int
	TXT      []string
}
