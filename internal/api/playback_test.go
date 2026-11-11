package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/dlna"
	"github.com/reelyard/server/internal/mediaserver"
	"github.com/reelyard/server/internal/store"
)

func TestPlaybackAPI(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// The server isn't started: tests must not announce on the network.
	d := dlna.New(ctx, dlna.Options{Store: st, LibraryDir: dir, Log: log})
	t.Cleanup(d.Close)
	ms := mediaserver.New(ctx, mediaserver.Options{Store: st, Log: log})
	jelly := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), `Token="key1"`) {
			w.WriteHeader(401)
			return
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(jelly.Close)

	cfg := config.Defaults(false)
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: log, DLNA: d, MediaServers: ms}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("GET", "/api/v1/settings/dlna", ""); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}

	code, body := c.do("GET", "/api/v1/settings/dlna", "")
	if code != 200 || body["settings"].(map[string]any)["name"] != "Reelyard" || body["settings"].(map[string]any)["port"] != 8200.0 {
		t.Fatalf("dlna defaults: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/v1/settings/dlna", `{"enabled":false,"name":"","port":8200}`); code != 400 {
		t.Fatalf("empty name: %d", code)
	}
	code, body = c.do("PUT", "/api/v1/settings/dlna", `{"enabled":false,"name":" Den ","port":8201,"interfaces":[]}`)
	if code != 200 || body["settings"].(map[string]any)["name"] != "Den" || body["status"].(map[string]any)["running"] != false {
		t.Fatalf("save dlna: %d %v", code, body)
	}

	if _, body := c.do("POST", "/api/v1/mediaservers/test", `{"kind":"jellyfin","url":"`+jelly.URL+`","token":"nope"}`); body["ok"] != false {
		t.Fatalf("bad token test: %v", body)
	}
	if _, body := c.do("POST", "/api/v1/mediaservers/test", `{"kind":"jellyfin","url":"`+jelly.URL+`","token":"key1"}`); body["ok"] != true {
		t.Fatalf("good token test: %v", body)
	}
	code, body = c.do("POST", "/api/v1/mediaservers", `{"kind":"jellyfin","url":"`+jelly.URL+`","token":"key1","enabled":true}`)
	if code != 200 || body["hasToken"] != true || body["token"] != nil {
		t.Fatalf("save: %d %v", code, body)
	}
	id := body["id"].(string)
	// Saved token is used when the test omits it.
	if _, body := c.do("POST", "/api/v1/mediaservers/test", `{"id":"`+id+`","kind":"jellyfin","url":"`+jelly.URL+`"}`); body["ok"] != true {
		t.Fatalf("saved token test: %v", body)
	}
	// The list never includes tokens.
	resp, err := c.c.Get(srv.URL + "/api/v1/mediaservers")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(raw), "key1") || !strings.Contains(string(raw), id) {
		t.Fatalf("list: %s", raw)
	}
	if code, _ := c.do("POST", "/api/v1/mediaservers/refresh", ""); code != 200 {
		t.Fatalf("refresh: %d", code)
	}
	if code, _ := c.do("PUT", "/api/v1/mediaservers/nope", `{"kind":"plex","url":"http://x"}`); code != 404 {
		t.Fatalf("update unknown: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/mediaservers/"+id, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
}
