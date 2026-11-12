// Package importer moves finished downloads into the media library with
// Jellyfin/Plex names and fetches subtitles for them.
package importer

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reelyard/server/internal/engine"
	"github.com/reelyard/server/internal/events"
	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/metadata"
	"github.com/reelyard/server/internal/release"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

// Downloads is the part of the engine the importer uses.
type Downloads interface {
	Get(id int64) (*engine.Status, error)
	SetImport(ctx context.Context, id int64, state, msg string) error
}

// Options wire the importer.
type Options struct {
	Store       *store.Store
	Downloads   Downloads
	Meta        *metadata.Service
	Suppliers   *suppliers.Service
	Client      *stremio.Client
	Bus         *events.Bus // may be nil
	Log         *slog.Logger
	LibraryDir  string
	DownloadDir string
	HTTP        *http.Client // for fetching subtitle files

	// Optional hooks from the tracker (M4): per-title subtitle languages,
	// and a check that rejects a file whose audio the profile can't use.
	SubtitleLangsFor func(ctx context.Context, itemID string) ([]string, bool)
	CheckAudio       func(ctx context.Context, itemID string, audio []string) error
	// AudioLangsFor gives a tracked title's audio languages, preferred
	// first; the preferred one present is made the track that plays first.
	AudioLangsFor func(ctx context.Context, itemID string) ([]string, bool)
	// AfterImport runs after each import attempt (e.g. to wake the tracker).
	AfterImport func()
	// OnImported runs after a successful import (e.g. to refresh Jellyfin).
	OnImported func()
}

// Importer runs imports one at a time from a queue.
type Importer struct {
	o     Options
	queue chan int64

	mu      sync.Mutex
	pending map[int64]bool
}

// New returns an importer; call Run to start it.
func New(o Options) *Importer {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 30 * time.Second}
	}
	return &Importer{o: o, queue: make(chan int64, 256), pending: map[int64]bool{}}
}

// Run imports queued downloads until ctx ends. Finished downloads that
// were never imported (e.g. Reelyard stopped mid-import) are queued first.
func (im *Importer) Run(ctx context.Context) {
	if ids, err := im.o.Store.PendingImports(ctx); err == nil {
		for _, id := range ids {
			im.Enqueue(id)
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case id := <-im.queue:
			im.mu.Lock()
			delete(im.pending, id)
			im.mu.Unlock()
			if err := im.Import(ctx, id); err != nil && ctx.Err() == nil {
				im.o.Log.Warn("import failed", "download", id, "err", err)
			} else if err == nil && im.o.OnImported != nil {
				im.o.OnImported()
			}
			if im.o.AfterImport != nil {
				im.o.AfterImport()
			}
		}
	}
}

// Enqueue asks for a download to be imported (no-op if already queued).
func (im *Importer) Enqueue(id int64) {
	im.mu.Lock()
	defer im.mu.Unlock()
	if im.pending[id] {
		return
	}
	select {
	case im.queue <- id:
		im.pending[id] = true
	default:
		im.o.Log.Warn("import queue full", "download", id)
	}
}

func (im *Importer) activity(ctx context.Context, level, kind, titleID, msg string) {
	a, err := im.o.Store.AddActivity(ctx, level, kind, titleID, msg)
	if err == nil && im.o.Bus != nil {
		im.o.Bus.Publish(events.Event{Type: "activity", Data: a})
	}
}

var videoExts = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true, ".wmv": true,
	".ts": true, ".m2ts": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

