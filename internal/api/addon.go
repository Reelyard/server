package api

// Reelyard as a Stremio-style addon, for Nuvio (and Stremio) on TVs and
// phones. Each person (the signed-in user or a viewer) gets their own install
// link, /addon/<key>/manifest.json. The key in the path is the credential,
// because these apps can't send headers. Through it the app lists the library
// as catalogs and gets signed links to play the files; viewers only see the
// titles they're allowed.

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/store"
)

// addonKeysSetting holds each person's addon key, by user id.
const addonKeysSetting = "addon_keys"

const addonPage = 100 // catalog items per page

var addonCatalogs = []struct{ typ, id, name string }{
	{"movie", "reelyard-movies", "Reelyard films"},
	{"series", "reelyard-series", "Reelyard series"},
}

func (s *Server) routesAddon(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/addon", s.authed(s.listAddonLinks))
	mux.Handle("POST /api/v1/addon/{id}", s.authed(s.newAddonLink))
	mux.Handle("DELETE /api/v1/addon/{id}", s.authed(s.deleteAddonLink))

	mux.HandleFunc("GET /addon/{key}/manifest.json", s.addon(s.addonManifest))
	mux.HandleFunc("GET /addon/{key}/catalog/{type}/{rest...}", s.addon(s.addonCatalog))
	mux.HandleFunc("GET /addon/{key}/stream/{type}/{id}", s.addon(s.addonStreams))
	mux.HandleFunc("OPTIONS /addon/{key}/{rest...}", func(w http.ResponseWriter, r *http.Request) {
		addonHeaders(w)
		w.WriteHeader(http.StatusNoContent)
	})
}

// ---- keys ----

func (s *Server) addonKeys(ctx context.Context) (map[string]string, error) {
	keys := map[string]string{}
	if err := s.Store.GetSetting(ctx, addonKeysSetting, &keys); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return keys, nil
}

// addonUser returns whose key this is.
func (s *Server) addonUser(ctx context.Context, key string) (int64, bool) {
	if len(key) < 16 {
		return 0, false
	}
	keys, err := s.addonKeys(ctx)
	if err != nil {
		return 0, false
	}
	for id, k := range keys {
		if subtle.ConstantTimeCompare([]byte(k), []byte(key)) == 1 {
			n, err := strconv.ParseInt(id, 10, 64)
			return n, err == nil
		}
	}
	return 0, false
}

func addonHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Headers", "*")
	h.Set("Cache-Control", "no-store")
}

// addonViewer is who an addon request is for.
type addonViewer struct {
	userID int64
	name   string // empty for the owner
	all    bool
	titles []string
}

func (v *addonViewer) allowed(itemID string) bool {
	return v.all || store.TitleAllowed(v.titles, itemID)
}

// addon checks the key in the path and passes on who it belongs to.
func (s *Server) addon(h func(http.ResponseWriter, *http.Request, *addonViewer)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		addonHeaders(w)
		id, ok := s.addonUser(r.Context(), r.PathValue("key"))
		if !ok {
			writeError(w, http.StatusNotFound, "unknown addon link; copy a new one from Reelyard's settings")
			return
		}
		v := &addonViewer{userID: id, all: true}
		vw, err := s.Store.GetViewer(r.Context(), id)
		switch {
		case err == nil:
			v.name, v.all, v.titles = vw.Name, vw.AllTitles, vw.Titles
		case errors.Is(err, store.ErrNotFound): // an account, not a viewer: everything
		default:
			s.internal(w, "get viewer", err)
			return
		}
		h(w, r, v)
	}
}

