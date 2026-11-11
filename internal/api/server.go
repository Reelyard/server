// Package api serves Harbor's REST API (/api/v1) and the embedded web UI.
package api

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/inluxc/harbor/internal/auth"
	"github.com/inluxc/harbor/internal/config"
	"github.com/inluxc/harbor/internal/engine"
	"github.com/inluxc/harbor/internal/events"
	"github.com/inluxc/harbor/internal/importer"
	"github.com/inluxc/harbor/internal/metadata"
	"github.com/inluxc/harbor/internal/search"
	"github.com/inluxc/harbor/internal/store"
	"github.com/inluxc/harbor/internal/suppliers"
	"github.com/inluxc/harbor/internal/tracker"
	"github.com/inluxc/harbor/internal/version"
)

// Server holds the API's dependencies.
type Server struct {
	Store     *store.Store
	Auth      *auth.Service
	Suppliers *suppliers.Service
	Meta      *metadata.Service
	Search    *search.Service
	Engine    *engine.Engine
	Importer  *importer.Importer
	Tracker   *tracker.Tracker
	Bus       *events.Bus
	Config    *config.Config
	UI        fs.FS // built web app (index.html + assets); may be empty
	Log       *slog.Logger
	started   time.Time

	closeOnce sync.Once
	closing   chan struct{}
}

// Shutdown ends long-lived responses (the live event stream) so a graceful
// HTTP shutdown doesn't wait for them. Register it with
// http.Server.RegisterOnShutdown; call it only after Handler.
func (s *Server) Shutdown() {
	s.closeOnce.Do(func() { close(s.closing) })
}

// Handler returns the full HTTP handler: API, UI, and middleware.
func (s *Server) Handler() http.Handler {
	if s.Log == nil {
		s.Log = slog.Default()
	}
	s.started = time.Now()
	s.closing = make(chan struct{})

	mux := http.NewServeMux()
	// Public.
	mux.HandleFunc("GET /api/v1/health", s.health)
	mux.HandleFunc("GET /api/v1/setup", s.setupStatus)
	mux.HandleFunc("POST /api/v1/setup", s.setup)
	mux.HandleFunc("POST /api/v1/auth/login", s.login)
	// Signed in.
	mux.Handle("POST /api/v1/auth/logout", s.authed(s.logout))
	mux.Handle("GET /api/v1/auth/me", s.authed(s.me))
	mux.Handle("POST /api/v1/auth/password", s.authed(s.changePassword))
	mux.Handle("GET /api/v1/auth/keys", s.authed(s.listKeys))
	mux.Handle("POST /api/v1/auth/keys", s.authed(s.createKey))
	mux.Handle("DELETE /api/v1/auth/keys/{id}", s.authed(s.deleteKey))
	mux.Handle("GET /api/v1/system", s.authed(s.system))
	s.routesMedia(mux)
	s.routesDownloads(mux)
	s.routesLibrary(mux)
	s.routesTracking(mux)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such API endpoint")
	})
	// Everything else is the web app.
	mux.Handle("/", uiHandler(s.UI))

	// Blocks cross-site POST/PUT/DELETE from browsers (CSRF); requests
	// without browser fetch metadata (curl, scripts) pass.
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusForbidden, "cross-origin request blocked")
	}))
	return s.recoverer(s.logRequests(securityHeaders(cop.Handler(mux))))
}

// ---- middleware ----

func (s *Server) authed(h func(http.ResponseWriter, *http.Request)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, err := s.Auth.Authenticate(r)
		if err != nil {
			if !errors.Is(err, store.ErrNotFound) {
				s.Log.Error("authenticate", "err", err)
			}
			writeError(w, http.StatusUnauthorized, "sign in required")
			return
		}
		h(w, r.WithContext(auth.WithUser(r.Context(), u)))
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int)        { w.status = code; w.ResponseWriter.WriteHeader(code) }
func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (s *Server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/api/") {
			s.Log.Debug("http", "method", r.Method, "path", r.URL.Path, "status", sw.status, "dur", time.Since(start).Round(time.Microsecond))
		}
	})
}

