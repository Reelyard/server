package api

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/reelyard/server/internal/dlna"
	"github.com/reelyard/server/internal/store"
)

// Viewers are family members who watch on TVs: their own watched marks and
// resume points, optionally limited titles, and their own DLNA server.

func (s *Server) routesViewers(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/viewers", s.authed(s.listViewers))
	mux.Handle("POST /api/v1/viewers", s.authed(s.saveViewer))
	mux.Handle("PUT /api/v1/viewers/{id}", s.authed(s.saveViewer))
	mux.Handle("DELETE /api/v1/viewers/{id}", s.authed(s.deleteViewer))
	mux.Handle("PUT /api/v1/viewers/{id}/titles/{title}", s.authed(s.viewerTitle(true)))
	mux.Handle("DELETE /api/v1/viewers/{id}/titles/{title}", s.authed(s.viewerTitle(false)))
	mux.Handle("GET /api/v1/dlna/tvs", s.authed(s.listTVs))
	mux.Handle("PUT /api/v1/dlna/tvs/{address}", s.authed(s.nameTV))
	mux.Handle("DELETE /api/v1/dlna/tvs/{address}", s.authed(s.forgetTV))
}

func (s *Server) listViewers(w http.ResponseWriter, r *http.Request) {
	vs, err := s.Store.ListViewers(r.Context())
	if err != nil {
		s.internal(w, "list viewers", err)
		return
	}
	writeJSON(w, http.StatusOK, vs)
}

type viewerBody struct {
	Name      string   `json:"name"`
	AllTitles bool     `json:"allTitles"`
	Titles    []string `json:"titles"`
	DLNA      bool     `json:"dlna"`
	TVs       []string `json:"tvs"`
}

func (b *viewerBody) validate() error {
	b.Name = strings.TrimSpace(b.Name)
	switch {
	case b.Name == "":
		return errors.New("give the viewer a name")
	case utf8.RuneCountInString(b.Name) > 32:
		return errors.New("the name can be at most 32 characters")
	case !b.AllTitles && len(b.Titles) > 5000:
		return errors.New("too many titles")
	}
	var titles []string
	for _, t := range b.Titles {
		t = strings.TrimSpace(t)
		if t != "" && !slices.Contains(titles, t) {
			titles = append(titles, t)
		}
	}
	b.Titles = titles
	if b.AllTitles {
		b.Titles = nil
	}
	b.TVs = dlna.CleanTVs(b.TVs)
	return nil
}

func (s *Server) saveViewer(w http.ResponseWriter, r *http.Request) {
	var b viewerBody
	if !decode(w, r, &b) {
		return
	}
	if err := b.validate(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := &store.Viewer{Name: b.Name, AllTitles: b.AllTitles, Titles: b.Titles, DLNA: b.DLNA, TVs: b.TVs}
	var err error
	if r.PathValue("id") == "" {
		err = s.Store.CreateViewer(r.Context(), v)
	} else {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		v.ID = id
		err = s.Store.UpdateViewer(r.Context(), v)
	}
	switch {
	case errors.Is(err, store.ErrNameTaken):
		writeError(w, http.StatusConflict, err.Error())
		return
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such viewer")
		return
	case err != nil:
		s.internal(w, "save viewer", err)
		return
	}
	saved, err := s.Store.GetViewer(r.Context(), v.ID)
	if err != nil {
		s.internal(w, "read viewer", err)
		return
	}
	if s.DLNA != nil {
		s.DLNA.ViewersChanged()
	}
	code := http.StatusOK
	if r.PathValue("id") == "" {
		code = http.StatusCreated
	}
	writeJSON(w, code, saved)
}

func (s *Server) deleteViewer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteViewer(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such viewer")
			return
		}
		s.internal(w, "delete viewer", err)
		return
	}
	if s.DLNA != nil {
		s.DLNA.ViewersChanged()
	}
	w.WriteHeader(http.StatusNoContent)
}

// viewerTitle lets a viewer see one title (a film or series id), or stops
// them, and answers with the viewer.
func (s *Server) viewerTitle(on bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		title := r.PathValue("title")
		if !strings.HasPrefix(title, "tt") || strings.Contains(title, ":") {
			writeError(w, http.StatusBadRequest, "title must be a film or series IMDb id")
			return
		}
		if err := s.Store.SetViewerTitle(r.Context(), id, title, on); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				writeError(w, http.StatusNotFound, "no such viewer")
				return
			}
			s.internal(w, "viewer title", err)
			return
		}
		v, err := s.Store.GetViewer(r.Context(), id)
		if err != nil {
			s.internal(w, "read viewer", err)
			return
		}
		if s.DLNA != nil {
			s.DLNA.ViewersChanged()
		}
		writeJSON(w, http.StatusOK, v)
	}
}

func (s *Server) listTVs(w http.ResponseWriter, r *http.Request) {
	tvs := []dlna.TV{}
	if s.DLNA != nil {
		tvs = s.DLNA.TVs()
	}
	writeJSON(w, http.StatusOK, tvs)
}

func (s *Server) nameTV(w http.ResponseWriter, r *http.Request) {
	var b struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &b) {
		return
	}
	if s.DLNA == nil || !s.DLNA.NameTV(r.PathValue("address"), b.Name) {
		writeError(w, http.StatusNotFound, "no such TV")
		return
	}
	writeJSON(w, http.StatusOK, s.DLNA.TVs())
}

func (s *Server) forgetTV(w http.ResponseWriter, r *http.Request) {
	if s.DLNA == nil || !s.DLNA.ForgetTV(r.PathValue("address")) {
		writeError(w, http.StatusNotFound, "no such TV")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