// baseURL is how the app reached Reelyard, for absolute links.
func baseURL(r *http.Request) string {
	scheme := "http"
	if isHTTPS(r) {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}

// ---- managing links ----

// AddonLink is one person's install link; URL is empty when they have none.
type AddonLink struct {
	UserID int64  `json:"userId"`
	Name   string `json:"name"`
	Viewer bool   `json:"viewer"`
	URL    string `json:"url"`
}

func (s *Server) listAddonLinks(w http.ResponseWriter, r *http.Request) {
	keys, err := s.addonKeys(r.Context())
	if err != nil {
		s.internal(w, "addon keys", err)
		return
	}
	vs, err := s.Store.ListViewers(r.Context())
	if err != nil {
		s.internal(w, "list viewers", err)
		return
	}
	me := auth.UserFrom(r.Context())
	link := func(id int64) string {
		if k := keys[strconv.FormatInt(id, 10)]; k != "" {
			return baseURL(r) + "/addon/" + k + "/manifest.json"
		}
		return ""
	}
	out := []AddonLink{{UserID: me.ID, Name: me.Username, URL: link(me.ID)}}
	for _, v := range vs {
		out = append(out, AddonLink{UserID: v.ID, Name: v.Name, Viewer: true, URL: link(v.ID)})
	}
	writeJSON(w, http.StatusOK, map[string]any{"links": out})
}

// addonTarget checks that a link may be made for this user: the signed-in
// account or a viewer.
func (s *Server) addonTarget(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return 0, false
	}
	if id == auth.UserFrom(r.Context()).ID {
		return id, true
	}
	if _, err := s.Store.GetViewer(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no such viewer")
		} else {
			s.internal(w, "get viewer", err)
		}
		return 0, false
	}
	return id, true
}

// newAddonLink makes (or replaces) a person's link; the old one stops working.
func (s *Server) newAddonLink(w http.ResponseWriter, r *http.Request) {
	id, ok := s.addonTarget(w, r)
	if !ok {
		return
	}
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		s.internal(w, "addon key", err)
		return
	}
	key := base64.RawURLEncoding.EncodeToString(b)
	keys, err := s.addonKeys(r.Context())
	if err != nil {
		s.internal(w, "addon keys", err)
		return
	}
	keys[strconv.FormatInt(id, 10)] = key
	if err := s.Store.SetSetting(r.Context(), addonKeysSetting, keys); err != nil {
		s.internal(w, "save addon keys", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"url": baseURL(r) + "/addon/" + key + "/manifest.json"})
}

