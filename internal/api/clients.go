package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/store"
)

// Endpoints for Reelyard's apps (TVs, phones, desktop): pairing, playback, and
// the library by title. See the Reelyard/clients repository.

// Public pairing endpoints use no cookie, so cross-site requests to them are
// harmless; the TV apps call them from their own origin.
var pairingRoutes = []string{"POST /api/v1/devices/pair", "POST /api/v1/devices/pair/token"}

func (s *Server) routesClients(mux *http.ServeMux) {
	mux.HandleFunc(pairingRoutes[0], s.startPairing)
	mux.HandleFunc(pairingRoutes[1], s.collectDeviceToken)
	mux.Handle("GET /api/v1/devices", s.authed(s.listDevices))
	mux.Handle("POST /api/v1/devices/approve", s.authed(s.approveDevice))
	mux.Handle("DELETE /api/v1/devices/{id}", s.authed(s.deleteDevice))

	mux.Handle("POST /api/v1/library/{id}/play", s.authed(s.playLink))
	mux.HandleFunc("GET /api/v1/play/{id}", s.serveLibraryFile) // signed link or signed in
	mux.Handle("GET /api/v1/library/titles", s.authed(s.libraryTitles))
	mux.Handle("GET /api/v1/library/titles/{imdb}", s.authed(s.libraryTitle))
}

// ---- CORS ----

// cors lets apps on other origins (Tizen and webOS web apps, the dev loop)
// call the API with a header credential. It never allows credentials, so a
// browser won't send Reelyard's session cookie on these requests.
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == "" || !strings.HasPrefix(r.URL.Path, "/api/") {
			next.ServeHTTP(w, r)
			return
		}
		h := w.Header()
		h.Set("Access-Control-Allow-Origin", "*")
		h.Set("Access-Control-Expose-Headers", "Content-Range, Accept-Ranges, Content-Length, Retry-After")
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Range, X-Api-Key")
			h.Set("Access-Control-Max-Age", "600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ---- devices ----

func (s *Server) startPairing(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name     string `json:"name"`
		Platform string `json:"platform"`
	}
	if !decode(w, r, &body) {
		return
	}
	p, err := s.Auth.StartPairing(r.Context(), body.Name, body.Platform, remoteIP(r))
	switch {
	case errors.Is(err, auth.ErrBadDeviceName):
		writeError(w, http.StatusBadRequest, err.Error())
		return
	case errors.Is(err, auth.ErrTooManyPairings):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		s.internal(w, "start pairing", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"code":       p.Code,
		"pollSecret": p.PollSecret,
		"expiresIn":  int(p.ExpiresIn.Seconds()),
		"interval":   int(p.Interval.Seconds()),
	})
}

func (s *Server) collectDeviceToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		PollSecret string `json:"pollSecret"`
	}
	if !decode(w, r, &body) {
		return
	}
	tok, d, err := s.Auth.CollectToken(r.Context(), body.PollSecret)
	switch {
	case errors.Is(err, store.ErrPending):
		w.Header().Set("Retry-After", strconv.Itoa(int(auth.PairingPollInterval.Seconds())))
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "this pairing expired, was turned down, or was already used; start again")
		return
	case err != nil:
		s.internal(w, "collect device token", err)
		return
	}
	s.Log.Info("device paired", "device", d.Name, "platform", d.Platform)
	writeJSON(w, http.StatusOK, map[string]any{"token": tok, "device": d})
}

func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	ds, err := s.Store.ListDevices(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list devices", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"devices": ds})
}

func (s *Server) approveDevice(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &body) {
		return
	}
	d, err := s.Auth.ApprovePairing(r.Context(), body.Code, auth.UserFrom(r.Context()).ID)
	if errors.Is(err, auth.ErrUnknownCode) {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if err != nil {
		s.internal(w, "approve device", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"device": d})
}

