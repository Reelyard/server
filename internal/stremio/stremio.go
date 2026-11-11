// Package stremio is a client for the Stremio addon protocol: manifests,
// streams, subtitles, catalogs and metadata.
//
// An addon is addressed by its manifest URL, e.g.
// https://torrentio.strem.fun/manifest.json or a configured one like
// https://host/<config>/manifest.json. Resources live next to it:
//
//	{base}/stream/{type}/{id}.json
//	{base}/subtitles/{type}/{id}/{extra}.json
//	{base}/catalog/{type}/{catalogId}/{extra}.json
//	{base}/meta/{type}/{id}.json
//
// Ids are IMDb ids (tt0111161) for movies and tt0903747:1:2 for episodes.
package stremio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Resource names.
const (
	ResStream    = "stream"
	ResSubtitles = "subtitles"
	ResCatalog   = "catalog"
	ResMeta      = "meta"
)

// maxBody caps how much of a response is read (some addons return a lot).
const maxBody = 8 << 20

// Client talks to addons over HTTP.
type Client struct {
	HTTP      *http.Client
	UserAgent string
}

// New returns a client with a sensible timeout.
func New(userAgent string) *Client {
	return &Client{HTTP: &http.Client{Timeout: 20 * time.Second}, UserAgent: userAgent}
}

// HTTPError is a non-2xx answer from an addon.
type HTTPError struct {
	URL    string
	Status int
}

func (e *HTTPError) Error() string {
	return fmt.Sprintf("addon answered HTTP %d", e.Status)
}

// ErrBadURL means the manifest URL is not usable.
var ErrBadURL = errors.New("not a valid addon URL")

// NormalizeManifestURL accepts what people paste (stremio:// links, URLs
// without /manifest.json, surrounding spaces) and returns the https
// manifest URL plus the base URL resources hang off.
func NormalizeManifestURL(raw string) (manifestURL, base string, err error) {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "stremio://") {
		s = "https://" + strings.TrimPrefix(s, "stremio://")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", ErrBadURL
	}
	u.Fragment = ""
	u.RawQuery = ""
	p := strings.TrimRight(u.EscapedPath(), "/")
	if !strings.HasSuffix(p, "/manifest.json") {
		p += "/manifest.json"
	}
	u.RawPath = ""
	u.Path, _ = url.PathUnescape(p)
	manifestURL = u.Scheme + "://" + u.Host + p
	base = strings.TrimSuffix(manifestURL, "/manifest.json")
	return manifestURL, base, nil
}

// BaseURL returns the base of a normalized manifest URL.
func BaseURL(manifestURL string) string {
	return strings.TrimSuffix(manifestURL, "/manifest.json")
}

// ---- manifest ----

// Resource is one resource an addon serves. In manifests it can be a bare
// string ("stream") or an object with its own types and id prefixes.
type Resource struct {
	Name       string   `json:"name"`
	Types      []string `json:"types,omitempty"`
	IDPrefixes []string `json:"idPrefixes,omitempty"`
}

func (r *Resource) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err == nil {
		r.Name = s
		return nil
	}
	type plain Resource
	return json.Unmarshal(b, (*plain)(r))
}

// Catalog is a catalog an addon offers (used for search).
type Catalog struct {
	Type  string         `json:"type"`
	ID    string         `json:"id"`
	Name  string         `json:"name,omitempty"`
	Extra []CatalogExtra `json:"extra,omitempty"`
}

// CatalogExtra is an extra parameter a catalog accepts.
type CatalogExtra struct {
	Name       string `json:"name"`
	IsRequired bool   `json:"isRequired,omitempty"`
}

// Manifest describes an addon.
type Manifest struct {
	ID            string     `json:"id"`
	Version       string     `json:"version"`
	Name          string     `json:"name"`
	Description   string     `json:"description,omitempty"`
	Logo          string     `json:"logo,omitempty"`
	Types         []string   `json:"types"`
	Resources     []Resource `json:"resources"`
	IDPrefixes    []string   `json:"idPrefixes,omitempty"`
	Catalogs      []Catalog  `json:"catalogs,omitempty"`
	BehaviorHints struct {
		Configurable          bool `json:"configurable,omitempty"`
		ConfigurationRequired bool `json:"configurationRequired,omitempty"`
		Adult                 bool `json:"adult,omitempty"`
	} `json:"behaviorHints"`
}

// ResourceNames lists the resources the addon serves, sorted.
func (m *Manifest) ResourceNames() []string {
	seen := map[string]bool{}
	var out []string
	for _, r := range m.Resources {
		if r.Name != "" && !seen[r.Name] {
			seen[r.Name] = true
			out = append(out, r.Name)
		}
	}
	sort.Strings(out)
	return out
}

