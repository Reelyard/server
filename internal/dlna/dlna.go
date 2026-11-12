// Package dlna serves the library to TVs and players on the local network
// as a DLNA/UPnP media server (built on github.com/anacrolix/dms).
//
// dms does the discovery (SSDP) and the content directory. Reelyard puts a
// small HTTP front in front of it that:
//   - only answers devices on local networks,
//   - serves subtitles: Reelyard saves them as "Movie (1999).en.srt", while
//     dms only looks for "Movie (1999).srt" (and relative to the working
//     directory), so the front picks the right file in the preferred
//     language order,
//   - tells Samsung TVs where the subtitle is (CaptionInfo.sec),
//   - shares watched marks and resume points (see progress.go),
//   - runs one more server per viewer (family member) with their own
//     titles and resume points, shown only on the TVs chosen for them
//     (see viewers.go).
package dlna

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/dms/dlna/dms"

	"github.com/reelyard/server/internal/store"
)

// Options configure the server.
type Options struct {
	Store      *store.Store
	LibraryDir string
	Log        *slog.Logger
	// SubtitleLangs is the preferred subtitle language order.
	SubtitleLangs func() []string
	// ExcludeInterface is never announced on (the VPN interface torrents
	// are bound to: the library shouldn't be offered there).
	ExcludeInterface string
}

// Status describes the running server.
type Status struct {
	Running bool `json:"running"`
	// Addresses are the URLs TVs reach the server at.
	Addresses []string `json:"addresses"`
	Error     string   `json:"error,omitempty"`
	// FFmpeg / FFprobe report whether the tools were found (transcoding
	// and durations need them).
	FFmpeg  bool `json:"ffmpeg"`
	FFprobe bool `json:"ffprobe"`
	// Servers are the main server and one per viewer, as TVs list them.
	Servers []ServerStatus `json:"servers"`
}

// ServerStatus is one of the DLNA servers Reelyard announces.
type ServerStatus struct {
	Name string `json:"name"`
	Port int    `json:"port"`
	// ViewerID is the viewer the server belongs to (0: the main server).
	ViewerID int64  `json:"viewerId,omitempty"`
	Running  bool   `json:"running"`
	Error    string `json:"error,omitempty"`
}

// Interface is a network the server can announce on.
type Interface struct {
	Name  string   `json:"name"`
	Addrs []string `json:"addrs"`
	// Virtual is a container or VM bridge, skipped unless chosen.
	Virtual bool `json:"virtual"`
}

// Server manages the DLNA server's lifecycle.
type Server struct {
	opts Options

	mu     sync.Mutex
	set    Settings
	runs   []*running
	status Status
	ffmpeg string
	tvs    *tvRegistry
}

type running struct {
	set    Settings
	dms    *dms.Server
	front  *http.Server
	pipe   *pipeListener
	root   *os.Root
	thumbs *thumbs
	done   chan struct{}
	// viewer owns this server (nil: the main server, the admin's).
	viewer *store.Viewer
	// tvs are the TV addresses allowed to use the server; empty: every TV.
	tvs []string
	// transport reaches dms over the pipe (set by front).
	transport http.RoundTripper
	vis       visCache
}

// New loads the saved settings. Call Start to begin serving.
func New(ctx context.Context, opts Options) *Server {
	if opts.Log == nil {
		opts.Log = slog.Default()
	}
	if opts.SubtitleLangs == nil {
		opts.SubtitleLangs = func() []string { return nil }
	}
	return &Server{opts: opts, set: LoadSettings(ctx, opts.Store), tvs: loadTVs(ctx, opts.Store, opts.Log)}
}

// Start serves with the saved settings. A failure (port in use, no
// network) is kept in Status rather than stopping Reelyard.
func (s *Server) Start() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.restart()
}

// Settings returns the current settings.
func (s *Server) Settings() Settings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.set
}

// SetSettings saves new settings and restarts the server with them.
func (s *Server) SetSettings(ctx context.Context, set Settings) (Settings, error) {
	set, err := SaveSettings(ctx, s.opts.Store, set)
	if err != nil {
		return set, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set = set
	s.restart()
	return set, nil
}

// Status reports whether the server runs and where.
func (s *Server) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.status
	st.Addresses = slices.Clone(st.Addresses)
	st.Servers = slices.Clone(st.Servers)
	return st
}

// Close stops the server.
func (s *Server) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stop()
}

