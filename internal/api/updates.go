package api

import (
	"errors"
	"net/http"

	"github.com/reelyard/server/internal/update"
)

func (s *Server) routesUpdates(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/updates", s.authed(s.getUpdates))
	mux.Handle("POST /api/v1/updates/check", s.authed(s.checkUpdates))
	mux.Handle("POST /api/v1/updates/apply", s.authed(s.applyUpdate))
	mux.Handle("PUT /api/v1/settings/updates", s.authed(s.putUpdateSettings))
}

func (s *Server) noUpdates(w http.ResponseWriter) bool {
	if s.Updates == nil {
		writeError(w, http.StatusNotFound, "updates aren't available")
		return true
	}
	return false
}

func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	writeJSON(w, http.StatusOK, s.Updates.Status(r.Context()))
}

func (s *Server) checkUpdates(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	_ = s.Updates.Check(r.Context()) // the error is in the status
	writeJSON(w, http.StatusOK, s.Updates.Status(r.Context()))
}

func (s *Server) applyUpdate(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	if err := s.Updates.Apply(r.Context()); err != nil {
		code := http.StatusConflict
		if errors.Is(err, update.ErrNothing) {
			code = http.StatusBadRequest
		}
		writeError(w, code, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, s.Updates.Status(r.Context()))
}

func (s *Server) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if s.noUpdates(w) {
		return
	}
	var body update.Settings
	if !decode(w, r, &body) {
		return
	}
	if err := s.Updates.SaveSettings(r.Context(), body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, s.Updates.Status(r.Context()))
}
