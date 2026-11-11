package auth

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/inluxc/harbor/internal/store"
)

func TestNormaliseCode(t *testing.T) {
	for in, want := range map[string]string{
		"K7Q-4MD":  "K7Q4MD",
		"k7q 4md":  "K7Q4MD",
		" k7q4md ": "K7Q4MD",
		"O0I1L":    "", // look-alikes aren't in the alphabet
	} {
		if got := NormaliseCode(in); got != want {
			t.Errorf("NormaliseCode(%q) = %q, want %q", in, got, want)
		}
	}
	for range 100 {
		c := randomCode()
		if len(c) != codeLen || NormaliseCode(c) != c {
			t.Fatalf("random code %q", c)
		}
	}
}

func TestStreamSigner(t *testing.T) {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s, err := NewStreamSigner(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 11, 11, 20, 0, 0, 0, time.UTC)
	s.now = func() time.Time { return clock }

	q, exp := s.Sign(7)
	if !exp.Equal(clock.Add(StreamLinkTTL)) {
		t.Fatalf("expires %v", exp)
	}
	v, _ := url.ParseQuery(q)
	if !s.Valid(7, v.Get("exp"), v.Get("sig")) {
		t.Fatal("fresh link rejected")
	}
	if s.Valid(8, v.Get("exp"), v.Get("sig")) {
		t.Fatal("link accepted for another file")
	}
	if s.Valid(7, "9999999999", v.Get("sig")) {
		t.Fatal("link accepted with a later expiry")
	}
	clock = clock.Add(StreamLinkTTL + time.Second)
	if s.Valid(7, v.Get("exp"), v.Get("sig")) {
		t.Fatal("expired link accepted")
	}

	// The key survives a restart, so links stay valid.
	s2, err := NewStreamSigner(context.Background(), st)
	if err != nil {
		t.Fatal(err)
	}
	s2.now = func() time.Time { return clock.Add(-time.Hour) }
	if !s2.Valid(7, v.Get("exp"), v.Get("sig")) {
		t.Fatal("link rejected after reloading the key")
	}
	// A signer without a key rejects everything.
	if (&StreamSigner{}).Valid(7, v.Get("exp"), v.Get("sig")) {
		t.Fatal("keyless signer accepted a link")
	}
}
