package api

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/importer"
	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/store"
)

// What Reelyard's apps need to watch things: subtitles as WebVTT, artwork,
// resume points, the home screen, and whether a file will play.

func (s *Server) routesWatch(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/subtitles/{id}", s.serveSubtitle) // signed link or signed in
	mux.HandleFunc("GET /api/v1/art/{imdb}/{kind}", s.serveArt)   // signed link
	mux.Handle("GET /api/v1/progress", s.authed(s.listProgress))
	mux.Handle("PUT /api/v1/progress/{item}", s.authed(s.saveProgress))
	mux.Handle("DELETE /api/v1/progress/{item}", s.authed(s.deleteProgress))
	mux.Handle("GET /api/v1/home", s.authed(s.home))
}

// ---- subtitles ----

// PlaySubtitle is a subtitle an app can load next to a play link.
type PlaySubtitle struct {
	ID   int64  `json:"id"`
	Lang string `json:"lang"`
	Name string `json:"name"`
	URL  string `json:"url"` // WebVTT, signed like the play link
}

func (s *Server) subtitleLinks(mf *store.MediaFile) []PlaySubtitle {
	out := []PlaySubtitle{}
	for _, sf := range mf.Subtitles {
		q, _ := s.links.SignLink(auth.LinkSubtitle, sf.ID)
		out = append(out, PlaySubtitle{ID: sf.ID, Lang: sf.Lang, Name: lang.Name(sf.Lang),
			URL: "/api/v1/subtitles/" + strconv.FormatInt(sf.ID, 10) + "?" + q})
	}
	return out
}

// serveSubtitle serves a saved subtitle as WebVTT.
func (s *Server) serveSubtitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("sig") != "" {
		if !s.links.ValidLink(auth.LinkSubtitle, id, q.Get("exp"), q.Get("sig")) {
			writeError(w, http.StatusForbidden, "this link has expired; ask for a new one")
			return
		}
	} else if _, err := s.Auth.Authenticate(r); err != nil {
		writeError(w, http.StatusUnauthorized, "sign in required")
		return
	}
	sf, err := s.Store.GetSubtitleFile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such subtitle")
		return
	}
	if err != nil {
		s.internal(w, "get subtitle", err)
		return
	}
	f, err := os.OpenInRoot(s.Config.LibraryDir, filepath.FromSlash(sf.Path))
	if err != nil {
		writeError(w, http.StatusGone, "this subtitle is missing from the library folder")
		return
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, 10<<20))
	if err != nil {
		writeError(w, http.StatusGone, "this subtitle can't be read")
		return
	}
	vtt, err := importer.ToVTT(b)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "this subtitle file isn't SRT or WebVTT")
		return
	}
	w.Header().Set("Content-Type", "text/vtt; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	_, _ = w.Write(vtt)
}

// ---- artwork ----

var artKinds = []string{"poster", "background", "logo"}

// artURLs returns signed links to a title's artwork, served by Reelyard so
// TVs don't fetch from other sites and images are cached locally.
func (s *Server) artURLs(imdb string) map[string]string {
	out := make(map[string]string, len(artKinds))
	for _, k := range artKinds {
		out[k] = "/api/v1/art/" + imdb + "/" + k + "?sig=" + s.links.ArtSig(imdb, k)
	}
	return out
}

const (
	artMaxBytes   = 8 << 20
	artMissingTTL = 24 * time.Hour // how long to remember that a title has no such image
)

var imdbID = regexp.MustCompile(`^tt[0-9]{1,12}$`)

// artLocks keeps two requests from fetching the same image at once.
var artLocks sync.Map

