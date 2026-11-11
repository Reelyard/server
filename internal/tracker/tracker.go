// Package tracker fetches tracked titles by itself: it keeps their episode
// lists current, searches suppliers for what's missing, grabs the best
// release for the title's quality profile, and follows each download until
// it is in the library.
package tracker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/reelyard/server/internal/engine"
	"github.com/reelyard/server/internal/events"
	"github.com/reelyard/server/internal/importer"
	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/metadata"
	"github.com/reelyard/server/internal/profile"
	"github.com/reelyard/server/internal/search"
	"github.com/reelyard/server/internal/store"
)

// Downloads is the part of the engine the tracker uses.
type Downloads interface {
	Add(ctx context.Context, r engine.AddRequest) (*engine.Status, error)
	Get(id int64) (*engine.Status, error)
	Find(infoHash string, fileIdx int) (*engine.Status, bool)
	Remove(ctx context.Context, id int64, deleteData bool) error
	RoomFor(need int64) error
}

// Streams finds candidates for a movie or episode.
type Streams interface {
	Streams(ctx context.Context, typ, id string) (*search.Result, error)
}

// Options wire the tracker.
type Options struct {
	Store      *store.Store
	Meta       *metadata.Service
	Search     Streams
	Downloads  Downloads
	Bus        *events.Bus
	Log        *slog.Logger
	Tick       time.Duration // how often to look for work (default 1 min)
	PerTick    int           // searches per tick (default 5)
	RefreshAge time.Duration // refresh series metadata this often (default 12 h)
}

// Tracker runs the automation loop.
type Tracker struct {
	o    Options
	wake chan struct{}
	mu   sync.Mutex // one pass at a time
}

// New returns a tracker; call Run to start it.
func New(o Options) *Tracker {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Tick == 0 {
		o.Tick = time.Minute
	}
	if o.PerTick == 0 {
		o.PerTick = 5
	}
	if o.RefreshAge == 0 {
		o.RefreshAge = 12 * time.Hour
	}
	return &Tracker{o: o, wake: make(chan struct{}, 1)}
}

// Kick runs a pass soon.
func (t *Tracker) Kick() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// Run loops until ctx ends.
func (t *Tracker) Run(ctx context.Context) {
	tick := time.NewTicker(t.o.Tick)
	defer tick.Stop()
	for {
		t.Pass(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-t.wake:
		}
	}
}

// Pass does one round: refresh metadata, follow downloads, search.
func (t *Tracker) Pass(ctx context.Context) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if titles, err := t.o.Store.TitlesToRefresh(ctx, t.o.RefreshAge); err == nil {
		for i := range titles {
			if err := t.refresh(ctx, &titles[i]); err != nil && ctx.Err() == nil {
				t.o.Log.Warn("refresh title", "title", titles[i].Name, "err", err)
			}
		}
	}
	t.follow(ctx)
	items, err := t.o.Store.DueItems(ctx, t.o.PerTick)
	if err != nil {
		return
	}
	for i := range items {
		if ctx.Err() != nil {
			return
		}
		if err := t.searchItem(ctx, &items[i]); err != nil {
			t.o.Log.Warn("search", "item", items[i].ItemID, "err", err)
		}
	}
}

func (t *Tracker) activity(ctx context.Context, level, titleID, msg string) {
	a, err := t.o.Store.AddActivity(ctx, level, "tracker", titleID, msg)
	if err == nil && t.o.Bus != nil {
		t.o.Bus.Publish(events.Event{Type: "activity", Data: a})
	}
}

// ---- tracking ----

// Errors shown to the user.
var (
	ErrTracked    = errors.New("this title is already tracked")
	ErrBadMonitor = errors.New("monitor must be all, future or none")
)

// TrackRequest describes a title to start tracking.
type TrackRequest struct {
	IMDbID    string
	Type      string // movie | series
	ProfileID int64
	Monitor   string // all | future | none (series)
	Seasons   []int  // empty = all
}

