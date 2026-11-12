package dlna

// Viewers: a DLNA server per family member.
//
// TVs don't sign in, so each viewer gets their own server ("Reelyard ·
// Kids") on its own port; the TV's list of sources is the profile picker.
// On a viewer's server:
//   - progress (watched marks, resume points) is the viewer's,
//   - a viewer limited to some titles only sees those: other folders and
//     files are dropped from Browse answers and refused when asked for
//     directly (Browse, streams, subtitles, thumbnails, bookmarks),
//   - the server is refused to TVs that aren't on its list.
//
// Every server (the main one too) also gets a "Continue watching" folder at
// the top with the person's half-watched films and episodes.
//
// To filter and page consistently, the front answers Browse itself: it asks
// dms for the whole folder, drops what the viewer can't see, adds the
// Continue watching folder, then pages and annotates the result.

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dms/soap"
	"github.com/anacrolix/dms/upnpav"

	"github.com/reelyard/server/internal/store"
)

const (
	// continueID is the Continue watching folder's ObjectID (not a path, so
	// dms never sees it).
	continueID = "reelyard-continue"
	// continueMax caps the Continue watching folder.
	continueMax = 30
	// visTTL is how long a viewer's visible files are cached: a TV fires
	// many requests while browsing.
	visTTL = 20 * time.Second
)

// ---- what a viewer may see ----

// visibility is the library paths a limited viewer may see. nil allows
// everything.
type visibility struct {
	files map[string]bool // library files of allowed titles
	dirs  map[string]bool // folders that lead to them
	// children counts the visible entries in each folder ("" is the root).
	children map[string]int
}

// allows reports whether a library-relative path ("" is the root) is
// visible: an allowed file or a folder that leads to one.
func (v *visibility) allows(rel string) bool {
	if v == nil || rel == "" || rel == "." {
		return true
	}
	return v.files[rel] || v.dirs[rel]
}

// visCache keeps a viewer server's settings and visible files for visTTL,
// so changes to a viewer's titles or TVs apply without restarting servers.
type visCache struct {
	mu       sync.Mutex
	viewer   *store.Viewer // latest settings of the server's viewer
	viewerAt time.Time
	vis      *visibility
	visAt    time.Time
}

// viewerOf returns the latest settings of server r's viewer (nil: the main
// server).
func (s *Server) viewerOf(ctx context.Context, r *running) *store.Viewer {
	if r == nil || r.viewer == nil {
		return nil
	}
	c := &r.vis
	c.mu.Lock()
	defer c.mu.Unlock()
	return s.viewerLocked(ctx, r)
}

// viewerLocked is viewerOf with r.vis.mu held.
func (s *Server) viewerLocked(ctx context.Context, r *running) *store.Viewer {
	c := &r.vis
	if c.viewer == nil {
		c.viewer, c.viewerAt = r.viewer, time.Now()
	}
	if time.Since(c.viewerAt) >= visTTL {
		if v, err := s.opts.Store.GetViewer(ctx, r.viewer.ID); err == nil {
			c.viewer = v
		}
		c.viewerAt = time.Now()
	}
	return c.viewer
}

// tvAllowed reports whether a TV may use server r.
func (s *Server) tvAllowed(ctx context.Context, r *running, ip string) bool {
	tvs := r.tvs
	if v := s.viewerOf(ctx, r); v != nil {
		tvs = v.TVs
	}
	return len(tvs) == 0 || slices.Contains(tvs, ip)
}

// refresh makes every server reread its viewer's settings and files.
func (s *Server) refresh() {
	for _, r := range s.runs {
		r.vis.mu.Lock()
		r.vis.viewerAt, r.vis.vis = time.Time{}, nil
		r.vis.mu.Unlock()
	}
}

