// Package metadata looks up movies and series (names, years, posters,
// episode lists) from Cinemeta, Stremio's public metadata addon.
package metadata

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/inluxc/harbor/internal/stremio"
)

// DefaultBase is Cinemeta.
const DefaultBase = "https://v3-cinemeta.strem.io"

// Result is a search hit.
type Result struct {
	ID     string `json:"id"`
	Type   string `json:"type"`
	Name   string `json:"name"`
	Year   string `json:"year,omitempty"`
	Poster string `json:"poster,omitempty"`
}

// Service queries Cinemeta with a small in-memory cache.
type Service struct {
	Client *stremio.Client
	Base   string
	TTL    time.Duration

	mu    sync.Mutex
	cache map[string]cached
}

type cached struct {
	at   time.Time
	meta *stremio.Meta
}

// New returns a Cinemeta-backed service.
func New(c *stremio.Client) *Service {
	return &Service{Client: c, Base: DefaultBase, TTL: 30 * time.Minute, cache: map[string]cached{}}
}

// Search finds movies and/or series ("" = both) matching query.
func (s *Service) Search(ctx context.Context, query, typ string) ([]Result, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return []Result{}, nil
	}
	types := []string{"movie", "series"}
	if typ == "movie" || typ == "series" {
		types = []string{typ}
	}
	type part struct {
		res []Result
		err error
	}
	parts := make([]part, len(types))
	var wg sync.WaitGroup
	for i, t := range types {
		wg.Add(1)
		go func() {
			defer wg.Done()
			metas, err := s.Client.Catalog(ctx, s.Base, t, "top", [][2]string{{"search", query}})
			if err != nil {
				parts[i].err = err
				return
			}
			for _, m := range metas {
				if !strings.HasPrefix(m.ID, "tt") {
					continue // Harbor's suppliers work with IMDb ids
				}
				parts[i].res = append(parts[i].res, Result{ID: m.ID, Type: t, Name: m.Name, Year: m.ReleaseInfo, Poster: m.Poster})
			}
		}()
	}
	wg.Wait()

	out := []Result{}
	var firstErr error
	for _, p := range parts {
		if p.err != nil && firstErr == nil {
			firstErr = p.err
		}
		out = append(out, p.res...)
	}
	if len(out) == 0 && firstErr != nil {
		return nil, firstErr
	}
	// Exact name matches first, then keep Cinemeta's (popularity) order.
	q := strings.ToLower(query)
	sort.SliceStable(out, func(i, j int) bool {
		return strings.ToLower(out[i].Name) == q && strings.ToLower(out[j].Name) != q
	})
	return out, nil
}

// Meta returns full metadata, with episodes sorted by season and number
// and specials (season 0) last.
func (s *Service) Meta(ctx context.Context, typ, id string) (*stremio.Meta, error) {
	key := typ + "/" + id
	s.mu.Lock()
	if c, ok := s.cache[key]; ok && time.Since(c.at) < s.TTL {
		s.mu.Unlock()
		return c.meta, nil
	}
	s.mu.Unlock()

	m, err := s.Client.Meta(ctx, s.Base, typ, id)
	if err != nil {
		return nil, err
	}
	for i := range m.Videos {
		v := &m.Videos[i]
		if v.Episode == 0 {
			v.Episode = v.Number
		}
		if v.Title == "" {
			v.Title = v.Name
		}
	}
	sort.SliceStable(m.Videos, func(i, j int) bool {
		a, b := m.Videos[i], m.Videos[j]
		if (a.Season == 0) != (b.Season == 0) {
			return b.Season == 0
		}
		if a.Season != b.Season {
			return a.Season < b.Season
		}
		return a.Episode < b.Episode
	})

	s.mu.Lock()
	if len(s.cache) > 500 {
		s.cache = map[string]cached{}
	}
	s.cache[key] = cached{at: time.Now(), meta: m}
	s.mu.Unlock()
	return m, nil
}
