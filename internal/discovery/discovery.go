// Package discovery announces Reelyard on the local network with multicast
// DNS (DNS-SD service _reelyard._tcp), so apps on phones, TVs and computers
// can find it without anyone typing an address.
//
// It is a small responder: it answers questions about its own records only,
// and shares port 5353 with the system's own (Avahi, Bonjour).
package discovery

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/ipv4"
)

// ServiceType is what apps browse for.
const ServiceType = "_reelyard._tcp.local."

const (
	servicesName = "_services._dns-sd._udp.local."
	hostTTL      = 120  // seconds, for SRV and A (RFC 6762 §10)
	otherTTL     = 4500 // for PTR and TXT
	cacheFlush   = 1 << 15
	unicastReply = 1 << 15
)

var group = &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}

// Options describe the service to announce.
type Options struct {
	// Instance is the name people see, e.g. "Reelyard on nas".
	Instance string
	// Host is the host label to use, without ".local" (unique per server).
	Host string
	Port int
	// TXT holds key=value pairs (id, version, api).
	TXT []string
	// ExcludeInterface is never announced on (the VPN interface torrents use).
	ExcludeInterface string
	Log              *slog.Logger
}

// records are the answers this responder gives.
type records struct {
	instance, host dnsmessage.Name
	service, svcs  dnsmessage.Name
	port           uint16
	txt            []string
}

func newRecords(o Options) (*records, error) {
	inst := strings.NewReplacer(".", "-", "\\", "").Replace(o.Instance)
	if len(inst) > 63 {
		inst = inst[:63]
	}
	var r records
	var err error
	names := []struct {
		dst *dnsmessage.Name
		s   string
	}{
		{&r.service, ServiceType}, {&r.svcs, servicesName},
		{&r.instance, inst + "." + ServiceType}, {&r.host, o.Host + ".local."},
	}
	for _, n := range names {
		if *n.dst, err = dnsmessage.NewName(n.s); err != nil {
			return nil, err
		}
	}
	r.port, r.txt = uint16(o.Port), o.TXT
	if len(r.txt) == 0 {
		r.txt = []string{""}
	}
	return &r, nil
}

func hdr(name dnsmessage.Name, typ dnsmessage.Type, ttl uint32, unique bool) dnsmessage.ResourceHeader {
	class := dnsmessage.ClassINET
	if unique {
		class |= cacheFlush
	}
	return dnsmessage.ResourceHeader{Name: name, Type: typ, Class: class, TTL: ttl}
}

func (r *records) ptr(ttl uint32) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(r.service, dnsmessage.TypePTR, ttl, false), Body: &dnsmessage.PTRResource{PTR: r.instance}}
}

func (r *records) srv(ttl uint32) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(r.instance, dnsmessage.TypeSRV, ttl, true), Body: &dnsmessage.SRVResource{Target: r.host, Port: r.port}}
}

func (r *records) txtRR(ttl uint32) dnsmessage.Resource {
	return dnsmessage.Resource{Header: hdr(r.instance, dnsmessage.TypeTXT, ttl, true), Body: &dnsmessage.TXTResource{TXT: r.txt}}
}

func (r *records) a(addrs []netip.Addr, ttl uint32) []dnsmessage.Resource {
	var out []dnsmessage.Resource
	for _, a := range addrs {
		if a.Is4() {
			out = append(out, dnsmessage.Resource{Header: hdr(r.host, dnsmessage.TypeA, ttl, true), Body: &dnsmessage.AResource{A: a.As4()}})
		}
	}
	return out
}

// announcement is every record at once: sent on start-up, and with ttl 0
// as a goodbye on shut-down.
func (r *records) announcement(addrs []netip.Addr, goodbye bool) *dnsmessage.Message {
	ptrTTL, hTTL := uint32(otherTTL), uint32(hostTTL)
	if goodbye {
		ptrTTL, hTTL = 0, 0
	}
	m := &dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}}
	m.Answers = append(m.Answers, r.ptr(ptrTTL), r.srv(hTTL), r.txtRR(ptrTTL))
	m.Answers = append(m.Answers, r.a(addrs, hTTL)...)
	return m
}

