package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/inluxc/harbor/internal/auth"
	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/store"
)

type clientsEnv struct {
	*client
	st  *store.Store
	lib string
	app *client // no cookies: a device
}

func newClientsServer(t *testing.T) *clientsEnv {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "api.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults(false)
	cfg.DataDir = dir
	cfg.LibraryDir = filepath.Join(dir, "library")
	if err := os.MkdirAll(cfg.LibraryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	admin := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := admin.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	return &clientsEnv{client: admin, st: st, lib: cfg.LibraryDir, app: &client{t: t, srv: srv, c: &http.Client{}}}
}

// pair runs the whole pairing flow and returns the device token.
func (e *clientsEnv) pair(name string) string {
	e.t.Helper()
	code, body := e.app.do("POST", "/api/v1/devices/pair", `{"name":"`+name+`","platform":"tizen"}`)
	if code != 201 {
		e.t.Fatalf("pair: %d %v", code, body)
	}
	pc, _ := body["code"].(string)
	poll, _ := body["pollSecret"].(string)
	if len(pc) != 7 || pc[3] != '-' || !strings.HasPrefix(poll, "hbp_") || body["interval"] != float64(3) {
		e.t.Fatalf("pair body: %v", body)
	}
	if code, body := e.app.do("POST", "/api/v1/devices/pair/token", `{"pollSecret":"`+poll+`"}`); code != 202 || body["status"] != "pending" {
		e.t.Fatalf("poll before approval: %d %v", code, body)
	}
	// Typed loosely on purpose: lower case, a space instead of the dash.
	typed := strings.ToLower(strings.Replace(pc, "-", " ", 1))
	if code, body := e.do("POST", "/api/v1/devices/approve", `{"code":"`+typed+`"}`); code != 200 {
		e.t.Fatalf("approve: %d %v", code, body)
	}
	code, body = e.app.do("POST", "/api/v1/devices/pair/token", `{"pollSecret":"`+poll+`"}`)
	tok, _ := body["token"].(string)
	if code != 200 || !strings.HasPrefix(tok, "hbd_") {
		e.t.Fatalf("collect: %d %v", code, body)
	}
	if code, _ := e.app.do("POST", "/api/v1/devices/pair/token", `{"pollSecret":"`+poll+`"}`); code != 404 {
		e.t.Fatalf("second collect: %d, want 404", code)
	}
	return tok
}

func TestDevicePairing(t *testing.T) {
	e := newClientsServer(t)

	if code, _ := e.do("POST", "/api/v1/devices/approve", `{"code":"AAA-AAA"}`); code != 404 {
		t.Fatalf("approve unknown code: %d", code)
	}
	if code, _ := e.app.do("POST", "/api/v1/devices/pair", `{"name":"  "}`); code != 400 {
		t.Fatalf("pair without a name: %d", code)
	}
	if code, _ := e.app.do("POST", "/api/v1/devices/pair", `{"name":"x","extra":1}`); code != 400 {
		t.Fatalf("pair with unknown field: %d", code)
	}

	tok := e.pair("Living room TV")
	bearer := []string{"Authorization", "Bearer " + tok}
	if code, body := e.app.do("GET", "/api/v1/auth/me", "", bearer...); code != 200 {
		t.Fatalf("me with device token: %d %v", code, body)
	}
	if code, _ := e.app.do("GET", "/api/v1/auth/me", "", "Authorization", "Bearer hbd_nope"); code != 401 {
		t.Fatalf("me with a wrong token: %d", code)
	}
	if code, _ := e.app.do("GET", "/api/v1/auth/me", "", "Authorization", "Basic abc"); code != 401 {
		t.Fatalf("me with Basic: %d", code)
	}

	_, body := e.do("GET", "/api/v1/devices", "")
	ds, _ := body["devices"].([]any)
	if len(ds) != 1 {
		t.Fatalf("devices: %v", body)
	}
	d := ds[0].(map[string]any)
	if d["name"] != "Living room TV" || d["platform"] != "tizen" || d["paired"] != true || d["lastUsedAt"] == nil {
		t.Fatalf("device: %v", d)
	}

	// A pending pairing shows up too, and can be turned down.
	_, pb := e.app.do("POST", "/api/v1/devices/pair", `{"name":"Phone"}`)
	_, body = e.do("GET", "/api/v1/devices", "")
	if ds, _ := body["devices"].([]any); len(ds) != 2 || ds[1].(map[string]any)["paired"] != false {
		t.Fatalf("devices with a pending one: %v", body)
	}
	pendingID := body["devices"].([]any)[1].(map[string]any)["id"].(float64)
	if code, _ := e.do("DELETE", "/api/v1/devices/"+itoa(pendingID), ""); code != 204 {
		t.Fatalf("turn down: %d", code)
	}
	if code, _ := e.app.do("POST", "/api/v1/devices/pair/token", `{"pollSecret":"`+pb["pollSecret"].(string)+`"}`); code != 404 {
		t.Fatalf("poll after turned down: %d", code)
	}

	// Removing the device signs it out.
	if code, _ := e.do("DELETE", "/api/v1/devices/"+itoa(d["id"].(float64)), ""); code != 204 {
		t.Fatalf("remove: %d", code)
	}
	if code, _ := e.app.do("GET", "/api/v1/auth/me", "", bearer...); code != 401 {
		t.Fatalf("me after removal: %d", code)
	}
}

// itoa formats a JSON number (an id) for a URL.
func itoa(f float64) string { return strconv.FormatInt(int64(f), 10) }

func TestPendingPairingsAreCapped(t *testing.T) {
	e := newClientsServer(t)
	for i := range auth.MaxPendingPairings {
		if code, _ := e.app.do("POST", "/api/v1/devices/pair", `{"name":"tv"}`); code != 201 {
			t.Fatalf("pair %d: %d", i, code)
		}
	}
	if code, _ := e.app.do("POST", "/api/v1/devices/pair", `{"name":"tv"}`); code != 429 {
		t.Fatalf("pair over the cap: %d, want 429", code)
	}
}

func TestHeaderCredentialsNeverFallBackToCookie(t *testing.T) {
	e := newClientsServer(t)
	// Signed in by cookie, but a bad Bearer token: judged on the header alone.
	if code, _ := e.do("GET", "/api/v1/auth/me", "", "Authorization", "Bearer hbd_bad"); code != 401 {
		t.Fatalf("bad bearer with a good cookie: %d, want 401", code)
	}
	if code, _ := e.do("GET", "/api/v1/auth/me", ""); code != 200 {
		t.Fatalf("cookie alone: %d", code)
	}
}

func TestCrossSiteRules(t *testing.T) {
	e := newClientsServer(t)
	cross := []string{"Sec-Fetch-Site", "cross-site", "Origin", "http://tv.app"}

	// Cookie request from another site: still blocked.
	if code, _ := e.do("POST", "/api/v1/devices/approve", `{"code":"AAAAAA"}`, cross...); code != 403 {
		t.Fatalf("cross-site cookie POST: %d, want 403", code)
	}
	// Pairing from a TV app's origin: allowed, with CORS.
	req, _ := http.NewRequest("POST", e.srv.URL+"/api/v1/devices/pair", strings.NewReader(`{"name":"TV"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(cross[0], cross[1])
	req.Header.Set(cross[2], cross[3])
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 201 || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("cross-site pair: %d, ACAO %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}
	// A device's own token from another site: allowed.
	tok := e.pair("TV")
	h := append([]string{"Authorization", "Bearer " + tok}, cross...)
	if code, _ := e.app.do("POST", "/api/v1/devices/approve", `{"code":"AAAAAA"}`, h...); code != 404 {
		t.Fatalf("cross-site bearer POST: %d, want 404 (passed the check)", code)
	}

	// Preflight.
	req, _ = http.NewRequest("OPTIONS", e.srv.URL+"/api/v1/library/titles", nil)
	req.Header.Set("Origin", "http://tv.app")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 || !strings.Contains(resp.Header.Get("Access-Control-Allow-Headers"), "Authorization") ||
		resp.Header.Get("Access-Control-Allow-Credentials") != "" {
		t.Fatalf("preflight: %d %v", resp.StatusCode, resp.Header)
	}
}

func (e *clientsEnv) addFile(titleType, titleID, name, rel string, size int) *store.MediaFile {
	e.t.Helper()
	full := filepath.Join(e.lib, filepath.FromSlash(rel))
	if size >= 0 {
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			e.t.Fatal(err)
		}
		b := make([]byte, size)
		for i := range b {
			b[i] = byte('a' + i%26)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			e.t.Fatal(err)
		}
	}
	m, err := e.st.UpsertMediaFile(context.Background(), &store.MediaFile{
		TitleType: titleType, TitleID: titleID, TitleName: name, Path: rel, Size: int64(size), Method: "copy",
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return m
}

func get(t *testing.T, url string, hdr ...string) (*http.Response, string) {
	t.Helper()
	req, _ := http.NewRequest("GET", url, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp, string(b)
}

func TestStreamLinks(t *testing.T) {
	e := newClientsServer(t)
	m := e.addFile("movie", "tt0063350", "Night of the Living Dead", "Movies/Night (1968)/Night (1968) - 720p.mkv", 1000)
	id := itoa(float64(m.ID))

	if code, _ := e.app.do("POST", "/api/v1/library/"+id+"/play", ""); code != 401 {
		t.Fatalf("play without signing in: %d", code)
	}
	code, body := e.do("POST", "/api/v1/library/"+id+"/play", "")
	url, _ := body["url"].(string)
	if code != 200 || !strings.HasPrefix(url, "/api/v1/play/"+id+"?exp=") || body["contentType"] != "video/x-matroska" {
		t.Fatalf("play: %d %v", code, body)
	}

	// A player with only the link: byte ranges work.
	resp, got := get(t, e.srv.URL+url, "Range", "bytes=10-19")
	if resp.StatusCode != 206 || got != "klmnopqrst" || resp.Header.Get("Content-Range") != "bytes 10-19/1000" ||
		resp.Header.Get("Content-Type") != "video/x-matroska" {
		t.Fatalf("range: %d %q %v", resp.StatusCode, got, resp.Header)
	}
	if resp, _ := get(t, e.srv.URL+url); resp.StatusCode != 200 || resp.ContentLength != 1000 {
		t.Fatalf("whole file: %d %d", resp.StatusCode, resp.ContentLength)
	}
	// Tampered or moved to another file: refused.
	if resp, _ := get(t, e.srv.URL+url[:len(url)-1]+"0"); resp.StatusCode != 403 {
		t.Fatalf("bad signature: %d", resp.StatusCode)
	}
	other := e.addFile("movie", "tt0000002", "Other", "Movies/Other/Other.mp4", 10)
	if resp, _ := get(t, e.srv.URL+strings.Replace(url, "/"+id+"?", "/"+itoa(float64(other.ID))+"?", 1)); resp.StatusCode != 403 {
		t.Fatalf("signature for another file: %d", resp.StatusCode)
	}
	// No link: needs a credential.
	if resp, _ := get(t, e.srv.URL+"/api/v1/play/"+id); resp.StatusCode != 401 {
		t.Fatalf("no link, no credential: %d", resp.StatusCode)
	}
	tok := e.pair("TV")
	if resp, _ := get(t, e.srv.URL+"/api/v1/play/"+id, "Authorization", "Bearer "+tok, "Range", "bytes=0-2"); resp.StatusCode != 206 {
		t.Fatalf("device token: %d", resp.StatusCode)
	}

	// A path outside the library folder is never served.
	esc := e.addFile("movie", "tt0000003", "Escape", "../outside.mp4", -1)
	if err := os.WriteFile(filepath.Join(filepath.Dir(e.lib), "outside.mp4"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	if resp, _ := get(t, e.srv.URL+"/api/v1/play/"+itoa(float64(esc.ID)), "Authorization", "Bearer "+tok); resp.StatusCode != 410 {
		t.Fatalf("path outside the library: %d, want 410", resp.StatusCode)
	}
	// A file gone from disk.
	gone := e.addFile("movie", "tt0000004", "Gone", "Movies/Gone/Gone.mp4", -1)
	if code, _ := e.do("POST", "/api/v1/library/"+itoa(float64(gone.ID))+"/play", ""); code != 410 {
		t.Fatalf("play a missing file: %d", code)
	}
}

func TestLibraryByTitle(t *testing.T) {
	e := newClientsServer(t)
	e.addFile("movie", "tt0063350", "Night of the Living Dead", "Movies/N/N.mp4", 1)
	e.addFile("series", "tt0903747:1:2", "Breaking Bad", "TV/BB/Season 01/BB - S01E02.mkv", 1)
	e.addFile("series", "tt0903747:1:1", "Breaking Bad", "TV/BB/Season 01/BB - S01E01.mkv", 1)
	e.addFile("series", "tt0903747:1:1", "Breaking Bad", "TV/BB/Season 01/BB - S01E01 - 2160p.mkv", 1)
	e.addFile("series", "tt0903747:2:1", "Breaking Bad", "TV/BB/Season 02/BB - S02E01.mkv", 1)

	code, body := e.do("GET", "/api/v1/library/titles", "")
	ts, _ := body["titles"].([]any)
	if code != 200 || len(ts) != 2 {
		t.Fatalf("titles: %d %v", code, body)
	}
	var bb map[string]any
	for _, x := range ts {
		if m := x.(map[string]any); m["imdbId"] == "tt0903747" {
			bb = m
		}
	}
	if bb == nil || bb["files"] != float64(4) || bb["episodes"] != float64(3) || len(bb["seasons"].([]any)) != 2 || bb["tracked"] != false {
		t.Fatalf("series entry: %v", bb)
	}

	code, body = e.do("GET", "/api/v1/library/titles/tt0903747", "")
	seasons, _ := body["seasons"].([]any)
	if code != 200 || len(seasons) != 2 {
		t.Fatalf("series detail: %d %v", code, body)
	}
	s1 := seasons[0].(map[string]any)
	eps := s1["episodes"].([]any)
	if s1["season"] != float64(1) || len(eps) != 2 || eps[0].(map[string]any)["episode"] != float64(1) ||
		len(eps[0].(map[string]any)["files"].([]any)) != 2 {
		t.Fatalf("season 1: %v", s1)
	}
	code, body = e.do("GET", "/api/v1/library/titles/tt0063350", "")
	if fs, _ := body["files"].([]any); code != 200 || len(fs) != 1 || len(body["seasons"].([]any)) != 0 {
		t.Fatalf("movie detail: %d %v", code, body)
	}
	if code, _ := e.do("GET", "/api/v1/library/titles/tt9999999", ""); code != 404 {
		t.Fatalf("unknown title: %d", code)
	}
	if code, _ := e.do("GET", "/api/v1/library/titles/nope", ""); code != 400 {
		t.Fatalf("bad id: %d", code)
	}
}
