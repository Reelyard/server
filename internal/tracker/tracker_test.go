package tracker

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
	"time"

	"github.com/inluxc/harbor/internal/engine"
	"github.com/inluxc/harbor/internal/metadata"
	"github.com/inluxc/harbor/internal/profile"
	"github.com/inluxc/harbor/internal/release"
	"github.com/inluxc/harbor/internal/search"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/stremio"
)

// fakeStreams answers from the captured Torrentio fixtures.
type fakeStreams struct{ calls []string }

func (f *fakeStreams) Streams(_ context.Context, typ, id string) (*search.Result, error) {
	f.calls = append(f.calls, id)
	file := map[string]string{"tt0063350": "torrentio-movie.json", "tt0903747:1:1": "torrentio-series.json"}[id]
	res := &search.Result{Type: typ, ID: id, Suppliers: []search.SupplierResult{{ID: 1, Name: "Torrentio"}}}
	if file == "" {
		return res, nil
	}
	b, _ := os.ReadFile(filepath.Join("..", "stremio", "testdata", file))
	var body struct{ Streams []stremio.Stream }
	json.Unmarshal(b, &body)
	for _, s := range body.Streams {
		res.Candidates = append(res.Candidates, search.Candidate{
			InfoHash: s.InfoHash, FileIdx: s.FileIdx, Sources: s.Sources, Usable: s.IsTorrent(),
			Stream: release.ParseStream(s.Name, s.Text(), s.BehaviorHints.Filename, 0),
		})
	}
	return res, nil
}

// fakeDownloads keeps downloads in memory but, like the engine, also
// creates the database row items point at.
type fakeDownloads struct {
	full bool
	mu   sync.Mutex
	st   *store.Store
	d    map[int64]*engine.Status
}

func (f *fakeDownloads) Add(ctx context.Context, r engine.AddRequest) (*engine.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	idx := -1
	if r.FileIdx != nil {
		idx = *r.FileIdx
	}
	row, err := f.st.CreateDownload(ctx, store.NewDownload{InfoHash: r.InfoHash, FileIdx: idx, Name: r.Name, TitleType: r.TitleType, TitleID: r.TitleID, TitleName: r.TitleName})
	if err != nil {
		return nil, err
	}
	st := &engine.Status{Download: store.Download{ID: row.ID, InfoHash: strings.ToLower(r.InfoHash), FileIdx: idx, Name: r.Name,
		TitleType: r.TitleType, TitleID: r.TitleID, TitleName: r.TitleName, State: store.StateDownloading}}
	f.d[st.ID] = st
	c := *st
	return &c, nil
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

func (f *fakeDownloads) Find(h string, idx int) (*engine.Status, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, d := range f.d {
		if d.InfoHash == strings.ToLower(h) && d.FileIdx == idx {
			c := *d
			return &c, true
		}
	}
	return nil, false
}

func (f *fakeDownloads) Remove(ctx context.Context, id int64, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.d, id)
	return f.st.DeleteDownload(ctx, id)
}

func (f *fakeDownloads) RoomFor(need int64) error {
	if f.full {
		return engine.ErrNoSpace
	}
	return nil
}

func (f *fakeDownloads) set(id int64, fn func(*engine.Status)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f.d[id])
}