// Track starts tracking a title and queues its first search.
func (t *Tracker) Track(ctx context.Context, r TrackRequest) (*store.Title, error) {
	if r.Type != "movie" && r.Type != "series" {
		return nil, errors.New("type must be movie or series")
	}
	if r.Monitor == "" {
		r.Monitor = "all"
	}
	if r.Monitor != "all" && r.Monitor != "future" && r.Monitor != "none" {
		return nil, ErrBadMonitor
	}
	if _, err := t.o.Store.GetProfile(ctx, r.ProfileID); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	meta, err := t.o.Meta.Meta(ctx, r.Type, r.IMDbID)
	if err != nil {
		return nil, fmt.Errorf("couldn't look up the title: %w", err)
	}
	ti, err := t.o.Store.CreateTitle(ctx, &store.Title{
		IMDbID: r.IMDbID, Type: r.Type, Name: meta.Name, Year: importer.YearFrom(firstNonEmpty(meta.Year, meta.ReleaseInfo)),
		Poster: meta.Poster, ProfileID: r.ProfileID, Monitor: r.Monitor, Seasons: r.Seasons,
	})
	if errors.Is(err, store.ErrDuplicate) {
		return nil, ErrTracked
	}
	if err != nil {
		return nil, err
	}
	if err := t.refresh(ctx, ti); err != nil {
		return nil, err
	}
	t.activity(ctx, "info", r.IMDbID, "Started tracking "+ti.Name)
	t.Kick()
	return t.o.Store.GetTitle(ctx, ti.ID)
}

// Update changes a title's profile and monitoring, and re-applies the
// monitoring to its episodes.
func (t *Tracker) Update(ctx context.Context, id, profileID int64, monitor string, seasons []int) (*store.Title, error) {
	if monitor != "all" && monitor != "future" && monitor != "none" {
		return nil, ErrBadMonitor
	}
	if _, err := t.o.Store.GetProfile(ctx, profileID); err != nil {
		return nil, fmt.Errorf("profile: %w", err)
	}
	if err := t.o.Store.UpdateTitle(ctx, id, profileID, monitor, seasons); err != nil {
		return nil, err
	}
	ti, err := t.o.Store.GetTitle(ctx, id)
	if err != nil {
		return nil, err
	}
	items, err := t.o.Store.ItemsOfTitle(ctx, id)
	if err != nil {
		return nil, err
	}
	for _, it := range items {
		if want := monitored(ti, it); want != it.Monitored {
			_ = t.o.Store.SetMonitored(ctx, it.ID, want)
		}
	}
	_ = t.o.Store.SearchSoon(ctx, id, 0)
	t.Kick()
	return t.o.Store.GetTitle(ctx, id)
}

// SearchNow makes a title's missing items (or one item) due and runs a pass.
func (t *Tracker) SearchNow(ctx context.Context, titleID, itemID int64) error {
	if err := t.o.Store.SearchSoon(ctx, titleID, itemID); err != nil {
		return err
	}
	t.Kick()
	return nil
}

// monitored decides whether an item should be fetched under the title's
// settings. Movies always are; specials only when season 0 is chosen.
func monitored(ti *store.Title, it store.Item) bool {
	if ti.Type == "movie" {
		return true
	}
	if ti.Monitor == "none" {
		return false
	}
	if len(ti.Seasons) > 0 {
		in := false
		for _, s := range ti.Seasons {
			if s == it.Season {
				in = true
			}
		}
		if !in {
			return false
		}
	} else if it.Season == 0 {
		return false
	}
	if ti.Monitor == "future" {
		air, err := time.Parse(time.RFC3339, it.AirDate)
		return err != nil || air.After(ti.AddedAt)
	}
	return true
}