func (s *Server) serveArt(w http.ResponseWriter, r *http.Request) {
	imdb, kind := r.PathValue("imdb"), r.PathValue("kind")
	if !imdbID.MatchString(imdb) || !slices.Contains(artKinds, kind) {
		writeError(w, http.StatusNotFound, "no such artwork")
		return
	}
	if !s.links.ArtValid(imdb, kind, r.URL.Query().Get("sig")) {
		writeError(w, http.StatusForbidden, "bad artwork link")
		return
	}
	dir := filepath.Join(s.Config.DataDir, "cache", "art")
	file := filepath.Join(dir, imdb+"."+kind)
	missing := file + ".missing"

	mu, _ := artLocks.LoadOrStore(file, &sync.Mutex{})
	mu.(*sync.Mutex).Lock()
	b, err := os.ReadFile(file)
	if err != nil {
		if st, serr := os.Stat(missing); serr == nil && time.Since(st.ModTime()) < artMissingTTL {
			mu.(*sync.Mutex).Unlock()
			writeError(w, http.StatusNotFound, "this title has no "+kind)
			return
		}
		b, err = s.fetchArt(r.Context(), imdb, kind)
		switch {
		case err == nil:
			if os.MkdirAll(dir, 0o755) == nil {
				_ = os.WriteFile(file+".tmp", b, 0o644)
				_ = os.Rename(file+".tmp", file)
				_ = os.Remove(missing)
			}
		case errors.Is(err, errNoArt):
			if os.MkdirAll(dir, 0o755) == nil {
				_ = os.WriteFile(missing, nil, 0o644)
			}
		}
	}
	mu.(*sync.Mutex).Unlock()
	if errors.Is(err, errNoArt) {
		writeError(w, http.StatusNotFound, "this title has no "+kind)
		return
	}
	if err != nil {
		s.Log.Warn("fetch artwork", "title", imdb, "kind", kind, "err", err)
		writeError(w, http.StatusBadGateway, "couldn't fetch this artwork; try again later")
		return
	}
	w.Header().Set("Content-Type", http.DetectContentType(b))
	w.Header().Set("Cache-Control", "public, max-age=604800")
	http.ServeContent(w, r, "", time.Time{}, bytes.NewReader(b))
}

var errNoArt = errors.New("no such artwork")

