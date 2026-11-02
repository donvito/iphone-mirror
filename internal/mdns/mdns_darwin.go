package mdns

// On macOS the services are registered with the system's Bonjour daemon
// (mDNSResponder), which owns UDP 5353.

/*
#include <stdlib.h>
#include "mdns_darwin.h"
*/
import "C"

import (
	"fmt"
	"log"
	"runtime/cgo"
	"unsafe"
)

type Responder struct {
	log  *log.Logger
	refs []C.DNSServiceRef
	hs   []cgo.Handle
}

type registration struct {
	svc Service
	log *log.Logger
}

//export goMDNSRegistered
func goMDNSRegistered(h C.uintptr_t, err C.int, name *C.char) {
	r := cgo.Handle(h).Value().(*registration)
	if err != 0 {
		r.log.Printf("mdns: %s registration failed: DNSServiceErrorType %d", r.svc.Type, int(err))
		return
	}
	r.log.Printf("mdns: registered %s %q on port %d", r.svc.Type, C.GoString(name), r.svc.Port)
}

func txtRecord(entries []string) []byte {
	var b []byte
	for _, e := range entries {
		if len(e) > 255 {
			e = e[:255]
		}
		b = append(append(b, byte(len(e))), e...)
	}
	return b
}

// Start registers the services with Bonjour. Bonjour renames an instance
// automatically (e.g. "Name (2)") if the name is already taken on the network.
// hostLabel and debug are unused: Bonjour advertises the Mac's own host name.
func Start(hostLabel string, services []Service, logger *log.Logger, debug bool) (*Responder, error) {
	r := &Responder{log: logger}
	for _, s := range services {
		name, typ := C.CString(s.Instance), C.CString(s.Type)
		txt := txtRecord(s.TXT)
		ctxt := C.CBytes(txt)
		h := cgo.NewHandle(&registration{svc: s, log: logger})
		var ref C.DNSServiceRef
		err := C.mdns_register(C.uintptr_t(h), name, typ, C.int(s.Port), ctxt, C.int(len(txt)), &ref)
		C.free(unsafe.Pointer(name))
		C.free(unsafe.Pointer(typ))
		C.free(ctxt)
		if err != 0 {
			h.Delete()
			r.Close()
			return nil, fmt.Errorf("register %s: DNSServiceErrorType %d (is mDNSResponder running?)", s.Type, int(err))
		}
		r.refs, r.hs = append(r.refs, ref), append(r.hs, h)
	}
	return r, nil
}

// Close withdraws the services (Bonjour sends goodbye packets).
func (r *Responder) Close() {
	for _, ref := range r.refs {
		C.mdns_deregister(ref)
	}
	for _, h := range r.hs {
		h.Delete()
	}
	r.refs, r.hs = nil, nil
}
