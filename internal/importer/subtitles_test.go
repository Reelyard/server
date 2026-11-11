package importer

import (
	"strings"
	"testing"
)

func TestToSRTFromVTT(t *testing.T) {
	vtt := "WEBVTT\nKind: captions\n\nNOTE a comment\n\n1\n00:01.000 --> 00:04.250 align:start position:10%\n<v Ben>Hello <c.yellow>there</c></v>\n\n01:02:03.500 --> 01:02:05.000\nSecond line\nand more\n"
	got, err := ToSRT([]byte(vtt))
	if err != nil {
		t.Fatal(err)
	}
	want := "1\n00:00:01,000 --> 00:00:04,250\nHello there\n\n2\n01:02:03,500 --> 01:02:05,000\nSecond line\nand more\n\n"
	if string(got) != want {
		t.Fatalf("got\n%q\nwant\n%q", got, want)
	}
}

func TestToSRTKeepsSRTAndFixesEncoding(t *testing.T) {
	// Windows-1252 "Olá" (0xE1 = á), CRLF line endings, BOM-less.
	srt := []byte("1\r\n00:00:01,000 --> 00:00:02,000\r\nOl\xe1\r\n\r\n2\r\n00:00:03,000 --> 00:00:04,000\r\n<i>Bye</i>\r\n")
	got, err := ToSRT(srt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "Olá") || strings.Contains(string(got), "\r") || !strings.Contains(string(got), "<i>Bye</i>") {
		t.Fatalf("got %q", got)
	}
	if _, err := ToSRT([]byte("<html>not found</html>")); err != ErrNotSubtitle {
		t.Fatalf("html accepted: %v", err)
	}
}

func TestPick(t *testing.T) {
	cands := []candidate{{Lang: "pt-BR", Supplier: "a"}, {Lang: "en", Supplier: "a"}, {Lang: "pt", Supplier: "b"}, {Lang: "pt-BR", Supplier: "b"}}
	got := pick("pt", cands)
	if len(got) != 3 || got[0].Lang != "pt" || got[1].Lang != "pt-BR" {
		t.Fatalf("pick pt = %+v", got)
	}
	if got := pick("fr", cands); len(got) != 0 {
		t.Fatalf("pick fr = %+v", got)
	}
	if got := normalizeLangs([]string{"eng", "pt_BR", "zz", "en"}); strings.Join(got, ",") != "en,pt-BR" {
		t.Fatalf("normalizeLangs = %v", got)
	}
}