func (s *Server) deleteDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	err := s.Store.DeleteDevice(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such device")
		return
	}
	if err != nil {
		s.internal(w, "delete device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- playback ----

var videoTypes = map[string]string{
	".mp4": "video/mp4", ".m4v": "video/mp4", ".mkv": "video/x-matroska", ".webm": "video/webm",
	".avi": "video/x-msvideo", ".mov": "video/quicktime", ".ts": "video/mp2t", ".m2ts": "video/mp2t",
	".wmv": "video/x-ms-wmv", ".mpg": "video/mpeg", ".mpeg": "video/mpeg",
}

// playLink returns a signed link a player can open without headers.
func (s *Server) playLink(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	mf, err := s.Store.GetMediaFile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such library file")
		return
	}
	if err != nil {
		s.internal(w, "get media file", err)
		return
	}
	if _, err := os.Stat(s.libraryPath(mf)); err != nil {
		writeError(w, http.StatusGone, "this file is missing from the library folder")
		return
	}
	q, exp := s.links.Sign(id)
	writeJSON(w, http.StatusOK, map[string]any{
		"url":         "/api/v1/play/" + strconv.FormatInt(id, 10) + "?" + q,
		"expiresAt":   exp.UTC(),
		"contentType": contentType(mf.Path),
		"file":        mf,
	})
}

func contentType(path string) string {
	if t, ok := videoTypes[strings.ToLower(filepath.Ext(path))]; ok {
		return t
	}
	return "application/octet-stream"
}

func (s *Server) libraryPath(mf *store.MediaFile) string {
	return filepath.Join(s.Config.LibraryDir, filepath.FromSlash(mf.Path))
}

// serveLibraryFile serves a library file with byte ranges, to a signed link or a
// signed-in request.
func (s *Server) serveLibraryFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	q := r.URL.Query()
	if q.Get("sig") != "" {
		if !s.links.Valid(id, q.Get("exp"), q.Get("sig")) {
			writeError(w, http.StatusForbidden, "this link has expired; ask for a new one")
			return
		}
	} else if _, err := s.Auth.Authenticate(r); err != nil {
		writeError(w, http.StatusUnauthorized, "sign in required")
		return
	}
	mf, err := s.Store.GetMediaFile(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such library file")
		return
	}
	if err != nil {
		s.internal(w, "get media file", err)
		return
	}
	// Opened inside the library folder: a stored path can't reach outside it.
	f, err := os.OpenInRoot(s.Config.LibraryDir, filepath.FromSlash(mf.Path))
	if err != nil {
		writeError(w, http.StatusGone, "this file is missing from the library folder")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil || st.IsDir() {
		writeError(w, http.StatusGone, "this file is missing from the library folder")
		return
	}
	w.Header().Set("Content-Type", contentType(mf.Path))
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// ---- library by title ----

// LibraryTitle is a movie or series with files in the library.
type LibraryTitle struct {
	IMDbID         string    `json:"imdbId"`
	Type           string    `json:"type"`
	Name           string    `json:"name"`
	Year           int       `json:"year,omitempty"`
	Poster         string    `json:"poster,omitempty"`
	Tracked        bool      `json:"tracked"`
	Files          int       `json:"files"`
	Episodes       int       `json:"episodes,omitempty"`
	Seasons        []int     `json:"seasons,omitempty"`
	LastImportedAt time.Time `json:"lastImportedAt"`
}

// LibraryEpisode is one episode with its files.
type LibraryEpisode struct {
	Season  int                `json:"season"`
	Episode int                `json:"episode"`
	ItemID  string             `json:"itemId"`
	Name    string             `json:"name,omitempty"`
	AirDate string             `json:"airDate,omitempty"`
	Files   []*store.MediaFile `json:"files"`
}

// LibrarySeason groups episodes.
type LibrarySeason struct {
	Season   int               `json:"season"`
	Episodes []*LibraryEpisode `json:"episodes"`
}

// splitItemID turns "tt123:1:2" into ("tt123", 1, 2, true).
func splitItemID(id string) (string, int, int, bool) {
	parts := strings.Split(id, ":")
	if len(parts) != 3 {
		return id, 0, 0, false
	}
	se, err1 := strconv.Atoi(parts[1])
	ep, err2 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil {
		return parts[0], 0, 0, false
	}
	return parts[0], se, ep, true
}

// groupLibrary builds titles from files, newest import first.
func groupLibrary(files []*store.MediaFile, tracked map[string]store.Title) []*LibraryTitle {
	by := map[string]*LibraryTitle{}
	eps := map[string]map[string]bool{}
	seasons := map[string]map[int]bool{}
	for _, f := range files {
		imdb, se, _, isEp := splitItemID(f.TitleID)
		t := by[imdb]
		if t == nil {
			t = &LibraryTitle{IMDbID: imdb, Type: f.TitleType, Name: f.TitleName}
			if tt, ok := tracked[imdb]; ok {
				t.Name, t.Year, t.Poster, t.Type, t.Tracked = tt.Name, tt.Year, tt.Poster, tt.Type, true
			}
			by[imdb] = t
			eps[imdb], seasons[imdb] = map[string]bool{}, map[int]bool{}
		}
		t.Files++
		if f.ImportedAt.After(t.LastImportedAt) {
			t.LastImportedAt = f.ImportedAt
		}
		if isEp {
			eps[imdb][f.TitleID] = true
			seasons[imdb][se] = true
		}
	}
	out := make([]*LibraryTitle, 0, len(by))
	for imdb, t := range by {
		t.Episodes = len(eps[imdb])
		for se := range seasons[imdb] {
			t.Seasons = append(t.Seasons, se)
		}
		sort.Ints(t.Seasons)
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].LastImportedAt.Equal(out[j].LastImportedAt) {
			return out[i].LastImportedAt.After(out[j].LastImportedAt)
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *Server) trackedByIMDb(r *http.Request) (map[string]store.Title, error) {
	ts, err := s.Store.ListTitles(r.Context())
	if err != nil {
		return nil, err
	}
	m := make(map[string]store.Title, len(ts))
	for _, t := range ts {
		m[t.IMDbID] = t
	}
	return m, nil
}

func (s *Server) libraryTitles(w http.ResponseWriter, r *http.Request) {
	files, err := s.Store.ListMediaFiles(r.Context(), "")
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	tracked, err := s.trackedByIMDb(r)
	if err != nil {
		s.internal(w, "list titles", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": groupLibrary(files, tracked)})
}

func (s *Server) libraryTitle(w http.ResponseWriter, r *http.Request) {
	imdb := r.PathValue("imdb")
	if !strings.HasPrefix(imdb, "tt") || strings.Contains(imdb, ":") {
		writeError(w, http.StatusBadRequest, "title must be an IMDb id")
		return
	}
	files, err := s.Store.ListMediaFiles(r.Context(), imdb)
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	if len(files) == 0 {
		writeError(w, http.StatusNotFound, "nothing from this title is in the library")
		return
	}
	tracked, err := s.trackedByIMDb(r)
	if err != nil {
		s.internal(w, "list titles", err)
		return
	}
	title := groupLibrary(files, tracked)[0]

	// Episode names and air dates come from the tracked title, when there is one.
	names := map[string]store.Item{}
	if tt, ok := tracked[imdb]; ok {
		items, err := s.Store.ItemsOfTitle(r.Context(), tt.ID)
		if err != nil {
			s.internal(w, "list items", err)
			return
		}
		for _, it := range items {
			names[it.ItemID] = it
		}
	}
	movieFiles := []*store.MediaFile{}
	bySeason := map[int]*LibrarySeason{}
	byEp := map[string]*LibraryEpisode{}
	for _, f := range files {
		_, se, ep, isEp := splitItemID(f.TitleID)
		if !isEp {
			movieFiles = append(movieFiles, f)
			continue
		}
		e := byEp[f.TitleID]
		if e == nil {
			it := names[f.TitleID]
			e = &LibraryEpisode{Season: se, Episode: ep, ItemID: f.TitleID, Name: it.Name, AirDate: it.AirDate}
			byEp[f.TitleID] = e
			if bySeason[se] == nil {
				bySeason[se] = &LibrarySeason{Season: se}
			}
			bySeason[se].Episodes = append(bySeason[se].Episodes, e)
		}
		e.Files = append(e.Files, f)
	}
	seasons := make([]*LibrarySeason, 0, len(bySeason))
	for _, sn := range bySeason {
		sort.Slice(sn.Episodes, func(i, j int) bool { return sn.Episodes[i].Episode < sn.Episodes[j].Episode })
		seasons = append(seasons, sn)
	}
	sort.Slice(seasons, func(i, j int) bool { return seasons[i].Season < seasons[j].Season })
	writeJSON(w, http.StatusOK, map[string]any{"title": title, "files": movieFiles, "seasons": seasons})
}
