package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/store"
)

// Saving one library setting leaves the others as they were.
func TestLibrarySettingsPartialSave(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "l.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults(false)
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: slog.New(slog.DiscardHandler)}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	if _, body := c.do("GET", "/api/v1/settings/library", ""); !strings.Contains(js(body), `"audioFirst":true`) {
		t.Fatalf("default: %s", body)
	}
	if code, body := c.do("PUT", "/api/v1/settings/library", `{"audioFirst":false}`); code != 200 || !strings.Contains(js(body), `"subtitleLangs":["en"]`) {
		t.Fatalf("turn off: %d %s", code, body)
	}
	if code, body := c.do("PUT", "/api/v1/settings/library", `{"subtitleLangs":["pt"]}`); code != 200 || !strings.Contains(js(body), `"audioFirst":false`) {
		t.Fatalf("subtitles only: %d %s", code, body)
	}
}

func js(v any) string { b, _ := json.Marshal(v); return string(b) }
