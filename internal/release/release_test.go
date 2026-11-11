package release

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		want Info
	}{
		{"Night.of.the.Living.Dead.1968.2160p.BluRay.REMUX.HEVC.SDR.LPCM.1.0-FGT",
			Info{Title: "Night of the Living Dead", Year: 1968, Resolution: "2160p", Source: "REMUX", Codec: "x265", Audio: []string{"LPCM"}, Channels: "1.0", Group: "FGT"}},
		{"Breaking Bad S01E01 Pilot 2160p NF WEB-DL DDP5 1 H 265-XEBEC",
			Info{Title: "Breaking Bad", Seasons: []int{1}, Episodes: []int{1}, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", Audio: []string{"DDP"}, Channels: "5.1", Group: "XEBEC"}},
		{"Breaking.Bad.S01E01-07.1080p.NF.WEB-DL.ITA-ENG.DDP5.1.AV1-G66",
			Info{Title: "Breaking Bad", Seasons: []int{1}, Episodes: []int{1, 2, 3, 4, 5, 6, 7}, Resolution: "1080p", Source: "WEB-DL", Codec: "AV1", Audio: []string{"DDP"}, Channels: "5.1", Languages: []string{"it", "en"}, Group: "G66"}},
		{"Breaking Bad (2008) S01-S05 1080p BluRay REMUX Dual Audio",
			Info{Title: "Breaking Bad", Year: 2008, Seasons: []int{1, 2, 3, 4, 5}, SeasonPack: true, Complete: true, Resolution: "1080p", Source: "REMUX", Multi: true}},
		{"Breaking.Bad.S01.1080p.BluRay.x265-RARBG",
			Info{Title: "Breaking Bad", Seasons: []int{1}, SeasonPack: true, Resolution: "1080p", Source: "BluRay", Codec: "x265", Group: "RARBG"}},
		{"Во все тяжкие / Breaking Bad [S01-05] (2008-2013) BDRip-HEVC 720p",
			Info{Title: "Breaking Bad", Year: 2008, Seasons: []int{1, 2, 3, 4, 5}, SeasonPack: true, Complete: true, Resolution: "720p", Source: "BDRip", Codec: "x265"}},
		{"Breaking Bad 1 Temporada BDRip 720p dublado",
			Info{Title: "Breaking Bad", Seasons: []int{1}, SeasonPack: true, Resolution: "720p", Source: "BDRip", Languages: []string{"pt-BR"}}},
		{"Breaking Bad - S01E01 - 1080p - PopHD",
			Info{Title: "Breaking Bad", Seasons: []int{1}, Episodes: []int{1}, Resolution: "1080p", Group: "PopHD"}},
		{"The.Mandalorian.S02E03.Chapter.11.2160p.DSNP.WEB-DL.DDP5.1.Atmos.DV.HDR10.H.265-FLUX",
			Info{Title: "The Mandalorian", Seasons: []int{2}, Episodes: []int{3}, Resolution: "2160p", Source: "WEB-DL", Codec: "x265", HDR: []string{"DV", "HDR10"}, Audio: []string{"Atmos", "DDP"}, Channels: "5.1", Group: "FLUX"}},
		{"Blade.Runner.2049.2017.1080p.BluRay.x264-SPARKS",
			Info{Title: "Blade Runner 2049", Year: 2017, Resolution: "1080p", Source: "BluRay", Codec: "x264", Group: "SPARKS"}},
		{"Some.Movie.2024.HDCAM.x264-NOGRP",
			Info{Title: "Some Movie", Year: 2024, Source: "CAM", Codec: "x264", Group: "NOGRP"}},
		{"Show.Name.3x07.720p.HDTV.x264-GRP",
			Info{Title: "Show Name", Seasons: []int{3}, Episodes: []int{7}, Resolution: "720p", Source: "HDTV", Codec: "x264", Group: "GRP"}},
	}
	for _, c := range cases {
		got := Parse(c.name)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Parse(%q)\n got  %+v\n want %+v", c.name, got, c.want)
		}
	}
}

func TestParseFile(t *testing.T) {
	got := ParseFile("Season 1/Breaking Bad Season 1 Episode 01 - Pilot.avi")
	if got.Season() != 1 || !reflect.DeepEqual(got.Episodes, []int{1}) {
		t.Errorf("ParseFile = %+v", got)
	}
	got = ParseFile(`Breaking Bad/Season 2/05 - Breakage.mkv`)
	if got.Season() != 2 || !reflect.DeepEqual(got.Episodes, []int{5}) {
		t.Errorf("ParseFile loose episode = %+v", got)
	}
}

func TestHasEpisode(t *testing.T) {
	pack := Parse("Show S01-S03 1080p")
	if !pack.HasEpisode(2, 9) || pack.HasEpisode(4, 1) {
		t.Errorf("pack HasEpisode wrong: %+v", pack)
	}
	ep := Parse("Show S02E09 1080p")
	if !ep.HasEpisode(2, 9) || ep.HasEpisode(2, 8) {
		t.Errorf("episode HasEpisode wrong: %+v", ep)
	}
}

