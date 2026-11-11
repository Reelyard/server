package engine

import (
	"context"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/inluxc/harbor/internal/store"
)

// fakeIfc is an interface whose address the test controls.
type fakeIfc struct {
	mu sync.Mutex
	ip net.IP
}

func (f *fakeIfc) addr(name string) (net.IP, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ip == nil {
		return nil, errors.New(`network interface "` + name + `" is down`)
	}
	return f.ip, nil
}

func (f *fakeIfc) set(ip net.IP) { f.mu.Lock(); f.ip = ip; f.mu.Unlock() }

func TestBoundDownloadAndGuard(t *testing.T) {
	ctx := context.Background()
	ih, peer := seeder(t, map[string][]byte{"Bound.mkv": randBytes(200 << 10)})
	base := t.TempDir()
	st, _ := store.Open(ctx, filepath.Join(base, "h.db"))
	defer st.Close()
	ifc := &fakeIfc{ip: net.IPv4(127, 0, 0, 1).To4()}
	var rebinds atomic.Int32
	e, err := New(ctx, Options{Store: st, Log: testLog(), DataDir: filepath.Join(base, "d"), DownloadDir: filepath.Join(base, "dl"),
		NoDHT: true, NoDefaultTrackers: true, Tick: 20 * time.Millisecond,
		BindInterface: "harbor0", InterfaceAddr: ifc.addr, OnRebind: func() { rebinds.Add(1) }})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	if n := e.Network(); !n.OK || n.Address != "127.0.0.1" || n.Interface != "harbor0" {
		t.Fatalf("network: %+v", n)
	}
	// Downloads work through the bound address.
	s, _ := e.Add(ctx, AddRequest{InfoHash: ih, Name: "Bound", Peers: []string{peer}})
	waitFor(t, e, s.ID, "seeding", func(s Status) bool { return s.State == store.StateSeeding })

	// Interface gone: guard trips.
	ifc.set(nil)
	waitNet(t, e, false, "is down")
	// Back: resumes.
	ifc.set(net.IPv4(127, 0, 0, 1).To4())
	waitNet(t, e, true, "")
	// Different address: paused and a restart requested once.
	ifc.set(net.IPv4(127, 0, 0, 2).To4())
	waitNet(t, e, false, "Restart Harbor")
	time.Sleep(100 * time.Millisecond)
	if rebinds.Load() != 1 {
		t.Fatalf("OnRebind called %d times", rebinds.Load())
	}
}

func TestStartWithInterfaceDown(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	st, _ := store.Open(ctx, filepath.Join(base, "h.db"))
	defer st.Close()
	ifc := &fakeIfc{}
	e, err := New(ctx, Options{Store: st, Log: testLog(), DataDir: filepath.Join(base, "d"), DownloadDir: filepath.Join(base, "dl"),
		NoDHT: true, NoDefaultTrackers: true, Tick: 20 * time.Millisecond, BindInterface: "wg0", InterfaceAddr: ifc.addr})
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	n := e.Network()
	if n.OK || n.Address != "127.0.0.1" || !strings.Contains(n.Message, "restart Harbor") {
		t.Fatalf("network with interface down: %+v", n)
	}
}

func waitNet(t *testing.T, e *Engine, ok bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		n := e.Network()
		if n.OK == ok && strings.Contains(n.Message, msg) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("network never became ok=%v %q: %+v", ok, msg, e.Network())
}