// Interfaces lists the networks the server can announce on.
func (s *Server) Interfaces() []Interface {
	out := []Interface{}
	for _, ifc := range candidates(s.opts.ExcludeInterface) {
		out = append(out, Interface{Name: ifc.Name, Addrs: ipv4s(ifc), Virtual: virtual(ifc.Name)})
	}
	return out
}

// candidates are up, multicast-capable, non-loopback interfaces with IPv4.
func candidates(exclude string) []net.Interface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.Interface
	for _, ifc := range ifs {
		if ifc.Name == exclude || ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 ||
			ifc.Flags&net.FlagMulticast == 0 || len(ipv4s(ifc)) == 0 {
			continue
		}
		out = append(out, ifc)
	}
	return out
}

// virtual reports container and VM bridges, skipped unless chosen.
func virtual(name string) bool {
	for _, p := range []string{"docker", "br-", "veth", "virbr", "vmnet", "vboxnet", "lxcbr", "lxdbr", "cni", "flannel", "podman"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func ipv4s(ifc net.Interface) []string {
	addrs, _ := ifc.Addrs()
	var out []string
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil {
			out = append(out, ipn.IP.String())
		}
	}
	return out
}

// restart stops the running servers and starts the main one and one per
// viewer with s.set. Caller holds s.mu.
func (s *Server) restart() {
	s.stop()
	ffmpeg, ffmpegErr := exec.LookPath("ffmpeg")
	s.ffmpeg = ffmpeg
	_, ffprobeErr := exec.LookPath("ffprobe")
	s.status = Status{Addresses: []string{}, Servers: []ServerStatus{}, FFmpeg: ffmpegErr == nil, FFprobe: ffprobeErr == nil}
	if !s.set.Enabled {
		return
	}
	ifcs, err := s.interfaces()
	if err != nil {
		s.status.Error = err.Error()
		s.opts.Log.Warn("dlna: not started", "err", err)
		return
	}
	for i, spec := range s.specs() {
		ss := ServerStatus{Name: spec.name, Port: spec.port}
		if spec.viewer != nil {
			ss.ViewerID = spec.viewer.ID
		}
		r, err := s.start(spec, ifcs)
		if err != nil {
			ss.Error = err.Error()
			s.opts.Log.Warn("dlna: not started", "name", spec.name, "err", err)
			if i == 0 {
				s.status.Error = err.Error()
			}
		} else {
			ss.Running = true
			s.runs = append(s.runs, r)
			if i == 0 {
				s.status.Running = true
				for _, ifc := range ifcs {
					for _, ip := range ipv4s(ifc) {
						s.status.Addresses = append(s.status.Addresses, fmt.Sprintf("http://%s:%d/", ip, spec.port))
					}
				}
			}
		}
		s.status.Servers = append(s.status.Servers, ss)
	}
}

// specs are the servers to run: the main one, then one per viewer with DLNA.
// Caller holds s.mu.
func (s *Server) specs() []serverSpec {
	specs := []serverSpec{{name: s.set.Name, port: s.set.Port, tvs: s.set.TVs}}
	viewers, err := s.opts.Store.ListViewers(context.Background())
	if err != nil {
		s.opts.Log.Warn("dlna: viewers", "err", err)
	}
	for _, v := range viewers {
		if v.DLNA {
			specs = append(specs, serverSpec{name: s.set.Name + " · " + v.Name, port: s.set.Port + v.Slot, tvs: v.TVs, viewer: v})
		}
	}
	return specs
}

// ViewersChanged applies changed viewers. Servers restart only when one is
// added, removed or renamed (TVs then see a different list); changes to
// titles or TVs apply to the running servers.
func (s *Server) ViewersChanged() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.set.Enabled || s.status.Servers == nil {
		return // off, or never started
	}
	specs := s.specs()
	same := len(specs) == len(s.status.Servers)
	for i := 0; same && i < len(specs); i++ {
		have := s.status.Servers[i]
		same = have.Name == specs[i].name && have.Port == specs[i].port
	}
	if !same {
		s.restart()
		return
	}
	s.refresh()
}

// serverSpec describes one DLNA server to start.
type serverSpec struct {
	name   string
	port   int
	tvs    []string
	viewer *store.Viewer
}

// interfaces are the networks to announce on. Caller holds s.mu.
func (s *Server) interfaces() ([]net.Interface, error) {
	var ifcs []net.Interface
	for _, ifc := range candidates(s.opts.ExcludeInterface) {
		if len(s.set.Interfaces) == 0 && !virtual(ifc.Name) || slices.Contains(s.set.Interfaces, ifc.Name) {
			ifcs = append(ifcs, ifc)
		}
	}
	if len(ifcs) == 0 {
		if len(s.set.Interfaces) > 0 {
			return nil, fmt.Errorf("none of the chosen networks (%s) is available", strings.Join(s.set.Interfaces, ", "))
		}
		return nil, errors.New("no local network to announce on")
	}
	return ifcs, nil
}

