package dlna

// Watched marks and resume points over DLNA.
//
// dms knows nothing of Reelyard's progress table, so the front does two
// things on the ContentDirectory control URL:
//   - Browse answers get, per video Reelyard imported, upnp:playbackCount
//     (1 once watched), upnp:lastPlaybackPosition and Samsung's
//     sec:dcmInfo BM= resume point, plus an optional check mark in the
//     title for TVs that ignore those fields.
//   - Samsung's X_SetBookmark and the standard UpdateObject (Kodi sends
//     playbackCount and lastPlaybackPosition with it) are answered here
//     and saved as the account's progress, instead of being dropped.
//
// DLNA devices don't sign in: the main server's progress is the first
// (admin) account's, and each viewer's server keeps that viewer's.

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/anacrolix/dms/soap"
	"github.com/anacrolix/dms/upnp"
	"github.com/anacrolix/dms/upnpav"

	"github.com/reelyard/server/internal/store"
)

const (
	cdsType     = "ContentDirectory"
	controlPath = "/ctl"
	secNS       = "http://www.sec.co.kr/"
	// watchedMark prefixes watched titles when WatchedMark is on.
	watchedMark = "✓ "
)

// cdsAction returns the ContentDirectory action a control request calls ("" if none).
func cdsAction(r *http.Request) string {
	if r.Method != http.MethodPost || r.URL.Path != controlPath {
		return ""
	}
	sa, err := upnp.ParseActionHTTPHeader(r.Header.Get("SOAPACTION"))
	if err != nil || sa.Type != cdsType {
		return ""
	}
	return sa.Action
}

// progressUser is whose progress DLNA devices see and update on server r:
// its viewer's, or the admin's on the main server (r may be nil).
func (s *Server) progressUser(ctx context.Context, r *running) (int64, bool) {
	if r != nil && r.viewer != nil {
		return r.viewer.ID, true // fixed for the server's life
	}
	u, err := s.opts.Store.FirstUser(ctx)
	if err != nil {
		return 0, false
	}
	return u.ID, true
}

// ---- reading: annotate Browse answers ----

// annotateBrowse adds play state to the DIDL-Lite in a Browse SOAP answer.
// Anything it can't make sense of is returned unchanged.
func (s *Server) annotateBrowse(ctx context.Context, r *running, body []byte, mark bool) []byte {
	start := bytes.Index(body, []byte("<Result>"))
	end := bytes.Index(body, []byte("</Result>"))
	if start < 0 || end < start {
		return body
	}
	inner := body[start+len("<Result>") : end]
	var result struct {
		DIDL string `xml:",chardata"`
	}
	if err := xml.Unmarshal(append(append([]byte("<Result>"), inner...), "</Result>"...), &result); err != nil {
		return body
	}
	didl, changed := s.annotateResult(ctx, r, result.DIDL, mark)
	if !changed {
		return body
	}
	escaped := escapeDIDL(didl)
	out := make([]byte, 0, len(body)+len(escaped)-len(inner))
	out = append(out, body[:start+len("<Result>")]...)
	out = append(out, escaped...)
	return append(out, body[end:]...)
}

// annotateResult adds the progress user's play state to a DIDL-Lite document.
func (s *Server) annotateResult(ctx context.Context, r *running, didl string, mark bool) (string, bool) {
	uid, ok := s.progressUser(ctx, r)
	if !ok {
		return didl, false
	}
	ps, err := s.opts.Store.ListProgress(ctx, uid, "", 0)
	if err != nil {
		return didl, false
	}
	byItem := make(map[string]*store.Progress, len(ps))
	for _, p := range ps {
		byItem[p.ItemID] = p
	}
	return annotateDIDL(didl, mark, func(rel string) (*store.Progress, bool) {
		mf, err := s.opts.Store.MediaFileByPath(ctx, rel)
		if err != nil {
			return nil, false
		}
		if p, ok := byItem[mf.TitleID]; ok {
			return p, true
		}
		return &store.Progress{ItemID: mf.TitleID}, true
	})
}

// escapeDIDL escapes DIDL-Lite for a SOAP <Result>.
func escapeDIDL(didl string) []byte {
	var esc bytes.Buffer
	_ = xml.EscapeText(&esc, []byte(didl))
	// dms un-escapes quotes in its answers for Samsung Frame TVs; keep that.
	return bytes.ReplaceAll(esc.Bytes(), []byte("&#34;"), []byte(`"`))
}

