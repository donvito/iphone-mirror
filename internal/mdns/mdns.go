// Package mdns is a small multicast DNS / DNS-SD responder, enough to make the
// receiver show up in the iPhone's Screen Mirroring list without needing
// Apple's Bonjour service to be installed on Windows.
package mdns

import (
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/miekg/dns"
	"golang.org/x/net/ipv4"
)

const (
	ttl          = 120
	servicesEnum = "_services._dns-sd._udp.local."
)

var groupAddr = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

type Service struct {
	Instance string // e.g. "My PC"
	Type     string // e.g. "_airplay._tcp"
	Port     int
	TXT      []string
}

func (s Service) typeName() string     { return s.Type + ".local." }
func (s Service) instanceName() string { return escapeLabel(s.Instance) + "." + s.typeName() }

func escapeLabel(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `.`, `\.`, ` `, `\ `, `"`, `\"`, `(`, `\(`, `)`, `\)`, `;`, `\;`)
	return r.Replace(s)
}

type iface struct {
	ifi   net.Interface
	ip    net.IP
	ipnet *net.IPNet
}

type Responder struct {
	Debug    bool // log every received mDNS packet
	host     string
	services []Service
	log      *log.Logger

	conn   *net.UDPConn
	pc     *ipv4.PacketConn
	ifaces []iface

	sendMu sync.Mutex
	quit   chan struct{}
	wg     sync.WaitGroup
}

// usableInterfaces returns up, multicast-capable interfaces with a routable IPv4 address.
func usableInterfaces() []iface {
	var out []iface
	ifis, _ := net.Interfaces()
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagMulticast == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := ifi.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip4 := ipn.IP.To4()
			if ip4 == nil || ip4.IsLinkLocalUnicast() || ip4.IsLoopback() {
				continue
			}
			out = append(out, iface{ifi: ifi, ip: ip4, ipnet: ipn})
			break
		}
	}
	return out
}

// Start begins answering mDNS queries for the given services.
func Start(hostLabel string, services []Service, logger *log.Logger, debug bool) (*Responder, error) {
	r := &Responder{
		Debug:    debug,
		host:     escapeLabel(hostLabel) + ".local.",
		services: services,
		log:      logger,
		ifaces:   usableInterfaces(),
		quit:     make(chan struct{}),
	}
	if len(r.ifaces) == 0 {
		return nil, fmt.Errorf("no usable network interface found")
	}
	conn, err := net.ListenMulticastUDP("udp4", &r.ifaces[0].ifi, groupAddr)
	if err != nil {
		return nil, fmt.Errorf("listen on mDNS port 5353: %w", err)
	}
	r.conn = conn
	r.pc = ipv4.NewPacketConn(conn)
	r.pc.SetMulticastTTL(255)
	r.pc.SetMulticastLoopback(true)
	for _, it := range r.ifaces[1:] {
		ifi := it.ifi
		if err := r.pc.JoinGroup(&ifi, &net.UDPAddr{IP: groupAddr.IP}); err != nil {
			logger.Printf("mdns: join group on %s: %v", ifi.Name, err)
		}
	}
	for _, it := range r.ifaces {
		logger.Printf("mdns: advertising on %s (%s)", it.ifi.Name, it.ip)
	}
	r.wg.Add(2)
	go r.readLoop()
	go r.announceLoop()
	return r, nil
}

// Close sends goodbye packets and stops the responder.
func (r *Responder) Close() {
	close(r.quit)
	for _, it := range r.ifaces {
		r.sendMulticast(it, r.buildAnnouncement(it, 0))
	}
	r.conn.Close()
	r.wg.Wait()
}

func (r *Responder) announceLoop() {
	defer r.wg.Done()
	delay := time.Second
	for {
		for _, it := range r.ifaces {
			r.sendMulticast(it, r.buildAnnouncement(it, ttl))
		}
		select {
		case <-r.quit:
			return
		case <-time.After(delay):
		}
		if delay < 60*time.Second {
			delay *= 2
		}
	}
}

func (r *Responder) readLoop() {
	defer r.wg.Done()
	buf := make([]byte, 9000)
	for {
		n, src, err := r.conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-r.quit:
				return
			default:
			}
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			// Windows reports ICMP "port unreachable" from earlier sends as read errors; ignore them.
			continue
		}
		var msg dns.Msg
		err = msg.Unpack(buf[:n])
		if r.Debug {
			r.logPacket(&msg, src, err)
		}
		if err != nil || msg.Response || len(msg.Question) == 0 {
			continue
		}
		r.handleQuery(&msg, src)
	}
}

func (r *Responder) logPacket(m *dns.Msg, src *net.UDPAddr, err error) {
	if err != nil {
		r.log.Printf("mdns: <- %s unparseable: %v", src, err)
		return
	}
	var parts []string
	for _, q := range m.Question {
		parts = append(parts, fmt.Sprintf("Q %s %s", q.Name, dns.TypeToString[q.Qtype]))
	}
	for i, rr := range m.Answer {
		if i == 4 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, fmt.Sprintf("A %s %s", rr.Header().Name, dns.TypeToString[rr.Header().Rrtype]))
	}
	r.log.Printf("mdns: <- %s response=%v %s", src, m.Response, strings.Join(parts, ", "))
}

