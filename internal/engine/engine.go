// Package engine runs Harbor's built-in BitTorrent client (anacrolix/torrent)
// and keeps the downloads table in step with it: queueing, file selection,
// progress, pause/resume, and resuming after a restart.
package engine

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"github.com/inluxc/harbor/internal/events"
	"github.com/inluxc/harbor/internal/store"
)

// DefaultTrackers are well-known public trackers added to every torrent so
// peers are found even when an addon sends none.
var DefaultTrackers = []string{
	"udp://tracker.opentrackr.org:1337/announce",
	"udp://open.stealth.si:80/announce",
	"udp://tracker.torrent.eu.org:451/announce",
	"udp://exodus.desync.com:6969/announce",
	"udp://open.demonii.com:1337/announce",
	"udp://tracker.openbittorrent.com:6969/announce",
}

// Options configure the engine at startup.
type Options struct {
	Store       *store.Store
	Bus         *events.Bus // may be nil
	Log         *slog.Logger
	DataDir     string // engine state (piece completion, saved .torrent info)
	DownloadDir string // torrent data
	ListenPort  int    // 0 picks a free port
	// For tests: no DHT and no public trackers.
	NoDHT             bool
	NoDefaultTrackers bool
	Tick              time.Duration // status interval (default 1s)
}

// Errors.
var (
	ErrBadInfoHash = errors.New("info hash must be 40 hex characters")
	ErrDuplicate   = errors.New("this file is already in your downloads")
	ErrNotFound    = errors.New("no such download")
)

// Status is a download plus live numbers.
type Status struct {
	store.Download
	Progress float64 `json:"progress"` // 0..1 of the wanted data
	DownRate int64   `json:"downRate"` // bytes/s
	UpRate   int64   `json:"upRate"`   // bytes/s
	Peers    int     `json:"peers"`
	Seeds    int     `json:"seeds"`
	ETA      int64   `json:"eta"` // seconds; -1 unknown
	Ratio    float64 `json:"ratio"`
}

type job struct {
	d        store.Download
	t        *torrent.Torrent
	file     *torrent.File // nil = whole torrent
	baseUp   int64         // uploaded before this session
	lastRead int64
	lastUp   int64
	downRate float64
	upRate   float64
	peers    int
	seeds    int
	dirty    bool
}

// Engine owns the torrent client.
type Engine struct {
	opts   Options
	client *torrent.Client
	pc     storage.PieceCompletion
	stor   storage.ClientImplCloser
	dl, ul *rate.Limiter

	mu       sync.Mutex
	jobs     map[int64]*job
	settings Settings
	ticks    int

	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates and starts the torrent client, restores downloads from the
// database, and starts the scheduler.
func New(ctx context.Context, o Options) (*Engine, error) {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Tick == 0 {
		o.Tick = time.Second
	}
	stateDir := filepath.Join(o.DataDir, "torrent")
	for _, d := range []string{stateDir, filepath.Join(stateDir, "meta"), o.DownloadDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return nil, err
		}
	}
	pc, err := storage.NewBoltPieceCompletion(stateDir)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil, fmt.Errorf("the download state in %s is locked: is another Harbor still running with this data folder?", stateDir)
		}
		return nil, fmt.Errorf("open piece state: %w", err)
	}
	e := &Engine{opts: o, pc: pc, jobs: map[int64]*job{}, wake: make(chan struct{}, 1)}
	e.settings = loadSettings(ctx, o.Store)
	e.dl = rate.NewLimiter(rate.Inf, unlimitedBurst)
	e.ul = rate.NewLimiter(rate.Inf, unlimitedBurst)
	e.applyLimits()

	e.stor = storage.NewFileOpts(storage.NewFileClientOpts{
		ClientBaseDir:   o.DownloadDir,
		PieceCompletion: pc,
		// <download dir>/<info hash>/<torrent's own layout>: torrents with
		// the same name can't collide.
		TorrentDirMaker: func(base string, _ *metainfo.Info, ih metainfo.Hash) string {
			return filepath.Join(base, ih.HexString())
		},
	})
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = o.DownloadDir
	cfg.DefaultStorage = e.stor
	cfg.ListenPort = o.ListenPort
	cfg.Seed = true
	cfg.NoDHT = o.NoDHT
	cfg.DisableWebtorrent = true
	cfg.DownloadRateLimiter = e.dl
	cfg.UploadRateLimiter = e.ul
	cfg.Slogger = slog.New(slog.NewTextHandler(io.Discard, nil))
	if o.Log.Enabled(ctx, slog.LevelDebug) {
		cfg.Slogger = o.Log.With("component", "torrent")
	}
	cl, err := torrent.NewClient(cfg)
	if err != nil {
		e.stor.Close()
		pc.Close()
		return nil, fmt.Errorf("start torrent client (port %d): %w", o.ListenPort, err)
	}
	e.client = cl

	if err := e.restore(ctx); err != nil {
		e.Close()
		return nil, err
	}
	runCtx, cancel := context.WithCancel(context.Background())
	e.cancel = cancel
	e.done = make(chan struct{})
	go e.loop(runCtx)
	o.Log.Info("torrent engine started", "port", cl.LocalPort(), "downloads", o.DownloadDir)
	return e, nil
}

