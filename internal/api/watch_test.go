package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/reelyard/server/internal/metadata"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
)

func TestHealthSaysWhichServer(t *testing.T) {
	e := newClientsServer(t)
	_, a := e.app.do("GET", "/api/v1/health", "")
	_, b := e.app.do("GET", "/api/v1/health", "")
	id, _ := a["id"].(string)
	if a["apiVersion"] != float64(APIVersion) || len(id) != 16 || b["id"] != id || !strings.HasPrefix(a["name"].(string), "Reelyard") {
		t.Fatalf("health: %v", a)
	}
}

func TestSubtitlesAsWebVTT(t *testing.T) {
	e := newClientsServer(t)
	m := e.addFile("movie", "tt0063350", "Night", "Movies/Night (1968)/Night (1968) - 720p.mkv", 100)
	srt := "1\r\n00:00:01,000 --> 00:00:02,000\r\nThey're coming to get you\r\n"
	if err := os.WriteFile(filepath.Join(e.lib, "Movies/Night (1968)/Night (1968) - 720p.pt-BR.srt"), []byte(srt), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := e.st.UpsertSubtitleFile(context.Background(), store.SubtitleFile{
		MediaFileID: m.ID, Lang: "pt-BR", Path: "Movies/Night (1968)/Night (1968) - 720p.pt-BR.srt", Source: "test",
	}); err != nil {
		t.Fatal(err)
	}
	tok := e.pair("TV")
	_, body := e.app.do("POST", "/api/v1/library/"+itoa(float64(m.ID))+"/play", "", "Authorization", "Bearer "+tok)
	subs, _ := body["subtitles"].([]any)
	if len(subs) != 1 {
		t.Fatalf("subtitles: %v", body)
	}
	sub := subs[0].(map[string]any)
	url := sub["url"].(string)
	if sub["lang"] != "pt-BR" || sub["name"] != "Portuguese (Brazil)" || !strings.Contains(url, "sig=") {
		t.Fatalf("subtitle: %v", sub)
	}
	resp, got := get(t, e.srv.URL+url)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "text/vtt; charset=utf-8" ||
		got != "WEBVTT\n\n00:00:01.000 --> 00:00:02.000\nThey're coming to get you\n\n" {
		t.Fatalf("vtt: %d %q", resp.StatusCode, got)
	}
	// The signature is for this subtitle only: not the video with the same id.
	sid := itoa(sub["id"].(float64))
	q := url[strings.Index(url, "?"):]
	if resp, _ := get(t, e.srv.URL+"/api/v1/play/"+sid+q); resp.StatusCode != 403 {
		t.Fatalf("subtitle link opened a video: %d", resp.StatusCode)
	}
	if resp, _ := get(t, e.srv.URL+"/api/v1/subtitles/"+sid); resp.StatusCode != 401 {
		t.Fatalf("no link, no credential: %d", resp.StatusCode)
	}
	if resp, _ := get(t, e.srv.URL+"/api/v1/subtitles/"+sid, "Authorization", "Bearer "+tok); resp.StatusCode != 200 {
		t.Fatalf("device token: %d", resp.StatusCode)
	}
}

// fakeCinemeta serves metadata whose poster is an image on the same server.
func fakeCinemeta(t *testing.T, hits *atomic.Int32) *metadata.Service {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/meta/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"meta": map[string]any{
				"id": "tt0063350", "name": "Night", "poster": srv.URL + "/img/poster.png", "background": srv.URL + "/img/gone.jpg",
			}})
		case r.URL.Path == "/img/poster.png":
			hits.Add(1)
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n fake poster"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	m := metadata.New(stremio.New("test"))
	m.Base = srv.URL
	return m
}