func (r *Responder) ifaceFor(src net.IP) iface {
	for _, it := range r.ifaces {
		if it.ipnet.Contains(src) {
			return it
		}
	}
	return r.ifaces[0]
}

func (r *Responder) handleQuery(q *dns.Msg, src *net.UDPAddr) {
	it := r.ifaceFor(src.IP)
	resp := new(dns.Msg)
	resp.Response = true
	resp.Authoritative = true
	unicast := src.Port != 5353
	for _, question := range q.Question {
		if question.Qclass&0x8000 != 0 {
			unicast = true
		}
		resp.Answer = append(resp.Answer, r.answers(question, it)...)
	}
	if len(resp.Answer) == 0 {
		return
	}
	resp.Extra = r.additionals(resp.Answer, it)
	if unicast && src.Port != 5353 { // legacy unicast query: must echo id and question
		resp.Id = q.Id
		resp.Question = q.Question
	}
	b, err := resp.Pack()
	if err != nil {
		return
	}
	if unicast {
		r.sendMu.Lock()
		r.conn.WriteToUDP(b, src)
		r.sendMu.Unlock()
	}
	if src.Port == 5353 {
		r.sendMulticast(it, b)
	}
}

// sameName compares two domain names in wire format, case-insensitively, so that
// differences in presentation-format escaping (e.g. "\@" vs "@") don't matter.
func sameName(a, b string) bool {
	return wireKey(a) == wireKey(b)
}

func wireKey(name string) string {
	buf := make([]byte, 256)
	n, err := dns.PackDomainName(dns.Fqdn(name), buf, 0, nil, false)
	if err != nil {
		return strings.ToLower(name)
	}
	return strings.ToLower(string(buf[:n]))
}

func (r *Responder) answers(q dns.Question, it iface) []dns.RR {
	name := q.Name
	match := func(t uint16) bool { return q.Qtype == t || q.Qtype == dns.TypeANY }
	var out []dns.RR
	if sameName(name, servicesEnum) && match(dns.TypePTR) {
		for _, s := range r.services {
			out = append(out, r.ptr(servicesEnum, s.typeName(), ttl))
		}
	}
	for _, s := range r.services {
		if sameName(name, s.typeName()) && match(dns.TypePTR) {
			out = append(out, r.ptr(s.typeName(), s.instanceName(), ttl))
		}
		if sameName(name, s.instanceName()) {
			if match(dns.TypeSRV) {
				out = append(out, r.srv(s, ttl))
			}
			if match(dns.TypeTXT) {
				out = append(out, r.txt(s, ttl))
			}
		}
	}
	if sameName(name, r.host) && match(dns.TypeA) {
		out = append(out, r.a(it, ttl))
	}
	return out
}

func (r *Responder) additionals(answers []dns.RR, it iface) []dns.RR {
	var extra []dns.RR
	haveA := false
	for _, rr := range answers {
		switch v := rr.(type) {
		case *dns.PTR:
			for _, s := range r.services {
				if sameName(v.Ptr, s.instanceName()) {
					extra = append(extra, r.srv(s, ttl), r.txt(s, ttl))
				}
			}
		case *dns.A:
			haveA = true
		}
	}
	if !haveA {
		extra = append(extra, r.a(it, ttl))
	}
	return extra
}

func (r *Responder) buildAnnouncement(it iface, t uint32) []byte {
	m := new(dns.Msg)
	m.Response = true
	m.Authoritative = true
	for _, s := range r.services {
		m.Answer = append(m.Answer, r.ptr(s.typeName(), s.instanceName(), t), r.srv(s, t), r.txt(s, t), r.ptr(servicesEnum, s.typeName(), t))
	}
	m.Answer = append(m.Answer, r.a(it, t))
	b, _ := m.Pack()
	return b
}

func (r *Responder) sendMulticast(it iface, b []byte) {
	if b == nil {
		return
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	ifi := it.ifi
	r.pc.SetMulticastInterface(&ifi)
	r.conn.WriteToUDP(b, groupAddr)
}

func (r *Responder) ptr(name, target string, t uint32) dns.RR {
	return &dns.PTR{Hdr: dns.RR_Header{Name: name, Rrtype: dns.TypePTR, Class: dns.ClassINET, Ttl: t}, Ptr: target}
}

func (r *Responder) srv(s Service, t uint32) dns.RR {
	return &dns.SRV{Hdr: dns.RR_Header{Name: s.instanceName(), Rrtype: dns.TypeSRV, Class: dns.ClassINET | 0x8000, Ttl: t},
		Port: uint16(s.Port), Target: r.host}
}

func (r *Responder) txt(s Service, t uint32) dns.RR {
	return &dns.TXT{Hdr: dns.RR_Header{Name: s.instanceName(), Rrtype: dns.TypeTXT, Class: dns.ClassINET | 0x8000, Ttl: t}, Txt: s.TXT}
}

func (r *Responder) a(it iface, t uint32) dns.RR {
	return &dns.A{Hdr: dns.RR_Header{Name: r.host, Rrtype: dns.TypeA, Class: dns.ClassINET | 0x8000, Ttl: t}, A: it.ip}
}