// visibility returns what server r's viewer may see (nil: everything).
func (s *Server) visibility(ctx context.Context, r *running) *visibility {
	if r == nil || r.viewer == nil {
		return nil
	}
	c := &r.vis
	c.mu.Lock()
	defer c.mu.Unlock()
	viewer := s.viewerLocked(ctx, r)
	if viewer.AllTitles {
		return nil
	}
	if c.vis != nil && time.Since(c.visAt) < visTTL {
		return c.vis
	}
	v := &visibility{files: map[string]bool{}, dirs: map[string]bool{}, children: map[string]int{}}
	files, err := s.opts.Store.ListMediaFiles(ctx, "")
	if err != nil {
		s.opts.Log.Warn("dlna: list library", "err", err)
		return v // fail closed: nothing visible
	}
	for _, mf := range files {
		if !store.TitleAllowed(viewer.Titles, mf.TitleID) {
			continue
		}
		rel := cleanRel(mf.Path)
		v.files[rel] = true
		v.children[parentDir(rel)]++
		for d := path.Dir(rel); d != "." && d != "/" && d != ""; d = path.Dir(d) {
			if !v.dirs[d] {
				v.dirs[d] = true
				v.children[parentDir(d)]++
			}
		}
	}
	c.vis, c.visAt = v, time.Now()
	return v
}

// parentDir is a library path's folder ("" for the root).
func parentDir(rel string) string {
	d := path.Dir(rel)
	if d == "." || d == "/" {
		return ""
	}
	return d
}

// ---- Browse ----

type browseArgs struct {
	ObjectID       string
	BrowseFlag     string
	Filter         string
	StartingIndex  int
	RequestedCount int
	SortCriteria   string
}

