// Package search asks every enabled supplier for streams of one movie or
// episode and returns the parsed, de-duplicated candidates.
package search

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/reelyard/server/internal/release"
	"github.com/reelyard/server/internal/stremio"
	"github.com/reelyard/server/internal/suppliers"
)

// Candidate is one stream Reelyard could download.
type Candidate struct {
	Key          string         `json:"key"` // infoHash[:fileIdx]
	SupplierID   int64          `json:"supplierId"`
	SupplierName string         `json:"supplierName"`
	AlsoFrom     []string       `json:"alsoFrom,omitempty"`
	InfoHash     string         `json:"infoHash,omitempty"`
	FileIdx      *int           `json:"fileIdx,omitempty"`
	Sources      []string       `json:"sources,omitempty"`
	Stream       release.Stream `json:"stream"`
	Usable       bool           `json:"usable"`
	Problems     []string       `json:"problems,omitempty"` // why it can't or shouldn't be used
}

// SupplierResult says how one supplier answered.
type SupplierResult struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Count      int    `json:"count"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"durationMs"`
}

// Result is a full search.
type Result struct {
	Type       string           `json:"type"`
	ID         string           `json:"id"`
	Season     int              `json:"season,omitempty"`
	Episode    int              `json:"episode,omitempty"`
	Candidates []Candidate      `json:"candidates"`
	Suppliers  []SupplierResult `json:"suppliers"`
}

// Service runs searches.
type Service struct {
	Suppliers *suppliers.Service
	Client    *stremio.Client
	Timeout   time.Duration
}

// ParseID splits tt0903747:1:2 into the IMDb id, season and episode.
func ParseID(typ, id string) (imdb string, season, episode int, err error) {
	parts := strings.Split(id, ":")
	if !strings.HasPrefix(parts[0], "tt") || len(parts[0]) < 4 {
		return "", 0, 0, fmt.Errorf("id must be an IMDb id like tt0111161")
	}
	switch {
	case typ == "movie" && len(parts) == 1:
		return parts[0], 0, 0, nil
	case typ == "series" && len(parts) == 3:
		s, err1 := strconv.Atoi(parts[1])
		e, err2 := strconv.Atoi(parts[2])
		if err1 != nil || err2 != nil || s < 0 || e < 1 {
			return "", 0, 0, fmt.Errorf("episode id must look like tt0903747:1:2")
		}
		return parts[0], s, e, nil
	case typ == "series":
		return "", 0, 0, fmt.Errorf("pick an episode: series ids look like tt0903747:1:2")
	}
	return "", 0, 0, fmt.Errorf("type must be movie or series")
}

// Streams asks all enabled stream suppliers in parallel.
func (s *Service) Streams(ctx context.Context, typ, id string) (*Result, error) {
	_, season, episode, err := ParseID(typ, id)
	if err != nil {
		return nil, err
	}
	sups, err := s.Suppliers.Enabled(ctx, stremio.ResStream)
	if err != nil {
		return nil, err
	}
	timeout := s.Timeout
	if timeout == 0 {
		timeout = 25 * time.Second
	}

	res := &Result{Type: typ, ID: id, Season: season, Episode: episode, Candidates: []Candidate{}, Suppliers: []SupplierResult{}}
	type answer struct {
		sr      SupplierResult
		streams []stremio.Stream
	}
	answers := make([]answer, len(sups))
	var wg sync.WaitGroup
	for i, sp := range sups {
		answers[i].sr = SupplierResult{ID: sp.ID, Name: sp.Name}
		if !sp.Manifest().Supports(stremio.ResStream, typ, id) {
			answers[i].sr.Error = "doesn't offer this kind of title"
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			defer cancel()
			start := time.Now()
			streams, err := s.Client.Streams(cctx, sp.Base(), typ, id)
			answers[i].sr.DurationMS = time.Since(start).Milliseconds()
			if err != nil {
				answers[i].sr.Error = shortError(err)
				_ = s.Suppliers.Store.RecordSupplierCheck(ctx, sp.ID, "", err)
				return
			}
			answers[i].sr.Count = len(streams)
			answers[i].streams = streams
			_ = s.Suppliers.Store.RecordSupplierCheck(ctx, sp.ID, "", nil)
		}()
	}
	wg.Wait()

	seen := map[string]int{} // key → index in res.Candidates
	for _, a := range answers {
		res.Suppliers = append(res.Suppliers, a.sr)
		for _, st := range a.streams {
			c := candidate(a.sr, st, typ, season, episode)
			if i, dup := seen[c.Key]; dup && c.InfoHash != "" {
				res.Candidates[i].AlsoFrom = appendUnique(res.Candidates[i].AlsoFrom, a.sr.Name)
				if c.Stream.Seeders > res.Candidates[i].Stream.Seeders {
					res.Candidates[i].Stream.Seeders = c.Stream.Seeders
				}
				continue
			}
			seen[c.Key] = len(res.Candidates)
			res.Candidates = append(res.Candidates, c)
		}
	}
	Sort(res.Candidates)
	return res, nil
}

func candidate(sr SupplierResult, st stremio.Stream, typ string, season, episode int) Candidate {
	ps := release.ParseStream(st.Name, st.Text(), st.BehaviorHints.Filename, st.BehaviorHints.VideoSize)
	c := Candidate{
		SupplierID: sr.ID, SupplierName: sr.Name,
		InfoHash: st.InfoHash, FileIdx: st.FileIdx, Sources: st.Sources,
		Stream: ps, Usable: st.IsTorrent(),
	}
	c.Key = st.InfoHash
	if st.FileIdx != nil {
		c.Key += ":" + strconv.Itoa(*st.FileIdx)
	}
	if c.Key == "" {
		c.Key = "url:" + st.URL + st.ExternalURL + st.YtID
	}
	if !c.Usable {
		c.Problems = append(c.Problems, "Not a torrent (a debrid or web link), so Reelyard can't download it")
	}
	if typ == "series" && len(ps.Info.Seasons) > 0 && !ps.Info.HasEpisode(season, episode) {
		c.Problems = append(c.Problems, fmt.Sprintf("The name doesn't look like S%02dE%02d", season, episode))
	}
	if ps.Info.IsLowQuality() {
		c.Problems = append(c.Problems, "Camera or telesync copy")
	}
	return c
}

var resRank = map[string]int{"2160p": 6, "1080p": 5, "720p": 4, "576p": 3, "480p": 2, "360p": 1}

// Sort orders candidates: usable first, then resolution, then seeders.
func Sort(cs []Candidate) {
	sort.SliceStable(cs, func(i, j int) bool {
		a, b := cs[i], cs[j]
		if (len(a.Problems) == 0) != (len(b.Problems) == 0) {
			return len(a.Problems) == 0
		}
		if ra, rb := resRank[a.Stream.Info.Resolution], resRank[b.Stream.Info.Resolution]; ra != rb {
			return ra > rb
		}
		return a.Stream.Seeders > b.Stream.Seeders
	})
}

func shortError(err error) string {
	var he *stremio.HTTPError
	switch {
	case errors.As(err, &he):
		return fmt.Sprintf("HTTP %d", he.Status)
	case strings.Contains(err.Error(), "deadline exceeded") || strings.Contains(err.Error(), "Client.Timeout"):
		return "timed out"
	}
	return err.Error()
}

func appendUnique(list []string, s string) []string {
	for _, v := range list {
		if v == s {
			return list
		}
	}
	return append(list, s)
}
