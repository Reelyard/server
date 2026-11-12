package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/profile"
	"github.com/reelyard/server/internal/search"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

func (s *Server) routesMedia(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/suppliers", s.authed(s.listSuppliers))
	mux.Handle("GET /api/v1/suppliers/catalog", s.authed(s.supplierCatalog))
	mux.Handle("POST /api/v1/suppliers", s.authed(s.addSupplier))
	mux.Handle("PATCH /api/v1/suppliers/{id}", s.authed(s.patchSupplier))
	mux.Handle("DELETE /api/v1/suppliers/{id}", s.authed(s.deleteSupplier))
	mux.Handle("POST /api/v1/suppliers/{id}/refresh", s.authed(s.refreshSupplier))
	mux.Handle("POST /api/v1/suppliers/{id}/test", s.authed(s.testSupplier))
	mux.Handle("GET /api/v1/metadata/search", s.authed(s.metadataSearch))
	mux.Handle("GET /api/v1/metadata/{type}/{id}", s.authed(s.metadataGet))
	mux.Handle("GET /api/v1/streams/{type}/{id}", s.authed(s.streams))
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "bad id")
		return 0, false
	}
	return id, true
}

func (s *Server) supplierErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such supplier")
	default:
		s.internal(w, what, err)
	}
}

func (s *Server) listSuppliers(w http.ResponseWriter, r *http.Request) {
	list, err := s.Suppliers.List(r.Context())
	if err != nil {
		s.internal(w, "list suppliers", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"suppliers": list})
}

func (s *Server) supplierCatalog(w http.ResponseWriter, r *http.Request) {
	list, err := s.Suppliers.Catalog(r.Context())
	if err != nil {
		s.Log.Warn("addon catalog", "err", err)
		writeError(w, http.StatusBadGateway, "Stremio's addon catalog isn't answering right now. Try again in a moment.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"addons": list})
}

func (s *Server) addSupplier(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if !decode(w, r, &body) {
		return
	}
	sp, err := s.Suppliers.Add(r.Context(), body.URL)
	switch {
	case errors.Is(err, stremio.ErrBadURL):
		writeError(w, http.StatusBadRequest, "That doesn't look like an addon URL. Paste the link that ends in /manifest.json.")
	case errors.Is(err, suppliers.ErrDuplicate):
		writeError(w, http.StatusConflict, "This addon is already in your suppliers.")
	case err != nil:
		// Manifest fetch problems are the user's to fix, not server faults.
		writeError(w, http.StatusBadGateway, capitalize(err.Error()))
	default:
		s.Log.Info("supplier added", "name", sp.Name, "url", sp.DisplayURL)
		writeJSON(w, http.StatusCreated, map[string]any{"supplier": sp})
	}
}

func (s *Server) patchSupplier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Name     *string `json:"name"`
		Enabled  *bool   `json:"enabled"`
		Priority *int    `json:"priority"`
		Language *string `json:"language"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.Name != nil {
		n := strings.TrimSpace(*body.Name)
		if n == "" || len(n) > 100 {
			writeError(w, http.StatusBadRequest, "name must be 1–100 characters")
			return
		}
		body.Name = &n
	}
	if body.Language != nil && *body.Language != "" {
		l := lang.Normalize(*body.Language)
		if l == "" {
			writeError(w, http.StatusBadRequest, "unknown language")
			return
		}
		body.Language = &l
	}
	if _, err := s.Store.UpdateSupplier(r.Context(), id, store.SupplierPatch{Name: body.Name, Enabled: body.Enabled, Priority: body.Priority, Language: body.Language}); err != nil {
		s.supplierErr(w, "update supplier", err)
		return
	}
	sp, err := s.Suppliers.Get(r.Context(), id)
	if err != nil {
		s.supplierErr(w, "get supplier", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": sp})
}

func (s *Server) deleteSupplier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Store.DeleteSupplier(r.Context(), id); err != nil {
		s.supplierErr(w, "delete supplier", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) refreshSupplier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sp, err := s.Suppliers.Refresh(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		s.supplierErr(w, "refresh", err)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadGateway, capitalize(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"supplier": sp})
}

func (s *Server) testSupplier(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	res, err := s.Suppliers.Test(r.Context(), id)
	if err != nil {
		s.supplierErr(w, "test supplier", err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) metadataSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if len(strings.TrimSpace(q)) < 2 {
		writeError(w, http.StatusBadRequest, "type at least 2 characters")
		return
	}
	res, err := s.Meta.Search(r.Context(), q, r.URL.Query().Get("type"))
	if err != nil {
		s.Log.Warn("metadata search", "err", err)
		writeError(w, http.StatusBadGateway, "The title search service isn't answering right now. Try again in a moment.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res})
}

func validType(t string) bool { return t == "movie" || t == "series" }

func (s *Server) metadataGet(w http.ResponseWriter, r *http.Request) {
	typ, id := r.PathValue("type"), r.PathValue("id")
	if !validType(typ) || !strings.HasPrefix(id, "tt") {
		writeError(w, http.StatusBadRequest, "want /metadata/{movie|series}/{imdb id}")
		return
	}
	m, err := s.Meta.Meta(r.Context(), typ, id)
	var he *stremio.HTTPError
	if errors.As(err, &he) && he.Status == http.StatusNotFound {
		writeError(w, http.StatusNotFound, "title not found")
		return
	}
	if err != nil {
		s.Log.Warn("metadata get", "err", err)
		writeError(w, http.StatusBadGateway, "The title information service isn't answering right now. Try again in a moment.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"meta": m})
}

func (s *Server) streams(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	if !validType(typ) {
		writeError(w, http.StatusBadRequest, "type must be movie or series")
		return
	}
	id := r.PathValue("id")
	res, err := s.Search.Streams(r.Context(), typ, id)
	if err != nil {
		if strings.Contains(err.Error(), "id") || strings.Contains(err.Error(), "type") {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.internal(w, "stream search", err)
		return
	}
	// With ?profile=ID, judge every candidate against that profile and
	// order them as the tracker would.
	if pid, perr := strconv.ParseInt(r.URL.Query().Get("profile"), 10, 64); perr == nil && pid > 0 {
		prof, err := s.Store.GetProfile(r.Context(), pid)
		if err != nil {
			writeError(w, http.StatusBadRequest, "no such profile")
			return
		}
		_, season, episode, _ := search.ParseID(typ, id)
		blocked, _ := s.Store.BlockedFor(r.Context(), id)
		ranked := profile.Rank(prof, res.Candidates, profile.Context{Episode: typ == "series", Season: season, Number: episode, Blocked: blocked})
		writeJSON(w, http.StatusOK, map[string]any{
			"type": res.Type, "id": res.ID, "season": res.Season, "episode": res.Episode,
			"candidates": ranked, "suppliers": res.Suppliers, "profile": prof.Name, "summary": profile.Summary(ranked),
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