// serveBrowse answers Browse when server r filters titles or shares
// progress. It returns false (having read nothing) otherwise.
func (s *Server) serveBrowse(w http.ResponseWriter, req *http.Request, r *running) bool {
	if cdsAction(req) != "Browse" {
		return false
	}
	ctx := req.Context()
	vis := s.visibility(ctx, r)
	if vis == nil && !r.set.TrackProgress {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(req.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	var env soap.Envelope
	var args browseArgs
	if err := xml.Unmarshal(body, &env); err != nil || xml.Unmarshal(env.Body.Action, &args) != nil {
		http.Error(w, "bad SOAP request", http.StatusBadRequest)
		return true
	}
	children := args.BrowseFlag == "BrowseDirectChildren"
	header := didlHeader
	var entries []string
	updateID := "0"

	if args.ObjectID == continueID {
		if !r.set.TrackProgress {
			writeSOAPFault(w, upnpav.NoSuchObjectErrorCode, "no such object")
			return true
		}
		items := s.continueItems(ctx, req, r, vis)
		if children {
			entries = items
		} else {
			entries = []string{continueFolder(len(items))}
		}
	} else {
		rel := ""
		if args.ObjectID != "0" {
			p, ok := objectPath(args.ObjectID)
			if !ok {
				writeSOAPFault(w, upnpav.NoSuchObjectErrorCode, "no such object")
				return true
			}
			rel = p
		}
		if !vis.allows(rel) {
			writeSOAPFault(w, upnpav.NoSuchObjectErrorCode, "no such object")
			return true
		}
		code, didl, uid, raw := s.dmsBrowse(ctx, req, r, args.ObjectID, args.BrowseFlag, args.Filter, args.SortCriteria)
		if code != http.StatusOK {
			// dms's own fault (no such folder, ...): pass it on.
			w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
			w.WriteHeader(code)
			_, _ = w.Write(raw)
			return true
		}
		updateID = uid
		var all []string
		header, all = splitDIDL(didl)
		for _, e := range all {
			p, ok := itemPath(e)
			if ok && !vis.allows(p) {
				continue
			}
			if ok && vis != nil && strings.HasPrefix(e, "<container ") {
				// Count only what the viewer sees inside.
				e = setAttr(e, "childCount", strconv.Itoa(vis.children[p]))
			}
			entries = append(entries, e)
		}
		if children && rel == "" && r.set.TrackProgress {
			if items := s.continueItems(ctx, req, r, vis); len(items) > 0 {
				entries = append([]string{continueFolder(len(items))}, entries...)
			}
		}
	}

	total := len(entries)
	if children {
		start := min(max(args.StartingIndex, 0), len(entries))
		entries = entries[start:]
		if args.RequestedCount > 0 && args.RequestedCount < len(entries) {
			entries = entries[:args.RequestedCount]
		}
	}
	didl := header + strings.Join(entries, "") + "</DIDL-Lite>"
	if r.set.TrackProgress {
		didl, _ = s.annotateResult(ctx, r, didl, r.set.WatchedMark)
	}
	writeSOAP(w, http.StatusOK, fmt.Sprintf(
		`<u:BrowseResponse xmlns:u="urn:schemas-upnp-org:service:%s:1"><Result>%s</Result><NumberReturned>%d</NumberReturned><TotalMatches>%d</TotalMatches><UpdateID>%s</UpdateID></u:BrowseResponse>`,
		cdsType, escapeDIDL(didl), len(entries), total, xmlText(updateID)))
	return true
}

// didlHeader is the DIDL-Lite opening tag dms writes.
const didlHeader = `<DIDL-Lite xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:upnp="urn:schemas-upnp-org:metadata-1-0/upnp/" xmlns="urn:schemas-upnp-org:metadata-1-0/DIDL-Lite/" xmlns:dlna="urn:schemas-dlna-org:metadata-1-0/">`

func continueFolder(n int) string {
	return fmt.Sprintf(`<container id="%s" parentID="0" restricted="1" searchable="0" childCount="%d"><dc:title>Continue watching</dc:title><upnp:class>object.container.storageFolder</upnp:class></container>`, continueID, n)
}

// continueItems are the progress user's half-watched files as DIDL items
// (as dms describes them, moved into the Continue watching folder).
func (s *Server) continueItems(ctx context.Context, req *http.Request, r *running, vis *visibility) []string {
	uid, ok := s.progressUser(ctx, r)
	if !ok {
		return nil
	}
	ps, err := s.opts.Store.ListProgress(ctx, uid, "", 200)
	if err != nil {
		return nil
	}
	var out []string
	for _, p := range ps {
		if p.Watched || p.PositionSec <= 0 || p.MediaFileID == nil {
			continue
		}
		mf, err := s.opts.Store.GetMediaFile(ctx, *p.MediaFileID)
		if err != nil {
			continue
		}
		rel := cleanRel(mf.Path)
		if !vis.allows(rel) {
			continue
		}
		code, didl, _, _ := s.dmsBrowse(ctx, req, r, url.QueryEscape(rel), "BrowseMetadata", "*", "")
		if code != http.StatusOK {
			continue // file gone from disk
		}
		if _, es := splitDIDL(didl); len(es) == 1 && strings.HasPrefix(es[0], "<item ") {
			out = append(out, setParent(es[0], continueID))
		}
		if len(out) == continueMax {
			break
		}
	}
	return out
}

// dmsBrowse asks dms for a whole object (StartingIndex 0, every child) as
// the TV would, and returns the HTTP status, the DIDL-Lite, the UpdateID and
// the raw answer.
func (s *Server) dmsBrowse(ctx context.Context, tv *http.Request, r *running, objectID, flag, filter, sort string) (int, string, string, []byte) {
	body := `<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>` +
		`<u:Browse xmlns:u="urn:schemas-upnp-org:service:` + cdsType + `:1"><ObjectID>` + xmlText(objectID) + `</ObjectID><BrowseFlag>` + xmlText(flag) +
		`</BrowseFlag><Filter>` + xmlText(filter) + `</Filter><StartingIndex>0</StartingIndex><RequestedCount>0</RequestedCount><SortCriteria>` + xmlText(sort) +
		`</SortCriteria></u:Browse></s:Body></s:Envelope>`
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://dms"+controlPath, strings.NewReader(body))
	if err != nil {
		return http.StatusInternalServerError, "", "", nil
	}
	req.Host = tv.Host // dms builds item URLs from it
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPACTION", `"urn:schemas-upnp-org:service:`+cdsType+`:1#Browse"`)
	req.Header.Set("User-Agent", tv.UserAgent()) // dms adapts some answers to the TV
	resp, err := r.transport.RoundTrip(req)
	if err != nil {
		return http.StatusBadGateway, "", "", nil
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return http.StatusBadGateway, "", "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, "", "", raw
	}
	var env struct {
		Body struct {
			Resp struct {
				Result   string
				UpdateID string
			} `xml:",any"`
		}
	}
	if err := xml.Unmarshal(raw, &env); err != nil {
		return http.StatusBadGateway, "", "", raw
	}
	uid := env.Body.Resp.UpdateID
	if uid == "" {
		uid = "0"
	}
	return http.StatusOK, env.Body.Resp.Result, uid, raw
}

// splitDIDL splits a DIDL-Lite document into its opening tag and its
// top-level <container> and <item> elements (they don't nest in Browse
// answers).
func splitDIDL(didl string) (string, []string) {
	header := didlHeader
	rest := didl
	if i := strings.Index(rest, "<DIDL-Lite"); i >= 0 {
		if j := strings.IndexByte(rest[i:], '>'); j >= 0 {
			header = rest[i : i+j+1]
			rest = rest[i+j+1:]
		}
	}
	var out []string
	for {
		ci, ii := strings.Index(rest, "<container "), strings.Index(rest, "<item ")
		start, closer := ci, "</container>"
		if ci < 0 || (ii >= 0 && ii < ci) {
			start, closer = ii, "</item>"
		}
		if start < 0 {
			break
		}
		end := strings.Index(rest[start:], closer)
		if end < 0 {
			break
		}
		end += start + len(closer)
		out = append(out, rest[start:end])
		rest = rest[end:]
	}
	return header, out
}

// setParent replaces an element's parentID.
func setParent(e, parent string) string { return setAttr(e, "parentID", parent) }

// setAttr replaces an attribute's value in an element's opening tag (if
// the attribute is there).
func setAttr(e, name, value string) string {
	tagEnd := strings.IndexByte(e, '>')
	if tagEnd < 0 {
		return e
	}
	attr := " " + name + `="`
	i := strings.Index(e[:tagEnd], attr)
	if i < 0 {
		return e
	}
	j := strings.IndexByte(e[i+len(attr):], '"')
	if j < 0 {
		return e
	}
	return e[:i+len(attr)] + value + e[i+len(attr)+j:]
}

func xmlText(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// ---- seen TVs ----

// TV is a device that used one of the DLNA servers, so people can pick it
// in "Show on these TVs".
type TV struct {
	Address  string    `json:"address"`
	Name     string    `json:"name,omitempty"` // given in Reelyard
	Model    string    `json:"model"`          // from the device's User-Agent
	LastSeen time.Time `json:"lastSeen"`
}

const (
	tvsKey      = "dlna_tvs"
	tvsMax      = 100
	tvSaveEvery = 10 * time.Minute
)

type tvRegistry struct {
	mu    sync.Mutex
	st    *store.Store
	log   interface{ Warn(string, ...any) }
	tvs   map[string]*TV
	saved map[string]time.Time
}

func loadTVs(ctx context.Context, st *store.Store, log interface{ Warn(string, ...any) }) *tvRegistry {
	t := &tvRegistry{st: st, log: log, tvs: map[string]*TV{}, saved: map[string]time.Time{}}
	var list []*TV
	if st != nil {
		_ = st.GetSetting(ctx, tvsKey, &list)
	}
	for _, tv := range list {
		t.tvs[tv.Address] = tv
		t.saved[tv.Address] = tv.LastSeen
	}
	return t
}

// seen records a request from a TV. It's saved when the TV is new, its
// model changed, or every tvSaveEvery.
func (t *tvRegistry) seen(ip, userAgent string) {
	if ip == "" {
		return
	}
	model := tvModel(userAgent)
	now := time.Now()
	t.mu.Lock()
	tv := t.tvs[ip]
	save := false
	if tv == nil {
		tv = &TV{Address: ip}
		t.tvs[ip] = tv
		save = true
	}
	if model != "" && model != tv.Model {
		tv.Model, save = model, true
	}
	tv.LastSeen = now
	if now.Sub(t.saved[ip]) > tvSaveEvery {
		save = true
	}
	if save {
		t.saved[ip] = now
		t.trim()
	}
	list := t.listLocked()
	t.mu.Unlock()
	if save {
		go t.save(list)
	}
}

// trim drops the longest-unseen TVs beyond tvsMax. Caller holds t.mu.
func (t *tvRegistry) trim() {
	for len(t.tvs) > tvsMax {
		var oldest *TV
		for _, tv := range t.tvs {
			if oldest == nil || tv.LastSeen.Before(oldest.LastSeen) {
				oldest = tv
			}
		}
		delete(t.tvs, oldest.Address)
		delete(t.saved, oldest.Address)
	}
}

func (t *tvRegistry) listLocked() []TV {
	out := make([]TV, 0, len(t.tvs))
	for _, tv := range t.tvs {
		out = append(out, *tv)
	}
	// Most recently seen first.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].LastSeen.After(out[j-1].LastSeen); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (t *tvRegistry) save(list []TV) {
	if t.st == nil {
		return
	}
	if err := t.st.SetSetting(context.Background(), tvsKey, list); err != nil && t.log != nil {
		t.log.Warn("dlna: save TVs", "err", err)
	}
}

// TVs lists the devices that used the DLNA servers, most recent first.
func (s *Server) TVs() []TV {
	s.tvs.mu.Lock()
	defer s.tvs.mu.Unlock()
	return s.tvs.listLocked()
}

// NameTV gives a TV a name ("" clears it). It reports whether the TV is known.
func (s *Server) NameTV(address, name string) bool {
	s.tvs.mu.Lock()
	tv := s.tvs.tvs[address]
	if tv == nil {
		s.tvs.mu.Unlock()
		return false
	}
	tv.Name = strings.TrimSpace(name)
	if len([]rune(tv.Name)) > 64 {
		tv.Name = string([]rune(tv.Name)[:64])
	}
	list := s.tvs.listLocked()
	s.tvs.mu.Unlock()
	s.tvs.save(list)
	return true
}

// ForgetTV removes a TV from the list (it comes back if it's used again).
func (s *Server) ForgetTV(address string) bool {
	s.tvs.mu.Lock()
	_, ok := s.tvs.tvs[address]
	delete(s.tvs.tvs, address)
	delete(s.tvs.saved, address)
	list := s.tvs.listLocked()
	s.tvs.mu.Unlock()
	if ok {
		s.tvs.save(list)
	}
	return ok
}

// tvModel makes a readable device name from a User-Agent, e.g.
// "SEC_HHP_[TV] Samsung Q80 Series (55)/1.0 DLNADOC/1.50" → "Samsung Q80
// Series (55)".
func tvModel(ua string) string {
	ua = strings.TrimSpace(ua)
	if i := strings.Index(ua, "[TV] "); i >= 0 {
		m := ua[i+len("[TV] "):]
		if j := strings.IndexByte(m, '/'); j > 0 {
			m = m[:j]
		}
		return strings.TrimSpace(m)
	}
	m := ua
	if j := strings.IndexAny(m, "/ "); j > 0 {
		m = m[:j]
	}
	if len(m) > 48 {
		m = m[:48]
	}
	return m
}
