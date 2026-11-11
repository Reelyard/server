package profile

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/reelyard/server/internal/release"
	"github.com/reelyard/server/internal/search"
	"github.com/reelyard/server/internal/store"
)

func cand(name string, seeders int, sizeMB int64) search.Candidate {
	st := release.ParseStream("Addon", name+"\n👤 "+itoa(seeders)+" 💾 "+itoa(int(sizeMB))+" MB ⚙️ X", "", 0)
	return search.Candidate{InfoHash: strings.Repeat("a", 40), Usable: true, Stream: st}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func hd() *store.Profile {
	p := Defaults()[0]
	return &p
}

func TestEvaluateRules(t *testing.T) {
	p := hd()
	mv := Context{}
	cases := []struct {
		name    string
		c       search.Candidate
		cx      Context
		ok      bool
		because string
	}{
		{"good 1080p", cand("Movie.2020.1080p.BluRay.x264-GRP", 50, 8000), mv, true, ""},
		{"4k not in HD profile", cand("Movie.2020.2160p.WEB-DL.x265-GRP", 50, 15000), mv, false, "2160p isn't in this profile"},
		{"camera", cand("Movie.2020.HDCAM.x264-GRP", 500, 1500), mv, false, "camera"},
		{"few seeders", cand("Movie.2020.720p.WEB-DL-GRP", 1, 1500), mv, false, "only 1 seeders"},
		{"too big", cand("Movie.2020.1080p.BluRay.REMUX-GRP", 40, 30000), mv, false, "too big"},
		{"too small", cand("Movie.2020.720p.WEBRip-GRP", 40, 200), mv, false, "too small"},
		{"series for movie", cand("Show.S01E01.1080p.WEB-DL-GRP", 40, 1500), mv, false, "looks like a series"},
		{"wrong episode", cand("Show.S01E03.1080p.WEB-DL-GRP", 40, 1500), Context{Episode: true, Season: 1, Number: 2}, false, "different episode"},
		{"right episode", cand("Show.S01E02.1080p.WEB-DL-GRP", 40, 1500), Context{Episode: true, Season: 1, Number: 2}, true, ""},
		{"pack size not checked", cand("Show.S01.1080p.BluRay-GRP", 40, 40000), Context{Episode: true, Season: 1, Number: 2}, true, ""},
		{"unknown resolution", cand("Movie 2020 BluRay", 40, 1500), mv, false, "resolution unknown"},
	}
	for _, c := range cases {
		ev := Evaluate(p, c.c, c.cx)
		if ev.Accepted != c.ok {
			t.Errorf("%s: accepted=%v (%v)", c.name, ev.Accepted, ev.Rejections)
			continue
		}
		if c.because != "" && !strings.Contains(strings.Join(ev.Rejections, "|"), c.because) {
			t.Errorf("%s: rejections %v, want %q", c.name, ev.Rejections, c.because)
		}
	}

	blocked := cand("Movie.2020.1080p.BluRay.x264-GRP", 50, 8000)
	if ev := Evaluate(p, blocked, Context{Blocked: map[string]bool{blocked.InfoHash: true}}); ev.Accepted {
		t.Error("blocklisted release accepted")
	}
}

func TestAudioLanguage(t *testing.T) {
	p := hd()
	p.AudioLangs = []string{"pt", "en"}
	pt := cand("Movie.2020.1080p.WEB-DL.DUBLADO-GRP", 20, 2000)
	en := cand("Movie.2020.1080p.WEB-DL-GRP", 20, 2000)
	fr := cand("Movie.2020.1080p.WEB-DL.FRENCH-GRP", 20, 2000)
	if a, b := Evaluate(p, pt, Context{}), Evaluate(p, en, Context{}); a.Score <= b.Score {
		t.Errorf("Portuguese should outrank English: %d vs %d", a.Score, b.Score)
	}
	if ev := Evaluate(p, fr, Context{}); !ev.Accepted {
		t.Error("French rejected although audio isn't required")
	}
	p.AudioRequired = true
	if ev := Evaluate(p, fr, Context{}); ev.Accepted || !strings.Contains(ev.Rejections[0], "audio is French") {
		t.Errorf("French with required pt/en: %+v", ev)
	}
	if ev := Evaluate(p, en, Context{}); !ev.Accepted {
		t.Errorf("untagged release should count as English: %+v", ev)
	}
}

func TestRankPrefersResolutionThenSiblings(t *testing.T) {
	p := hd()
	a := cand("Show.S01E02.720p.WEB-DL-GRP", 900, 800)
	b := cand("Show.S01E02.1080p.WEB-DL-GRP", 10, 1500)
	c := cand("Show.S01.1080p.WEB-DL-PACK", 10, 1500)
	c.InfoHash = strings.Repeat("c", 40)
	r := Rank(p, []search.Candidate{a, b, c}, Context{Episode: true, Season: 1, Number: 2, Siblings: map[string]bool{c.InfoHash: true}})
	if r[0].InfoHash != c.InfoHash || r[1].Stream.Info.Resolution != "1080p" || r[2].Stream.Info.Resolution != "720p" {
		t.Fatalf("order: %s %s %s", r[0].Stream.TorrentName, r[1].Stream.TorrentName, r[2].Stream.TorrentName)
	}
}

func TestSummary(t *testing.T) {
	p := hd()
	r := Rank(p, []search.Candidate{cand("M.2020.2160p.WEB-DL-G", 50, 9000), cand("M.2020.2160p.WEB-DL-H", 50, 9000), cand("M.2020.1080p.WEB-DL-G", 1, 2000)}, Context{})
	if got := Summary(r); got != "3 streams, none fit: 2 in other resolutions, 1 with too few seeders" {
		t.Fatalf("Summary = %q", got)
	}
}

func TestClean(t *testing.T) {
	p := store.Profile{Name: " X ", Resolutions: []string{"1080P", "1080p"}, AudioLangs: []string{"por"}, BlockedTerms: []string{" CAM ", "cam", ""}}
	if err := Clean(&p); err != nil {
		t.Fatal(err)
	}
	if p.Name != "X" || len(p.Resolutions) != 1 || p.AudioLangs[0] != "pt" || len(p.BlockedTerms) != 1 {
		t.Fatalf("cleaned: %+v", p)
	}
	bad := []store.Profile{{Name: "", Resolutions: []string{"1080p"}}, {Name: "a"}, {Name: "a", Resolutions: []string{"8k"}},
		{Name: "a", Resolutions: []string{"720p"}, AudioRequired: true}, {Name: "a", Resolutions: []string{"720p"}, MovieMinMB: 10, MovieMaxMB: 5}}
	for i, b := range bad {
		if err := Clean(&b); err == nil {
			t.Errorf("bad profile %d accepted", i)
		}
	}
}

// Real Torrentio answers through the HD profile: something is accepted for
// both the movie and the episode, and nothing accepted breaks the rules.
func TestFixtures(t *testing.T) {
	p := hd()
	for _, f := range []struct {
		file string
		cx   Context
	}{
		{"../stremio/testdata/torrentio-movie.json", Context{}},
		{"../stremio/testdata/torrentio-series.json", Context{Episode: true, Season: 1, Number: 1}},
	} {
		b, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		var body struct {
			Streams []struct {
				Name, Title, InfoHash string
				BehaviorHints         struct{ Filename string }
			}
		}
		json.Unmarshal(b, &body)
		var cands []search.Candidate
		for _, s := range body.Streams {
			cands = append(cands, search.Candidate{InfoHash: s.InfoHash, Usable: true, Stream: release.ParseStream(s.Name, s.Title, s.BehaviorHints.Filename, 0)})
		}
		r := Rank(p, cands, f.cx)
		if !r[0].Eval.Accepted {
			t.Fatalf("%s: nothing accepted: %s", f.file, Summary(r))
		}
		for _, x := range r {
			if x.Eval.Accepted && (x.Stream.Info.IsLowQuality() || x.Stream.Info.Resolution == "2160p" || x.Stream.Seeders < 3) {
				t.Errorf("%s: accepted %q (%+v)", f.file, x.Stream.TorrentName, x.Eval)
			}
		}
		t.Logf("%s → %s (score %d)", f.file, r[0].Stream.TorrentName, r[0].Eval.Score)
	}
}
