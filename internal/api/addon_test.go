package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/store"
)

func addonGet(t *testing.T, url string) (int, map[string]any) {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("%s: no CORS header", url)
	}
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

func TestAddon(t *testing.T) {
	e := newClientsServer(t)
	ctx := context.Background()
	film := e.addFile("movie", "tt0063350", "Night of the Living Dead", "Films/notld.mkv", 2048)
	e.addFile("series", "tt0000002:1:1", "Some Series", "Series/s01e01.mp4", 1024)

	v := &store.Viewer{Name: "Kid", Titles: []string{"tt0000002"}}
	if err := e.st.CreateViewer(ctx, v); err != nil {
		t.Fatal(err)
	}

	code, body := e.do("GET", "/api/v1/addon", "")
	links, _ := body["links"].([]any)
	if code != 200 || len(links) != 2 || links[0].(map[string]any)["url"] != "" {
		t.Fatalf("list: %d %v", code, body)
	}
	me := int64(links[0].(map[string]any)["userId"].(float64))

	code, body = e.do("POST", "/api/v1/addon/"+i64(me), "")
	ownerURL, _ := body["url"].(string)
	if code != 201 || !strings.HasSuffix(ownerURL, "/manifest.json") {
		t.Fatalf("new link: %d %v", code, body)
	}
	_, body = e.do("POST", "/api/v1/addon/"+i64(v.ID), "")
	kidURL := body["url"].(string)
	if code, _ := e.do("POST", "/api/v1/addon/999", ""); code != 404 {
		t.Fatalf("link for a stranger: %d", code)
	}

	// The manifest.
	code, m := addonGet(t, ownerURL)
	if code != 200 || m["name"] != "Reelyard" || len(m["catalogs"].([]any)) != 2 {
		t.Fatalf("manifest: %d %v", code, m)
	}
	if _, m := addonGet(t, kidURL); m["name"] != "Reelyard · Kid" {
		t.Fatalf("kid manifest: %v", m)
	}
	ownerBase := strings.TrimSuffix(ownerURL, "/manifest.json")
	kidBase := strings.TrimSuffix(kidURL, "/manifest.json")

	// Catalogs: the owner sees the film; the kid doesn't.
	_, c := addonGet(t, ownerBase+"/catalog/movie/reelyard-movies.json")
	if metas := c["metas"].([]any); len(metas) != 1 || metas[0].(map[string]any)["id"] != "tt0063350" {
		t.Fatalf("owner films: %v", c)
	}
	_, c = addonGet(t, kidBase+"/catalog/movie/reelyard-movies.json")
	if len(c["metas"].([]any)) != 0 {
		t.Fatalf("kid films: %v", c)
	}
	_, c = addonGet(t, kidBase+"/catalog/series/reelyard-series.json")
	if len(c["metas"].([]any)) != 1 {
		t.Fatalf("kid series: %v", c)
	}
	_, c = addonGet(t, ownerBase+"/catalog/movie/reelyard-movies/search=night.json")
	_, none := addonGet(t, ownerBase+"/catalog/movie/reelyard-movies/search=zzz.json")
	_, skipped := addonGet(t, ownerBase+"/catalog/movie/reelyard-movies/skip=100.json")
	if len(c["metas"].([]any)) != 1 || len(none["metas"].([]any)) != 0 || len(skipped["metas"].([]any)) != 0 {
		t.Fatalf("search/skip: %v %v %v", c, none, skipped)
	}

	// Streams play through a signed link.
	_, st := addonGet(t, ownerBase+"/stream/movie/tt0063350.json")
	streams := st["streams"].([]any)
	if len(streams) != 1 {
		t.Fatalf("streams: %v", st)
	}
	url := streams[0].(map[string]any)["url"].(string)
	if !strings.Contains(url, "/api/v1/play/"+i64(film.ID)+"?") {
		t.Fatalf("stream url %q", url)
	}
	resp, got := get(t, url)
	if resp.StatusCode != 200 || len(got) != 2048 {
		t.Fatalf("play: %d %d bytes", resp.StatusCode, len(got))
	}
	if _, st := addonGet(t, kidBase+"/stream/movie/tt0063350.json"); len(st["streams"].([]any)) != 0 {
		t.Fatalf("kid got a film it can't see: %v", st)
	}
	if _, st := addonGet(t, kidBase+"/stream/series/tt0000002%3A1%3A1.json"); len(st["streams"].([]any)) != 1 {
		t.Fatalf("kid episode: %v", st)
	}

	// A new link replaces the old one; deleting it stops it.
	_, body = e.do("POST", "/api/v1/addon/"+i64(me), "")
	if code, _ := addonGet(t, ownerURL); code != 404 {
		t.Fatalf("old link still works: %d", code)
	}
	if code, _ := addonGet(t, body["url"].(string)); code != 200 {
		t.Fatalf("new link: %d", code)
	}
	if code, _ := e.do("DELETE", "/api/v1/addon/"+i64(me), ""); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := addonGet(t, body["url"].(string)); code != 404 {
		t.Fatalf("deleted link still works: %d", code)
	}
	if code, _ := addonGet(t, e.srv.URL+"/addon/short/manifest.json"); code != 404 {
		t.Fatalf("bad key: %d", code)
	}
}

func i64(n int64) string { return strconv.FormatInt(n, 10) }
