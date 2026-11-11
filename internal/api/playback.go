package api

import (
	"errors"
	"net/http"

	"github.com/reelyard/server/internal/dlna"
	"github.com/reelyard/server/internal/mediaserver"
)

func (s *Server) routesPlayback(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/settings/dlna", s.authed(s.getDLNA))
	mux.Handle("PUT /api/v1/settings/dlna", s.authed(s.putDLNA))
	mux.Handle("GET /api/v1/mediaservers", s.authed(s.listMediaServers))
	mux.Handle("POST /api/v1/mediaservers", s.authed(s.saveMediaServer))
	mux.Handle("PUT /api/v1/mediaservers/{id}", s.authed(s.saveMediaServer))
	mux.Handle("DELETE /api/v1/mediaservers/{id}", s.authed(s.deleteMediaServer))
	mux.Handle("POST /api/v1/mediaservers/test", s.authed(s.testMediaServer))
	mux.Handle("POST /api/v1/mediaservers/refresh", s.authed(s.refreshMediaServers))
}

func (s *Server) dlnaState() map[string]any {
	return map[string]any{
		"settings":   s.DLNA.Settings(),
		"status":     s.DLNA.Status(),
		"interfaces": s.DLNA.Interfaces(),
	}
}

func (s *Server) getDLNA(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.dlnaState())
}

func (s *Server) putDLNA(w http.ResponseWriter, r *http.Request) {
	var body dlna.Settings
	if !decode(w, r, &body) {
		return
	}
	if _, err := s.DLNA.SetSettings(r.Context(), body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.dlnaState())
}

func (s *Server) listMediaServers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.MediaServers.List())
}

func (s *Server) saveMediaServer(w http.ResponseWriter, r *http.Request) {
	var body mediaserver.Target
	if !decode(w, r, &body) {
		return
	}
	body.ID = r.PathValue("id")
	v, err := s.MediaServers.Save(r.Context(), body)
	switch {
	case errors.Is(err, mediaserver.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
		return
	case err != nil:
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) deleteMediaServer(w http.ResponseWriter, r *http.Request) {
	if err := s.MediaServers.Delete(r.Context(), r.PathValue("id")); err != nil {
		if errors.Is(err, mediaserver.ErrNotFound) {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		s.internal(w, "delete media server", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// testMediaServer checks an address and token (or a saved server's token).
func (s *Server) testMediaServer(w http.ResponseWriter, r *http.Request) {
	var body mediaserver.Target
	if !decode(w, r, &body) {
		return
	}
	if err := s.MediaServers.Test(r.Context(), body); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) refreshMediaServers(w http.ResponseWriter, r *http.Request) {
	s.MediaServers.RefreshAll(r.Context())
	writeJSON(w, http.StatusOK, s.MediaServers.List())
}