// Import places one finished download in the library and fetches its
// subtitles.
func (im *Importer) Import(ctx context.Context, id int64) (err error) {
	d, err := im.o.Downloads.Get(id)
	if err != nil {
		return err
	}
	if d.State != store.StateSeeding && d.State != store.StatePaused {
		return fmt.Errorf("download %d isn't finished (%s)", id, d.State)
	}
	if d.Size > 0 && d.DoneBytes < d.Size {
		return fmt.Errorf("download %d isn't finished", id)
	}
	if d.TitleType == "" || d.TitleID == "" {
		return im.o.Downloads.SetImport(ctx, id, store.ImportSkipped, "Not linked to a movie or episode, so it stays in the download folder.")
	}
	_ = im.o.Downloads.SetImport(ctx, id, store.ImportImporting, "")
	defer func() {
		if err != nil {
			msg := friendly(err)
			_ = im.o.Downloads.SetImport(ctx, id, store.ImportFailed, msg)
			im.activity(ctx, "error", "import", d.TitleID, fmt.Sprintf("Couldn't import %s: %s", d.TitleName, msg))
		}
	}()

	src, err := im.videoFile(d)
	if err != nil {
		return err
	}
	probe, probed, perr := ProbeFile(ctx, src)
	if perr != nil {
		im.o.Log.Warn("probe", "file", src, "err", perr)
	}
	if probed && im.o.CheckAudio != nil {
		if aerr := im.o.CheckAudio(ctx, d.TitleID, probe.AudioLangs); aerr != nil {
			msg := capitalize(aerr.Error())
			_ = im.o.Downloads.SetImport(ctx, id, store.ImportRejected, msg)
			im.activity(ctx, "warn", "import", d.TitleID, fmt.Sprintf("Didn't import %s: %s", d.TitleName, aerr))
			return nil
		}
	}
	// Name the file by what's really in it when ffprobe can tell.
	res := firstNonEmpty(probe.Resolution, release.Parse(d.Name).Resolution)
	rel, err := im.libraryPath(ctx, d, src, res)
	if err != nil {
		return err
	}
	dst := filepath.Join(im.o.LibraryDir, filepath.FromSlash(rel))
	var prefs []string
	if probed && im.o.AudioLangsFor != nil && LoadSettings(ctx, im.o.Store).AudioFirst {
		prefs, _ = im.o.AudioLangsFor(ctx, d.TitleID)
	}
	method, lead, audioNote, err := placeAudioFirst(ctx, src, dst, probe, prefs)
	if err != nil {
		return err
	}
	if audioNote != "" {
		im.o.Log.Warn("audio order", "file", src, "err", audioNote)
	}
	if method == MethodRemux {
		// The track order changed: describe the new file.
		if p2, ok, err := ProbeFile(ctx, dst); ok && err == nil {
			probe.AudioLangs, probe.AudioCodecs, probe.EmbeddedSubs = p2.AudioLangs, p2.AudioCodecs, p2.EmbeddedSubs
		}
	}
	fi, err := os.Stat(dst)
	if err != nil {
		return err
	}
	hash, _ := OSHash(dst)
	did := d.ID
	mf, err := im.o.Store.UpsertMediaFile(ctx, &store.MediaFile{
		DownloadID: &did, TitleType: d.TitleType, TitleID: d.TitleID, TitleName: d.TitleName,
		Path: rel, Size: fi.Size(), Method: method, Resolution: res,
		VideoCodec: probe.VideoCodec, DurationSec: probe.DurationSec, AudioLangs: probe.AudioLangs, EmbeddedSubs: probe.EmbeddedSubs,
		AudioCodecs: probe.AudioCodecs, OSHash: hash,
	})
	if err != nil {
		return err
	}
	if err := im.o.Downloads.SetImport(ctx, id, store.ImportImported, ""); err != nil {
		return err
	}
	how := "linked"
	switch method {
	case MethodCopy:
		how = "copied"
	case MethodRemux:
		how = "copied with the " + lang.Name(lead) + " audio first"
	}
	note := ""
	switch {
	case !probed:
		note = " (install ffprobe to check languages and quality of imported files)"
	case audioNote != "":
		note = " (" + audioNote + ")"
	}
	im.activity(ctx, "info", "import", d.TitleID, fmt.Sprintf("Imported %s → %s (%s)%s", d.TitleName, rel, how, note))
	im.o.Log.Info("imported", "download", id, "path", rel, "method", method)

	if _, err := im.Subtitles(ctx, mf.ID); err != nil {
		im.o.Log.Warn("subtitles", "media", mf.ID, "err", err)
	}
	return nil
}

