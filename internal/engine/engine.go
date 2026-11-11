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
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	generics "github.com/anacrolix/generics"
	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"golang.org/x/time/rate"

	"github.com/inluxc/harbor/internal/diskfree"
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
	// LimitsFor returns the sharing limits for a download (from its tracked
	// title's profile). ok=false falls back to the global settings.
	LimitsFor func(d store.Download) (ratio float64, hours int, ok bool)
	// FreeSpace reports free bytes in the download folder (tests override).
	FreeSpace func(path string) (uint64, error)
	// Now is the clock (tests override).
	Now func() time.Time
	// BindInterface binds all torrent traffic to this interface's IPv4
	// address (e.g. a VPN's "wg0" or "tun0"). Empty = no binding.
	BindInterface string
	// InterfaceAddr looks up the interface address (tests override).
	InterfaceAddr func(name string) (net.IP, error)
	// OnRebind is called once when the bound interface gets a different
	// address (the app restarts itself when running as a service).
	OnRebind func()
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

	lastDone     int64     // DoneBytes at the last progress
	lastProgress time.Time // when DoneBytes last grew (or the job started)
	limRatio     float64   // cached sharing limits
	limHours     int
	limAt        time.Time
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

	onComplete func(id int64)

	boundIP     net.IP
	net         NetworkStatus
	rebindAsked bool
}

// SetLimitsFor sets where per-download sharing limits come from (the
// tracked title's profile).
func (e *Engine) SetLimitsFor(fn func(d store.Download) (float64, int, bool)) {
	e.mu.Lock()
	e.opts.LimitsFor = fn
	for _, j := range e.jobs {
		j.limAt = time.Time{}
	}
	e.mu.Unlock()
}

// OnComplete sets a function called (without locks held by the caller's
// goroutine) when a download finishes. Used to start the library import.
func (e *Engine) OnComplete(fn func(id int64)) {
	e.mu.Lock()
	e.onComplete = fn
	e.mu.Unlock()
}

