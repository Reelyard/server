// Package profile decides which streams fit a quality profile, why the
// others don't, and which one to pick.
package profile

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"

	"github.com/reelyard/server/internal/lang"
	"github.com/reelyard/server/internal/search"
	"github.com/reelyard/server/internal/store"
)

// Resolutions Reelyard knows, best first.
var Resolutions = []string{"2160p", "1080p", "720p", "576p", "480p"}

// DefaultBlocked keeps out releases nobody wants by default.
var DefaultBlocked = []string{"CAM", "HDCAM", "TS", "HDTS", "TELESYNC", "TC", "SCREENER", "3D"}

// Defaults are created on first start.
func Defaults() []store.Profile {
	return []store.Profile{
		{Name: "HD 1080p", Resolutions: []string{"1080p", "720p"}, MovieMinMB: 700, MovieMaxMB: 20000, EpisodeMinMB: 150, EpisodeMaxMB: 6000,
			AudioLangs: []string{}, SubtitleLangs: []string{"en"}, BlockedTerms: DefaultBlocked, PreferredTerms: []string{}, MinSeeders: 3, SeedRatio: 1, SeedHours: 48},
		{Name: "4K", Resolutions: []string{"2160p", "1080p"}, MovieMinMB: 3000, MovieMaxMB: 80000, EpisodeMinMB: 1000, EpisodeMaxMB: 20000,
			AudioLangs: []string{}, SubtitleLangs: []string{"en"}, BlockedTerms: append(append([]string{}, DefaultBlocked...), "Upscaled"), PreferredTerms: []string{"HDR", "DV"}, MinSeeders: 3, SeedRatio: 1, SeedHours: 48},
		{Name: "Small", Resolutions: []string{"720p", "1080p", "480p"}, MovieMinMB: 300, MovieMaxMB: 3000, EpisodeMinMB: 80, EpisodeMaxMB: 1000,
			AudioLangs: []string{}, SubtitleLangs: []string{"en"}, BlockedTerms: DefaultBlocked, PreferredTerms: []string{"x265"}, MinSeeders: 2, SeedRatio: 1, SeedHours: 24},
	}
}

// EnsureDefaults creates the default profiles if there are none.
func EnsureDefaults(ctx context.Context, st *store.Store) error {
	list, err := st.ListProfiles(ctx)
	if err != nil || len(list) > 0 {
		return err
	}
	for _, p := range Defaults() {
		p := p
		if _, err := st.SaveProfile(ctx, &p); err != nil {
			return err
		}
	}
	return nil
}

// Clean validates and normalizes a profile before saving.
func Clean(p *store.Profile) error {
	p.Name = strings.TrimSpace(p.Name)
	if p.Name == "" || len(p.Name) > 60 {
		return errors.New("give the profile a name of 1–60 characters")
	}
	var res []string
	for _, r := range p.Resolutions {
		r = strings.ToLower(strings.TrimSpace(r))
		if !contains(Resolutions, r) {
			return fmt.Errorf("unknown resolution %q (use %s)", r, strings.Join(Resolutions, ", "))
		}
		if !contains(res, r) {
			res = append(res, r)
		}
	}
	if len(res) == 0 {
		return errors.New("pick at least one resolution")
	}
	p.Resolutions = res
	for _, v := range []*int{&p.MovieMinMB, &p.MovieMaxMB, &p.EpisodeMinMB, &p.EpisodeMaxMB, &p.MinSeeders, &p.SeedHours} {
		if *v < 0 {
			return errors.New("sizes, seeders and hours can't be negative")
		}
	}
	if p.MovieMaxMB > 0 && p.MovieMinMB > p.MovieMaxMB || p.EpisodeMaxMB > 0 && p.EpisodeMinMB > p.EpisodeMaxMB {
		return errors.New("a minimum size is larger than its maximum")
	}
	if p.SeedRatio < 0 {
		return errors.New("seed ratio can't be negative")
	}
	var err error
	if p.AudioLangs, err = langs(p.AudioLangs); err != nil {
		return err
	}
	if p.SubtitleLangs, err = langs(p.SubtitleLangs); err != nil {
		return err
	}
	if p.AudioRequired && len(p.AudioLangs) == 0 {
		return errors.New("“audio language required” needs at least one audio language")
	}
	p.BlockedTerms, p.PreferredTerms = terms(p.BlockedTerms), terms(p.PreferredTerms)
	return nil
}