// answer builds the reply to a query, or nil when it asks nothing about us.
// unicast is true when every question asked for a unicast reply.
func (r *records) answer(q *dnsmessage.Message, addrs []netip.Addr) (resp *dnsmessage.Message, unicast bool) {
	if q.Header.Response || q.Header.OpCode != 0 {
		return nil, false
	}
	m := &dnsmessage.Message{Header: dnsmessage.Header{Response: true, Authoritative: true}}
	seen := map[string]bool{}
	add := func(dst *[]dnsmessage.Resource, rs ...dnsmessage.Resource) {
		for _, rr := range rs {
			key := rr.Header.Name.String() + rr.Header.Type.String() + rr.Body.GoString()
			if !seen[key] {
				seen[key] = true
				*dst = append(*dst, rr)
			}
		}
	}
	unicast = len(q.Questions) > 0
	extra := false // add SRV, TXT and A as additional records
	for _, qq := range q.Questions {
		if qq.Class&unicastReply == 0 {
			unicast = false
		}
		if c := qq.Class &^ unicastReply; c != dnsmessage.ClassINET && c != dnsmessage.ClassANY {
			continue
		}
		name, t := strings.ToLower(qq.Name.String()), qq.Type
		is := func(types ...dnsmessage.Type) bool {
			for _, x := range types {
				if t == x || t == dnsmessage.TypeALL {
					return true
				}
			}
			return false
		}
		switch name {
		case strings.ToLower(r.service.String()):
			if is(dnsmessage.TypePTR) {
				add(&m.Answers, r.ptr(otherTTL))
				extra = true
			}
		case strings.ToLower(r.svcs.String()):
			if is(dnsmessage.TypePTR) {
				add(&m.Answers, dnsmessage.Resource{Header: hdr(r.svcs, dnsmessage.TypePTR, otherTTL, false), Body: &dnsmessage.PTRResource{PTR: r.service}})
			}
		case strings.ToLower(r.instance.String()):
			if is(dnsmessage.TypeSRV) {
				add(&m.Answers, r.srv(hostTTL))
				add(&m.Additionals, r.a(addrs, hostTTL)...)
			}
			if is(dnsmessage.TypeTXT) {
				add(&m.Answers, r.txtRR(otherTTL))
			}
		case strings.ToLower(r.host.String()):
			if is(dnsmessage.TypeA) {
				add(&m.Answers, r.a(addrs, hostTTL)...)
			}
		}
	}
	if len(m.Answers) == 0 {
		return nil, false
	}
	if extra {
		add(&m.Additionals, r.srv(hostTTL), r.txtRR(otherTTL))
		add(&m.Additionals, r.a(addrs, hostTTL)...)
	}
	return m, unicast
}

// Responder answers mDNS queries until closed.
type Responder struct {
	o    Options
	rec  *records
	conn *ipv4.PacketConn
	raw  net.PacketConn

	mu     sync.Mutex
	joined map[int]net.Interface
	cancel context.CancelFunc
	done   chan struct{}
}

// Start opens the mDNS socket, announces the service and starts answering.
func Start(o Options) (*Responder, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	rec, err := newRecords(o)
	if err != nil {
		return nil, err
	}
	lc := net.ListenConfig{Control: reuseControl}
	raw, err := lc.ListenPacket(context.Background(), "udp4", "0.0.0.0:5353")
	if err != nil {
		return nil, err
	}
	p := ipv4.NewPacketConn(raw)
	_ = p.SetControlMessage(ipv4.FlagInterface, true) // not on Windows: answers then carry every address
	_ = p.SetMulticastTTL(255)
	_ = p.SetMulticastLoopback(true)
	ctx, cancel := context.WithCancel(context.Background())
	r := &Responder{o: o, rec: rec, conn: p, raw: raw, joined: map[int]net.Interface{}, cancel: cancel, done: make(chan struct{})}
	if r.join() == 0 {
		cancel()
		raw.Close()
		return nil, errors.New("no network interface to announce on")
	}
	go r.serve()
	go r.maintain(ctx)
	return r, nil
}

// Close says goodbye on the network and stops answering.
func (r *Responder) Close() {
	r.cancel()
	r.broadcast(true)
	r.raw.Close()
	<-r.done
}

