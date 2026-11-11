package dlna

import (
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func library(t *testing.T, files ...string) *os.Root {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		p := filepath.Join(dir, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { root.Close() })
	return root
}

func TestFindSubtitle(t *testing.T) {
	const v = "Movies/Night (1968)/Night (1968).mkv"
	root := library(t, v,
		"Movies/Night (1968)/Night (1968).en.srt",
		"Movies/Night (1968)/Night (1968).pt-BR.srt",
		"Movies/Other (2000)/Other (2000).mkv",
		"Movies/Other (2000)/Other (2000).srt",
		"TV/Show/Season 01/Show - S01E01.mkv",
		"TV/Show/Season 01/Show - S01E01.fr.srt",
		"TV/Show/Season 01/Show - S01E02.mkv",
	)
	cases := []struct {
		langs []string
		video string
		want  string
	}{
		{[]string{"pt-BR", "en"}, v, "Movies/Night (1968)/Night (1968).pt-BR.srt"},
		{[]string{"en"}, v, "Movies/Night (1968)/Night (1968).en.srt"},
		{[]string{"de"}, "/" + v, "Movies/Night (1968)/Night (1968).en.srt"}, // any language as a last resort
		{nil, "Movies/Other (2000)/Other (2000).mkv", "Movies/Other (2000)/Other (2000).srt"},
		{[]string{"en"}, "TV/Show/Season 01/Show - S01E01.mkv", "TV/Show/Season 01/Show - S01E01.fr.srt"},
		{[]string{"en"}, "TV/Show/Season 01/Show - S01E02.mkv", ""},
		{[]string{"en"}, "../../etc/passwd", ""},
		{[]string{"en"}, "", ""},
	}
	for _, c := range cases {
		s := &Server{opts: Options{SubtitleLangs: func() []string { return c.langs }}}
		got, ok := s.findSubtitle(root, c.video)
		if got != c.want || ok != (c.want != "") {
			t.Errorf("findSubtitle(%q, %v) = %q %v, want %q", c.video, c.langs, got, ok, c.want)
		}
	}
}

func TestLocalClient(t *testing.T) {
	for addr, want := range map[string]bool{
		"192.168.1.20:5000": true,
		"10.1.2.3:1":        true,
		"172.20.0.5:1":      true,
		"127.0.0.1:1":       true,
		"[fe80::1]:1":       true,
		"[::1]:1":           true,
		"8.8.8.8:53":        false,
		"172.32.0.1:1":      false,
		"[2001:db8::1]:1":   false,
		"pipe":              false,
	} {
		if got := localClient(addr); got != want {
			t.Errorf("localClient(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestSettingsValidate(t *testing.T) {
	if err := DefaultSettings.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Settings{{Name: "", Port: 8200}, {Name: "x", Port: 80}, {Name: "x", Port: 70000}} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be invalid", bad)
		}
	}
}

// The front proxies to dms over in-memory pipes, keeps the Host header,
// serves subtitles itself and adds Samsung's CaptionInfo.sec header.
func TestFront(t *testing.T) {
	root := library(t, "Movies/A (2001)/A (2001).mkv", "Movies/A (2001)/A (2001).en.srt")
	pipe := newPipeListener(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8200})
	backend := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "dms:"+r.URL.Path+":"+r.Host)
	})}
	go backend.Serve(pipe)
	t.Cleanup(func() { backend.Close(); pipe.Close() })

	s := &Server{opts: Options{Log: slog.New(slog.DiscardHandler), SubtitleLangs: func() []string { return []string{"en"} }}}
	h := s.front(&running{pipe: pipe, root: root})

	get := func(remote, target string, hdr map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", target, nil)
		req.RemoteAddr = remote
		req.Host = "192.168.1.5:8200"
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	if w := get("192.168.1.9:4000", "/rootDesc.xml", nil); w.Code != 200 || w.Body.String() != "dms:/rootDesc.xml:192.168.1.5:8200" {
		t.Fatalf("proxy: %d %q", w.Code, w.Body.String())
	}
	if w := get("203.0.113.9:4000", "/rootDesc.xml", nil); w.Code != http.StatusForbidden {
		t.Fatalf("public client: %d", w.Code)
	}
	w := get("192.168.1.9:4000", "/subtitle?path=Movies%2FA+%282001%29%2FA+%282001%29.mkv", nil)
	if w.Code != 200 || w.Body.String() != "Movies/A (2001)/A (2001).en.srt" {
		t.Fatalf("subtitle: %d %q", w.Code, w.Body.String())
	}
	if w := get("192.168.1.9:4000", "/subtitle?path=Movies%2FB.mkv", nil); w.Code != 404 {
		t.Fatalf("missing subtitle: %d", w.Code)
	}
	w = get("192.168.1.9:4000", "/res?path=Movies%2FA+%282001%29%2FA+%282001%29.mkv", map[string]string{"getCaptionInfo.sec": "1"})
	if got := w.Header().Get("CaptionInfo.sec"); got != "http://192.168.1.5:8200/subtitle?path=Movies%2FA+%282001%29%2FA+%282001%29.mkv" {
		t.Fatalf("CaptionInfo.sec = %q", got)
	}
}

func TestIconFallback(t *testing.T) {
	root := library(t, "Movies/A (2001)/A (2001).mkv")
	th := newThumbs("") // no ffmpeg
	for _, p := range []string{"Movies%2FA+%282001%29%2FA+%282001%29.mkv", "..%2F..%2Fetc%2Fpasswd", ""} {
		req := httptest.NewRequest("GET", "/icon?c=jpeg&path="+p, nil)
		w := httptest.NewRecorder()
		th.serve(w, req, root, "/nonexistent")
		if w.Code != 200 || w.Header().Get("Content-Type") != "image/jpeg" || w.Body.Len() == 0 {
			t.Fatalf("icon %q: %d %q %d bytes", p, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
	}
	if n := len(deviceIcons()); n != 4 {
		t.Fatalf("%d device icons", n)
	}
}
