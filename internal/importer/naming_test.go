package importer

import (
	"strings"
	"testing"
)

func TestPaths(t *testing.T) {
	cases := []struct{ got, want string }{
		{MoviePath("Night of the Living Dead", 1968, "720p", ".MP4"), "Movies/Night of the Living Dead (1968)/Night of the Living Dead (1968) - 720p.mp4"},
		{MoviePath("Star Wars: Episode IV – A New Hope", 1977, "", "mkv"), "Movies/Star Wars - Episode IV – A New Hope (1977)/Star Wars - Episode IV – A New Hope (1977).mkv"},
		{EpisodePath("Breaking Bad", 2008, 1, 2, "Cat's in the Bag...", "1080p", ".mkv"), "TV/Breaking Bad (2008)/Season 01/Breaking Bad (2008) - S01E02 - Cat's in the Bag - 1080p.mkv"},
		{EpisodePath("Show", 0, 0, 3, "Episode 3", "", ".mkv"), "TV/Show/Season 00/Show - S00E03.mkv"},
		{SubtitlePath("Movies/A (1999)/A (1999) - 1080p.mkv", "pt-BR"), "Movies/A (1999)/A (1999) - 1080p.pt-BR.srt"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("got  %q\nwant %q", c.got, c.want)
		}
	}
}

func TestSafeName(t *testing.T) {
	cases := map[string]string{
		`What? Why/How "quoted" <x>|*`: "What Why How quoted x",
		"Trailing dots...":             "Trailing dots",
		"CON":                          "CON_",
		"nul.txt":                      "nul.txt_",
		"tab\there\x00":                "tabhere",
	}
	for in, want := range cases {
		if got := SafeName(in); got != want {
			t.Errorf("SafeName(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("é", 200)
	if got := SafeName(long); len(got) > 150 || !strings.HasPrefix(long, got) {
		t.Errorf("long name trimmed badly: %d bytes", len(got))
	}
}

func TestYearFrom(t *testing.T) {
	if YearFrom("2008–2013") != 2008 || YearFrom("1968") != 1968 || YearFrom("") != 0 || YearFrom("TBA") != 0 {
		t.Error("YearFrom wrong")
	}
}