func TestArtwork(t *testing.T) {
	var hits atomic.Int32
	e := newClientsServer(t, func(s *Server) { s.Meta = fakeCinemeta(t, &hits) })
	e.addFile("movie", "tt0063350", "Night", "Movies/Night (1968)/Night (1968).mkv", 10)
	_, body := e.do("GET", "/api/v1/library/titles", "")
	art := body["titles"].([]any)[0].(map[string]any)["art"].(map[string]any)
	poster := art["poster"].(string)
	if !strings.HasPrefix(poster, "/api/v1/art/tt0063350/poster?sig=") {
		t.Fatalf("art: %v", art)
	}
	for range 2 {
		resp, got := get(t, e.srv.URL+poster)
		if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || !strings.HasSuffix(got, "fake poster") {
			t.Fatalf("poster: %d %v %q", resp.StatusCode, resp.Header, got)
		}
	}
	if hits.Load() != 1 {
		t.Fatalf("poster fetched %d times, want once (then cached)", hits.Load())
	}
	// No such image upstream, or none in the metadata: 404.
	for _, k := range []string{"background", "logo"} {
		if resp, _ := get(t, e.srv.URL+art[k].(string)); resp.StatusCode != 404 {
			t.Fatalf("%s: %d", k, resp.StatusCode)
		}
	}
	// A signature is for one title and kind.
	sig := poster[strings.Index(poster, "?"):]
	for _, p := range []string{"/api/v1/art/tt0000001/poster", "/api/v1/art/tt0063350/background"} {
		if resp, _ := get(t, e.srv.URL+p+sig); resp.StatusCode != 403 {
			t.Fatalf("%s with another signature: %d", p, resp.StatusCode)
		}
	}
	if resp, _ := get(t, e.srv.URL+"/api/v1/art/tt0063350/poster"); resp.StatusCode != 403 {
		t.Fatalf("unsigned: %d", resp.StatusCode)
	}
}

func TestProgressAndHome(t *testing.T) {
	e := newClientsServer(t)
	movie := e.addFile("movie", "tt0063350", "Night", "Movies/Night (1968)/Night (1968).mkv", 10)
	ep1 := e.addFile("series", "tt0903747:1:1", "Breaking Bad", "TV/BB/S01/BB - S01E01.mkv", 10)
	e.addFile("series", "tt0903747:1:2", "Breaking Bad", "TV/BB/S01/BB - S01E02.mkv", 10)
	e.addFile("series", "tt0903747:2:1", "Breaking Bad", "TV/BB/S02/BB - S02E01.mkv", 10)
	tok := e.pair("TV")
	bearer := []string{"Authorization", "Bearer " + tok}
	put := func(item, body string) (int, map[string]any) {
		return e.app.do("PUT", "/api/v1/progress/"+item, body, bearer...)
	}

	if code, _ := put("nope", `{"positionSec":1}`); code != 400 {
		t.Fatalf("bad item: %d", code)
	}
	if code, _ := put("tt0903747:1:2", `{"positionSec":1,"fileId":`+itoa(float64(ep1.ID))+`}`); code != 400 {
		t.Fatalf("file of another episode: %d", code)
	}
	// Near the end counts as watched, and starts over next time.
	code, body := put("tt0903747:1:1", `{"positionSec":1300,"durationSec":1400,"fileId":`+itoa(float64(ep1.ID))+`}`)
	p, _ := body["progress"].(map[string]any)
	if code != 200 || p["watched"] != true || p["positionSec"] != float64(0) {
		t.Fatalf("watched: %d %v", code, body)
	}
	if code, body := put("tt0063350", `{"positionSec":600,"durationSec":6000}`); code != 200 || body["progress"].(map[string]any)["watched"] != false {
		t.Fatalf("film: %d %v", code, body)
	}

	_, home := e.app.do("GET", "/api/v1/home", "", bearer...)
	cont, _ := home["continueWatching"].([]any)
	next, _ := home["nextUp"].([]any)
	recent, _ := home["recentlyAdded"].([]any)
	if len(cont) != 1 || cont[0].(map[string]any)["itemId"] != "tt0063350" ||
		cont[0].(map[string]any)["progress"].(map[string]any)["positionSec"] != float64(600) {
		t.Fatalf("continue watching: %v", cont)
	}
	if len(next) != 1 || next[0].(map[string]any)["itemId"] != "tt0903747:1:2" || next[0].(map[string]any)["file"] == nil {
		t.Fatalf("next up: %v", next)
	}
	if len(recent) != 2 || recent[0].(map[string]any)["art"] == nil {
		t.Fatalf("recently added: %v", recent)
	}

	// Playing the film picks up where it stopped.
	_, play := e.app.do("POST", "/api/v1/library/"+itoa(float64(movie.ID))+"/play", "", bearer...)
	if r, _ := play["resume"].(map[string]any); r == nil || r["positionSec"] != float64(600) {
		t.Fatalf("resume: %v", play["resume"])
	}
	// Episodes carry their progress.
	_, title := e.app.do("GET", "/api/v1/library/titles/tt0903747", "", bearer...)
	eps := title["seasons"].([]any)[0].(map[string]any)["episodes"].([]any)
	if eps[0].(map[string]any)["progress"].(map[string]any)["watched"] != true || eps[1].(map[string]any)["progress"] != nil {
		t.Fatalf("episodes: %v", eps)
	}
	// After the last episode of season 1 comes season 2.
	put("tt0903747:1:2", `{"watched":true}`)
	_, home = e.app.do("GET", "/api/v1/home", "", bearer...)
	if next := home["nextUp"].([]any); len(next) != 1 || next[0].(map[string]any)["itemId"] != "tt0903747:2:1" {
		t.Fatalf("next up after S1E2: %v", next)
	}
	// Progress belongs to the user, not the device: the admin who paired the
	// TV sees it in the browser too.
	_, home = e.do("GET", "/api/v1/home", "")
	if len(home["continueWatching"].([]any)) != 1 {
		t.Fatalf("same user in the browser: %v", home)
	}
	// Forgetting the film takes it off the row.
	if code, _ := e.app.do("DELETE", "/api/v1/progress/tt0063350", "", bearer...); code != 204 {
		t.Fatalf("delete: %d", code)
	}
	_, list := e.app.do("GET", "/api/v1/progress?title=tt0903747", "", bearer...)
	if ps := list["progress"].([]any); len(ps) != 2 {
		t.Fatalf("progress list: %v", ps)
	}
	_, home = e.app.do("GET", "/api/v1/home", "", bearer...)
	if len(home["continueWatching"].([]any)) != 0 {
		t.Fatalf("still continuing: %v", home["continueWatching"])
	}
}

