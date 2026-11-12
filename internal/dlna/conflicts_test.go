package dlna

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anacrolix/dms/dlna/dms"
)

// deviceUUID must match the UDN dms really announces for a name.
func TestDeviceUUIDMatchesDMS(t *testing.T) {
	root := library(t, "Movies/A (2001)/A (2001).mkv")
	pipe := newPipeListener(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8200})
	d := &dms.Server{FS: libraryFS(root.Name()), HTTPConn: pipe, FriendlyName: "Reelyard", Interfaces: []net.Interface{},
		RootObjectPath: root.Name(), Logger: slog.New(slog.DiscardHandler), NoTranscode: true, NoProbe: true,
		AllowedIpNets: []*net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}}}
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	go d.Run()
	t.Cleanup(func() { d.Close() })
	c := &http.Client{Transport: &http.Transport{DialContext: pipe.dial}}
	resp, err := c.Get("http://dms/rootDesc.xml")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if want := "<UDN>" + deviceUUID("Reelyard") + "</UDN>"; !strings.Contains(string(body), want) {
		t.Fatalf("dms description lacks %s:\n%s", want, body)
	}
}

func TestCheckConflicts(t *testing.T) {
	desc := func(udn string) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device><friendlyName>x</friendlyName><UDN>`+udn+`</UDN></device></root>`)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	twin := desc(deviceUUID("Reelyard"))   // another install with the default name
	other := desc(deviceUUID("Living TV")) // an unrelated server

	s := &Server{opts: Options{Log: slog.New(slog.DiscardHandler)}, gen: 3, ips: []string{"192.168.1.5"},
		status: Status{Servers: []ServerStatus{{Name: "Reelyard", Port: 8200}, {Name: "Reelyard · Kids", Port: 8201}}}}
	ctx := context.Background()
	s.checkConflicts(ctx, 3, []string{
		"http://192.168.1.5:8200/rootDesc.xml", // ourselves: skipped, never fetched
		other.URL + "/rootDesc.xml",
		twin.URL + "/rootDesc.xml",
		"not a url\x7f",
	})
	if got := s.Status().Servers; got[0].Conflict != "127.0.0.1" || got[1].Conflict != "" {
		t.Fatalf("conflicts: %+v", got)
	}
	// A later check without the twin clears it.
	s.checkConflicts(ctx, 3, []string{other.URL + "/rootDesc.xml"})
	if got := s.Status().Servers; got[0].Conflict != "" {
		t.Fatalf("not cleared: %+v", got)
	}
	// Results from before a restart are dropped.
	s.checkConflicts(ctx, 2, []string{twin.URL + "/rootDesc.xml"})
	if got := s.Status().Servers; got[0].Conflict != "" {
		t.Fatalf("stale check applied: %+v", got)
	}
}

func TestSSDPLocation(t *testing.T) {
	answer := "HTTP/1.1 200 OK\r\nCACHE-CONTROL: max-age=1800\r\nlocation: http://192.168.0.162:8200/rootDesc.xml\r\nST: urn:schemas-upnp-org:device:MediaServer:1\r\n\r\n"
	if got := ssdpLocation([]byte(answer)); got != "http://192.168.0.162:8200/rootDesc.xml" {
		t.Fatalf("%q", got)
	}
	if got := ssdpLocation([]byte("NOTIFY * HTTP/1.1\r\n\r\n")); got != "" {
		t.Fatalf("%q", got)
	}
}
