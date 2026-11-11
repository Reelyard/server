package importer

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/inluxc/harbor/internal/engine"
	"github.com/inluxc/harbor/internal/metadata"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/stremio"
	"github.com/inluxc/harbor/internal/suppliers"
)

type fakeDownloads struct {
	mu     sync.Mutex
	d      map[int64]*engine.Status
	states map[int64]string
	msgs   map[int64]string
}

func (f *fakeDownloads) Get(id int64) (*engine.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	d, ok := f.d[id]
	if !ok {
		return nil, engine.ErrNotFound
	}
	c := *d
	return &c, nil
}

func (f *fakeDownloads) SetImport(_ context.Context, id int64, state, msg string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[id], f.msgs[id] = state, msg
	return nil
}

// addons serves Cinemeta meta (fixtures) and a subtitle addon whose
// subtitles are VTT files on the same server.
func addons(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.EscapedPath()
		switch {
		case p == "/cinemeta/meta/series/tt0903747.json":
			http.ServeFile(w, r, filepath.Join("..", "stremio", "testdata", "cinemeta-meta-series.json"))
		case p == "/cinemeta/meta/movie/tt0063350.json":
			http.ServeFile(w, r, filepath.Join("..", "stremio", "testdata", "cinemeta-meta-movie.json"))
		case p == "/subs/manifest.json":
			io.WriteString(w, `{"id":"test.subs","version":"1.0.0","name":"Test Subs","types":["movie","series"],"idPrefixes":["tt"],"resources":["subtitles"]}`)
		case strings.HasPrefix(p, "/subs/subtitles/"):
			if !strings.Contains(p, "videoHash=") {
				t.Errorf("subtitle request without videoHash: %s", p)
			}
			subs := []map[string]string{
				{"id": "1", "lang": "eng", "url": srv.URL + "/file/broken.srt"},
				{"id": "2", "lang": "eng", "url": srv.URL + "/file/en.vtt"},
				{"id": "3", "lang": "pob", "url": srv.URL + "/file/pt.srt"},
			}
			json.NewEncoder(w).Encode(map[string]any{"subtitles": subs})
		case p == "/file/en.vtt":
			io.WriteString(w, "WEBVTT\n\n00:01.000 --> 00:02.000\nHello\n")
		case p == "/file/pt.srt":
			io.WriteString(w, "1\n00:00:01,000 --> 00:00:02,000\nOlá\n")
		case p == "/file/broken.srt":
			io.WriteString(w, "<html>gone</html>")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestImportMovieAndEpisode(t *testing.T) {
	ctx := context.Background()
	srv := addons(t)
	base := t.TempDir()
	dlDir, libDir := filepath.Join(base, "downloads"), filepath.Join(base, "library")
	st, err := store.Open(ctx, filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	client := stremio.New("test")
	sup := &suppliers.Service{Store: st, Client: client}
	if _, err := sup.Add(ctx, srv.URL+"/subs/manifest.json"); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveSettings(ctx, st, Settings{SubtitleLangs: []string{"en", "pt"}}); err != nil {
		t.Fatal(err)
	}
	meta := metadata.New(client)
	meta.Base = srv.URL + "/cinemeta"

	// Two finished downloads on disk: a movie file and an episode inside a
	// season-pack folder. Both need real download rows for the media_files
	// foreign key.
	write := func(rel string, n int) {
		p := filepath.Join(dlDir, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, make([]byte, n), 0o644)
	}
	write("aaa/Night.of.the.Living.Dead.1968.720p.BluRay.mp4", 200<<10)
	write("bbb/Breaking.Bad.S01.1080p/Breaking.Bad.S01E02.1080p.mkv", 300<<10)
	mk := func(nd store.NewDownload) int64 {
		d, err := st.CreateDownload(ctx, nd)
		if err != nil {
			t.Fatal(err)
		}
		return d.ID
	}
	movieID := mk(store.NewDownload{InfoHash: strings.Repeat("a", 40), FileIdx: 0, Name: "Night.of.the.Living.Dead.1968.720p.BluRay"})
	epID := mk(store.NewDownload{InfoHash: strings.Repeat("b", 40), FileIdx: 1, Name: "Breaking.Bad.S01.1080p"})
	looseID := mk(store.NewDownload{InfoHash: strings.Repeat("c", 40), FileIdx: 0, Name: "something"})

	fd := &fakeDownloads{d: map[int64]*engine.Status{}, states: map[int64]string{}, msgs: map[int64]string{}}
	fd.d[movieID] = &engine.Status{Download: store.Download{ID: movieID, State: store.StateSeeding, Name: "Night.of.the.Living.Dead.1968.720p.BluRay",
		FilePath: "aaa/Night.of.the.Living.Dead.1968.720p.BluRay.mp4", Size: 200 << 10, DoneBytes: 200 << 10,
		TitleType: "movie", TitleID: "tt0063350", TitleName: "Night of the Living Dead"}}
	fd.d[epID] = &engine.Status{Download: store.Download{ID: epID, State: store.StateSeeding, Name: "Breaking.Bad.S01.1080p",
		FilePath: "bbb/Breaking.Bad.S01.1080p/Breaking.Bad.S01E02.1080p.mkv", Size: 300 << 10, DoneBytes: 300 << 10,
		TitleType: "series", TitleID: "tt0903747:1:2", TitleName: "Breaking Bad"}}
	fd.d[looseID] = &engine.Status{Download: store.Download{ID: looseID, State: store.StateSeeding, Name: "something", FilePath: "ccc/x.mkv"}}

	im := New(Options{Store: st, Downloads: fd, Meta: meta, Suppliers: sup, Client: client,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LibraryDir: libDir, DownloadDir: dlDir})

	// Movie.
	if err := im.Import(ctx, movieID); err != nil {
		t.Fatal(err)
	}
	if fd.states[movieID] != store.ImportImported {
		t.Fatalf("movie import state %q %q", fd.states[movieID], fd.msgs[movieID])
	}
	movieRel := "Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.mp4"
	assertSameFile(t, filepath.Join(dlDir, "aaa/Night.of.the.Living.Dead.1968.720p.BluRay.mp4"), filepath.Join(libDir, movieRel))
	en, err := os.ReadFile(filepath.Join(libDir, SubtitlePath(movieRel, "en")))
	if err != nil || !strings.Contains(string(en), "00:00:01,000 --> 00:00:02,000\nHello") {
		t.Fatalf("English subtitle: %q %v", en, err)
	}
	// "pt" wanted, only pt-BR offered: saved as pt-BR.
	if pt, err := os.ReadFile(filepath.Join(libDir, SubtitlePath(movieRel, "pt-BR"))); err != nil || !strings.Contains(string(pt), "Olá") {
		t.Fatalf("Portuguese subtitle: %q %v", pt, err)
	}
	mf, err := st.MediaFileByPath(ctx, movieRel)
	if err != nil || len(mf.Subtitles) != 2 || mf.Method != MethodHardlink || mf.OSHash == "" {
		t.Fatalf("media row: %+v %v", mf, err)
	}

	// Asking again finds both present and downloads nothing.
	res, err := im.Subtitles(ctx, mf.ID)
	if err != nil || len(res) != 2 || res[0].Status != "present" || res[1].Status != "present" {
		t.Fatalf("second Subtitles = %+v %v", res, err)
	}

	// Episode: name and episode title come from Cinemeta.
	if err := im.Import(ctx, epID); err != nil {
		t.Fatal(err)
	}
	epRel := "TV/Breaking Bad (2008)/Season 01/Breaking Bad (2008) - S01E02 - Cat's in the Bag - 1080p.mkv"
	assertSameFile(t, filepath.Join(dlDir, "bbb/Breaking.Bad.S01.1080p/Breaking.Bad.S01E02.1080p.mkv"), filepath.Join(libDir, epRel))

	// Importing the same download again is harmless.
	if err := im.Import(ctx, epID); err != nil || fd.states[epID] != store.ImportImported {
		t.Fatalf("re-import: %v %q", err, fd.states[epID])
	}

	// Not linked to a title: skipped, left in place.
	if err := im.Import(ctx, looseID); err != nil || fd.states[looseID] != store.ImportSkipped {
		t.Fatalf("loose: %v %q", err, fd.states[looseID])
	}

	all, _ := st.ListMediaFiles(ctx, "tt0903747")
	if len(all) != 1 || all[0].TitleID != "tt0903747:1:2" {
		t.Fatalf("ListMediaFiles(show) = %+v", all)
	}
	acts, _ := st.ListActivity(ctx, 50)
	var text []string
	for _, a := range acts {
		text = append(text, a.Message)
	}
	joined := strings.Join(text, "\n")
	for _, want := range []string{"Imported Night of the Living Dead", "Saved English, Portuguese (Brazil) subtitles", "Imported Breaking Bad"} {
		if !strings.Contains(joined, want) {
			t.Errorf("activity missing %q:\n%s", want, joined)
		}
	}
}

func TestImportConflict(t *testing.T) {
	ctx := context.Background()
	srv := addons(t)
	base := t.TempDir()
	dlDir, libDir := filepath.Join(base, "dl"), filepath.Join(base, "lib")
	st, _ := store.Open(ctx, filepath.Join(base, "h.db"))
	defer st.Close()
	client := stremio.New("test")
	meta := metadata.New(client)
	meta.Base = srv.URL + "/cinemeta"
	SaveSettings(ctx, st, Settings{SubtitleLangs: []string{}})

	os.MkdirAll(filepath.Join(dlDir, "x"), 0o755)
	os.WriteFile(filepath.Join(dlDir, "x", "m.mkv"), []byte(strings.Repeat("A", 100000)), 0o644)
	// Something else already in the library under the target name.
	target := filepath.Join(libDir, "Movies", "Night of the Living Dead (1968)", "Night of the Living Dead (1968).mkv")
	os.MkdirAll(filepath.Dir(target), 0o755)
	os.WriteFile(target, []byte("mine"), 0o644)

	d, _ := st.CreateDownload(ctx, store.NewDownload{InfoHash: strings.Repeat("d", 40), FileIdx: 0, Name: "m"})
	fd := &fakeDownloads{d: map[int64]*engine.Status{d.ID: {Download: store.Download{ID: d.ID, State: store.StateSeeding, Name: "m",
		FilePath: "x/m.mkv", Size: 100000, DoneBytes: 100000, TitleType: "movie", TitleID: "tt0063350", TitleName: "Night of the Living Dead"}}},
		states: map[int64]string{}, msgs: map[int64]string{}}
	im := New(Options{Store: st, Downloads: fd, Meta: meta, Suppliers: &suppliers.Service{Store: st, Client: client}, Client: client,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), LibraryDir: libDir, DownloadDir: dlDir})
	if err := im.Import(ctx, d.ID); err == nil {
		t.Fatal("conflict not reported")
	}
	if fd.states[d.ID] != store.ImportFailed || !strings.Contains(fd.msgs[d.ID], "already has this name") {
		t.Fatalf("state %q msg %q", fd.states[d.ID], fd.msgs[d.ID])
	}
	if b, _ := os.ReadFile(target); string(b) != "mine" {
		t.Fatal("existing library file was overwritten")
	}
}

func assertSameFile(t *testing.T, a, b string) {
	t.Helper()
	ai, err1 := os.Stat(a)
	bi, err2 := os.Stat(b)
	if err1 != nil || err2 != nil || !os.SameFile(ai, bi) {
		t.Fatalf("%s and %s are not the same file (%v, %v)", a, b, err1, err2)
	}
}