func setup(t *testing.T) (*Tracker, *store.Store, *fakeDownloads, *fakeStreams) {
	t.Helper()
	ctx := context.Background()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/meta/series/tt0903747.json":
			http.ServeFile(w, r, filepath.Join("..", "stremio", "testdata", "cinemeta-meta-series.json"))
		case "/meta/movie/tt0063350.json":
			http.ServeFile(w, r, filepath.Join("..", "stremio", "testdata", "cinemeta-meta-movie.json"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := profile.EnsureDefaults(ctx, st); err != nil {
		t.Fatal(err)
	}
	meta := metadata.New(stremio.New("test"))
	meta.Base = srv.URL
	fd := &fakeDownloads{st: st, d: map[int64]*engine.Status{}}
	fs := &fakeStreams{}
	tr := New(Options{Store: st, Meta: meta, Search: fs, Downloads: fd, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), PerTick: 3})
	return tr, st, fd, fs
}

func TestSeriesFlow(t *testing.T) {
	ctx := context.Background()
	tr, st, fd, fs := setup(t)
	ti, err := tr.Track(ctx, TrackRequest{IMDbID: "tt0903747", Type: "series", ProfileID: 1, Monitor: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if ti.Name != "Breaking Bad" || ti.Year != 2008 || ti.Wanted < 60 {
		t.Fatalf("tracked: %+v", ti)
	}
	if _, err := tr.Track(ctx, TrackRequest{IMDbID: "tt0903747", Type: "series", ProfileID: 1}); err != ErrTracked {
		t.Fatalf("second Track err = %v", err)
	}
	items, _ := st.ItemsOfTitle(ctx, ti.ID)
	for _, it := range items {
		if it.Season == 0 && it.Monitored {
			t.Errorf("special %s monitored by default", it.ItemID)
		}
	}

	tr.Pass(ctx)
	if len(fs.calls) != 3 || fs.calls[0] != "tt0903747:1:1" {
		t.Fatalf("searched %v, want 3 starting at S01E01", fs.calls)
	}
	e1, _ := st.ItemByItemID(ctx, "tt0903747:1:1")
	if e1.State != store.ItemDownloading || e1.DownloadID == nil || !strings.HasPrefix(e1.Note, "Grabbed ") {
		t.Fatalf("S01E01: %+v", e1)
	}
	d, _ := fd.Get(*e1.DownloadID)
	if d.TitleID != "tt0903747:1:1" || d.TitleName != "Breaking Bad" {
		t.Fatalf("download: %+v", d)
	}
	e2, _ := st.ItemByItemID(ctx, "tt0903747:1:2")
	if e2.State != store.ItemMissing || e2.Attempts != 1 || !strings.Contains(e2.Note, "no streams found") || e2.NextSearchAt == "" {
		t.Fatalf("S01E02 after empty search: %+v", e2)
	}

	// Import rejected (wrong audio): blocked, removed, searched again with
	// the next best release.
	first := d.InfoHash
	fd.set(d.ID, func(s *engine.Status) {
		s.State = store.StateSeeding
		s.ImportState = store.ImportRejected
		s.ImportError = "The audio is French"
	})
	st.SearchSoon(ctx, ti.ID, e1.ID)
	tr.Pass(ctx)
	e1, _ = st.ItemByItemID(ctx, "tt0903747:1:1")
	if e1.State != store.ItemDownloading || e1.DownloadID == nil {
		t.Fatalf("S01E01 after rejection: %+v", e1)
	}
	d2, _ := fd.Get(*e1.DownloadID)
	if d2.InfoHash == first {
		t.Fatal("grabbed the blocked release again")
	}
	if bl, _ := st.BlockedFor(ctx, "tt0903747:1:1"); !bl[first] {
		t.Fatal("rejected release not on the blocklist")
	}
	if got, err := fd.Get(d.ID); err == nil && got.InfoHash == first {
		t.Fatal("rejected download not removed")
	}

	// Imported → item done.
	fd.set(d2.ID, func(s *engine.Status) { s.State = store.StateSeeding; s.ImportState = store.ImportImported })
	tr.Pass(ctx)
	e1, _ = st.ItemByItemID(ctx, "tt0903747:1:1")
	if e1.State != store.ItemImported {
		t.Fatalf("S01E01 after import: %+v", e1)
	}

	// Switching to "none" unmonitors everything.
	if _, err := tr.Update(ctx, ti.ID, 1, "none", nil); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.DueItems(ctx, 100); len(due) != 0 {
		t.Fatalf("%d items due with monitor=none", len(due))
	}
}

func TestMovieFlowAndRemovedDownload(t *testing.T) {
	ctx := context.Background()
	tr, st, fd, _ := setup(t)
	ti, err := tr.Track(ctx, TrackRequest{IMDbID: "tt0063350", Type: "movie", ProfileID: 1})
	if err != nil {
		t.Fatal(err)
	}
	tr.Pass(ctx)
	it, _ := st.ItemByItemID(ctx, "tt0063350")
	if it.State != store.ItemDownloading {
		t.Fatalf("movie: %+v", it)
	}
	d, _ := fd.Get(*it.DownloadID)
	if !strings.Contains(d.Name, "1080p") {
		t.Fatalf("HD profile grabbed %q", d.Name)
	}
	// The user removes the download: the item goes back to searching.
	fd.Remove(ctx, d.ID, true)
	tr.Pass(ctx)
	it, _ = st.ItemByItemID(ctx, "tt0063350")
	if it.State != store.ItemDownloading || it.GrabbedHash == d.InfoHash {
		t.Fatalf("after removal it should grab a different release: %+v", it)
	}
	if bl, _ := st.BlockedFor(ctx, "tt0063350"); !bl[d.InfoHash] {
		t.Fatal("removed release not blocked")
	}
	_ = ti
}

func TestNoSpaceWaits(t *testing.T) {
	ctx := context.Background()
	tr, st, fd, _ := setup(t)
	fd.full = true
	if _, err := tr.Track(ctx, TrackRequest{IMDbID: "tt0063350", Type: "movie", ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	tr.Pass(ctx)
	it, _ := st.ItemByItemID(ctx, "tt0063350")
	if it.State != store.ItemMissing || !strings.Contains(it.Note, "Not enough free disk space") || it.NextSearchAt == "" {
		t.Fatalf("item with a full disk: %+v", it)
	}
	if bl, _ := st.BlockedFor(ctx, "tt0063350"); len(bl) != 0 {
		t.Fatal("a full disk blocklisted a release")
	}
	if r, h, ok := tr.Limits(store.Download{TitleID: "tt0063350"}); !ok || r != 1 || h != 48 {
		t.Fatalf("Limits = %v %v %v", r, h, ok)
	}
}

func TestMonitoredRules(t *testing.T) {
	added := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	ti := &store.Title{Type: "series", Monitor: "future", AddedAt: added}
	old := store.Item{Season: 1, AirDate: "2020-01-01T00:00:00Z"}
	newer := store.Item{Season: 2, AirDate: "2025-01-01T00:00:00Z"}
	if monitored(ti, old) || !monitored(ti, newer) {
		t.Error("future mode wrong")
	}
	ti.Monitor, ti.Seasons = "all", []int{2}
	if monitored(ti, old) || !monitored(ti, newer) {
		t.Error("season filter wrong")
	}
	ti.Seasons = nil
	if monitored(ti, store.Item{Season: 0}) {
		t.Error("specials monitored without asking")
	}
	if NextSearch(1) != 15*time.Minute || NextSearch(99) != 24*time.Hour {
		t.Error("backoff wrong")
	}
}

func TestCheckAudio(t *testing.T) {
	ctx := context.Background()
	tr, st, _, _ := setup(t)
	p, _ := st.GetProfile(ctx, 1)
	p.AudioLangs, p.AudioRequired = []string{"pt"}, true
	st.SaveProfile(ctx, p)
	if _, err := tr.Track(ctx, TrackRequest{IMDbID: "tt0063350", Type: "movie", ProfileID: 1}); err != nil {
		t.Fatal(err)
	}
	if err := tr.CheckAudio(ctx, "tt0063350", []string{"en"}); err == nil || !strings.Contains(err.Error(), "English") {
		t.Fatalf("English audio accepted: %v", err)
	}
	if err := tr.CheckAudio(ctx, "tt0063350", []string{"pt-BR"}); err != nil {
		t.Fatalf("pt-BR rejected: %v", err)
	}
	if err := tr.CheckAudio(ctx, "tt9999999", []string{"en"}); err != nil {
		t.Fatalf("untracked item checked: %v", err)
	}
	if l, ok := tr.SubtitleLangs(ctx, "tt0063350"); !ok || len(l) != 1 || l[0] != "en" {
		t.Fatalf("SubtitleLangs = %v %v", l, ok)
	}
}
