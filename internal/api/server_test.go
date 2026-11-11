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
	"strings"
	"testing"
	"testing/fstest"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/store"
)

type client struct {
	t   *testing.T
	srv *httptest.Server
	c   *http.Client
}

func newServer(t *testing.T, ui fstest.MapFS) *client {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults(false)
	cfg.DataDir = dir
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if ui != nil {
		s.UI = ui
	}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
}

func (c *client) do(method, path, body string, hdr ...string) (int, map[string]any) {
	c.t.Helper()
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, rd)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := c.c.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	b, _ := io.ReadAll(resp.Body)
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestSetupLoginFlow(t *testing.T) {
	c := newServer(t, nil)

	if code, body := c.do("GET", "/api/v1/health", ""); code != 200 || body["status"] != "ok" {
		t.Fatalf("health: %d %v", code, body)
	}
	if code, _ := c.do("GET", "/api/v1/auth/me", ""); code != 401 {
		t.Fatalf("me before login: %d", code)
	}
	if _, body := c.do("GET", "/api/v1/setup", ""); body["needed"] != true {
		t.Fatalf("setup needed: %v", body)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"short"}`); code != 400 {
		t.Fatalf("weak password: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"evil","password":"long enough pw"}`); code != 409 {
		t.Fatalf("second setup: %d", code)
	}
	// Setup signs you in.
	if code, body := c.do("GET", "/api/v1/auth/me", ""); code != 200 || body["user"].(map[string]any)["username"] != "admin" {
		t.Fatalf("me after setup: %d %v", code, body)
	}
	if code, _ := c.do("GET", "/api/v1/system", ""); code != 200 {
		t.Fatalf("system: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/auth/logout", ""); code != 204 {
		t.Fatalf("logout: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/auth/me", ""); code != 401 {
		t.Fatalf("me after logout: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"wrong pw!"}`); code != 401 {
		t.Fatalf("bad login: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/auth/login", `{"username":"admin","password":"long enough pw"}`); code != 200 {
		t.Fatalf("login: %d", code)
	}

	// API keys.
	code, body := c.do("POST", "/api/v1/auth/keys", `{"name":"script"}`)
	if code != 201 {
		t.Fatalf("create key: %d %v", code, body)
	}
	secret := body["secret"].(string)
	anon := &http.Client{}
	req, _ := http.NewRequest("GET", c.srv.URL+"/api/v1/auth/me", nil)
	req.Header.Set(auth.APIKeyHeader, secret)
	resp, err := anon.Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("api key auth: %v %v", resp.StatusCode, err)
	}
	resp.Body.Close()

	// Password change keeps this session working, with the new password.
	if code, _ := c.do("POST", "/api/v1/auth/password", `{"current":"long enough pw","new":"another long pw"}`); code != 204 {
		t.Fatalf("change password: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/auth/me", ""); code != 200 {
		t.Fatalf("me after password change: %d", code)
	}
}

func TestCrossOriginBlocked(t *testing.T) {
	c := newServer(t, nil)
	code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`,
		"Sec-Fetch-Site", "cross-site", "Origin", "https://evil.example")
	if code != 403 {
		t.Fatalf("cross-site POST: %d, want 403", code)
	}
	code, _ = c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`, "Sec-Fetch-Site", "same-origin")
	if code != 201 {
		t.Fatalf("same-origin POST: %d", code)
	}
}

func TestUnknownAPIAndBadJSON(t *testing.T) {
	c := newServer(t, nil)
	if code, body := c.do("GET", "/api/v1/nope", ""); code != 404 || body["error"] == nil {
		t.Fatalf("unknown api: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"a","password":"long enough pw","extra":1}`); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
}

func TestUIFallback(t *testing.T) {
	ui := fstest.MapFS{
		"index.html":    {Data: []byte("<html>app</html>")},
		"assets/app.js": {Data: []byte("console.log(1)")},
	}
	c := newServer(t, ui)
	get := func(p string) (int, string, string) {
		resp, err := http.Get(c.srv.URL + p)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Cache-Control")
	}
	if code, body, _ := get("/titles/42"); code != 200 || body != "<html>app</html>" {
		t.Fatalf("spa route: %d %q", code, body)
	}
	if code, body, cc := get("/assets/app.js"); code != 200 || body != "console.log(1)" || !strings.Contains(cc, "immutable") {
		t.Fatalf("asset: %d %q %q", code, body, cc)
	}
	if code, _, _ := get("/missing.png"); code != 404 {
		t.Fatalf("missing file: %d", code)
	}
}

func TestUIMissing(t *testing.T) {
	c := newServer(t, nil)
	resp, err := http.Get(c.srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "Reelyard is running") {
		t.Fatalf("missing-ui page: %q", b)
	}
}