func langs(in []string) ([]string, error) {
	out := []string{}
	for _, l := range in {
		n := lang.Normalize(l)
		if n == "" {
			return nil, fmt.Errorf("unknown language %q", l)
		}
		out = lang.Merge(out, []string{n})
	}
	return out, nil
}

func terms(in []string) []string {
	out := []string{}
	for _, t := range in {
		if t = strings.TrimSpace(t); t != "" && !containsFold(out, t) {
			out = append(out, t)
		}
	}
	return out
}

// Evaluation is the verdict on one candidate.
type Evaluation struct {
	Accepted   bool     `json:"accepted"`
	Score      int      `json:"score"`
	Rejections []string `json:"rejections,omitempty"`
	Notes      []string `json:"notes,omitempty"`
}

// Context carries per-search facts.
type Context struct {
	Episode  bool            // judging for an episode (else a movie)
	Season   int             // wanted season/episode when Episode
	Number   int             //
	Blocked  map[string]bool // info hashes not to grab for this item
	Siblings map[string]bool // info hashes already grabbed for other episodes of this season
}

var sourceScore = map[string]int{"REMUX": 60, "BluRay": 50, "WEB-DL": 45, "WEBRip": 35, "BDRip": 30, "HDTV": 20, "HDRip": 15, "DVDRip": 10}

// Evaluate judges one candidate against a profile.
func Evaluate(p *store.Profile, c search.Candidate, cx Context) Evaluation {
	var ev Evaluation
	reject := func(f string, a ...any) { ev.Rejections = append(ev.Rejections, fmt.Sprintf(f, a...)) }
	note := func(f string, a ...any) { ev.Notes = append(ev.Notes, fmt.Sprintf(f, a...)) }
	info := c.Stream.Info

	if !c.Usable || c.InfoHash == "" {
		reject("not a torrent")
	}
	if cx.Blocked[strings.ToLower(c.InfoHash)] {
		reject("on the blocklist (failed before)")
	}
	switch {
	case cx.Episode && len(info.Seasons) > 0 && !info.HasEpisode(cx.Season, cx.Number):
		reject("is a different episode")
	case !cx.Episode && len(info.Seasons) > 0:
		reject("looks like a series, not this movie")
	}
	if info.IsLowQuality() {
		reject("camera/telesync copy")
	}

	// Resolution.
	res := info.Resolution
	if res == "" && (info.Source == "DVDRip" || info.Source == "HDTV") {
		res = "480p"
	}
	idx := indexOf(p.Resolutions, res)
	switch {
	case res == "":
		reject("resolution unknown")
	case idx < 0:
		reject("%s isn't in this profile (%s)", res, strings.Join(p.Resolutions, ", "))
	default:
		ev.Score += (len(p.Resolutions) - idx) * 1000
	}

	// Size: per file. Season packs often report the whole torrent, so they
	// aren't held to per-episode limits.
	min, max := p.MovieMinMB, p.MovieMaxMB
	if cx.Episode {
		min, max = p.EpisodeMinMB, p.EpisodeMaxMB
	}
	mb := int(c.Stream.Size >> 20)
	switch {
	case c.Stream.Size <= 0:
		note("size unknown")
	case info.SeasonPack && cx.Episode:
		note("size not checked (season pack)")
	case min > 0 && mb < min:
		reject("too small (%s, minimum %s)", mbText(mb), mbText(min))
	case max > 0 && mb > max:
		reject("too big (%s, maximum %s)", mbText(mb), mbText(max))
	}

	if c.Stream.Seeders >= 0 && c.Stream.Seeders < p.MinSeeders {
		reject("only %d seeders (needs %d)", c.Stream.Seeders, p.MinSeeders)
	}
	if c.Stream.Seeders > 0 {
		ev.Score += int(math.Min(100, 20*math.Log2(1+float64(c.Stream.Seeders))))
	}

	text := c.Stream.TorrentName + " " + c.Stream.FilePath
	for _, t := range p.BlockedTerms {
		if hasTerm(text, t) {
			reject("contains “%s”", t)
		}
	}
	for _, t := range p.PreferredTerms {
		if hasTerm(text, t) || containsFold(info.HDR, t) || strings.EqualFold(info.Codec, t) || strings.EqualFold(info.Source, t) {
			ev.Score += 80
			note("has “%s”", t)
		}
	}
	ev.Score += sourceScore[info.Source]
	if info.Upscaled {
		ev.Score -= 200
		note("upscaled")
	}

	// Audio language. Releases rarely tag English; untagged counts as English.
	if len(p.AudioLangs) > 0 {
		have := info.Languages
		assumed := false
		if len(have) == 0 {
			have, assumed = []string{"en"}, true
		}
		best := -1
		for i, want := range p.AudioLangs {
			for _, h := range have {
				if _, ok := sameLang(want, h); ok && (best < 0 || i < best) {
					best = i
				}
			}
		}
		switch {
		case best >= 0:
			ev.Score += 300 - 50*best
			switch {
			case assumed:
				note("language not stated, assumed English")
			case c.LangFrom != "":
				note("%s audio, from %s's language setting", lang.Name(c.Stream.Info.Languages[0]), c.LangFrom)
			}
		case p.AudioRequired:
			reject("audio is %s; profile needs %s", langNames(have), langNames(p.AudioLangs))
		}
	}

	if cx.Siblings[strings.ToLower(c.InfoHash)] {
		ev.Score += 150
		note("same release as other episodes")
	}
	ev.Accepted = len(ev.Rejections) == 0
	return ev
}