// annotateDIDL adds play state to each video item lookup knows (by its
// library-relative path).
func annotateDIDL(didl string, mark bool, lookup func(rel string) (*store.Progress, bool)) (string, bool) {
	var out strings.Builder
	changed, sec := false, false
	rest := didl
	for {
		i := strings.Index(rest, "<item ")
		if i < 0 {
			break
		}
		j := strings.Index(rest[i:], "</item>")
		if j < 0 {
			break
		}
		item := rest[i : i+j]
		out.WriteString(rest[:i])
		rest = rest[i+j:]
		rel, ok := itemPath(item)
		if !ok || !strings.Contains(item, "<upnp:class>object.item.videoItem") {
			out.WriteString(item)
			continue
		}
		p, ok := lookup(rel)
		if !ok {
			out.WriteString(item)
			continue
		}
		changed = true
		if mark && p.Watched {
			item = strings.Replace(item, "<dc:title>", "<dc:title>"+watchedMark, 1)
		}
		out.WriteString(item)
		count := 0
		if p.Watched {
			count = 1
		}
		fmt.Fprintf(&out, "<upnp:playbackCount>%d</upnp:playbackCount>", count)
		if !p.Watched && p.PositionSec > 0 {
			fmt.Fprintf(&out, "<upnp:lastPlaybackPosition>%s</upnp:lastPlaybackPosition>", clock(p.PositionSec))
			fmt.Fprintf(&out, "<sec:dcmInfo>BM=%d</sec:dcmInfo>", p.PositionSec)
			sec = true
		}
	}
	out.WriteString(rest)
	res := out.String()
	if sec && !strings.Contains(res, `xmlns:sec=`) {
		res = strings.Replace(res, "<DIDL-Lite", `<DIDL-Lite xmlns:sec="`+secNS+`"`, 1)
	}
	return res, changed
}

// itemPath returns the library-relative path of a DIDL item (its id).
func itemPath(item string) (string, bool) {
	const attr = `id="`
	i := strings.Index(item, attr)
	if i < 0 {
		return "", false
	}
	j := strings.IndexByte(item[i+len(attr):], '"')
	if j < 0 {
		return "", false
	}
	return objectPath(html.UnescapeString(item[i+len(attr) : i+len(attr)+j]))
}

// objectPath turns a dms ObjectID into a library-relative file path.
func objectPath(id string) (string, bool) {
	p, err := url.QueryUnescape(id)
	if err != nil {
		return "", false
	}
	rel := cleanRel(p)
	if rel == "" || rel == "." || rel == "0" {
		return "", false
	}
	return rel, true
}

// clock formats seconds as H:MM:SS (the DIDL duration format).
func clock(sec int) string {
	return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec/60%60, sec%60)
}

