package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/reelyard/server/internal/store"
)

// held reports whether this process still has path open or mapped although
// it was deleted (Linux only: it reads /proc).
func held(t *testing.T, path string) bool {
	t.Helper()
	fds, _ := os.ReadDir("/proc/self/fd")
	for _, fd := range fds {
		if l, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil && l == path+" (deleted)" {
			return true
		}
	}
	maps, _ := os.ReadFile("/proc/self/maps")
	return strings.Contains(string(maps), path+" (deleted)")
}

func twoEpisodes(t *testing.T) (ih, peer string, e *Engine, st *store.Store, dataDir, dlDir string) {
	t.Helper()
	ih, peer = seeder(t, map[string][]byte{
		"Show.S01E01.mkv": randBytes(400 << 10),
		"Show.S01E02.mkv": randBytes(400 << 10),
	})
	base := t.TempDir()
	var err error
	st, err = store.Open(context.Background(), filepath.Join(base, "h.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	dataDir, dlDir = filepath.Join(base, "data"), filepath.Join(base, "downloads")
	e = newEngine(t, st, dataDir, dlDir)
	return
}

// Deleting one episode of a torrent other downloads still share frees its
// space at once: the torrent is reopened so the file isn't held.
func TestRemoveSharedFileReleasesIt(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("checks open files through /proc")
	}
	ctx := context.Background()
	ih, peer, e, _, _, dlDir := twoEpisodes(t)
	defer e.Close()
	zero, one := 0, 1
	a, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &zero, Name: "E01", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &one, Name: "E02", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	sa := waitFor(t, e, a.ID, "E01 seeding", func(s Status) bool { return s.State == store.StateSeeding })
	waitFor(t, e, b.ID, "E02 seeding", func(s Status) bool { return s.State == store.StateSeeding })
	fileA, _ := filepath.Abs(filepath.Join(dlDir, sa.FilePath))

	if err := e.Remove(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(fileA); !os.IsNotExist(err) {
		t.Fatalf("E01 not deleted: %v", err)
	}
	if held(t, fileA) {
		t.Fatal("E01 was deleted but is still held open, so its space isn't freed")
	}
	// E02 carries on sharing, its file untouched. (The piece it shared
	// with E01 can't be shared any more, and isn't downloaded again.)
	sb := waitFor(t, e, b.ID, "E02 still seeding", func(s Status) bool { return s.State == store.StateSeeding })
	if st, err := os.Stat(filepath.Join(dlDir, sb.FilePath)); err != nil || st.Size() != sb.Size {
		t.Fatalf("E02's file changed: %v", err)
	}
}

// A seeding download whose files were deleted by hand stops sharing on
// restart instead of downloading them all again.
func TestSeedingWithoutFilesDoesNotDownload(t *testing.T) {
	ctx := context.Background()
	ih, peer, e, st, dataDir, dlDir := twoEpisodes(t)
	zero, one := 0, 1
	a, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &zero, Name: "E01", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.Add(ctx, AddRequest{InfoHash: ih, FileIdx: &one, Name: "E02", Peers: []string{peer}})
	if err != nil {
		t.Fatal(err)
	}
	sa := waitFor(t, e, a.ID, "E01 seeding", func(s Status) bool { return s.State == store.StateSeeding })
	sb := waitFor(t, e, b.ID, "E02 seeding", func(s Status) bool { return s.State == store.StateSeeding })
	if err := e.SetImport(ctx, a.ID, store.ImportImported, ""); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	// Both copies deleted while Reelyard was stopped; only E01 was imported.
	fileA, fileB := filepath.Join(dlDir, sa.FilePath), filepath.Join(dlDir, sb.FilePath)
	os.Remove(fileA)
	os.Remove(fileB)

	e2 := newEngine(t, st, dataDir, dlDir)
	defer e2.Close()
	ga := waitFor(t, e2, a.ID, "E01 done", func(s Status) bool { return s.State == store.StateDone })
	if !ga.DataRemoved {
		t.Fatal("E01 should be marked as having no download copy")
	}
	waitFor(t, e2, b.ID, "E02 failed", func(s Status) bool { return s.State == store.StateFailed })
	time.Sleep(time.Second) // give the peer a chance to send data
	for _, f := range []string{fileA, fileB} {
		if st, err := os.Stat(f); err == nil && st.Size() > 0 {
			t.Fatalf("%s was downloaded again (%d bytes)", filepath.Base(f), st.Size())
		}
	}
}