// refresh syncs a title's items with its metadata.
func (t *Tracker) refresh(ctx context.Context, ti *store.Title) error {
	meta, err := t.o.Meta.Meta(ctx, ti.Type, ti.IMDbID)
	if err != nil {
		return err
	}
	now := time.Now()
	if ti.Type == "movie" {
		it := store.Item{TitleID: ti.ID, ItemID: ti.IMDbID, Name: meta.Name, AirDate: meta.Released, Monitored: true, State: store.ItemMissing}
		if r, err := time.Parse(time.RFC3339, meta.Released); err == nil && r.After(now) {
			it.State = store.ItemUnaired
		}
		if err := t.o.Store.UpsertItem(ctx, it); err != nil {
			return err
		}
	} else {
		for _, v := range meta.Videos {
			if v.Episode <= 0 {
				continue
			}
			id := v.ID
			if !strings.HasPrefix(id, ti.IMDbID+":") {
				id = fmt.Sprintf("%s:%d:%d", ti.IMDbID, v.Season, v.Episode)
			}
			it := store.Item{TitleID: ti.ID, ItemID: id, Season: v.Season, Episode: v.Episode, Name: firstNonEmpty(v.Title, v.Name)}
			if !v.Released.IsZero() {
				it.AirDate = v.Released.UTC().Format(time.RFC3339)
			}
			it.Monitored = monitored(ti, it)
			it.State = store.ItemMissing
			if v.Released.IsZero() || v.Released.After(now) {
				it.State = store.ItemUnaired
			}
			if err := t.o.Store.UpsertItem(ctx, it); err != nil {
				return err
			}
		}
	}
	if n, _ := t.o.Store.PromoteAired(ctx, ti.ID); n > 0 {
		t.activity(ctx, "info", ti.IMDbID, fmt.Sprintf("%d new episode(s) of %s aired", n, ti.Name))
	}
	return t.o.Store.TouchTitleRefreshed(ctx, ti.ID, firstNonEmpty(meta.Name, ti.Name),
		max(ti.Year, importer.YearFrom(firstNonEmpty(meta.Year, meta.ReleaseInfo))), firstNonEmpty(meta.Poster, ti.Poster))
}

// ---- searching ----

var backoff = []time.Duration{15 * time.Minute, time.Hour, 3 * time.Hour, 6 * time.Hour, 12 * time.Hour, 24 * time.Hour}

// NextSearch returns when to look again after n fruitless searches.
func NextSearch(n int) time.Duration {
	if n < 1 {
		n = 1
	}
	if n > len(backoff) {
		n = len(backoff)
	}
	return backoff[n-1]
}

func stamp(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }

