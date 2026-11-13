package api

// Settings export and import: a JSON file with Reelyard's configuration, to
// move it to another server or keep a copy. It holds the settings pages,
// suppliers, quality profiles, viewers, media servers and tracked titles.
// It never holds passwords, sessions, API keys, paired devices, addon links,
// or this server's own id and signing secret. Supplier links and media
// server tokens can carry keys, so the file is to be kept private.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/reelyard/server/internal/auth"
	"github.com/reelyard/server/internal/dlna"
	"github.com/reelyard/server/internal/engine"
	"github.com/reelyard/server/internal/importer"
	"github.com/reelyard/server/internal/mediaserver"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/tracker"
	"github.com/reelyard/server/internal/update"
	"github.com/reelyard/server/internal/version"
)

const (
	exportKind    = "reelyard-settings"
	exportVersion = 1
	importLimit   = 32 << 20
)

// Sections of an export.
var exportSections = []string{"settings", "suppliers", "profiles", "mediaServers", "viewers", "titles"}

// Export is the settings file.
type Export struct {
	Kind       string    `json:"kind"`
	Version    int       `json:"version"`
	ExportedAt time.Time `json:"exportedAt"`
	From       string    `json:"from"` // Reelyard version that wrote it

	Settings     *ExportSettings      `json:"settings,omitempty"`
	Suppliers    []ExportSupplier     `json:"suppliers,omitempty"`
	Profiles     []store.Profile      `json:"profiles,omitempty"`
	MediaServers []mediaserver.Target `json:"mediaServers,omitempty"`
	Viewers      []ExportViewer       `json:"viewers,omitempty"`
	Titles       []ExportTitle        `json:"titles,omitempty"`
}

// ExportSettings are the settings pages.
type ExportSettings struct {
	Library   *importer.Settings `json:"library,omitempty"`
	Downloads *engine.Settings   `json:"downloads,omitempty"`
	DLNA      *dlna.Settings     `json:"dlna,omitempty"`
	Updates   *update.Settings   `json:"updates,omitempty"`
}

// ExportSupplier is an addon; Manifest is its last fetched manifest, so it
// can be added again without asking the addon.
type ExportSupplier struct {
	Name        string          `json:"name"`
	ManifestURL string          `json:"manifestUrl"`
	Manifest    json.RawMessage `json:"manifest,omitempty"`
	Enabled     bool            `json:"enabled"`
	Priority    int             `json:"priority"`
	Language    string          `json:"language,omitempty"`
}

// ExportViewer is a viewer without its user id or DLNA slot.
type ExportViewer struct {
	Name      string   `json:"name"`
	AllTitles bool     `json:"allTitles"`
	Titles    []string `json:"titles"`
	DLNA      bool     `json:"dlna"`
	TVs       []string `json:"tvs"`
}

// ExportTitle is a tracked title. Skipped lists a series' episodes that were
// turned off by hand, so they stay off; the rest follow Monitor.
type ExportTitle struct {
	IMDbID  string   `json:"imdbId"`
	Type    string   `json:"type"`
	Name    string   `json:"name"`
	Profile string   `json:"profile"`
	Monitor string   `json:"monitor"`
	Seasons []int    `json:"seasons"`
	Skipped []string `json:"skipped,omitempty"`
}

func (s *Server) routesTransfer(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/settings/export", s.authed(s.exportSettings))
	mux.Handle("POST /api/v1/settings/import", s.authed(s.importSettings))
}

// ---- export ----