func (s *Server) recoverer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				s.Log.Error("panic", "path", r.URL.Path, "err", v, "stack", string(debug.Stack()))
				writeError(w, http.StatusInternalServerError, "internal error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

type errorBody struct {
	Error string `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorBody{Error: msg})
}

func (s *Server) internal(w http.ResponseWriter, what string, err error) {
	s.Log.Error(what, "err", err)
	writeError(w, http.StatusInternalServerError, "something went wrong; see the server log")
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid JSON body: trailing data")
		return false
	}
	return true
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- handlers ----

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": version.Version})
}

func (s *Server) setupStatus(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.CountUsers(r.Context())
	if err != nil {
		s.internal(w, "count users", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"needed": n == 0})
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type userBody struct {
	User *store.User `json:"user"`
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	c.Username = strings.TrimSpace(c.Username)
	if err := auth.ValidateUsername(c.Username); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	u, err := s.Store.CreateFirstUser(r.Context(), c.Username, hash)
	if errors.Is(err, store.ErrUsersExist) {
		writeError(w, http.StatusConflict, "setup is already done; sign in instead")
		return
	}
	if err != nil {
		s.internal(w, "create first user", err)
		return
	}
	tok, err := s.Auth.NewSession(r.Context(), u.ID, r.UserAgent(), remoteIP(r))
	if err != nil {
		s.internal(w, "create session", err)
		return
	}
	s.Log.Info("admin account created", "username", u.Username)
	http.SetCookie(w, auth.SessionCookie(tok, isHTTPS(r)))
	writeJSON(w, http.StatusCreated, userBody{u})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if !decode(w, r, &c) {
		return
	}
	tok, u, err := s.Auth.Login(r.Context(), strings.TrimSpace(c.Username), c.Password, r.UserAgent(), remoteIP(r))
	switch {
	case errors.Is(err, auth.ErrBadCredentials):
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	case errors.Is(err, auth.ErrTooManyTries):
		w.Header().Set("Retry-After", "60")
		writeError(w, http.StatusTooManyRequests, err.Error())
		return
	case err != nil:
		s.internal(w, "login", err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(tok, isHTTPS(r)))
	writeJSON(w, http.StatusOK, userBody{u})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(auth.CookieName); err == nil && c.Value != "" {
		if err := s.Auth.Logout(r.Context(), c.Value); err != nil {
			s.internal(w, "logout", err)
			return
		}
	}
	http.SetCookie(w, auth.ClearCookie(isHTTPS(r)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, userBody{auth.UserFrom(r.Context())})
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if !decode(w, r, &body) {
		return
	}
	u := auth.UserFrom(r.Context())
	// Re-check the current password through Login so it is rate limited.
	if _, _, err := s.Auth.Login(r.Context(), u.Username, body.Current, r.UserAgent(), remoteIP(r)); err != nil {
		if errors.Is(err, auth.ErrBadCredentials) {
			writeError(w, http.StatusForbidden, "current password is wrong")
			return
		}
		if errors.Is(err, auth.ErrTooManyTries) {
			writeError(w, http.StatusTooManyRequests, err.Error())
			return
		}
		s.internal(w, "check password", err)
		return
	}
	hash, err := auth.HashPassword(body.New)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.SetPassword(r.Context(), u.ID, hash); err != nil { // also ends all sessions
		s.internal(w, "set password", err)
		return
	}
	tok, err := s.Auth.NewSession(r.Context(), u.ID, r.UserAgent(), remoteIP(r))
	if err != nil {
		s.internal(w, "create session", err)
		return
	}
	http.SetCookie(w, auth.SessionCookie(tok, isHTTPS(r)))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := s.Store.ListAPIKeys(r.Context(), auth.UserFrom(r.Context()).ID)
	if err != nil {
		s.internal(w, "list keys", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": keys})
}

func (s *Server) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decode(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || len(body.Name) > 64 {
		writeError(w, http.StatusBadRequest, "give the key a name of 1–64 characters")
		return
	}
	raw, k, err := s.Auth.NewAPIKey(r.Context(), auth.UserFrom(r.Context()).ID, body.Name)
	if err != nil {
		s.internal(w, "create key", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"key": k, "secret": raw})
}

func (s *Server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "bad key id")
		return
	}
	err = s.Store.DeleteAPIKey(r.Context(), auth.UserFrom(r.Context()).ID, id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "no such key")
		return
	}
	if err != nil {
		s.internal(w, "delete key", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	schema, err := s.Store.SchemaVersion(r.Context())
	if err != nil {
		s.internal(w, "schema version", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version":     version.Version,
		"commit":      version.Commit,
		"buildDate":   version.Date,
		"goVersion":   runtime.Version(),
		"os":          runtime.GOOS,
		"arch":        runtime.GOARCH,
		"dataDir":     s.Config.DataDir,
		"downloadDir": s.Config.DownloadDir,
		"libraryDir":  s.Config.LibraryDir,
		"configFile":  s.Config.Path(),
		"schema":      schema,
		"uptimeSec":   int(time.Since(s.started).Seconds()),
	})
}
