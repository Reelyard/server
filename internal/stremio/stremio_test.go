package stremio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestNormalizeManifestURL(t *testing.T) {
	cases := []struct{ in, manifest, base string }{
		{"https://torrentio.strem.fun/manifest.json", "https://torrentio.strem.fun/manifest.json", "https://torrentio.strem.fun"},
		{"  stremio://torrentio.strem.fun/sort=qualitysize/manifest.json ", "https://torrentio.strem.fun/sort=qualitysize/manifest.json", "https://torrentio.strem.fun/sort=qualitysize"},
		{"torrentio.strem.fun", "https://torrentio.strem.fun/manifest.json", "https://torrentio.strem.fun"},
		{"http://192.168.1.5:7000/abc/", "http://192.168.1.5:7000/abc/manifest.json", "http://192.168.1.5:7000/abc"},
		{"https://host/a%7Cb/manifest.json?x=1#y", "https://host/a%7Cb/manifest.json", "https://host/a%7Cb"},
	}
	for _, c := range cases {
		m, b, err := NormalizeManifestURL(c.in)
		if err != nil || m != c.manifest || b != c.base {
			t.Errorf("%q → %q %q %v, want %q %q", c.in, m, b, err, c.manifest, c.base)
		}
	}
	for _, bad := range []string{"", "ftp://x/manifest.json", "https://"} {
		if _, _, err := NormalizeManifestURL(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestResourceURL(t *testing.T) {
	got := resourceURL("https://a.b", ResSubtitles, "series", "tt0903747:1:1", [][2]string{{"videoHash", "abc"}, {"filename", "Breaking Bad S01E01.mkv"}})
	want := "https://a.b/subtitles/series/tt0903747:1:1/videoHash=abc&filename=Breaking%20Bad%20S01E01.mkv.json"
	if got != want {
		t.Errorf("got  %s\nwant %s", got, want)
	}
	if got := resourceURL("https://a.b/", ResCatalog, "movie", "top", [][2]string{{"search", "night of the living dead"}}); got != "https://a.b/catalog/movie/top/search=night%20of%20the%20living%20dead.json" {
		t.Errorf("catalog url %s", got)
	}
}

// fixtureServer serves testdata files keyed by request path.
func fixtureServer(t *testing.T, routes map[string]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		file, ok := routes[r.URL.EscapedPath()]
		if !ok {
			http.NotFound(w, r)
			return
		}
		b, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(b)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestClientAgainstFixtures(t *testing.T) {
	srv := fixtureServer(t, map[string]string{
		"/manifest.json":                                                  "torrentio-manifest.json",
		"/stream/movie/tt0063350.json":                                    "torrentio-movie.json",
		"/stream/series/tt0903747:1:1.json":                               "torrentio-series.json",
		"/subtitles/movie/tt0063350.json":                                 "opensubs-movie.json",
		"/meta/series/tt0903747.json":                                     "cinemeta-meta-series.json",
		"/catalog/movie/top/search=night%20of%20the%20living%20dead.json": "cinemeta-search.json",
	})
	c := New("test")
	ctx := context.Background()

	m, err := c.FetchManifest(ctx, srv.URL+"/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Torrentio" || !m.Supports(ResStream, "movie", "tt0063350") || !m.Supports(ResStream, "series", "tt1:1:1") {
		t.Errorf("manifest: %+v", m)
	}
	if m.Supports(ResStream, "movie", "yt123") || m.Supports(ResSubtitles, "movie", "tt1") {
		t.Error("Supports too permissive")
	}
	if got := strings.Join(m.ResourceNames(), ","); got != "stream" {
		t.Errorf("ResourceNames = %s", got)
	}

	streams, err := c.Streams(ctx, srv.URL, "movie", "tt0063350")
	if err != nil {
		t.Fatal(err)
	}
	if len(streams) != 50 || !streams[0].IsTorrent() || streams[0].FileIdx == nil || streams[0].BehaviorHints.Filename == "" {
		t.Errorf("movie streams: %d %+v", len(streams), streams[0])
	}
	eps, err := c.Streams(ctx, srv.URL, "series", "tt0903747:1:1")
	if err != nil || len(eps) != 75 {
		t.Fatalf("episode streams: %d %v", len(eps), err)
	}

	subs, err := c.Subtitles(ctx, srv.URL, "movie", "tt0063350", SubtitleQuery{})
	if err != nil || len(subs) == 0 || subs[0].Lang == "" || subs[0].URL == "" {
		t.Fatalf("subtitles: %v %v", subs, err)
	}

	meta, err := c.Meta(ctx, srv.URL, "series", "tt0903747")
	if err != nil {
		t.Fatal(err)
	}
	if meta.Name != "Breaking Bad" || len(meta.Videos) < 60 || meta.Videos[0].Released.IsZero() {
		t.Errorf("meta: %s, %d videos", meta.Name, len(meta.Videos))
	}

	found, err := c.Catalog(ctx, srv.URL, "movie", "top", [][2]string{{"search", "night of the living dead"}})
	if err != nil || len(found) == 0 || found[0].ID != "tt0063350" {
		t.Fatalf("catalog: %v %v", found, err)
	}

	if _, err := c.Streams(ctx, srv.URL, "movie", "tt0000000"); err == nil {
		t.Fatal("expected HTTP error")
	} else if he, ok := err.(*HTTPError); !ok || he.Status != 404 {
		t.Fatalf("err = %v", err)
	}
}

func TestStringResources(t *testing.T) {
	srv := fixtureServer(t, map[string]string{"/manifest.json": "opensubs-manifest.json"})
	m, err := New("t").FetchManifest(context.Background(), srv.URL+"/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if !m.Supports(ResSubtitles, "series", "tt1:1:1") || m.Supports(ResStream, "movie", "tt1") {
		t.Errorf("opensubs manifest supports wrong things: %+v", m.Resources)
	}
}