func (t *Tracker) searchItem(ctx context.Context, it *store.Item) error {
	ti, err := t.o.Store.GetTitle(ctx, it.TitleID)
	if err != nil {
		return err
	}
	prof, err := t.o.Store.GetProfile(ctx, ti.ProfileID)
	if err != nil {
		return err
	}
	now := time.Now()
	label := itemLabel(ti, it)

	res, err := t.o.Search.Streams(ctx, ti.Type, it.ItemID)
	if err != nil {
		note := "search failed: " + err.Error()
		next := stamp(now.Add(NextSearch(it.Attempts + 1)))
		return t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{NextSearchAt: &next, LastSearchAt: ptr(stamp(now)), Note: &note})
	}
	blocked, _ := t.o.Store.BlockedFor(ctx, it.ItemID)
	cx := profile.Context{Episode: ti.Type == "series", Season: it.Season, Number: it.Episode, Blocked: blocked, Siblings: t.siblings(ctx, ti, it)}
	ranked := profile.Rank(prof, res.Candidates, cx)

	if len(ranked) == 0 || !ranked[0].Eval.Accepted {
		attempts := it.Attempts + 1
		wait := NextSearch(attempts)
		note := profile.Summary(ranked) + fmt.Sprintf("; next try in %s", human(wait))
		if len(res.Suppliers) == 0 {
			note = "no stream suppliers are on"
		}
		next := stamp(now.Add(wait))
		if attempts == 1 {
			t.activity(ctx, "warn", it.ItemID, "Nothing fits "+prof.Name+" for "+label+" yet: "+profile.Summary(ranked))
		}
		return t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{Attempts: &attempts, NextSearchAt: &next, LastSearchAt: ptr(stamp(now)), Note: &note})
	}

	best := ranked[0]
	idx := -1
	if best.FileIdx != nil {
		idx = *best.FileIdx
	}
	// Season packs report the whole torrent; only the one file is fetched.
	if size := best.Stream.Size; size > 0 && !best.Stream.Info.SeasonPack {
		if err := t.o.Downloads.RoomFor(size); err != nil {
			wait := time.Hour
			note := capitalize(err.Error()) + fmt.Sprintf("; will try again in %s", human(wait))
			if !strings.HasPrefix(it.Note, "Not enough free disk space") {
				t.activity(ctx, "error", it.ItemID, fmt.Sprintf("Can't grab %s: %s", label, err))
			}
			return t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{NextSearchAt: ptr(stamp(now.Add(wait))), LastSearchAt: ptr(stamp(now)), Note: &note})
		}
	}
	var d *engine.Status
	if existing, ok := t.o.Downloads.Find(best.InfoHash, idx); ok {
		d = existing
	} else {
		d, err = t.o.Downloads.Add(ctx, engine.AddRequest{
			InfoHash: best.InfoHash, FileIdx: best.FileIdx, Sources: best.Sources, Name: best.Stream.TorrentName,
			TitleType: ti.Type, TitleID: it.ItemID, TitleName: ti.Name,
		})
		if err != nil {
			return err
		}
	}
	note := fmt.Sprintf("Grabbed %s (%s)", best.Stream.TorrentName, strings.Join(nonEmpty(best.Stream.Info.Resolution, best.Stream.Info.Source, langList(best.Stream.Info.Languages)), ", "))
	if err := t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{
		State: ptr(store.ItemDownloading), DownloadID: &d.ID, Attempts: ptr(0),
		Grabbed: &store.BlockedRelease{InfoHash: best.InfoHash, FileIdx: idx, Name: best.Stream.TorrentName}, NextSearchAt: ptr(""), LastSearchAt: ptr(stamp(now)), Note: &note,
	}); err != nil {
		return err
	}
	t.activity(ctx, "info", it.ItemID, fmt.Sprintf("Picked a release for %s: %s", label, best.Stream.TorrentName))
	return nil
}

// siblings are the info hashes already downloading or downloaded for other
// episodes of the same season, so one season pack serves them all.
func (t *Tracker) siblings(ctx context.Context, ti *store.Title, it *store.Item) map[string]bool {
	out := map[string]bool{}
	if ti.Type != "series" {
		return out
	}
	items, err := t.o.Store.ItemsOfTitle(ctx, ti.ID)
	if err != nil {
		return out
	}
	for _, o := range items {
		if o.ID == it.ID || o.Season != it.Season || o.DownloadID == nil {
			continue
		}
		if d, err := t.o.Downloads.Get(*o.DownloadID); err == nil {
			out[d.InfoHash] = true
		}
	}
	return out
}

// ---- following downloads ----

func (t *Tracker) follow(ctx context.Context) {
	items, err := t.o.Store.ItemsDownloading(ctx)
	if err != nil {
		return
	}
	for i := range items {
		it := &items[i]
		if it.DownloadID == nil {
			t.retry(ctx, it, it.GrabbedHash, it.GrabbedIdx, it.GrabbedName, "the download was removed")
			continue
		}
		d, err := t.o.Downloads.Get(*it.DownloadID)
		if errors.Is(err, engine.ErrNotFound) {
			t.retry(ctx, it, it.GrabbedHash, it.GrabbedIdx, it.GrabbedName, "the download was removed")
			continue
		}
		if err != nil {
			continue
		}
		switch {
		case d.ImportState == store.ImportImported:
			_ = t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{State: ptr(store.ItemImported), Note: ptr("In your library")})
		case d.ImportState == store.ImportRejected:
			t.retry(ctx, it, d.InfoHash, d.FileIdx, d.Name, d.ImportError)
			_ = t.o.Downloads.Remove(ctx, d.ID, true)
		case d.State == store.StateFailed:
			t.retry(ctx, it, d.InfoHash, d.FileIdx, d.Name, "download failed: "+d.Error)
			_ = t.o.Downloads.Remove(ctx, d.ID, true)
		case d.ImportState == store.ImportFailed:
			_ = t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{Note: ptr("Downloaded, but not imported: " + d.ImportError)})
		}
	}
}