// Port is the port peers connect to.
func (e *Engine) Port() int { return e.client.LocalPort() }

// Close persists progress and stops the client.
func (e *Engine) Close() error {
	if e.cancel != nil {
		e.cancel()
		<-e.done
	}
	e.mu.Lock()
	e.persistAll(context.Background(), true)
	e.mu.Unlock()
	if e.client != nil {
		e.client.Close()
	}
	e.stor.Close()
	return e.pc.Close()
}

// restore loads downloads from the database: active ones are queued again
// and seeding ones resume seeding.
func (e *Engine) restore(ctx context.Context) error {
	list, err := e.opts.Store.ListDownloads(ctx)
	if err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i := range list {
		d := list[i]
		j := &job{d: d, baseUp: d.Uploaded}
		e.jobs[d.ID] = j
		switch d.State {
		case store.StateMetadata, store.StateDownloading:
			e.setState(j, store.StateQueued, "")
		case store.StateSeeding:
			if err := e.start(j); err != nil {
				e.setState(j, store.StateFailed, err.Error())
			}
		}
	}
	e.persistAll(ctx, false)
	return nil
}

// AddRequest describes a new download.
type AddRequest struct {
	InfoHash  string
	FileIdx   *int // nil = whole torrent
	Sources   []string
	Name      string
	TitleType string
	TitleID   string
	TitleName string
	Peers     []string // host:port to connect to directly (tests, LAN)
}

// Add queues a download.
func (e *Engine) Add(ctx context.Context, r AddRequest) (*Status, error) {
	ih := strings.ToLower(strings.TrimSpace(r.InfoHash))
	if b, err := hex.DecodeString(ih); err != nil || len(b) != 20 {
		return nil, ErrBadInfoHash
	}
	idx := -1
	if r.FileIdx != nil && *r.FileIdx >= 0 {
		idx = *r.FileIdx
	}
	name := strings.TrimSpace(r.Name)
	if name == "" {
		name = ih
	}
	d, err := e.opts.Store.CreateDownload(ctx, store.NewDownload{
		InfoHash: ih, FileIdx: idx, Name: name, Sources: trackersFrom(r.Sources),
		TitleType: r.TitleType, TitleID: r.TitleID, TitleName: r.TitleName,
	})
	if errors.Is(err, store.ErrDuplicate) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, err
	}
	e.mu.Lock()
	j := &job{d: *d}
	e.jobs[d.ID] = j
	if len(r.Peers) > 0 {
		// Start right away so the peers can be handed over.
		if err := e.start(j); err == nil {
			addPeers(j.t, r.Peers)
		}
	}
	st := e.status(j)
	e.mu.Unlock()
	e.kick()
	e.opts.Log.Info("download added", "id", d.ID, "name", name, "file", idx)
	return &st, nil
}