func (s *Server) buildExport(ctx context.Context) (*Export, error) {
	ex := &Export{Kind: exportKind, Version: exportVersion, ExportedAt: time.Now().UTC(), From: version.Version}

	set := &ExportSettings{}
	lib := importer.LoadSettings(ctx, s.Store)
	set.Library = &lib
	if s.Engine != nil {
		d := s.Engine.Settings()
		set.Downloads = &d
	}
	if s.DLNA != nil {
		d := s.DLNA.Settings()
		set.DLNA = &d
	}
	if s.Updates != nil {
		u := s.Updates.LoadSettings(ctx)
		set.Updates = &u
	}
	ex.Settings = set

	sups, err := s.Store.ListSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	for _, sp := range sups {
		e := ExportSupplier{Name: sp.Name, ManifestURL: sp.ManifestURL, Enabled: sp.Enabled, Priority: sp.Priority, Language: sp.Language}
		if json.Valid([]byte(sp.Manifest)) {
			e.Manifest = json.RawMessage(sp.Manifest)
		}
		ex.Suppliers = append(ex.Suppliers, e)
	}

	profiles, err := s.Store.ListProfiles(ctx)
	if err != nil {
		return nil, err
	}
	names := map[int64]string{}
	for _, p := range profiles {
		names[p.ID] = p.Name
		p.ID, p.CreatedAt, p.UpdatedAt = 0, time.Time{}, time.Time{}
		ex.Profiles = append(ex.Profiles, p)
	}

	var targets []mediaserver.Target
	if err := s.Store.GetSetting(ctx, "media_servers", &targets); err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	for _, t := range targets {
		t.ID = ""
		ex.MediaServers = append(ex.MediaServers, t)
	}

	vs, err := s.Store.ListViewers(ctx)
	if err != nil {
		return nil, err
	}
	for _, v := range vs {
		ex.Viewers = append(ex.Viewers, ExportViewer{Name: v.Name, AllTitles: v.AllTitles, Titles: v.Titles, DLNA: v.DLNA, TVs: v.TVs})
	}

	titles, err := s.Store.ListTitles(ctx)
	if err != nil {
		return nil, err
	}
	for _, t := range titles {
		e := ExportTitle{IMDbID: t.IMDbID, Type: t.Type, Name: t.Name, Profile: names[t.ProfileID], Monitor: t.Monitor, Seasons: t.Seasons}
		if t.Type == "series" {
			items, err := s.Store.ItemsOfTitle(ctx, t.ID)
			if err != nil {
				return nil, err
			}
			for _, it := range items {
				if !it.Monitored {
					e.Skipped = append(e.Skipped, it.ItemID)
				}
			}
		}
		ex.Titles = append(ex.Titles, e)
	}
	return ex, nil
}

func (s *Server) exportSettings(w http.ResponseWriter, r *http.Request) {
	ex, err := s.buildExport(r.Context())
	if err != nil {
		s.internal(w, "export settings", err)
		return
	}
	name := "reelyard-settings-" + time.Now().Format("2006-01-02") + ".json"
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(ex)
}

// ---- import ----

// ImportResult says what an import changed.
type ImportResult struct {
	Settings     []string       `json:"settings"`
	Suppliers    ImportCount    `json:"suppliers"`
	Profiles     ImportCount    `json:"profiles"`
	MediaServers ImportCount    `json:"mediaServers"`
	Viewers      ImportCount    `json:"viewers"`
	Titles       ImportCount    `json:"titles"`
	Problems     []string       `json:"problems"`
	Sections     map[string]int `json:"sections"` // what the file holds
}

// ImportCount counts rows added and updated. Titles are added in the
// background (each is looked up first), so for them Added is how many were
// queued.
type ImportCount struct {
	Added   int `json:"added"`
	Updated int `json:"updated"`
	Skipped int `json:"skipped"`
}

