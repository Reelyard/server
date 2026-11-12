package dlna

// Duplicate servers on the network.
//
// dms derives a server's device ID (UDN) from its name alone, so two
// installs that keep the default name "Reelyard" announce the same device.
// TVs then treat them as one server that keeps changing address and
// content. Reelyard can't set the ID, so it looks for the clash instead:
// shortly after starting and then every few minutes it searches the network
// for media servers (SSDP), reads each one's description, and reports any
// that isn't one of its own but carries one of its device IDs.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/xml"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/anacrolix/dms/upnp"
	"golang.org/x/net/ipv4"
)

const (
	conflictFirstCheck = 3 * time.Second
	conflictEvery      = 10 * time.Minute
	// searchWait is how long answers to a search are collected (MX + 1).
	searchWait = 3 * time.Second
)

// deviceUUID is the device ID dms gives a server called name.
func deviceUUID(name string) string {
	sum := md5.Sum([]byte(name))
	return upnp.FormatUUID(sum[:])
}

// searchFunc returns the description URLs of media servers that answer a
// search sent from these local IPs.
type searchFunc func(ctx context.Context, ips []string) []string

// watchConflicts checks for clashing servers until stop is closed. gen is
// the restart it belongs to; results from an older restart are dropped.
func (s *Server) watchConflicts(gen int, ips []string, stop <-chan struct{}) {
	search := s.search
	if search == nil {
		search = ssdpSearch
	}
	t := time.NewTimer(conflictFirstCheck)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), searchWait+10*time.Second)
		s.checkConflicts(ctx, gen, search(ctx, ips))
		cancel()
		t.Reset(conflictEvery)
	}
}

// checkConflicts reads each found server's description and records, per
// running server, another device that has its ID.
func (s *Server) checkConflicts(ctx context.Context, gen int, locations []string) {
	s.mu.Lock()
	if gen != s.gen {
		s.mu.Unlock()
		return
	}
	own := map[string]bool{} // our servers' "ip:port"
	byUUID := map[string]int{}
	for i, ss := range s.status.Servers {
		byUUID[deviceUUID(ss.Name)] = i
		for _, ip := range s.ips {
			own[net.JoinHostPort(ip, strconv.Itoa(ss.Port))] = true
		}
	}
	s.mu.Unlock()

	found := map[int]string{}
	client := &http.Client{Timeout: 3 * time.Second}
	seen := map[string]bool{}
	for _, loc := range locations {
		u, err := url.Parse(loc)
		if err != nil || u.Host == "" || own[u.Host] || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		udn, err := fetchUDN(ctx, client, loc)
		if err != nil {
			continue
		}
		if i, ok := byUUID[udn]; ok {
			if _, dup := found[i]; !dup {
				found[i] = u.Hostname()
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if gen != s.gen {
		return
	}
	for i := range s.status.Servers {
		ss := &s.status.Servers[i]
		was := ss.Conflict
		ss.Conflict = found[i]
		if ss.Conflict != "" && ss.Conflict != was {
			s.opts.Log.Warn("dlna: another server on the network has the same name, so TVs mix them up; rename one",
				"name", ss.Name, "other", ss.Conflict)
		}
	}
}

// fetchUDN reads a device description's UDN.
func fetchUDN(ctx context.Context, client *http.Client, loc string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, loc, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var desc struct {
		Device struct {
			UDN string `xml:"UDN"`
		} `xml:"device"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 256<<10)).Decode(&desc); err != nil {
		return "", err
	}
	return strings.TrimSpace(desc.Device.UDN), nil
}

// ssdpSearch sends an SSDP M-SEARCH for media servers from each local IP and
// collects the LOCATION of every answer.
func ssdpSearch(ctx context.Context, ips []string) []string {
	const msg = "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\n" +
		"ST: urn:schemas-upnp-org:device:MediaServer:1\r\n\r\n"
	group := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	var out []string
	for _, ip := range ips {
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.ParseIP(ip)})
		if err != nil {
			continue
		}
		if ifc := interfaceOf(ip); ifc != nil {
			_ = ipv4.NewPacketConn(conn).SetMulticastInterface(ifc)
		}
		for i := 0; i < 2; i++ { // UDP may drop one
			_, _ = conn.WriteTo([]byte(msg), group)
		}
		deadline := time.Now().Add(searchWait)
		if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
			deadline = d
		}
		_ = conn.SetReadDeadline(deadline)
		buf := make([]byte, 4096)
		for {
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				break
			}
			if loc := ssdpLocation(buf[:n]); loc != "" {
				out = append(out, loc)
			}
		}
		conn.Close()
	}
	return out
}

// ssdpLocation returns the LOCATION header of an SSDP answer.
func ssdpLocation(b []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(b))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), "LOCATION") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func interfaceOf(ip string) *net.Interface {
	ifs, _ := net.Interfaces()
	for _, ifc := range ifs {
		for _, a := range ipv4s(ifc) {
			if a == ip {
				return &ifc
			}
		}
	}
	return nil
}