// start runs one DLNA server. Caller holds s.mu.
func (s *Server) start(spec serverSpec, ifcs []net.Interface) (*running, error) {
	if spec.port > 65535 {
		return nil, fmt.Errorf("port %d is out of range; lower the DLNA port", spec.port)
	}
	root, err := os.OpenRoot(s.opts.LibraryDir)
	if err != nil {
		return nil, fmt.Errorf("open library: %w", err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", spec.port))
	if err != nil {
		root.Close()
		return nil, fmt.Errorf("port %d is in use; pick another in the DLNA settings", spec.port)
	}
	pipe := newPipeListener(ln.Addr())
	d := &dms.Server{
		HTTPConn:         pipe,
		FriendlyName:     spec.name,
		Interfaces:       ifcs,
		RootObjectPath:   s.opts.LibraryDir,
		Logger:           slog.New(minLevel{s.opts.Log.With("component", "dlna").Handler(), slog.LevelWarn}),
		NoTranscode:      !s.set.Transcode || !s.status.FFmpeg,
		NoProbe:          !s.status.FFprobe,
		IgnoreHidden:     true,
		IgnoreUnreadable: true,
		NotifyInterval:   30 * time.Second,
		Icons:            deviceIcons(),
		AllowedIpNets:    []*net.IPNet{{IP: net.IPv4(127, 0, 0, 0), Mask: net.CIDRMask(8, 32)}},
	}
	if err := d.Init(); err != nil {
		ln.Close()
		root.Close()
		return nil, fmt.Errorf("start dlna: %w", err)
	}
	r := &running{set: s.set, dms: d, pipe: pipe, root: root, thumbs: newThumbs(s.ffmpeg), done: make(chan struct{}),
		viewer: spec.viewer, tvs: spec.tvs}
	r.front = &http.Server{Handler: s.front(r), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := d.Run(); err != nil {
			s.opts.Log.Warn("dlna: stopped", "err", err)
		}
	}()
	go func() {
		defer close(r.done)
		if err := r.front.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.opts.Log.Warn("dlna: http stopped", "err", err)
		}
	}()
	s.opts.Log.Info("dlna: serving library", "name", spec.name, "port", spec.port, "interfaces", len(ifcs))
	return r, nil
}

// stop shuts the servers down. Caller holds s.mu.
func (s *Server) stop() {
	runs := s.runs
	s.runs = nil
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	for _, r := range runs {
		_ = r.front.Shutdown(ctx)
		<-r.done
		_ = r.dms.Close() // closes the pipe listener and stops SSDP (sends byebye)
		r.root.Close()
	}
}

// front answers local devices, serves subtitles, and passes the rest to dms.
func (s *Server) front(r *running) http.Handler {
	r.transport = &http.Transport{
		DialContext:        r.pipe.dial,
		DisableCompression: true,
		MaxIdleConns:       16,
		IdleConnTimeout:    time.Minute,
	}
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme = "http"
			pr.Out.URL.Host = "dms"
			pr.Out.Host = pr.In.Host // dms builds item URLs from it
		},
		Transport:     r.transport,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if !errors.Is(err, context.Canceled) {
				s.opts.Log.Debug("dlna: proxy", "err", err)
			}
			w.WriteHeader(http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !localClient(req.RemoteAddr) {
			http.Error(w, "only devices on the local network may use this server", http.StatusForbidden)
			return
		}
		ip := clientIP(req.RemoteAddr)
		if s.tvs != nil {
			s.tvs.seen(ip, req.UserAgent())
		}
		if !s.tvAllowed(req.Context(), r, ip) {
			http.Error(w, "this server isn't shown on this TV", http.StatusForbidden)
			return
		}
		// A viewer limited to some titles can't open the others' files.
		if p := req.URL.Query().Get("path"); p != "" && (req.URL.Path == "/res" || req.URL.Path == "/subtitle" || req.URL.Path == "/icon") {
			if !s.visibility(req.Context(), r).allows(cleanRel(p)) {
				http.NotFound(w, req)
				return
			}
		}
		switch req.URL.Path {
		case controlPath:
			if r.set.TrackProgress && s.serveProgressAction(w, req, r) {
				return
			}
			if s.serveBrowse(w, req, r) {
				return
			}
		case "/subtitle":
			s.serveSubtitle(w, req, r.root)
			return
		case "/icon":
			r.thumbs.serve(w, req, r.root, s.opts.LibraryDir)
			return
		case "/res":
			if req.Header.Get("getCaptionInfo.sec") != "" {
				if _, ok := s.findSubtitle(r.root, req.URL.Query().Get("path")); ok {
					u := url.URL{Scheme: "http", Host: req.Host, Path: "/subtitle", RawQuery: url.Values{"path": {req.URL.Query().Get("path")}}.Encode()}
					w.Header().Set("CaptionInfo.sec", u.String())
				}
			}
		}
		proxy.ServeHTTP(w, req)
	})
}

