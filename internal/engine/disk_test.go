package engine

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/reelyard/server/internal/store"
)

const gib = 1 << 30

// fakeDisk is a free-space function tests can change while the engine runs.
type fakeDisk struct{ free atomic.Uint64 }

func (f *fakeDisk) FreeSpace(string) (uint64, error) { return f.free.Load(), nil }

func newDiskEngine(t *testing.T, free uint64) (*Engine, *fakeDisk) {
	t.Helper()
	d := &fakeDisk{}
	d.free.Store(free)
	e, _, _, _ := newFreeEngine(t, d.FreeSpace)
	return e, d
}

// putJob adds a download straight to the engine's table, without a
// torrent behind it, standing in for one that is running.
func putJob(e *Engine, id int64, state string, size, done int64) {
	e.mu.Lock()
	e.jobs[id] = &job{d: store.Download{ID: id, InfoHash: strings.Repeat("e", 40), Name: "fake", State: state, Size: size, DoneBytes: done}}
	e.mu.Unlock()
}

func dropJob(e *Engine, id int64) {
	e.mu.Lock()
	delete(e.jobs, id)
	e.mu.Unlock()
}

// Running downloads keep room for what they still have to write: another
// that fits the free space on its own doesn't fit next to them.
func TestRoomReservesRunningDownloads(t *testing.T) {
	ctx := context.Background()
	e, _ := newDiskEngine(t, 10*gib)
	if err := e.SetSettings(ctx, Settings{MaxActive: 3, MinFreeGB: 1}); err != nil {
		t.Fatal(err)
	}
	putJob(e, 9001, store.StateDownloading, 5*gib, 1*gib) // 4 GiB to go
	putJob(e, 9002, store.StateMetadata, 4*gib, 0)        // 4 GiB to go
	putJob(e, 9003, store.StatePaused, 100*gib, 0)        // paused: writes nothing
	putJob(e, 9004, store.StateSeeding, 50*gib, 50*gib)   // done

	// 10 GiB free, 8 GiB still to write, 1 GiB kept: 1 GiB left.
	if err := e.RoomFor(2 * gib); !errors.Is(err, ErrNoSpace) || !waitsForSpace(err) {
		t.Fatalf("2 GiB: %v", err)
	}
	if err := e.RoomFor(1 * gib); err != nil {
		t.Fatalf("1 GiB should fit: %v", err)
	}
	// Too big even on its own: a plain no-space error, not a wait.
	err := e.RoomFor(20 * gib)
	if !errors.Is(err, ErrNoSpace) || waitsForSpace(err) {
		t.Fatalf("20 GiB: %v", err)
	}
	if !strings.HasPrefix(capitalizeFirst(err.Error()), "Not enough free disk space") {
		t.Fatalf("message the tracker matches on changed: %q", err)
	}
	// A job doesn't reserve room against itself.
	e.mu.Lock()
	self := e.roomFor(6*gib, e.jobs[9001])
	e.mu.Unlock()
	if self == nil {
		t.Fatal("6 GiB fit with the other 4 GiB reserved and 1 GiB kept")
	}
	dropJob(e, 9002)
	e.mu.Lock()
	self = e.roomFor(6*gib, e.jobs[9001])
	e.mu.Unlock()
	if self != nil {
		t.Fatalf("job counted against itself: %v", self)
	}
}

