package dlna

import (
	"context"
	"encoding/xml"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anacrolix/dms/dlna/dms"

	"github.com/reelyard/server/internal/store"
)

const (
	film    = "Movies/Night (1968)/Night (1968).mkv"
	episode = "TV/Show/Season 01/Show - S01E01.mkv"
)

func progressServer(t *testing.T) (*Server, int64) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	u, err := st.CreateFirstUser(ctx, "admin", "x")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []struct{ typ, id, path string }{
		{"movie", "tt0063350", film},
		{"episode", "tt0903747:1:1", episode},
	} {
		if _, err := st.UpsertMediaFile(ctx, &store.MediaFile{TitleType: f.typ, TitleID: f.id, TitleName: "x", Path: f.path, Size: 1, Method: "copy"}); err != nil {
			t.Fatal(err)
		}
		if err := st.SetMediaProbe(ctx, mustFile(t, st, f.path).ID, "h264", []string{"aac"}, 6000); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{opts: Options{Store: st, Log: slog.New(slog.DiscardHandler)}}, u.ID
}

func mustFile(t *testing.T, st *store.Store, rel string) *store.MediaFile {
	t.Helper()
	mf, err := st.MediaFileByPath(context.Background(), rel)
	if err != nil {
		t.Fatal(err)
	}
	return mf
}

// didlItem is a video item as dms writes it.
func didlItem(rel string) string {
	return `<item id="` + url.QueryEscape(rel) + `" parentID="x" restricted="1" searchable="0"><dc:title>` +
		filepath.Base(rel) + `</dc:title><upnp:class>object.item.videoItem</upnp:class><dc:date>2026-01-01</dc:date>` +
		`<res protocolInfo="http-get:*:video/x-matroska:*">http://h/res?path=x</res></item>`
}

func browseBody(items ...string) []byte {
	didl := `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/">` +
		strings.Join(items, "") + `</DIDL-Lite>`
	var esc strings.Builder
	_ = xml.EscapeText(&esc, []byte(didl))
	return []byte(`<?xml version="1.0" encoding="utf-8" standalone="yes"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1"><Result>` +
		strings.ReplaceAll(esc.String(), "&#34;", `"`) + `</Result><NumberReturned>2</NumberReturned></u:BrowseResponse></s:Body></s:Envelope>`)
}

// resultDIDL pulls the DIDL-Lite out of a Browse answer, checking it parses.
func resultDIDL(t *testing.T, body []byte) string {
	t.Helper()
	var env struct {
		Body struct {
			Resp struct {
				Result string
			} `xml:",any"`
		}
	}
	if err := xml.Unmarshal(body, &env); err != nil {
		t.Fatalf("answer isn't XML: %v\n%s", err, body)
	}
	var didl struct{}
	if err := xml.Unmarshal([]byte(env.Body.Resp.Result), &didl); err != nil {
		t.Fatalf("DIDL isn't XML: %v\n%s", err, env.Body.Resp.Result)
	}
	return env.Body.Resp.Result
}