// videoFile finds the file to import: the selected file, or the largest
// video in a whole-torrent download.
func (im *Importer) videoFile(d *engine.Status) (string, error) {
	if d.FilePath == "" {
		return "", errors.New("the download has no file yet")
	}
	p := filepath.Join(im.o.DownloadDir, d.FilePath)
	fi, err := os.Stat(p)
	if err != nil {
		return "", fmt.Errorf("the downloaded file is missing: %w", err)
	}
	if !fi.IsDir() {
		if !videoExts[strings.ToLower(filepath.Ext(p))] {
			return "", fmt.Errorf("%s isn't a video file", filepath.Base(p))
		}
		return p, nil
	}
	var best string
	var bestSize int64
	_ = filepath.WalkDir(p, func(path string, de fs.DirEntry, err error) error {
		if err != nil || de.IsDir() || !videoExts[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		if strings.Contains(strings.ToLower(path), "sample") {
			return nil
		}
		if info, err := de.Info(); err == nil && info.Size() > bestSize {
			best, bestSize = path, info.Size()
		}
		return nil
	})
	if best == "" {
		return "", errors.New("no video file in the download")
	}
	return best, nil
}

func (im *Importer) libraryPath(ctx context.Context, d *engine.Status, src, res string) (string, error) {
	imdb, s, e, err := splitID(d.TitleType, d.TitleID)
	if err != nil {
		return "", err
	}
	meta, err := im.o.Meta.Meta(ctx, d.TitleType, imdb)
	if err != nil {
		return "", fmt.Errorf("couldn't look up %s: %w", d.TitleName, err)
	}
	name := firstNonEmpty(meta.Name, d.TitleName)
	year := YearFrom(firstNonEmpty(meta.Year, meta.ReleaseInfo))
	ext := filepath.Ext(src)
	if d.TitleType == "movie" {
		return MoviePath(name, year, res, ext), nil
	}
	epTitle := ""
	for _, v := range meta.Videos {
		if v.Season == s && v.Episode == e {
			epTitle = firstNonEmpty(v.Title, v.Name)
			break
		}
	}
	return EpisodePath(name, year, s, e, epTitle, res, ext), nil
}

func splitID(typ, id string) (imdb string, season, episode int, err error) {
	parts := strings.Split(id, ":")
	imdb = parts[0]
	if typ == "series" {
		if len(parts) != 3 {
			return "", 0, 0, fmt.Errorf("episode id %q should look like tt0903747:1:2", id)
		}
		season, _ = strconv.Atoi(parts[1])
		episode, _ = strconv.Atoi(parts[2])
	}
	return imdb, season, episode, nil
}

// SubtitleResult says what happened per wanted language.
type SubtitleResult struct {
	Lang   string `json:"lang"`
	Status string `json:"status"` // embedded | present | saved | none | failed
	Path   string `json:"path,omitempty"`
	Source string `json:"source,omitempty"`
	Error  string `json:"error,omitempty"`
}

// Subtitles fetches missing subtitles for a media file, for every wanted
// language that isn't embedded in the video or already saved.
func (im *Importer) Subtitles(ctx context.Context, mediaID int64) ([]SubtitleResult, error) {
	mf, err := im.o.Store.GetMediaFile(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	langsWanted := LoadSettings(ctx, im.o.Store).SubtitleLangs
	if im.o.SubtitleLangsFor != nil {
		if l, ok := im.o.SubtitleLangsFor(ctx, mf.TitleID); ok {
			langsWanted = l
		}
	}
	wanted := normalizeLangs(langsWanted)
	results := []SubtitleResult{}
	var missing []string
	for _, w := range wanted {
		switch {
		case hasLang(mf.EmbeddedSubs, w):
			results = append(results, SubtitleResult{Lang: w, Status: "embedded"})
		case hasSubFile(mf, w):
			results = append(results, SubtitleResult{Lang: w, Status: "present"})
		default:
			missing = append(missing, w)
		}
	}
	if len(missing) == 0 {
		return results, nil
	}

	cands, err := im.offers(ctx, mf)
	if err != nil {
		return results, err
	}
	saved := []string{}
	for _, w := range missing {
		r := SubtitleResult{Lang: w, Status: "none"}
		tries := pick(w, cands)
		for i, c := range tries {
			if i >= 3 {
				break
			}
			srt, err := fetchSubtitle(ctx, im.o.HTTP, c.Sub.URL)
			if err != nil {
				r.Status, r.Error = "failed", err.Error()
				continue
			}
			rel := SubtitlePath(mf.Path, c.Lang)
			dst := filepath.Join(im.o.LibraryDir, filepath.FromSlash(rel))
			if err := writeFileAtomic(dst, srt); err != nil {
				r.Status, r.Error = "failed", err.Error()
				break
			}
			if err := im.o.Store.UpsertSubtitleFile(ctx, store.SubtitleFile{MediaFileID: mf.ID, Lang: c.Lang, Path: rel, Source: c.Supplier, SourceID: c.Sub.ID}); err != nil {
				return results, err
			}
			r = SubtitleResult{Lang: w, Status: "saved", Path: rel, Source: c.Supplier}
			saved = append(saved, lang.Name(c.Lang))
			break
		}
		results = append(results, r)
	}
	switch {
	case len(saved) > 0:
		im.activity(ctx, "info", "subtitles", mf.TitleID, fmt.Sprintf("Saved %s subtitles for %s", strings.Join(saved, ", "), mf.TitleName))
	case len(cands) == 0:
		im.activity(ctx, "warn", "subtitles", mf.TitleID, fmt.Sprintf("No subtitle suppliers had anything for %s", mf.TitleName))
	default:
		im.activity(ctx, "warn", "subtitles", mf.TitleID, fmt.Sprintf("No %s subtitles found for %s", langNames(missing), mf.TitleName))
	}
	return results, nil
}

// offers asks every enabled subtitle supplier, in priority order.
func (im *Importer) offers(ctx context.Context, mf *store.MediaFile) ([]candidate, error) {
	sups, err := im.o.Suppliers.Enabled(ctx, stremio.ResSubtitles)
	if err != nil {
		return nil, err
	}
	q := stremio.SubtitleQuery{VideoHash: mf.OSHash, VideoSize: mf.Size, Filename: filepath.Base(mf.Path)}
	var out []candidate
	for _, sp := range sups {
		if !sp.Manifest().Supports(stremio.ResSubtitles, mf.TitleType, mf.TitleID) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		subs, err := im.o.Client.Subtitles(cctx, sp.Base(), mf.TitleType, mf.TitleID, q)
		cancel()
		if err != nil {
			im.o.Log.Warn("subtitle supplier", "supplier", sp.Name, "err", err)
			continue
		}
		for _, s := range subs {
			if l := lang.Normalize(s.Lang); l != "" && s.URL != "" {
				out = append(out, candidate{Supplier: sp.Name, Sub: s, Lang: l})
			}
		}
	}
	return out, nil
}

func hasLang(list []string, w string) bool {
	for _, l := range list {
		if _, ok := matches(w, l); ok {
			return true
		}
	}
	return false
}

func hasSubFile(mf *store.MediaFile, w string) bool {
	for _, s := range mf.Subtitles {
		if _, ok := matches(w, s.Lang); ok {
			return true
		}
	}
	return false
}

func langNames(codes []string) string {
	names := make([]string, len(codes))
	for i, c := range codes {
		names[i] = lang.Name(c)
	}
	return strings.Join(names, ", ")
}

func writeFileAtomic(dst string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".reelyard-sub-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(tmp.Name(), 0o644)
	return os.Rename(tmp.Name(), dst)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func friendly(err error) string {
	switch {
	case errors.Is(err, ErrExists):
		return "a different file already has this name in the library; move or delete it and import again"
	case errors.Is(err, fs.ErrPermission):
		return "Reelyard isn't allowed to write to the library folder"
	}
	return err.Error()
}