func capitalizeFirst(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// A download whose size turns out not to fit next to the running ones goes
// back to the queue with a note (not failed: the tracker would delete it),
// and starts once there is room.
func TestDownloadWaitsForReservedSpace(t *testing.T) {
	ctx := context.Background()
	ih, peer := seeder(t, map[string][]byte{"Movie.mkv": randBytes(300 << 10)})
	e, disk := newDiskEngine(t, 600<<10)
	e.SetSettings(ctx, Settings{MaxActive: 3, MinFreeGB: 0})
	putJob(e, 9001, store.StateDownloading, 500<<10, 100<<10) // 400 KiB to go

	s, err := e.Add(ctx, AddRequest{InfoHash: ih, Name: "Movie", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	got := waitFor(t, e, s.ID, "waiting for space", func(s Status) bool {
		return s.State == store.StateQueued && strings.HasPrefix(s.Error, waitSpacePrefix)
	})
	if !strings.Contains(got.Error, "running downloads still need") {
		t.Fatalf("note = %q", got.Error)
	}
	// Still waiting a while later, without failing.
	time.Sleep(300 * time.Millisecond)
	if st, _ := e.Get(s.ID); st.State != store.StateQueued {
		t.Fatalf("left the queue without room: %+v", st)
	}
	// Room appears: it starts (from the saved metadata) and the note goes.
	disk.free.Store(1 << 40)
	waitFor(t, e, s.ID, "started", func(s Status) bool {
		return (s.State == store.StateDownloading || s.State == store.StateSeeding) && s.Error == ""
	})
}

// Free space dropping below MinFreeGB pauses running downloads with a
// note, starts nothing new, and once space is back they continue by
// themselves. A download the user paused stays paused.
func TestDiskGuardPausesAndResumes(t *testing.T) {
	ctx := context.Background()
	e, disk := newDiskEngine(t, 100*gib)
	e.SetSettings(ctx, Settings{MaxActive: 2, MinFreeGB: 7, StallMinutes: 30})
	a, _ := e.Add(ctx, AddRequest{InfoHash: "a100000000000000000000000000000000000000", Name: "A"})
	b, _ := e.Add(ctx, AddRequest{InfoHash: "b100000000000000000000000000000000000000", Name: "B"})
	waitFor(t, e, a.ID, "A runs", func(s Status) bool { return s.State == store.StateMetadata })
	waitFor(t, e, b.ID, "B runs", func(s Status) bool { return s.State == store.StateMetadata })
	if _, err := e.Pause(ctx, b.ID); err != nil {
		t.Fatal(err)
	}

	disk.free.Store(6 * gib) // below the 7 GB kept free
	waitFor(t, e, a.ID, "A paused for space", func(s Status) bool {
		return s.State == store.StatePaused && s.Error == DiskFullMsg
	})
	// New downloads wait while the disk is low.
	c, _ := e.Add(ctx, AddRequest{InfoHash: "c100000000000000000000000000000000000000", Name: "C"})
	time.Sleep(500 * time.Millisecond)
	if st, _ := e.Get(c.ID); st.State != store.StateQueued {
		t.Fatalf("C started on a full disk: %+v", st)
	}

	disk.free.Store(100 * gib)
	waitFor(t, e, a.ID, "A again", func(s Status) bool { return s.State == store.StateMetadata && s.Error == "" })
	waitFor(t, e, c.ID, "C runs", func(s Status) bool { return s.State == store.StateMetadata })
	if st, _ := e.Get(b.ID); st.State != store.StatePaused || st.Error != "" {
		t.Fatalf("user-paused B changed: %+v", st)
	}
}

// A download paused for disk space (as one restored from the database
// after a restart) stays paused while the disk is low, then queues again.
func TestDiskPausedQueuesAgainWithRoom(t *testing.T) {
	ctx := context.Background()
	e, disk := newDiskEngine(t, 100*gib)
	e.SetSettings(ctx, Settings{MaxActive: 3, MinFreeGB: 7, StallMinutes: 30})
	putJob(e, 9001, store.StatePaused, 10*gib, 1*gib)
	e.mu.Lock()
	e.jobs[9001].d.Error = DiskFullMsg
	e.mu.Unlock()
	disk.free.Store(5 * gib)
	time.Sleep(300 * time.Millisecond)
	if st, _ := e.Get(9001); st.State != store.StatePaused {
		t.Fatalf("queued while the disk is low: %+v", st)
	}
	disk.free.Store(100 * gib)
	waitFor(t, e, 9001, "queued again", func(s Status) bool { return s.State != store.StatePaused })
}
