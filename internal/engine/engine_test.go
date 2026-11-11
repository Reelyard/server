package engine

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"

	"github.com/inluxc/harbor/internal/store"
)

// seeder makes a two-file torrent and seeds it from a separate client on
// loopback. It returns the info hash and the seeder's address.
func seeder(t *testing.T, files map[string][]byte) (string, string) {
	t.Helper()
	dir := t.TempDir()
	root := filepath.Join(dir, "Test.Release.2024")
	for name, data := range files {
		p := filepath.Join(root, name)
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	info := metainfo.Info{PieceLength: 32 << 10}
	if err := info.BuildFromFilePath(root); err != nil {
		t.Fatal(err)
	}
	ib, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := metainfo.MetaInfo{InfoBytes: ib}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.DefaultStorage = storage.NewFileOpts(storage.NewFileClientOpts{ClientBaseDir: dir, PieceCompletion: storage.NewMapPieceCompletion()})
	cfg.Seed = true
	cfg.NoDHT = true
	cfg.DisableWebtorrent = true
	cfg.ListenPort = 0
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.DisableIPv6 = true
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cl.Close() })
	tor, err := cl.AddTorrent(&mi)
	if err != nil {
		t.Fatal(err)
	}
	<-tor.GotInfo()
	tor.VerifyData()
	if tor.BytesMissing() != 0 {
		t.Fatalf("seeder is missing %d bytes", tor.BytesMissing())
	}
	return mi.HashInfoBytes().HexString(), fmt.Sprintf("127.0.0.1:%d", cl.LocalPort())
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.Read(b)
	return b
}

func newEngine(t *testing.T, st *store.Store, dataDir, dlDir string) *Engine {
	t.Helper()
	e, err := New(context.Background(), Options{
		Store: st, Log: testLog(),
		DataDir: dataDir, DownloadDir: dlDir, ListenPort: 0,
		NoDHT: true, NoDefaultTrackers: true, Tick: 50 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func waitFor(t *testing.T, e *Engine, id int64, what string, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st, err := e.Get(id)
		if err != nil {
			t.Fatal(err)
		}
		if ok(*st) {
			return *st
		}
		time.Sleep(50 * time.Millisecond)
	}
	st, _ := e.Get(id)
	t.Fatalf("timed out waiting for %s: %+v", what, st)
	return Status{}
}

func TestDownloadOneFileAndResume(t *testing.T) {
	ctx := context.Background()
	video := randBytes(600 << 10)
	files := map[string][]byte{
		"Test.Release.2024.1080p.mkv": video,
		"Sample/sample.mkv":           randBytes(200 << 10),
	}
	ih, peer := seeder(t, files)

	base := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	dataDir, dlDir := filepath.Join(base, "data"), filepath.Join(base, "downloads")
	e := newEngine(t, st, dataDir, dlDir)

	// The main video is file 0 or 1 depending on sorting; find it by name
	// after metadata arrives, so first add the whole torrent's file list.
	idx := -1
	{
		probe := 0
		s, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &probe, Name: "probe", Peers: []string{peer}})
		if err != nil {
			t.Fatal(err)
		}
		got := waitFor(t, e, s.ID, "metadata", func(s Status) bool { return s.FilePath != "" })
		if filepath.Base(got.FilePath) == "Test.Release.2024.1080p.mkv" {
			idx = 0
		} else {
			idx = 1
		}
		if err := e.Remove(ctx, s.ID, true); err != nil {
			t.Fatal(err)
		}
	}

	s, err := e.Add(ctx, AddRequest{
		InfoHash: ih, FileIdx: &idx, Name: "Test Release", Peers: []string{peer},
		TitleType: "movie", TitleID: "tt0000001", TitleName: "Test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &idx}); err != ErrDuplicate {
		t.Fatalf("duplicate add err = %v", err)
	}

	done := waitFor(t, e, s.ID, "seeding", func(s Status) bool { return s.State == store.StateSeeding })
	if done.Size != int64(len(video)) || done.Progress != 1 || done.CompletedAt == nil {
		t.Fatalf("finished status: %+v", done)
	}
	got, err := os.ReadFile(filepath.Join(dlDir, done.FilePath))
	if err != nil || !bytes.Equal(got, video) {
		t.Fatalf("downloaded file differs (err %v)", err)
	}
	// Written in place: no leftover .part file, and still writable (so it
	// can be deleted on Windows and hardlinked/moved by the importer).
	if _, err := os.Stat(filepath.Join(dlDir, done.FilePath) + ".part"); err == nil {
		t.Fatal("a .part file was left behind")
	}
	if fi, err := os.Stat(filepath.Join(dlDir, done.FilePath)); err != nil || fi.Mode().Perm()&0o200 == 0 {
		t.Fatalf("finished file mode %v (err %v): want owner-writable", fi.Mode(), err)
	}
	// Pause/resume while seeding keeps it complete.
	if p, err := e.Pause(ctx, s.ID); err != nil || p.State != store.StatePaused {
		t.Fatalf("pause: %+v %v", p, err)
	}
	if r, err := e.Resume(ctx, s.ID); err != nil || r.State != store.StateSeeding {
		t.Fatalf("resume: %+v %v", r, err)
	}

	// Restart: state comes back from the database and saved metadata,
	// without needing peers.
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e2 := newEngine(t, st, dataDir, dlDir)
	defer e2.Close()
	again := waitFor(t, e2, s.ID, "seeding after restart", func(s Status) bool { return s.State == store.StateSeeding && s.Progress == 1 })
	if again.TitleID != "tt0000001" || again.FilePath != done.FilePath {
		t.Fatalf("restored: %+v", again)
	}

	if err := e2.Remove(ctx, s.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dlDir, ih)); !os.IsNotExist(err) {
		t.Fatalf("data not deleted: %v", err)
	}
	if list := e2.List(); len(list) != 0 {
		t.Fatalf("list after remove: %+v", list)
	}
}

