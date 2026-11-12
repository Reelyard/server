package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

// A catalog-only addon (like Nuvio's) is accepted, tested, and browsable.
func TestBrowseCatalogAddon(t *testing.T) {
	var calls atomic.Int32
	addon := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/manifest.json":
			w.Write([]byte(`{"id":"com.example.catalogs","version":"1.0.0","name":"Catalogs","types":["movie","series"],
				"resources":["catalog",{"name":"meta","types":["movie","series"],"idPrefixes":["tt"]}],
				"catalogs":[
					{"type":"movie","id":"trending","name":"Trending Movies","extra":[{"name":"skip"}]},
					{"type":"movie","id":"by-year","name":"Movies by Year","extra":[{"name":"genre","isRequired":true,"options":["2026","2025"]}]},
					{"type":"movie","id":"search-only","extra":[{"name":"search","isRequired":true}]},
					{"type":"tv","id":"channels"}]}`))
		case "/catalog/movie/trending.json":
			calls.Add(1)
			w.Write([]byte(`{"metas":[{"id":"tt0063350","type":"movie","name":"Night of the Living Dead","releaseInfo":"1968","imdbRating":"7.8"}]}`))
		case "/catalog/movie/trending/skip=20.json":
			w.Write([]byte(`{"metas":[]}`))
		case "/catalog/movie/by-year/genre=2026.json":
			w.Write([]byte(`{"metas":[{"id":"tt1","type":"movie","name":"New"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer addon.Close()
	c := newMediaServer(t, addon.URL)

	code, body := c.do("POST", "/api/v1/suppliers", `{"url":"`+addon.URL+`/manifest.json"}`)
	if code != 201 {
		t.Fatalf("add catalog addon: %d %v", code, body)
	}
	id := itoa(body["supplier"].(map[string]any)["id"].(float64))
	if code, body := c.do("POST", "/api/v1/suppliers/"+id+"/test", ""); code != 200 || body["ok"] != true || !strings.Contains(body["message"].(string), "1 titles") {
		t.Fatalf("test: %d %v", code, body)
	}

	_, body = c.do("GET", "/api/v1/browse", "")
	cats := body["catalogs"].([]any)
	if len(cats) != 2 {
		t.Fatalf("want trending and by-year only: %v", cats)
	}
	if byYear := cats[1].(map[string]any); byYear["genreRequired"] != true || len(byYear["genres"].([]any)) != 2 {
		t.Fatalf("by-year: %v", byYear)
	}

	before := calls.Load() // the supplier test read it once
	for range 2 {
		code, body = c.do("GET", "/api/v1/browse/"+id+"/movie/trending", "")
		if m := body["metas"].([]any); code != 200 || len(m) != 1 || m[0].(map[string]any)["imdbRating"] != "7.8" {
			t.Fatalf("trending: %d %v", code, body)
		}
	}
	if calls.Load()-before != 1 {
		t.Fatalf("second page load should come from the cache; addon called %d times", calls.Load()-before)
	}
	if _, body := c.do("GET", "/api/v1/browse/"+id+"/movie/trending?skip=20", ""); len(body["metas"].([]any)) != 0 {
		t.Fatalf("skip: %v", body)
	}
	if _, body := c.do("GET", "/api/v1/browse/"+id+"/movie/by-year", ""); len(body["metas"].([]any)) != 1 {
		t.Fatalf("required genre defaults to the first: %v", body)
	}
	if code, _ := c.do("GET", "/api/v1/browse/"+id+"/movie/search-only", ""); code != 404 {
		t.Fatalf("search-only catalog: %d", code)
	}

	// An addon with nothing Reelyard can use is still refused.
	bare := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"x","version":"1.0.0","name":"Meta only","types":["movie"],"resources":["meta"]}`))
	}))
	defer bare.Close()
	if code, body := c.do("POST", "/api/v1/suppliers", `{"url":"`+bare.URL+`/manifest.json"}`); code == 201 || !strings.Contains(body["error"].(string), "no streams, subtitles or catalogs") {
		t.Fatalf("meta-only addon: %d %v", code, body)
	}
}