// Ranked is a candidate with its evaluation.
type Ranked struct {
	search.Candidate
	Eval Evaluation `json:"eval"`
}

// Rank evaluates all candidates: accepted first by score, then the rest.
func Rank(p *store.Profile, cands []search.Candidate, cx Context) []Ranked {
	out := make([]Ranked, len(cands))
	for i, c := range cands {
		out[i] = Ranked{Candidate: c, Eval: Evaluate(p, c, cx)}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i].Eval, out[j].Eval
		if a.Accepted != b.Accepted {
			return a.Accepted
		}
		return a.Score > b.Score
	})
	return out
}

// Summary explains why nothing was accepted: "12 streams: 8 not in this
// profile's resolutions, 4 too few seeders".
func Summary(ranked []Ranked) string {
	if len(ranked) == 0 {
		return "no streams found"
	}
	counts := map[string]int{}
	var order []string
	for _, r := range ranked {
		if len(r.Eval.Rejections) == 0 {
			continue
		}
		k := reasonKind(r.Eval.Rejections[0])
		if counts[k] == 0 {
			order = append(order, k)
		}
		counts[k]++
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	parts := []string{}
	for _, k := range order {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	return fmt.Sprintf("%d streams, none fit: %s", len(ranked), strings.Join(parts, ", "))
}

var digits = regexp.MustCompile(`\d[\d.,]*\s*[GM]?B?`)

func reasonKind(r string) string {
	switch {
	case strings.HasPrefix(r, "only "):
		return "with too few seeders"
	case strings.Contains(r, "isn't in this profile"):
		return "in other resolutions"
	case strings.HasPrefix(r, "too big"):
		return "too big"
	case strings.HasPrefix(r, "too small"):
		return "too small"
	case strings.HasPrefix(r, "contains"):
		return "with blocked words"
	case strings.HasPrefix(r, "audio is"):
		return "in other languages"
	}
	return strings.TrimSpace(digits.ReplaceAllString(r, ""))
}

func hasTerm(text, term string) bool {
	re, err := regexp.Compile(`(?i)(^|[^a-z0-9])` + regexp.QuoteMeta(term) + `($|[^a-z0-9])`)
	return err == nil && re.MatchString(text)
}

func sameLang(want, have string) (exact, ok bool) {
	if want == have {
		return true, true
	}
	b := func(l string) string { x, _, _ := strings.Cut(l, "-"); return x }
	return false, b(want) == b(have)
}

func langNames(codes []string) string {
	n := make([]string, len(codes))
	for i, c := range codes {
		n[i] = lang.Name(c)
	}
	return strings.Join(n, "/")
}

func mbText(mb int) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}

func indexOf(list []string, s string) int {
	for i, v := range list {
		if v == s {
			return i
		}
	}
	return -1
}

func contains(list []string, s string) bool { return indexOf(list, s) >= 0 }

func containsFold(list []string, s string) bool {
	for _, v := range list {
		if strings.EqualFold(v, s) {
			return true
		}
	}
	return false
}
