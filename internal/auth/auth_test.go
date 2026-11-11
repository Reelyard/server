package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/reelyard/server/internal/store"
)

func newTest(t *testing.T) (*Service, *store.User) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	h, err := HashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.CreateUser(ctx, "admin", h)
	if err != nil {
		t.Fatal(err)
	}
	return New(s), u
}

func TestPasswordRules(t *testing.T) {
	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Fatalf("err = %v", err)
	}
	if err := ValidateUsername("has space"); err == nil {
		t.Fatal("username with space accepted")
	}
}

func TestLoginAndAuthenticate(t *testing.T) {
	a, u := newTest(t)
	ctx := context.Background()

	if _, _, err := a.Login(ctx, "admin", "wrong pass", "", "ip"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password err = %v", err)
	}
	if _, _, err := a.Login(ctx, "nobody", "whatever1", "", "ip"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("unknown user err = %v", err)
	}
	tok, got, err := a.Login(ctx, "ADMIN", "correct horse", "ua", "ip")
	if err != nil || got.ID != u.ID {
		t.Fatalf("login: %v %v", got, err)
	}

	r := httptest.NewRequest("GET", "/", nil)
	r.AddCookie(SessionCookie(tok, false))
	if got, err := a.Authenticate(r); err != nil || got.ID != u.ID {
		t.Fatalf("Authenticate cookie: %v %v", got, err)
	}

	if err := a.Logout(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Authenticate(r); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after logout err = %v", err)
	}

	raw, _, err := a.NewAPIKey(ctx, u.ID, "script")
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("GET", "/", nil)
	r.Header.Set(APIKeyHeader, raw)
	if got, err := a.Authenticate(r); err != nil || got.ID != u.ID {
		t.Fatalf("Authenticate api key: %v %v", got, err)
	}
	r.Header.Set(APIKeyHeader, raw+"x")
	if _, err := a.Authenticate(r); err == nil {
		t.Fatal("bad api key accepted")
	}
}

func TestLoginRateLimit(t *testing.T) {
	a, _ := newTest(t)
	ctx := context.Background()
	clock := time.Now()
	a.now = func() time.Time { return clock }
	for i := 0; i < 5; i++ {
		a.Login(ctx, "admin", "nope nope", "", "1.1.1.1")
	}
	if _, _, err := a.Login(ctx, "admin", "correct horse", "", "1.1.1.1"); !errors.Is(err, ErrTooManyTries) {
		t.Fatalf("err = %v, want ErrTooManyTries", err)
	}
	// Another address is not affected.
	if _, _, err := a.Login(ctx, "admin", "correct horse", "", "2.2.2.2"); err != nil {
		t.Fatalf("other ip: %v", err)
	}
	clock = clock.Add(61 * time.Second)
	if _, _, err := a.Login(ctx, "admin", "correct horse", "", "1.1.1.1"); err != nil {
		t.Fatalf("after window: %v", err)
	}
}
