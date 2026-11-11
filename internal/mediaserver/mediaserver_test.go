package mediaserver

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/inluxc/harbor/internal/store"
)

type fake struct {
	mu   sync.Mutex
	hits []string
}

func (f *fake) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("X-Plex-Token") + r.Header.Get("X-Emby-Token")
		if a := r.Header.Get("Authorization"); strings.Contains(a, `Token="good"`) {
			auth = "good"
		}
		f.mu.Lock()
		f.hits = append(f.hits, r.Method+" "+r.URL.Path)
		f.mu.Unlock()
		if auth != "good" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func (f *fake) got() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.hits...)
}

func open(t *testing.T) *store.Store {
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func TestRefreshCalls(t *testing.T) {
	ctx := context.Background()
	f := &fake{}
	srv := httptest.NewServer(f.handler(t))
	defer srv.Close()
	st := open(t)
	s := New(ctx, Options{Store: st, Log: slog.New(slog.DiscardHandler), Delay: 20 * time.Millisecond})

	for _, kind := range []string{Jellyfin, Emby, Plex} {
		if err := s.Test(ctx, Target{Kind: kind, URL: srv.URL + "/", Token: "good"}); err != nil {
			t.Fatalf("%s test: %v", kind, err)
		}
		if err := s.Test(ctx, Target{Kind: kind, URL: srv.URL, Token: "bad"}); err == nil || !strings.Contains(err.Error(), "refused") {
			t.Fatalf("%s bad token: %v", kind, err)
		}
		if _, err := s.Save(ctx, Target{Kind: kind, URL: srv.URL, Token: "good", Enabled: true}); err != nil {
			t.Fatal(err)
		}
	}
	// Three kicks close together make one refresh per server.
	s.Kick()
	s.Kick()
	s.Kick()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		ok := 0
		for _, v := range s.List() {
			if v.LastAt != nil {
				ok++
			}
		}
		if ok == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	var refresh []string
	for _, h := range f.got() {
		if strings.Contains(h, "efresh") {
			refresh = append(refresh, h)
		}
	}
	want := []string{"POST /Library/Refresh", "POST /Library/Refresh", "GET /library/sections/all/refresh"}
	if strings.Join(refresh, ",") != strings.Join(want, ",") {
		t.Fatalf("refresh calls = %v, want %v", refresh, want)
	}
	for _, v := range s.List() {
		if v.LastError != "" || !v.HasToken {
			t.Fatalf("view %+v", v)
		}
	}
}

func TestSaveKeepsTokenAndPersists(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	s := New(ctx, Options{Store: st})
	if _, err := s.Save(ctx, Target{Kind: Jellyfin, URL: "192.168.1.10:8096"}); err == nil {
		t.Fatal("a new server needs a token")
	}
	if _, err := s.Save(ctx, Target{Kind: "kodi", URL: "http://x", Token: "t"}); err == nil {
		t.Fatal("unknown kind accepted")
	}
	if _, err := s.Save(ctx, Target{Kind: Plex, URL: "ftp://x", Token: "t"}); err == nil {
		t.Fatal("bad url accepted")
	}
	v, err := s.Save(ctx, Target{Kind: Jellyfin, URL: "192.168.1.10:8096/", Token: "secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if v.URL != "http://192.168.1.10:8096" || v.Name != "Jellyfin" {
		t.Fatalf("saved %+v", v)
	}
	if _, err := s.Save(ctx, Target{ID: v.ID, Kind: Jellyfin, Name: "Living room", URL: v.URL}); err != nil {
		t.Fatal(err)
	}
	s2 := New(ctx, Options{Store: st})
	if len(s2.targets) != 1 || s2.targets[0].Token != "secret" || s2.targets[0].Name != "Living room" || s2.targets[0].Enabled {
		t.Fatalf("reloaded %+v", s2.targets)
	}
	if err := s2.Delete(ctx, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := s2.Delete(ctx, v.ID); err != ErrNotFound {
		t.Fatalf("second delete: %v", err)
	}
}

func TestReachErr(t *testing.T) {
	ctx := context.Background()
	srv := httptest.NewServer(http.NotFoundHandler())
	addr := srv.URL
	srv.Close() // nothing listens there now
	s := New(ctx, Options{Store: open(t)})
	err := s.Test(ctx, Target{Kind: Jellyfin, URL: addr, Token: "x"})
	if err == nil || !strings.Contains(err.Error(), "nothing is answering") {
		t.Fatalf("refused: %v", err)
	}
	err = s.Test(ctx, Target{Kind: Plex, URL: "http://harbor-test.invalid:32400", Token: "x"})
	if err == nil || !strings.Contains(err.Error(), "couldn't find the host") {
		t.Fatalf("dns: %v", err)
	}
}
