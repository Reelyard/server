package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/inluxc/harbor/internal/auth"
	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/metadata"
	"github.com/inluxc/harbor/internal/profile"
	"github.com/inluxc/harbor/internal/search"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/stremio"
	"github.com/inluxc/harbor/internal/suppliers"
	"github.com/inluxc/harbor/internal/tracker"
)

func newTrackingServer(t *testing.T) *client {
	t.Helper()
	ctx := context.Background()
	addons := fakeAddons(t)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := profile.EnsureDefaults(ctx, st); err != nil {
		t.Fatal(err)
	}
	sc := stremio.New("test")
	sup := &suppliers.Service{Store: st, Client: sc}
	if _, err := sup.Add(ctx, addons.URL+"/torrentio/manifest.json"); err != nil {
		t.Fatal(err)
	}
	meta := metadata.New(sc)
	meta.Base = addons.URL + "/cinemeta"
	srch := &search.Service{Suppliers: sup, Client: sc}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	trk := tracker.New(tracker.Options{Store: st, Meta: meta, Search: srch, Log: log})
	cfg := config.Defaults(false)
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: log, Suppliers: sup, Meta: meta, Search: srch, Tracker: trk}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	return c
}

func TestProfilesAndTracking(t *testing.T) {
	c := newTrackingServer(t)

	_, body := c.do("GET", "/api/v1/profiles", "")
	if n := len(body["profiles"].([]any)); n != 3 {
		t.Fatalf("default profiles: %d", n)
	}
	if code, b := c.do("POST", "/api/v1/profiles", `{"name":"Bad","resolutions":["8k"]}`); code != 400 {
		t.Fatalf("bad profile: %d %v", code, b)
	}
	code, body := c.do("POST", "/api/v1/profiles", `{"name":"PT 720p","resolutions":["720p","1080p"],"audioLangs":["jpn"],"audioRequired":true,"subtitleLangs":["en"],"minSeeders":1}`)
	if code != 201 {
		t.Fatalf("create profile: %d %v", code, body)
	}
	pid := fmt.Sprint(body["profile"].(map[string]any)["id"])
	if body["profile"].(map[string]any)["audioLangs"].([]any)[0] != "ja" {
		t.Fatalf("language not normalized: %v", body)
	}
	if code, _ := c.do("POST", "/api/v1/profiles", `{"name":"pt 720P","resolutions":["720p"]}`); code != 409 {
		t.Fatalf("duplicate name: %d", code)
	}

	if code, _ := c.do("GET", "/api/v1/titles/by-imdb/tt0063350", ""); code != 404 {
		t.Fatalf("untracked lookup: %d", code)
	}
	code, body = c.do("POST", "/api/v1/titles", `{"imdbId":"tt0063350","type":"movie","profileId":`+pid+`}`)
	if code != 201 {
		t.Fatalf("track: %d %v", code, body)
	}
	tid := fmt.Sprint(body["title"].(map[string]any)["id"])
	if items := body["items"].([]any); len(items) != 1 || items[0].(map[string]any)["state"] != "missing" {
		t.Fatalf("items: %v", items)
	}
	if code, _ := c.do("POST", "/api/v1/titles", `{"imdbId":"tt0063350","type":"movie","profileId":1}`); code != 409 {
		t.Fatalf("track twice: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/profiles/"+pid, ""); code != 409 {
		t.Fatalf("delete used profile: %d", code)
	}
	if code, body := c.do("GET", "/api/v1/titles/by-imdb/tt0063350", ""); code != 200 || body["title"].(map[string]any)["name"] != "Night of the Living Dead" {
		t.Fatalf("lookup: %d %v", code, body)
	}

	// Streams judged against the profile: the Japanese-required profile
	// rejects everything in this fixture with a reason.
	code, body = c.do("GET", "/api/v1/streams/movie/tt0063350?profile="+pid, "")
	if code != 200 || !strings.Contains(body["summary"].(string), "none fit") {
		t.Fatalf("judged streams: %d %v", code, body["summary"])
	}
	first := body["candidates"].([]any)[0].(map[string]any)
	if first["eval"].(map[string]any)["accepted"] != false {
		t.Fatalf("first candidate: %v", first["eval"])
	}
	code, body = c.do("GET", "/api/v1/streams/movie/tt0063350?profile=1", "")
	if code != 200 || body["candidates"].([]any)[0].(map[string]any)["eval"].(map[string]any)["accepted"] != true {
		t.Fatalf("HD profile should accept something: %d %v", code, body["summary"])
	}

	if code, _ := c.do("PATCH", "/api/v1/titles/"+tid, `{"profileId":1,"monitor":"sometimes"}`); code != 400 {
		t.Fatalf("bad monitor: %d", code)
	}
	if code, body := c.do("PATCH", "/api/v1/titles/"+tid, `{"profileId":1,"monitor":"all"}`); code != 200 || body["title"].(map[string]any)["profileId"].(float64) != 1 {
		t.Fatalf("update: %d %v", code, body)
	}
	if code, _ := c.do("POST", "/api/v1/titles/"+tid+"/search", ""); code != 202 {
		t.Fatalf("search now: %d", code)
	}
	_, body = c.do("GET", "/api/v1/titles", "")
	if n := len(body["titles"].([]any)); n != 1 {
		t.Fatalf("titles: %d", n)
	}
	if code, _ := c.do("DELETE", "/api/v1/titles/"+tid, ""); code != 204 {
		t.Fatalf("untrack: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/profiles/"+pid, ""); code != 204 {
		t.Fatalf("delete unused profile: %d", code)
	}
}