// join joins the mDNS group on every usable interface not joined yet, and
// returns how many are joined.
func (r *Responder) join() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	ifcs, _ := net.Interfaces()
	for _, ifc := range ifcs {
		if _, ok := r.joined[ifc.Index]; ok || !usable(ifc, r.o.ExcludeInterface) {
			continue
		}
		if err := r.conn.JoinGroup(&ifc, group); err != nil {
			r.o.Log.Debug("mdns: join", "interface", ifc.Name, "err", err)
			continue
		}
		r.joined[ifc.Index] = ifc
	}
	return len(r.joined)
}

func usable(ifc net.Interface, exclude string) bool {
	if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagMulticast == 0 || ifc.Flags&net.FlagLoopback != 0 {
		return false
	}
	if exclude != "" && ifc.Name == exclude {
		return false
	}
	return len(addrsOf(ifc)) > 0
}

func addrsOf(ifc net.Interface) []netip.Addr {
	as, _ := ifc.Addrs()
	var out []netip.Addr
	for _, a := range as {
		if n, ok := a.(*net.IPNet); ok {
			if ip, ok := netip.AddrFromSlice(n.IP.To4()); ok && n.IP.To4() != nil && !ip.IsLinkLocalUnicast() {
				out = append(out, ip)
			}
		}
	}
	return out
}

// maintain announces at start (twice, a second apart, as RFC 6762 §8.3
// asks), then picks up interfaces that appear later.
func (r *Responder) maintain(ctx context.Context) {
	r.broadcast(false)
	t := time.NewTimer(time.Second)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-t.C:
	}
	r.broadcast(false)
	tick := time.NewTicker(time.Minute)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			r.mu.Lock()
			before := len(r.joined)
			r.mu.Unlock()
			if r.join() > before {
				r.broadcast(false)
			}
		}
	}
}

// broadcast sends the announcement (or goodbye) on every joined interface.
func (r *Responder) broadcast(goodbye bool) {
	r.mu.Lock()
	ifcs := make([]net.Interface, 0, len(r.joined))
	for _, ifc := range r.joined {
		ifcs = append(ifcs, ifc)
	}
	r.mu.Unlock()
	for _, ifc := range ifcs {
		b, err := r.rec.announcement(addrsOf(ifc), goodbye).Pack()
		if err == nil {
			r.send(b, &ifc, group)
		}
	}
}

func (r *Responder) send(b []byte, ifc *net.Interface, to net.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if ifc != nil {
		_ = r.conn.SetMulticastInterface(ifc)
	}
	if _, err := r.conn.WriteTo(b, nil, to); err != nil {
		r.o.Log.Debug("mdns: send", "err", err)
	}
}

func (r *Responder) serve() {
	defer close(r.done)
	buf := make([]byte, 9000)
	for {
		n, cm, src, err := r.conn.ReadFrom(buf)
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}
		var q dnsmessage.Message
		if q.Unpack(buf[:n]) != nil {
			continue
		}
		// Answer with the addresses of the interface the question came in on.
		r.mu.Lock()
		var ifc *net.Interface
		if cm != nil {
			if i, ok := r.joined[cm.IfIndex]; ok {
				ifc = &i
			} else {
				r.mu.Unlock()
				continue // an interface we don't announce on
			}
		}
		var addrs []netip.Addr
		if ifc != nil {
			addrs = addrsOf(*ifc)
		} else {
			for _, i := range r.joined {
				addrs = append(addrs, addrsOf(i)...)
			}
		}
		r.mu.Unlock()
		resp, unicast := r.rec.answer(&q, addrs)
		if resp == nil {
			continue
		}
		b, err := resp.Pack()
		if err != nil {
			continue
		}
		// A legacy query (not from port 5353) or a QU question gets a direct reply.
		if u, ok := src.(*net.UDPAddr); ok && (unicast || u.Port != 5353) {
			if u.Port != 5353 {
				resp.Header.ID = q.Header.ID
				resp.Questions = q.Questions
				if b, err = resp.Pack(); err != nil {
					continue
				}
			}
			r.send(b, nil, src)
			continue
		}
		r.send(b, ifc, group)
	}
}