func (s *Server) importSettings(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Data     json.RawMessage `json:"data"`
		Sections []string        `json:"sections"`
		DryRun   bool            `json:"dryRun"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, importLimit))
	if err := dec.Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "That file couldn't be read: "+err.Error())
		return
	}
	var ex Export
	if err := json.Unmarshal(body.Data, &ex); err != nil || ex.Kind != exportKind {
		writeError(w, http.StatusBadRequest, "That isn't a Reelyard settings file.")
		return
	}
	if ex.Version > exportVersion {
		writeError(w, http.StatusBadRequest, "This file comes from a newer Reelyard; update this server first.")
		return
	}
	res := &ImportResult{Settings: []string{}, Problems: []string{}, Sections: map[string]int{
		"suppliers": len(ex.Suppliers), "profiles": len(ex.Profiles), "mediaServers": len(ex.MediaServers),
		"viewers": len(ex.Viewers), "titles": len(ex.Titles),
	}}
	if ex.Settings != nil {
		res.Sections["settings"] = 1
	}
	if body.DryRun {
		writeJSON(w, http.StatusOK, res)
		return
	}
	want := func(sec string) bool { return len(body.Sections) == 0 || slices.Contains(body.Sections, sec) }
	ctx := r.Context()

	// Profiles first: titles refer to them by name.
	if want("profiles") {
		s.importProfiles(ctx, ex.Profiles, res)
	}
	if want("settings") && ex.Settings != nil {
		s.importSettingsPages(ctx, ex.Settings, res)
	}
	if want("suppliers") {
		s.importSuppliers(ctx, ex.Suppliers, res)
	}
	if want("mediaServers") {
		s.importMediaServers(ctx, ex.MediaServers, res)
	}
	if want("viewers") {
		s.importViewers(ctx, ex.Viewers, res)
	}
	if want("titles") {
		s.importTitles(ex.Titles, res)
	}
	s.Log.Info("settings imported", "settings", len(res.Settings), "suppliers", res.Suppliers, "profiles", res.Profiles,
		"viewers", res.Viewers, "titles", res.Titles, "problems", len(res.Problems), "by", auth.UserFrom(ctx).Username)
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) importProfiles(ctx context.Context, list []store.Profile, res *ImportResult) {
	have, err := s.Store.ListProfiles(ctx)
	if err != nil {
		res.Problems = append(res.Problems, "profiles: "+err.Error())
		return
	}
	for _, p := range list {
		p.ID = 0
		for _, h := range have {
			if strings.EqualFold(h.Name, p.Name) {
				p.ID = h.ID
			}
		}
		if _, err := s.Store.SaveProfile(ctx, &p); err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("profile %q: %v", p.Name, err))
		} else if p.ID == 0 {
			res.Profiles.Added++
		} else {
			res.Profiles.Updated++
		}
	}
}

func (s *Server) importSettingsPages(ctx context.Context, set *ExportSettings, res *ImportResult) {
	note := func(name string, err error) {
		if err != nil {
			res.Problems = append(res.Problems, name+": "+err.Error())
		} else {
			res.Settings = append(res.Settings, name)
		}
	}
	if set.Library != nil {
		_, err := importer.SaveSettings(ctx, s.Store, *set.Library)
		note("library", err)
	}
	if set.Downloads != nil && s.Engine != nil {
		note("downloads", s.Engine.SetSettings(ctx, *set.Downloads))
	}
	if set.DLNA != nil && s.DLNA != nil {
		// Keep this server's DLNA name, port and network: another server's
		// name would make TVs mix the two up, and its network may not exist here.
		d, cur := *set.DLNA, s.DLNA.Settings()
		d.Name, d.Port, d.Interfaces = cur.Name, cur.Port, cur.Interfaces
		_, err := s.DLNA.SetSettings(ctx, d)
		note("dlna", err)
	}
	if set.Updates != nil && s.Updates != nil {
		note("updates", s.Updates.SaveSettings(ctx, *set.Updates))
	}
}

func (s *Server) importSuppliers(ctx context.Context, list []ExportSupplier, res *ImportResult) {
	have, err := s.Store.ListSuppliers(ctx)
	if err != nil {
		res.Problems = append(res.Problems, "suppliers: "+err.Error())
		return
	}
	for _, e := range list {
		var id int64
		for _, h := range have {
			if h.ManifestURL == e.ManifestURL {
				id = h.ID
			}
		}
		if id == 0 {
			var sp *store.Supplier
			if len(e.Manifest) > 0 && s.Suppliers != nil {
				sp, err = s.Store.CreateSupplier(ctx, e.Name, e.ManifestURL, string(e.Manifest))
			} else if s.Suppliers != nil {
				// No manifest in the file: ask the addon for it.
				added, aerr := s.Suppliers.Add(ctx, e.ManifestURL)
				if aerr == nil {
					sp = &added.Supplier
				}
				err = aerr
			}
			if err != nil || sp == nil {
				res.Problems = append(res.Problems, fmt.Sprintf("supplier %q: %v", e.Name, err))
				continue
			}
			id = sp.ID
			res.Suppliers.Added++
		} else {
			res.Suppliers.Updated++
		}
		name, lang := e.Name, e.Language
		if _, err := s.Store.UpdateSupplier(ctx, id, store.SupplierPatch{Name: &name, Enabled: &e.Enabled, Priority: &e.Priority, Language: &lang}); err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("supplier %q: %v", e.Name, err))
		}
	}
}

func (s *Server) importMediaServers(ctx context.Context, list []mediaserver.Target, res *ImportResult) {
	if s.MediaServers == nil {
		return
	}
	for _, t := range list {
		t.ID = ""
		for _, v := range s.MediaServers.List() {
			if strings.TrimRight(v.URL, "/") == strings.TrimRight(t.URL, "/") && v.Kind == t.Kind {
				t.ID = v.ID
			}
		}
		existed := t.ID != ""
		if _, err := s.MediaServers.Save(ctx, t); err != nil {
			res.Problems = append(res.Problems, fmt.Sprintf("media server %q: %v", t.Name, err))
		} else if existed {
			res.MediaServers.Updated++
		} else {
			res.MediaServers.Added++
		}
	}
}

func (s *Server) importViewers(ctx context.Context, list []ExportViewer, res *ImportResult) {
	have, err := s.Store.ListViewers(ctx)
	if err != nil {
		res.Problems = append(res.Problems, "viewers: "+err.Error())
		return
	}
	changed := false
	for _, e := range list {
		v := &store.Viewer{Name: e.Name, AllTitles: e.AllTitles, Titles: e.Titles, DLNA: e.DLNA, TVs: e.TVs}
		for _, h := range have {
			if strings.EqualFold(h.Name, e.Name) {
				v.ID, v.Slot = h.ID, h.Slot
			}
		}
		if v.Titles == nil {
			v.Titles = []string{}
		}
		if v.TVs == nil {
			v.TVs = []string{}
		}
		existed := v.ID != 0
		if existed {
			err = s.Store.UpdateViewer(ctx, v)
		} else {
			err = s.Store.CreateViewer(ctx, v)
		}
		switch {
		case err != nil:
			res.Problems = append(res.Problems, fmt.Sprintf("viewer %q: %v", e.Name, err))
		case existed:
			res.Viewers.Updated++
			changed = true
		default:
			res.Viewers.Added++
			changed = true
		}
	}
	if changed && s.DLNA != nil {
		s.DLNA.ViewersChanged()
	}
}

// importTitles tracks the titles in the background: each is looked up
// first, which takes a moment per title.
func (s *Server) importTitles(list []ExportTitle, res *ImportResult) {
	if s.Tracker == nil || len(list) == 0 {
		return
	}
	ctx := context.Background()
	have, err := s.Store.ListTitles(ctx)
	if err != nil {
		res.Problems = append(res.Problems, "titles: "+err.Error())
		return
	}
	tracked := map[string]bool{}
	for _, t := range have {
		tracked[t.IMDbID] = true
	}
	profiles, err := s.Store.ListProfiles(ctx)
	if err != nil || len(profiles) == 0 {
		res.Problems = append(res.Problems, "titles: no quality profiles")
		return
	}
	profileID := func(name string) int64 {
		for _, p := range profiles {
			if strings.EqualFold(p.Name, name) {
				return p.ID
			}
		}
		return profiles[0].ID
	}
	var todo []ExportTitle
	for _, t := range list {
		if tracked[t.IMDbID] {
			res.Titles.Skipped++
			continue
		}
		todo = append(todo, t)
	}
	res.Titles.Added = len(todo)
	if len(todo) == 0 {
		return
	}
	go func() {
		added, failed := 0, 0
		for _, t := range todo {
			ti, err := s.Tracker.Track(ctx, tracker.TrackRequest{IMDbID: t.IMDbID, Type: t.Type, ProfileID: profileID(t.Profile), Monitor: t.Monitor, Seasons: t.Seasons})
			if err != nil {
				failed++
				s.Log.Warn("import: couldn't track title", "title", t.Name, "err", err)
				continue
			}
			added++
			if len(t.Skipped) > 0 {
				s.skipEpisodes(ctx, ti.ID, t.Skipped)
			}
		}
		s.Log.Info("import: tracked titles", "added", added, "failed", failed)
		if s.Tracker != nil {
			s.Tracker.Kick()
		}
	}()
}

// skipEpisodes turns off a series' episodes that were off where it came from.
func (s *Server) skipEpisodes(ctx context.Context, titleID int64, skipped []string) {
	off := map[string]bool{}
	for _, e := range skipped {
		off[e] = true
	}
	items, err := s.Store.ItemsOfTitle(ctx, titleID)
	if err != nil {
		return
	}
	no := false
	for _, it := range items {
		if off[it.ItemID] && it.Monitored {
			_ = s.Store.UpdateItem(ctx, it.ID, store.ItemUpdate{Monitored: &no})
		}
	}
}
