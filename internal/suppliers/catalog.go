package suppliers

import (
	"context"
	"html"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inluxc/harbor/internal/stremio"
)

// DefaultCatalogBase is Cinemeta, which serves Stremio's addon catalogs.
const DefaultCatalogBase = "https://v3-cinemeta.strem.io"

// CatalogEntry is an addon from Stremio's catalog that Harbor could use.
type CatalogEntry struct {
	Name                  string   `json:"name"`
	Description           string   `json:"description,omitempty"`
	Logo                  string   `json:"logo,omitempty"`
	Version               string   `json:"version"`
	ManifestURL           string   `json:"manifestUrl"`
	ConfigureURL          string   `json:"configureUrl,omitempty"`
	Resources             []string `json:"resources"` // stream and/or subtitles
	Types                 []string `json:"types"`
	Official              bool     `json:"official"`
	Configurable          bool     `json:"configurable"`
	ConfigurationRequired bool     `json:"configurationRequired"`
	Installed             bool     `json:"installed"`
}

type catalogCache struct {
	mu      sync.Mutex
	at      time.Time
	entries []CatalogEntry
}

// catalogTTL is how long the fetched catalog is reused.
const catalogTTL = time.Hour

// Catalog lists addons from Stremio's official and community catalogs that
// serve streams or subtitles for IMDb ids, marking the ones already added.
func (s *Service) Catalog(ctx context.Context) ([]CatalogEntry, error) {
	entries, err := s.fetchCatalog(ctx)
	if err != nil {
		return nil, err
	}
	installed := map[string]bool{}
	list, err := s.Store.ListSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	for _, sp := range list {
		installed[sp.ManifestURL] = true
	}
	out := make([]CatalogEntry, len(entries))
	for i, e := range entries {
		e.Installed = installed[e.ManifestURL]
		out[i] = e
	}
	return out, nil
}

func (s *Service) fetchCatalog(ctx context.Context) ([]CatalogEntry, error) {
	s.catalog.mu.Lock()
	defer s.catalog.mu.Unlock()
	if s.catalog.entries != nil && time.Since(s.catalog.at) < catalogTTL {
		return s.catalog.entries, nil
	}
	base := s.CatalogBase
	if base == "" {
		base = DefaultCatalogBase
	}

	var all []stremio.AddonDescriptor
	var firstErr error
	for _, id := range []string{"official", "community"} {
		addons, err := s.Client.AddonCatalog(ctx, base, "all", id)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		all = append(all, addons...)
	}
	if len(all) == 0 && firstErr != nil {
		return nil, friendly(firstErr)
	}

	seen := map[string]bool{}
	entries := []CatalogEntry{}
	for _, d := range all {
		e, ok := catalogEntry(d)
		if !ok || seen[e.ManifestURL] {
			continue
		}
		seen[e.ManifestURL] = true
		entries = append(entries, e)
	}
	// Official first, then streams before subtitles, then by name.
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if a.Official != b.Official {
			return a.Official
		}
		as, bs := contains(a.Resources, stremio.ResStream), contains(b.Resources, stremio.ResStream)
		if as != bs {
			return as
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	s.catalog.entries, s.catalog.at = entries, time.Now()
	return entries, nil
}

// catalogEntry keeps addons Harbor can use: reachable over the network,
// using the current protocol, and serving streams or subtitles for movies
// or series by IMDb id.
func catalogEntry(d stremio.AddonDescriptor) (CatalogEntry, bool) {
	manifestURL, _, err := stremio.NormalizeManifestURL(d.TransportURL)
	if err != nil || !strings.HasSuffix(d.TransportURL, "/manifest.json") {
		return CatalogEntry{}, false // legacy (/stremio/v1) or odd transport
	}
	if u, err := url.Parse(manifestURL); err != nil || isLocal(u.Hostname()) {
		return CatalogEntry{}, false // e.g. Stremio's own local-files addon
	}
	m := &d.Manifest
	var res []string
	for _, r := range []string{stremio.ResStream, stremio.ResSubtitles} {
		if m.Supports(r, "movie", "tt0063350") || m.Supports(r, "series", "tt0903747:1:1") {
			res = append(res, r)
		}
	}
	if len(res) == 0 {
		return CatalogEntry{}, false
	}
	e := CatalogEntry{
		Name: strings.TrimSpace(m.Name), Description: plainText(m.Description), Logo: m.Logo,
		Version: m.Version, ManifestURL: manifestURL, Resources: res, Types: m.Types,
		Official: d.Flags.Official, Configurable: m.BehaviorHints.Configurable,
		ConfigurationRequired: m.BehaviorHints.ConfigurationRequired,
	}
	if e.Configurable || e.ConfigurationRequired {
		// Stremio addons serve their settings page next to the manifest.
		e.ConfigureURL = strings.TrimSuffix(manifestURL, "manifest.json") + "configure"
	}
	if e.Types == nil {
		e.Types = []string{}
	}
	return e, true
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

// plainText strips HTML some community manifests put in descriptions.
func plainText(s string) string {
	s = html.UnescapeString(tagRe.ReplaceAllString(s, " "))
	return strings.Join(strings.Fields(s), " ")
}

func isLocal(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost")
}
