package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/dlna"
	"github.com/reelyard/server/internal/store"
)

func TestViewersAPI(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(dir, "v.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.DiscardHandler)
	// Not started: tests must not announce on the network.
	d := dlna.New(ctx, dlna.Options{Store: st, LibraryDir: dir, Log: log})
	t.Cleanup(d.Close)
	cfg := config.Defaults(false)
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: log, DLNA: d}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("GET", "/api/v1/viewers", ""); code != 401 {
		t.Fatalf("unauthenticated: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}

	if code, _ := c.do("POST", "/api/v1/viewers", `{"name":"  "}`); code != 400 {
		t.Fatalf("empty name: %d", code)
	}
	code, body := c.do("POST", "/api/v1/viewers", `{"name":" Kids ","allTitles":false,"titles":["tt1","tt1"," tt2"],"dlna":true,"tvs":["192.168.1.20",""," 192.168.1.20"]}`)
	if code != 201 || body["name"] != "Kids" || body["slot"] != 1.0 || len(body["titles"].([]any)) != 2 || len(body["tvs"].([]any)) != 1 {
		t.Fatalf("create: %d %v", code, body)
	}
	id := strconv.Itoa(int(body["id"].(float64)))
	if code, _ := c.do("POST", "/api/v1/viewers", `{"name":"kids","allTitles":true}`); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	code, body = c.do("PUT", "/api/v1/viewers/"+id, `{"name":"Kids","allTitles":true,"titles":["tt9"],"dlna":false,"tvs":[]}`)
	if code != 200 || body["allTitles"] != true || len(body["titles"].([]any)) != 0 || body["dlna"] != false {
		t.Fatalf("update: %d %v", code, body)
	}
	if code, _ := c.do("PUT", "/api/v1/viewers/999", `{"name":"X","allTitles":true}`); code != 404 {
		t.Fatalf("update unknown: %d", code)
	}
	resp, err := c.c.Get(srv.URL + "/api/v1/viewers")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil || len(list) != 1 || list[0]["name"] != "Kids" {
		t.Fatalf("list: %s %v", raw, err)
	}
	// A viewer can't sign in, even with an empty password.
	if code, _ := c.do("POST", "/api/v1/auth/login", `{"username":"Kids","password":""}`); code != 401 {
		t.Fatalf("viewer login: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/viewers/"+id, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/viewers/"+id, ""); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
	if code, _ := c.do("PUT", "/api/v1/dlna/tvs/192.168.1.99", `{"name":"Den"}`); code != 404 {
		t.Fatalf("name unknown TV: %d", code)
	}
}
