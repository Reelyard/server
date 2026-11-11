package engine

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
)

// Network binding ("kill switch"): with Options.BindInterface set, every
// torrent socket (peers, DHT, trackers, web seeds) is bound to that
// interface's IPv4 address. Traffic then can't leave by any other route:
// if the VPN drops, the address disappears and the sockets stop working.
// A watchdog also pauses all torrents while the address is missing, so the
// state is visible in the UI, and asks for a restart when the address
// changes (sockets stay bound to the old one).

// NetworkStatus describes the binding.
type NetworkStatus struct {
	Interface string `json:"interface,omitempty"` // "" = not bound
	Address   string `json:"address,omitempty"`   // address the sockets use
	OK        bool   `json:"ok"`
	Message   string `json:"message,omitempty"`
}

// InterfaceIPv4 returns the first IPv4 address of an interface that is up.
func InterfaceIPv4(name string) (net.IP, error) {
	ifc, err := net.InterfaceByName(name)
	if err != nil {
		return nil, fmt.Errorf("network interface %q not found", name)
	}
	if ifc.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("network interface %q is down", name)
	}
	addrs, err := ifc.Addrs()
	if err != nil {
		return nil, err
	}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			return ipn.IP.To4(), nil
		}
	}
	return nil, fmt.Errorf("network interface %q has no IPv4 address", name)
}

// bindConfig sets the client config to use only bindIP.
func bindConfig(cfg *torrent.ClientConfig, bindIP net.IP) {
	ip := bindIP.String()
	cfg.ListenHost = func(network string) string { return ip }
	cfg.DisableIPv6 = true
	cfg.NoDefaultPortForwarding = true // no UPnP on other interfaces
	dialer := &net.Dialer{LocalAddr: &net.TCPAddr{IP: bindIP}, Timeout: 30 * time.Second}
	cfg.TrackerDialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasSuffix(network, "6") {
			return nil, errors.New("IPv6 disabled while bound")
		}
		return dialer.DialContext(ctx, network, addr)
	}
	cfg.HTTPDialContext = cfg.TrackerDialContext
	cfg.TrackerListenPacket = func(network, _ string) (net.PacketConn, error) {
		if strings.HasSuffix(network, "6") {
			return nil, errors.New("IPv6 disabled while bound")
		}
		return net.ListenPacket("udp4", net.JoinHostPort(ip, "0"))
	}
}

// Network returns the binding state.
func (e *Engine) Network() NetworkStatus {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.net
}

// checkNetwork compares the interface's address with the bound one and
// pauses or resumes torrents. Caller holds e.mu.
func (e *Engine) checkNetwork() {
	name := e.opts.BindInterface
	if name == "" {
		return
	}
	cur, err := e.opts.InterfaceAddr(name)
	switch {
	case err != nil:
		e.guard(false, fmt.Sprintf("Paused: %s. Torrents won't use any other connection.", err))
	case !cur.Equal(e.boundIP):
		e.guard(false, fmt.Sprintf("Paused: %s now has address %s, but Harbor is bound to %s. Restart Harbor to use the new address.", name, cur, e.boundIP))
		if e.opts.OnRebind != nil && !e.rebindAsked {
			e.rebindAsked = true
			go e.opts.OnRebind()
		}
	default:
		e.guard(true, "")
	}
}

func (e *Engine) guard(ok bool, msg string) {
	if e.net.OK == ok && e.net.Message == msg {
		return
	}
	was := e.net.OK
	e.net.OK, e.net.Message = ok, msg
	for _, j := range e.jobs {
		if j.t != nil {
			e.applyGuard(j.t)
		}
	}
	switch {
	case was && !ok:
		e.opts.Log.Warn("network guard: paused", "interface", e.opts.BindInterface, "reason", msg)
		e.activity("error", "", msg)
	case !was && ok:
		e.opts.Log.Info("network guard: resumed", "interface", e.opts.BindInterface)
		e.activity("info", "", fmt.Sprintf("%s is back (%s); torrents resumed", e.opts.BindInterface, e.boundIP))
	}
}

// applyGuard blocks or allows a torrent's data transfer.
func (e *Engine) applyGuard(t *torrent.Torrent) {
	if e.opts.BindInterface == "" {
		return
	}
	if e.net.OK {
		t.AllowDataDownload()
		t.AllowDataUpload()
	} else {
		t.DisallowDataDownload()
		t.DisallowDataUpload()
	}
}