func (e *Engine) activity(level, titleID, msg string) {
	a, err := e.opts.Store.AddActivity(context.Background(), level, "download", titleID, msg)
	if err == nil && e.opts.Bus != nil {
		e.opts.Bus.Publish(events.Event{Type: "activity", Data: a})
	}
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
	if o.FreeSpace == nil {
		o.FreeSpace = diskfree.Free
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.InterfaceAddr == nil {
		o.InterfaceAddr = InterfaceIPv4
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
		// Write files in place. With part files the library writes
		// "name.part" and renames it (read-only) only after a flush once
		// the last piece verifies, so a finished download can briefly be
		// missing from its path (seen on macOS), and read-only files
		// can't be deleted on Windows. Completion is tracked in pc.
		UsePartFiles: generics.Some(false),
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
	e.net = NetworkStatus{OK: true}
	if o.BindInterface != "" {
		ip, ierr := o.InterfaceAddr(o.BindInterface)
		e.net.Interface = o.BindInterface
		if ierr != nil {
			// Bind to loopback: nothing can reach the internet until the
			// interface is up and Harbor restarts.
			ip = net.IPv4(127, 0, 0, 1)
			e.net.OK = false
			e.net.Message = fmt.Sprintf("Paused: %s. Torrents won't use any other connection; restart Harbor once it's up.", ierr)
		}
		e.boundIP = ip
		e.net.Address = ip.String()
		bindConfig(cfg, ip)
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
	o.Log.Info("torrent engine started", "port", cl.LocalPort(), "downloads", o.DownloadDir, "bind", e.net.Address)
	if !e.net.OK {
		o.Log.Warn("network guard: paused", "reason", e.net.Message)
	}
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
	e.activity("info", d.TitleID, "Started downloading "+displayName(*d))
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
	e.applyGuard(t)
	j.lastProgress, j.lastDone = e.opts.Now(), j.d.DoneBytes
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
	if j.d.State != store.StateSeeding {
		if err := e.roomFor(j.d.Size - j.d.DoneBytes); err != nil {
			e.stopJob(j)
			e.setState(j, store.StateFailed, err.Error())
			return
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
	msg := "Removed " + displayName(j.d) + " from downloads"
	if deleteData {
		msg += " and deleted its files"
	}
	e.activity("info", j.d.TitleID, msg)
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

// Find returns the download of this torrent file, if there is one.
func (e *Engine) Find(infoHash string, fileIdx int) (*Status, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ih := strings.ToLower(infoHash)
	for _, j := range e.jobs {
		if j.d.InfoHash == ih && j.d.FileIdx == fileIdx {
			st := e.status(j)
			return &st, true
		}
	}
	return nil, false
}

// ---- limits, stalls, disk space ----

// FreeBytes returns free space in the download folder.
func (e *Engine) FreeBytes() (uint64, error) { return e.opts.FreeSpace(e.opts.DownloadDir) }

// ErrNoSpace means a download wouldn't fit on disk.
var ErrNoSpace = errors.New("not enough free disk space")

// RoomFor checks that need more bytes fit in the download folder while
// keeping the configured space free.
func (e *Engine) RoomFor(need int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.roomFor(need)
}

func (e *Engine) roomFor(need int64) error {
	if need <= 0 {
		return nil
	}
	free, err := e.opts.FreeSpace(e.opts.DownloadDir)
	if err != nil {
		return nil // can't tell; don't block
	}
	keep := uint64(e.settings.MinFreeGB) << 30
	if uint64(need)+keep > free {
		return fmt.Errorf("%w: %s free, needs %s plus %d GB kept free", ErrNoSpace, gb(int64(free)), gb(need), e.settings.MinFreeGB)
	}
	return nil
}

func gb(n int64) string {
	if n >= 1<<30 {
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	}
	return fmt.Sprintf("%d MB", n>>20)
}

// checkStall fails a download that hasn't progressed for StallMinutes.
// Caller holds e.mu.
func (e *Engine) checkStall(j *job) {
	if j.d.State != store.StateDownloading && j.d.State != store.StateMetadata {
		return
	}
	now := e.opts.Now()
	if !e.net.OK {
		j.lastProgress = now // paused by the network guard, not stalled
		return
	}
	if j.d.DoneBytes > j.lastDone {
		j.lastDone, j.lastProgress = j.d.DoneBytes, now
		return
	}
	limit := time.Duration(e.settings.StallMinutes) * time.Minute
	if limit <= 0 || now.Sub(j.lastProgress) < limit {
		return
	}
	msg := fmt.Sprintf("Stalled: no progress for %d minutes", e.settings.StallMinutes)
	if j.d.State == store.StateMetadata {
		msg = fmt.Sprintf("Stalled: no peers had this torrent for %d minutes", e.settings.StallMinutes)
	} else if j.peers == 0 {
		msg += " (no peers)"
	}
	e.stopJob(j)
	e.setState(j, store.StateFailed, msg)
	e.opts.Log.Info("download stalled", "id", j.d.ID, "name", j.d.Name)
	e.activity("warn", j.d.TitleID, displayName(j.d)+": "+msg)
}

// limits returns the sharing limits for j, cached for a few minutes.
func (e *Engine) limits(j *job) (float64, int) {
	now := e.opts.Now()
	if !j.limAt.IsZero() && now.Sub(j.limAt) < 5*time.Minute {
		return j.limRatio, j.limHours
	}
	ratio, hours := e.settings.SeedRatio, e.settings.SeedHours
	if e.opts.LimitsFor != nil {
		if r, h, ok := e.opts.LimitsFor(j.d); ok {
			ratio, hours = r, h
		}
	}
	j.limRatio, j.limHours, j.limAt = ratio, hours, now
	return ratio, hours
}

// checkSharing stops seeding once a limit is reached, and deletes the
// download copy when the file is safely in the library. Caller holds e.mu.
func (e *Engine) checkSharing(j *job) {
	if j.d.State != store.StateSeeding || j.d.CompletedAt == nil || j.d.Size <= 0 {
		return
	}
	// Never stop before the import is settled: the library copy may still
	// need the downloaded file.
	if j.d.ImportState == "" || j.d.ImportState == store.ImportImporting {
		return
	}
	ratio, hours := e.limits(j)
	got := float64(j.d.Uploaded) / float64(j.d.Size)
	seeded := e.opts.Now().Sub(*j.d.CompletedAt)
	var why string
	switch {
	case ratio > 0 && got >= ratio:
		why = fmt.Sprintf("ratio %.2f reached", got)
	case hours > 0 && seeded >= time.Duration(hours)*time.Hour:
		why = fmt.Sprintf("shared for %d hours", hours)
	default:
		return
	}
	e.stopJob(j)
	e.setState(j, store.StateDone, "")
	msg := "Stopped sharing " + displayName(j.d) + " (" + why + ")"
	if j.d.ImportState == store.ImportImported {
		if err := e.removeData(j); err != nil {
			e.opts.Log.Warn("delete download copy", "id", j.d.ID, "err", err)
		} else {
			j.d.DataRemoved, j.dirty = true, true
			msg += "; deleted the download copy, the library file stays"
		}
	} else {
		msg += "; the file stays in the download folder"
	}
	e.opts.Log.Info("sharing stopped", "id", j.d.ID, "why", why)
	e.activity("info", j.d.TitleID, msg)
}

// removeData deletes j's files from the download folder (only j's file
// when other downloads share the torrent). Caller holds e.mu.
func (e *Engine) removeData(j *job) error {
	others := 0
	for _, o := range e.jobs {
		if o != j && o.d.InfoHash == j.d.InfoHash && !o.d.DataRemoved {
			others++
		}
	}
	target := filepath.Join(e.opts.DownloadDir, j.d.InfoHash)
	if others > 0 && j.d.FilePath != "" {
		target = filepath.Join(e.opts.DownloadDir, j.d.FilePath)
	}
	return e.removeInside(target)
}

// SetImport records the library import state of a download, in memory and
// in the database.
func (e *Engine) SetImport(ctx context.Context, id int64, state, msg string) error {
	e.mu.Lock()
	if j, ok := e.jobs[id]; ok {
		j.d.ImportState, j.d.ImportError = state, msg
	}
	e.mu.Unlock()
	return e.opts.Store.SetImportState(ctx, id, state, msg)
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
		if e.ticks%10 == 0 {
			e.checkNetwork()
		}
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
			if j.t == nil {
				continue // onInfo stopped it (no disk space, bad file index)
			}
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
		e.checkStall(j)
		if j.t == nil {
			continue
		}
		if j.d.State == store.StateDownloading && j.d.Size > 0 && j.d.DoneBytes >= j.d.Size {
			e.setState(j, store.StateSeeding, "")
			j.d.CompletedAt = ptr(e.opts.Now().UTC())
			e.opts.Log.Info("download complete", "id", j.d.ID, "name", j.d.Name)
			e.activity("info", j.d.TitleID, "Finished downloading "+displayName(j.d))
			if fn := e.onComplete; fn != nil {
				id := j.d.ID
				go fn(id)
			}
		}
		if e.ticks%30 == 0 {
			e.checkSharing(j)
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

func displayName(d store.Download) string {
	if d.TitleName == "" {
		return d.Name
	}
	if parts := strings.Split(d.TitleID, ":"); len(parts) == 3 {
		return fmt.Sprintf("%s S%sE%s", d.TitleName, pad(parts[1]), pad(parts[2]))
	}
	return d.TitleName
}

func pad(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

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
		DoneBytes: &j.d.DoneBytes, Uploaded: &j.d.Uploaded, Completed: j.d.CompletedAt != nil, DataRemoved: &j.d.DataRemoved,
	}
	if err := e.opts.Store.UpdateDownload(ctx, j.d.ID, u); err != nil && !errors.Is(err, store.ErrNotFound) {
		e.opts.Log.Warn("save download", "id", j.d.ID, "err", err)
		return
	}
	j.dirty = false
}