func TestPlayability(t *testing.T) {
	yes, no := true, false
	web := &Supports{Containers: []string{"mp4", "webm"}, VideoCodecs: []string{"h264", "vp9"}, AudioCodecs: []string{"aac", "opus"}}
	for _, c := range []struct {
		name string
		mf   store.MediaFile
		sup  *Supports
		want *bool
		n    int
	}{
		{"app didn't say", store.MediaFile{Path: "a.mkv"}, nil, nil, 0},
		{"plays", store.MediaFile{Path: "a.mp4", VideoCodec: "h264", AudioCodecs: []string{"aac", "ac3"}}, web, &yes, 0},
		{"codec names in any case", store.MediaFile{Path: "a.MP4", VideoCodec: "H264", AudioCodecs: []string{"AAC"}}, web, &yes, 0},
		{"everything wrong", store.MediaFile{Path: "a.mkv", VideoCodec: "hevc", AudioCodecs: []string{"eac3", "aac"}}, web, &no, 3},
		{"audio not checked yet", store.MediaFile{Path: "a.mp4", VideoCodec: "h264"}, web, nil, 0},
		{"known bad beats unknown", store.MediaFile{Path: "a.avi"}, web, &no, 1},
		{"only containers given", store.MediaFile{Path: "a.webm"}, &Supports{Containers: []string{"webm"}}, &yes, 0},
	} {
		got, problems := playability(&c.mf, c.sup)
		if (got == nil) != (c.want == nil) || got != nil && *got != *c.want || len(problems) != c.n {
			t.Errorf("%s: got %v %v", c.name, got, problems)
		}
	}

	e := newClientsServer(t)
	m := e.addFile("movie", "tt0063350", "Night", "Movies/Night (1968)/Night (1968).mkv", 10)
	if err := e.st.SetMediaProbe(context.Background(), m.ID, "hevc", []string{"eac3"}, 0); err != nil {
		t.Fatal(err)
	}
	_, body := e.do("POST", "/api/v1/library/"+itoa(float64(m.ID))+"/play",
		`{"supports":{"containers":["mp4"],"videoCodecs":["h264"],"audioCodecs":["aac"]}}`)
	if body["playable"] != false || len(body["problems"].([]any)) != 3 || body["container"] != "mkv" {
		t.Fatalf("play: %v", body)
	}
	if code, _ := e.do("POST", "/api/v1/library/"+itoa(float64(m.ID))+"/play", `{"supports":{"bogus":1}}`); code != 400 {
		t.Fatalf("unknown field: %d", code)
	}
}
