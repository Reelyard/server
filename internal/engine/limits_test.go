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

// A download with no peers for 10 minutes is paused while others wait for
// a slot, and the next one starts; with nobody waiting it keeps going.
func TestNoPeersPaused(t *testing.T) {
	ctx := context.Background()
	e, _, c, _ := newClockedEngine(t, 1<<40)
	e.SetSettings(ctx, Settings{MaxActive: 1, StallMinutes: 30})
	a, _ := e.Add(ctx, AddRequest{InfoHash: "5555555555555555555555555555555555555555", Name: "A"})
	b, _ := e.Add(ctx, AddRequest{InfoHash: "6666666666666666666666666666666666666666", Name: "B"})
	waitFor(t, e, a.ID, "A runs", func(s Status) bool { return s.State == store.StateMetadata })
	state := func(id int64) Status { s, _ := e.Get(id); return *s }

	c.Add(9 * time.Minute)
	time.Sleep(150 * time.Millisecond)
	if s := state(a.ID); s.State != store.StateMetadata {
		t.Fatalf("A paused too early: %+v", s)
	}
	c.Add(2 * time.Minute)
	waitFor(t, e, b.ID, "B runs", func(s Status) bool { return s.State == store.StateMetadata })
	if s := state(a.ID); s.State != store.StatePaused || s.Error != NoPeersMsg {
		t.Fatalf("A: %+v", s)
	}
	// Nobody waits behind B: it keeps its slot past 10 minutes.
	c.Add(12 * time.Minute)
	time.Sleep(150 * time.Millisecond)
	if s := state(b.ID); s.State != store.StateMetadata {
		t.Fatalf("B paused with nobody waiting: %+v", s)
	}
	// Resuming A queues it again, with the note cleared.
	if s, err := e.Resume(ctx, a.ID); err != nil || s.State != store.StateQueued || s.Error != "" {
		t.Fatalf("resume A: %+v %v", s, err)
	}
}

// The queue starts the best-seeded downloads first, then those with an
// unknown count, then those known to have no seeders.
func TestQueueBySeeders(t *testing.T) {
	ctx := context.Background()
	e, _, _, _ := newClockedEngine(t, 1<<40)
	e.SetSettings(ctx, Settings{MaxActive: 1, StallMinutes: 30})
	n := func(v int) *int { return &v }
	first, _ := e.Add(ctx, AddRequest{InfoHash: "7000000000000000000000000000000000000000", Name: "first"})
	waitFor(t, e, first.ID, "first runs", func(s Status) bool { return s.State == store.StateMetadata })
	unknown, _ := e.Add(ctx, AddRequest{InfoHash: "7100000000000000000000000000000000000000", Name: "unknown"})
	few, _ := e.Add(ctx, AddRequest{InfoHash: "7200000000000000000000000000000000000000", Name: "few", Seeders: n(5)})
	none, _ := e.Add(ctx, AddRequest{InfoHash: "7300000000000000000000000000000000000000", Name: "none", Seeders: n(0)})
	many, _ := e.Add(ctx, AddRequest{InfoHash: "7400000000000000000000000000000000000000", Name: "many", Seeders: n(50)})
	if s, _ := e.Get(few.ID); s.Seeders != 5 {
		t.Fatalf("seeders not kept: %+v", s)
	}
	// Learned later: the unknown one turns out to be the best.
	if err := e.SetSeeders(ctx, unknown.ID, 80); err != nil {
		t.Fatal(err)
	}
	prev := first.ID
	for _, want := range []int64{unknown.ID, many.ID, few.ID, none.ID} {
		if _, err := e.Pause(ctx, prev); err != nil {
			t.Fatal(err)
		}
		waitFor(t, e, want, "next by seeders", func(s Status) bool { return s.State == store.StateMetadata })
		prev = want
	}
}
