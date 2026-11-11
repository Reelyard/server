package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/reelyard/server/internal/store"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func newClockedEngine(t *testing.T, free uint64) (*Engine, *store.Store, *clock, string) {
	t.Helper()
	base := t.TempDir()
	st, err := store.Open(context.Background(), filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := &clock{t: time.Now()}
	dl := filepath.Join(base, "downloads")
	e, err := New(context.Background(), Options{
		Store: st, Log: testLog(), DataDir: filepath.Join(base, "data"), DownloadDir: dl,
		NoDHT: true, NoDefaultTrackers: true, Tick: 20 * time.Millisecond, Now: c.Now,
		FreeSpace: func(string) (uint64, error) { return free, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.Close() })
	return e, st, c, dl
}

func TestSharingLimitDeletesCopyOnlyAfterImport(t *testing.T) {
	ctx := context.Background()
	ih, peer := seeder(t, map[string][]byte{"Movie.2020.1080p.mkv": randBytes(300 << 10)})
	e, _, c, dl := newClockedEngine(t, 1<<40)
	if err := e.SetSettings(ctx, Settings{MaxActive: 3, SeedRatio: 0, SeedHours: 1, StallMinutes: 30}); err != nil {
		t.Fatal(err)
	}
	idx := 0
	s, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &idx, Name: "Movie", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	done := waitFor(t, e, s.ID, "seeding", func(s Status) bool { return s.State == store.StateSeeding })
	file := filepath.Join(dl, done.FilePath)

	// Past the time limit but not imported yet: keeps sharing.
	c.Add(2 * time.Hour)
	time.Sleep(800 * time.Millisecond)
	if st, _ := e.Get(s.ID); st.State != store.StateSeeding {
		t.Fatalf("stopped before import settled: %s", st.State)
	}
	// Imported: stops and deletes the download copy.
	e.SetImport(ctx, s.ID, store.ImportImported, "")
	fin := waitFor(t, e, s.ID, "done", func(s Status) bool { return s.State == store.StateDone })
	if !fin.DataRemoved {
		t.Fatal("data not marked removed")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("download copy still there: %v", err)
	}
	// Done downloads don't resume.
	if r, _ := e.Resume(ctx, s.ID); r.State != store.StateDone {
		t.Fatalf("resume changed a done download to %s", r.State)
	}
}

func TestSharingLimitKeepsUnimportedFile(t *testing.T) {
	ctx := context.Background()
	ih, peer := seeder(t, map[string][]byte{"Home.Video.mkv": randBytes(200 << 10)})
	e, _, c, dl := newClockedEngine(t, 1<<40)
	e.SetSettings(ctx, Settings{MaxActive: 3, SeedRatio: 0, SeedHours: 1})
	s, _ := e.Add(ctx, AddRequest{InfoHash: ih, Name: "Home video", Peers: []string{peer}})
	done := waitFor(t, e, s.ID, "seeding", func(s Status) bool { return s.State == store.StateSeeding })
	e.SetImport(ctx, s.ID, store.ImportSkipped, "not linked")
	c.Add(2 * time.Hour)
	fin := waitFor(t, e, s.ID, "done", func(s Status) bool { return s.State == store.StateDone })
	if fin.DataRemoved {
		t.Fatal("deleted the only copy of an unimported download")
	}
	if _, err := os.Stat(filepath.Join(dl, done.FilePath)); err != nil {
		t.Fatalf("file gone: %v", err)
	}
}

func TestStall(t *testing.T) {
	ctx := context.Background()
	e, _, c, _ := newClockedEngine(t, 1<<40)
	e.SetSettings(ctx, Settings{MaxActive: 3, StallMinutes: 10})
	s, _ := e.Add(ctx, AddRequest{InfoHash: "4444444444444444444444444444444444444444", Name: "nobody seeds this"})
	waitFor(t, e, s.ID, "metadata", func(s Status) bool { return s.State == store.StateMetadata })
	c.Add(5 * time.Minute)
	time.Sleep(200 * time.Millisecond)
	if st, _ := e.Get(s.ID); st.State != store.StateMetadata {
		t.Fatalf("failed too early: %+v", st)
	}
	c.Add(6 * time.Minute)
	got := waitFor(t, e, s.ID, "stalled", func(s Status) bool { return s.State == store.StateFailed })
	if !strings.Contains(got.Error, "Stalled") {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestNotEnoughSpace(t *testing.T) {
	ctx := context.Background()
	ih, peer := seeder(t, map[string][]byte{"Big.mkv": randBytes(400 << 10)})
	e, _, _, _ := newClockedEngine(t, 100<<10) // 100 KiB free
	e.SetSettings(ctx, Settings{MaxActive: 3, MinFreeGB: 0})
	s, _ := e.Add(ctx, AddRequest{InfoHash: ih, Name: "Big", Peers: []string{peer}})
	got := waitFor(t, e, s.ID, "failed for space", func(s Status) bool { return s.State == store.StateFailed })
	if !strings.Contains(got.Error, "not enough free disk space") {
		t.Fatalf("error = %q", got.Error)
	}
	if err := e.RoomFor(50 << 10); err != nil {
		t.Fatalf("50 KiB should fit: %v", err)
	}
	if err := e.SetSettings(ctx, Settings{MaxActive: 1, StallMinutes: 2}); err == nil {
		t.Fatal("2-minute stall limit accepted")
	}
}