func TestParseStreamTorrentio(t *testing.T) {
	s := ParseStream("Torrentio\n4k",
		"Breaking Bad COMPLETE S01-S05 2160p WEB-DL Rus Ukr Eng DTS-HD MA5.1 x264-TrollUHD [RiCK]\nBreaking.Bad.S01.2160p.WEBRip.DTS-HD.MA5.1.x264-TrollUHD/Breaking.Bad.S01E01.2160p.WEBRip.DTS-HD.MA5.1.x264-TrollUHD.mkv\n👤 41 💾 50.81 GB ⚙️ 1337x\n🇬🇧 / 🇷🇺 / 🇺🇦",
		"", 0)
	if s.Seeders != 41 || s.Site != "1337x" || s.Size != parseSize("50.81", "GB") || s.Label != "4k" {
		t.Errorf("meta: %+v", s)
	}
	i := s.Info
	if i.Season() != 1 || !reflect.DeepEqual(i.Episodes, []int{1}) || !i.SeasonPack || i.Resolution != "2160p" {
		t.Errorf("info: %+v", i)
	}
	if !reflect.DeepEqual(i.Languages, []string{"ru", "uk", "en"}) {
		t.Errorf("languages = %v", i.Languages)
	}
	if s.FilePath == "" || s.TorrentName == "" {
		t.Errorf("names: %q / %q", s.TorrentName, s.FilePath)
	}
}

func TestParseStreamFlagsOnly(t *testing.T) {
	s := ParseStream("Torrentio", "Night of the Living Dead 1968 EN/PTBR\n👤 4 💾 7.23 GB ⚙️ EXT\n🇬🇧 / 🇵🇹", "", 0)
	if s.Info.Title != "Night of the Living Dead" || s.Info.Year != 1968 {
		t.Errorf("info: %+v", s.Info)
	}
	if !reflect.DeepEqual(s.Info.Languages, []string{"en", "pt-BR", "pt"}) {
		t.Errorf("languages = %v", s.Info.Languages)
	}
}

type fixtureStream struct {
	Name          string `json:"name"`
	Title         string `json:"title"`
	BehaviorHints struct {
		Filename string `json:"filename"`
	} `json:"behaviorHints"`
}

func loadFixture(t *testing.T, file string) []Stream {
	t.Helper()
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var body struct{ Streams []fixtureStream }
	if err := json.Unmarshal(b, &body); err != nil {
		t.Fatal(err)
	}
	out := make([]Stream, len(body.Streams))
	for i, s := range body.Streams {
		out[i] = ParseStream(s.Name, s.Title, s.BehaviorHints.Filename, 0)
	}
	return out
}

// Real Torrentio answers: every stream must yield its metadata, and the
// parsed fields must be sane for the title that was asked for.
func TestFixtureInvariants(t *testing.T) {
	movie := loadFixture(t, "testdata/torrentio-movie.json")
	series := loadFixture(t, "testdata/torrentio-series.json")

	for _, s := range append(append([]Stream{}, movie...), series...) {
		if s.Seeders < 0 || s.Size <= 0 || s.Site == "" || s.TorrentName == "" {
			t.Errorf("missing metadata: %+v", s)
		}
		for _, e := range s.Info.Episodes {
			if e <= 0 || e > 200 {
				t.Errorf("implausible episode %d in %q / %q", e, s.TorrentName, s.FilePath)
			}
		}
	}

	withRes := 0
	for _, s := range movie {
		if s.Info.Resolution != "" {
			withRes++
		}
		if len(s.Info.Seasons) > 0 {
			t.Errorf("movie parsed as series: %q → %+v", s.TorrentName, s.Info)
		}
	}
	if withRes*10 < len(movie)*6 { // old DVD rips often carry no resolution
		t.Errorf("resolution found for only %d/%d movie streams", withRes, len(movie))
	}

	hits := 0
	for _, s := range series {
		if s.Info.HasEpisode(1, 1) {
			hits++
		}
		if s.Info.Title != "Breaking Bad" && s.Info.Title != "Breaking Bad Full" {
			t.Logf("title: %q from %q", s.Info.Title, s.TorrentName)
		}
	}
	if hits*10 < len(series)*9 {
		t.Errorf("S01E01 recognised in only %d/%d series streams", hits, len(series))
	}
}

// DUMP=1 go test ./internal/release -run Dump -v prints every parse, for
// checking a new fixture by eye.
func TestDumpFixtures(t *testing.T) {
	if os.Getenv("DUMP") == "" {
		t.Skip("set DUMP=1")
	}
	for _, f := range []string{"testdata/torrentio-movie.json", "testdata/torrentio-series.json"} {
		for _, p := range loadFixture(t, f) {
			i := p.Info
			fmt.Printf("%-60.60q | t=%q y=%d s=%v e=%v pack=%v res=%s src=%s c=%s hdr=%v a=%v ch=%s l=%v m=%v g=%q seed=%d size=%dMB site=%q\n",
				p.TorrentName, i.Title, i.Year, i.Seasons, i.Episodes, i.SeasonPack, i.Resolution, i.Source, i.Codec, i.HDR, i.Audio, i.Channels, i.Languages, i.Multi, i.Group, p.Seeders, p.Size>>20, p.Site)
		}
	}
}
