package discovery

import (
	"net/netip"
	"testing"

	"golang.org/x/net/dns/dnsmessage"
)

func query(t *testing.T, name string, typ dnsmessage.Type, qu bool) *dnsmessage.Message {
	t.Helper()
	class := dnsmessage.ClassINET
	if qu {
		class |= unicastReply
	}
	m := &dnsmessage.Message{Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName(name), Type: typ, Class: class}}}
	// Through the wire format, as a real query arrives.
	b, err := m.Pack()
	if err != nil {
		t.Fatal(err)
	}
	var out dnsmessage.Message
	if err := out.Unpack(b); err != nil {
		t.Fatal(err)
	}
	return &out
}

func types(rs []dnsmessage.Resource) map[dnsmessage.Type]int {
	out := map[dnsmessage.Type]int{}
	for _, r := range rs {
		out[r.Header.Type]++
	}
	return out
}

func TestAnswers(t *testing.T) {
	rec, err := newRecords(Options{Instance: "Reelyard on nas.home", Host: "reelyard-1a2b3c4d", Port: 8787, TXT: []string{"id=1a2b", "api=1"}})
	if err != nil {
		t.Fatal(err)
	}
	addrs := []netip.Addr{netip.MustParseAddr("192.168.1.20"), netip.MustParseAddr("fe80::1")}

	// Browsing: the PTR, with SRV, TXT and the address alongside.
	resp, unicast := rec.answer(query(t, "_reelyard._tcp.local.", dnsmessage.TypePTR, false), addrs)
	if resp == nil || unicast {
		t.Fatalf("browse: %v %v", resp, unicast)
	}
	if got := types(resp.Answers); got[dnsmessage.TypePTR] != 1 || len(resp.Answers) != 1 {
		t.Fatalf("browse answers: %v", resp.Answers)
	}
	if got := types(resp.Additionals); got[dnsmessage.TypeSRV] != 1 || got[dnsmessage.TypeTXT] != 1 || got[dnsmessage.TypeA] != 1 {
		t.Fatalf("browse additionals: %v", got)
	}
	ptr := resp.Answers[0].Body.(*dnsmessage.PTRResource).PTR.String()
	if ptr != "Reelyard on nas-home._reelyard._tcp.local." {
		t.Fatalf("instance name %q", ptr)
	}
	if _, err := resp.Pack(); err != nil {
		t.Fatal(err)
	}

	// Resolving the instance, asking for a unicast reply; names are case-insensitive.
	resp, unicast = rec.answer(query(t, "REELYARD on nas-home._reelyard._tcp.local.", dnsmessage.TypeSRV, true), addrs)
	if resp == nil || !unicast || types(resp.Answers)[dnsmessage.TypeSRV] != 1 {
		t.Fatalf("resolve: %v %v", resp, unicast)
	}
	if srv := resp.Answers[0].Body.(*dnsmessage.SRVResource); srv.Port != 8787 || srv.Target.String() != "reelyard-1a2b3c4d.local." {
		t.Fatalf("srv: %+v", srv)
	}
	if resp.Answers[0].Header.Class != dnsmessage.ClassINET|cacheFlush {
		t.Fatal("SRV should set cache-flush")
	}

	// The host's address, and the list of service types.
	if resp, _ := rec.answer(query(t, "reelyard-1a2b3c4d.local.", dnsmessage.TypeA, false), addrs); resp == nil ||
		resp.Answers[0].Body.(*dnsmessage.AResource).A != [4]byte{192, 168, 1, 20} {
		t.Fatalf("A: %v", resp)
	}
	if resp, _ := rec.answer(query(t, "_services._dns-sd._udp.local.", dnsmessage.TypePTR, false), addrs); resp == nil {
		t.Fatal("service types not answered")
	}

	// Questions about anything else, and other responders' answers, are ignored.
	if resp, _ := rec.answer(query(t, "_googlecast._tcp.local.", dnsmessage.TypePTR, false), addrs); resp != nil {
		t.Fatalf("answered another service: %v", resp)
	}
	q := query(t, "_reelyard._tcp.local.", dnsmessage.TypePTR, false)
	q.Header.Response = true
	if resp, _ := rec.answer(q, addrs); resp != nil {
		t.Fatal("answered a response")
	}

	// The goodbye carries TTL 0.
	for _, rr := range rec.announcement(addrs, true).Answers {
		if rr.Header.TTL != 0 {
			t.Fatalf("goodbye TTL %d", rr.Header.TTL)
		}
	}
}