func TestQueueLimit(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	e := newEngine(t, st, filepath.Join(base, "data"), filepath.Join(base, "dl"))
	defer e.Close()
	if err := e.SetSettings(ctx, Settings{MaxActive: 1}); err != nil {
		t.Fatal(err)
	}
	// Two torrents nobody seeds: the first waits for metadata, the second
	// stays queued.
	a, _ := e.Add(ctx, AddRequest{InfoHash: "1111111111111111111111111111111111111111"})
	b, _ := e.Add(ctx, AddRequest{InfoHash: "2222222222222222222222222222222222222222"})
	waitFor(t, e, a.ID, "first active", func(s Status) bool { return s.State == store.StateMetadata })
	time.Sleep(200 * time.Millisecond)
	if s, _ := e.Get(b.ID); s.State != store.StateQueued {
		t.Fatalf("second should wait in the queue, is %s", s.State)
	}
	// Pausing the first frees the slot.
	if _, err := e.Pause(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, e, b.ID, "second starts", func(s Status) bool { return s.State == store.StateMetadata })

	if _, err := e.Add(ctx, AddRequest{InfoHash: "nothex"}); err != ErrBadInfoHash {
		t.Fatalf("bad hash err = %v", err)
	}
	if err := e.SetSettings(ctx, Settings{MaxActive: -1}); err == nil {
		t.Fatal("negative max accepted")
	}
}

func TestTrackersFrom(t *testing.T) {
	got := trackersFrom([]string{"tracker:udp://a:1/announce", "dht:abc", "tracker:udp://a:1/announce", "http://b/announce"})
	if len(got) != 2 || got[0] != "udp://a:1/announce" || got[1] != "http://b/announce" {
		t.Fatalf("trackersFrom = %v", got)
	}
}

func testLog() *slog.Logger {
	if os.Getenv("ENGINE_DEBUG") != "" {
		return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
