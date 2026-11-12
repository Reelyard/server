package dlna

import (
	"context"
	"encoding/xml"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/anacrolix/dms/dlna/dms"

	"github.com/reelyard/server/internal/store"
)

// A viewer's server, through the front and a real dms: only their titles,
// their own resume points, a Continue watching folder, and only on their TVs.
func TestViewerServer(t *testing.T) {
	s, admin := progressServer(t)
	ctx := context.Background()
	st := s.opts.Store
	s.tvs = loadTVs(ctx, st, nil)
	kids := &store.Viewer{Name: "Kids", Titles: []string{"tt0903747"}, DLNA: true, TVs: []string{"192.168.1.20"}}
	if err := st.CreateViewer(ctx, kids); err != nil {
		t.Fatal(err)
	}
	root := library(t, film, episode)
	pipe := newPipeListener(&net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 8201})
	d := &dms.Server{
		FS:             libraryFS(root.Name()),
		HTTPConn:       pipe,
		Interfaces:     []net.Interface{},
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

	kidsRun := &running{set: DefaultSettings, dms: d, pipe: pipe, root: root, viewer: kids, tvs: kids.TVs}
	h := s.front(kidsRun)
	send := func(from string, r *http.Request) *httptest.ResponseRecorder {
		t.Helper()
		r.RemoteAddr = from + ":4000"
		r.Host = "192.168.1.5:8201"
		r.Header.Set("User-Agent", "DLNADOC/1.50 SEC_HHP_[TV] Samsung Q80 Series (55)/1.0")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	const tv = "192.168.1.20"
	browse := func(id, flag string, start, count int) (int, browseResult) {
		t.Helper()
		w := send(tv, soapRequest("Browse", `<ObjectID>`+id+`</ObjectID><BrowseFlag>`+flag+`</BrowseFlag><Filter>*</Filter><StartingIndex>`+strconv.Itoa(start)+`</StartingIndex><RequestedCount>`+strconv.Itoa(count)+`</RequestedCount>`))
		if w.Code != 200 {
			return w.Code, browseResult{}
		}
		return 200, parseBrowse(t, w.Body.Bytes())
	}

	// Another TV can't use the Kids server, but shows up in the TV list.
	if w := send("192.168.1.9", httptest.NewRequest("GET", "/rootDesc.xml", nil)); w.Code != http.StatusForbidden {
		t.Fatalf("other TV: %d", w.Code)
	}
	if tvs := s.TVs(); len(tvs) != 1 || tvs[0].Address != "192.168.1.9" || tvs[0].Model != "Samsung Q80 Series (55)" || tvs[0].Agent == "" {
		t.Fatalf("seen TVs: %+v", tvs)
	}
	// This machine isn't a TV.
	send("127.0.0.1", httptest.NewRequest("GET", "/rootDesc.xml", nil))
	if tvs := s.TVs(); len(tvs) != 1 {
		t.Fatalf("loopback recorded: %+v", tvs)
	}

	// Root: only the series' folder, no films.
	_, res := browse("0", "BrowseDirectChildren", 0, 0)
	if res.Total != 1 || !strings.Contains(res.DIDL, `<dc:title>TV</dc:title>`) || strings.Contains(res.DIDL, "Movies") {
		t.Fatalf("root: %+v", res)
	}
	// The films folder and file are out of reach.
	for _, id := range []string{url.QueryEscape("Movies"), url.QueryEscape(film)} {
		if code, _ := browse(id, "BrowseMetadata", 0, 0); code != http.StatusInternalServerError {
			t.Fatalf("hidden %s: %d", id, code)
		}
	}
	if w := send(tv, httptest.NewRequest("GET", "/res?path="+url.QueryEscape(film), nil)); w.Code != http.StatusNotFound {
		t.Fatalf("hidden stream: %d", w.Code)
	}
	if w := send(tv, httptest.NewRequest("GET", "/res?path="+url.QueryEscape(episode), nil)); w.Code != http.StatusOK {
		t.Fatalf("allowed stream: %d", w.Code)
	}
	if w := send(tv, soapRequest("X_SetBookmark", `<ObjectID>`+url.QueryEscape(film)+`</ObjectID><PosSecond>60</PosSecond>`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("bookmark on a hidden file: %d", w.Code)
	}

	// A bookmark is the viewer's, not the admin's.
	if w := send(tv, soapRequest("X_SetBookmark", `<ObjectID>`+url.QueryEscape(episode)+`</ObjectID><PosSecond>300</PosSecond>`)); w.Code != 200 {
		t.Fatalf("bookmark: %d %s", w.Code, w.Body.String())
	}
	if p, err := st.GetProgress(ctx, kids.ID, "tt0903747:1:1"); err != nil || p.PositionSec != 300 {
		t.Fatalf("viewer progress: %+v %v", p, err)
	}
	if _, err := st.GetProgress(ctx, admin, "tt0903747:1:1"); err == nil {
		t.Fatal("bookmark went to the admin")
	}

	// Continue watching appears first at the root, with the episode inside.
	_, res = browse("0", "BrowseDirectChildren", 0, 0)
	if res.Total != 2 || !strings.HasPrefix(strings.TrimPrefix(res.DIDL, res.header()), `<container id="`+continueID+`"`) || !strings.Contains(res.DIDL, `childCount="1"`) {
		t.Fatalf("root with continue: %+v", res)
	}
	_, res = browse(continueID, "BrowseDirectChildren", 0, 0)
	if res.Total != 1 || !strings.Contains(res.DIDL, `parentID="`+continueID+`"`) || !strings.Contains(res.DIDL, "<sec:dcmInfo>BM=300</sec:dcmInfo>") ||
		!strings.Contains(res.DIDL, "/res?path=") {
		t.Fatalf("continue folder: %+v", res)
	}
	if _, res = browse(continueID, "BrowseMetadata", 0, 0); !strings.Contains(res.DIDL, "<dc:title>Continue watching</dc:title>") {
		t.Fatalf("continue metadata: %+v", res)
	}
	// The folder reports the library's UpdateID (TVs reload when it differs).
	_, rootRes := browse("0", "BrowseDirectChildren", 0, 0)
	if res.UpdateID == "" || res.UpdateID == "0" && rootRes.UpdateID != "0" || res.UpdateID != rootRes.UpdateID {
		t.Fatalf("continue UpdateID %q, library %q", res.UpdateID, rootRes.UpdateID)
	}
	// Paging counts what the viewer can see.
	if _, res = browse("0", "BrowseDirectChildren", 1, 1); res.Returned != 1 || res.Total != 2 || !strings.Contains(res.DIDL, "<dc:title>TV</dc:title>") {
		t.Fatalf("paged: %+v", res)
	}

	// The main server (no viewer) still shows everything, with the admin's
	// progress: no Continue watching folder, since the admin has none.
	mainRun := &running{set: DefaultSettings, dms: d, pipe: pipe, root: root}
	h = s.front(mainRun)
	if _, res = browse("0", "BrowseDirectChildren", 0, 0); res.Total != 2 || !strings.Contains(res.DIDL, "Movies") || strings.Contains(res.DIDL, continueID) {
		t.Fatalf("main root: %+v", res)
	}
}

type browseResult struct {
	DIDL     string
	Returned int
	Total    int
	UpdateID string
}

func (b browseResult) header() string {
	h, _ := splitDIDL(b.DIDL)
	return h
}

func parseBrowse(t *testing.T, body []byte) browseResult {
	t.Helper()
	var env struct {
		Body struct {
			Resp struct {
				Result         string
				NumberReturned int
				UpdateID       string
				TotalMatches   int
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
	return browseResult{env.Body.Resp.Result, env.Body.Resp.NumberReturned, env.Body.Resp.TotalMatches, env.Body.Resp.UpdateID}
}

func TestTVModel(t *testing.T) {
	for ua, want := range map[string]string{
		"SEC_HHP_[TV] UE40ES6800/1.0 DLNADOC/1.50":              "UE40ES6800",
		"DLNADOC/1.50 SEC_HHP_[TV] Samsung Q80 Series (55)/1.0": "Samsung Q80 Series (55)",
		"VLC/3.0.20 LibVLC/3.0.20":                              "VLC",
		"DLNADOC/1.50 SEC_HHP_Living room/1.0":                  "Living room",
		"DLNADOC/1.50 SEC_HHP_/1.0 UPnP/1.0":                    "Samsung TV",
		"Linux/5.4 UPnP/1.0 Kodi/21.0":                          "Kodi",
		"Microsoft-Windows/10.0 UPnP/1.0":                       "Microsoft-Windows",
		"":                                                      "",
	} {
		if got := tvModel(ua); got != want {
			t.Errorf("%q: %q, want %q", ua, got, want)
		}
	}
}

func TestSplitDIDL(t *testing.T) {
	didl := didlHeader + `<container id="a" parentID="0"><dc:title>A</dc:title></container><item id="b" parentID="a"><dc:title>B</dc:title></item></DIDL-Lite>`
	h, es := splitDIDL(didl)
	if h != didlHeader || len(es) != 2 || !strings.HasPrefix(es[1], `<item id="b"`) {
		t.Fatalf("%q %q", h, es)
	}
	if got := setParent(es[1], "x"); !strings.Contains(got, `parentID="x"`) {
		t.Fatal(got)
	}
}