func (s *Server) serveSubtitle(w http.ResponseWriter, r *http.Request, root *os.Root) {
	p, ok := s.findSubtitle(root, r.URL.Query().Get("path"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	f, err := root.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/srt; charset=utf-8")
	http.ServeContent(w, r, path.Base(p), fi.ModTime(), f)
}

// findSubtitle returns the library-relative subtitle for a video path:
// preferred languages first, then a plain .srt, then any other language.
func (s *Server) findSubtitle(root *os.Root, video string) (string, bool) {
	rel := cleanRel(video)
	if rel == "" {
		return "", false
	}
	base := strings.TrimSuffix(rel, path.Ext(rel))
	var tries []string
	for _, l := range s.opts.SubtitleLangs() {
		tries = append(tries, base+"."+l+".srt")
	}
	tries = append(tries, base+".srt")
	for _, p := range tries {
		if fi, err := root.Stat(p); err == nil && !fi.IsDir() {
			return p, true
		}
	}
	dir, name := path.Split(base)
	entries, err := fs.ReadDir(root.FS(), cleanDir(dir))
	if err != nil {
		return "", false
	}
	var others []string
	for _, e := range entries {
		n := e.Name()
		if !e.IsDir() && strings.HasPrefix(n, name+".") && strings.EqualFold(path.Ext(n), ".srt") {
			others = append(others, dir+n)
		}
	}
	if len(others) == 0 {
		return "", false
	}
	sort.Strings(others)
	return others[0], true
}

// cleanRel turns a request path into a clean library-relative path ("" if empty).
func cleanRel(p string) string {
	return strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(p, "\\", "/")), "/")
}

func cleanDir(dir string) string {
	dir = strings.TrimSuffix(dir, "/")
	if dir == "" {
		return "."
	}
	return dir
}

var localNets = func() []*net.IPNet {
	var out []*net.IPNet
	for _, c := range []string{"127.0.0.0/8", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16", "100.64.0.0/10", "::1/128", "fc00::/7", "fe80::/10"} {
		_, n, _ := net.ParseCIDR(c)
		out = append(out, n)
	}
	return out
}()

// clientIP is the IP part of a remote address ("ip:port").
func clientIP(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// localClient reports whether addr ("ip:port") is on a private network.
func localClient(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	for _, n := range localNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// pipeListener hands in-memory connections from the front to dms, while
// reporting the real TCP address (dms announces its port from it).
type pipeListener struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener(addr net.Addr) *pipeListener {
	return &pipeListener{addr: addr, conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return l.addr }

func (l *pipeListener) dial(ctx context.Context, _, _ string) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- loopbackConn{server}:
		return client, nil
	case <-l.closed:
		client.Close()
		server.Close()
		return nil, net.ErrClosed
	case <-ctx.Done():
		client.Close()
		server.Close()
		return nil, ctx.Err()
	}
}

// loopbackConn reports a loopback peer. dms checks the client address
// against AllowedIpNets; the front has already checked the real one.
type loopbackConn struct{ net.Conn }

var loopbackAddr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)}

func (loopbackConn) RemoteAddr() net.Addr { return loopbackAddr }

// minLevel drops records below a level (dms logs a lot at Info).
type minLevel struct {
	slog.Handler
	min slog.Level
}

func (h minLevel) Enabled(ctx context.Context, l slog.Level) bool {
	return l >= h.min && h.Handler.Enabled(ctx, l)
}
func (h minLevel) WithAttrs(a []slog.Attr) slog.Handler {
	return minLevel{h.Handler.WithAttrs(a), h.min}
}
func (h minLevel) WithGroup(g string) slog.Handler { return minLevel{h.Handler.WithGroup(g), h.min} }