func TestAnnotateBrowse(t *testing.T) {
	s, uid := progressServer(t)
	ctx := context.Background()
	if _, err := s.opts.Store.SaveProgress(ctx, uid, store.Progress{ItemID: "tt0063350", Watched: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.opts.Store.SaveProgress(ctx, uid, store.Progress{ItemID: "tt0903747:1:1", PositionSec: 3725}); err != nil {
		t.Fatal(err)
	}
	stranger := didlItem("Other/clip.mkv")
	body := browseBody(didlItem(film), didlItem(episode), stranger)

	didl := resultDIDL(t, s.annotateBrowse(ctx, nil, body, true))
	filmXML, epXML, _ := strings.Cut(didl, "</item>")
	if !strings.Contains(filmXML, "<dc:title>"+watchedMark+"Night (1968).mkv</dc:title>") ||
		!strings.Contains(filmXML, "<upnp:playbackCount>1</upnp:playbackCount>") ||
		strings.Contains(filmXML, "lastPlaybackPosition") {
		t.Errorf("watched film: %s", filmXML)
	}
	for _, want := range []string{"<upnp:playbackCount>0</upnp:playbackCount>", "<upnp:lastPlaybackPosition>1:02:05</upnp:lastPlaybackPosition>", "<sec:dcmInfo>BM=3725</sec:dcmInfo>"} {
		if !strings.Contains(epXML, want) {
			t.Errorf("episode lacks %s: %s", want, epXML)
		}
	}
	if strings.Contains(epXML, watchedMark) {
		t.Errorf("unwatched episode marked: %s", epXML)
	}
	if !strings.Contains(didl, stranger) || !strings.Contains(didl, `xmlns:sec="`+secNS+`"`) {
		t.Errorf("stranger changed or sec namespace missing: %s", didl)
	}

	if got := resultDIDL(t, s.annotateBrowse(ctx, nil, body, false)); strings.Contains(got, watchedMark) {
		t.Errorf("mark without WatchedMark: %s", got)
	}
	// Nothing Reelyard knows: the answer is left alone.
	if plain := browseBody(stranger); string(s.annotateBrowse(ctx, nil, plain, true)) != string(plain) {
		t.Error("answer without library files changed")
	}
}

func soapRequest(action, args string) *http.Request {
	body := `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body><u:` +
		action + ` xmlns:u="urn:schemas-upnp-org:service:ContentDirectory:1">` + args + `</u:` + action + `></s:Body></s:Envelope>`
	r := httptest.NewRequest(http.MethodPost, controlPath, strings.NewReader(body))
	r.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:ContentDirectory:1#`+action+`"`)
	return r
}

func TestProgressActions(t *testing.T) {
	s, uid := progressServer(t)
	ctx := context.Background()
	id := url.QueryEscape(film)
	call := func(action, args string) (int, string) {
		t.Helper()
		w := httptest.NewRecorder()
		if !s.serveProgressAction(w, soapRequest(action, args), nil) {
			t.Fatalf("%s not handled", action)
		}
		b, _ := io.ReadAll(w.Body)
		return w.Code, string(b)
	}
	progress := func() store.Progress {
		t.Helper()
		p, err := s.opts.Store.GetProgress(ctx, uid, "tt0063350")
		if err != nil {
			t.Fatal(err)
		}
		return *p
	}

	// Samsung bookmark mid-film: a resume point.
	if code, body := call("X_SetBookmark", `<CategoryType>1</CategoryType><RID>0</RID><ObjectID>`+id+`</ObjectID><PosSecond>1200</PosSecond>`); code != 200 || !strings.Contains(body, "X_SetBookmarkResponse") {
		t.Fatalf("bookmark: %d %s", code, body)
	}
	if p := progress(); p.PositionSec != 1200 || p.Watched || p.MediaFileID == nil || p.DurationSec != 6000 {
		t.Fatalf("after bookmark: %+v", p)
	}
	// Past 90%: watched, resumes from the start; a later 0 keeps the mark.
	call("X_SetBookmark", `<ObjectID>`+id+`</ObjectID><PosSecond>5700</PosSecond>`)
	call("X_SetBookmark", `<ObjectID>`+id+`</ObjectID><PosSecond>0</PosSecond>`)
	if p := progress(); !p.Watched || p.PositionSec != 0 {
		t.Fatalf("after end: %+v", p)
	}
	// Kodi marks unwatched with a position.
	if code, body := call("UpdateObject", `<ObjectID>`+id+`</ObjectID><CurrentTagValue>&lt;upnp:playbackCount&gt;1&lt;/upnp:playbackCount&gt;</CurrentTagValue><NewTagValue>&lt;upnp:playbackCount&gt;0&lt;/upnp:playbackCount&gt;,&lt;upnp:lastPlaybackPosition&gt;0:10:00&lt;/upnp:lastPlaybackPosition&gt;</NewTagValue>`); code != 200 {
		t.Fatalf("update: %d %s", code, body)
	}
	if p := progress(); p.Watched || p.PositionSec != 600 {
		t.Fatalf("after update: %+v", p)
	}
	// Kodi marks watched.
	call("UpdateObject", `<ObjectID>`+id+`</ObjectID><NewTagValue>&lt;upnp:playbackCount&gt;1&lt;/upnp:playbackCount&gt;</NewTagValue>`)
	if p := progress(); !p.Watched || p.PositionSec != 0 {
		t.Fatalf("after watched: %+v", p)
	}

	// Files Reelyard didn't import: bookmarks are ignored, updates refused.
	other := url.QueryEscape("Other/clip.mkv")
	if code, _ := call("X_SetBookmark", `<ObjectID>`+other+`</ObjectID><PosSecond>10</PosSecond>`); code != 200 {
		t.Errorf("bookmark on unknown file: %d", code)
	}
	if code, body := call("UpdateObject", `<ObjectID>`+other+`</ObjectID><NewTagValue>&lt;upnp:playbackCount&gt;1&lt;/upnp:playbackCount&gt;</NewTagValue>`); code != 500 || !strings.Contains(body, "<errorCode>701</errorCode>") {
		t.Errorf("update on unknown file: %d %s", code, body)
	}

	// Other actions go on to dms untouched.
	r := soapRequest("Browse", `<ObjectID>0</ObjectID>`)
	if s.serveProgressAction(httptest.NewRecorder(), r, nil) {
		t.Error("Browse handled")
	}
	if b, _ := io.ReadAll(r.Body); len(b) == 0 {
		t.Error("Browse body consumed")
	}
}

func TestParseClock(t *testing.T) {
	for in, want := range map[string]int{"0": 0, "1200": 1200, "1:02:05": 3725, "0:00:07.500": 7, " 2:00:00 ": 7200} {
		if got, ok := parseClock(in); !ok || got != want {
			t.Errorf("parseClock(%q) = %d %v, want %d", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "-5", "1:2", "a:00:00"} {
		if _, ok := parseClock(in); ok {
			t.Errorf("parseClock(%q) ok", in)
		}
	}
	if clock(3725) != "1:02:05" {
		t.Errorf("clock = %s", clock(3725))
	}
}

// Through the front and a real dms: Browse answers carry the play state
// and a TV's bookmark lands in the store.
func TestProgressThroughDMS(t *testing.T) {
	s, uid := progressServer(t)
	ctx := context.Background()
	root := library(t, film, episode)
	pipe := newPipeListener(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8200})
	d := &dms.Server{
		FS:             libraryFS(root.Name()),
		HTTPConn:       pipe,
		Interfaces:     []net.Interface{}, // no SSDP
		RootObjectPath: root.Name(),
		Logger:         slog.New(slog.DiscardHandler),
		NoTranscode:    true,
		NoProbe:        true,
		AllowedIpNets:  []*net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}},
	}
	if err := d.Init(); err != nil {
		t.Fatal(err)
	}
	go d.Run()
	t.Cleanup(func() { d.Close() })

	set := DefaultSettings
	set.WatchedMark = true
	h := s.front(&running{set: set, dms: d, pipe: pipe, root: root})
	do := func(r *http.Request) string {
		t.Helper()
		r.RemoteAddr = "192.168.1.9:4000"
		r.Host = "192.168.1.5:8200"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("%s: %d %s", r.Header.Get("SOAPACTION"), w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	browse := func() string {
		t.Helper()
		return resultDIDL(t, []byte(do(soapRequest("Browse", `<ObjectID>`+url.QueryEscape("Movies/Night (1968)")+`</ObjectID><BrowseFlag>BrowseDirectChildren</BrowseFlag><Filter>*</Filter><StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount>`))))
	}

	if got := browse(); !strings.Contains(got, "<upnp:playbackCount>0</upnp:playbackCount>") || strings.Contains(got, watchedMark) {
		t.Fatalf("before: %s", got)
	}
	do(soapRequest("X_SetBookmark", `<ObjectID>`+url.QueryEscape(film)+`</ObjectID><PosSecond>1200</PosSecond>`))
	if p, err := s.opts.Store.GetProgress(ctx, uid, "tt0063350"); err != nil || p.PositionSec != 1200 {
		t.Fatalf("bookmark not saved: %+v %v", p, err)
	}
	if got := browse(); !strings.Contains(got, "<sec:dcmInfo>BM=1200</sec:dcmInfo>") {
		t.Fatalf("resume point not shown: %s", got)
	}
	do(soapRequest("X_SetBookmark", `<ObjectID>`+url.QueryEscape(film)+`</ObjectID><PosSecond>5900</PosSecond>`))
	if got := browse(); !strings.Contains(got, "<dc:title>"+watchedMark) || !strings.Contains(got, "<upnp:playbackCount>1</upnp:playbackCount>") {
		t.Fatalf("watched not shown: %s", got)
	}

	// Turned off: dms answers as it always did.
	set.TrackProgress = false
	h = s.front(&running{set: set, dms: d, pipe: pipe, root: root})
	if got := browse(); strings.Contains(got, "playbackCount") {
		t.Fatalf("tracking off: %s", got)
	}
}