// retry blocks the release (if known) and puts the item back to missing.
func (t *Tracker) retry(ctx context.Context, it *store.Item, infoHash string, fileIdx int, name, reason string) {
	if infoHash != "" {
		_ = t.o.Store.Block(ctx, store.BlockedRelease{InfoHash: infoHash, FileIdx: fileIdx, ItemID: it.ItemID, Name: name, Reason: reason})
	}
	note := "Searching again: " + reason
	_ = t.o.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{State: ptr(store.ItemMissing), DownloadID: ptr(int64(0)), NextSearchAt: ptr(""), Note: &note})
	t.activity(ctx, "warn", it.ItemID, fmt.Sprintf("%s: %s; looking for another release", it.ItemID, reason))
}

// ---- hooks for the importer ----

// ProfileFor returns the profile of the tracked title an item id belongs to.
func (t *Tracker) ProfileFor(ctx context.Context, itemID string) (*store.Profile, bool) {
	it, err := t.o.Store.ItemByItemID(ctx, itemID)
	if err != nil {
		return nil, false
	}
	ti, err := t.o.Store.GetTitle(ctx, it.TitleID)
	if err != nil {
		return nil, false
	}
	p, err := t.o.Store.GetProfile(ctx, ti.ProfileID)
	return p, err == nil
}

// SubtitleLangs returns the profile's subtitle languages for a tracked item.
func (t *Tracker) SubtitleLangs(ctx context.Context, itemID string) ([]string, bool) {
	p, ok := t.ProfileFor(ctx, itemID)
	if !ok {
		return nil, false
	}
	return p.SubtitleLangs, true
}

// CheckAudio rejects a file whose audio languages (from ffprobe) don't
// include one the profile requires.
func (t *Tracker) CheckAudio(ctx context.Context, itemID string, audio []string) error {
	p, ok := t.ProfileFor(ctx, itemID)
	if !ok || !p.AudioRequired || len(p.AudioLangs) == 0 || len(audio) == 0 {
		return nil
	}
	for _, want := range p.AudioLangs {
		for _, have := range audio {
			if lang.Normalize(want) == lang.Normalize(have) || strings.SplitN(want, "-", 2)[0] == strings.SplitN(have, "-", 2)[0] {
				return nil
			}
		}
	}
	return fmt.Errorf("the audio is %s, but the profile needs %s", names(audio), names(p.AudioLangs))
}

// ---- helpers ----

func itemLabel(ti *store.Title, it *store.Item) string {
	if ti.Type == "series" {
		return fmt.Sprintf("%s S%02dE%02d", ti.Name, it.Season, it.Episode)
	}
	return ti.Name
}

func names(codes []string) string {
	n := make([]string, len(codes))
	for i, c := range codes {
		n[i] = lang.Name(c)
	}
	return strings.Join(n, "/")
}

func langList(l []string) string {
	if len(l) == 0 {
		return ""
	}
	return strings.ToUpper(strings.Join(l, "/"))
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func human(d time.Duration) string {
	if d < time.Hour {
		return fmt.Sprintf("%d min", int(d.Minutes()))
	}
	return fmt.Sprintf("%d h", int(d.Hours()))
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func ptr[T any](v T) *T { return &v }

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Limits returns the sharing limits of the profile of a tracked download.
func (t *Tracker) Limits(d store.Download) (float64, int, bool) {
	if d.TitleID == "" {
		return 0, 0, false
	}
	p, ok := t.ProfileFor(context.Background(), d.TitleID)
	if !ok {
		return 0, 0, false
	}
	return p.SeedRatio, p.SeedHours, true
}