func (s *Server) deleteAddonLink(w http.ResponseWriter, r *http.Request) {
	id, ok := s.addonTarget(w, r)
	if !ok {
		return
	}
	keys, err := s.addonKeys(r.Context())
	if err != nil {
		s.internal(w, "addon keys", err)
		return
	}
	delete(keys, strconv.FormatInt(id, 10))
	if err := s.Store.SetSetting(r.Context(), addonKeysSetting, keys); err != nil {
		s.internal(w, "save addon keys", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- the addon ----

func (s *Server) addonManifest(w http.ResponseWriter, r *http.Request, v *addonViewer) {
	id, name := "org.reelyard.library", "Reelyard"
	if v.name != "" {
		id += "." + strconv.FormatInt(v.userID, 10)
		name += " · " + v.name
	}
	catalogs := []map[string]any{}
	for _, c := range addonCatalogs {
		catalogs = append(catalogs, map[string]any{
			"type": c.typ, "id": c.id, "name": c.name,
			"extra": []map[string]any{{"name": "search"}, {"name": "skip"}},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":          id,
		"version":     "1.0.0",
		"name":        name,
		"description": "Films and series in your Reelyard library, played from your server.",
		"resources":   []any{"catalog", map[string]any{"name": "stream", "types": []string{"movie", "series"}, "idPrefixes": []string{"tt"}}},
		"types":       []string{"movie", "series"},
		"idPrefixes":  []string{"tt"},
		"catalogs":    catalogs,
		"behaviorHints": map[string]any{
			"configurable": false,
		},
	})
}

// addonExtra reads a catalog request's "search=…&skip=…" path segment.
func addonExtra(rest string) (catalog string, search string, skip int) {
	rest = strings.TrimSuffix(rest, ".json")
	catalog, extra, _ := strings.Cut(rest, "/")
	for _, kv := range strings.Split(extra, "&") {
		k, val, _ := strings.Cut(kv, "=")
		switch k {
		case "search":
			search = strings.ToLower(strings.TrimSpace(val))
		case "skip":
			skip, _ = strconv.Atoi(val)
		}
	}
	return catalog, search, max(skip, 0)
}

func (s *Server) addonCatalog(w http.ResponseWriter, r *http.Request, v *addonViewer) {
	typ := r.PathValue("type")
	catalog, search, skip := addonExtra(r.PathValue("rest"))
	known := false
	for _, c := range addonCatalogs {
		known = known || (c.typ == typ && c.id == catalog)
	}
	if !known {
		writeJSON(w, http.StatusOK, map[string]any{"metas": []any{}})
		return
	}
	files, err := s.Store.ListMediaFiles(r.Context(), "")
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	mine := files[:0]
	for _, f := range files {
		if v.allowed(f.TitleID) {
			mine = append(mine, f)
		}
	}
	tracked, err := s.trackedByIMDb(r)
	if err != nil {
		s.internal(w, "list titles", err)
		return
	}
	metas := []map[string]any{}
	for _, t := range groupLibrary(mine, tracked) {
		if t.Type != typ || !strings.HasPrefix(t.IMDbID, "tt") {
			continue
		}
		if search != "" && !strings.Contains(strings.ToLower(t.Name), search) {
			continue
		}
		m := map[string]any{
			"id": t.IMDbID, "type": t.Type, "name": t.Name, "posterShape": "poster",
			"poster":     baseURL(r) + s.artURLs(t.IMDbID)["poster"],
			"background": baseURL(r) + s.artURLs(t.IMDbID)["background"],
		}
		if t.Year > 0 {
			m["releaseInfo"] = strconv.Itoa(t.Year)
		}
		metas = append(metas, m)
	}
	if skip >= len(metas) {
		metas = metas[:0]
	} else {
		metas = metas[skip:min(len(metas), skip+addonPage)]
	}
	writeJSON(w, http.StatusOK, map[string]any{"metas": metas})
}

func (s *Server) addonStreams(w http.ResponseWriter, r *http.Request, v *addonViewer) {
	itemID := strings.TrimSuffix(r.PathValue("id"), ".json")
	imdb, _, _, isEp := splitItemID(itemID)
	streams := []map[string]any{}
	if !strings.HasPrefix(imdb, "tt") || (r.PathValue("type") == "series" && !isEp) || !v.allowed(itemID) {
		writeJSON(w, http.StatusOK, map[string]any{"streams": streams})
		return
	}
	files, err := s.Store.ListMediaFiles(r.Context(), imdb)
	if err != nil {
		s.internal(w, "list library", err)
		return
	}
	var mine []*store.MediaFile
	for _, f := range files {
		if f.TitleID == itemID {
			mine = append(mine, f)
		}
	}
	// Best first: higher resolution, then bigger.
	sort.SliceStable(mine, func(i, j int) bool {
		ri, rj := resRank(mine[i].Resolution), resRank(mine[j].Resolution)
		if ri != rj {
			return ri > rj
		}
		return mine[i].Size > mine[j].Size
	})
	base := baseURL(r)
	for _, f := range mine {
		q, _ := s.links.Sign(f.ID)
		label := "Reelyard"
		if f.Resolution != "" {
			label += "\n" + f.Resolution
		}
		desc := []string{path.Base(f.Path)}
		info := []string{humanSize(f.Size)}
		if f.VideoCodec != "" {
			info = append(info, f.VideoCodec)
		}
		if len(f.AudioLangs) > 0 {
			info = append(info, strings.Join(f.AudioLangs, "/"))
		}
		desc = append(desc, strings.Join(info, " · "))
		subs := []map[string]string{}
		for _, sub := range s.subtitleLinks(f) {
			subs = append(subs, map[string]string{"id": strconv.FormatInt(sub.ID, 10), "url": base + sub.URL, "lang": sub.Lang})
		}
		ct := contentType(f.Path)
		streams = append(streams, map[string]any{
			"url":         base + "/api/v1/play/" + strconv.FormatInt(f.ID, 10) + "?" + q,
			"name":        label,
			"description": strings.Join(desc, "\n"),
			"subtitles":   subs,
			"behaviorHints": map[string]any{
				"notWebReady": ct != "video/mp4" && ct != "video/webm",
				"bingeGroup":  "reelyard-" + strings.ToLower(f.Resolution),
				"filename":    path.Base(f.Path),
				"videoSize":   f.Size,
			},
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"streams": streams})
}

func resRank(res string) int {
	n, _ := strconv.Atoi(strings.TrimSuffix(strings.ToLower(res), "p"))
	if strings.EqualFold(res, "4k") || strings.EqualFold(res, "2160p") {
		return 2160
	}
	return n
}

func humanSize(n int64) string {
	const gb, mb = 1 << 30, 1 << 20
	if n >= gb {
		return strconv.FormatFloat(float64(n)/gb, 'f', 1, 64) + " GB"
	}
	return strconv.FormatInt(n/mb, 10) + " MB"
}
