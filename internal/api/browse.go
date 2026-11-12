package api

// Browse: the catalogs of the suppliers that offer them (Nuvio's catalog
// addon, for instance: Trending, Popular, Netflix…), page by page.

import (
	"errors"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

const browseCacheFor = 15 * time.Minute

// BrowseCatalog is a catalog Reelyard can show.
type BrowseCatalog struct {
	SupplierID int64    `json:"supplierId"`
	Supplier   string   `json:"supplier"`
	Type       string   `json:"type"`
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Genres     []string `json:"genres"`
	// GenreRequired means a genre must be picked (the first is used by default).
	GenreRequired bool `json:"genreRequired"`
}

type browseEntry struct {
	at    time.Time
	metas []stremio.MetaPreview
}

// browseCache keeps catalog pages for a while, so paging back and forth
// doesn't ask the addon again.
type browseCache struct {
	mu sync.Mutex
	m  map[string]browseEntry
}

func (c *browseCache) get(k string) ([]stremio.MetaPreview, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[k]
	if !ok || time.Since(e.at) > browseCacheFor {
		return nil, false
	}
	return e.metas, true
}

func (c *browseCache) put(k string, metas []stremio.MetaPreview) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil || len(c.m) > 500 {
		c.m = map[string]browseEntry{}
	}
	c.m[k] = browseEntry{time.Now(), metas}
}

func (s *Server) routesBrowse(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/browse", s.authed(s.listBrowse))
	mux.Handle("GET /api/v1/browse/{supplier}/{type}/{catalog}", s.authed(s.browseCatalog))
}

func browseCatalogs(sp *suppliers.Supplier) []BrowseCatalog {
	out := []BrowseCatalog{}
	m := sp.Manifest()
	if m == nil {
		return out
	}
	for _, c := range m.Catalogs {
		if !suppliers.Browsable(c) {
			continue
		}
		bc := BrowseCatalog{SupplierID: sp.ID, Supplier: sp.Name, Type: c.Type, ID: c.ID, Name: c.Name, Genres: []string{}}
		if bc.Name == "" {
			bc.Name = c.ID
		}
		for _, e := range c.Extra {
			if e.Name == "genre" && len(e.Options) > 0 {
				bc.Genres, bc.GenreRequired = e.Options, e.IsRequired
			}
		}
		out = append(out, bc)
	}
	return out
}

func (s *Server) listBrowse(w http.ResponseWriter, r *http.Request) {
	sups, err := s.Suppliers.Enabled(r.Context(), stremio.ResCatalog)
	if err != nil {
		s.internal(w, "list suppliers", err)
		return
	}
	out := []BrowseCatalog{}
	for _, sp := range sups {
		out = append(out, browseCatalogs(sp)...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"catalogs": out})
}

func (s *Server) browseCatalog(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("supplier"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad supplier id")
		return
	}
	sp, err := s.Suppliers.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such supplier")
		return
	}
	if err != nil {
		s.internal(w, "get supplier", err)
		return
	}
	typ, catID := r.PathValue("type"), r.PathValue("catalog")
	var cat *BrowseCatalog
	for _, c := range browseCatalogs(sp) {
		if c.Type == typ && c.ID == catID {
			cat = &c
			break
		}
	}
	if cat == nil || !sp.Enabled {
		writeError(w, http.StatusNotFound, "this supplier has no such catalog")
		return
	}
	q := r.URL.Query()
	var extra [][2]string
	genre := q.Get("genre")
	if genre == "" && cat.GenreRequired {
		genre = cat.Genres[0]
	}
	if genre != "" {
		extra = append(extra, [2]string{"genre", genre})
	}
	if skip, _ := strconv.Atoi(q.Get("skip")); skip > 0 {
		extra = append(extra, [2]string{"skip", strconv.Itoa(skip)})
	}
	key := strconv.FormatInt(id, 10) + "|" + typ + "|" + catID + "|" + genre + "|" + q.Get("skip")
	metas, ok := s.browse.get(key)
	if !ok {
		metas, err = s.Suppliers.Client.Catalog(r.Context(), sp.Base(), typ, catID, extra)
		if err != nil {
			s.Log.Warn("browse catalog", "supplier", sp.Name, "catalog", catID, "err", err)
			writeError(w, http.StatusBadGateway, sp.Name+" isn't answering right now. Try again in a moment.")
			return
		}
		s.browse.put(key, metas)
	}
	if metas == nil {
		metas = []stremio.MetaPreview{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"metas": metas})
}