// trackersFrom turns Stremio "sources" (tracker:udp://…, dht:…) into
// tracker URLs.
func trackersFrom(sources []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range sources {
		u := strings.TrimPrefix(s, "tracker:")
		if !strings.Contains(u, "://") || seen[u] {
			continue
		}
		seen[u] = true
		out = append(out, u)
	}
	return out
}

func addPeers(t *torrent.Torrent, peers []string) {
	var pi []torrent.PeerInfo
	for _, p := range peers {
		if addr, err := resolvePeer(p); err == nil {
			pi = append(pi, torrent.PeerInfo{Addr: addr, Trusted: true})
		}
	}
	t.AddPeers(pi)
}

// start adds the torrent to the client (if needed) and selects the file.
// Caller holds e.mu.
func (e *Engine) start(j *job) error {
	ih := metainfo.NewHashFromHex(j.d.InfoHash)
	opts := torrent.AddTorrentOpts{InfoHash: ih}
	if mi, err := metainfo.LoadFromFile(e.metaPath(j.d.InfoHash)); err == nil {
		opts.InfoBytes = mi.InfoBytes
	}
	t, _ := e.client.AddTorrentOpt(opts)
	var trackers []string
	trackers = append(trackers, j.d.Sources...)
	if !e.opts.NoDefaultTrackers {
		trackers = append(trackers, DefaultTrackers...)
	}
	if len(trackers) > 0 {
		tiers := make([][]string, len(trackers))
		for i, tr := range trackers {
			tiers[i] = []string{tr}
		}
		t.AddTrackers(tiers)
	}
	j.t = t
	stats := t.Stats()
	j.lastRead = stats.BytesReadUsefulData.Int64()
	j.lastUp = stats.BytesWrittenData.Int64()
	j.baseUp = j.d.Uploaded - j.lastUp
	if t.Info() != nil {
		e.onInfo(j)
	} else if j.d.State != store.StateSeeding {
		e.setState(j, store.StateMetadata, "")
	}
	return nil
}

// onInfo runs once torrent metadata is known: pick the file, record its
// size and path, save the metadata for restarts. Caller holds e.mu.
func (e *Engine) onInfo(j *job) {
	t := j.t
	info := t.Info()
	if j.d.FileIdx >= 0 {
		files := t.Files()
		if j.d.FileIdx >= len(files) {
			e.setState(j, store.StateFailed, fmt.Sprintf("the torrent has %d files, file #%d doesn't exist", len(files), j.d.FileIdx))
			return
		}
		j.file = files[j.d.FileIdx]
		j.file.Download()
		if j.d.FilePath == "" || j.d.Size == 0 {
			j.d.FilePath = filepath.Join(j.d.InfoHash, filepath.FromSlash(j.file.Path()))
			j.d.Size = j.file.Length()
			j.dirty = true
		}
	} else {
		t.DownloadAll()
		if j.d.FilePath == "" || j.d.Size == 0 {
			j.d.FilePath = filepath.Join(j.d.InfoHash, info.BestName())
			j.d.Size = t.Length()
			j.dirty = true
		}
	}
	if _, err := os.Stat(e.metaPath(j.d.InfoHash)); err != nil {
		mi := t.Metainfo()
		if f, err := os.Create(e.metaPath(j.d.InfoHash)); err == nil {
			_ = mi.Write(f)
			f.Close()
		}
	}
	if j.d.State != store.StateSeeding {
		e.setState(j, store.StateDownloading, "")
	}
}

func (e *Engine) metaPath(ih string) string {
	return filepath.Join(e.opts.DataDir, "torrent", "meta", ih+".torrent")
}

func (e *Engine) setState(j *job, state, msg string) {
	if j.d.State != state || j.d.Error != msg {
		j.d.State, j.d.Error = state, msg
		j.dirty = true
	}
}

// ---- control ----

func (e *Engine) get(id int64) (*job, error) {
	j, ok := e.jobs[id]
	if !ok {
		return nil, ErrNotFound
	}
	return j, nil
}