// Supports reports whether the addon serves resource for this type and id.
// An empty id skips the prefix check.
func (m *Manifest) Supports(resource, typ, id string) bool {
	for _, r := range m.Resources {
		if r.Name != resource {
			continue
		}
		types := r.Types
		if len(types) == 0 {
			types = m.Types
		}
		prefixes := r.IDPrefixes
		if prefixes == nil {
			prefixes = m.IDPrefixes
		}
		return contains(types, typ) && (id == "" || hasPrefix(id, prefixes))
	}
	return false
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func hasPrefix(id string, prefixes []string) bool {
	if len(prefixes) == 0 {
		return true // no restriction declared
	}
	for _, p := range prefixes {
		if strings.HasPrefix(id, p) {
			return true
		}
	}
	return false
}

// Validate checks the fields Harbor relies on.
func (m *Manifest) Validate() error {
	if m.ID == "" || m.Name == "" {
		return errors.New("manifest is missing id or name")
	}
	if len(m.Resources) == 0 {
		return errors.New("manifest lists no resources")
	}
	return nil
}

// FetchManifest downloads and validates a manifest.
func (c *Client) FetchManifest(ctx context.Context, manifestURL string) (*Manifest, error) {
	var m Manifest
	if err := c.getJSON(ctx, manifestURL, &m); err != nil {
		return nil, err
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// ---- streams ----

// StreamHints are a stream's behaviorHints.
type StreamHints struct {
	BingeGroup  string `json:"bingeGroup,omitempty"`
	Filename    string `json:"filename,omitempty"`
	VideoSize   int64  `json:"videoSize,omitempty"`
	VideoHash   string `json:"videoHash,omitempty"`
	NotWebReady bool   `json:"notWebReady,omitempty"`
}

// Stream is one playable source returned by an addon.
type Stream struct {
	Name          string      `json:"name,omitempty"`
	Title         string      `json:"title,omitempty"`
	Description   string      `json:"description,omitempty"`
	InfoHash      string      `json:"infoHash,omitempty"`
	FileIdx       *int        `json:"fileIdx,omitempty"`
	URL           string      `json:"url,omitempty"`
	YtID          string      `json:"ytId,omitempty"`
	ExternalURL   string      `json:"externalUrl,omitempty"`
	Sources       []string    `json:"sources,omitempty"`
	BehaviorHints StreamHints `json:"behaviorHints"`
}

// IsTorrent reports whether Harbor can download this stream itself.
func (s *Stream) IsTorrent() bool { return len(s.InfoHash) == 40 || len(s.InfoHash) == 32 }

// Text returns the descriptive text (addons use title or description).
func (s *Stream) Text() string {
	if s.Description != "" {
		return s.Description
	}
	return s.Title
}

// Streams fetches the streams for a movie (tt…) or episode (tt…:S:E).
func (c *Client) Streams(ctx context.Context, base, typ, id string) ([]Stream, error) {
	var body struct {
		Streams []Stream `json:"streams"`
	}
	if err := c.getJSON(ctx, resourceURL(base, ResStream, typ, id, nil), &body); err != nil {
		return nil, err
	}
	for i := range body.Streams {
		body.Streams[i].InfoHash = strings.ToLower(body.Streams[i].InfoHash)
	}
	return body.Streams, nil
}

// ---- subtitles ----

// Subtitle is one subtitle file offered by an addon.
type Subtitle struct {
	ID   string `json:"id"`
	URL  string `json:"url"`
	Lang string `json:"lang"` // usually ISO 639-2 (eng, por, pob…)
}

// SubtitleQuery holds the optional extras for a subtitle lookup.
type SubtitleQuery struct {
	VideoHash string // OpenSubtitles hash of the file
	VideoSize int64
	Filename  string
}

// Subtitles fetches subtitles for a movie or episode.
func (c *Client) Subtitles(ctx context.Context, base, typ, id string, q SubtitleQuery) ([]Subtitle, error) {
	var extra [][2]string
	if q.VideoHash != "" {
		extra = append(extra, [2]string{"videoHash", q.VideoHash})
	}
	if q.VideoSize > 0 {
		extra = append(extra, [2]string{"videoSize", fmt.Sprint(q.VideoSize)})
	}
	if q.Filename != "" {
		extra = append(extra, [2]string{"filename", q.Filename})
	}
	var body struct {
		Subtitles []Subtitle `json:"subtitles"`
	}
	if err := c.getJSON(ctx, resourceURL(base, ResSubtitles, typ, id, extra), &body); err != nil {
		return nil, err
	}
	return body.Subtitles, nil
}

// ---- catalogs & meta ----

// MetaPreview is a catalog entry.
type MetaPreview struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Poster      string `json:"poster,omitempty"`
	ReleaseInfo string `json:"releaseInfo,omitempty"`
	Description string `json:"description,omitempty"`
}

// Video is an episode (or part) of a meta item.
type Video struct {
	ID        string    `json:"id"`
	Title     string    `json:"title,omitempty"`
	Name      string    `json:"name,omitempty"`
	Season    int       `json:"season"`
	Episode   int       `json:"episode"`
	Number    int       `json:"number,omitempty"`
	Released  time.Time `json:"released"`
	Overview  string    `json:"overview,omitempty"`
	Thumbnail string    `json:"thumbnail,omitempty"`
}

// Meta is full metadata for a movie or series.
type Meta struct {
	ID          string   `json:"id"`
	Type        string   `json:"type"`
	Name        string   `json:"name"`
	Year        string   `json:"year,omitempty"`
	ReleaseInfo string   `json:"releaseInfo,omitempty"`
	Description string   `json:"description,omitempty"`
	Poster      string   `json:"poster,omitempty"`
	Background  string   `json:"background,omitempty"`
	Logo        string   `json:"logo,omitempty"`
	Runtime     string   `json:"runtime,omitempty"`
	Genres      []string `json:"genres,omitempty"`
	IMDbRating  string   `json:"imdbRating,omitempty"`
	Status      string   `json:"status,omitempty"`
	Videos      []Video  `json:"videos,omitempty"`
}

// Catalog fetches a catalog page, e.g. a search (extra search=…).
func (c *Client) Catalog(ctx context.Context, base, typ, catalogID string, extra [][2]string) ([]MetaPreview, error) {
	var body struct {
		Metas []MetaPreview `json:"metas"`
	}
	if err := c.getJSON(ctx, resourceURL(base, ResCatalog, typ, catalogID, extra), &body); err != nil {
		return nil, err
	}
	return body.Metas, nil
}

// Meta fetches full metadata.
func (c *Client) Meta(ctx context.Context, base, typ, id string) (*Meta, error) {
	var body struct {
		Meta *Meta `json:"meta"`
	}
	if err := c.getJSON(ctx, resourceURL(base, ResMeta, typ, id, nil), &body); err != nil {
		return nil, err
	}
	if body.Meta == nil {
		return nil, &HTTPError{Status: http.StatusNotFound}
	}
	return body.Meta, nil
}

// ---- addon catalogs ----

// AddonDescriptor is an entry in an addon catalog: an addon's manifest
// plus where to reach it.
type AddonDescriptor struct {
	TransportURL  string   `json:"transportUrl"`
	TransportName string   `json:"transportName"`
	Manifest      Manifest `json:"manifest"`
	Flags         struct {
		Official  bool `json:"official,omitempty"`
		Protected bool `json:"protected,omitempty"`
	} `json:"flags"`
}

// AddonCatalog fetches an addon catalog ({base}/addon_catalog/{type}/{id}.json),
// the list Stremio's Addons screen shows. Cinemeta serves "official" and
// "community".
func (c *Client) AddonCatalog(ctx context.Context, base, typ, id string) ([]AddonDescriptor, error) {
	var body struct {
		Addons []AddonDescriptor `json:"addons"`
	}
	if err := c.getJSON(ctx, resourceURL(base, "addon_catalog", typ, id, nil), &body); err != nil {
		return nil, err
	}
	return body.Addons, nil
}

// ---- transport ----

// resourceURL builds {base}/{resource}/{type}/{id}[/{extra}].json.
// Path segments are escaped like Stremio does (encodeURIComponent), but
// ':' is kept so episode ids stay readable.
func resourceURL(base, resource, typ, id string, extra [][2]string) string {
	var b strings.Builder
	b.WriteString(strings.TrimRight(base, "/"))
	b.WriteString("/" + resource + "/" + seg(typ) + "/" + seg(id))
	if len(extra) > 0 {
		parts := make([]string, len(extra))
		for i, kv := range extra {
			parts[i] = seg(kv[0]) + "=" + seg(kv[1])
		}
		b.WriteString("/" + strings.Join(parts, "&"))
	}
	b.WriteString(".json")
	return b.String()
}

func seg(s string) string {
	return strings.ReplaceAll(url.PathEscape(s), "%3A", ":")
}

func (c *Client) getJSON(ctx context.Context, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return &HTTPError{URL: u, Status: resp.StatusCode}
	}
	dec := json.NewDecoder(io.LimitReader(resp.Body, maxBody))
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("addon sent invalid JSON: %w", err)
	}
	return nil
}
