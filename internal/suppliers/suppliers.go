// Package suppliers manages the Stremio addons Reelyard searches: adding
// them by manifest URL, refreshing their manifests, and testing them.
package suppliers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/reelyard/server/internal/release"
	"github.com/reelyard/server/internal/store"
	"github.com/reelyard/server/internal/stremio"
)

// Supplier is a stored supplier plus what its manifest says it can do.
type Supplier struct {
	store.Supplier
	DisplayURL   string   `json:"displayUrl"` // manifest URL with config segments hidden
	AddonID      string   `json:"addonId"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Logo         string   `json:"logo,omitempty"`
	Resources    []string `json:"resources"`
	Types        []string `json:"types"`
	Configurable bool     `json:"configurable"`
	manifest     *stremio.Manifest
}

// Manifest returns the parsed manifest.
func (s *Supplier) Manifest() *stremio.Manifest { return s.manifest }

// Base returns the URL resources hang off.
func (s *Supplier) Base() string { return stremio.BaseURL(s.ManifestURL) }

// Service ties the store to the addon client.
type Service struct {
	Store  *store.Store
	Client *stremio.Client
	// CatalogBase serves Stremio's addon catalogs (default: Cinemeta).
	CatalogBase string

	catalog catalogCache
}

// Errors shown to the user.
var (
	ErrDuplicate   = errors.New("this addon is already added")
	ErrNoResources = errors.New("this addon offers neither streams nor subtitles, so Reelyard can't use it")
)

func wrap(sp *store.Supplier) *Supplier {
	out := &Supplier{Supplier: *sp, DisplayURL: DisplayURL(sp.ManifestURL)}
	var m stremio.Manifest
	if json.Unmarshal([]byte(sp.Manifest), &m) == nil {
		out.manifest = &m
		out.AddonID, out.Version, out.Description, out.Logo = m.ID, m.Version, m.Description, m.Logo
		out.Resources, out.Types = m.ResourceNames(), m.Types
		out.Configurable = m.BehaviorHints.Configurable
	}
	if out.Resources == nil {
		out.Resources = []string{}
	}
	if out.Types == nil {
		out.Types = []string{}
	}
	return out
}

// DisplayURL hides addon configuration in the path, which often holds
// debrid API keys: https://host/<config>/manifest.json → https://host/…/manifest.json.
func DisplayURL(manifestURL string) string {
	u, err := url.Parse(manifestURL)
	if err != nil {
		return manifestURL
	}
	if strings.Trim(strings.TrimSuffix(u.Path, "manifest.json"), "/") == "" {
		return manifestURL
	}
	return u.Scheme + "://" + u.Host + "/…/manifest.json"
}

// List returns all suppliers.
func (s *Service) List(ctx context.Context) ([]*Supplier, error) {
	rows, err := s.Store.ListSuppliers(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*Supplier, len(rows))
	for i := range rows {
		out[i] = wrap(&rows[i])
	}
	return out, nil
}

// Enabled returns enabled suppliers that serve resource.
func (s *Service) Enabled(ctx context.Context, resource string) ([]*Supplier, error) {
	all, err := s.List(ctx)
	if err != nil {
		return nil, err
	}
	var out []*Supplier
	for _, sp := range all {
		if sp.Enabled && sp.manifest != nil && contains(sp.Resources, resource) {
			out = append(out, sp)
		}
	}
	return out, nil
}

// Get returns one supplier.
func (s *Service) Get(ctx context.Context, id int64) (*Supplier, error) {
	sp, err := s.Store.GetSupplier(ctx, id)
	if err != nil {
		return nil, err
	}
	return wrap(sp), nil
}

// Add fetches the manifest behind rawURL and stores the addon.
func (s *Service) Add(ctx context.Context, rawURL string) (*Supplier, error) {
	manifestURL, _, err := stremio.NormalizeManifestURL(rawURL)
	if err != nil {
		return nil, err
	}
	m, err := s.Client.FetchManifest(ctx, manifestURL)
	if err != nil {
		return nil, fmt.Errorf("couldn't read the addon's manifest: %w", friendly(err))
	}
	names := m.ResourceNames()
	if !contains(names, stremio.ResStream) && !contains(names, stremio.ResSubtitles) {
		return nil, ErrNoResources
	}
	raw, _ := json.Marshal(m)
	sp, err := s.Store.CreateSupplier(ctx, m.Name, manifestURL, string(raw))
	if errors.Is(err, store.ErrDuplicate) {
		return nil, ErrDuplicate
	}
	if err != nil {
		return nil, err
	}
	return wrap(sp), nil
}

// Refresh re-reads the manifest.
func (s *Service) Refresh(ctx context.Context, id int64) (*Supplier, error) {
	sp, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	m, ferr := s.Client.FetchManifest(ctx, sp.ManifestURL)
	if ferr != nil {
		ferr = friendly(ferr)
		_ = s.Store.RecordSupplierCheck(ctx, id, "", ferr)
		return nil, fmt.Errorf("couldn't read the addon's manifest: %w", ferr)
	}
	raw, _ := json.Marshal(m)
	if err := s.Store.RecordSupplierCheck(ctx, id, string(raw), nil); err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Probe titles for tests: a public-domain film and a well-indexed episode.
var probes = []struct{ Type, ID, Label string }{
	{"movie", "tt0063350", "Night of the Living Dead (1968)"},
	{"series", "tt0903747:1:1", "Breaking Bad S01E01"},
}

// Check is one probe request made by Test.
type Check struct {
	Resource   string   `json:"resource"`
	Type       string   `json:"type"`
	Title      string   `json:"title"`
	Count      int      `json:"count"`
	Usable     int      `json:"usable"` // streams with an info hash
	Samples    []string `json:"samples,omitempty"`
	Error      string   `json:"error,omitempty"`
	DurationMS int64    `json:"durationMs"`
}

// TestResult summarises a test.
type TestResult struct {
	OK      bool    `json:"ok"`
	Message string  `json:"message"`
	Checks  []Check `json:"checks"`
}

// Test asks the addon for well-known titles and reports what came back.
func (s *Service) Test(ctx context.Context, id int64) (*TestResult, error) {
	sp, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	m := sp.manifest
	if m == nil {
		return nil, errors.New("stored manifest is unreadable; refresh the supplier")
	}
	res := &TestResult{Checks: []Check{}}
	for _, p := range probes {
		for _, resource := range []string{stremio.ResStream, stremio.ResSubtitles} {
			if !m.Supports(resource, p.Type, p.ID) {
				continue
			}
			c := Check{Resource: resource, Type: p.Type, Title: p.Label}
			start := time.Now()
			if resource == stremio.ResStream {
				streams, err := s.Client.Streams(ctx, sp.Base(), p.Type, p.ID)
				if err != nil {
					c.Error = friendly(err).Error()
				}
				c.Count = len(streams)
				for _, st := range streams {
					if st.IsTorrent() {
						c.Usable++
						if len(c.Samples) < 3 {
							c.Samples = append(c.Samples, release.ParseStream(st.Name, st.Text(), st.BehaviorHints.Filename, st.BehaviorHints.VideoSize).TorrentName)
						}
					}
				}
			} else {
				subs, err := s.Client.Subtitles(ctx, sp.Base(), p.Type, p.ID, stremio.SubtitleQuery{})
				if err != nil {
					c.Error = friendly(err).Error()
				}
				c.Count, c.Usable = len(subs), len(subs)
				langs := map[string]bool{}
				for _, sub := range subs {
					if !langs[sub.Lang] && len(c.Samples) < 6 {
						langs[sub.Lang] = true
						c.Samples = append(c.Samples, sub.Lang)
					}
				}
			}
			c.DurationMS = time.Since(start).Milliseconds()
			res.Checks = append(res.Checks, c)
		}
	}
	res.OK, res.Message = summarize(res.Checks)
	var checkErr error
	if !res.OK {
		checkErr = errors.New(res.Message)
	}
	_ = s.Store.RecordSupplierCheck(ctx, id, "", checkErr)
	return res, nil
}

func summarize(checks []Check) (bool, string) {
	if len(checks) == 0 {
		return false, "This addon doesn't serve movies or series by IMDb id, so Reelyard can't test it."
	}
	var streams, usable, subs, failed int
	for _, c := range checks {
		if c.Error != "" {
			failed++
			continue
		}
		if c.Resource == stremio.ResStream {
			streams += c.Count
			usable += c.Usable
		} else {
			subs += c.Count
		}
	}
	switch {
	case failed == len(checks):
		return false, "The addon didn't answer: " + checks[0].Error
	case streams > 0 && usable == 0:
		return false, "The addon returns streams, but none are torrents. It is probably set up for a debrid service, which Reelyard can't download from yet."
	case usable > 0:
		return true, fmt.Sprintf("Works: %d torrent streams found for the test titles.", usable)
	case subs > 0:
		return true, fmt.Sprintf("Works: %d subtitles found for the test titles.", subs)
	default:
		return false, "The addon answered but found nothing for the test titles."
	}
}

// friendly rewrites transport errors into short sentences.
func friendly(err error) error {
	var he *stremio.HTTPError
	switch {
	case errors.As(err, &he):
		return fmt.Errorf("the addon answered with HTTP %d", he.Status)
	case errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "Client.Timeout"):
		return errors.New("the addon took too long to answer")
	case strings.Contains(err.Error(), "no such host"):
		return errors.New("the addon's address could not be found")
	case strings.Contains(err.Error(), "connection refused"):
		return errors.New("the addon refused the connection")
	}
	return err
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
