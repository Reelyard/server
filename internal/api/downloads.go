package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/inluxc/harbor/internal/engine"
)

func (s *Server) routesDownloads(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/downloads", s.authed(s.listDownloads))
	mux.Handle("POST /api/v1/downloads", s.authed(s.addDownload))
	mux.Handle("POST /api/v1/downloads/{id}/pause", s.authed(s.pauseDownload))
	mux.Handle("POST /api/v1/downloads/{id}/resume", s.authed(s.resumeDownload))
	mux.Handle("DELETE /api/v1/downloads/{id}", s.authed(s.removeDownload))
	mux.Handle("GET /api/v1/settings/downloads", s.authed(s.getDownloadSettings))
	mux.Handle("PUT /api/v1/settings/downloads", s.authed(s.putDownloadSettings))
	mux.Handle("GET /api/v1/stream", s.authed(s.stream))
}

func (s *Server) downloadErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		writeError(w, http.StatusNotFound, "no such download")
	case errors.Is(err, engine.ErrDuplicate):
		writeError(w, http.StatusConflict, "This file is already in your downloads.")
	case errors.Is(err, engine.ErrBadInfoHash):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		s.internal(w, what, err)
	}
}

func (s *Server) listDownloads(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"downloads": s.Engine.List(), "port": s.Engine.Port(), "network": s.Engine.Network(), "free": s.freeBytes()})
}

func (s *Server) addDownload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InfoHash  string   `json:"infoHash"`
		FileIdx   *int     `json:"fileIdx"`
		Sources   []string `json:"sources"`
		Name      string   `json:"name"`
		TitleType string   `json:"titleType"`
		TitleID   string   `json:"titleId"`
		TitleName string   `json:"titleName"`
	}
	if !decode(w, r, &body) {
		return
	}
	if body.TitleType != "" && !validType(body.TitleType) {
		writeError(w, http.StatusBadRequest, "titleType must be movie or series")
		return
	}
	st, err := s.Engine.Add(r.Context(), engine.AddRequest{
		InfoHash: body.InfoHash, FileIdx: body.FileIdx, Sources: body.Sources, Name: body.Name,
		TitleType: body.TitleType, TitleID: body.TitleID, TitleName: body.TitleName,
	})
	if err != nil {
		s.downloadErr(w, "add download", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"download": st})
}

func (s *Server) pauseDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	st, err := s.Engine.Pause(r.Context(), id)
	if err != nil {
		s.downloadErr(w, "pause", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"download": st})
}

func (s *Server) resumeDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	st, err := s.Engine.Resume(r.Context(), id)
	if err != nil {
		s.downloadErr(w, "resume", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"download": st})
}

func (s *Server) removeDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	deleteData := r.URL.Query().Get("deleteData") == "true"
	if err := s.Engine.Remove(r.Context(), id, deleteData); err != nil {
		s.downloadErr(w, "remove", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) getDownloadSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.Engine.Settings(), "port": s.Engine.Port(), "network": s.Engine.Network(), "free": s.freeBytes()})
}

func (s *Server) freeBytes() int64 {
	n, err := s.Engine.FreeBytes()
	if err != nil {
		return -1
	}
	return int64(n)
}

func (s *Server) putDownloadSettings(w http.ResponseWriter, r *http.Request) {
	var body engine.Settings
	if !decode(w, r, &body) {
		return
	}
	if err := s.Engine.SetSettings(r.Context(), body); err != nil {
		if body.Validate() != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		s.internal(w, "save download settings", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": s.Engine.Settings(), "port": s.Engine.Port(), "network": s.Engine.Network(), "free": s.freeBytes()})
}

// stream sends live events as Server-Sent Events.
func (s *Server) stream(w http.ResponseWriter, r *http.Request) {
	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no") // nginx: don't buffer
	w.WriteHeader(http.StatusOK)

	send := func(typ string, data any) error {
		b, err := json.Marshal(data)
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", typ, b); err != nil {
			return err
		}
		return rc.Flush()
	}
	// Current state first, so the page doesn't wait for the next tick.
	if err := send("downloads", s.Engine.List()); err != nil {
		return
	}
	events, stop := s.Bus.Subscribe()
	defer stop()
	ping := time.NewTicker(25 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-s.closing:
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := send(ev.Type, ev.Data); err != nil {
				return
			}
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil || rc.Flush() != nil {
				return
			}
		}
	}
}
