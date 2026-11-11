package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/reelyard/server/internal/profile"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/tracker"
)

func (s *Server) routesTracking(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/profiles", s.authed(s.listProfiles))
	mux.Handle("POST /api/v1/profiles", s.authed(s.saveProfile))
	mux.Handle("PUT /api/v1/profiles/{id}", s.authed(s.saveProfile))
	mux.Handle("DELETE /api/v1/profiles/{id}", s.authed(s.deleteProfile))

	mux.Handle("GET /api/v1/titles", s.authed(s.listTitles))
	mux.Handle("POST /api/v1/titles", s.authed(s.trackTitle))
	mux.Handle("GET /api/v1/titles/by-imdb/{imdb}", s.authed(s.titleByIMDb))
	mux.Handle("GET /api/v1/titles/{id}", s.authed(s.getTitle))
	mux.Handle("PATCH /api/v1/titles/{id}", s.authed(s.updateTitle))
	mux.Handle("DELETE /api/v1/titles/{id}", s.authed(s.untrackTitle))
	mux.Handle("POST /api/v1/titles/{id}/search", s.authed(s.searchTitle))
	mux.Handle("POST /api/v1/items/{id}/search", s.authed(s.searchItem))
	mux.Handle("PATCH /api/v1/items/{id}", s.authed(s.patchItem))

	mux.Handle("GET /api/v1/blocklist", s.authed(s.listBlocklist))
	mux.Handle("DELETE /api/v1/blocklist", s.authed(s.unblock))
}

// ---- profiles ----

func (s *Server) listProfiles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListProfiles(r.Context())
	if err != nil {
		s.internal(w, "list profiles", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": list, "resolutions": profile.Resolutions})
}

func (s *Server) saveProfile(w http.ResponseWriter, r *http.Request) {
	var p store.Profile
	if !decode(w, r, &p) {
		return
	}
	p.ID = 0
	if r.PathValue("id") != "" {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		p.ID = id
	}
	if err := profile.Clean(&p); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.Store.SaveProfile(r.Context(), &p)
	switch {
	case errors.Is(err, store.ErrDuplicate):
		writeError(w, http.StatusConflict, "A profile with this name already exists.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such profile")
	case err != nil:
		s.internal(w, "save profile", err)
	default:
		code := http.StatusOK
		if p.ID == 0 {
			code = http.StatusCreated
		}
		writeJSON(w, code, map[string]any{"profile": saved})
	}
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	switch err := s.Store.DeleteProfile(r.Context(), id); {
	case errors.Is(err, store.ErrInUse):
		writeError(w, http.StatusConflict, "Titles still use this profile. Move them to another profile first.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such profile")
	case err != nil:
		s.internal(w, "delete profile", err)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

// ---- titles ----

func (s *Server) listTitles(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListTitles(r.Context())
	if err != nil {
		s.internal(w, "list titles", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"titles": list})
}

func (s *Server) titleDetail(w http.ResponseWriter, r *http.Request, t *store.Title) {
	items, err := s.Store.ItemsOfTitle(r.Context(), t.ID)
	if err != nil {
		s.internal(w, "items", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"title": t, "items": items})
}

func (s *Server) getTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	t, err := s.Store.GetTitle(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not tracked")
		return
	}
	if err != nil {
		s.internal(w, "get title", err)
		return
	}
	s.titleDetail(w, r, t)
}

func (s *Server) titleByIMDb(w http.ResponseWriter, r *http.Request) {
	t, err := s.Store.TitleByIMDb(r.Context(), r.PathValue("imdb"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not tracked")
		return
	}
	if err != nil {
		s.internal(w, "title by imdb", err)
		return
	}
	s.titleDetail(w, r, t)
}

type titleBody struct {
	IMDbID    string `json:"imdbId"`
	Type      string `json:"type"`
	ProfileID int64  `json:"profileId"`
	Monitor   string `json:"monitor"`
	Seasons   []int  `json:"seasons"`
}

func (s *Server) trackTitle(w http.ResponseWriter, r *http.Request) {
	var b titleBody
	if !decode(w, r, &b) {
		return
	}
	if !strings.HasPrefix(b.IMDbID, "tt") {
		writeError(w, http.StatusBadRequest, "imdbId must be an IMDb id")
		return
	}
	t, err := s.Tracker.Track(r.Context(), tracker.TrackRequest{IMDbID: b.IMDbID, Type: b.Type, ProfileID: b.ProfileID, Monitor: b.Monitor, Seasons: b.Seasons})
	switch {
	case errors.Is(err, tracker.ErrTracked):
		writeError(w, http.StatusConflict, "This title is already tracked.")
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusBadRequest, "Pick an existing quality profile.")
	case err != nil:
		writeError(w, http.StatusBadRequest, capitalize(err.Error()))
	default:
		s.titleDetailCreated(w, r, t)
	}
}

func (s *Server) titleDetailCreated(w http.ResponseWriter, r *http.Request, t *store.Title) {
	items, _ := s.Store.ItemsOfTitle(r.Context(), t.ID)
	writeJSON(w, http.StatusCreated, map[string]any{"title": t, "items": items})
}

func (s *Server) updateTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b titleBody
	if !decode(w, r, &b) {
		return
	}
	t, err := s.Tracker.Update(r.Context(), id, b.ProfileID, b.Monitor, b.Seasons)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "not tracked, or no such profile")
	case err != nil:
		writeError(w, http.StatusBadRequest, capitalize(err.Error()))
	default:
		s.titleDetail(w, r, t)
	}
}

func (s *Server) untrackTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteTitle(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not tracked")
			return
		}
		s.internal(w, "untrack", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) searchTitle(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Tracker.SearchNow(r.Context(), id, 0); err != nil {
		s.internal(w, "search title", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) searchItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	it, err := s.Store.GetItem(r.Context(), id)
	if err != nil {
		writeError(w, http.StatusNotFound, "no such item")
		return
	}
	if it.State != store.ItemMissing {
		writeError(w, http.StatusConflict, "Only missing items can be searched.")
		return
	}
	if err := s.Tracker.SearchNow(r.Context(), 0, id); err != nil {
		s.internal(w, "search item", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) patchItem(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var b struct {
		Monitored *bool `json:"monitored"`
	}
	if !decode(w, r, &b) {
		return
	}
	if b.Monitored == nil {
		writeError(w, http.StatusBadRequest, "nothing to change")
		return
	}
	if _, err := s.Store.GetItem(r.Context(), id); err != nil {
		writeError(w, http.StatusNotFound, "no such item")
		return
	}
	if err := s.Store.SetMonitored(r.Context(), id, *b.Monitored); err != nil {
		s.internal(w, "monitor item", err)
		return
	}
	if *b.Monitored {
		_ = s.Tracker.SearchNow(r.Context(), 0, id)
	}
	it, _ := s.Store.GetItem(r.Context(), id)
	writeJSON(w, http.StatusOK, map[string]any{"item": it})
}

// ---- blocklist ----

func (s *Server) listBlocklist(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListBlocklist(r.Context())
	if err != nil {
		s.internal(w, "blocklist", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocklist": list})
}

func (s *Server) unblock(w http.ResponseWriter, r *http.Request) {
	var b struct {
		InfoHash string `json:"infoHash"`
		FileIdx  int    `json:"fileIdx"`
		ItemID   string `json:"itemId"`
	}
	if !decode(w, r, &b) {
		return
	}
	if err := s.Store.Unblock(r.Context(), strings.ToLower(b.InfoHash), b.FileIdx, b.ItemID); err != nil {
		s.internal(w, "unblock", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