// stopJob stops transferring for j, dropping the torrent if no other
// running job shares it. Caller holds e.mu.
func (e *Engine) stopJob(j *job) {
	if j.t == nil {
		return
	}
	if j.file != nil {
		j.file.SetPriority(torrent.PiecePriorityNone)
	}
	shared := false
	for _, o := range e.jobs {
		if o != j && o.t == j.t {
			shared = true
		}
	}
	if !shared {
		j.t.Drop()
	}
	j.t, j.file = nil, nil
	j.downRate, j.upRate, j.peers, j.seeds = 0, 0, 0, 0
}

// Pause stops a download (or seeding) until resumed.
func (e *Engine) Pause(ctx context.Context, id int64) (*Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, err := e.get(id)
	if err != nil {
		return nil, err
	}
	e.updateProgress(j)
	e.stopJob(j)
	e.setState(j, store.StatePaused, "")
	e.persist(ctx, j)
	st := e.status(j)
	return &st, nil
}

// Resume queues a paused or failed download again (or resumes seeding if
// it was complete).
func (e *Engine) Resume(ctx context.Context, id int64) (*Status, error) {
	e.mu.Lock()
	j, err := e.get(id)
	if err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if j.d.State == store.StatePaused || j.d.State == store.StateFailed {
		if j.d.Size > 0 && j.d.DoneBytes >= j.d.Size {
			e.setState(j, store.StateSeeding, "")
			if err := e.start(j); err != nil {
				e.setState(j, store.StateFailed, err.Error())
			}
		} else {
			e.setState(j, store.StateQueued, "")
		}
		e.persist(ctx, j)
	}
	st := e.status(j)
	e.mu.Unlock()
	e.kick()
	return &st, nil
}

// Remove deletes a download, and its data if asked.
func (e *Engine) Remove(ctx context.Context, id int64, deleteData bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, err := e.get(id)
	if err != nil {
		return err
	}
	e.stopJob(j)
	delete(e.jobs, id)
	if err := e.opts.Store.DeleteDownload(ctx, id); err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	others := 0
	for _, o := range e.jobs {
		if o.d.InfoHash == j.d.InfoHash {
			others++
		}
	}
	if others == 0 {
		_ = os.Remove(e.metaPath(j.d.InfoHash))
	}
	if deleteData {
		target := filepath.Join(e.opts.DownloadDir, j.d.InfoHash)
		if others > 0 && j.d.FilePath != "" {
			target = filepath.Join(e.opts.DownloadDir, j.d.FilePath)
		}
		if err := e.removeInside(target); err != nil {
			e.opts.Log.Warn("delete download data", "id", id, "err", err)
		}
	}
	e.opts.Log.Info("download removed", "id", id, "deletedData", deleteData)
	e.kick()
	return nil
}

// removeInside deletes path only if it is inside the download folder.
func (e *Engine) removeInside(path string) error {
	base, _ := filepath.Abs(e.opts.DownloadDir)
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(base, abs)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("refusing to delete %s: outside the download folder", abs)
	}
	return os.RemoveAll(abs)
}

// ---- status ----

// List returns all downloads with live numbers, newest first.
func (e *Engine) List() []Status {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.listLocked()
}

