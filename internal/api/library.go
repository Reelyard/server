package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/reelyard/server/internal/importer"
	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/store"
)

func (s *Server) routesLibrary(mux *http.ServeMux) {
	mux.Handle("POST /api/v1/downloads/{id}/import", s.authed(s.importDownload))
	mux.Handle("GET /api/v1/library", s.authed(s.listLibrary))
	mux.Handle("POST /api/v1/library/{id}/subtitles", s.authed(s.fetchSubtitles))
	mux.Handle("GET /api/v1/activity", s.authed(s.listActivity))
	mux.Handle("GET /api/v1/settings/library", s.authed(s.getLibrarySettings))
	mux.Handle("PUT /api/v1/settings/library", s.authed(s.putLibrarySettings))
	mux.Handle("GET /api/v1/languages", s.authed(s.languages))
}

func (s *Server) importDownload(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	d, err := s.Engine.Get(id)
	if err != nil {
		s.downloadErr(w, "import", err)
		return
	}
	if d.State != store.StateSeeding && d.State != store.StatePaused || (d.Size > 0 && d.DoneBytes < d.Size) {
		writeError(w, http.StatusConflict, "This download isn't finished yet.")
		return
	}
	_ = s.Engine.SetImport(r.Context(), id, store.ImportImporting, "")
	s.Importer.Enqueue(id)
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true})
}

func (s *Server) listLibrary(w http.ResponseWriter, r *http.Request) {
	title := strings.TrimSpace(r.URL.Query().Get("title"))
	if title != "" && !strings.HasPrefix(title, "tt") {
		writeError(w, http.StatusBadRequest, "title must be an IMDb id")
		return
	}
	files, err := s.Store.ListMediaFiles(r.Context(), title)
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "libraryDir": s.Config.LibraryDir})
}

func (s *Server) fetchSubtitles(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	res, err := s.Importer.Subtitles(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such library file")
		return
	}
	if err != nil {
		s.internal(w, "fetch subtitles", err)
		return
	}
	mf, err := s.Store.GetMediaFile(r.Context(), id)
	if err != nil {
		s.internal(w, "get media file", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": res, "file": mf})
}

func (s *Server) listActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	list, err := s.Store.ListActivity(r.Context(), limit)
	if err != nil {
		s.internal(w, "list activity", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"activity": list})
}

func (s *Server) getLibrarySettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"settings": importer.LoadSettings(r.Context(), s.Store), "libraryDir": s.Config.LibraryDir,
	})
}

func (s *Server) putLibrarySettings(w http.ResponseWriter, r *http.Request) {
	var body importer.Settings
	if !decode(w, r, &body) {
		return
	}
	saved, err := importer.SaveSettings(r.Context(), s.Store, body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"settings": saved, "libraryDir": s.Config.LibraryDir})
}

func (s *Server) languages(w http.ResponseWriter, r *http.Request) {
	type item struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	out := []item{}
	for _, c := range lang.Known() {
		out = append(out, item{c, lang.Name(c)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"languages": out})
}