// parseClock reads seconds, or H:MM:SS with optional fractions.
func parseClock(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if n, err := strconv.Atoi(v); err == nil {
		return n, n >= 0
	}
	parts := strings.Split(v, ":")
	if len(parts) != 3 {
		return 0, false
	}
	total := 0
	for i, part := range parts {
		if i == 2 {
			part, _, _ = strings.Cut(part, ".")
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 {
			return 0, false
		}
		total = total*60 + n
	}
	return total, true
}

// ---- writing: bookmarks and UpdateObject ----

var (
	errNoSuchObject  = errors.New("no such object")
	tagPlaybackCount = regexp.MustCompile(`<upnp:playbackCount>\s*([0-9]+)\s*</upnp:playbackCount>`)
	tagLastPosition  = regexp.MustCompile(`<upnp:lastPlaybackPosition>\s*([0-9:.]+)\s*</upnp:lastPlaybackPosition>`)
)

// serveProgressAction answers X_SetBookmark and UpdateObject. It returns
// false (having read nothing) for any other request.
func (s *Server) serveProgressAction(w http.ResponseWriter, r *http.Request, run *running) bool {
	action := cdsAction(r)
	if action != "X_SetBookmark" && action != "UpdateObject" {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return true
	}
	var env soap.Envelope
	var args struct {
		ObjectID        string
		PosSecond       string
		CurrentTagValue string
		NewTagValue     string
	}
	if err := xml.Unmarshal(body, &env); err != nil || xml.Unmarshal(env.Body.Action, &args) != nil {
		http.Error(w, "bad SOAP request", http.StatusBadRequest)
		return true
	}
	rel, ok := objectPath(args.ObjectID)
	if !ok || !s.visibility(r.Context(), run).allows(rel) {
		writeSOAPFault(w, upnpav.NoSuchObjectErrorCode, "no such object")
		return true
	}
	switch action {
	case "X_SetBookmark":
		pos, ok := parseClock(args.PosSecond)
		if !ok {
			writeSOAPFault(w, upnp.ArgumentValueInvalidErrorCode, "PosSecond must be a number of seconds")
			return true
		}
		err = s.savePlayState(r.Context(), run, rel, func(p *store.Progress, dur int) {
			if pos == 0 && p.Watched {
				return // some TVs report 0 after the credits; keep the mark
			}
			p.PositionSec = pos
			p.Watched = watchedBy(pos, dur)
		})
		if errors.Is(err, errNoSuchObject) {
			err = nil // not one of Reelyard's files: nothing to keep, like before
		}
	case "UpdateObject":
		count, pos := -1, -1
		if m := tagPlaybackCount.FindStringSubmatch(args.NewTagValue); m != nil {
			count, _ = strconv.Atoi(m[1])
		}
		if m := tagLastPosition.FindStringSubmatch(args.NewTagValue); m != nil {
			if n, ok := parseClock(m[1]); ok {
				pos = n
			}
		}
		if count < 0 && pos < 0 {
			break // nothing Reelyard keeps
		}
		err = s.savePlayState(r.Context(), run, rel, func(p *store.Progress, dur int) {
			if pos >= 0 {
				p.PositionSec = pos
				p.Watched = watchedBy(pos, dur)
			}
			if count >= 0 {
				p.Watched = count > 0
			}
		})
		if errors.Is(err, errNoSuchObject) {
			writeSOAPFault(w, upnpav.NoSuchObjectErrorCode, "not a library file Reelyard tracks")
			return true
		}
	}
	if err != nil {
		s.opts.Log.Warn("dlna: save progress", "path", rel, "err", err)
		writeSOAPFault(w, upnp.ActionFailedErrorCode, "couldn't save")
		return true
	}
	writeSOAP(w, http.StatusOK, fmt.Sprintf(`<u:%[1]sResponse xmlns:u="urn:schemas-upnp-org:service:%[2]s:1"></u:%[1]sResponse>`, action, cdsType))
	return true
}

func watchedBy(pos, dur int) bool {
	return dur > 0 && float64(pos) >= store.WatchedAt*float64(dur)
}

// savePlayState loads the account's progress for a library file, lets
// apply change it, and saves it. A watched item resumes from the start.
func (s *Server) savePlayState(ctx context.Context, r *running, rel string, apply func(p *store.Progress, durationSec int)) error {
	mf, err := s.opts.Store.MediaFileByPath(ctx, rel)
	if errors.Is(err, store.ErrNotFound) {
		return errNoSuchObject
	}
	if err != nil {
		return err
	}
	uid, ok := s.progressUser(ctx, r)
	if !ok {
		return errors.New("no account yet")
	}
	p := store.Progress{ItemID: mf.TitleID}
	if old, err := s.opts.Store.GetProgress(ctx, uid, mf.TitleID); err == nil {
		p = *old
	} else if !errors.Is(err, store.ErrNotFound) {
		return err
	}
	p.MediaFileID = &mf.ID
	dur := mf.DurationSec
	if dur == 0 {
		dur = p.DurationSec
	}
	p.DurationSec = dur
	apply(&p, dur)
	if p.Watched {
		p.PositionSec = 0
	}
	_, err = s.opts.Store.SaveProgress(ctx, uid, p)
	return err
}

func writeSOAP(w http.ResponseWriter, code int, inner string) {
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	w.Header().Set("Ext", "")
	w.WriteHeader(code)
	_, _ = io.WriteString(w, `<?xml version="1.0" encoding="utf-8" standalone="yes"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`+inner+`</s:Body></s:Envelope>`)
}

func writeSOAPFault(w http.ResponseWriter, code uint, desc string) {
	b, _ := xml.Marshal(soap.NewFault("UPnPError", upnp.Error{Code: code, Desc: desc}))
	writeSOAP(w, http.StatusInternalServerError, string(b))
}