func (e *Engine) listLocked() []Status {
	out := make([]Status, 0, len(e.jobs))
	for _, j := range e.jobs {
		out = append(out, e.status(j))
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

// Get returns one download.
func (e *Engine) Get(id int64) (*Status, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	j, err := e.get(id)
	if err != nil {
		return nil, err
	}
	st := e.status(j)
	return &st, nil
}

func (e *Engine) status(j *job) Status {
	st := Status{Download: j.d, DownRate: int64(j.downRate), UpRate: int64(j.upRate), Peers: j.peers, Seeds: j.seeds, ETA: -1}
	if st.Sources == nil {
		st.Sources = []string{}
	}
	if j.d.Size > 0 {
		st.Progress = float64(j.d.DoneBytes) / float64(j.d.Size)
		st.Ratio = float64(j.d.Uploaded) / float64(j.d.Size)
		if j.downRate > 1 && j.d.DoneBytes < j.d.Size {
			st.ETA = int64(float64(j.d.Size-j.d.DoneBytes) / j.downRate)
		}
	}
	return st
}

// ---- loop ----

func (e *Engine) kick() {
	select {
	case e.wake <- struct{}{}:
	default:
	}
}

func (e *Engine) loop(ctx context.Context) {
	defer close(e.done)
	t := time.NewTicker(e.opts.Tick)
	defer t.Stop()
	for {
		e.mu.Lock()
		e.schedule()
		e.refresh(e.opts.Tick.Seconds())
		e.ticks++
		e.persistAll(ctx, e.ticks%10 == 0)
		var snapshot []Status
		if e.opts.Bus != nil && e.opts.Bus.Subscribers() > 0 {
			snapshot = e.listLocked()
		}
		e.mu.Unlock()
		if snapshot != nil {
			e.opts.Bus.Publish(events.Event{Type: "downloads", Data: snapshot})
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-e.wake:
		}
	}
}

// schedule starts queued downloads while slots are free (oldest first).
func (e *Engine) schedule() {
	active := 0
	var queued []*job
	for _, j := range e.jobs {
		switch j.d.State {
		case store.StateMetadata, store.StateDownloading:
			active++
		case store.StateQueued:
			queued = append(queued, j)
		}
	}
	sort.Slice(queued, func(a, b int) bool { return queued[a].d.ID < queued[b].d.ID })
	for _, j := range queued {
		if e.settings.MaxActive > 0 && active >= e.settings.MaxActive {
			return
		}
		if err := e.start(j); err != nil {
			e.setState(j, store.StateFailed, err.Error())
			continue
		}
		active++
	}
}

// refresh reads progress and rates from the client.
func (e *Engine) refresh(dt float64) {
	for _, j := range e.jobs {
		if j.t == nil {
			continue
		}
		if j.d.State == store.StateMetadata && j.t.Info() != nil {
			e.onInfo(j)
		}
		e.updateProgress(j)
		s := j.t.Stats()
		read, up := s.BytesReadUsefulData.Int64(), s.BytesWrittenData.Int64()
		j.downRate = smooth(j.downRate, float64(read-j.lastRead)/dt)
		j.upRate = smooth(j.upRate, float64(up-j.lastUp)/dt)
		j.lastRead, j.lastUp = read, up
		j.peers, j.seeds = s.ActivePeers, s.ConnectedSeeders
		if total := j.baseUp + up; total != j.d.Uploaded {
			j.d.Uploaded = total
		}
		if j.d.State == store.StateDownloading && j.d.Size > 0 && j.d.DoneBytes >= j.d.Size {
			e.setState(j, store.StateSeeding, "")
			j.d.CompletedAt = ptr(time.Now().UTC())
			e.opts.Log.Info("download complete", "id", j.d.ID, "name", j.d.Name)
		}
	}
}

func (e *Engine) updateProgress(j *job) {
	if j.t == nil || j.t.Info() == nil {
		return
	}
	var done int64
	if j.file != nil {
		done = j.file.BytesCompleted()
	} else {
		done = j.t.BytesCompleted()
	}
	if done != j.d.DoneBytes {
		j.d.DoneBytes = done
	}
}

func smooth(prev, now float64) float64 {
	if now < 0 {
		now = 0
	}
	return prev*0.6 + now*0.4
}

func ptr[T any](v T) *T { return &v }

// persistAll writes state changes now, and progress counters when full.
func (e *Engine) persistAll(ctx context.Context, full bool) {
	for _, j := range e.jobs {
		if j.dirty || full {
			e.persist(ctx, j)
		}
	}
}

func (e *Engine) persist(ctx context.Context, j *job) {
	u := store.DownloadUpdate{
		State: &j.d.State, Error: &j.d.Error, FilePath: &j.d.FilePath, Size: &j.d.Size,
		DoneBytes: &j.d.DoneBytes, Uploaded: &j.d.Uploaded, Completed: j.d.CompletedAt != nil,
	}
	if err := e.opts.Store.UpdateDownload(ctx, j.d.ID, u); err != nil && !errors.Is(err, store.ErrNotFound) {
		e.opts.Log.Warn("save download", "id", j.d.ID, "err", err)
		return
	}
	j.dirty = false
}