// fetchArt downloads a title's artwork from the address its metadata gives.
func (s *Server) fetchArt(ctx context.Context, imdb, kind string) ([]byte, error) {
	if s.Meta == nil {
		return nil, errors.New("metadata is unavailable")
	}
	typ := "movie"
	if files, err := s.Store.ListMediaFiles(ctx, imdb); err == nil && len(files) > 0 {
		typ = files[0].TitleType
	} else if t, err := s.Store.TitleByIMDb(ctx, imdb); err == nil {
		typ = t.Type
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	m, err := s.Meta.Meta(ctx, typ, imdb)
	if err != nil {
		return nil, err
	}
	src := map[string]string{"poster": m.Poster, "background": m.Background, "logo": m.Logo}[kind]
	u, err := url.Parse(src)
	if src == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errNoArt
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, errNoArt
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, artMaxBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > artMaxBytes || !strings.HasPrefix(http.DetectContentType(b), "image/") {
		return nil, errNoArt
	}
	return b, nil
}

// ---- resume points ----

var itemIDPattern = regexp.MustCompile(`^tt[0-9]{1,12}(:[0-9]{1,4}:[0-9]{1,5})?$`)

func (s *Server) listProgress(w http.ResponseWriter, r *http.Request) {
	title := r.URL.Query().Get("title")
	if title != "" && !imdbID.MatchString(title) {
		writeError(w, http.StatusBadRequest, "title must be an IMDb id")
		return
	}
	ps, err := s.Store.ListProgress(r.Context(), auth.UserFrom(r.Context()).ID, title, 0)
	if err != nil {
		s.internal(w, "list progress", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"progress": ps})
}

func (s *Server) saveProgress(w http.ResponseWriter, r *http.Request) {
	item := r.PathValue("item")
	if !itemIDPattern.MatchString(item) {
		writeError(w, http.StatusBadRequest, "item must be an IMDb id, or tt…:season:episode")
		return
	}
	var body struct {
		FileID      *int64 `json:"fileId"`
		PositionSec int    `json:"positionSec"`
		DurationSec int    `json:"durationSec"`
		Watched     *bool  `json:"watched"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.PositionSec < 0 || body.DurationSec < 0 {
		writeError(w, http.StatusBadRequest, "positions can't be negative")
		return
	}
	p := store.Progress{ItemID: item, MediaFileID: body.FileID, PositionSec: body.PositionSec, DurationSec: body.DurationSec}
	if p.MediaFileID != nil {
		mf, err := s.Store.GetMediaFile(r.Context(), *p.MediaFileID)
		if err != nil || mf.TitleID != item {
			writeError(w, http.StatusBadRequest, "fileId isn't a library file of this item")
			return
		}
		if p.DurationSec == 0 {
			p.DurationSec = mf.DurationSec
		}
	}
	switch {
	case body.Watched != nil:
		p.Watched = *body.Watched
	case p.DurationSec > 0:
		p.Watched = float64(p.PositionSec) >= store.WatchedAt*float64(p.DurationSec)
	}
	if p.Watched {
		p.PositionSec = 0 // start from the beginning next time
	}
	saved, err := s.Store.SaveProgress(r.Context(), auth.UserFrom(r.Context()).ID, p)
	if err != nil {
		s.internal(w, "save progress", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"progress": saved})
}

func (s *Server) deleteProgress(w http.ResponseWriter, r *http.Request) {
	item := r.PathValue("item")
	if !itemIDPattern.MatchString(item) {
		writeError(w, http.StatusBadRequest, "item must be an IMDb id, or tt…:season:episode")
		return
	}
	if err := s.Store.DeleteProgress(r.Context(), auth.UserFrom(r.Context()).ID, item); err != nil {
		s.internal(w, "delete progress", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- home ----

// HomeItem is a film or episode to offer on the home screen.
type HomeItem struct {
	Title    *LibraryTitle    `json:"title"`
	ItemID   string           `json:"itemId"`
	Season   int              `json:"season,omitempty"`
	Episode  int              `json:"episode,omitempty"`
	Name     string           `json:"name,omitempty"` // the episode's name
	File     *store.MediaFile `json:"file"`
	Progress *store.Progress  `json:"progress,omitempty"`
}

const homeRowLen = 20

// home lists what to watch: films and episodes in progress, the next
// episode of series being watched, and what arrived lately.
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	uid := auth.UserFrom(ctx).ID
	files, err := s.Store.ListMediaFiles(ctx, "")
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	tracked, err := s.trackedByIMDb(r)
	if err != nil {
		s.internal(w, "list titles", err)
		return
	}
	progress, err := s.Store.ListProgress(ctx, uid, "", 500)
	if err != nil {
		s.internal(w, "list progress", err)
		return
	}
	titles := groupLibrary(files, tracked)
	byIMDb := map[string]*LibraryTitle{}
	for _, t := range titles {
		t.Art = s.artURLs(t.IMDbID)
		byIMDb[t.IMDbID] = t
	}
	// The newest file of each film or episode (ListMediaFiles sorts by id desc within an item).
	fileOf := map[string]*store.MediaFile{}
	episodes := map[string][]string{} // series → its episode ids in the library, in order
	for _, f := range files {
		if fileOf[f.TitleID] == nil {
			fileOf[f.TitleID] = f
			if imdb, _, _, ok := splitItemID(f.TitleID); ok {
				episodes[imdb] = append(episodes[imdb], f.TitleID)
			}
		}
	}
	for _, eps := range episodes {
		sort.Slice(eps, func(i, j int) bool { return episodeBefore(eps[i], eps[j]) })
	}
	progressOf := map[string]*store.Progress{}
	for _, p := range progress {
		progressOf[p.ItemID] = p
	}

	names := episodeNames{s: s, ctx: ctx, tracked: tracked}
	item := func(id string, p *store.Progress) HomeItem {
		imdb, se, ep, _ := splitItemID(id)
		return HomeItem{Title: byIMDb[imdb], ItemID: id, Season: se, Episode: ep, Name: names.of(id), File: fileOf[id], Progress: p}
	}

	cont, next := []HomeItem{}, []HomeItem{}
	seen := map[string]bool{} // titles already on a row
	for _, p := range progress {
		imdb, _, _, isEp := splitItemID(p.ItemID)
		if seen[imdb] || byIMDb[imdb] == nil {
			continue
		}
		seen[imdb] = true // only a title's most recent item counts
		switch {
		case !p.Watched && p.PositionSec > 0 && fileOf[p.ItemID] != nil:
			if len(cont) < homeRowLen {
				cont = append(cont, item(p.ItemID, p))
			}
		case p.Watched && isEp:
			eps := episodes[imdb]
			i := slices.Index(eps, p.ItemID)
			if i < 0 { // the watched episode's file is gone: find where it would be
				i = sort.Search(len(eps), func(k int) bool { return episodeBefore(p.ItemID, eps[k]) }) - 1
			}
			for _, id := range eps[i+1:] {
				if q := progressOf[id]; q == nil || !q.Watched {
					if len(next) < homeRowLen {
						next = append(next, item(id, progressOf[id]))
					}
					break
				}
			}
		}
	}
	recent := titles
	if len(recent) > homeRowLen {
		recent = recent[:homeRowLen]
	}
	writeJSON(w, http.StatusOK, map[string]any{"continueWatching": cont, "nextUp": next, "recentlyAdded": recent})
}

// episodeBefore orders "tt…:S:E" ids by season then episode, specials last.
func episodeBefore(a, b string) bool {
	_, sa, ea, _ := splitItemID(a)
	_, sb, eb, _ := splitItemID(b)
	if (sa == 0) != (sb == 0) {
		return sb == 0
	}
	if sa != sb {
		return sa < sb
	}
	return ea < eb
}

// episodeNames looks up episode names from tracked titles, once per title.
type episodeNames struct {
	s       *Server
	ctx     context.Context
	tracked map[string]store.Title
	cache   map[string]map[string]string
}

func (n *episodeNames) of(itemID string) string {
	imdb, _, _, isEp := splitItemID(itemID)
	if !isEp {
		return ""
	}
	if n.cache == nil {
		n.cache = map[string]map[string]string{}
	}
	m, ok := n.cache[imdb]
	if !ok {
		m = map[string]string{}
		if t, tracked := n.tracked[imdb]; tracked {
			if items, err := n.s.Store.ItemsOfTitle(n.ctx, t.ID); err == nil {
				for _, it := range items {
					m[it.ItemID] = it.Name
				}
			}
		}
		n.cache[imdb] = m
	}
	return m[itemID]
}

// ---- will it play? ----

// Supports is what an app's player can play, in ffprobe's names
// (containers "mp4", "mkv", "webm"…; codecs "h264", "hevc", "aac", "eac3"…).
// A list left out isn't checked.
type Supports struct {
	Containers  []string `json:"containers"`
	VideoCodecs []string `json:"videoCodecs"`
	AudioCodecs []string `json:"audioCodecs"`
}

var containers = map[string]string{
	".mp4": "mp4", ".m4v": "mp4", ".mkv": "mkv", ".webm": "webm", ".mov": "mov", ".avi": "avi",
	".ts": "ts", ".m2ts": "ts", ".wmv": "wmv", ".mpg": "mpeg", ".mpeg": "mpeg",
}

func containerOf(path string) string {
	return containers[strings.ToLower(filepath.Ext(path))]
}

// playability says whether a player that supports sup can play mf: true,
// false (with the reasons), or nil when Reelyard can't tell.
func playability(mf *store.MediaFile, sup *Supports) (*bool, []string) {
	problems := []string{}
	if sup == nil {
		return nil, problems
	}
	unknown := false
	has := func(list []string, v string) bool {
		return slices.ContainsFunc(list, func(x string) bool { return strings.EqualFold(x, v) })
	}
	check := func(list []string, v, what string) {
		switch {
		case list == nil:
		case v == "":
			unknown = true
		case !has(list, v):
			problems = append(problems, fmt.Sprintf("this player can't play %s %s", what, v))
		}
	}
	check(sup.Containers, containerOf(mf.Path), "the container")
	check(sup.VideoCodecs, mf.VideoCodec, "video in")
	if sup.AudioCodecs != nil {
		switch {
		case mf.AudioCodecs == nil:
			unknown = true
		case len(mf.AudioCodecs) > 0 && !has(sup.AudioCodecs, mf.AudioCodecs[0]):
			// Players start on the first audio track.
			problems = append(problems, "this player can't play audio in "+mf.AudioCodecs[0])
		}
	}
	var ok bool
	switch {
	case len(problems) > 0:
		ok = false
	case unknown:
		return nil, problems
	default:
		ok = true
	}
	return &ok, problems
}

// probeIfNeeded checks files imported before Reelyard kept audio codecs,
// once, when ffprobe is installed.
func (s *Server) probeIfNeeded(ctx context.Context, mf *store.MediaFile) {
	if mf.AudioCodecs != nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	pr, ok, err := importer.ProbeFile(ctx, s.libraryPath(mf))
	if !ok || err != nil {
		return
	}
	if err := s.Store.SetMediaProbe(ctx, mf.ID, pr.VideoCodec, pr.AudioCodecs, pr.DurationSec); err != nil {
		s.Log.Warn("save probe", "file", mf.ID, "err", err)
		return
	}
	mf.AudioCodecs = pr.AudioCodecs
	if pr.VideoCodec != "" {
		mf.VideoCodec = pr.VideoCodec
	}
	if pr.DurationSec > 0 {
		mf.DurationSec = pr.DurationSec
	}
}
