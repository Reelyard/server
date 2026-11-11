package api

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/config"
	"github.com/reelyard/server/internal/metadata"
	"github.com/reelyard/server/internal/search"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

// fakeAddons serves the captured Torrentio, OpenSubtitles and Cinemeta
// answers under /torrentio, /opensubs and /cinemeta.
func fakeAddons(t *testing.T) *httptest.Server {
	t.Helper()
	routes := map[string]string{
		"/torrentio/manifest.json":                                                  "torrentio-manifest.json",
		"/torrentio/stream/movie/tt0063350.json":                                    "torrentio-movie.json",
		"/torrentio/stream/series/tt0903747:1:1.json":                               "torrentio-series.json",
		"/opensubs/manifest.json":                                                   "opensubs-manifest.json",
		"/opensubs/subtitles/movie/tt0063350.json":                                  "opensubs-movie.json",
		"/cinemeta/catalog/movie/top/search=night%20of%20the%20living%20dead.json":  "cinemeta-search.json",
		"/cinemeta/catalog/series/top/search=night%20of%20the%20living%20dead.json": "",
		"/cinemeta/meta/series/tt0903747.json":                                      "cinemeta-meta-series.json",
		"/cinemeta/meta/movie/tt0063350.json":                                       "cinemeta-meta-movie.json",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if file == "" {
			io.WriteString(w, `{"metas":[]}`)
			return
		}
		b, err := os.ReadFile(filepath.Join("..", "stremio", "testdata", file))
		if err != nil {
			t.Error(err)
		}
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func newMediaServer(t *testing.T, addons string) *client {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(dir, "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := config.Defaults(false)
	cfg.DataDir = dir
	sc := stremio.New("test")
	sup := &suppliers.Service{Store: st, Client: sc}
	meta := metadata.New(sc)
	meta.Base = addons + "/cinemeta"
	s := &Server{Store: st, Auth: auth.New(st), Config: &cfg, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Suppliers: sup, Meta: meta, Search: &search.Service{Suppliers: sup, Client: sc}}
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	c := &client{t: t, srv: srv, c: &http.Client{Jar: jar}}
	if code, _ := c.do("POST", "/api/v1/setup", `{"username":"admin","password":"long enough pw"}`); code != 201 {
		t.Fatalf("setup: %d", code)
	}
	return c
}

func TestSuppliersAndSearch(t *testing.T) {
	addons := fakeAddons(t)
	c := newMediaServer(t, addons.URL)

	// Add: bad URL, unreachable addon, real ones, duplicate.
	if code, _ := c.do("POST", "/api/v1/suppliers", `{"url":"ftp://nope"}`); code != 400 {
		t.Fatalf("bad url: %d", code)
	}
	if code, body := c.do("POST", "/api/v1/suppliers", `{"url":"`+addons.URL+`/missing/manifest.json"}`); code != 502 || !strings.Contains(body["error"].(string), "HTTP 404") {
		t.Fatalf("missing addon: %d %v", code, body)
	}
	code, body := c.do("POST", "/api/v1/suppliers", `{"url":"`+addons.URL+`/torrentio"}`)
	if code != 201 {
		t.Fatalf("add torrentio: %d %v", code, body)
	}
	tor := body["supplier"].(map[string]any)
	if tor["name"] != "Torrentio" || fmt.Sprint(tor["resources"]) != "[stream]" {
		t.Fatalf("supplier: %v", tor)
	}
	torID := fmt.Sprint(tor["id"])
	if code, _ := c.do("POST", "/api/v1/suppliers", `{"url":"`+addons.URL+`/torrentio/manifest.json"}`); code != 409 {
		t.Fatalf("duplicate: %d", code)
	}
	if code, _ := c.do("POST", "/api/v1/suppliers", `{"url":"`+addons.URL+`/opensubs/manifest.json"}`); code != 201 {
		t.Fatalf("add opensubs: %d", code)
	}
	_, body = c.do("GET", "/api/v1/suppliers", "")
	if n := len(body["suppliers"].([]any)); n != 2 {
		t.Fatalf("list: %d suppliers", n)
	}

	// Test: the movie probe is served by the fixtures.
	code, body = c.do("POST", "/api/v1/suppliers/"+torID+"/test", "")
	if code != 200 || body["ok"] != true {
		t.Fatalf("test: %d %v", code, body)
	}

	// Metadata.
	code, body = c.do("GET", "/api/v1/metadata/search?q=night%20of%20the%20living%20dead", "")
	if code != 200 || len(body["results"].([]any)) == 0 {
		t.Fatalf("metadata search: %d %v", code, body)
	}
	first := body["results"].([]any)[0].(map[string]any)
	if first["id"] != "tt0063350" || first["type"] != "movie" {
		t.Fatalf("first result: %v", first)
	}
	code, body = c.do("GET", "/api/v1/metadata/series/tt0903747", "")
	if code != 200 || len(body["meta"].(map[string]any)["videos"].([]any)) < 60 {
		t.Fatalf("metadata get: %d", code)
	}

	// Streams: movie and episode.
	code, body = c.do("GET", "/api/v1/streams/movie/tt0063350", "")
	if code != 200 {
		t.Fatalf("streams: %d %v", code, body)
	}
	cands := body["candidates"].([]any)
	if len(cands) < 40 {
		t.Fatalf("only %d candidates", len(cands))
	}
	top := cands[0].(map[string]any)
	if top["usable"] != true || top["stream"].(map[string]any)["info"].(map[string]any)["resolution"] != "2160p" {
		t.Fatalf("top candidate: %v", top)
	}
	code, body = c.do("GET", "/api/v1/streams/series/tt0903747:1:1", "")
	if code != 200 || len(body["candidates"].([]any)) < 60 {
		t.Fatalf("episode streams: %d", code)
	}
	if code, _ := c.do("GET", "/api/v1/streams/series/tt0903747", ""); code != 400 {
		t.Fatalf("series without episode: %d", code)
	}

	// Disable → not searched.
	if code, _ := c.do("PATCH", "/api/v1/suppliers/"+torID, `{"enabled":false}`); code != 200 {
		t.Fatalf("disable: %d", code)
	}
	_, body = c.do("GET", "/api/v1/streams/movie/tt0063350", "")
	if len(body["candidates"].([]any)) != 0 {
		t.Fatal("disabled supplier still searched")
	}
	if code, _ := c.do("DELETE", "/api/v1/suppliers/"+torID, ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := c.do("DELETE", "/api/v1/suppliers/"+torID, ""); code != 404 {
		t.Fatalf("delete again: %d", code)
	}
}

func TestDisplayURLHidesConfig(t *testing.T) {
	if got := suppliers.DisplayURL("https://torrentio.strem.fun/realdebrid=SECRET/manifest.json"); strings.Contains(got, "SECRET") {
		t.Fatalf("DisplayURL leaks config: %s", got)
	}
	if got := suppliers.DisplayURL("https://torrentio.strem.fun/manifest.json"); got != "https://torrentio.strem.fun/manifest.json" {
		t.Fatalf("DisplayURL = %s", got)
	}
}
