package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMigrateIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "h.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := s.SchemaVersion(ctx)
	s.Close()

	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	v2, _ := s.SchemaVersion(ctx)
	if v1 == 0 || v1 != v2 {
		t.Fatalf("schema version %d then %d", v1, v2)
	}
}

func TestUsersSessionsAndKeys(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)

	if n, _ := s.CountUsers(ctx); n != 0 {
		t.Fatalf("CountUsers = %d", n)
	}
	u, err := s.CreateFirstUser(ctx, "Admin", "hash1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateFirstUser(ctx, "other", "x"); !errors.Is(err, ErrUsersExist) {
		t.Fatalf("second CreateFirstUser err = %v, want ErrUsersExist", err)
	}
	if got, err := s.UserByName(ctx, "admin"); err != nil || got.ID != u.ID {
		t.Fatalf("UserByName case-insensitive: %v %v", got, err)
	}

	if err := s.CreateSession(ctx, "tok", u.ID, time.Now().Add(time.Hour), "ua", "1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateSession(ctx, "old", u.ID, time.Now().Add(-time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if got, err := s.SessionUser(ctx, "tok"); err != nil || got.ID != u.ID {
		t.Fatalf("SessionUser: %v %v", got, err)
	}
	if _, err := s.SessionUser(ctx, "old"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session err = %v", err)
	}
	if n, _ := s.PruneSessions(ctx); n != 1 {
		t.Fatalf("PruneSessions = %d", n)
	}

	// Changing the password ends sessions.
	if err := s.SetPassword(ctx, u.ID, "hash2"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SessionUser(ctx, "tok"); !errors.Is(err, ErrNotFound) {
		t.Fatal("session survived password change")
	}

	k, err := s.CreateAPIKey(ctx, u.ID, "script", "hbr_abcd", "keyhash")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.APIKeyUser(ctx, "keyhash"); err != nil || got.ID != u.ID {
		t.Fatalf("APIKeyUser: %v %v", got, err)
	}
	keys, _ := s.ListAPIKeys(ctx, u.ID)
	if len(keys) != 1 || keys[0].LastUsedAt == nil {
		t.Fatalf("ListAPIKeys = %+v", keys)
	}
	if err := s.DeleteAPIKey(ctx, u.ID, k.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAPIKey(ctx, u.ID, k.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete err = %v", err)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := openTest(t)
	var v map[string]int
	if err := s.GetSetting(ctx, "x", &v); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	if err := s.SetSetting(ctx, "x", map[string]int{"a": 1}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "x", map[string]int{"a": 2}); err != nil {
		t.Fatal(err)
	}
	if err := s.GetSetting(ctx, "x", &v); err != nil || v["a"] != 2 {
		t.Fatalf("GetSetting = %v %v", v, err)
	}
}
